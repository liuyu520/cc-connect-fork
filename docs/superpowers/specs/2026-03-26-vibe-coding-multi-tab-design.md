# Vibe Coding Multi-Tab Design

## Summary

Add multi-tab support to the Vibe Coding page, allowing users to run multiple
independent Claude Code sessions in parallel, each targeting a different project
(work_dir). Tabs are fully isolated — each has its own WebSocket connection,
message history, and process lifecycle.

## Requirements

- Each tab selects its own project from the dropdown (different work_dir)
- Maximum 10 tabs open simultaneously
- Closing a tab with an active session shows a confirmation dialog
- Last remaining tab cannot be closed
- Switching tabs preserves all state; background tabs keep receiving messages
- Backend (Go) requires zero changes — all logic is frontend-only

## Architecture

### Approach: Pure Frontend Multi-Tab with Independent WebSockets

Each tab maintains its own WebSocket connection and complete state. The backend
`handleVibeWS()` has no concept of tabs — each WebSocket connection is already
an independent session. This gives us natural isolation with zero backend changes.

```
┌─ Tab Bar ──────────────────────────────────────────────────────┐
│ [🟢 ProjectA ×] [⚫ ProjectB ×] [⚫ New Tab ×]      [+ New]   │
├────────────────────────────────────────────────────────────────┤
│  Header: [Project ▼]  [Model]  [Status] [Start/Stop] [New]   │
├────────────────────────────────────────────────────────────────┤
│                    Chat Messages Area                          │
│              (active tab's messages only)                      │
├────────────────────────────────────────────────────────────────┤
│  [Input ...                                    ] [Send]       │
└────────────────────────────────────────────────────────────────┘
```

### Data Model

```typescript
interface TabState {
  id: string;                          // unique ID (crypto.randomUUID)
  label: string;                       // display name, auto-set from project name
  workDir: string;                     // selected project work_dir
  modelName: string;                   // model name
  messages: ChatMessage[];             // chat history
  userInput: string;                   // input box content
  connectionStatus: 'disconnected' | 'connecting' | 'connected';
  processAlive: boolean;
  waiting: boolean;
  sessionId: string;
  expandedItems: Set<number>;
}
```

WebSocket refs and counters stored in Maps keyed by tab ID:

```typescript
const wsMap = useRef<Map<string, WebSocket>>(new Map());
const msgIdMap = useRef<Map<string, number>>(new Map());
const currentTextMsgIdMap = useRef<Map<string, number | null>>(new Map());
```

### Component Structure

```
web/src/pages/VibeCoding/
├── VibeCoding.tsx        ← Main container: tab management + routing
├── TabBar.tsx            ← Tab bar component (tab list + new button)
├── VibeSession.tsx       ← Single tab session (extracted from VibeCoding)
├── VibeMarkdown.tsx      ← Markdown renderer (extracted for reuse)
└── types.ts              ← ChatMessage, TabState type definitions
```

**Responsibility split:**

- `VibeCoding.tsx`: Manages `tabs[]` and `activeTabId`, renders TabBar + all
  VibeSession instances (active one visible, others hidden with `display: none`)
- `TabBar.tsx`: Pure presentational — receives tabs, activeTabId, callbacks
- `VibeSession.tsx`: Core session logic (WebSocket, message processing, chat UI).
  Manages its own WebSocket connection independently. Hidden tabs stay mounted
  so WebSocket connections remain alive and messages continue to accumulate.
- `VibeMarkdown.tsx`: Extracted Markdown renderer component
- `types.ts`: Shared type definitions

### Tab Lifecycle

1. **Create**: Click `+` → new TabState with empty workDir, added to `tabs[]`
2. **Activate**: Click tab → set `activeTabId`, corresponding VibeSession becomes visible
3. **Background**: Tab hidden via `display: none`, WebSocket stays connected,
   messages continue to accumulate in state
4. **Close (no active session)**: Remove from `tabs[]`, VibeSession unmounts
5. **Close (active session)**: Show confirmation dialog → on confirm, send abort,
   disconnect WebSocket, remove tab

### Tab Bar Behavior

- Tab label: shows project name after selection, "New Tab" before
- Status indicator: green dot = processAlive, gray dot = disconnected
- Close button `×`: hidden on last remaining tab
- `+` button: disabled when tabs.length >= 10
- Overflow: horizontal scroll (`overflow-x: auto`) when tabs exceed available width

## Edge Cases

| Scenario | Behavior |
|----------|----------|
| Hit 10-tab limit | `+` button disabled with tooltip |
| Close last tab | `×` button hidden/disabled |
| Close tab with running process | Confirmation dialog before termination |
| WebSocket disconnect on one tab | Only that tab's status affected |
| Page refresh | All tabs and connections lost (acceptable) |
| Background tab receives messages | Messages stored, visible when switching back |
| Multiple tabs same project | Allowed, each gets independent Claude process |

## i18n Keys

| Key | EN | ZH |
|-----|----|----|
| `vibe.newTab` | `New Tab` | `新标签页` |
| `vibe.closeTabConfirm` | `Session is still running. Close this tab?` | `会话仍在运行，确认关闭此标签页？` |
| `vibe.maxTabsReached` | `Maximum 10 tabs` | `最多 10 个标签页` |
| `vibe.confirm` | `Confirm` | `确认` |
| `vibe.cancel` | `Cancel` | `取消` |

## Files to Modify/Create

| File | Action |
|------|--------|
| `web/src/pages/VibeCoding/types.ts` | **Create** — ChatMessage, TabState types |
| `web/src/pages/VibeCoding/VibeMarkdown.tsx` | **Create** — extract from VibeCoding |
| `web/src/pages/VibeCoding/TabBar.tsx` | **Create** — tab bar component |
| `web/src/pages/VibeCoding/VibeSession.tsx` | **Create** — extract session logic |
| `web/src/pages/VibeCoding/VibeCoding.tsx` | **Rewrite** — tab management container |
| `web/src/i18n/locales/*.json` | **Edit** — add new vibe.* keys (5 locales) |

## Testing

- TypeScript type check: `npx tsc --noEmit`
- Manual: open 3 tabs with different projects, verify messages don't cross tabs
- Manual: close tab with running session, verify confirmation dialog
- Manual: switch between tabs, verify state preserved
