# Platform Session Key Construction — Complete Reference

## Feishu / Lark

**File:** `platform/feishu/feishu.go`

### Configuration Options

| Option | Type | Default | Effect |
|--------|------|---------|--------|
| `thread_isolation` | bool | false | Each thread gets its own session key |
| `share_session_in_channel` | bool | false | All users in a channel share one key |

### Session Key Construction (`makeSessionKey`)

```go
func (p *Platform) makeSessionKey(msg *larkim.EventMessage, chatID, userID string) string {
    // Priority 1: Thread isolation (group chats only)
    if p.threadIsolation && msg != nil && stringValue(msg.ChatType) == "group" {
        rootID := stringValue(msg.RootId)
        if rootID == "" {
            rootID = stringValue(msg.MessageId) // top-level message = its own root
        }
        if rootID != "" {
            return fmt.Sprintf("%s:%s:root:%s", p.tag(), chatID, rootID)
        }
    }
    // Priority 2: Channel-shared session
    if p.shareSessionInChannel {
        return fmt.Sprintf("%s:%s", p.tag(), chatID)
    }
    // Default: Per-user per-chat
    return fmt.Sprintf("%s:%s:%s", p.tag(), chatID, userID)
}
```

### Card Action Session Key (`sessionKeyFromCardAction`)

Card callbacks can carry a `session_key` in the action value payload:
```go
func (p *Platform) sessionKeyFromCardAction(chatID, userID string, value map[string]any) string {
    if value != nil {
        if sessionKey, _ := value["session_key"].(string); sessionKey != "" {
            return sessionKey  // Use embedded session key from card action
        }
    }
    // Fallback: same logic as share_session / per-user
    ...
}
```

### BaseSessionKeyer Implementation

```go
func (p *Platform) BaseSessionKey(msg *core.Message) string {
    if !p.threadIsolation {
        return msg.SessionKey
    }
    parts := strings.SplitN(msg.SessionKey, ":", 3)
    if len(parts) < 3 {
        return msg.SessionKey
    }
    if _, ok := parseThreadRootID(parts[2]); !ok {
        return msg.SessionKey
    }
    chatID := parts[1]
    return fmt.Sprintf("%s:%s:%s", p.tag(), chatID, msg.UserID)
}
```

### Thread Detection

```go
func isThreadSessionKey(sessionKey string) bool {
    parts := strings.SplitN(sessionKey, ":", 3)
    if len(parts) != 3 { return false }
    _, ok := parseThreadRootID(parts[2])
    return ok
}

func parseThreadRootID(sessionTail string) (string, bool) {
    for _, prefix := range []string{"root:", "thread:"} {
        if strings.HasPrefix(sessionTail, prefix) {
            rootID := strings.TrimPrefix(sessionTail, prefix)
            if rootID != "" { return rootID, true }
        }
    }
    return "", false
}
```

### Platform Name Tag

`p.tag()` returns `"feishu"` or `"lark"` depending on `lark_mode` config.

---

## Telegram

**File:** `platform/telegram/telegram.go`

### Configuration Options

| Option | Type | Default | Effect |
|--------|------|---------|--------|
| `share_session_in_channel` | bool | false | All users in group share one key |

### Session Key Construction

```go
// Per-user per-chat (default)
sessionKey = fmt.Sprintf("telegram:%d:%d", chatID, userID)

// Channel-shared
sessionKey = fmt.Sprintf("telegram:%d", chatID)
```

No thread isolation support — Telegram threads don't create separate session keys.

---

## Discord

**File:** `platform/discord/discord.go`

### Configuration Options

| Option | Type | Default | Effect |
|--------|------|---------|--------|
| `thread_isolation` | bool | false | Thread messages use thread channel ID |
| `share_session_in_channel` | bool | false | All users in channel share one key |

### Session Key Construction

```go
// Thread isolation: use thread's channel ID
if p.threadIsolation && isThread {
    sessionKey = fmt.Sprintf("discord:%s", threadChannelID)
}

// Channel-shared
sessionKey = fmt.Sprintf("discord:%s", channelID)

// Default: per-user per-channel
sessionKey = fmt.Sprintf("discord:%s:%s", channelID, userID)
```

Note: Discord threads are separate channels, so thread isolation uses the
thread's own channel ID directly (no `:root:` pattern like Feishu).

---

## Slack

**File:** `platform/slack/slack.go`

### Configuration Options

| Option | Type | Default | Effect |
|--------|------|---------|--------|
| `share_session_in_channel` | bool | false | All users in channel share one key |

### Session Key Construction

```go
// Channel-shared
sessionKey = fmt.Sprintf("slack:%s", channel)

// Default: per-user per-channel
sessionKey = fmt.Sprintf("slack:%s:%s", channel, user)
```

---

## DingTalk

**File:** `platform/dingtalk/dingtalk.go`

### Configuration Options

| Option | Type | Default | Effect |
|--------|------|---------|--------|
| `share_session_in_channel` | bool | false | All users in conversation share one key |

### Session Key Construction

```go
// Channel-shared
sessionKey = fmt.Sprintf("dingtalk:%s", conversationId)

// Default: per-user per-conversation
sessionKey = fmt.Sprintf("dingtalk:%s:%s", conversationId, senderStaffId)
```

---

## WeChat Work (WeCom)

**File:** `platform/wecom/wecom.go`

### Session Key Construction

```go
// Always per-user (no group chat concept in WeCom API)
sessionKey = fmt.Sprintf("wecom:%s", userID)
```

No `share_session_in_channel` or `thread_isolation` support.

---

## QQ (OneBot)

**File:** `platform/qq/qq.go`

### Configuration Options

| Option | Type | Default | Effect |
|--------|------|---------|--------|
| `share_session_in_channel` | bool | false | Group messages use group-only key |

### Session Key Construction

```go
// Private message
sessionKey = fmt.Sprintf("qq:%d", userID)

// Group message, channel-shared
sessionKey = fmt.Sprintf("qq:g:%d", groupID)

// Group message, per-user (default)
sessionKey = fmt.Sprintf("qq:%d:%d", groupID, userID)
```

---

## QQ Bot (Official API)

**File:** `platform/qqbot/qqbot.go`

### Session Key Construction

Uses `groupOpenID` and `userOpenID` from the official QQ Bot API.

---

## LINE

**File:** `platform/line/line.go`

### Session Key Construction

```go
// Uses target ID (user, group, or room)
sessionKey = fmt.Sprintf("line:%s", targetID)
```

No per-user isolation in groups — all users in a group/room share one session key.

---

## Common Patterns

### Session Key Parsing

To extract components from a session key:
```go
parts := strings.SplitN(sessionKey, ":", 3)
// parts[0] = platform name ("feishu", "telegram", etc.)
// parts[1] = primary ID (chatID, channelID, etc.)
// parts[2] = remaining (userID, "root:msgId", etc.)
```

### Engine-Level Session Key Utilities

`core/engine.go` provides helpers:
```go
// extractChannelID extracts the channel/chat portion from a session key
func extractChannelID(sessionKey string) string

// extractWorkspaceChannelKey extracts a workspace-scoped channel key
func extractWorkspaceChannelKey(sessionKey string) string
```

### Impact on ProjectRouter

Session key format directly affects ProjectRouter behavior:

1. **Per-user keys** (default) — `/project` switch is per-user-per-chat, works as expected
2. **Channel-shared keys** — `/project` switch affects all users in the channel
3. **Thread-isolated keys** — `/project` switch only affects the current thread;
   `BaseSessionKeyer` provides fallback to make switches sticky across threads
