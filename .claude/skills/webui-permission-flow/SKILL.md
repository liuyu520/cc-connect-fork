---
name: webui-permission-flow
description: >
  This skill should be used when the user asks about "permission request",
  "control_request", "control_response", "permission prompt tool",
  "permission-prompt-tool stdio", "permission popup not showing",
  "permission dialog missing", "authorize tool use", "allow deny button",
  "webuiSession permission", "respondPermission", "updatedInput",
  "control_cancel_request", "permission cancelled", "permission_cancelled",
  "pendingInputs", "permission flow webui", "vibe permission",
  "前端没有弹出授权", "权限请求不显示", "权限弹窗",
  "webuiSession vs claudeSession", "webui parity",
  or needs to debug, extend, or understand how tool permission requests
  flow between Claude Code CLI, the Go WebUI backend, and the React frontend.
---

# WebUI Permission Flow

## Purpose

Document the complete permission request/response pipeline in the WebUI/Vibe
Coding path. This is a critical flow that differs structurally from the IM path
and has been a source of multi-layer bugs.

## Architecture Overview

```
Claude Code CLI (subprocess)
  │  stdout: {"type":"control_request","request_id":"...","request":{...}}
  ▼
webuiSession.parseEvent()          ← core/webui.go
  │  WebSocket: {"type":"permission_request","request_id":"...","tool_name":"..."}
  ▼
VibeSession.tsx handleServerMessage  ← web/src/pages/VibeCoding/VibeSession.tsx
  │  User clicks Allow/Deny
  ▼
WebSocket: {"type":"permission","request_id":"...","behavior":"allow"}
  │
  ▼
webuiSession.respondPermission()   ← core/webui.go
  │  stdin: {"type":"control_response","response":{"subtype":"success",...}}
  ▼
Claude Code CLI (resumes execution)
```

## Critical Requirement: --permission-prompt-tool stdio

**Without `--permission-prompt-tool stdio`, Claude Code CLI will NOT output
`control_request` events to stdout.** Instead, it handles permissions through
its internal TTY mechanism, which doesn't work in a subprocess context without
a terminal.

```go
// core/webui.go — webuiSession.start()
// MUST include --permission-prompt-tool stdio
args := []string{
    "--output-format", "stream-json",
    "--input-format", "stream-json",
    "--permission-prompt-tool", "stdio",  // CRITICAL: enables control_request events
    "--verbose",
}
```

Compare with the IM path (`agent/claudecode/session.go`):
```go
args := []string{
    "--output-format", "stream-json",
    "--input-format", "stream-json",
    "--permission-prompt-tool", "stdio",  // Same flag required
    "--verbose",
}
```

## Wire Formats

### CLI → Go: control_request Event

Claude Code CLI outputs permission requests in this format:

```json
{
  "type": "control_request",
  "request_id": "req_abc123",
  "request": {
    "subtype": "can_use_tool",
    "tool_name": "Bash",
    "input": {
      "command": "rm -rf /tmp/test",
      "description": "Delete temp files"
    }
  }
}
```

**Key structure points:**
- The tool info is in `event["request"]`, NOT `event["tool"]`
- Tool name is `request["tool_name"]`, NOT `request["name"]`
- Tool input is `request["input"]`
- Must check `request["subtype"] == "can_use_tool"` before processing

### Go → Frontend: permission_request WebSocket Message

```json
{
  "type": "permission_request",
  "request_id": "req_abc123",
  "tool_name": "Bash",
  "tool_input": "命令: rm -rf /tmp/test",
  "tool_input_full": {"command": "rm -rf /tmp/test", "description": "Delete temp files"}
}
```

### Frontend → Go: permission WebSocket Message

```json
{"type": "permission", "request_id": "req_abc123", "behavior": "allow"}
```

### Go → CLI: control_response Stdin Message

```json
{
  "type": "control_response",
  "response": {
    "subtype": "success",
    "request_id": "req_abc123",
    "response": {
      "behavior": "allow",
      "updatedInput": {"command": "rm -rf /tmp/test", "description": "Delete temp files"}
    }
  }
}
```

**For deny:**
```json
{
  "type": "control_response",
  "response": {
    "subtype": "success",
    "request_id": "req_abc123",
    "response": {
      "behavior": "deny",
      "message": "The user denied this tool use. Stop and wait for the user's instructions."
    }
  }
}
```

### CLI → Go: control_cancel_request Event

When Claude Code cancels a pending permission request:

```json
{
  "type": "control_cancel_request",
  "request_id": "req_abc123"
}
```

Go forwards this as `{"type": "permission_cancelled", "request_id": "..."}` to
the frontend, which auto-marks the permission dialog as "Cancelled".

## pendingInputs Cache

The `webuiSession` maintains a `pendingInputs map[string]map[string]any` that
caches the original tool input from each `control_request`. This is needed
because:

1. When user clicks "Allow", the `control_response` must include `updatedInput`
   with the original tool input (see `agent/claudecode/session.go:480-487`)
2. The frontend only sends back `request_id` + `behavior`, not the tool input
3. The cache is cleaned up on allow, deny, and cancel

```go
// On control_request: cache the input
s.pendingInputs[requestID] = toolInput

// On respondPermission (allow): retrieve and return
updatedInput := s.pendingInputs[requestID]
delete(s.pendingInputs, requestID)

// On control_cancel_request: clean up
delete(s.pendingInputs, requestID)
```

## AskUserQuestion Special Handling

`AskUserQuestion` is a Claude Code tool that presents structured questions to
users. It flows through the permission system but needs special handling:

1. **tool_use event**: Skip `AskUserQuestion` in the `assistant` event handler
   to avoid sending a redundant tool_use message to the frontend
2. **control_request event**: The IM path (`agent/claudecode/session.go:374`)
   parses `input` into structured `Questions` for rich UI rendering

```go
// In parseEvent, assistant/tool_use:
case "tool_use":
    toolName, _ := block["name"].(string)
    if toolName == "AskUserQuestion" {
        continue  // Skip — this goes through control_request path
    }
```

## webuiSession vs claudeSession Parity Checklist

When modifying `webuiSession` (core/webui.go), always cross-check with
`claudeSession` (agent/claudecode/session.go) for consistency:

| Feature | claudeSession | webuiSession | Notes |
|---------|--------------|--------------|-------|
| `--permission-prompt-tool stdio` | Yes | Yes | CRITICAL for permission flow |
| `--output-format stream-json` | Yes | Yes | |
| `--input-format stream-json` | Yes | Yes | |
| `--verbose` | Yes (configurable) | Yes | |
| `control_request` field: `event["request"]` | Yes | Yes | NOT `event["tool"]` |
| `control_request` subtype check | Yes | Yes | Must be `can_use_tool` |
| `respondPermission` with `updatedInput` | Yes | Yes | Required for allow |
| `control_cancel_request` handling | Yes (log only) | Yes (forward to frontend) | |
| AskUserQuestion filter in tool_use | Yes | Yes | |
| `--permission-mode` | Yes | No | WebUI always manual |
| `--resume` / `--continue` | Yes | No | WebUI fresh sessions only |
| `--append-system-prompt` | Yes | No | WebUI no system prompt injection |
| `--allowedTools` / `--disallowedTools` | Yes | No | WebUI no tool restrictions |
| stderr capture | Yes (Buffer) | No (nil) | WebUI loses error details |
| Auto-approve modes | Yes | No | WebUI always asks user |

## Common Bugs and Debugging

### Bug: Permission popup never appears

**Symptoms**: Claude Code runs but never asks for permission in the WebUI.
The AI's text output may reference needing permission but no dialog shows.

**Checklist**:
1. Is `--permission-prompt-tool stdio` in the CLI args? (Root cause of original bug)
2. Is `parseEvent` reading `event["request"]` (not `event["tool"]`)? (Second-layer bug)
3. Is the frontend handling `permission_request` message type?
4. Check browser DevTools WebSocket frames for `permission_request` messages

### Bug: Permission allowed but tool doesn't execute

**Symptoms**: User clicks Allow but Claude Code seems stuck.

**Checklist**:
1. Is `respondPermission` sending `updatedInput` with the original tool input?
2. Is the `control_response` format correct? (nested `response.response`)
3. Check Go logs for "permission response" debug messages

### Bug: Permission dialog stays after cancellation

**Symptoms**: Permission buttons remain clickable even though CLI moved on.

**Checklist**:
1. Is `control_cancel_request` handled in `parseEvent`?
2. Is the frontend handling `permission_cancelled` WebSocket message?
3. Is `pendingInputs` cleaned up on cancel?

### Debugging with logs

```bash
# Enable debug logging to see all events
# In Go: slog.Debug messages show event parsing
# In browser: check WebSocket frames in DevTools → Network → WS

# Key log messages:
# "webui: unknown control request subtype" — non-permission control_request received
# "webui: permission cancelled" — control_cancel_request processed
```

## Key Files

| File | Role |
|------|------|
| `core/webui.go` | `webuiSession.start()` (CLI args), `parseEvent()` (event parsing), `respondPermission()` (response formatting), `pendingInputs` cache |
| `agent/claudecode/session.go` | Reference implementation: `handleControlRequest()`, `RespondPermission()` — the authoritative format |
| `web/src/pages/VibeCoding/VibeSession.tsx` | Frontend: `handleServerMessage` case `permission_request` / `permission_cancelled`, `allowPermission()`, `denyPermission()` |
| `web/src/pages/VibeCoding/types.ts` | `ChatMessage.type` includes `permission_request` |
| `web/src/i18n/locales/*.json` | `vibe.permissionRequest`, `vibe.allow`, `vibe.deny`, `vibe.allowed`, `vibe.denied`, `vibe.cancelled` |

## Related Skills

- **`webui-vibe-coding`** — Overall WebUI architecture, WebSocket protocol, session lifecycle
- **`message-flow-architecture`** — IM path permission flow (Engine event loop, pendingPermission channel)
- **`webui-attachment-upload`** — Another WebSocket feature with full-stack data flow reference
