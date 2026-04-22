# Telegram Userbot Foundation 

Go MTProto userbot foundation. It logs in as a Telegram user account with `gotd/td`, stores the MTProto session in an encrypted local file, tracks seen users/groups in MongoDB, and exposes owner-only self commands.

## What's inside
- Environment config loader with validation (`internal/config`).
- Structured logging setup using logrus (`internal/logging`).
- MongoDB manager with base collections/indexes (`internal/store`).
- MTProto userbot client with encrypted session storage and `.ping`/`.status` owner commands (`internal/userbot`).
- User/group/owner registration hooks (`internal/feature`).

## Configuration
Required env vars:
- `TELEGRAM_API_ID`: Telegram app API ID from `my.telegram.org`.
- `TELEGRAM_API_HASH`: Telegram app API hash from `my.telegram.org`.
- `TELEGRAM_PHONE`: Telegram account phone number in international format.
- `TELEGRAM_SESSION_PATH`: local encrypted session file path.
- `TELEGRAM_SESSION_PASSPHRASE`: passphrase used to encrypt the session file.
- `USERBOT_OWNER_ID`: Telegram user ID that must match the authenticated account.
- `MONGO_URI`, `MONGO_DB`.

Optional env vars: `APP_ENV` (`production` default, `development` enables `.env`), `LOG_LEVEL`, and `MIRROR_BOT_MESSAGES`.

## Local development
- Start MongoDB: `docker compose -f docker-compose.local.yml up -d mongo`.
- If running the `app` service with Docker Compose, set `REPOSITORY_NAME` to your repository name so the local image, container, and session filename match production.
- First login: `go run ./cmd/bot -login` and enter the Telegram login code when prompted.
- Run normally: `go run ./cmd/bot`.
- Verify config only: `go run ./cmd/bot -config-only`.

Owner commands are sent from your own Telegram account in any chat:
- `.ping`
- `.status`

## Bot message mirroring
Set `MIRROR_BOT_MESSAGES=true` to repost incoming bot-authored text messages in group/supergroup chats.

Behavior:
- Only mirrors messages where the sender is a Telegram bot.
- Only mirrors group/supergroup messages.
- Ignores your own outgoing messages, so the userbot does not mirror itself.
- Mirrors text only; media/captions are not copied in this version.

For production, add a GitHub repository variable:

```text
MIRROR_BOT_MESSAGES=true
```

## Production one-time login
Production deploys do not perform Telegram login automatically. Login is a one-time manual step on the VPS that creates an encrypted session file. The normal production container then reuses that file.

### 1. Prepare the VPS session directory
SSH into the VPS and create a persistent directory for the encrypted Telegram session:

```sh
sudo mkdir -p /var/lib/YOUR_REPO
sudo chown "$(id -u):$(id -g)" /var/lib/YOUR_REPO
chmod 700 /var/lib/YOUR_REPO
```

Run those commands manually on the VPS. The GitHub deploy workflow does not run `sudo` because SSH deploys are non-interactive and cannot answer a sudo password prompt.

The deploy workflow expects this file to exist after login:

```sh
/var/lib/YOUR_REPO/YOUR_REPO.session.enc
```

### 2. Run interactive login once
Use a production image that already exists in GHCR and run it with `-login`. Replace the placeholder values before running.

The release workflow publishes a commit SHA tag every time. On successful releases from `main`, it also publishes `main` and `latest` tags. If `latest` does not exist yet, use `main` or the commit SHA tag from the successful release:

```sh
docker run --rm -it \
  --user "$(id -u):$(id -g)" \
  -v /var/lib/YOUR_REPO:/data \
  -e APP_ENV=production \
  -e TELEGRAM_API_ID="YOUR_API_ID" \
  -e TELEGRAM_API_HASH="YOUR_API_HASH" \
  -e TELEGRAM_PHONE="+YOUR_PHONE" \
  -e TELEGRAM_SESSION_PATH="/data/YOUR_REPO.session.enc" \
  -e TELEGRAM_SESSION_PASSPHRASE="YOUR_LONG_PASSPHRASE" \
  -e USERBOT_OWNER_ID="YOUR_TELEGRAM_USER_ID" \
  -e MONGO_URI="YOUR_PRODUCTION_MONGO_URI" \
  -e MONGO_DB="tg_bot" \
  ghcr.io/YOUR_GITHUB_USERNAME/YOUR_REPO:main \
  -login
```

Telegram will ask for the login code and, if enabled, the account 2FA password. After success, verify the session file exists:

```sh
ls -l /var/lib/YOUR_REPO/YOUR_REPO.session.enc
```

### 3. Configure GitHub deploy secrets
Set these repository secrets in GitHub:

- `VPS_HOST`
- `VPS_USER`
- `SSH_KEY`
- `VPS_PORT`
- `TELEGRAM_API_ID`
- `TELEGRAM_API_HASH`
- `TELEGRAM_PHONE`
- `TELEGRAM_SESSION_PASSPHRASE`
- `USERBOT_OWNER_ID`
- `MONGO_URI`

`TELEGRAM_SESSION_PASSPHRASE` must be exactly the same value used during the one-time login. If it changes, the existing session file cannot be decrypted.

Optional repository variables:

- `MONGO_DB`, default is the repository name.
- `LOG_LEVEL`, default is `info`.
- `MIRROR_BOT_MESSAGES`, default is `false`.
- `USERBOT_SESSION_DIR`, default is `/var/lib/<repository-name>`.
- `USERBOT_SESSION_FILE`, default is `<repository-name>.session.enc`.

### 4. Automatic production deploy
After the one-time login is complete, pushing to `main` triggers:

```text
CI -> Release -> Production Deploy
```

The deploy workflow pulls the GHCR image, mounts `/var/lib/<repository-name>` into the container as `/data`, and starts the userbot without `-login`. If `/var/lib/<repository-name>/<repository-name>.session.enc` is missing, deploy fails intentionally so production does not start unauthenticated.

## Notes
- This is not a BotFather Bot API bot. It uses MTProto and acts as the configured user account.
- Keep the encrypted session file and passphrase private. Either one alone is not enough; together they grant account access.
- Automation beyond self-only commands should be added carefully to avoid account-risk and policy issues.
