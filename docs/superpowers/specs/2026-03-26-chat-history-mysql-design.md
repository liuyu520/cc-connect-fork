# Chat History MySQL Persistence Design

## Overview

Add MySQL persistence for chat history alongside the existing JSON file storage (dual-write strategy). MySQL becomes an append-only log of all conversations; JSON files continue to serve as the runtime source of truth.

## Database

- MySQL 8, DSN configured in `config.toml` under `[database]`
- Tables auto-created on startup via `CREATE TABLE IF NOT EXISTS`
- If MySQL is unavailable, service degrades gracefully to JSON-only mode

## Table Schema

### cc_sessions

Mirrors the existing `core.Session` struct.

| Column | Type | Description |
|--------|------|-------------|
| id | BIGINT UNSIGNED AUTO_INCREMENT | PK |
| session_id | VARCHAR(64) UNIQUE | cc-connect internal session ID |
| session_key | VARCHAR(255) | user context key (e.g. `feishu:{chatID}:{userID}`) |
| project | VARCHAR(128) | project name |
| agent_type | VARCHAR(64) | agent type (claudecode, codex, etc.) |
| agent_session_id | VARCHAR(128) | agent-side session ID |
| name | VARCHAR(255) | session display name |
| created_at | DATETIME(3) | creation timestamp |
| updated_at | DATETIME(3) | last update timestamp |

### cc_chat_messages

Extends the existing `core.HistoryEntry` with platform/user metadata.

| Column | Type | Description |
|--------|------|-------------|
| id | BIGINT UNSIGNED AUTO_INCREMENT | PK |
| session_id | VARCHAR(64) | FK to cc_sessions.session_id |
| role | VARCHAR(16) | "user" or "assistant" |
| content | LONGTEXT | message content |
| platform | VARCHAR(32) | source platform |
| user_id | VARCHAR(128) | platform user ID |
| user_name | VARCHAR(128) | display name |
| message_id | VARCHAR(128) | platform message ID |
| created_at | DATETIME(3) | timestamp |

## Architecture

### New Files

- `core/chatstore.go` — `ChatStore` interface + `ChatMessage`/`SessionInfo` types
- `core/chatstore_mysql.go` — MySQL implementation with async write, auto-DDL

### Interface

```go
type ChatStore interface {
    SaveMessage(ctx context.Context, msg ChatMessage) error
    EnsureSession(ctx context.Context, info SessionInfo) error
    Close() error
}
```

### Integration Points

The `Engine` holds an optional `chatStore ChatStore` field. When non-nil, it calls:

1. `EnsureSession` — when a session is first used in a turn
2. `SaveMessage(role="user")` — at `engine.go:1824` (user message received)
3. `SaveMessage(role="assistant")` — at `engine.go:2489` (EventResult)
4. Same for queued message paths at `engine.go:2640` and `engine.go:2695`

### Write Strategy

- Async goroutine per write, non-blocking
- Failures logged via `slog.Warn`, never propagated to user
- JSON file persistence continues unchanged (dual-write)

### Configuration

```toml
[database]
dsn = "user:pass@tcp(host:port)/dbname?charset=utf8mb4&parseTime=true"
max_open_conns = 10
max_idle_conns = 5
```

When `[database]` is absent, `chatStore` remains nil and no MySQL code executes.

### Config Struct Addition

```go
type DatabaseConfig struct {
    DSN          string `toml:"dsn"`
    MaxOpenConns int    `toml:"max_open_conns"`
    MaxIdleConns int    `toml:"max_idle_conns"`
}
```

## Graceful Degradation

| Scenario | Behavior |
|----------|----------|
| No `[database]` in config | chatStore = nil, pure JSON mode |
| MySQL unreachable at startup | Log error, chatStore = nil |
| MySQL write fails at runtime | Log warning, continue normally |
| MySQL recovers after failure | Next write succeeds automatically (connection pool handles reconnect) |
