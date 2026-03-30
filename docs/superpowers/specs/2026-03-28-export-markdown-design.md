# Chat History Export to Markdown

## Overview

Add a Markdown export feature for Vibe Coding chat history. Users can export conversations from both the active tab (with rich tool_use info) and the history panel (text-only user/assistant messages). The backend generates the Markdown file via a single POST endpoint.

## Architecture

### Approach: POST with Frontend-Supplied Data

A single `POST /api/vibe/export` endpoint handles both export scenarios:

- **Active Tab export**: Frontend sends the full `tab.messages` array (including `tool_use`, `error`, etc.) along with session metadata.
- **History Panel export**: Frontend first fetches messages via `getVibeMessages()`, maps them to the export format, then POSTs to the same endpoint.

The backend is responsible for all Markdown formatting and returns the file as a download.

### Why This Approach

- One endpoint, one formatting logic — no duplication
- Backend controls the Markdown format — easy to evolve without frontend changes
- Active Tab's rich messages (tool_use) can be exported despite not being persisted to the database

## API Design

### `POST /api/vibe/export`

> **Note:** The path is `/api/vibe/export` (not `/api/vibe/sessions/export`) to avoid routing conflicts with the existing `/api/vibe/sessions/` prefix handler in Go's `http.ServeMux`.

**Request Body:**

```json
{
  "session_name": "astrBot_hw",
  "project": "/Users/ywwl/.../astrBot_hw",
  "agent_type": "claudecode",
  "session_id": "abc123",
  "messages": [
    {
      "role": "user",
      "type": "text",
      "content": "Help me read this file",
      "timestamp": 1711612800000
    },
    {
      "role": "assistant",
      "type": "tool_use",
      "content": "Read file: src/main.py",
      "tool_name": "Read",
      "timestamp": 1711612801000
    },
    {
      "role": "assistant",
      "type": "text",
      "content": "This file is an entry point...",
      "timestamp": 1711612802000
    }
  ]
}
```

**Go Struct Definitions:**

```go
type ExportRequest struct {
    SessionName string          `json:"session_name"`
    Project     string          `json:"project"`
    AgentType   string          `json:"agent_type"`
    SessionID   string          `json:"session_id"`
    Messages    []ExportMessage `json:"messages"`
}

type ExportMessage struct {
    Role      string `json:"role"`                // "user" or "assistant"
    Type      string `json:"type"`                // "text", "tool_use", "result", "tool_result", "error"
    Content   string `json:"content"`
    ToolName  string `json:"tool_name,omitempty"` // only for type="tool_use"
    Timestamp int64  `json:"timestamp"`           // millisecond Unix epoch
}
```

**Response:**

- `Content-Type: text/markdown; charset=utf-8`
- `Content-Disposition: attachment; filename="astrBot_hw_20260328_143000.md"; filename*=UTF-8''astrBot_hw_20260328_143000.md`
- Body: UTF-8 Markdown content

**Error Responses:**

- `400 Bad Request` — invalid JSON, empty messages array, or body exceeds 20MB limit
- `401 Unauthorized` — authentication failed
- `405 Method Not Allowed` — non-POST request

### Generated Markdown Format

```markdown
# Chat Export: astrBot_hw

| Field | Value |
|-------|-------|
| Project | /Users/ywwl/.../astrBot_hw |
| Agent | claudecode |
| Session ID | abc123 |
| Messages | 15 |
| Exported At | 2026-03-28 14:30:00 (server local) |

---

## User (14:00:00)

Help me read this file

---

## Assistant - Tool: Read (14:00:01)

> Read file: src/main.py

---

## Assistant (14:00:02)

This file is an entry point...

---
```

### Message Type Mapping

| Frontend `type` | Markdown Rendering |
|-----------------|-------------------|
| `text` | Direct content output |
| `tool_use` | Blockquote with tool name in heading: `## Assistant - Tool: {toolName}`, content in blockquote |
| `tool_result` | Fenced code block for tool execution output |
| `result` | Treated as normal `text` (final assistant response with token stats) |
| `thinking` | Skipped (not exported) |
| `permission_request` | Skipped (not exported) |
| `error` | Blockquote with warning prefix: `> Error: ...` |
| `system` | Skipped (not exported) |

## Frontend Changes

### Files Modified

1. **`web/src/api/vibe.ts`** — New `exportVibeSession(data)` function
2. **`web/src/pages/VibeCoding/VibeCoding.tsx`** — Export button in tab bar area (alongside History and Prompts buttons)
3. **`web/src/pages/VibeCoding/VibeHistory.tsx`** — Export icon button on each session item (visible on hover)
4. **`web/src/i18n/locales/{en,zh,zh-TW,ja,es}.json`** — New translation keys

### i18n (Frontend)

Add keys under the `vibe` namespace in each locale JSON file:

| Key | EN | ZH | ZH-TW | JA | ES |
|-----|----|----|-------|----|----|
| `vibe.exportMarkdown` | Export Markdown | 导出 Markdown | 匯出 Markdown | Markdownエクスポート | Exportar Markdown |
| `vibe.exportFailed` | Export failed | 导出失败 | 匯出失敗 | エクスポート失敗 | Error al exportar |

> **Note:** These are frontend-only UI strings. No changes to `core/i18n.go` are needed since the Go backend does not produce user-facing i18n strings for this feature.

### Export Button Locations

**Active Tab (Tab bar area):**
- Download icon button next to History/Prompts buttons
- Collects `tab.messages`, `tab.workDir`, `tab.sessionId` and POSTs to backend
- **Disabled when `tab.messages` is empty** (no messages to export)

**History Panel (per session item):**
- Small download icon on hover for each session entry
- Fetches messages via `getVibeMessages()`, maps to export format, POSTs to backend
- **Disabled when `session.message_count === 0`** (empty session)

### History Panel Message Mapping

When exporting from the history panel, `VibeMessageRecord` is mapped to `ExportMessage`:

```typescript
const exportMsg: ExportMessage = {
  role: msg.role,
  type: 'text',  // history records are always text-only
  content: msg.content,
  timestamp: new Date(msg.created_at).getTime(),  // ISO string → ms epoch
};
```

### Interaction Details

- Button shows loading spinner during export (prevents double-click)
- Successful export triggers browser download with no additional notification
- Failed export shows toast error message using `vibe.exportFailed` i18n key

## Backend Changes

### Files Modified

1. **`core/webui.go`** — New route registration + `handleVibeExportMarkdown` handler function

### Route Registration

```go
mux.HandleFunc("/api/vibe/export", s.handleVibeExportMarkdown)
```

### Handler: `handleVibeExportMarkdown`

1. Calls `s.setCORS(w, r)` and handles OPTIONS preflight
2. Authenticates via `s.authenticate(r)` (same as other vibe endpoints)
3. Rejects non-POST methods with 405
4. Wraps `r.Body` with `http.MaxBytesReader(w, r.Body, 20<<20)` (20MB limit)
5. Decodes JSON into `ExportRequest` struct
6. Validates: returns 400 if `Messages` is empty
7. Iterates messages, formats each by type according to mapping table
8. Builds metadata header table with server local time + timezone label
9. Sanitizes project name for filename (keep `[a-zA-Z0-9_-]`, replace others with `_`)
10. Sets `Content-Type`, `Content-Disposition` (with RFC 5987 `filename*` for Unicode support)
11. Writes Markdown content to response

### File Naming

Pattern: `{project_name}_{YYYYMMDD}_{HHmmss}.md`

- Project name extracted from path (last segment), e.g. `/Users/.../astrBot_hw` → `astrBot_hw`
- Sanitization: keep `[a-zA-Z0-9_-]`, replace all other characters with `_`
- Timestamp from server local time
- `Content-Disposition` uses both `filename` (ASCII fallback) and `filename*=UTF-8''` (RFC 5987) for browser compatibility

### Timestamps in Markdown

- Messages: converted from millisecond Unix epoch to `HH:mm:ss` in server local timezone
- `Exported At` header: server local time formatted as `YYYY-MM-DD HH:mm:ss` with timezone label (e.g. `(CST)`)

## Security

- **Request body limit**: 20MB via `http.MaxBytesReader` to prevent OOM
- **Authentication**: Uses existing `s.authenticate(r)` check
- **CORS**: Uses existing `s.setCORS(w, r)` for cross-origin support
- **Filename sanitization**: Only `[a-zA-Z0-9_-]` allowed, others replaced with `_`
- **No user-supplied content used in file paths** without sanitization

## Testing

### Backend Tests

- Unit test for Markdown generation logic (various message type combinations: text, tool_use, tool_result, error, mixed)
- Unit test for filename sanitization (ASCII, Unicode, special characters, empty string)
- Unit test for metadata header generation (with/without optional fields)
- Unit test for empty messages rejection (400 response)
- Unit test for body size limit enforcement

### Frontend

- Manual testing of export from active tab and history panel
- Verify download triggers correctly in Chrome/Firefox/Safari
