## Tech Stack
- **Language and runtime**: Go 1.25 with goroutines and context propagation; module at repository root.
- **Frameworks and libraries**:
  - `github.com/gotd/td` for Telegram MTProto user-session interactions.
  - `go.mongodb.org/mongo-driver` for MongoDB access.
  - `github.com/sirupsen/logrus` for structured logging.
  - `go.uber.org/zap` as the logger type required by gotd; the client currently uses a no-op zap logger while app logs stay in logrus.
- **Infrastructure**: MongoDB for persistence; Docker Compose (`docker-compose.local.yml`) for local development and integration.
- **Configuration management**: `internal/config` loads Telegram API credentials, encrypted session settings, userbot owner, Mongo URI/DB, and optional `APP_ENV`/`LOG_LEVEL`.
