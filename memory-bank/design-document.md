# Telegram Userbot Foundation Design Document

## Intent
- Provide a minimal MTProto userbot foundation: config loading/validation, encrypted local session storage, structured logging, MongoDB persistence, and owner-only command handling.
- Keep the first version conservative: the authenticated user account can issue self commands, while broader automation is left for later explicit features.
- Keep local development simple with Docker Compose and clear environment contracts.

## Scope
- Included: environment config loader with validation and `-config-only` dry run; `-login` interactive Telegram user login; AES-GCM encrypted session file storage; logrus-based app logging; Mongo client manager with base indexes; MTProto update handling; user/group registration; owner-only `.ping` and `.status`; optional group bot-message mirroring; graceful shutdown.

## Architecture Overview
- `cmd/bot`: process bootstrap, config load, Mongo manager wiring, userbot client start/shutdown.
- `internal/config`: env contract, `.env` load in development, validation, dry-run output with masking.
- `internal/logging`: logrus setup with JSON formatter, standard fields, helpers for contextual logs.
- `internal/store`: Mongo client lifecycle, base collections (`users`, `groups`), and index creation.
- `internal/domain`: user/group domain models and role helpers.
- `internal/feature/user|group|owner`: registrars for user/group upserts and owner bootstrap.
- `internal/userbot`: gotd MTProto client setup, encrypted session storage, auth prompts, update routing, and owner commands.

## Data Model (MongoDB)
- `users`: `user_id` (unique), `role` (`owner|admin|user`), `created_at`, `updated_at`, `last_seen_at`.
- `groups`: `chat_id` (unique), `title`, `joined_at`, `last_seen_at`.
- Unique indexes: `users.user_id`, `groups.chat_id`.

## Operational Notes
- Required env: `TELEGRAM_API_ID`, `TELEGRAM_API_HASH`, `TELEGRAM_PHONE`, `TELEGRAM_SESSION_PATH`, `TELEGRAM_SESSION_PASSPHRASE`, `USERBOT_OWNER_ID`, `MONGO_URI`, `MONGO_DB`; optional `APP_ENV`, `LOG_LEVEL`, and `MIRROR_BOT_MESSAGES`.
- First run uses `go run ./cmd/bot -login` to create the encrypted session. Normal runs refuse to start if the session is missing or unauthorized.
- Local stack: `docker compose -f docker-compose.local.yml up -d mongo` for Mongo; run the userbot with `go run ./cmd/bot`.
- CI: `go fmt ./...`, `go test ./...`, `go build ./cmd/bot`.

## Extensibility Guidelines
- Add new features under `internal/feature/<name>` and register them through `internal/userbot`.
- Keep external calls behind interfaces to enable testing without live Telegram or MongoDB dependencies.
- Avoid broad automation until permissions, rate limits, and account-risk controls are explicitly designed.
