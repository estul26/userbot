# Error Handling Guidelines

## Goals
- Keep Telegram handlers predictable: fail fast on unrecoverable issues, degrade gracefully on transient/user-caused errors.
- Use consistent log levels and context fields so incidents can be triaged quickly without leaking secrets.

## Return vs. Handle Locally
- Return upward: startup/config validation failures; Mongo connection or write errors; outbound Telegram API failures that prevent replying; upstream provider errors after final retry; broken invariants (nil contexts, missing required IDs).
- Handle locally: user input/command errors (reply with help and continue); idempotent duplicates or already-processed updates; transient dependency blips when a retry/fallback succeeds and state is consistent.
- Wrap errors with operation context (`fmt.Errorf("describe operation: %w", err)`) and avoid panics outside process boot.

## Logging Levels
- error: user-visible failure, dropped update, data loss risk, dependency unreachable after retries, startup/config invalid; include `err` and operation fields.
- warn: degraded but recovered paths (retry scheduled, fallback used), malformed user payloads we safely ignore, rate limiting/backoff events.
- info: expected control flow (start/stop), handled user mistakes with a reply, successful retry attempts, periodic health logs.

## Standard Context Fields
- Always include: `user_id`, `chat_id`, `event` (command/update type) via `logging.WithContext`.
- Add when available: `update_id`, `command`, `provider`, `endpoint`, `attempt`, `request_id/trace_id`.
- Never log secrets or raw tokens; prefer high-level identifiers (host names, order IDs).

## Classification Examples
- Network timeout to upstream provider: log `warn` with attempt/endpoint and schedule a retry; escalate to `error` and return when retries exhausted or user-facing action fails.
- User sent invalid command: handle locally, reply with guidance; log `info` (`event=command_invalid`, `command=<sanitized>`).
- Mongo unavailable: treat as fatal; log `error` with operation/host, return the error to abort the handler or fail startup.
