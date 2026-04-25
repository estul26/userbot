package userbot

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	tdcrypto "github.com/gotd/td/crypto"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
	"github.com/sirupsen/logrus"
	"go.uber.org/zap"

	"bot/internal/config"
	"bot/internal/logging"
)

const (
	pingMongoTimeout    = 2 * time.Second
	statusLookupTimeout = 2 * time.Second
	statusCountTimeout  = 2 * time.Second
	historyCutoffGrace  = 10 * time.Second
)

var (
	defaultMirrorOrderPattern = regexp.MustCompile(config.DefaultMirrorOrderPattern)
	dashDatePattern           = regexp.MustCompile(`^\d{4}-\d{1,2}-\d{1,2}$`)
	compactDatePattern        = regexp.MustCompile(`^(19|20)\d{6}$`)
	evmAddressPattern         = regexp.MustCompile(`^0x[0-9A-Fa-f]{40}$`)
	tronAddressPattern        = regexp.MustCompile(`^T[1-9A-HJ-NP-Za-km-z]{33}$`)
)

type UserRegistrar interface {
	EnsureUser(ctx context.Context, userID int64) (bool, error)
}

type GroupRegistrar interface {
	EnsureGroup(ctx context.Context, chatID int64, title string) (bool, error)
}

type MongoChecker interface {
	Ping(ctx context.Context) error
}

type StatsProvider interface {
	CountUsers(ctx context.Context) (int64, error)
	CountGroups(ctx context.Context) (int64, error)
}

type telegramUserGetter interface {
	UsersGetUsers(ctx context.Context, id []tg.InputUserClass) ([]tg.UserClass, error)
}

type telegramMessageForwarder interface {
	MessagesForwardMessages(ctx context.Context, request *tg.MessagesForwardMessagesRequest) (tg.UpdatesClass, error)
}

type telegramMessageSender interface {
	MessagesSendMessage(ctx context.Context, request *tg.MessagesSendMessageRequest) (tg.UpdatesClass, error)
}

type telegramMessageGetter interface {
	MessagesGetMessages(ctx context.Context, id []tg.InputMessageClass) (tg.MessagesMessagesClass, error)
	ChannelsGetMessages(ctx context.Context, request *tg.ChannelsGetMessagesRequest) (tg.MessagesMessagesClass, error)
}

type telegramAPI interface {
	telegramUserGetter
	telegramMessageForwarder
	telegramMessageSender
	telegramMessageGetter
}

type ClientOption func(*clientOptions)

type clientOptions struct {
	userRegistrar  UserRegistrar
	groupRegistrar GroupRegistrar
	mongoChecker   MongoChecker
	processStart   time.Time
	statsProvider  StatsProvider
	loginMode      bool
}

type Client struct {
	cfg            config.Config
	logger         *logrus.Entry
	userRegistrar  UserRegistrar
	groupRegistrar GroupRegistrar
	mongoChecker   MongoChecker
	processStart   time.Time
	statsProvider  StatsProvider
	loginMode      bool
	sessionStorage *EncryptedFileSessionStorage
	senderBotMu    sync.RWMutex
	senderBotCache map[int64]bool
	orderPattern   *regexp.Regexp
}

type messageMeta struct {
	userID         int64
	chatID         int64
	chatType       string
	chatTitle      string
	text           string
	out            bool
	senderIsBot    bool
	hasMedia       bool
	hasOrderNumber bool
	replyToMsgID   int
	replyToTopID   int
	replyToUserbot bool
}

func WithUserRegistrar(registrar UserRegistrar) ClientOption {
	return func(opts *clientOptions) {
		opts.userRegistrar = registrar
	}
}

func WithGroupRegistrar(registrar GroupRegistrar) ClientOption {
	return func(opts *clientOptions) {
		opts.groupRegistrar = registrar
	}
}

func WithMongoChecker(checker MongoChecker) ClientOption {
	return func(opts *clientOptions) {
		opts.mongoChecker = checker
	}
}

func WithProcessStart(start time.Time) ClientOption {
	return func(opts *clientOptions) {
		opts.processStart = start
	}
}

func WithStatsProvider(provider StatsProvider) ClientOption {
	return func(opts *clientOptions) {
		opts.statsProvider = provider
	}
}

func WithLoginMode(enabled bool) ClientOption {
	return func(opts *clientOptions) {
		opts.loginMode = enabled
	}
}

func NewClient(cfg config.Config, logger *logrus.Entry, opts ...ClientOption) (*Client, error) {
	if logger == nil {
		logger = logging.Logger()
	}

	clientOpts := clientOptions{processStart: time.Now()}
	for _, opt := range opts {
		if opt != nil {
			opt(&clientOpts)
		}
	}
	if clientOpts.processStart.IsZero() {
		clientOpts.processStart = time.Now()
	}

	storage, err := NewEncryptedFileSessionStorage(cfg.TelegramSessionPath, cfg.TelegramSessionPassphrase)
	if err != nil {
		return nil, err
	}
	orderPattern, err := compileMirrorOrderPattern(cfg.MirrorOrderPattern)
	if err != nil {
		return nil, err
	}

	return &Client{
		cfg:            cfg,
		logger:         logger,
		userRegistrar:  clientOpts.userRegistrar,
		groupRegistrar: clientOpts.groupRegistrar,
		mongoChecker:   clientOpts.mongoChecker,
		processStart:   clientOpts.processStart,
		statsProvider:  clientOpts.statsProvider,
		loginMode:      clientOpts.loginMode,
		sessionStorage: storage,
		senderBotCache: make(map[int64]bool),
		orderPattern:   orderPattern,
	}, nil
}

func (c *Client) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	if !c.loginMode {
		if _, err := c.sessionStorage.LoadSession(ctx); err != nil {
			if errors.Is(err, session.ErrNotFound) {
				return fmt.Errorf("encrypted session not found at %s; run with -login first", c.cfg.TelegramSessionPath)
			}
			return err
		}
	}

	dispatcher := tg.NewUpdateDispatcher()
	updateManager := updates.New(updates.Config{
		Handler: dispatcher,
		Logger:  zap.NewNop(),
	})
	tgClient := telegram.NewClient(c.cfg.TelegramAPIID, c.cfg.TelegramAPIHash, telegram.Options{
		Logger:         zap.NewNop(),
		SessionStorage: c.sessionStorage,
		UpdateHandler:  updateManager,
	})
	raw := tgClient.API()

	dispatcher.OnNewMessage(func(ctx context.Context, entities tg.Entities, update *tg.UpdateNewMessage) error {
		return c.handleMessage(ctx, raw, entities, update.Message, "new_message", func(ctx context.Context, text string) error {
			return sendTextToMessagePeer(ctx, raw, entities, update.Message, text)
		})
	})
	dispatcher.OnNewChannelMessage(func(ctx context.Context, entities tg.Entities, update *tg.UpdateNewChannelMessage) error {
		return c.handleMessage(ctx, raw, entities, update.Message, "new_channel_message", func(ctx context.Context, text string) error {
			return sendTextToMessagePeer(ctx, raw, entities, update.Message, text)
		})
	})

	c.logger.WithFields(logging.Fields{
		"event":        "userbot_listen",
		"login_mode":   c.loginMode,
		"session_path": c.cfg.TelegramSessionPath,
	}).Info("starting telegram mtproto client")

	err := tgClient.Run(ctx, func(ctx context.Context) error {
		if c.loginMode {
			flow := auth.NewFlow(newTerminalAuth(c.cfg.TelegramPhone), auth.SendCodeOptions{})
			if err := tgClient.Auth().IfNecessary(ctx, flow); err != nil {
				return fmt.Errorf("telegram login: %w", err)
			}
		} else {
			status, err := tgClient.Auth().Status(ctx)
			if err != nil {
				return fmt.Errorf("check telegram auth status: %w", err)
			}
			if !status.Authorized {
				return errors.New("telegram session is not authorized; run with -login first")
			}
		}

		self, err := tgClient.Self(ctx)
		if err != nil {
			return fmt.Errorf("get telegram self: %w", err)
		}
		if self.ID != c.cfg.UserbotOwnerID {
			return fmt.Errorf("authenticated user id %d does not match %s=%d", self.ID, config.KeyUserbotOwner, c.cfg.UserbotOwnerID)
		}

		if c.userRegistrar != nil {
			if _, err := c.userRegistrar.EnsureUser(ctx, self.ID); err != nil {
				return fmt.Errorf("ensure owner user: %w", err)
			}
		}

		c.logger.WithFields(logging.Fields{
			"event":    "userbot_ready",
			"user_id":  self.ID,
			"username": self.Username,
		}).Info("telegram userbot authenticated")

		return updateManager.Run(ctx, tgClient.API(), self.ID, updates.AuthOptions{
			IsBot: self.Bot,
			OnStart: func(context.Context) {
				c.logger.WithField("event", "userbot_updates_ready").Info("telegram update recovery initialized")
			},
		})
	})
	if err != nil && errors.Is(err, context.Canceled) && ctx.Err() != nil {
		c.logger.WithField("event", "userbot_stopped").Info("telegram userbot stopped")
		return nil
	}
	if err != nil {
		return err
	}

	c.logger.WithField("event", "userbot_stopped").Info("telegram userbot stopped")
	return nil
}

func (c *Client) handleMessage(ctx context.Context, api telegramAPI, entities tg.Entities, messageClass tg.MessageClass, updateType string, reply func(context.Context, string) error) error {
	msg, ok := messageClass.(*tg.Message)
	if !ok || msg == nil {
		return nil
	}
	if c.isHistoricalMessage(msg) {
		return nil
	}

	meta := c.extractMessageMeta(entities, msg)
	if err := c.resolveMessageSenderBot(ctx, api, entities, msg, &meta); err != nil {
		c.logger.WithError(err).WithFields(logging.Fields{
			"event":   "bot_sender_lookup_failed",
			"chat_id": meta.chatID,
			"user_id": meta.userID,
		}).Warn("failed to resolve message sender bot status")
	}
	if err := c.resolveReplyToUserbot(ctx, api, entities, msg, &meta); err != nil {
		c.logger.WithError(err).WithFields(logging.Fields{
			"event":           "reply_target_lookup_failed",
			"chat_id":         meta.chatID,
			"reply_to_msg_id": meta.replyToMsgID,
			"user_id":         meta.userID,
		}).Warn("failed to resolve reply target author")
	}
	if err := c.registerSeen(ctx, meta); err != nil {
		c.logger.WithError(err).WithFields(logging.Fields{
			"event":   "userbot_registration_failed",
			"user_id": meta.userID,
			"chat_id": meta.chatID,
		}).Error("failed to register seen telegram entities")
	}

	fields := logging.Fields{
		"event":            "userbot_update",
		"update_type":      updateType,
		"chat_type":        meta.chatType,
		"has_order_number": meta.hasOrderNumber,
		"out":              meta.out,
		"reply_to_userbot": meta.replyToUserbot,
		"sender_is_bot":    meta.senderIsBot,
	}
	if meta.userID != 0 {
		fields["user_id"] = meta.userID
	}
	if meta.chatID != 0 {
		fields["chat_id"] = meta.chatID
	}
	if meta.replyToMsgID != 0 {
		fields["reply_to_msg_id"] = meta.replyToMsgID
	}
	if meta.replyToTopID != 0 {
		fields["reply_to_top_id"] = meta.replyToTopID
	}
	if meta.text != "" {
		fields["text"] = meta.text
	}
	c.logger.WithFields(fields).Info("telegram update received")

	if c.shouldMirrorBotMessage(meta) {
		if err := mirrorMessageToSamePeer(ctx, api, entities, msg); err != nil {
			c.logger.WithError(err).WithFields(logging.Fields{
				"event":           "bot_message_mirror_failed",
				"chat_id":         meta.chatID,
				"reply_to_msg_id": meta.replyToMsgID,
				"user_id":         meta.userID,
			}).Error("failed to mirror bot message")
			return err
		}

		c.logger.WithFields(logging.Fields{
			"event":           "bot_message_mirrored",
			"chat_id":         meta.chatID,
			"reply_to_msg_id": meta.replyToMsgID,
			"user_id":         meta.userID,
		}).Info("mirrored bot message")
		return nil
	}

	if !isOwnerCommand(meta, c.cfg.UserbotOwnerID) {
		return nil
	}

	response, handled := c.commandResponse(ctx, commandName(meta.text))
	if !handled {
		c.logger.WithFields(logging.Fields{
			"event":   "userbot_command_unknown",
			"command": commandName(meta.text),
			"chat_id": meta.chatID,
		}).Info("ignored unknown owner command")
		return nil
	}

	if err := reply(ctx, response); err != nil {
		c.logger.WithError(err).WithFields(logging.Fields{
			"event":   "userbot_command_reply_failed",
			"command": commandName(meta.text),
			"chat_id": meta.chatID,
		}).Error("failed to send userbot command response")
		return err
	}

	c.logger.WithFields(logging.Fields{
		"event":   "userbot_command_sent",
		"command": commandName(meta.text),
		"chat_id": meta.chatID,
	}).Info("sent userbot command response")

	return nil
}

func (c *Client) isHistoricalMessage(msg *tg.Message) bool {
	if msg == nil || msg.Date == 0 || c.processStart.IsZero() {
		return false
	}

	messageTime := time.Unix(int64(msg.Date), 0)
	return messageTime.Before(c.processStart.Add(-historyCutoffGrace))
}

func (c *Client) registerSeen(ctx context.Context, meta messageMeta) error {
	if c.userRegistrar != nil && meta.userID != 0 {
		if _, err := c.userRegistrar.EnsureUser(ctx, meta.userID); err != nil {
			return err
		}
	}
	if c.groupRegistrar != nil && meta.chatID != 0 && meta.chatType != "private" {
		if _, err := c.groupRegistrar.EnsureGroup(ctx, meta.chatID, meta.chatTitle); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) extractMessageMeta(entities tg.Entities, msg *tg.Message) messageMeta {
	meta := messageMeta{
		text:     strings.TrimSpace(msg.Message),
		out:      msg.Out,
		hasMedia: hasMessageMedia(msg),
	}
	meta.hasOrderNumber = c.hasOrderNumber(meta.text)

	if from, ok := msg.GetFromID(); ok {
		meta.userID = peerUserID(from)
	}
	if meta.userID == 0 && msg.Out {
		meta.userID = c.cfg.UserbotOwnerID
	}
	if user := entities.Users[meta.userID]; user != nil {
		meta.senderIsBot = user.Bot
	}
	meta.replyToMsgID, meta.replyToTopID = replyTargetFromMessage(msg)

	switch peer := msg.PeerID.(type) {
	case *tg.PeerUser:
		meta.chatType = "private"
		meta.chatID = peer.UserID
	case *tg.PeerChat:
		meta.chatType = "group"
		meta.chatID = canonicalBasicGroupID(peer.ChatID)
		if chat := entities.Chats[peer.ChatID]; chat != nil {
			meta.chatTitle = strings.TrimSpace(chat.Title)
		}
	case *tg.PeerChannel:
		meta.chatType = "channel"
		meta.chatID = canonicalChannelID(peer.ChannelID)
		if channel := entities.Channels[peer.ChannelID]; channel != nil {
			meta.chatTitle = strings.TrimSpace(channel.Title)
			if channel.Megagroup {
				meta.chatType = "group"
			}
		}
	default:
		meta.chatType = "unknown"
	}

	return meta
}

func canonicalBasicGroupID(chatID int64) int64 {
	if chatID < 0 {
		return chatID
	}
	return -chatID
}

func canonicalChannelID(channelID int64) int64 {
	const channelPrefix = int64(1_000_000_000_000)
	if channelID <= -channelPrefix {
		return channelID
	}
	if channelID < 0 {
		channelID = -channelID
	}
	return -(channelPrefix + channelID)
}

func (c *Client) shouldMirrorBotMessage(meta messageMeta) bool {
	return c.cfg.MirrorBotMessages &&
		meta.chatType == "group" &&
		meta.senderIsBot &&
		meta.hasOrderNumber &&
		!meta.out &&
		!meta.replyToUserbot &&
		meta.hasMirrorableContent()
}

func (c *Client) shouldLookupReplyToUserbot(meta messageMeta) bool {
	return c.cfg.MirrorBotMessages &&
		meta.chatType == "group" &&
		meta.senderIsBot &&
		meta.hasOrderNumber &&
		!meta.out &&
		meta.hasMirrorableContent() &&
		meta.replyToMsgID != 0 &&
		c.cfg.UserbotOwnerID != 0
}

func (c *Client) shouldLookupMessageSenderBot(meta messageMeta) bool {
	return c.cfg.MirrorBotMessages &&
		meta.chatType == "group" &&
		meta.hasOrderNumber &&
		!meta.senderIsBot &&
		!meta.out &&
		meta.hasMirrorableContent() &&
		meta.userID != 0
}

func (m messageMeta) hasMirrorableContent() bool {
	return m.text != "" || m.hasMedia
}

func compileMirrorOrderPattern(pattern string) (*regexp.Regexp, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		pattern = config.DefaultMirrorOrderPattern
	}

	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("compile mirror order pattern: %w", err)
	}
	return compiled, nil
}

func (c *Client) hasOrderNumber(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}

	pattern := c.orderPattern
	if pattern == nil {
		pattern = defaultMirrorOrderPattern
	}

	for _, candidate := range pattern.FindAllString(text, -1) {
		if containsASCIIDigit(candidate) && !isIgnoredOrderCandidate(candidate) {
			return true
		}
	}

	return false
}

func isIgnoredOrderCandidate(value string) bool {
	return isLikelyDateToken(value) || isLikelyUSDTAddress(value)
}

func isLikelyDateToken(value string) bool {
	return dashDatePattern.MatchString(value) || compactDatePattern.MatchString(value)
}

func isLikelyUSDTAddress(value string) bool {
	return evmAddressPattern.MatchString(value) || tronAddressPattern.MatchString(value)
}

func containsASCIIDigit(value string) bool {
	for _, r := range value {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

func (c *Client) resolveMessageSenderBot(ctx context.Context, userGetter telegramUserGetter, entities tg.Entities, msg *tg.Message, meta *messageMeta) error {
	if meta == nil || meta.userID == 0 {
		return nil
	}
	if meta.senderIsBot {
		c.cacheSenderBot(meta.userID, true)
		return nil
	}
	if cached, ok := c.cachedSenderBot(meta.userID); ok {
		meta.senderIsBot = cached
		return nil
	}
	if !c.shouldLookupMessageSenderBot(*meta) || userGetter == nil || msg == nil || msg.ID == 0 {
		return nil
	}

	inputPeer, err := inputPeerFromPeer(entities, msg.PeerID)
	if err != nil {
		return fmt.Errorf("resolve sender peer: %w", err)
	}

	users, err := userGetter.UsersGetUsers(ctx, []tg.InputUserClass{
		&tg.InputUserFromMessage{
			Peer:   inputPeer,
			MsgID:  msg.ID,
			UserID: meta.userID,
		},
	})
	if err != nil {
		return fmt.Errorf("get sender user: %w", err)
	}

	for _, userClass := range users {
		user, ok := userClass.AsNotEmpty()
		if !ok || user.ID != meta.userID {
			continue
		}

		meta.senderIsBot = user.Bot
		c.cacheSenderBot(meta.userID, user.Bot)
		return nil
	}

	return nil
}

func (c *Client) resolveReplyToUserbot(ctx context.Context, messageGetter telegramMessageGetter, entities tg.Entities, msg *tg.Message, meta *messageMeta) error {
	if meta == nil || !c.shouldLookupReplyToUserbot(*meta) || messageGetter == nil || msg == nil {
		return nil
	}

	replyMsg, err := getMessageByID(ctx, messageGetter, entities, msg.PeerID, meta.replyToMsgID)
	if err != nil {
		return fmt.Errorf("get reply target message: %w", err)
	}
	if replyMsg == nil {
		return nil
	}

	meta.replyToUserbot = messageAuthorUserID(replyMsg, c.cfg.UserbotOwnerID) == c.cfg.UserbotOwnerID
	return nil
}

func getMessageByID(ctx context.Context, messageGetter telegramMessageGetter, entities tg.Entities, peerID tg.PeerClass, msgID int) (*tg.Message, error) {
	if msgID == 0 {
		return nil, nil
	}

	inputMessage := &tg.InputMessageID{ID: msgID}
	var messages tg.MessagesMessagesClass
	var err error

	if _, ok := peerID.(*tg.PeerChannel); ok {
		inputPeer, err := inputPeerFromPeer(entities, peerID)
		if err != nil {
			return nil, fmt.Errorf("resolve channel peer: %w", err)
		}
		inputChannel, ok := peer.ToInputChannel(inputPeer)
		if !ok {
			return nil, fmt.Errorf("peer %T is not an input channel", inputPeer)
		}

		messages, err = messageGetter.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: inputChannel,
			ID:      []tg.InputMessageClass{inputMessage},
		})
	} else {
		messages, err = messageGetter.MessagesGetMessages(ctx, []tg.InputMessageClass{inputMessage})
	}
	if err != nil {
		return nil, err
	}

	return firstFullMessage(messages), nil
}

func firstFullMessage(messages tg.MessagesMessagesClass) *tg.Message {
	if messages == nil {
		return nil
	}
	modified, ok := messages.AsModified()
	if !ok {
		return nil
	}

	for _, messageClass := range modified.GetMessages() {
		msg, ok := messageClass.(*tg.Message)
		if ok && msg != nil {
			return msg
		}
	}

	return nil
}

func (c *Client) cachedSenderBot(userID int64) (bool, bool) {
	c.senderBotMu.RLock()
	defer c.senderBotMu.RUnlock()

	if c.senderBotCache == nil {
		return false, false
	}

	isBot, ok := c.senderBotCache[userID]
	return isBot, ok
}

func (c *Client) cacheSenderBot(userID int64, isBot bool) {
	c.senderBotMu.Lock()
	defer c.senderBotMu.Unlock()

	if c.senderBotCache == nil {
		c.senderBotCache = make(map[int64]bool)
	}
	c.senderBotCache[userID] = isBot
}

func peerUserID(peer tg.PeerClass) int64 {
	user, ok := peer.(*tg.PeerUser)
	if !ok || user == nil {
		return 0
	}
	return user.UserID
}

func messageAuthorUserID(msg *tg.Message, fallbackOutgoingUserID int64) int64 {
	if msg == nil {
		return 0
	}
	if from, ok := msg.GetFromID(); ok {
		return peerUserID(from)
	}
	if msg.Out {
		return fallbackOutgoingUserID
	}
	return 0
}

func sendTextToMessagePeer(ctx context.Context, raw *tg.Client, entities tg.Entities, messageClass tg.MessageClass, text string) error {
	inputPeer, err := inputPeerFromMessage(entities, messageClass)
	if err != nil {
		return err
	}

	randomID, err := tdcrypto.RandInt64(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate message random id: %w", err)
	}

	_, err = raw.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
		Peer:     inputPeer,
		Message:  text,
		RandomID: randomID,
	})
	if err != nil {
		return fmt.Errorf("send text: %w", err)
	}

	return nil
}

func mirrorMessageToSamePeer(ctx context.Context, api telegramAPI, entities tg.Entities, msg *tg.Message) error {
	if msg == nil {
		return errors.New("telegram message is nil")
	}

	if !hasMessageMedia(msg) && strings.TrimSpace(msg.Message) != "" {
		return sendTextToSamePeerWithReply(ctx, api, entities, msg, strings.TrimSpace(msg.Message))
	}

	return forwardMessageToSamePeer(ctx, api, entities, msg)
}

func sendTextToSamePeerWithReply(ctx context.Context, sender telegramMessageSender, entities tg.Entities, msg tg.NotEmptyMessage, text string) error {
	if sender == nil {
		return errors.New("telegram message sender is nil")
	}

	inputPeer, err := inputPeerFromPeer(entities, msg.GetPeerID())
	if err != nil {
		return err
	}

	randomID, err := tdcrypto.RandInt64(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate message random id: %w", err)
	}

	request := &tg.MessagesSendMessageRequest{
		Peer:     inputPeer,
		Message:  text,
		RandomID: randomID,
	}
	if replyTo := inputReplyToFromMessage(msg); replyTo != nil {
		request.SetReplyTo(replyTo)
	}

	if _, err := sender.MessagesSendMessage(ctx, request); err != nil {
		return fmt.Errorf("send text: %w", err)
	}

	return nil
}

func forwardMessageToSamePeer(ctx context.Context, forwarder telegramMessageForwarder, entities tg.Entities, messageClass tg.MessageClass) error {
	if forwarder == nil {
		return errors.New("telegram message forwarder is nil")
	}

	msg, ok := messageClass.AsNotEmpty()
	if !ok {
		return fmt.Errorf("unexpected message type %T", messageClass)
	}

	inputPeer, err := inputPeerFromPeer(entities, msg.GetPeerID())
	if err != nil {
		return err
	}

	randomID, err := tdcrypto.RandInt64(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate forward random id: %w", err)
	}

	request := &tg.MessagesForwardMessagesRequest{
		FromPeer: inputPeer,
		ToPeer:   inputPeer,
		ID:       []int{msg.GetID()},
		RandomID: []int64{randomID},
	}
	request.SetDropAuthor(true)
	if replyTo := inputReplyToFromMessage(msg); replyTo != nil {
		request.SetReplyTo(replyTo)
	}

	if _, err := forwarder.MessagesForwardMessages(ctx, request); err != nil {
		return fmt.Errorf("forward message: %w", err)
	}

	return nil
}

func replyTargetFromMessage(msg tg.NotEmptyMessage) (int, int) {
	replyTo := inputReplyToFromMessage(msg)
	if replyTo == nil {
		return 0, 0
	}

	replyToMessage, ok := replyTo.(*tg.InputReplyToMessage)
	if !ok {
		return 0, 0
	}

	topMsgID, _ := replyToMessage.GetTopMsgID()
	return replyToMessage.ReplyToMsgID, topMsgID
}

func inputReplyToFromMessage(msg tg.NotEmptyMessage) tg.InputReplyToClass {
	if msg == nil {
		return nil
	}

	replyHeaderClass, ok := msg.GetReplyTo()
	if !ok {
		return nil
	}

	replyHeader, ok := replyHeaderClass.(*tg.MessageReplyHeader)
	if !ok {
		return nil
	}

	replyToMsgID, ok := replyHeader.GetReplyToMsgID()
	if !ok || replyToMsgID == 0 {
		return nil
	}

	replyTo := &tg.InputReplyToMessage{ReplyToMsgID: replyToMsgID}
	if topMsgID, ok := replyHeader.GetReplyToTopID(); ok && topMsgID != 0 {
		replyTo.SetTopMsgID(topMsgID)
	}

	return replyTo
}

func hasMessageMedia(msg *tg.Message) bool {
	if msg == nil || msg.Media == nil {
		return false
	}
	_, empty := msg.Media.(*tg.MessageMediaEmpty)
	return !empty
}

func inputPeerFromMessage(entities tg.Entities, messageClass tg.MessageClass) (tg.InputPeerClass, error) {
	msg, ok := messageClass.AsNotEmpty()
	if !ok {
		emptyMsg, ok := messageClass.(*tg.MessageEmpty)
		if !ok {
			return nil, fmt.Errorf("unexpected message type %T", messageClass)
		}

		peerID, ok := emptyMsg.GetPeerID()
		if !ok {
			return nil, fmt.Errorf("message %T has no peer id", messageClass)
		}
		return inputPeerFromPeer(entities, peerID)
	}

	return inputPeerFromPeer(entities, msg.GetPeerID())
}

func inputPeerFromPeer(entities tg.Entities, peerID tg.PeerClass) (tg.InputPeerClass, error) {
	switch p := peerID.(type) {
	case *tg.PeerChat:
		return &tg.InputPeerChat{ChatID: p.ChatID}, nil
	default:
		peerEntities := peer.EntitiesFromUpdate(entities)
		return peerEntities.ExtractPeer(peerID)
	}
}

func isOwnerCommand(meta messageMeta, ownerID int64) bool {
	return meta.out && meta.userID == ownerID && strings.HasPrefix(meta.text, ".")
}

func commandName(text string) string {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, ".") {
		return ""
	}

	name := strings.TrimPrefix(strings.Fields(text)[0], ".")
	return strings.ToLower(strings.TrimSpace(name))
}

func (c *Client) commandResponse(ctx context.Context, name string) (string, bool) {
	switch name {
	case "ping":
		return c.pingResponse(ctx), true
	case "status":
		return c.statusResponse(ctx), true
	default:
		return "", false
	}
}

func (c *Client) pingResponse(ctx context.Context) string {
	mongoState := "not_configured"
	if c.mongoChecker != nil {
		pingCtx, cancel := context.WithTimeout(ctx, pingMongoTimeout)
		err := c.mongoChecker.Ping(pingCtx)
		cancel()
		if err != nil {
			c.logger.WithError(err).WithField("event", "userbot_ping_mongo_error").Warn("mongo ping failed")
			mongoState = "error"
		} else {
			mongoState = "ok"
		}
	}

	return strings.Join([]string{
		"pong",
		"env: " + c.cfg.AppEnv,
		"uptime: " + time.Since(c.processStart).Round(time.Second).String(),
		"mongo: " + mongoState,
	}, "\n")
}

func (c *Client) statusResponse(ctx context.Context) string {
	mongoState := "not_configured"
	if c.mongoChecker != nil {
		pingCtx, cancel := context.WithTimeout(ctx, statusLookupTimeout)
		err := c.mongoChecker.Ping(pingCtx)
		cancel()
		if err != nil {
			c.logger.WithError(err).WithField("event", "userbot_status_mongo_error").Warn("mongo ping failed")
			mongoState = "error"
		} else {
			mongoState = "ok"
		}
	}

	users := "n/a"
	groups := "n/a"
	if c.statsProvider != nil {
		countCtx, cancel := context.WithTimeout(ctx, statusCountTimeout)
		if count, err := c.statsProvider.CountUsers(countCtx); err != nil {
			c.logger.WithError(err).WithField("event", "userbot_status_user_count_error").Warn("user count failed")
			users = "error"
		} else {
			users = fmt.Sprintf("%d", count)
		}
		if count, err := c.statsProvider.CountGroups(countCtx); err != nil {
			c.logger.WithError(err).WithField("event", "userbot_status_group_count_error").Warn("group count failed")
			groups = "error"
		} else {
			groups = fmt.Sprintf("%d", count)
		}
		cancel()
	}

	return strings.Join([]string{
		"userbot_status: running",
		"env: " + c.cfg.AppEnv,
		"uptime: " + time.Since(c.processStart).Round(time.Second).String(),
		"mongo: " + mongoState,
		"registered_users: " + users,
		"known_chats: " + groups,
	}, "\n")
}
