package userbot

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/sirupsen/logrus"

	"bot/internal/config"
)

type fakeMongoChecker struct {
	err error
}

func (f fakeMongoChecker) Ping(context.Context) error {
	return f.err
}

type fakeStatsProvider struct {
	users    int64
	groups   int64
	userErr  error
	groupErr error
}

func (f fakeStatsProvider) CountUsers(context.Context) (int64, error) {
	return f.users, f.userErr
}

func (f fakeStatsProvider) CountGroups(context.Context) (int64, error) {
	return f.groups, f.groupErr
}

type fakeTelegramUserGetter struct {
	users []tg.UserClass
	ids   []tg.InputUserClass
	err   error
}

func (f *fakeTelegramUserGetter) UsersGetUsers(_ context.Context, id []tg.InputUserClass) ([]tg.UserClass, error) {
	f.ids = id
	return f.users, f.err
}

type fakeTelegramMessageForwarder struct {
	requests []*tg.MessagesForwardMessagesRequest
	err      error
}

func (f *fakeTelegramMessageForwarder) MessagesForwardMessages(_ context.Context, request *tg.MessagesForwardMessagesRequest) (tg.UpdatesClass, error) {
	f.requests = append(f.requests, request)
	return &tg.Updates{}, f.err
}

type fakeTelegramMessageSender struct {
	requests []*tg.MessagesSendMessageRequest
	err      error
}

func (f *fakeTelegramMessageSender) MessagesSendMessage(_ context.Context, request *tg.MessagesSendMessageRequest) (tg.UpdatesClass, error) {
	f.requests = append(f.requests, request)
	return &tg.Updates{}, f.err
}

type fakeTelegramMessageGetter struct {
	messages        tg.MessagesMessagesClass
	messagesErr     error
	messagesIDs     [][]tg.InputMessageClass
	channelMessages tg.MessagesMessagesClass
	channelErr      error
	channelRequests []*tg.ChannelsGetMessagesRequest
}

func (f *fakeTelegramMessageGetter) MessagesGetMessages(_ context.Context, id []tg.InputMessageClass) (tg.MessagesMessagesClass, error) {
	f.messagesIDs = append(f.messagesIDs, id)
	return f.messages, f.messagesErr
}

func (f *fakeTelegramMessageGetter) ChannelsGetMessages(_ context.Context, request *tg.ChannelsGetMessagesRequest) (tg.MessagesMessagesClass, error) {
	f.channelRequests = append(f.channelRequests, request)
	return f.channelMessages, f.channelErr
}

type fakeTelegramAPI struct {
	fakeTelegramUserGetter
	fakeTelegramMessageForwarder
	fakeTelegramMessageSender
	fakeTelegramMessageGetter
}

func TestOwnerCommandRecognition(t *testing.T) {
	tests := []struct {
		name string
		meta messageMeta
		want bool
	}{
		{
			name: "owner outgoing dot command",
			meta: messageMeta{userID: 42, out: true, text: ".ping"},
			want: true,
		},
		{
			name: "incoming command ignored",
			meta: messageMeta{userID: 42, out: false, text: ".ping"},
		},
		{
			name: "non owner ignored",
			meta: messageMeta{userID: 41, out: true, text: ".ping"},
		},
		{
			name: "plain message ignored",
			meta: messageMeta{userID: 42, out: true, text: "ping"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isOwnerCommand(tt.meta, 42); got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestCommandName(t *testing.T) {
	if got := commandName(".PING now"); got != "ping" {
		t.Fatalf("expected ping, got %q", got)
	}
	if got := commandName("ping"); got != "" {
		t.Fatalf("expected empty command, got %q", got)
	}
}

func TestCommandResponses(t *testing.T) {
	client := &Client{
		cfg: config.Config{
			AppEnv: EnvForTest,
		},
		logger:        logrus.NewEntry(logrus.New()),
		mongoChecker:  fakeMongoChecker{},
		processStart:  time.Now().Add(-2 * time.Minute),
		statsProvider: fakeStatsProvider{users: 3, groups: 4},
	}

	ping, handled := client.commandResponse(context.Background(), "ping")
	if !handled {
		t.Fatalf("expected ping to be handled")
	}
	if !strings.Contains(ping, "pong") || !strings.Contains(ping, "mongo: ok") {
		t.Fatalf("unexpected ping response: %q", ping)
	}

	status, handled := client.commandResponse(context.Background(), "status")
	if !handled {
		t.Fatalf("expected status to be handled")
	}
	for _, part := range []string{"userbot_status: running", "registered_users: 3", "known_chats: 4"} {
		if !strings.Contains(status, part) {
			t.Fatalf("expected status to contain %q, got %q", part, status)
		}
	}
}

func TestCommandResponsesSurfaceDependencyErrors(t *testing.T) {
	client := &Client{
		cfg:          config.Config{AppEnv: EnvForTest},
		logger:       logrus.NewEntry(logrus.New()),
		mongoChecker: fakeMongoChecker{err: errors.New("mongo down")},
		processStart: time.Now(),
		statsProvider: fakeStatsProvider{
			userErr:  errors.New("users down"),
			groupErr: errors.New("groups down"),
		},
	}

	status := client.statusResponse(context.Background())
	for _, part := range []string{"mongo: error", "registered_users: error", "known_chats: error"} {
		if !strings.Contains(status, part) {
			t.Fatalf("expected status to contain %q, got %q", part, status)
		}
	}
}

func TestShouldMirrorBotMessage(t *testing.T) {
	client := &Client{cfg: config.Config{MirrorBotMessages: true}}

	tests := []struct {
		name string
		meta messageMeta
		want bool
	}{
		{
			name: "incoming bot order text in group",
			meta: messageMeta{chatType: "group", senderIsBot: true, hasOrderNumber: true, text: "M1776217307123"},
			want: true,
		},
		{
			name: "incoming bot order media in group",
			meta: messageMeta{chatType: "group", senderIsBot: true, hasOrderNumber: true, hasMedia: true},
			want: true,
		},
		{
			name: "feature disabled",
			meta: messageMeta{chatType: "group", senderIsBot: true, hasOrderNumber: true, text: "M1776217307123"},
			want: false,
		},
		{
			name: "bot message without order ignored",
			meta: messageMeta{chatType: "group", senderIsBot: true, text: "hello"},
			want: false,
		},
		{
			name: "human message ignored",
			meta: messageMeta{chatType: "group", hasOrderNumber: true, text: "M1776217307123"},
			want: false,
		},
		{
			name: "outgoing bot-like message ignored",
			meta: messageMeta{chatType: "group", senderIsBot: true, hasOrderNumber: true, out: true, text: "M1776217307123"},
			want: false,
		},
		{
			name: "bot reply to userbot ignored",
			meta: messageMeta{chatType: "group", senderIsBot: true, hasOrderNumber: true, replyToUserbot: true, text: "M1776217307123"},
			want: false,
		},
		{
			name: "private bot message ignored",
			meta: messageMeta{chatType: "private", senderIsBot: true, hasOrderNumber: true, text: "M1776217307123"},
			want: false,
		},
		{
			name: "empty content ignored",
			meta: messageMeta{chatType: "group", senderIsBot: true},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.name == "feature disabled" {
				client = &Client{}
			} else {
				client = &Client{cfg: config.Config{MirrorBotMessages: true}}
			}
			if got := client.shouldMirrorBotMessage(tt.meta); got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestResolveReplyToUserbotDetectsOwnReplyTarget(t *testing.T) {
	client := &Client{cfg: config.Config{MirrorBotMessages: true, UserbotOwnerID: 42}}
	api := &fakeTelegramAPI{
		fakeTelegramMessageGetter: fakeTelegramMessageGetter{
			messages: &tg.MessagesMessages{
				Messages: []tg.MessageClass{
					messageFromUser(44, 42),
				},
			},
		},
	}
	msg := botOrderReplyMessage(55, 44, &tg.PeerChat{ChatID: 200})
	meta := client.extractMessageMeta(tg.Entities{
		Users: map[int64]*tg.User{
			100: {ID: 100, Bot: true},
		},
	}, msg)

	if err := client.resolveReplyToUserbot(context.Background(), api, tg.Entities{}, msg, &meta); err != nil {
		t.Fatalf("expected reply target lookup to succeed: %v", err)
	}
	if !meta.replyToUserbot {
		t.Fatalf("expected reply target to be detected as userbot-authored")
	}
	if len(api.messagesIDs) != 1 {
		t.Fatalf("expected one messages.getMessages lookup, got %d", len(api.messagesIDs))
	}
	inputID, ok := api.messagesIDs[0][0].(*tg.InputMessageID)
	if !ok {
		t.Fatalf("expected InputMessageID, got %T", api.messagesIDs[0][0])
	}
	if inputID.ID != 44 {
		t.Fatalf("expected reply target message id 44, got %d", inputID.ID)
	}
	if len(api.channelRequests) != 0 {
		t.Fatalf("expected no channel lookup, got %d", len(api.channelRequests))
	}
}

func TestResolveReplyToUserbotLeavesHumanReplyTargetMirrorable(t *testing.T) {
	client := &Client{cfg: config.Config{MirrorBotMessages: true, UserbotOwnerID: 42}}
	api := &fakeTelegramAPI{
		fakeTelegramMessageGetter: fakeTelegramMessageGetter{
			messages: &tg.MessagesMessages{
				Messages: []tg.MessageClass{
					messageFromUser(44, 43),
				},
			},
		},
	}
	msg := botOrderReplyMessage(55, 44, &tg.PeerChat{ChatID: 200})
	meta := client.extractMessageMeta(tg.Entities{
		Users: map[int64]*tg.User{
			100: {ID: 100, Bot: true},
		},
	}, msg)

	if err := client.resolveReplyToUserbot(context.Background(), api, tg.Entities{}, msg, &meta); err != nil {
		t.Fatalf("expected reply target lookup to succeed: %v", err)
	}
	if meta.replyToUserbot {
		t.Fatalf("expected human reply target not to be marked as userbot-authored")
	}
	if !client.shouldMirrorBotMessage(meta) {
		t.Fatalf("expected bot reply to normal user message to remain mirrorable")
	}
}

func TestResolveReplyToUserbotUsesChannelLookupForMegagroup(t *testing.T) {
	client := &Client{cfg: config.Config{MirrorBotMessages: true, UserbotOwnerID: 42}}
	api := &fakeTelegramAPI{
		fakeTelegramMessageGetter: fakeTelegramMessageGetter{
			channelMessages: &tg.MessagesMessages{
				Messages: []tg.MessageClass{
					messageFromUser(44, 42),
				},
			},
		},
	}
	peerID := &tg.PeerChannel{ChannelID: 200}
	msg := botOrderReplyMessage(55, 44, peerID)
	entities := tg.Entities{
		Users: map[int64]*tg.User{
			100: {ID: 100, Bot: true},
		},
		Channels: map[int64]*tg.Channel{
			200: {ID: 200, AccessHash: 123, Megagroup: true},
		},
	}
	meta := client.extractMessageMeta(entities, msg)

	if err := client.resolveReplyToUserbot(context.Background(), api, entities, msg, &meta); err != nil {
		t.Fatalf("expected reply target lookup to succeed: %v", err)
	}
	if !meta.replyToUserbot {
		t.Fatalf("expected channel reply target to be detected as userbot-authored")
	}
	if len(api.channelRequests) != 1 {
		t.Fatalf("expected one channels.getMessages lookup, got %d", len(api.channelRequests))
	}
	if _, ok := api.channelRequests[0].Channel.(*tg.InputChannel); !ok {
		t.Fatalf("expected InputChannel, got %T", api.channelRequests[0].Channel)
	}
	if len(api.messagesIDs) != 0 {
		t.Fatalf("expected no normal message lookup, got %d", len(api.messagesIDs))
	}
}

func messageFromUser(msgID int, userID int64) *tg.Message {
	msg := &tg.Message{ID: msgID}
	msg.SetFromID(&tg.PeerUser{UserID: userID})
	return msg
}

func botOrderReplyMessage(msgID int, replyToMsgID int, peerID tg.PeerClass) *tg.Message {
	replyHeader := &tg.MessageReplyHeader{}
	replyHeader.SetReplyToMsgID(replyToMsgID)
	msg := &tg.Message{
		ID:      msgID,
		PeerID:  peerID,
		Message: "M1776217307123",
	}
	msg.SetFromID(&tg.PeerUser{UserID: 100})
	msg.SetReplyTo(replyHeader)
	return msg
}

func TestHasOrderNumber(t *testing.T) {
	client := &Client{}

	tests := []struct {
		name string
		text string
		want bool
	}{
		{name: "long numeric order", text: "1776217307123", want: true},
		{name: "mixed order", text: "[M1776217307123] 永顺", want: true},
		{name: "dash order", text: "order A-123456789-Z ready", want: true},
		{name: "evm usdt address ignored", text: "0x52908400098527886E0F7030069857D2E4169EE7"},
		{name: "tron usdt address ignored", text: "TRX6p8hnP9DNmDZELkwZpGbC1zT3Y8v7Pc"},
		{name: "order with evm usdt address accepted", text: "order M1776217307123 pay 0x52908400098527886E0F7030069857D2E4169EE7", want: true},
		{name: "order with tron usdt address accepted", text: "order M1776217307123 pay TRX6p8hnP9DNmDZELkwZpGbC1zT3Y8v7Pc", want: true},
		{name: "twelve character order ignored", text: "A12345678901"},
		{name: "short number ignored", text: "12345"},
		{name: "price ignored", text: "6.83"},
		{name: "date ignored", text: "2026-04-22"},
		{name: "plain long word ignored", text: "configuration"},
		{name: "empty ignored", text: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := client.hasOrderNumber(tt.text); got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestIsHistoricalMessage(t *testing.T) {
	processStart := time.Unix(1_700_000_000, 0)
	client := &Client{processStart: processStart}

	tests := []struct {
		name string
		msg  *tg.Message
		want bool
	}{
		{
			name: "old message ignored",
			msg:  &tg.Message{Date: int(processStart.Add(-time.Minute).Unix())},
			want: true,
		},
		{
			name: "message inside startup grace allowed",
			msg:  &tg.Message{Date: int(processStart.Add(-5 * time.Second).Unix())},
		},
		{
			name: "new message allowed",
			msg:  &tg.Message{Date: int(processStart.Add(time.Second).Unix())},
		},
		{
			name: "missing telegram date allowed",
			msg:  &tg.Message{},
		},
		{
			name: "nil message allowed",
			msg:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := client.isHistoricalMessage(tt.msg); got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestExtractMessageMetaDetectsBotSender(t *testing.T) {
	client := &Client{}
	msg := &tg.Message{
		PeerID:  &tg.PeerChat{ChatID: 200},
		Message: "M1776217307123",
	}
	msg.SetFromID(&tg.PeerUser{UserID: 100})

	meta := client.extractMessageMeta(tg.Entities{
		Users: map[int64]*tg.User{
			100: {ID: 100, Bot: true},
		},
	}, msg)

	if !meta.senderIsBot {
		t.Fatalf("expected sender to be detected as bot")
	}
	if meta.chatType != "group" || meta.chatID != -200 || meta.userID != 100 {
		t.Fatalf("unexpected metadata: %+v", meta)
	}
}

func TestExtractMessageMetaUsesCanonicalGroupChatIDs(t *testing.T) {
	client := &Client{}

	tests := []struct {
		name     string
		peerID   tg.PeerClass
		entities tg.Entities
		wantID   int64
		wantType string
	}{
		{
			name:     "basic group",
			peerID:   &tg.PeerChat{ChatID: 200},
			wantID:   -200,
			wantType: "group",
		},
		{
			name:   "megagroup channel",
			peerID: &tg.PeerChannel{ChannelID: 200},
			entities: tg.Entities{
				Channels: map[int64]*tg.Channel{
					200: {ID: 200, Megagroup: true},
				},
			},
			wantID:   -1000000000200,
			wantType: "group",
		},
		{
			name:   "broadcast channel",
			peerID: &tg.PeerChannel{ChannelID: 300},
			entities: tg.Entities{
				Channels: map[int64]*tg.Channel{
					300: {ID: 300},
				},
			},
			wantID:   -1000000000300,
			wantType: "channel",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := &tg.Message{PeerID: tt.peerID}
			meta := client.extractMessageMeta(tt.entities, msg)

			if meta.chatID != tt.wantID {
				t.Fatalf("expected chat id %d, got %d", tt.wantID, meta.chatID)
			}
			if meta.chatType != tt.wantType {
				t.Fatalf("expected chat type %s, got %s", tt.wantType, meta.chatType)
			}
		})
	}
}

func TestExtractMessageMetaDetectsMedia(t *testing.T) {
	client := &Client{}
	msg := &tg.Message{
		PeerID: &tg.PeerChat{ChatID: 200},
		Media:  &tg.MessageMediaPhoto{},
	}
	msg.SetFromID(&tg.PeerUser{UserID: 100})

	meta := client.extractMessageMeta(tg.Entities{}, msg)

	if !meta.hasMedia {
		t.Fatalf("expected media to be detected")
	}
	if !meta.hasMirrorableContent() {
		t.Fatalf("expected media-only message to be mirrorable")
	}
}

func TestExtractMessageMetaDetectsReplyTarget(t *testing.T) {
	client := &Client{}
	replyHeader := &tg.MessageReplyHeader{}
	replyHeader.SetReplyToMsgID(44)
	replyHeader.SetReplyToTopID(10)
	msg := &tg.Message{
		PeerID:  &tg.PeerChat{ChatID: 200},
		Message: "bot says hi",
	}
	msg.SetFromID(&tg.PeerUser{UserID: 100})
	msg.SetReplyTo(replyHeader)

	meta := client.extractMessageMeta(tg.Entities{}, msg)

	if meta.replyToMsgID != 44 {
		t.Fatalf("expected reply target 44, got %d", meta.replyToMsgID)
	}
	if meta.replyToTopID != 10 {
		t.Fatalf("expected reply top id 10, got %d", meta.replyToTopID)
	}
}

func TestResolveMessageSenderBotUsesInputUserFromMessage(t *testing.T) {
	client := &Client{cfg: config.Config{MirrorBotMessages: true}}
	getter := &fakeTelegramUserGetter{
		users: []tg.UserClass{
			&tg.User{ID: 100, Bot: true},
		},
	}
	msg := &tg.Message{
		ID:      55,
		PeerID:  &tg.PeerChat{ChatID: 200},
		Message: "M1776217307123",
	}
	msg.SetFromID(&tg.PeerUser{UserID: 100})
	meta := client.extractMessageMeta(tg.Entities{}, msg)

	if err := client.resolveMessageSenderBot(context.Background(), getter, tg.Entities{}, msg, &meta); err != nil {
		t.Fatalf("expected sender lookup to succeed: %v", err)
	}

	if !meta.senderIsBot {
		t.Fatalf("expected resolved sender to be detected as bot")
	}
	if len(getter.ids) != 1 {
		t.Fatalf("expected one user lookup, got %d", len(getter.ids))
	}

	input, ok := getter.ids[0].(*tg.InputUserFromMessage)
	if !ok {
		t.Fatalf("expected InputUserFromMessage, got %T", getter.ids[0])
	}
	peer, ok := input.Peer.(*tg.InputPeerChat)
	if !ok {
		t.Fatalf("expected InputPeerChat, got %T", input.Peer)
	}
	if peer.ChatID != 200 || input.MsgID != 55 || input.UserID != 100 {
		t.Fatalf("unexpected lookup input: %+v", input)
	}
}

func TestResolveMessageSenderBotUsesCache(t *testing.T) {
	client := &Client{cfg: config.Config{MirrorBotMessages: true}}
	client.cacheSenderBot(100, true)
	getter := &fakeTelegramUserGetter{}
	meta := messageMeta{chatType: "group", userID: 100, hasOrderNumber: true, text: "M1776217307123"}

	if err := client.resolveMessageSenderBot(context.Background(), getter, tg.Entities{}, &tg.Message{ID: 55}, &meta); err != nil {
		t.Fatalf("expected cached sender lookup to succeed: %v", err)
	}

	if !meta.senderIsBot {
		t.Fatalf("expected cached bot status")
	}
	if len(getter.ids) != 0 {
		t.Fatalf("expected no telegram lookup when cache exists, got %d", len(getter.ids))
	}
}

func TestMirrorMessageToSamePeerSendsTextReply(t *testing.T) {
	api := &fakeTelegramAPI{}
	replyHeader := &tg.MessageReplyHeader{}
	replyHeader.SetReplyToMsgID(44)
	msg := &tg.Message{
		ID:      55,
		PeerID:  &tg.PeerChat{ChatID: 200},
		Message: "bot says hi",
	}
	msg.SetReplyTo(replyHeader)

	if err := mirrorMessageToSamePeer(context.Background(), api, tg.Entities{}, msg); err != nil {
		t.Fatalf("expected mirror to succeed: %v", err)
	}
	if len(api.fakeTelegramMessageSender.requests) != 1 {
		t.Fatalf("expected one send request, got %d", len(api.fakeTelegramMessageSender.requests))
	}
	if len(api.fakeTelegramMessageForwarder.requests) != 0 {
		t.Fatalf("expected no forward request, got %d", len(api.fakeTelegramMessageForwarder.requests))
	}

	request := api.fakeTelegramMessageSender.requests[0]
	if request.Message != "bot says hi" {
		t.Fatalf("expected mirrored text, got %q", request.Message)
	}
	replyClass, ok := request.GetReplyTo()
	if !ok {
		t.Fatalf("expected send request to include reply target")
	}
	replyTo, ok := replyClass.(*tg.InputReplyToMessage)
	if !ok {
		t.Fatalf("expected InputReplyToMessage, got %T", replyClass)
	}
	if replyTo.ReplyToMsgID != 44 {
		t.Fatalf("expected reply target 44, got %d", replyTo.ReplyToMsgID)
	}
}

func TestMirrorMessageToSamePeerForwardsMedia(t *testing.T) {
	api := &fakeTelegramAPI{}
	msg := &tg.Message{
		ID:     55,
		PeerID: &tg.PeerChat{ChatID: 200},
		Media:  &tg.MessageMediaPhoto{},
	}

	if err := mirrorMessageToSamePeer(context.Background(), api, tg.Entities{}, msg); err != nil {
		t.Fatalf("expected mirror to succeed: %v", err)
	}
	if len(api.fakeTelegramMessageForwarder.requests) != 1 {
		t.Fatalf("expected one forward request, got %d", len(api.fakeTelegramMessageForwarder.requests))
	}
	if len(api.fakeTelegramMessageSender.requests) != 0 {
		t.Fatalf("expected no send request, got %d", len(api.fakeTelegramMessageSender.requests))
	}
}

func TestForwardMessageToSamePeerDropsAuthor(t *testing.T) {
	forwarder := &fakeTelegramMessageForwarder{}
	msg := &tg.Message{
		ID:     55,
		PeerID: &tg.PeerChat{ChatID: 200},
	}

	if err := forwardMessageToSamePeer(context.Background(), forwarder, tg.Entities{}, msg); err != nil {
		t.Fatalf("expected forward to succeed: %v", err)
	}
	if len(forwarder.requests) != 1 {
		t.Fatalf("expected one forward request, got %d", len(forwarder.requests))
	}

	request := forwarder.requests[0]
	fromPeer, ok := request.FromPeer.(*tg.InputPeerChat)
	if !ok {
		t.Fatalf("expected FromPeer InputPeerChat, got %T", request.FromPeer)
	}
	toPeer, ok := request.ToPeer.(*tg.InputPeerChat)
	if !ok {
		t.Fatalf("expected ToPeer InputPeerChat, got %T", request.ToPeer)
	}
	if fromPeer.ChatID != 200 || toPeer.ChatID != 200 {
		t.Fatalf("expected same chat peer, got from=%+v to=%+v", fromPeer, toPeer)
	}
	if len(request.ID) != 1 || request.ID[0] != 55 {
		t.Fatalf("expected message id 55, got %+v", request.ID)
	}
	if len(request.RandomID) != 1 || request.RandomID[0] == 0 {
		t.Fatalf("expected non-zero random id, got %+v", request.RandomID)
	}
	if !request.GetDropAuthor() {
		t.Fatalf("expected forwarded message to drop original author")
	}
	if request.GetDropMediaCaptions() {
		t.Fatalf("expected media captions to be preserved")
	}
}

func TestForwardMessageToSamePeerPreservesReply(t *testing.T) {
	forwarder := &fakeTelegramMessageForwarder{}
	replyHeader := &tg.MessageReplyHeader{}
	replyHeader.SetReplyToMsgID(44)
	msg := &tg.Message{
		ID:     55,
		PeerID: &tg.PeerChat{ChatID: 200},
	}
	msg.SetReplyTo(replyHeader)

	if err := forwardMessageToSamePeer(context.Background(), forwarder, tg.Entities{}, msg); err != nil {
		t.Fatalf("expected forward to succeed: %v", err)
	}

	request := forwarder.requests[0]
	replyClass, ok := request.GetReplyTo()
	if !ok {
		t.Fatalf("expected forward request to include reply target")
	}
	replyTo, ok := replyClass.(*tg.InputReplyToMessage)
	if !ok {
		t.Fatalf("expected InputReplyToMessage, got %T", replyClass)
	}
	if replyTo.ReplyToMsgID != 44 {
		t.Fatalf("expected reply target 44, got %d", replyTo.ReplyToMsgID)
	}
}

func TestInputReplyToFromMessagePreservesTopic(t *testing.T) {
	replyHeader := &tg.MessageReplyHeader{}
	replyHeader.SetReplyToMsgID(44)
	replyHeader.SetReplyToTopID(10)
	msg := &tg.Message{}
	msg.SetReplyTo(replyHeader)

	replyClass := inputReplyToFromMessage(msg)
	replyTo, ok := replyClass.(*tg.InputReplyToMessage)
	if !ok {
		t.Fatalf("expected InputReplyToMessage, got %T", replyClass)
	}
	if replyTo.ReplyToMsgID != 44 {
		t.Fatalf("expected reply target 44, got %d", replyTo.ReplyToMsgID)
	}
	if topMsgID, ok := replyTo.GetTopMsgID(); !ok || topMsgID != 10 {
		t.Fatalf("expected topic id 10, got %d, ok=%v", topMsgID, ok)
	}
}

func TestInputPeerFromPeerAllowsBasicGroupWithoutEntity(t *testing.T) {
	inputPeer, err := inputPeerFromPeer(tg.Entities{}, &tg.PeerChat{ChatID: 4750561458})
	if err != nil {
		t.Fatalf("expected basic group input peer, got error: %v", err)
	}

	chat, ok := inputPeer.(*tg.InputPeerChat)
	if !ok {
		t.Fatalf("expected InputPeerChat, got %T", inputPeer)
	}
	if chat.ChatID != 4750561458 {
		t.Fatalf("expected chat id 4750561458, got %d", chat.ChatID)
	}
}

const EnvForTest = "test"
