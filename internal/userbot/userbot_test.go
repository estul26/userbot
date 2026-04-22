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
			name: "incoming bot text in group",
			meta: messageMeta{chatType: "group", senderIsBot: true, text: "hello"},
			want: true,
		},
		{
			name: "incoming bot media in group",
			meta: messageMeta{chatType: "group", senderIsBot: true, hasMedia: true},
			want: true,
		},
		{
			name: "feature disabled",
			meta: messageMeta{chatType: "group", senderIsBot: true, text: "hello"},
			want: false,
		},
		{
			name: "human message ignored",
			meta: messageMeta{chatType: "group", text: "hello"},
			want: false,
		},
		{
			name: "outgoing bot-like message ignored",
			meta: messageMeta{chatType: "group", senderIsBot: true, out: true, text: "hello"},
			want: false,
		},
		{
			name: "private bot message ignored",
			meta: messageMeta{chatType: "private", senderIsBot: true, text: "hello"},
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

func TestExtractMessageMetaDetectsBotSender(t *testing.T) {
	client := &Client{}
	msg := &tg.Message{
		PeerID:  &tg.PeerChat{ChatID: 200},
		Message: "bot says hi",
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
	if meta.chatType != "group" || meta.chatID != 200 || meta.userID != 100 {
		t.Fatalf("unexpected metadata: %+v", meta)
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
		Message: "bot says hi",
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
	meta := messageMeta{chatType: "group", userID: 100, text: "bot says hi"}

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
