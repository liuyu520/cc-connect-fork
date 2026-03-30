---
name: vibe-chat-history
description: >
  This skill should be used when the user asks about "vibe history", "chat history persistence",
  "vibe chat database", "ChatStore biz_type", "cc_sessions biz_type", "cc_chat_messages biz_type",
  "vibe MySQL", "vibe session save", "/api/vibe/sessions", "VibeHistory component",
  "handleVibeSessions", "handleVibeSessionMessages", "ListSessions", "GetMessages",
  "chat history sidebar", "load history session", "continue conversation from history",
  "vibe chatstore integration", "webuiSession chatStore", "chatStore is nil",
  "MySQL DSN format", "mysql driver log", "slogWriter", "chatstore log",
  "database operation log", "vite proxy /api/vibe", "404 api vibe sessions",
  "unified history", "IM history in vibe", "listAllSessionsSQL", "biz_type filter",
  "history includes IM", "history source tag",
  or needs to debug, extend, or understand how Vibe Coding chat messages are persisted
  to MySQL and loaded as browsable history in the frontend.
---

# Vibe Coding Chat History Persistence

## Purpose

Document how Vibe Coding chat messages are persisted to MySQL via the shared
`ChatStore` infrastructure and how the frontend provides browsable/resumable
chat history. This is a reference for extending chat persistence to other
business types or adding new query patterns.

## Core Design: Reuse ChatStore + biz_type 区分

**不新建表**。Vibe Coding 复用现有 `cc_sessions` + `cc_chat_messages` 表，
通过 `biz_type` 字段区分来源：

| biz_type | 来源 | 写入方 |
|----------|------|--------|
| `im` | IM 平台消息 (Feishu/Telegram/Discord 等) | `engine_session.go` / `engine_events.go` |
| `vibe` | Vibe Coding 浏览器对话 | `webui.go` (`handleVibeWS`) |

## 表结构变更

两张表各新增一列：

```sql
ALTER TABLE cc_sessions ADD COLUMN biz_type VARCHAR(32) NOT NULL DEFAULT 'im'
  COMMENT '业务类型: im=IM平台, vibe=Vibe Coding';

ALTER TABLE cc_chat_messages ADD COLUMN biz_type VARCHAR(32) NOT NULL DEFAULT 'im'
  COMMENT '业务类型: im=IM平台, vibe=Vibe Coding';
```

- `DEFAULT 'im'` 确保旧数据自动标记为 IM，完全向后兼容
- 升级逻辑在 `NewMySQLChatStore()` 中执行 ALTER（Duplicate column 错误静默忽略）
- 新表建表 DDL 已包含 `biz_type` 列和索引

## Data Flow

### 写入路径（实时）

```
Browser WebSocket → webui.go handleVibeWS()
  ├─ "start" message → chatStore.EnsureSession(BizType:"vibe")
  ├─ "send" message  → chatStore.SaveMessage(Role:"user", BizType:"vibe")
  └─ parseEvent("result") → chatStore.SaveMessage(Role:"assistant", BizType:"vibe")
      └─ 同时 EnsureSession 更新 agent_session_id
```

### 读取路径（历史浏览）

```
Frontend VibeHistory 面板
  → GET /api/vibe/sessions?limit=50
    → WebUIServer.handleVibeSessions()
      → chatStore.ListSessions("vibe", 50)
        → SELECT ... FROM cc_sessions WHERE biz_type='vibe' ORDER BY updated_at DESC

  → GET /api/vibe/sessions/{id}/messages?limit=200
    → WebUIServer.handleVibeSessionMessages()
      → chatStore.GetMessages(sessionID, 200)
        → SELECT ... FROM cc_chat_messages WHERE session_id=? ORDER BY created_at ASC
```

### 导出路径（Markdown 下载）

```
Frontend (Active Tab 或 History Panel)
  → POST /api/vibe/export (JSON: metadata + messages[])
    → WebUIServer.handleVibeExportMarkdown()
      → buildExportMarkdown() → Markdown string
      → Content-Disposition: attachment → browser downloads .md file
```

History Panel 导出时会先调用 `getVibeMessages()` 获取消息，再 POST 到导出端点。
Active Tab 导出时直接使用内存中的 `tab.messages`（含 tool_use 等富信息）。
详见 **`webui-export-markdown`** skill。

## File Map

| File | Role |
|------|------|
| `core/chatstore.go` | `ChatStore` 接口 + `ChatMessage`/`ChatSessionInfo` 结构体（含 BizType 字段）+ 读取记录类型 |
| `core/chatstore_mysql.go` | MySQL 实现：DDL、ALTER 升级、INSERT/UPSERT（含 biz_type）、`ListSessions`/`GetMessages` 查询 |
| `core/chatstore_test.go` | `stubChatStore` 测试桩（实现 ListSessions/GetMessages） |
| `core/webui.go` | WebUIServer 注入 ChatStore + 写入调用 + REST API handlers |
| `cmd/cc-connect/main.go` | 将 chatStore 注入 WebUIServer |
| `web/src/api/vibe.ts` | 前端 Vibe 历史 API 客户端 |
| `web/src/pages/VibeCoding/VibeHistory.tsx` | 历史会话侧边面板组件（含每条会话的 Markdown 导出按钮） |
| `web/src/pages/VibeCoding/VibeCoding.tsx` | 容器中的历史面板入口 + 加载回调 + Tab 栏导出按钮 |
| `web/src/i18n/locales/*.json` | `vibe.history`/`vibe.loading`/`vibe.noHistory` 等翻译 |

## ChatStore Interface

```go
type ChatStore interface {
    // 写入（异步，非阻塞）
    SaveMessage(ctx context.Context, msg ChatMessage)
    EnsureSession(ctx context.Context, info ChatSessionInfo)

    // 读取（同步，返回结果）
    ListSessions(ctx context.Context, bizType string, limit int) ([]ChatSessionRecord, error)
    GetMessages(ctx context.Context, sessionID string, limit int) ([]ChatMessageRecord, error)

    Close() error
}
```

## Vibe Session ID 约定

- 格式：`vibe-{UnixNano}` (如 `vibe-1711468800000000000`)
- 每次 WebSocket `"start"` 消息创建新 ID
- `session_key` 格式：`vibe:{workDir}` (如 `vibe:/Users/ywwl/project-a`)

## Frontend Architecture

### VibeHistory 侧边面板

```
┌─ VibeCoding 容器 ──────────────────────────────────┐
│ [Tab 栏]                              [历史记录 📋] │ ← 点击打开侧边面板
│ ┌─ VibeSession ─────────────────────┐              │
│ │ 聊天 UI                           │              │
│ └───────────────────────────────────┘              │
└────────────────────────────────────────────────────┘

点击"历史记录"后：
┌──────────────────────────────┬─── VibeHistory ──┐
│       当前聊天界面            │ 📋 历史记录      │
│                              │ ───────────────  │
│                              │ [项目A]  2m ago  │
│                              │  最后一条消息...  │
│                              │ [项目B]  1h ago  │
│                              │  最后一条消息...  │
│                              │ [项目C]  3d ago  │
└──────────────────────────────┴──────────────────┘
```

### 加载历史流程

1. 用户点击历史会话 → `getVibeMessages(sessionId)` 获取消息
2. 创建新 Tab，`messages` 设为历史消息，`workDir` 设为项目路径
3. 用户可只读浏览历史
4. 用户可点击"启动"按钮重新连接 WebSocket 继续对话

## 举一反三：添加新的 biz_type

当需要为其他业务场景（如 API 调用、定时任务）添加聊天持久化时：

1. **定义新 biz_type 常量**：如 `"api"`, `"cron"`, `"webhook"`
2. **写入时设置 BizType**：在调用 `SaveMessage`/`EnsureSession` 时传入对应值
3. **读取时按 biz_type 过滤**：`chatStore.ListSessions("api", limit)`
4. **前端按需添加历史面板**：复用 `VibeHistory` 组件模式

无需修改表结构、无需新建表、无需改 ChatStore 接口。

## 举一反三：为 IM 消息也添加数据库历史浏览

当前 IM 消息的 Management API (`GET /projects/{project}/sessions/{id}`) 读取的是
JSON 文件/内存，不是 MySQL。如果想改为 MySQL 读取：

1. 在 `management.go` 中注入 `ChatStore`
2. `handleProjectSessions` 调用 `chatStore.ListSessions("im", limit)` 替代 JSON 读取
3. `handleProjectSessionDetail` 调用 `chatStore.GetMessages(id, limit)` 替代内存读取

接口完全一致，只需切换数据源。

## 统一历史：IM + Vibe 混合展示

历史记录面板默认展示**所有类型**的会话（Vibe + IM），通过颜色标签区分来源。

### 后端：`biz_type` 查询参数

`GET /api/vibe/sessions` 支持可选的 `biz_type` 参数：

| 请求 | 行为 |
|------|------|
| `/api/vibe/sessions?limit=50` | 返回所有类型（Vibe + IM），默认 |
| `/api/vibe/sessions?biz_type=vibe` | 仅 Vibe 会话 |
| `/api/vibe/sessions?biz_type=im` | 仅 IM 会话 |

实现方式：两条 SQL 常量

```go
// 按 biz_type 过滤
listSessionsSQL = `SELECT ... WHERE s.biz_type = ? ORDER BY s.updated_at DESC LIMIT ?`

// 不过滤（查所有类型）
listAllSessionsSQL = `SELECT ... ORDER BY s.updated_at DESC LIMIT ?`
```

`ListSessions` 方法根据 `bizType` 是否为空选择 SQL：

```go
func (s *MySQLChatStore) ListSessions(ctx, bizType, limit) {
    if bizType == "" {
        rows, err = s.db.QueryContext(ctx, listAllSessionsSQL, limit)
    } else {
        rows, err = s.db.QueryContext(ctx, listSessionsSQL, bizType, limit)
    }
}
```

### 前端：来源标签

`VibeHistory.tsx` 每条记录显示颜色标签区分来源：

```tsx
<span className={cn(
  'text-[10px] px-1.5 py-0.5 rounded',
  session.biz_type === 'vibe'
    ? 'text-emerald-600 bg-emerald-50'     // 绿色 = Vibe
    : 'text-blue-600 bg-blue-50'           // 蓝色 = IM
)}>
  {session.biz_type === 'vibe' ? 'Vibe' : 'IM'}
</span>
```

### 举一反三：添加前端筛选 Tab

如果未来需要前端按来源筛选，只需在 `VibeHistory` 中添加 Tab 切换：

```typescript
const [filter, setFilter] = useState<'' | 'vibe' | 'im'>('');
// 调用时传 biz_type 参数
listVibeSessions(50, filter);
```

API 层 `listVibeSessions` 添加 `bizType` 参数即可。

## AgentSystemPrompt 项目感知

`core/interfaces.go` 的 `AgentSystemPrompt()` 包含 "Project management" 段，
告知 Claude Code agent 关于 `/project` 命令的存在：

```
### Project management
cc-connect manages multiple projects, each with its own work_dir.
The current project name is in the CC_PROJECT environment variable.

When the user asks about "projects", "switch project", "list projects",
"current project", or similar, tell them to use the /project command:
  - /project         — list all configured projects
  - /project <name>  — switch to a different project

Do NOT try to list projects by scanning the filesystem.
```

这避免了 Claude Code 在收到"展示当前项目"等自然语言时去执行 `ls` 扫描文件系统，
而是引导用户使用 `/project` 命令查看 cc-connect 配置中的项目列表。

## Related Skills

- **`webui-vibe-coding`** — Vibe Coding 完整架构（WebSocket 协议、Go 后端、配置）
- **`webui-export-markdown`** — 聊天记录导出 Markdown（POST /api/vibe/export、全栈数据流）
- **`frontend-multi-tab`** — 多 Tab 架构模式（状态管理、组件拆分）
- **`add-new-feature`** — 通用功能添加 checklist（含 Management API 模式）
- **`config-startup-validation`** — 启动配置校验（database.dsn 必填）

## Database Operation Logging

所有数据库操作都有 `slog.Info` (成功) 和 `slog.Error` (失败) 日志：

| 操作 | 成功日志 | 失败日志 |
|------|---------|---------|
| MySQL 连接 | `INFO chatstore: MySQL connected dsn=***` | `ERROR chatstore: MySQL ping failed` |
| 消息队列投递 | (静默) | `ERROR chatstore: message queue full` |
| INSERT 消息 | `INFO chatstore: message saved session_id=... role=... biz_type=...` | `ERROR chatstore: save message failed` |
| UPSERT 会话 | `INFO chatstore: session ensured session_id=... project=...` | `ERROR chatstore: ensure session failed` |
| 查询会话列表 | `INFO chatstore: list sessions ok biz_type=... count=...` | `ERROR chatstore: list sessions query failed` |
| 查询消息列表 | `INFO chatstore: get messages ok session_id=... count=...` | `ERROR chatstore: get messages query failed` |

### MySQL Driver 日志桥接

go-sql-driver 的内部日志（如 `packets.go:58 unexpected EOF`）通过 `slogWriter` 桥接到 slog：

```go
// chatstore_mysql.go
func init() {
    _ = mysqldriver.SetLogger(log.New(slogWriter{}, "[mysql] ", 0))
}

type slogWriter struct{}
func (slogWriter) Write(p []byte) (int, error) {
    slog.Error("chatstore: mysql driver", "detail", strings.TrimSpace(string(p)))
    return len(p), nil
}
```

这确保底层 MySQL 协议错误也通过 cc-connect 的 slog 体系输出，不会因日志级别被过滤。

## Vite Dev Proxy 配置

`/api/vibe/*` 的 REST 端点注册在 WebUIServer (9830)，不是 Management API (9820)。
Vite 开发代理必须将 `/api/vibe` 转发到 9830，且规则要写在 `/api` 之前：

```typescript
// web/vite.config.ts
proxy: {
  '/api/vibe/ws': { target: 'http://localhost:9830', ws: true },
  '/api/vibe':    { target: 'http://localhost:9830' },   // ← REST API
  '/api':         { target: 'http://localhost:9820' },   // ← Management API
}
```

**常见问题**：`GET /api/vibe/sessions` 返回 404
**原因**：缺少 `/api/vibe` 代理规则，请求被 `/api` 规则转发到 9820
**修复**：在 vite.config.ts 中添加 `/api/vibe` 代理

## Troubleshooting

### 问题: `chatStore_nil=true`

日志中 `webui: creating vibe session chatStore_nil=true` 表示 chatStore 未注入。

**排查步骤**：
1. 看启动日志有无 `chatstore: MySQL connected` → 没有说明连接失败
2. 看有无 `chatstore: MySQL ping failed` → DSN 格式或网络问题
3. 看有无 `chatstore: mysql driver detail=[mysql] unexpected EOF` → MySQL 拒绝连接

### 问题: DSN 格式错误

go-sql-driver/mysql 要求 `tcp(host:port)` 格式：

```
错误: hw3:pass@172.17.6.38:3309/db
正确: hw3:pass@tcp(172.17.6.38:3309)/db?charset=utf8mb4&parseTime=true
```

`parseTime=true` 是必须的，否则 `DATETIME` 列扫描会失败。

### 问题: `unexpected EOF` / `invalid connection`

通常是 MySQL 服务端拒绝连接（IP 白名单、max_connections 已满、SSL 不匹配）。
用 `mysql -h host -P port -u user -p` 命令行测试确认。

### 问题: `context deadline exceeded`

连接超时（默认 10 秒）。可能是网络不通或 DNS 解析慢。
在 DSN 中添加 `timeout=5s` 可缩短超时时间以快速失败。
