# telegram-a2a

Lightweight Go gateway between Telegram (long polling) and an A2A-compatible agent (such as [kagent](https://kagent.dev)).

## Features

- **A2A JSON-RPC 2.0 (`message/send`)**: Forwards Telegram messages to your agent and maintains per-user conversation continuity via `contextId`.
- **Human-in-the-Loop (HITL) Approvals**: Handles `input-required` responses and `adk_request_confirmation` parts by presenting `[ Approve ]` / `[ Reject ]` or choice inline keyboard buttons in Telegram.
- **Access Control**: Optional allowlist of Telegram user IDs (`TELEGRAM_ALLOWED_USERS`).
- **Automatic Chunking**: Splits long agent responses into 4000-character messages to respect Telegram's 4096-character limit.
- **Composite Health Check (`/health`)**: Actively verifies both the Telegram Bot API (`getMe`) and A2A agent (`/.well-known/agent.json`) connections.

## Configuration

Configuration is loaded from `configs/default.yml` and can be overridden with environment variables:

| Environment Variable | Config Key | Description |
|---|---|---|
| `TELEGRAM_BOT_TOKEN` | `telegram.botToken` | Telegram Bot token from `@BotFather` (required) |
| `KAGENT_A2A_URL` / `A2A_URL` | `a2a.url` | Full URL to the A2A agent endpoint (required) |
| `TELEGRAM_ALLOWED_USERS` | `telegram.allowedUsers` | Comma-separated list of allowed Telegram user IDs (empty = allow all) |
| `A2A_TIMEOUT` | `a2a.timeout` | HTTP timeout for A2A requests (default `120s`) |
| `LOG_LEVEL` | `logger.level` | Log level (`debug`, `info`, `warn`, `error`) |

## Bot Commands

- `/start` — Display welcome message and available commands
- `/new` — Reset your conversation `contextId` and start a fresh session
- `/status` — Check connectivity to the A2A agent and display active `contextId`

## Run Service

```sh
export TELEGRAM_BOT_TOKEN="your-telegram-token"
export KAGENT_A2A_URL="http://kagent-controller.kagent.svc.cluster.local:8083/api/a2a/kagent/telegram-k8s-agent/"
go run cmd/app/main.go
```

## Run Tests

```sh
go test -v -race ./...
```
