# Architecture Notes

## File Map
- `design-document.md`: High-level intent and scope for the MTProto userbot foundation.
- `tech-stack.md`: Stack choices (Go 1.25, gotd/td, mongo-driver, logrus, Docker Compose).
- `architecture.md`: This document; captures repository structure notes and database schema status.
- `error-handling-guidelines.md`: Error propagation and logging conventions for handlers and services.
- `README.md`: Quickstart, local development steps, and reference links.

## Runtime Configuration
- Config loader validates Telegram MTProto settings: `TELEGRAM_API_ID`, `TELEGRAM_API_HASH`, `TELEGRAM_PHONE`, `TELEGRAM_SESSION_PATH`, `TELEGRAM_SESSION_PASSPHRASE`, and `USERBOT_OWNER_ID`.
- Mongo settings remain `MONGO_URI` and `MONGO_DB`; optional `APP_ENV` defaults to production and only development loads `.env`.
- `MIRROR_BOT_MESSAGES=true` enables reposting incoming bot-authored messages seen in group/supergroup chats when their text/caption contains an order-number candidate; `MIRROR_ORDER_PATTERN` can override the default candidate regex.
- Configuration dry-run is supported via `-config-only` and redacts API hash, session passphrase, Mongo credentials, and most of the phone number.
- Structured app logging uses logrus JSON with `service=telegram-userbot` and `env`.

## MongoDB Client Management
- `internal/store.Manager` owns one Mongo client, pings on startup, exposes `users` and `groups`, and ensures unique indexes.
- `users.user_id` and `groups.chat_id` remain the base unique indexes.

## Domain Models
- Users are represented by `domain.User` with `user_id`, `role`, timestamps, and `last_seen_at`.
- Groups are represented by `domain.Group` with `chat_id`, `title`, `joined_at`, and `last_seen_at`.
- Startup bootstraps exactly one configured owner from `USERBOT_OWNER_ID`; previous owners are demoted to admin.

## MTProto Userbot Client
- `internal/userbot` uses `github.com/gotd/td` instead of the Bot API.
- `-login` runs an interactive user auth flow and writes the gotd session bytes to `TELEGRAM_SESSION_PATH`.
- Session storage is a local AES-GCM encrypted JSON file with a scrypt-derived key and `0600` file permissions.
- Normal mode preflights that the encrypted session exists, then starts only if the session is authorized and the authenticated Telegram user ID matches `USERBOT_OWNER_ID`.
- Update handling uses gotd's updates manager with in-memory recovery state, ignores historical backlog from before process start, registers seen users and group/supergroup chats, logs update metadata, optionally mirrors order-bearing incoming bot-authored group messages, and handles owner-sent `.ping` and `.status` commands.

## Commands
- `.ping`: owner-only self command; replies with `pong`, env, uptime, and Mongo health.
- `.status`: owner-only self command; replies with running status, env, uptime, Mongo health, registered user count, and known chat count.
- Commands are recognized only from outgoing messages sent by the authenticated owner account.

## Bot Message Mirroring
- When enabled, incoming messages from Telegram bot accounts in groups/supergroups are reposted as the authenticated user in the same chat only if text/caption contains an order-number candidate.
- The default order candidate regex is `\b[A-Za-z0-9][A-Za-z0-9_-]{9,}\b`, so candidates must be at least 10 characters; matched candidates must contain at least one digit and obvious date tokens are ignored.
- Text mirrors preserve the original reply target when Telegram provides one; media mirrors are forwarded/copied back into the group.
- Mirroring ignores historical messages, outgoing messages, private messages, human senders, messages without an order candidate, and empty text/media to reduce accidental loops.

## Shutdown Flow
- Process listens for `SIGINT`/`SIGTERM`, cancels the userbot context, waits up to 10s for shutdown, then closes Mongo with a 5s timeout.

## Local Development Stack
- `docker-compose.local.yml` provides MongoDB 6.0 and a `userbot` service.
- The Compose `app` service expects MTProto env vars and mounts `./data` at `/data` for the encrypted session file.
- First login should be run interactively outside detached compose or with an attached container session.

## Containerization
- The Dockerfile builds a static `/app/app` binary from `./cmd/bot` and runs it in a distroless nonroot runtime.
- Runtime configuration is entirely environment-driven; the encrypted session path should point to a writable local or mounted path.
