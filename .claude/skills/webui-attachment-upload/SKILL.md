---
name: webui-attachment-upload
description: >
  This skill should be used when the user asks about "attachment upload",
  "file upload", "image upload", "paste image", "drag drop file",
  "vibe attachment", "pendingAttachments", "sendWithAttachments",
  "fileToAttachment", "AttachmentItem", "base64 attachment",
  "WebSocket attachment protocol", "multimodal content",
  "image content block", "file content block", "clipboard paste image",
  "drag and drop upload", "attachment preview", "file size limit",
  "10MB limit", "ExtFromMime", "附件上传", "图片上传", "拖拽上传",
  "粘贴图片", "附件预览", "文件大小限制",
  or needs to debug, extend, or understand the WebUI attachment upload
  feature including the full-stack data flow from browser to Claude Code CLI.
---

# WebUI Attachment Upload Architecture

## Purpose

Document the full-stack attachment upload feature for Vibe Coding WebUI.
Users can upload images and files through the browser, which are transmitted
via WebSocket as base64, decoded by the Go backend, and sent to the Claude
Code CLI as multimodal content blocks.

## Data Flow

```
Browser File/Paste/Drop
  → FileReader.readAsDataURL() → base64 string
  → WebSocket JSON { type:"send", message, attachments:[{type,name,mime_type,data}] }
  → Go backend base64.DecodeString → ImageAttachment / FileAttachment
  → webuiSession.sendWithAttachments():
      Images: disk save + base64 image content block
      Files:  SaveFilesToDisk() + text path references
  → Claude Code CLI stdin (stream-json, content array format)
```

## File Map

| File | Role |
|------|------|
| `core/message.go` | `ExtFromMime()` public helper, `ImageAttachment`, `FileAttachment`, `SaveFilesToDisk()`, `AppendFileRefs()` |
| `core/webui.go` | WebSocket ReadLimit (20MB), `case "send"` attachment parsing, `sendWithAttachments()` method |
| `agent/claudecode/session.go` | Reference implementation: `claudeSession.Send()` multimodal encoding (the pattern `sendWithAttachments` mirrors) |
| `web/src/pages/VibeCoding/types.ts` | `AttachmentItem` interface, `ChatMessage.attachments?`, `TabState.pendingAttachments` |
| `web/src/pages/VibeCoding/VibeSession.tsx` | File input, paste, drag-drop handlers, attachment preview UI, sendMessage with attachments |
| `web/src/i18n/locales/*.json` | `vibe.attach`, `vibe.dropFiles`, `vibe.fileTooLarge` translations |

## Backend: Go Implementation

### ExtFromMime (core/message.go)

Public function for mapping image MIME types to file extensions.
Extracted from `agent/claudecode/session.go`'s private `extFromMime()`.

```go
func ExtFromMime(mime string) string {
    switch mime {
    case "image/jpeg": return ".jpg"
    case "image/gif":  return ".gif"
    case "image/webp": return ".webp"
    default:           return ".png"
    }
}
```

### WebSocket ReadLimit

Default gorilla/websocket read limit is ~512 bytes. Increased to 20MB
to support base64-encoded attachments (~10MB file = ~14MB base64):

```go
conn.SetReadLimit(20 * 1024 * 1024)
```

### Attachment Parsing (case "send")

The `handleVibeWS` switch for `"send"` messages extracts attachments:

```go
if rawAttach, ok := msg["attachments"].([]any); ok {
    for _, item := range rawAttach {
        att, _ := item.(map[string]any)
        attType, _ := att["type"].(string)    // "image" or "file"
        name, _    := att["name"].(string)
        mimeType, _ := att["mime_type"].(string)
        dataStr, _  := att["data"].(string)   // pure base64
        data, err := base64.StdEncoding.DecodeString(dataStr)
        // Size check: > 10MB → error
        // Route to ImageAttachment or FileAttachment
    }
}
```

Validation rules:
- base64 decode failure → error message to frontend, skip attachment
- Size > 10MB → error message to frontend, skip attachment
- Empty message + empty attachments → silently skip (no send)

### sendWithAttachments (core/webui.go)

Mirrors `claudeSession.Send()` from `agent/claudecode/session.go`:

```go
func (s *webuiSession) sendWithAttachments(message string, images []ImageAttachment, files []FileAttachment) error {
    // No attachments → delegate to simple send() (content: string)
    // With attachments → build content array:
    //   1. Images: save to disk + base64 content block
    //   2. Files: SaveFilesToDisk() + text path references
    //   3. Text part: user prompt + file/image path references
    // → stdin.Encode({"type":"user","message":{"role":"user","content": parts}})
}
```

**Key difference from `claudeSession.Send()`**: `sendWithAttachments` is on
`webuiSession` (WebUI) while `Send()` is on `claudeSession` (IM platform path).
Both produce identical stdin JSON. They share `SaveFilesToDisk()`, `ExtFromMime()`.

### Claude Code stdin JSON format

Without attachments (simple text):
```json
{"type":"user","message":{"role":"user","content":"fix the bug"}}
```

With attachments (multimodal content array):
```json
{
  "type": "user",
  "message": {
    "role": "user",
    "content": [
      {"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "iVBOR..."}},
      {"type": "text", "text": "fix this UI bug\n\n(Images also saved locally: /path/img.png)\n\n(Files saved locally, please read them: /path/report.pdf)"}
    ]
  }
}
```

## Frontend: TypeScript Implementation

### AttachmentItem Type

```typescript
export interface AttachmentItem {
  id: string;           // crypto.randomUUID()
  type: 'image' | 'file';
  name: string;
  mimeType: string;
  size: number;         // raw bytes
  data: string;         // pure base64 (no data: prefix)
  previewUrl?: string;  // data URL for image preview
}
```

### File → AttachmentItem Conversion

```typescript
function fileToAttachment(file: File): Promise<AttachmentItem> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      const dataUrl = reader.result as string;
      const base64 = dataUrl.split(',')[1] || '';
      const item: AttachmentItem = {
        id: crypto.randomUUID(),
        type: isImageMime(file.type) ? 'image' : 'file',
        name: file.name,
        mimeType: file.type || 'application/octet-stream',
        size: file.size,
        data: base64,
        previewUrl: isImageMime(file.type) ? dataUrl : undefined,
      };
      resolve(item);
    };
    reader.onerror = () => reject(reader.error);
    reader.readAsDataURL(file);
  });
}
```

### Three Input Methods

| Method | Handler | Trigger |
|--------|---------|---------|
| Click button | `handleFileInputChange` | Hidden `<input type="file" multiple>` |
| Paste | `handlePaste` | `textarea onPaste`, extracts clipboard images |
| Drag & Drop | `handleDrop` | `onDragEnter/Leave/Over/Drop` on outer div |

#### Drag & Drop: Counter Pattern

`dragenter`/`dragleave` events fire on every child element, causing flicker.
The counter pattern solves this:

```typescript
const dragCounterRef = useRef(0);

const handleDragEnter = (e) => {
  dragCounterRef.current++;
  if (dragCounterRef.current === 1) setIsDragging(true);
};
const handleDragLeave = (e) => {
  dragCounterRef.current--;
  if (dragCounterRef.current === 0) setIsDragging(false);
};
const handleDrop = (e) => {
  dragCounterRef.current = 0;
  setIsDragging(false);
  addAttachments(e.dataTransfer.files);
};
```

### WebSocket Send Format

```typescript
const wsMsg: Record<string, unknown> = { type: 'send', message: msg };
if (attachments.length > 0) {
  wsMsg.attachments = attachments.map((a) => ({
    type: a.type,       // "image" | "file"
    name: a.name,
    mime_type: a.mimeType,
    data: a.data,       // pure base64
  }));
}
sendWsMessage(wsMsg);
```

### User Message Rendering

User message bubbles render attachments above the text content:

```tsx
{msg.attachments?.map((att) =>
  att.type === 'image' && att.previewUrl ? (
    <img src={att.previewUrl} className="max-w-[200px] max-h-[150px] rounded-lg" />
  ) : (
    <span className="inline-flex items-center gap-1 ...">
      <FileText size={12} /> {att.name} {formatFileSize(att.size)}
    </span>
  )
)}
```

### Input Area Layout

```
+--------------------------------------------------+
| [img1] [img2] [file.pdf 2.3MB] [x]              |  <- Attachment preview (shown when pending)
+--------------------------------------------------+
| [clip] [         textarea          ] [Send]      |  <- Attach btn + input + send
+--------------------------------------------------+
```

- Attach button: triggers hidden `<input type="file" multiple>`
- Preview area: images show 64x64 thumbnails, files show name+size
- Delete button: appears on hover (absolute positioned, top-right corner)
- Send button enabled when: `processAlive && (userInput.trim() || pendingAttachments.length > 0)`

## i18n Keys

| Key | en | zh | zh-TW | ja | es |
|-----|----|----|-------|----|----|
| `vibe.attach` | Attach file | 添加附件 | 新增附件 | ファイルを添付 | Adjuntar archivo |
| `vibe.dropFiles` | Drop files here | 拖放文件到此处 | 拖放檔案到此處 | ここにファイルをドロップ | Suelta archivos aqui |
| `vibe.fileTooLarge` | File too large (>10MB): {{name}} | 文件过大（>10MB）：{{name}} | 檔案過大（>10MB）：{{name}} | ファイルが大きすぎます（>10MB）：{{name}} | Archivo demasiado grande (>10MB): {{name}} |

## Size Limits

| Limit | Value | Where Enforced |
|-------|-------|----------------|
| WebSocket ReadLimit | 20 MB | `core/webui.go` `conn.SetReadLimit()` |
| Single file size | 10 MB | Go backend: `len(data) > 10*1024*1024` |
| Frontend check | 10 MB | `addAttachments()`: `file.size > MAX_FILE_SIZE` |

Frontend and backend both validate file size. Frontend check gives instant
user feedback; backend check prevents bypass.

## Extending: Adding New Attachment Types

### Adding audio/video support

1. **Frontend**: Add `'audio' | 'video'` to `AttachmentItem.type`, update `isImageMime` → `getAttachmentType`
2. **Backend**: Add `AudioAttachment` handling in `case "send"`, extend `sendWithAttachments()`
3. **Preview**: Add audio/video player in attachment preview area
4. **i18n**: No changes needed (generic "attach file" label covers all types)

### Adding URL/link attachments

1. **Frontend**: Add `'url'` type to `AttachmentItem`, no base64 needed
2. **Backend**: Fetch URL content server-side, or pass URL to Claude as text
3. **This avoids CORS issues** with client-side fetching

## Troubleshooting

### Problem: Large file upload fails silently

**Symptom**: File selected but no attachment appears, no error shown.
**Cause**: WebSocket ReadLimit too small for the base64-encoded file.
**Check**: Browser DevTools → Network → WebSocket frames → look for close frame.
**Fix**: Increase `conn.SetReadLimit()` in `core/webui.go`.

### Problem: Image sent but Claude can't "see" it

**Symptom**: Claude responds with generic text, doesn't describe the image.
**Cause**: Image content block format wrong (missing `media_type`, wrong `type`).
**Debug**: Add `slog.Debug` in `sendWithAttachments()` to log the full payload.
**Reference**: `agent/claudecode/session.go` Send() — the proven working format.

### Problem: Paste doesn't capture image

**Symptom**: Ctrl+V pastes text instead of capturing clipboard image.
**Cause**: `handlePaste` only captures items with `kind === 'file'`. If the
clipboard contains text + image, both are present. The handler only prevents
default when files are found.
**Verify**: `e.clipboardData.items` in browser console.

### Problem: Drag overlay flickers

**Symptom**: Drop zone overlay rapidly appears/disappears while dragging.
**Cause**: Missing or broken drag counter pattern. Each child element fires
its own `dragenter`/`dragleave` events.
**Fix**: Use `dragCounterRef` counter pattern (see "Three Input Methods" above).

## Design Decisions

### Why base64 over multipart upload?

1. **Simplicity**: Reuses existing WebSocket JSON protocol; no HTTP upload endpoint needed
2. **Atomicity**: Message + attachments arrive as one JSON message, no assembly needed
3. **Consistency**: Mirrors how IM platforms already deliver attachments (base64 in memory)
4. **Tradeoff**: ~33% overhead for base64, but 10MB limit keeps it practical

### Why dual validation (frontend + backend)?

Frontend validation gives instant feedback (before FileReader even reads the file).
Backend validation prevents bypass (e.g., crafted WebSocket messages). Both are cheap.

### Why save images to disk AND send as base64?

Claude Code CLI can "see" base64 images natively (multimodal content block).
But images saved to disk allow Claude to reference them in tool calls (`Read` tool)
and persist across conversation turns. Belt and suspenders.

## Related Skills

- **`webui-vibe-coding`** — WebUI architecture, WebSocket protocol, Go backend
- **`frontend-multi-tab`** — Tab state model (includes `pendingAttachments` field)
- **`message-flow-architecture`** — How IM platforms send attachments via `agentSession.Send()`
- **`vibe-chat-history`** — Chat persistence (user messages with attachments are persisted as text only)
