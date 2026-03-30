---
name: webui-vibe-coding
description: >
  This skill should be used when the user asks about "webui", "web ui",
  "vibe coding", "WebUIServer", "web interface", "browser Claude Code",
  "web frontend", "React admin dashboard", "web/src", "VibeCoding page",
  "WebSocket /api/vibe/ws", "webui config", "port 9830", "static file serving",
  "webuiSession", "claude code subprocess from web", "project dropdown",
  "work dir select", "listProjects", "Management API frontend",
  "multi tab vibe", "TabBar", "VibeSession component",
  "copy work dir", "clipboard copy", "disconnect confirm",
  "断开确认", "复制路径", "copyWorkDir",
  "AgentSystemPrompt", "project awareness", "/project command in agent",
  "attachment upload", "sendWithAttachments", "file upload vibe",
  "image upload vibe", "drag drop vibe", "paste image vibe",
  or needs to debug, extend, or understand the browser-based Vibe Coding
  interface and its Go backend.
---

# WebUI / Vibe Coding Architecture

## Purpose

The WebUI server provides a browser-based interface for directly interacting
with Claude Code CLI. Users select a project from a dropdown (populated from
config.toml projects via Management API) and chat with Claude Code from the
browser.

## Architecture Overview

```
┌─────────────────────────────────────────────────────────┐
│                    Browser (React SPA)                   │
│  web/src/pages/VibeCoding/VibeCoding.tsx                │
│  - On mount: GET /api/v1/projects → populate dropdown   │
│  - WebSocket client → ws://host:9830/api/vibe/ws        │
│  - Streaming text append, tool/thinking/permission UI   │
├─────────────────────────────────────────────────────────┤
│            ManagementServer (Go, core/management.go)    │
│  - GET /api/v1/projects → returns project list with     │
│    work_dir from agent.GetWorkDir()                     │
├─────────────────────────────────────────────────────────┤
│                  WebUIServer (Go, core/webui.go)        │
│  - HTTP server on port 9830 (configurable)              │
│  - Serves Vue/React SPA static files (embed or disk)    │
│  - WebSocket handler /api/vibe/ws                       │
│  - Token auth + CORS                                    │
├─────────────────────────────────────────────────────────┤
│                  webuiSession (Go, core/webui.go)       │
│  - Spawns `claude` CLI as subprocess                    │
│  - --output-format stream-json --input-format stream-json│
│  - --permission-prompt-tool stdio (CRITICAL for perms)  │
│  - stdin: user messages, permission responses           │
│  - stdout: event stream (system/assistant/result/control)│
└─────────────────────────────────────────────────────────┘
```

### Key Design Decisions

1. **Project Dropdown from Config** — The work directory is selected from a
   `<select>` dropdown populated by `GET /api/v1/projects`. Each project's
   `work_dir` comes from the agent's `GetWorkDir()` interface, which reads
   from `config.toml`. This eliminates manual path entry.

2. **Independent of Engine** — The WebUI spawns its own Claude Code processes
   directly, bypassing the Engine/Session infrastructure. The project list
   is only used to provide the work directory path.

3. **Same Go binary** — The WebUI server is part of the main cc-connect binary,
   enabled via `[webui]` config section. No separate Python backend needed.

4. **Existing React frontend** — Vibe Coding is a page in the existing React
   admin dashboard (`web/`), not a separate Vue app. It shares the same build
   tooling, i18n, theme system, and auth flow.

## File Map

| File | Purpose |
|------|---------|
| `core/webui.go` | WebUIServer + webuiSession + event parsing |
| `core/management.go` | Management API; `handleProjects()` returns project list with `work_dir` |
| `web/src/pages/VibeCoding/VibeCoding.tsx` | Multi-tab container: tab management + routing |
| `web/src/pages/VibeCoding/TabBar.tsx` | Tab bar component (tab list + status indicators + new/close) |
| `web/src/pages/VibeCoding/VibeSession.tsx` | Single-tab session (WebSocket + messages + chat UI) |
| `web/src/pages/VibeCoding/VibeMarkdown.tsx` | Markdown renderer (extracted for reuse) |
| `web/src/pages/VibeCoding/VibeHistory.tsx` | History sidebar panel (loads from MySQL via REST API) |
| `web/src/pages/VibeCoding/types.ts` | ChatMessage, TabState types + createTabState() factory |
| `web/src/api/projects.ts` | `ProjectSummary` type (includes `work_dir`) + `listProjects()` |
| `web/src/api/vibe.ts` | Vibe history API client (`listVibeSessions`, `getVibeMessages`, `exportVibeSession`) |
| `web/src/api/client.ts` | API client (`api.get<T>()` returns `json.data as T`) |
| `web/src/App.tsx` | Route `/vibe` → VibeCoding |
| `web/src/components/Layout/Sidebar.tsx` | Nav item "Vibe Coding" |
| `web/vite.config.ts` | Dev proxy: `/api/vibe/*` → 9830, `/api/*` → 9820 |
| `config/config.go` | `WebUIConfig` struct + startup validation (`database.dsn`, `management.port`) |
| `cmd/cc-connect/main.go` | WebUI server lifecycle (start/stop) |
| `web/src/i18n/locales/*.json` | `vibe.*` translation keys |

## Project Dropdown Integration

### Data Flow

```
config.toml [project] work_dir
  → Agent.GetWorkDir() (interface in core/interfaces.go)
  → management.go handleProjects() adds "work_dir" field
  → GET /api/v1/projects response
  → VibeCoding.tsx useEffect → listProjects()
  → <select> dropdown options: "projectName — /path/to/work_dir"
  → user selects → setWorkDir(work_dir)
  → WebSocket start message: {"type":"start","workDir":"/selected/path"}
```

### Backend: Management API Response

`core/management.go` `handleProjects()` uses capability interface check:

```go
// 获取 agent 的 work_dir（如果支持 GetWorkDir 接口）
workDir := ""
if wd, ok := e.agent.(interface{ GetWorkDir() string }); ok {
    workDir = wd.GetWorkDir()
}
projects = append(projects, map[string]any{
    "name":              name,
    "work_dir":          workDir,
    // ... other fields ...
})
```

### Frontend: API Types

`web/src/api/projects.ts`:

```typescript
export interface ProjectSummary {
  name: string;
  agent_type: string;
  platforms: string[];
  sessions_count: number;
  heartbeat_enabled: boolean;
  work_dir: string;  // from agent.GetWorkDir()
}
```

### Frontend: Project Dropdown UI

`web/src/pages/VibeCoding/VibeCoding.tsx`:

```typescript
// State
const [projects, setProjects] = useState<ProjectSummary[]>([]);

// 页面加载时获取项目列表
useEffect(() => {
  listProjects().then((res) => {
    const list = res?.projects || [];
    setProjects(list);
    // 单项目时自动选中
    if (list.length === 1 && list[0].work_dir && !workDir) {
      setWorkDir(list[0].work_dir);
    }
  }).catch(() => {});
}, []);

// JSX: <select> 替代 <input>
<select value={workDir} onChange={(e) => setWorkDir(e.target.value)} disabled={processAlive}>
  <option value="">{t('vibe.selectProject')}</option>
  {projects.map((p) => (
    <option key={p.name} value={p.work_dir}>
      {p.name} — {p.work_dir}
    </option>
  ))}
</select>
```

### Work Directory Copy Button

`web/src/pages/VibeCoding/VibeSession.tsx` 中实现了工作目录路径一键复制：

```typescript
// 状态：控制图标从 Copy → Check 的短暂切换
const [copySuccess, setCopySuccess] = useState(false);

// 复制工作目录路径到剪贴板
const copyWorkDir = () => {
  if (!tab.workDir) return;
  navigator.clipboard.writeText(tab.workDir).then(() => {
    setCopySuccess(true);
    setTimeout(() => setCopySuccess(false), 1500);
  });
};

// JSX: 在 <select> 右侧添加 Copy 按钮
<div className="flex gap-1.5">
  <select ...>{/* 项目下拉 */}</select>
  <button onClick={copyWorkDir} disabled={!tab.workDir}>
    {copySuccess ? <Check className="text-emerald-500" /> : <Copy />}
  </button>
</div>
```

**复用模式**：`navigator.clipboard.writeText()` + `setTimeout` 状态复位，
可套用到任何需要复制文本的场景（session ID、API key 等）。

### Disconnect Confirmation

点击"断开"按钮时，如果会话正在运行（`processAlive === true`），弹出确认弹窗，
复用 `VibeCoding.tsx` 中关闭 Tab 确认弹窗的完全相同样式：

```typescript
// 状态
const [showDisconnectConfirm, setShowDisconnectConfirm] = useState(false);

// 断开连接（带确认逻辑）
const handleDisconnect = () => {
  if (tab.processAlive) {
    setShowDisconnectConfirm(true);  // 会话活跃 → 弹确认
    return;
  }
  newSession();  // 会话不活跃 → 直接断开
};

const confirmDisconnect = () => {
  setShowDisconnectConfirm(false);
  newSession();
};
```

弹窗 JSX 与 `VibeCoding.tsx` 中 `closingTabId` 弹窗结构一致（颜色、按钮、布局）。

### i18n Keys

| Key | EN | ZH |
|-----|----|----|
| `vibe.selectProject` | `-- Select a project --` | `-- 请选择项目 --` |
| `vibe.emptyHint` | `Select a project and start...` | `选择项目并启动会话...` |
| `vibe.copied` | `Copied` | `已复制` |
| `vibe.disconnectConfirm` | `Session is still running. Disconnect?` | `会话正在运行中，确认断开连接？` |

All 5 locales must have these keys: `en.json`, `zh.json`, `zh-TW.json`, `ja.json`, `es.json`.

## WebSocket Protocol

### Client → Server

```jsonc
// Start a new Claude Code session (workDir from project dropdown)
{"type": "start", "workDir": "/path/to/project", "model": ""}

// Send user message (with optional attachments)
{"type": "send", "message": "fix the bug in main.go"}
{"type": "send", "message": "analyze this image", "attachments": [
  {"type": "image", "name": "screenshot.png", "mime_type": "image/png", "data": "iVBOR...base64..."}
]}

// Respond to permission request
{"type": "permission", "request_id": "xxx", "behavior": "allow"}  // or "deny"

// Interrupt current execution (sends SIGINT)
{"type": "abort"}
```

### Server → Client

```jsonc
{"type": "connected", "status": "ok"}
{"type": "session_id", "session_id": "xxx"}
{"type": "text", "content": "partial text..."}           // stream, append to current msg
{"type": "tool_use", "tool_name": "Bash", "tool_input": "命令: go test", "tool_input_full": {...}}
{"type": "tool_result", "tool_name": "...", "content": "..."}
{"type": "thinking", "content": "..."}
{"type": "result", "content": "done", "input_tokens": 1234, "output_tokens": 567}
{"type": "permission_request", "request_id": "xxx", "tool_name": "Bash", "tool_input": "...", "tool_input_full": {...}}
{"type": "permission_cancelled", "request_id": "xxx"}
{"type": "error", "message": "..."}
{"type": "status", "alive": false}
```

## Configuration

```toml
# 必须配置（启动校验）
[database]
dsn = "user:pass@tcp(host:port)/db?charset=utf8mb4&parseTime=true"

[management]
port = 9820                    # 必填，WebUI 前端依赖此端口

[webui]
enabled = true
port = 9830                    # HTTP listen port (default: 9830)
token = "your-secret"          # Auth token; empty = no auth
cors_origins = ["*"]           # Allowed CORS origins
static_dir = ""                # Path to web/dist; empty = use embedded files
```

## Frontend Streaming Pattern

The React component uses a `currentTextMsgIdRef` to implement streaming text append:

```typescript
case 'text': {
  const content = data.content as string;
  setMessages((prev) => {
    const curId = currentTextMsgIdRef.current;
    if (curId !== null) {
      // Append to existing message (streaming)
      return prev.map((m) => (m.id === curId ? { ...m, content: m.content + content } : m));
    } else {
      // Create new message
      const id = ++msgIdRef.current;
      currentTextMsgIdRef.current = id;
      return [...prev, { id, role: 'assistant', type: 'text', content, timestamp: Date.now() }];
    }
  });
  break;
}
```

When a non-text event arrives (tool_use, thinking, result, etc.), `currentTextMsgIdRef`
is reset to `null`, so the next text event creates a new message bubble.

## Go Backend: webuiSession Lifecycle

1. **start()** — `exec.Command("claude", "--output-format", "stream-json", "--input-format", "stream-json", "--permission-prompt-tool", "stdio", ...)` with stdin/stdout pipes. **Note:** `--permission-prompt-tool stdio` is CRITICAL — without it, Claude Code won't output `control_request` events and permission dialogs will never appear. See **`webui-permission-flow`** skill for details.
2. **send(message)** — Writes `{"type":"user","message":{"role":"user","content":"..."}}` to stdin
3. **sendWithAttachments(msg, images, files)** — Multimodal content array with base64 images + file path refs (see `webui-attachment-upload` skill)
4. **respondPermission(id, behavior)** — Writes `{"type":"control_response",...}` to stdin with `updatedInput` from `pendingInputs` cache
5. **forwardEvents(ctx, sendJSON)** — Goroutine: reads stdout line-by-line, parses JSON, calls sendJSON
6. **abort()** — Sends SIGINT to process
7. **stop()** — SIGINT → wait 3s → SIGKILL

## Extending the WebUI

### Adding a new message type

1. Add parsing in `webuiSession.parseEvent()` (Go)
2. Add rendering in `VibeCoding.tsx` inside the message map
3. If the message has expandable content, use the `expandedItems` Set pattern

### Adding new session features (e.g., audio upload)

The attachment upload feature is a complete reference implementation.
See **`webui-attachment-upload`** skill for the full pattern:

1. Add new WebSocket message type in the client→server protocol
2. Handle in `handleVibeWS()` switch statement
3. Implement in `webuiSession` (may need to extend the stdin protocol)
4. Add UI controls in VibeSession.tsx
5. Add i18n keys to all 5 locale files

### Adding new REST endpoints (e.g., export, analytics)

The Markdown export feature is a complete reference implementation.
See **`webui-export-markdown`** skill for the full pattern:

1. Register route in `Start()`: `mux.HandleFunc("/api/vibe/xxx", s.handleXxx)`
2. Implement handler following existing pattern (authenticate → setCORS → method check → body limit → logic)
3. Add frontend API function in `web/src/api/vibe.ts`
4. Add UI trigger in `VibeCoding.tsx` or `VibeHistory.tsx`
5. Add i18n keys to all 5 locale files

**Routing caveat:** Avoid sub-paths of `/api/vibe/sessions/` — Go's `http.ServeMux`
will route them to `handleVibeSessionMessages`. Use top-level paths like `/api/vibe/export`.

### Adding new fields from Management API to VibeCoding

Pattern used for the project dropdown — reusable for any config data:

1. **Backend**: Add field to `handleProjects()` response in `core/management.go`
   (use capability interface check: `if x, ok := e.agent.(interface{ ... }); ok`)
2. **API type**: Add field to `ProjectSummary` in `web/src/api/projects.ts`
3. **Frontend**: Call `listProjects()` in `useEffect`, map data to UI elements
4. **i18n**: Add translation keys to all 5 locale files

### Serving embedded static files (production)

Use `embed.FS` in a build-tag file:
```go
//go:build !dev

import "embed"

//go:embed web/dist
var webDistFS embed.FS
```
Pass `webDistFS` as the `staticFS` parameter to `NewWebUIServer()`.

## Agent System Prompt: Project Awareness

`core/interfaces.go` 的 `AgentSystemPrompt()` 包含一段 "Project management" 说明，
注入到 Claude Code agent 的系统提示中，确保 agent 在收到项目相关的自然语言问题时
引导用户使用 `/project` 命令，而不是去执行 `ls` 扫描文件系统。

**关键文件**: `core/interfaces.go` — `AgentSystemPrompt()` 函数

**注入内容**:
- 告知 agent cc-connect 管理多个项目，`CC_PROJECT` 环境变量包含当前项目名
- 引导用户使用 `/project` 列出项目、`/project <name>` 切换项目
- 明确指示 "Do NOT try to list projects by scanning the filesystem"

**生效条件**: 新建会话（`/new`）后生效，已有会话不受影响。

## Related Skills

- **`webui-attachment-upload`** — 附件上传完整实现（前后端数据流、多模态编码、三种上传方式）
- **`webui-permission-flow`** — 权限请求完整管线（CLI→Go→React→Go→CLI）、正确的 wire format、pendingInputs 缓存、常见 bug 排查
- **`webui-export-markdown`** — 聊天记录导出 Markdown（POST /api/vibe/export、消息类型映射、全栈数据流）
- **`vibe-chat-history`** — 聊天记录持久化 + 统一历史（IM+Vibe）+ 排错指南
- **`frontend-multi-tab`** — Multi-tab architecture pattern (state model, component splitting, hidden keep-alive)
- **`config-startup-validation`** — 启动配置校验模式（database.dsn、management.port 必填）
- **`add-new-feature`** — General feature implementation checklist (includes Management API pattern)
- **`message-flow-architecture`** — How cc-connect talks to Claude Code via stdio
