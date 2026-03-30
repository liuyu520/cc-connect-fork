---
name: session-key-architecture
description: >
  This skill should be used when the user asks about "session key", "SessionKey",
  "thread isolation", "thread_isolation", "share_session_in_channel",
  "BaseSessionKeyer", "base session key", "session binding",
  "different threads different sessions", "session key format",
  "per-user session", "per-channel session", or needs to debug, extend, or
  understand how session keys are constructed across platforms and how they
  affect routing, project binding, and conversation isolation.
---

# Session Key Architecture

## Purpose

Session keys are the primary mechanism for mapping incoming messages to
conversations, agent sessions, and project bindings. Each platform constructs
session keys differently based on its chat model. Understanding session key
formats is critical when debugging routing issues, implementing thread isolation,
or extending the ProjectRouter binding system.

## Core Concept

A session key is a string that uniquely identifies a "conversation context."
All cc-connect subsystems use it:

| Subsystem | Usage |
|-----------|-------|
| `Engine.handleMessage()` | Maps message to interactive state / agent session |
| `SessionManager` | Persists session history per session key |
| `ProjectRouter` | Binds session key → project name |
| Rate limiter | Tracks per-session message counts |

## Session Key Formats by Platform

| Platform | Default Format | share_session | thread_isolation |
|----------|---------------|---------------|------------------|
| Feishu/Lark | `feishu:{chatID}:{userID}` | `feishu:{chatID}` | `feishu:{chatID}:root:{rootMsgID}` |
| Telegram | `telegram:{chatID}:{userID}` | `telegram:{chatID}` | N/A |
| Discord | `discord:{channelID}:{userID}` | `discord:{channelID}` | `discord:{threadID}` |
| Slack | `slack:{channel}:{user}` | `slack:{channel}` | N/A |
| DingTalk | `dingtalk:{convID}:{staffID}` | `dingtalk:{convID}` | N/A |
| WeChat Work | `wecom:{userID}` | N/A | N/A |
| QQ (OneBot) | `qq:{groupID}:{userID}` / `qq:{userID}` | `qq:g:{groupID}` | N/A |
| LINE | `line:{targetID}` | N/A | N/A |

For the complete format details with code references, consult
**`references/platform-session-keys.md`**.

## Thread Isolation and BaseSessionKeyer

### The Problem

When `thread_isolation` is enabled (Feishu, Discord), each top-level message
creates a unique session key containing the root message ID. This causes:

- `/project` switches to apply only to the current thread
- New threads to require re-selection or fall back to previous bindings
- User confusion when project context doesn't persist across threads

### The Solution: BaseSessionKeyer

`BaseSessionKeyer` is an optional interface in `core/interfaces.go`:

```go
type BaseSessionKeyer interface {
    BaseSessionKey(msg *Message) string
}
```

Platforms implement this to return a broader "user-in-chat" level key,
stripping thread-specific components. ProjectRouter uses it for fallback
binding lookup.

### Binding Lookup Order

```
1. bindings[msg.SessionKey]          → exact match (per-thread)
2. bindings[baseSessionKey(msg)]     → fallback (per-user-in-chat)
3. No binding found                  → show project selection
```

### Binding Storage

When a project is selected or switched, `setBinding()` stores both:
- `bindings[sessionKey] = project` — exact thread binding
- `bindings[baseKey] = project` — broader fallback binding

This ensures new threads inherit the most recent project selection.

### Feishu Implementation

```go
// BaseSessionKey derives user-in-chat key from thread-isolated key.
// "feishu:chatId:root:msgId" → "feishu:chatId:userId"
func (p *Platform) BaseSessionKey(msg *core.Message) string {
    if !p.threadIsolation { return msg.SessionKey }
    parts := strings.SplitN(msg.SessionKey, ":", 3)
    if len(parts) < 3 { return msg.SessionKey }
    if _, ok := parseThreadRootID(parts[2]); !ok { return msg.SessionKey }
    return fmt.Sprintf("%s:%s:%s", p.tag(), parts[1], msg.UserID)
}
```

## Key Files

| File | Role |
|------|------|
| `core/interfaces.go` | `BaseSessionKeyer` interface definition |
| `core/project_router.go` | `baseSessionKey()`, `setBinding()`, fallback lookup |
| `platform/feishu/feishu.go` | `makeSessionKey()`, `BaseSessionKey()`, `isThreadSessionKey()` |
| `platform/discord/discord.go` | Discord session key construction |

## Adding BaseSessionKeyer to a New Platform

1. Implement `BaseSessionKey(msg *Message) string` on the Platform struct
2. Return a broader key (strip thread/topic-specific components, keep chat + user)
3. Return `msg.SessionKey` unchanged when thread isolation is not active
4. Add tests verifying the base key derivation

## Testing

```bash
# Session key related tests
go test ./core/ -v -run "TestProjectRouter_BaseSessionKey"
go test ./platform/feishu/ -v -run "TestLark_SessionKey|TestLark_ThreadIsolation"
```

Test stubs for BaseSessionKeyer:
```go
type stubBaseSessionKeyPlatform struct {
    stubRouterPlatform
    baseKeyFunc func(msg *Message) string
}
func (p *stubBaseSessionKeyPlatform) BaseSessionKey(msg *Message) string { ... }
```

## Debugging Session Key Issues

Common symptoms and diagnosis:

| Symptom | Likely Cause |
|---------|-------------|
| `/project` switch doesn't persist to new messages | thread_isolation + missing BaseSessionKeyer |
| Different users share the same session | `share_session_in_channel` enabled |
| Reply in thread creates new session | thread_isolation splits per-root-message |
| Project selection keeps appearing | No binding for this session key + no base key fallback |

Diagnostic steps:
1. Check logs for `session=` field to see the actual session key format
2. Verify platform config options (`thread_isolation`, `share_session_in_channel`)
3. Inspect persisted bindings file: `{dataDir}/project_bindings_*.json`

## Additional Resources

### Reference Files

- **`references/platform-session-keys.md`** — Complete session key construction code for each platform with line references

### Related Skills

- **`message-flow-architecture`** — How session keys feed into the Engine message processing pipeline
- **`project-router`** — How session keys map to project bindings
