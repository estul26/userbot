package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"

	"bot/internal/config"
	"bot/internal/feature/group"
	"bot/internal/feature/owner"
	"bot/internal/feature/user"
	"bot/internal/logging"
	"bot/internal/store"
	"bot/internal/userbot"
)

const (
	mongoConnectTimeout    = 10 * time.Second
	mongoIndexTimeout      = 5 * time.Second
	mongoDisconnectTimeout = 5 * time.Second
	ownerBootstrapTimeout  = 5 * time.Second
	userbotShutdownTimeout = 10 * time.Second
)

var processStart = time.Now()

func main() {
	configOnly := flag.Bool("config-only", false, "load and print configuration then exit")
	loginMode := flag.Bool("login", false, "perform interactive Telegram user login and store encrypted session")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		logging.Error("configuration error", logging.Fields{"error": err})
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		os.Exit(1)
	}

	logger, err := logging.Setup(cfg)
	if err != nil {
		logging.Error("logger setup error", logging.Fields{"error": err})
		fmt.Fprintf(os.Stderr, "logger setup error: %v\n", err)
		os.Exit(1)
	}

	if *configOnly {
		logging.Info("configuration check", logging.Fields{"event": "config_only"})
		fmt.Println("configuration check: ok")
		fmt.Println(config.FormatRedacted(cfg))
		return
	}

	logger.WithFields(logging.Fields{
		"event":                "startup",
		"mongo_db":             cfg.MongoDB,
		"login_mode":           *loginMode,
		"mirror_bot_messages":  cfg.MirrorBotMessages,
		"mirror_order_pattern": cfg.MirrorOrderPattern,
		"session_path":         cfg.TelegramSessionPath,
	}).Info("configuration loaded")

	connectCtx, cancel := context.WithTimeout(context.Background(), mongoConnectTimeout)
	mongoManager, err := store.NewManager(connectCtx, cfg)
	cancel()
	if err != nil {
		logger.WithError(err).Error("mongo connection error")
		fmt.Fprintf(os.Stderr, "mongo connection error: %v\n", err)
		os.Exit(1)
	}

	logger.WithField("event", "mongo_connect").Info("connected to mongo")

	indexCtx, cancelIndexes := context.WithTimeout(context.Background(), mongoIndexTimeout)
	if err := mongoManager.EnsureBaseIndexes(indexCtx); err != nil {
		cancelIndexes()
		logger.WithError(err).Error("mongo index setup error")
		fmt.Fprintf(os.Stderr, "mongo index setup error: %v\n", err)
		os.Exit(1)
	}
	cancelIndexes()

	logger.WithField("event", "mongo_indexes").Info("ensured base mongo indexes")

	ownerRegistrar := owner.NewRegistrar(mongoManager.Users(), logger)
	ownerCtx, cancelOwner := context.WithTimeout(context.Background(), ownerBootstrapTimeout)
	if err := ownerRegistrar.EnsureOwner(ownerCtx, cfg.UserbotOwnerID); err != nil {
		cancelOwner()
		logger.WithError(err).Error("owner bootstrap error")
		fmt.Fprintf(os.Stderr, "owner bootstrap error: %v\n", err)
		os.Exit(1)
	}
	cancelOwner()

	userRegistrar := user.NewRegistrar(mongoManager.Users(), logger)
	groupRegistrar := group.NewRegistrar(mongoManager.Groups(), logger)
	statsProvider := store.NewStatsProvider(mongoManager.Users(), mongoManager.Groups())

	tgClient, err := userbot.NewClient(cfg, logger,
		userbot.WithUserRegistrar(userRegistrar),
		userbot.WithGroupRegistrar(groupRegistrar),
		userbot.WithMongoChecker(mongoManager),
		userbot.WithProcessStart(processStart),
		userbot.WithStatsProvider(statsProvider),
		userbot.WithLoginMode(*loginMode),
	)
	if err != nil {
		logger.WithError(err).Error("userbot client setup error")
		fmt.Fprintf(os.Stderr, "userbot client setup error: %v\n", err)
		os.Exit(1)
	}

	logger.WithField("event", "userbot_initialized").Info("userbot client initialized")

	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	userbotCtx, cancelUserbot := context.WithCancel(context.Background())
	tgDone := make(chan error, 1)
	stopped := false

	go func() {
		tgDone <- tgClient.Start(userbotCtx)
	}()

	select {
	case <-signalCtx.Done():
		logger.WithField("event", "shutdown_signal").Info("received termination signal, stopping userbot")
	case err := <-tgDone:
		stopped = true
		if err != nil {
			logger.WithError(err).Error("userbot stopped with error")
			fmt.Fprintf(os.Stderr, "userbot error: %v\n", err)
			cancelUserbot()
			closeMongo(logger, mongoManager)
			os.Exit(1)
		}
		logger.WithField("event", "userbot_stopped_early").Warn("userbot stopped before shutdown signal")
	}

	cancelUserbot()

	if !stopped {
		waitCtx, cancelWait := context.WithTimeout(context.Background(), userbotShutdownTimeout)
		select {
		case err := <-tgDone:
			if err != nil {
				logger.WithError(err).Error("userbot shutdown error")
			}
		case <-waitCtx.Done():
			logger.WithField("event", "userbot_shutdown_timeout").Warn("timed out waiting for userbot to stop")
		}
		cancelWait()
	}

	closeMongo(logger, mongoManager)

	logger.WithField("event", "shutdown_complete").Info("shutdown complete")
}

func closeMongo(logger *logrus.Entry, mongoManager *store.Manager) {
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), mongoDisconnectTimeout)
	if err := mongoManager.Close(shutdownCtx); err != nil {
		logger.WithError(err).Error("mongo disconnect error")
	} else {
		logger.WithField("event", "mongo_disconnect").Info("mongo client disconnected")
	}
	cancelShutdown()
}
