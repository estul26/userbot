package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDefaultsAndRequired(t *testing.T) {
	unsetEnv(t, KeyAppEnv)
	unsetEnv(t, KeyLogLevel)
	unsetEnv(t, KeyMirrorBotMessages)
	unsetEnv(t, KeyMirrorOrderPattern)

	t.Setenv(KeyTelegramAPIID, "123456")
	t.Setenv(KeyTelegramAPIHash, "hash")
	t.Setenv(KeyTelegramPhone, "+15551234567")
	t.Setenv(KeyTelegramSessionPath, "./data/session.enc")
	t.Setenv(KeyTelegramSessionPassphrase, "passphrase")
	t.Setenv(KeyUserbotOwner, "12345")
	t.Setenv(KeyMongoURI, "mongodb://localhost:27017")
	t.Setenv(KeyMongoDB, "tg_bot")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected config to load, got error: %v", err)
	}

	if cfg.AppEnv != DefaultAppEnv {
		t.Fatalf("expected app env %s, got %s", DefaultAppEnv, cfg.AppEnv)
	}

	if cfg.TelegramAPIID != 123456 {
		t.Fatalf("expected api id to be parsed, got %d", cfg.TelegramAPIID)
	}

	if cfg.UserbotOwnerID != 12345 {
		t.Fatalf("expected userbot owner id to be parsed, got %d", cfg.UserbotOwnerID)
	}

	if cfg.LogLevel != DefaultLogLevel {
		t.Fatalf("expected default log level %s, got %s", DefaultLogLevel, cfg.LogLevel)
	}

	if cfg.MirrorBotMessages != DefaultMirrorBotMessages {
		t.Fatalf("expected mirror bot messages default %v, got %v", DefaultMirrorBotMessages, cfg.MirrorBotMessages)
	}
	if cfg.MirrorOrderPattern != DefaultMirrorOrderPattern {
		t.Fatalf("expected mirror order pattern default %q, got %q", DefaultMirrorOrderPattern, cfg.MirrorOrderPattern)
	}
}

func TestLoadFailsOnMissingRequired(t *testing.T) {
	unsetEnv(t, KeyAppEnv)

	unsetEnv(t, KeyTelegramAPIID)
	t.Setenv(KeyTelegramAPIHash, "hash")
	t.Setenv(KeyTelegramPhone, "+15551234567")
	t.Setenv(KeyTelegramSessionPath, "./data/session.enc")
	t.Setenv(KeyTelegramSessionPassphrase, "passphrase")
	t.Setenv(KeyUserbotOwner, "999")
	t.Setenv(KeyMongoURI, "mongodb://localhost:27017")
	t.Setenv(KeyMongoDB, "tg_bot")

	_, err := Load()
	if err == nil {
		t.Fatalf("expected missing required env to error")
	}

	if !strings.Contains(err.Error(), KeyTelegramAPIID) {
		t.Fatalf("expected error to mention missing %s, got %v", KeyTelegramAPIID, err)
	}
}

func TestLoadValidatesOwnerID(t *testing.T) {
	unsetEnv(t, KeyAppEnv)

	t.Setenv(KeyTelegramAPIID, "123456")
	t.Setenv(KeyTelegramAPIHash, "hash")
	t.Setenv(KeyTelegramPhone, "+15551234567")
	t.Setenv(KeyTelegramSessionPath, "./data/session.enc")
	t.Setenv(KeyTelegramSessionPassphrase, "passphrase")
	t.Setenv(KeyUserbotOwner, "abc")
	t.Setenv(KeyMongoURI, "mongodb://localhost:27017")
	t.Setenv(KeyMongoDB, "tg_bot")

	_, err := Load()
	if err == nil {
		t.Fatalf("expected error for invalid %s", KeyUserbotOwner)
	}

	if !strings.Contains(err.Error(), KeyUserbotOwner) {
		t.Fatalf("expected error to mention %s, got %v", KeyUserbotOwner, err)
	}
}

func TestLoadUsesDotEnvInDevelopment(t *testing.T) {
	tmpDir := t.TempDir()
	dotenvContent := []byte(`
APP_ENV=development
TELEGRAM_API_ID=777
TELEGRAM_API_HASH=dotenv-hash
TELEGRAM_PHONE=+15550007777
TELEGRAM_SESSION_PATH=./data/dotenv-session.enc
TELEGRAM_SESSION_PASSPHRASE=dotenv-passphrase
USERBOT_OWNER_ID=77
MONGO_URI=mongodb://from-dotenv
MONGO_DB=tg_bot_dev
LOG_LEVEL=debug
MIRROR_BOT_MESSAGES=true
MIRROR_ORDER_PATTERN=\b[A-Z0-9]{6,}\b
`)

	if err := os.WriteFile(filepath.Join(tmpDir, ".env"), dotenvContent, 0o644); err != nil {
		t.Fatalf("failed to write dotenv: %v", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd failed: %v", err)
	}

	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir failed: %v", err)
	}

	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})

	unsetEnv(t, KeyAppEnv)
	unsetEnv(t, KeyTelegramAPIID)
	unsetEnv(t, KeyTelegramAPIHash)
	unsetEnv(t, KeyTelegramPhone)
	unsetEnv(t, KeyTelegramSessionPath)
	unsetEnv(t, KeyTelegramSessionPassphrase)
	unsetEnv(t, KeyUserbotOwner)
	unsetEnv(t, KeyMongoURI)
	unsetEnv(t, KeyMongoDB)
	unsetEnv(t, KeyLogLevel)
	unsetEnv(t, KeyMirrorBotMessages)
	unsetEnv(t, KeyMirrorOrderPattern)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected dotenv-backed config to load, got error: %v", err)
	}

	if cfg.AppEnv != EnvDevelopment {
		t.Fatalf("expected development env from dotenv, got %s", cfg.AppEnv)
	}

	if cfg.TelegramAPIID != 777 {
		t.Fatalf("expected api id from dotenv, got %d", cfg.TelegramAPIID)
	}

	if cfg.TelegramAPIHash != "dotenv-hash" {
		t.Fatalf("expected api hash from dotenv, got %s", cfg.TelegramAPIHash)
	}

	if cfg.TelegramPhone != "+15550007777" {
		t.Fatalf("expected phone from dotenv, got %s", cfg.TelegramPhone)
	}

	if cfg.TelegramSessionPath != "./data/dotenv-session.enc" {
		t.Fatalf("expected session path from dotenv, got %s", cfg.TelegramSessionPath)
	}

	if cfg.UserbotOwnerID != 77 {
		t.Fatalf("expected owner id 77 from dotenv, got %d", cfg.UserbotOwnerID)
	}

	if cfg.MongoURI != "mongodb://from-dotenv" {
		t.Fatalf("expected mongo uri from dotenv, got %s", cfg.MongoURI)
	}

	if cfg.MongoDB != "tg_bot_dev" {
		t.Fatalf("expected mongo db from dotenv, got %s", cfg.MongoDB)
	}

	if cfg.LogLevel != "debug" {
		t.Fatalf("expected log level from dotenv, got %s", cfg.LogLevel)
	}

	if !cfg.MirrorBotMessages {
		t.Fatalf("expected mirror bot messages from dotenv")
	}
	if cfg.MirrorOrderPattern != `\b[A-Z0-9]{6,}\b` {
		t.Fatalf("expected mirror order pattern from dotenv, got %q", cfg.MirrorOrderPattern)
	}
}

func TestLoadValidatesMirrorBotMessages(t *testing.T) {
	unsetEnv(t, KeyAppEnv)

	t.Setenv(KeyTelegramAPIID, "123456")
	t.Setenv(KeyTelegramAPIHash, "hash")
	t.Setenv(KeyTelegramPhone, "+15551234567")
	t.Setenv(KeyTelegramSessionPath, "./data/session.enc")
	t.Setenv(KeyTelegramSessionPassphrase, "passphrase")
	t.Setenv(KeyUserbotOwner, "123")
	t.Setenv(KeyMongoURI, "mongodb://localhost:27017")
	t.Setenv(KeyMongoDB, "tg_bot")
	t.Setenv(KeyMirrorBotMessages, "not-bool")

	_, err := Load()
	if err == nil {
		t.Fatalf("expected invalid %s to error", KeyMirrorBotMessages)
	}

	if !strings.Contains(err.Error(), KeyMirrorBotMessages) {
		t.Fatalf("expected error to mention %s, got %v", KeyMirrorBotMessages, err)
	}
}

func TestLoadValidatesMirrorOrderPattern(t *testing.T) {
	unsetEnv(t, KeyAppEnv)

	t.Setenv(KeyTelegramAPIID, "123456")
	t.Setenv(KeyTelegramAPIHash, "hash")
	t.Setenv(KeyTelegramPhone, "+15551234567")
	t.Setenv(KeyTelegramSessionPath, "./data/session.enc")
	t.Setenv(KeyTelegramSessionPassphrase, "passphrase")
	t.Setenv(KeyUserbotOwner, "123")
	t.Setenv(KeyMongoURI, "mongodb://localhost:27017")
	t.Setenv(KeyMongoDB, "tg_bot")
	t.Setenv(KeyMirrorOrderPattern, "[")

	_, err := Load()
	if err == nil {
		t.Fatalf("expected invalid %s to error", KeyMirrorOrderPattern)
	}

	if !strings.Contains(err.Error(), KeyMirrorOrderPattern) {
		t.Fatalf("expected error to mention %s, got %v", KeyMirrorOrderPattern, err)
	}
}

func TestLoadValidatesMongoURIFormat(t *testing.T) {
	unsetEnv(t, KeyAppEnv)

	t.Setenv(KeyTelegramAPIID, "123456")
	t.Setenv(KeyTelegramAPIHash, "hash")
	t.Setenv(KeyTelegramPhone, "+15551234567")
	t.Setenv(KeyTelegramSessionPath, "./data/session.enc")
	t.Setenv(KeyTelegramSessionPassphrase, "passphrase")
	t.Setenv(KeyUserbotOwner, "123")
	t.Setenv(KeyMongoURI, "http://localhost:27017")
	t.Setenv(KeyMongoDB, "tg_bot")

	_, err := Load()
	if err == nil {
		t.Fatalf("expected invalid mongo uri to error")
	}

	if !strings.Contains(err.Error(), KeyMongoURI) {
		t.Fatalf("expected error to mention %s, got %v", KeyMongoURI, err)
	}
}

func TestFormatRedactedMasksSecrets(t *testing.T) {
	cfg := Config{
		TelegramAPIID:             123456,
		TelegramAPIHash:           "abcd1234secret",
		TelegramPhone:             "+15551234567",
		TelegramSessionPath:       "./data/session.enc",
		TelegramSessionPassphrase: "pass1234secret",
		UserbotOwnerID:            42,
		MongoURI:                  "mongodb://user:pass@localhost:27017/tg_bot",
		MongoDB:                   "tg_bot",
		AppEnv:                    EnvDevelopment,
		LogLevel:                  "debug",
		MirrorBotMessages:         true,
		MirrorOrderPattern:        DefaultMirrorOrderPattern,
	}

	summary := FormatRedacted(cfg)

	if strings.Contains(summary, "user:pass@") {
		t.Fatalf("expected mongo uri credentials to be redacted, got %s", summary)
	}

	if !strings.Contains(summary, "mongodb://localhost:27017/tg_bot") {
		t.Fatalf("expected mongo uri host to remain after redaction, got %s", summary)
	}

	if strings.Contains(summary, "1234secret") {
		t.Fatalf("expected telegram api hash to be redacted, got %s", summary)
	}

	if strings.Contains(summary, "pass1234secret") {
		t.Fatalf("expected session passphrase to be redacted, got %s", summary)
	}

	if !strings.Contains(summary, "telegram_api_hash: abcd...redacted") {
		t.Fatalf("expected api hash to show masked prefix, got %s", summary)
	}

	if !strings.Contains(summary, "telegram_phone: ...redacted4567") {
		t.Fatalf("expected phone to show masked suffix, got %s", summary)
	}

	if !strings.Contains(summary, "mirror_bot_messages: true") {
		t.Fatalf("expected mirror setting in summary, got %s", summary)
	}
	if !strings.Contains(summary, "mirror_order_pattern: "+DefaultMirrorOrderPattern) {
		t.Fatalf("expected mirror order pattern in summary, got %s", summary)
	}
}

func unsetEnv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "")
	_ = os.Unsetenv(key)
}
