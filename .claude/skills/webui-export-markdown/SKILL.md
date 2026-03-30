---
name: webui-export-markdown
description: >
  This skill should be used when the user asks about "export markdown", "export chat",
  "download markdown", "export conversation", "导出 Markdown", "导出聊天记录",
  "ExportRequest", "ExportMessage", "handleVibeExportMarkdown", "buildExportMarkdown",
  "POST /api/vibe/export", "exportVibeSession", "chat export", "markdown download",
  "sanitizeFilename", "Content-Disposition attachment", "export button vibe",
  "history export", "tool_use in export", "message type mapping export",
  or needs to debug, extend, or understand the chat history Markdown export feature
  including the full-stack data flow from frontend trigger to file download.
---

# Chat History Export to Markdown

## Purpose

Allow users to export Vibe Coding chat conversations as Markdown files. Supports two
entry points: the active tab (with rich tool_use info) and the history panel (text-only).
The backend generates the Markdown via a single `POST /api/vibe/export` endpoint.

## Architecture Overview

```
Frontend trigger (Export button)
  ├─ Active Tab: collect tab.messages (includes tool_use, error, etc.)
  └─ History Panel: fetch via getVibeMessages() → map to ExportMessage[]
      ↓
POST /api/vibe/export  (JSON body with metadata + messages)
      ↓
Go handler: handleVibeExportMarkdown()
  → authenticate → parse body → buildExportMarkdown()
  → Content-Disposition: attachment → browser downloads .md file
```

## File Map

| File | Role |
|------|------|
| `core/webui.go` | `ExportRequest`/`ExportMessage` structs, `handleVibeExportMarkdown` handler, `buildExportMarkdown`/`renderMessageMarkdown` formatter, `sanitizeFilename` |
| `core/webui_export_test.go` | Unit tests: sanitize, markdown build, empty/405/success handler tests |
| `web/src/api/vibe.ts` | `ExportRequest`/`ExportMessage` types, `exportVibeSession()` — POST + Blob download |
| `web/src/pages/VibeCoding/VibeCoding.tsx` | Export button in tab bar, `handleExportCurrentTab()`, `chatToExportMessages()` |
| `web/src/pages/VibeCoding/VibeHistory.tsx` | Per-session export button (hover), `handleExportSession()` |
| `web/src/i18n/locales/*.json` | `vibe.exportMarkdown`, `vibe.exportFailed` (5 locales) |

## API: POST /api/vibe/export

> Path is `/api/vibe/export` (NOT `/api/vibe/sessions/export`) to avoid routing conflict
> with the existing `/api/vibe/sessions/` prefix handler in Go's `http.ServeMux`.

### Request

```json
{
  "session_name": "astrBot_hw",
  "project": "/Users/ywwl/.../astrBot_hw",
  "agent_type": "claudecode",
  "session_id": "abc123",
  "messages": [
    {"role": "user", "type": "text", "content": "Hello", "timestamp": 1711612800000},
    {"role": "assistant", "type": "tool_use", "content": "Read file: main.go", "tool_name": "Read", "timestamp": 1711612801000},
    {"role": "assistant", "type": "text", "content": "This file is...", "timestamp": 1711612802000}
  ]
}
```

### Go Structs

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

### Response

- `Content-Type: text/markdown; charset=utf-8`
- `Content-Disposition: attachment; filename="name_20260328_143000.md"; filename*=UTF-8''...`

### Error Responses

| Code | Condition |
|------|-----------|
| 400 | Invalid JSON, empty messages array, body > 20MB |
| 401 | Authentication failed |
| 405 | Non-POST method |

## Message Type Mapping

| Frontend `type` | Markdown Rendering |
|-----------------|-------------------|
| `text` | Direct content: `## User (HH:mm:ss)` |
| `tool_use` | Blockquote + tool name: `## Assistant - Tool: Read (HH:mm:ss)` |
| `tool_result` | Fenced code block: `` ``` `` |
| `result` | Treated as normal text (final response) |
| `error` | Warning blockquote: `> Error: ...` |
| `thinking` | **Skipped** |
| `permission_request` | **Skipped** |
| `system` | **Skipped** |

## Generated Markdown Format

```markdown
# Chat Export: astrBot_hw

| Field | Value |
|-------|-------|
| Project | /Users/ywwl/.../astrBot_hw |
| Agent | claudecode |
| Session ID | abc123 |
| Messages | 15 |
| Exported At | 2026-03-28 14:30:00 (CST) |

---

## User (14:00:00)

Hello

---

## Assistant - Tool: Read (14:00:01)

> Read file: main.go

---

## Assistant (14:00:02)

This file is...
```

## Handler Implementation Pattern

```go
func (s *WebUIServer) handleVibeExportMarkdown(w http.ResponseWriter, r *http.Request) {
    // 1. authenticate (same as other vibe endpoints)
    // 2. setCORS + OPTIONS handling
    // 3. method check (POST only)
    // 4. http.MaxBytesReader(w, r.Body, 20<<20) — 20MB limit
    // 5. json.Decode → ExportRequest
    // 6. validate: len(Messages) > 0
    // 7. buildExportMarkdown() → Markdown string
    // 8. sanitizeFilename() → safe filename
    // 9. set Content-Type + Content-Disposition headers
    // 10. w.Write(md)
}
```

**Note:** Follow existing handler pattern: `authenticate` first, then `setCORS`, matching
`handleVibeSessions` / `handleVibePrompts` code style.

## Frontend: Two Export Entry Points

### Active Tab Export (VibeCoding.tsx)

- Download icon button in tab bar (next to History/Prompts buttons)
- Disabled when `tab.messages.length === 0`
- Shows `<Loader2>` spinner during export
- Collects `tab.messages` → filters out thinking/permission_request/system → POST

```typescript
const chatToExportMessages = (messages: ChatMessage[]): ExportMessage[] => {
  return messages
    .filter((m) => !['thinking', 'permission_request', 'system'].includes(m.type))
    .map((m) => ({
      role: m.role, type: m.type, content: m.content,
      tool_name: m.toolName, timestamp: m.timestamp,
    }));
};
```

### History Panel Export (VibeHistory.tsx)

- Small download icon on each session item (visible on hover)
- Disabled when `session.message_count === 0`
- Fetches messages via `getVibeMessages()` first, then maps:

```typescript
// History records are text-only (no tool_use)
const messages = data.messages.map((msg) => ({
  role: msg.role, type: 'text',
  content: msg.content,
  timestamp: new Date(msg.created_at).getTime(), // ISO → ms epoch
}));
```

## Frontend: Browser Download via Blob

```typescript
export const exportVibeSession = async (data: ExportRequest): Promise<void> => {
  const res = await fetch('/api/vibe/export', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(data),
  });
  if (!res.ok) throw new Error(await res.text());
  // Extract filename from Content-Disposition
  const match = res.headers.get('Content-Disposition')?.match(/filename="([^"]+)"/);
  const filename = match?.[1] || 'chat_export.md';
  // Trigger browser download
  const blob = await res.blob();
  const a = document.createElement('a');
  a.href = URL.createObjectURL(blob);
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(a.href);
};
```

## Filename Sanitization

```go
var filenameRe = regexp.MustCompile(`[^a-zA-Z0-9_\-]`)

func sanitizeFilename(name string) string {
    s := filenameRe.ReplaceAllString(name, "_")
    s = strings.Trim(s, "_")
    if s == "" { s = "export" }
    return s
}
```

Pattern: `{sanitized_project_name}_{YYYYMMDD}_{HHmmss}.md`

## i18n Keys

| Key | EN | ZH | ZH-TW | JA | ES |
|-----|----|----|-------|----|----|
| `vibe.exportMarkdown` | Export Markdown | 导出 Markdown | 匯出 Markdown | Markdownエクスポート | Exportar Markdown |
| `vibe.exportFailed` | Export failed | 导出失败 | 匯出失敗 | エクスポート失敗 | Error al exportar |

## Extending the Export

### Adding a new message type to export

1. Add case in `renderMessageMarkdown()` in `core/webui.go`
2. Update `chatToExportMessages()` filter in `VibeCoding.tsx` if the type was previously excluded
3. Update message type mapping table in this skill

### Adding a new export format (e.g., JSON, HTML)

1. Add `format` field to `ExportRequest` (default: `"markdown"`)
2. Add new builder function (e.g., `buildExportJSON`)
3. Switch on format in handler, set appropriate Content-Type
4. Frontend: add format selector or new button

### Adding export from IM sessions

The same `POST /api/vibe/export` endpoint works — just POST the messages in the
same format. The frontend needs a new export button in the IM session UI that
fetches messages and calls `exportVibeSession()`.

## Troubleshooting

### Export button disabled

Active Tab: `tab.messages` is empty (no messages yet).
History: `session.message_count === 0` (empty session).

### Download not triggering

Check browser console for CORS errors. Ensure `setCORS()` is called in the handler.
In dev mode, verify vite proxy forwards `/api/vibe/export` to port 9830.

### Filename garbled in some browsers

Ensure `Content-Disposition` includes both `filename` (ASCII fallback) and
`filename*=UTF-8''` (RFC 5987). The handler uses `url.PathEscape()` for encoding.

### Body too large (413)

The handler limits body to 20MB via `http.MaxBytesReader`. For very long sessions,
consider increasing the limit or paginating messages.

## Related Skills

- **`vibe-chat-history`** — ChatStore persistence, history panel, REST API for sessions/messages
- **`webui-vibe-coding`** — WebUI architecture, WebSocket protocol, webuiSession lifecycle
- **`add-new-feature`** — General feature implementation checklist
