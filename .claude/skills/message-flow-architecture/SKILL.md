---
name: message-flow-architecture
description: >
  This skill should be used when the user asks about "message flow",
  "how messages are processed", "Engine handleMessage", "interactive session",
  "agent session lifecycle", "processInteractiveEvents", "event loop",
  "how cc-connect talks to Claude Code", "stdio protocol", "stream-json",
  "permission request flow", "streaming preview", "how typing indicator works",
  "pending messages queue", "session lock", "TryLock", "how API key is passed",
  "provider proxy", "ANTHROPIC_API_KEY", "router_url", "authentication",
  "how Claude Code is started", or needs to debug, extend, or understand the
  end-to-end message processing pipeline from IM platform to agent execution.
---

# Message Flow Architecture

## Purpose

Document the complete data flow from when a user sends a message in an IM
platform to when Claude Code executes coding tasks and the response is delivered
back. This is the core pipeline of cc-connect.

## End-to-End Overview

```
IM User → Platform → [ProjectRouter] → Engine.handleMessage()
  → processInteractiveMessageWith()
    → getOrCreateInteractiveStateWith() → agent.StartSession()
    → agentSession.Send() → stdin JSON to claude CLI
    → processInteractiveEvents() ← stdout JSON from claude CLI
      → EventThinking/ToolUse/Text/Permission/Result → Platform.Reply()
  → User receives response
```

## Key Files

| File | Role |
|------|------|
| `core/engine.go` | `handleMessage()` main entry, Engine struct, lifecycle, `resolveAlias()`, `checkRateLimit()`, `matchBannedWord()` |
| `core/engine_session.go` | `processInteractiveMessage()`, `processInteractiveMessageWith()`, `getOrCreateInteractiveStateWith()`, `handlePendingPermission()`, `queueMessageForBusySession()`, `cleanupInteractiveState()`, type definitions (`interactiveState`, `pendingPermission`, `queuedMessage`) |
| `core/engine_events.go` | `processInteractiveEvents()` — the core event select loop (~600 lines), `drainPendingMessages()`, `notifyDroppedQueuedMessages()` |
| `core/engine_commands.go` | `handleCommand()` — slash command dispatch via registry pattern |
| `core/engine_util.go` | `send()`, `reply()`, `sendPermissionPrompt()`, `sendAskQuestionPrompt()`, `HandleRelay()`, `splitMessage()` |
| `core/engine_cards.go` | `handleCardNav()`, `executeCardAction()`, all `render*Card()` functions |
| `core/engine_workspace.go` | `buildSenderPrompt()`, `commandContext()`, `resolveWorkspace()`, workspace helpers |
| `core/interfaces.go` | `Platform`, `Agent`, `AgentSession`, `SessionEnvInjector` interfaces |
| `core/streaming.go` | `streamPreview` for real-time message updates |
| `agent/claudecode/claudecode.go` | Agent constructor, `StartSession()`, provider/router env injection |
| `agent/claudecode/session.go` | CLI subprocess management, stdin/stdout JSON protocol, event parsing |
| `core/providerproxy.go` | Local reverse proxy for third-party providers |

## Phase 1: Message Preprocessing

`Engine.handleMessage()` (in `core/engine.go`) performs these checks before agent dispatch:

1. Voice message transcription (STT)
2. Alias resolution (`e.resolveAlias()`)
3. Rate limiting (`e.checkRateLimit()`)
4. Banned word filtering
5. Multi-workspace resolution (if enabled)
6. Slash command interception (`/status`, `/new`, `/model`, etc.) — routed to `handleCommand()` in `engine_commands.go`

If not a command, the message proceeds to interactive processing.

## Phase 2: Session Locking

```
session.TryLock()
  ├─ Success (idle)     → go processInteractiveMessageWith()
  ├─ Fail + "/btw"     → inject into running session (no new turn)
  ├─ Fail + queueable  → pendingMessages queue (auto-processed after current turn)
  └─ Fail + full       → reply "previous request still processing"
```

The `pendingMessages` queue ensures consecutive messages are not lost—they
are automatically dequeued and processed when `EventResult` completes.

Session locking functions are in `core/engine_session.go`:
- `queueMessageForBusySession()` — queues messages for busy sessions
- `drainOrphanedQueue()` — handles orphaned queued messages

## Phase 3: Interactive State (Create vs Reuse)

`getOrCreateInteractiveStateWith()` (in `core/engine_session.go`) decides whether
to start a new claude CLI process or reuse an existing one:

```
Check e.interactiveStates[sessionKey]
  ├─ Exists + Alive() + session ID matches → REUSE (no new process)
  ├─ Exists + Alive() + session ID changed → Close old, CREATE new
  └─ Not exists / dead process → CREATE new
      ├─ First user message → startSessionID = "--continue" (bridge to latest CLI session)
      ├─ Saved session ID   → startSessionID = "uuid" (resume)
      └─ No saved ID        → startSessionID = "" (fresh session)
```

The `interactiveState` struct (defined in `core/engine_session.go`) holds:
- `agentSession` — running claude process handle
- `platform` / `replyCtx` — where to send responses
- `pending` — current permission request awaiting user response
- `pendingMessages` — queued messages when agent is busy
- `approveAll` — user clicked "allow all"

## Phase 4: Agent Session Startup

`agent.StartSession(ctx, sessionID)` launches the claude CLI subprocess.
For implementation details, consult **`references/stdio-protocol.md`**.

For authentication and API key injection, consult **`references/auth-and-providers.md`**.

## Phase 5: Sending Message to Agent

```go
sendDone := make(chan error, 1)
go func() {
    sendDone <- state.agentSession.Send(prompt, images, files)
}()
```

Send runs concurrently so the event loop can start immediately. The JSON
message is written to the claude process stdin. See **`references/stdio-protocol.md`**
for the wire format.

## Phase 6: Event Processing Loop

`processInteractiveEvents()` (in `core/engine_events.go`) is the core select loop:

| Event Type | Action | Platform Method |
|------------|--------|-----------------|
| `EventThinking` | Flush text, send thinking status | `send()` |
| `EventToolUse` | Flush text, freeze preview, send tool info | `send()` |
| `EventToolResult` | Send tool execution result | `send()` |
| `EventText` | Accumulate in `textParts[]`, update stream preview | (buffered) |
| `EventPermissionRequest` | Send permission card, **block until resolved** | `sendPermissionPrompt()` / `sendAskQuestionPrompt()` |
| `EventResult` | Send final response, save history, drain queue | `send()` / stream `finish()` |
| `EventError` | Send error message, cleanup | `send()` |

Helper functions used by the event loop (in `core/engine_util.go`):
- `send()`, `reply()` — platform message sending with logging
- `sendPermissionPrompt()` — renders permission cards/buttons
- `sendAskQuestionPrompt()` — renders user question UI
- `splitMessage()` — splits long messages at newline boundaries

### Permission Request Flow

Permission requests are **interrupt events** that pause the event loop:

1. Engine sends permission card to IM (buttons: Allow / Deny / Allow All)
2. Creates `pendingPermission` with a `Resolved` channel (defined in `engine_session.go`)
3. Blocks on `<-pending.Resolved`
4. User clicks button → `handlePendingPermission()` (in `engine_session.go`) parses response
5. Sends `control_response` to claude CLI stdin
6. Event loop resumes

### Streaming Preview

When platform supports `MessageUpdater`:
- `EventText` content is appended to `streamPreview`
- Preview updates are throttled (default: 1500ms interval, 30 char minimum)
- On interrupt (tool use, permission), preview is frozen
- On result, preview can serve as final message or be replaced

## Phase 7: Turn Completion

When `EventResult` arrives:
1. Merge all `textParts` into final response
2. Save to session history: `session.AddHistory("assistant", response)`
3. Send final response to platform
4. Check `pendingMessages` queue → if non-empty, send next and continue loop
5. If empty, return and release session lock

Queue draining is handled by `drainPendingMessages()` in `core/engine_events.go`.

## Debugging Tips

| Symptom | Check |
|---------|-------|
| Agent never responds | Logs for `session spawned` — did StartSession succeed? |
| "Previous request still processing" | Session lock held — check if permission request is pending |
| Response appears twice | Stream preview + final send — check `finish()` return value |
| Agent process dies mid-turn | Check stderr output in logs, look for OOM or crash |
| Messages lost when busy | Verify `pendingMessages` queue — check `drainPendingMessages()` in `engine_events.go` |

## Additional Resources

### Reference Files

- **`references/stdio-protocol.md`** — Complete JSON wire protocol between cc-connect and claude CLI
- **`references/auth-and-providers.md`** — How API keys, providers, and routers are configured and injected

### Related Skills

- **`card-callback-performance`** — IM card callback 3s timeout constraint and caching pattern for card rendering paths
- **`slash-command-system`** — Command registration via registry pattern, dispatch, and routing
