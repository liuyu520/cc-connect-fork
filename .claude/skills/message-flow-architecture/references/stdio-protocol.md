# stdio JSON Protocol Reference

## Overview

cc-connect communicates with the `claude` CLI subprocess via a bidirectional
JSONL (JSON Lines) protocol over stdin/stdout. Each message is a single JSON
object followed by a newline character.

## CLI Launch Command

```bash
claude \
  --output-format stream-json \
  --input-format stream-json \
  --permission-prompt-tool stdio \
  --verbose \
  --model <model> \
  --mode <permission-mode> \
  [--resume <sessionID> | --continue --fork-session] \
  [--append-system-prompt <text>] \
  [--allowedTools <tool1,tool2>] \
  [--disallowedTools <tool1,tool2>]
```

**Key flags:**
- `--output-format stream-json` — stdout emits JSONL events
- `--input-format stream-json` — stdin accepts JSONL messages
- `--permission-prompt-tool stdio` — permission requests via JSON, not system dialogs
- `--verbose` — emit tool use/result events (disabled when using router)
- `--resume <id>` — resume an existing conversation
- `--continue --fork-session` — bridge to the most recent CLI session

**Source:** `agent/claudecode/session.go:44-100`

## Process Setup

```go
cmd := exec.CommandContext(sessionCtx, "claude", args...)
cmd.Dir = workDir                              // project work_dir from config
cmd.Env = filterEnv(os.Environ(), "CLAUDECODE") // inherit all env, filter CLAUDECODE
cmd.Env = core.MergeEnv(cmd.Env, extraEnv)     // merge provider/router/session env
stdin, _ := cmd.StdinPipe()                     // cc-connect writes here
stdout, _ := cmd.StdoutPipe()                   // cc-connect reads here
cmd.Start()
go cs.readLoop(stdout)                          // background goroutine
```

**Source:** `agent/claudecode/session.go:91-134`

## Messages: cc-connect → claude (stdin)

### User Message (text only)

```json
{"type":"user","message":{"role":"user","content":"帮我修复登录bug"}}
```

### User Message (with images)

```json
{
  "type": "user",
  "message": {
    "role": "user",
    "content": [
      {
        "type": "image",
        "source": {"type": "base64", "media_type": "image/png", "data": "iVBOR..."}
      },
      {
        "type": "text",
        "text": "这个截图中的bug怎么修？\n\n(Files saved locally: /path/to/file.txt)"
      }
    ]
  }
}
```

Images are base64-encoded inline. Files are saved to disk first, then their
paths are appended to the text content for Claude Code to read.

**Source:** `agent/claudecode/session.go:389-457`

### Permission Response (allow)

```json
{
  "type": "control_response",
  "response": {
    "subtype": "success",
    "request_id": "req-uuid",
    "response": {
      "behavior": "allow",
      "updatedInput": {"file_path": "/path/to/file.go", "new_string": "..."}
    }
  }
}
```

### Permission Response (deny)

```json
{
  "type": "control_response",
  "response": {
    "subtype": "success",
    "request_id": "req-uuid",
    "response": {
      "behavior": "deny",
      "message": "User denied permission"
    }
  }
}
```

**Source:** `agent/claudecode/session.go:472-510`

## Events: claude → cc-connect (stdout)

### System Event (session established)

```json
{"type": "system", "session_id": "uuid-string"}
```

First event emitted. The session ID is stored for future resume.

**Source:** `agent/claudecode/session.go:204-214`

### Assistant Event (thinking)

```json
{
  "type": "assistant",
  "message": {
    "content": [{"type": "thinking", "thinking": "Let me analyze this bug..."}]
  }
}
```

Mapped to `EventThinking`.

### Assistant Event (tool use)

```json
{
  "type": "assistant",
  "message": {
    "content": [{
      "type": "tool_use",
      "name": "Read",
      "input": {"file_path": "login.go"}
    }]
  }
}
```

Mapped to `EventToolUse`. The `input` is JSON-serialized for display.

### Assistant Event (text)

```json
{
  "type": "assistant",
  "message": {
    "content": [{"type": "text", "text": "I found the bug. The cache key..."}]
  }
}
```

Mapped to `EventText`. Multiple text events accumulate before sending.

**Source:** `agent/claudecode/session.go:216-264`

### User Event (tool result)

```json
{
  "type": "user",
  "message": {
    "content": [{
      "type": "tool_result",
      "is_error": false,
      "content": "file contents here..."
    }]
  }
}
```

Mapped to `EventToolResult`. Shows what the tool returned to Claude.

**Source:** `agent/claudecode/session.go:266-289`

### Control Request (permission needed)

```json
{
  "type": "control_request",
  "request_id": "req-uuid",
  "request": {
    "subtype": "can_use_tool",
    "tool_name": "Edit",
    "input": {
      "file_path": "login.go",
      "old_string": "buggy code",
      "new_string": "fixed code"
    }
  }
}
```

Mapped to `EventPermissionRequest`. The event loop pauses until
cc-connect sends a `control_response` back.

**Auto-handling by permission mode:**
- `bypassPermissions` → auto-allow all tools
- `acceptEdits` → auto-allow Edit/Write/NotebookEdit/MultiEdit only
- `dontAsk` → auto-deny all
- `default` → forward to user via IM

**Source:** `agent/claudecode/session.go:325-383`

### Control Cancel Request

```json
{"type": "control_cancel_request", "request_id": "req-uuid"}
```

Claude Code cancelled a pending permission request (e.g., timeout).

### Result Event (turn complete)

```json
{
  "type": "result",
  "result": "done",
  "session_id": "uuid-string",
  "usage": {
    "input_tokens": 15000,
    "output_tokens": 2000,
    "cache_creation_input_tokens": 0,
    "cache_read_input_tokens": 8000
  }
}
```

Mapped to `EventResult` with `Done=true`. Contains token usage statistics.

**Source:** `agent/claudecode/session.go:291-323`

## Event Channel

- Type: `chan core.Event` with buffer size 64
- Reader: `readLoop()` goroutine reads stdout line by line
- Scanner buffer: initial 64KB, max 10MB
- Channel closed when process exits or context cancelled

## Write Serialization

All stdin writes are serialized via `stdinMu sync.Mutex` to prevent
interleaved JSON when `Send()` and `RespondPermission()` race.

```go
func (cs *claudeSession) writeJSON(v any) error {
    cs.stdinMu.Lock()
    defer cs.stdinMu.Unlock()
    data, _ := json.Marshal(v)
    _, err := cs.stdin.Write(append(data, '\n'))
    return err
}
```

## Session Lifecycle

```
New()     → cmd.Start() → alive=true → readLoop() starts
Running   → Send() writes stdin, readLoop() emits events
Close()   → cancel context → wait 8s for graceful exit → force kill
Dead      → alive=false → Events() channel closed
```

`Alive()` checks the atomic bool; `CurrentSessionID()` returns the stored ID.
