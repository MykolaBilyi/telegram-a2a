# Telegram agent gateway

[![xc compatible](https://xcfile.dev/badge.svg)](https://xcfile.dev)

Lightweight Go gateway between Telegram (long polling) and an A2A-compatible agent.

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
| `A2A_URL` | `a2a.url` | Full URL to the A2A agent endpoint (required) |
| `TELEGRAM_ALLOWED_USERS` | `telegram.allowedUsers` | Comma-separated list of allowed Telegram user IDs (empty = allow all) |
| `A2A_TIMEOUT` | `a2a.timeout` | HTTP timeout for A2A requests (default `120s`) |
| `LOG_LEVEL` | `logger.level` | Log level (`debug`, `info`, `warn`, `error`) |

## Bot Commands

- `/start` — Display welcome message and available commands
- `/new` — Reset your conversation `contextId` and start a fresh session
- `/status` — Check connectivity to the A2A agent and display active `contextId`

## Tasks

### test

Run the full test suite with race detection.

```sh
go test -v -race ./...
```

### build

Build the application binary.

```sh
go build -o app ./cmd/app/main.go
```

### run

Run the service locally. Requires `TELEGRAM_BOT_TOKEN` and `A2A_URL` environment variables.

```sh
go run cmd/app/main.go
```

### docker-build

Build the Docker image locally.

```sh
docker build -t telegram-a2a:latest .
```

### lint

Run linters.

```sh
golangci-lint run
```
