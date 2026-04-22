package userbot

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
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
}

type messageMeta struct {
	userID    int64
	chatID    int64
	chatType  string
	chatTitle string
	text      string
	out       bool
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
		return c.handleMessage(ctx, entities, update.Message, "new_message", func(ctx context.Context, text string) error {
			return sendTextToMessagePeer(ctx, raw, entities, update.Message, text)
		})
	})
	dispatcher.OnNewChannelMessage(func(ctx context.Context, entities tg.Entities, update *tg.UpdateNewChannelMessage) error {
		return c.handleMessage(ctx, entities, update.Message, "new_channel_message", func(ctx context.Context, text string) error {
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

func (c *Client) handleMessage(ctx context.Context, entities tg.Entities, messageClass tg.MessageClass, updateType string, reply func(context.Context, string) error) error {
	msg, ok := messageClass.(*tg.Message)
	if !ok || msg == nil {
		return nil
	}

	meta := c.extractMessageMeta(entities, msg)
	if err := c.registerSeen(ctx, meta); err != nil {
		c.logger.WithError(err).WithFields(logging.Fields{
			"event":   "userbot_registration_failed",
			"user_id": meta.userID,
			"chat_id": meta.chatID,
		}).Error("failed to register seen telegram entities")
	}

	fields := logging.Fields{
		"event":       "userbot_update",
		"update_type": updateType,
		"chat_type":   meta.chatType,
		"out":         meta.out,
	}
	if meta.userID != 0 {
		fields["user_id"] = meta.userID
	}
	if meta.chatID != 0 {
		fields["chat_id"] = meta.chatID
	}
	if meta.text != "" {
		fields["text"] = meta.text
	}
	c.logger.WithFields(fields).Info("telegram update received")

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
		text: strings.TrimSpace(msg.Message),
		out:  msg.Out,
	}

	if from, ok := msg.GetFromID(); ok {
		meta.userID = peerUserID(from)
	}
	if meta.userID == 0 && msg.Out {
		meta.userID = c.cfg.UserbotOwnerID
	}

	switch peer := msg.PeerID.(type) {
	case *tg.PeerUser:
		meta.chatType = "private"
		meta.chatID = peer.UserID
	case *tg.PeerChat:
		meta.chatType = "group"
		meta.chatID = peer.ChatID
		if chat := entities.Chats[peer.ChatID]; chat != nil {
			meta.chatTitle = strings.TrimSpace(chat.Title)
		}
	case *tg.PeerChannel:
		meta.chatType = "channel"
		meta.chatID = peer.ChannelID
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

func peerUserID(peer tg.PeerClass) int64 {
	user, ok := peer.(*tg.PeerUser)
	if !ok || user == nil {
		return 0
	}
	return user.UserID
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
