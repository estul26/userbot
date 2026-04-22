// Package config defines the configuration contract and will handle loading and validating environment configuration.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

const (
	// Canonical environment variable keys.
	KeyTelegramAPIID             = "TELEGRAM_API_ID"
	KeyTelegramAPIHash           = "TELEGRAM_API_HASH"
	KeyTelegramPhone             = "TELEGRAM_PHONE"
	KeyTelegramSessionPath       = "TELEGRAM_SESSION_PATH"
	KeyTelegramSessionPassphrase = "TELEGRAM_SESSION_PASSPHRASE"
	KeyUserbotOwner              = "USERBOT_OWNER_ID"
	KeyMongoURI                  = "MONGO_URI"
	KeyMongoDB                   = "MONGO_DB"
	KeyAppEnv                    = "APP_ENV"
	KeyLogLevel                  = "LOG_LEVEL"
	KeyMirrorBotMessages         = "MIRROR_BOT_MESSAGES"
	KeyMirrorOrderPattern        = "MIRROR_ORDER_PATTERN"

	// Allowed environment values.
	EnvDevelopment = "development"
	EnvProduction  = "production"

	// Defaults for optional settings.
	DefaultAppEnv             = EnvProduction
	DefaultLogLevel           = "info"
	DefaultMirrorBotMessages  = false
	DefaultMirrorOrderPattern = `\b[A-Za-z0-9][A-Za-z0-9_-]{9,}\b`

	// Recommended database names by environment.
	DefaultMongoDBProd = "tg_bot"
	DefaultMongoDBDev  = "tg_bot_dev"
)

// VarSpec describes a single configuration key.
type VarSpec struct {
	Key         string // environment variable name
	Example     string // human-friendly sample value
	Required    bool   // whether the userbot must refuse to start without this value
	Default     string // default when unset (empty when required)
	Description string // what the variable controls
	Notes       string // extra guidance or policies
}

// Contract enumerates the authoritative configuration keys for the userbot.
// .env loading is only permitted when APP_ENV=development; production must rely
// on environment variables supplied by the runtime.
var Contract = []VarSpec{
	{
		Key:         KeyTelegramAPIID,
		Example:     "123456",
		Required:    true,
		Description: "Telegram application api_id from my.telegram.org.",
	},
	{
		Key:         KeyTelegramAPIHash,
		Example:     "0123456789abcdef0123456789abcdef",
		Required:    true,
		Description: "Telegram application api_hash from my.telegram.org.",
	},
	{
		Key:         KeyTelegramPhone,
		Example:     "+15551234567",
		Required:    true,
		Description: "Phone number for the Telegram user account, in international format.",
	},
	{
		Key:         KeyTelegramSessionPath,
		Example:     "./data/<repository-name>.session.enc",
		Required:    true,
		Description: "Path to the encrypted MTProto session file.",
	},
	{
		Key:         KeyTelegramSessionPassphrase,
		Example:     "long-random-passphrase",
		Required:    true,
		Description: "Passphrase used to encrypt the local MTProto session file.",
	},
	{
		Key:         KeyUserbotOwner,
		Example:     "123456789",
		Required:    true,
		Description: "Telegram user_id allowed to control owner-only userbot commands.",
	},
	{
		Key:         KeyMongoURI,
		Example:     "mongodb://localhost:27017",
		Required:    true,
		Description: "MongoDB connection string.",
	},
	{
		Key:         KeyMongoDB,
		Example:     DefaultMongoDBProd + " / " + DefaultMongoDBDev,
		Required:    true,
		Description: "MongoDB database name.",
		Notes:       "Recommended: production=" + DefaultMongoDBProd + ", development=" + DefaultMongoDBDev + ".",
	},
	{
		Key:         KeyAppEnv,
		Example:     EnvDevelopment + " / " + EnvProduction,
		Default:     DefaultAppEnv,
		Description: "Runtime environment; controls dotenv usage.",
		Notes:       "Load .env files only when APP_ENV=" + EnvDevelopment + ".",
	},
	{
		Key:         KeyLogLevel,
		Example:     DefaultLogLevel,
		Default:     DefaultLogLevel,
		Description: "Overrides default log level.",
	},
	{
		Key:         KeyMirrorBotMessages,
		Example:     "true / false",
		Default:     "false",
		Description: "When true, repost incoming bot-authored messages seen in groups when an order number is present.",
	},
	{
		Key:         KeyMirrorOrderPattern,
		Example:     DefaultMirrorOrderPattern,
		Default:     DefaultMirrorOrderPattern,
		Description: "Regular expression for candidate order numbers in mirrored bot text/captions; candidates must also contain at least one digit.",
	},
}

// Config mirrors resolved configuration values after loading.
type Config struct {
	TelegramAPIID             int
	TelegramAPIHash           string
	TelegramPhone             string
	TelegramSessionPath       string
	TelegramSessionPassphrase string
	UserbotOwnerID            int64
	MongoURI                  string
	MongoDB                   string
	AppEnv                    string
	LogLevel                  string
	MirrorBotMessages         bool
	MirrorOrderPattern        string
}

// Load resolves configuration from the environment (with optional dotenv in development).
func Load() (Config, error) {
	appEnv, err := resolveAppEnv()
	if err != nil {
		return Config{}, err
	}

	if err := loadDotEnv(appEnv); err != nil {
		return Config{}, err
	}

	cfg := Config{
		AppEnv:                    firstNonEmpty(normalizeEnv(os.Getenv(KeyAppEnv)), appEnv),
		TelegramAPIHash:           strings.TrimSpace(os.Getenv(KeyTelegramAPIHash)),
		TelegramPhone:             strings.TrimSpace(os.Getenv(KeyTelegramPhone)),
		TelegramSessionPath:       strings.TrimSpace(os.Getenv(KeyTelegramSessionPath)),
		TelegramSessionPassphrase: strings.TrimSpace(os.Getenv(KeyTelegramSessionPassphrase)),
		MongoURI:                  strings.TrimSpace(os.Getenv(KeyMongoURI)),
		MongoDB:                   strings.TrimSpace(os.Getenv(KeyMongoDB)),
		LogLevel:                  firstNonEmpty(strings.TrimSpace(os.Getenv(KeyLogLevel)), DefaultLogLevel),
		MirrorBotMessages:         DefaultMirrorBotMessages,
		MirrorOrderPattern:        firstNonEmpty(strings.TrimSpace(os.Getenv(KeyMirrorOrderPattern)), DefaultMirrorOrderPattern),
	}

	if err := validateAppEnv(cfg.AppEnv); err != nil {
		return Config{}, err
	}
	if mirrorRaw := strings.TrimSpace(os.Getenv(KeyMirrorBotMessages)); mirrorRaw != "" {
		mirror, parseErr := strconv.ParseBool(mirrorRaw)
		if parseErr != nil {
			return Config{}, fmt.Errorf("invalid %s: %w", KeyMirrorBotMessages, parseErr)
		}
		cfg.MirrorBotMessages = mirror
	}
	if _, compileErr := regexp.Compile(cfg.MirrorOrderPattern); compileErr != nil {
		return Config{}, fmt.Errorf("invalid %s: %w", KeyMirrorOrderPattern, compileErr)
	}

	missing := make([]string, 0)

	apiIDRaw := strings.TrimSpace(os.Getenv(KeyTelegramAPIID))
	if apiIDRaw == "" {
		missing = append(missing, KeyTelegramAPIID)
	} else {
		apiID, parseErr := strconv.Atoi(apiIDRaw)
		if parseErr != nil {
			return Config{}, fmt.Errorf("invalid %s: %w", KeyTelegramAPIID, parseErr)
		}
		if apiID <= 0 {
			return Config{}, fmt.Errorf("invalid %s: must be positive", KeyTelegramAPIID)
		}
		cfg.TelegramAPIID = apiID
	}

	if cfg.TelegramAPIHash == "" {
		missing = append(missing, KeyTelegramAPIHash)
	}
	if cfg.TelegramPhone == "" {
		missing = append(missing, KeyTelegramPhone)
	}
	if cfg.TelegramSessionPath == "" {
		missing = append(missing, KeyTelegramSessionPath)
	}
	if cfg.TelegramSessionPassphrase == "" {
		missing = append(missing, KeyTelegramSessionPassphrase)
	}

	ownerRaw := strings.TrimSpace(os.Getenv(KeyUserbotOwner))
	if ownerRaw == "" {
		missing = append(missing, KeyUserbotOwner)
	} else {
		ownerID, parseErr := strconv.ParseInt(ownerRaw, 10, 64)
		if parseErr != nil {
			return Config{}, fmt.Errorf("invalid %s: %w", KeyUserbotOwner, parseErr)
		}
		if ownerID == 0 {
			return Config{}, fmt.Errorf("invalid %s: must be non-zero", KeyUserbotOwner)
		}
		cfg.UserbotOwnerID = ownerID
	}

	if cfg.MongoURI == "" {
		missing = append(missing, KeyMongoURI)
	} else if err := validateMongoURI(cfg.MongoURI); err != nil {
		return Config{}, err
	}

	if cfg.MongoDB == "" {
		missing = append(missing, KeyMongoDB)
	}

	if len(missing) > 0 {
		return Config{}, fmt.Errorf("missing required environment variable(s): %s", strings.Join(missing, ", "))
	}

	return cfg, nil
}

// IsDevelopment reports if APP_ENV is development.
func (c Config) IsDevelopment() bool {
	return c.AppEnv == EnvDevelopment
}

// FormatRedacted returns a human-readable, secret-safe summary of the resolved configuration.
// Secrets such as TELEGRAM_API_HASH, TELEGRAM_SESSION_PASSPHRASE, and MongoDB
// credentials are redacted.
func FormatRedacted(cfg Config) string {
	lines := []string{
		"app_env: " + cfg.AppEnv,
		fmt.Sprintf("telegram_api_id: %d", cfg.TelegramAPIID),
		"telegram_api_hash: " + maskSecret(cfg.TelegramAPIHash),
		"telegram_phone: " + maskPhone(cfg.TelegramPhone),
		"telegram_session_path: " + cfg.TelegramSessionPath,
		"telegram_session_passphrase: " + maskSecret(cfg.TelegramSessionPassphrase),
		fmt.Sprintf("userbot_owner_id: %d", cfg.UserbotOwnerID),
		"mongo_uri: " + redactMongoURI(cfg.MongoURI),
		"mongo_db: " + cfg.MongoDB,
		"log_level: " + cfg.LogLevel,
		fmt.Sprintf("mirror_bot_messages: %t", cfg.MirrorBotMessages),
		"mirror_order_pattern: " + cfg.MirrorOrderPattern,
	}

	return strings.Join(lines, "\n")
}

func resolveAppEnv() (string, error) {
	if explicit := normalizeEnv(os.Getenv(KeyAppEnv)); explicit != "" {
		return explicit, nil
	}

	dotEnvValues, err := godotenv.Read()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DefaultAppEnv, nil
		}
		return "", fmt.Errorf("read .env: %w", err)
	}

	if envFromFile := normalizeEnv(dotEnvValues[KeyAppEnv]); envFromFile != "" {
		return envFromFile, nil
	}

	return DefaultAppEnv, nil
}

func loadDotEnv(appEnv string) error {
	if appEnv != EnvDevelopment {
		return nil
	}

	if err := godotenv.Load(); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("load .env: %w", err)
	}

	return nil
}

func validateAppEnv(appEnv string) error {
	if appEnv == EnvDevelopment || appEnv == EnvProduction {
		return nil
	}

	return fmt.Errorf("invalid %s: must be %q or %q", KeyAppEnv, EnvDevelopment, EnvProduction)
}

func validateMongoURI(uri string) error {
	parsed, err := url.Parse(uri)
	if err != nil {
		return fmt.Errorf("invalid %s: %w", KeyMongoURI, err)
	}

	if parsed.Scheme != "mongodb" && parsed.Scheme != "mongodb+srv" {
		return fmt.Errorf("invalid %s: unsupported scheme %q", KeyMongoURI, parsed.Scheme)
	}

	if parsed.Host == "" {
		return fmt.Errorf("invalid %s: missing host", KeyMongoURI)
	}

	return nil
}

func normalizeEnv(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func firstNonEmpty(values ...string) string {
	for _, val := range values {
		if strings.TrimSpace(val) != "" {
			return strings.TrimSpace(val)
		}
	}
	return ""
}

func maskSecret(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "<empty>"
	}

	if len(value) <= 4 {
		return "***"
	}

	return value[:4] + "...redacted"
}

func maskPhone(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 4 {
		return "...redacted"
	}

	return "...redacted" + value[len(value)-4:]
}

func redactMongoURI(uri string) string {
	parsed, err := url.Parse(uri)
	if err != nil {
		return "<invalid>"
	}

	parsed.User = nil

	return parsed.String()
}
