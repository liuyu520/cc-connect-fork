---
name: frontend-multi-tab
description: >
  This skill should be used when the user asks about "multi tab", "multiple tabs",
  "tab isolation", "TabBar", "VibeSession", "multi-instance frontend",
  "independent WebSocket per tab", "tab state management", "pure frontend multi-instance",
  "component splitting for multi-instance", "React multi-tab pattern",
  "display:none keep alive", "tab close confirmation", "disconnect confirm",
  "confirmation modal pattern", "tabState", "createTabState",
  "hidden tab WebSocket", "parallel sessions", "multi-panel UI",
  or needs to implement, debug, or extend any multi-tab / multi-instance UI pattern
  where each tab maintains independent backend connections and isolated state.
---

# Frontend Multi-Tab Pattern

## Purpose

Document the **pure frontend multi-instance** architecture pattern used in
cc-connect's Vibe Coding page. This pattern is generalizable to any scenario
where users need multiple parallel, isolated sessions within the same page —
each with its own backend connection, message history, and lifecycle.

## When to Use This Pattern

| Scenario | Example |
|----------|---------|
| 多个并行 WebSocket 会话 | 每个 Tab 连一个 Claude Code 进程 |
| 多个独立 API 客户端 | 每个 Tab 连不同的服务端点 |
| 多工作区并行操作 | 每个 Tab 对应不同的代码仓库 |
| 多聊天窗口 | 类似浏览器多标签页的聊天界面 |
| 多终端面板 | 类似 VS Code 多终端的并行命令执行 |

## Core Principle: Backend Zero-Change

**后端不需要知道"Tab"的存在。** 每个 Tab 创建独立的后端连接（WebSocket/HTTP），
从后端视角看，它只是多个独立客户端。这带来三大好处：

1. **完全隔离** — Tab A 崩溃不影响 Tab B
2. **零后端改动** — 无需引入多路复用协议
3. **简单可靠** — 没有 tabId 路由、消息分发等复杂逻辑

## Architecture Overview

```
┌─────────────────────────────────────────────────────┐
│  VibeCoding.tsx (容器)                               │
│  ├── tabs: TabState[]     ← 所有 Tab 的状态         │
│  ├── activeTabId: string  ← 当前活跃的 Tab          │
│  ├── TabBar               ← Tab 栏 UI               │
│  └── VibeSession × N      ← 每个 Tab 一个实例       │
│       ├── 独立 WebSocket 连接                        │
│       ├── 独立消息列表                               │
│       ├── 独立输入状态                               │
│       └── visible={tab.id === activeTabId}           │
├─────────────────────────────────────────────────────┤
│  后端：每个 WebSocket 是独立的 session               │
│  对"Tab"无感知，无需任何改动                         │
└─────────────────────────────────────────────────────┘
```

## File Structure

```
web/src/pages/VibeCoding/
├── VibeCoding.tsx        ← 容器：Tab 增删切换 + 关闭确认弹窗
├── TabBar.tsx            ← Tab 栏：展示 + 状态指示器 + 关闭/新建
├── VibeSession.tsx       ← 单实例：WebSocket + 消息 + 聊天 UI
├── VibeMarkdown.tsx      ← 提取出的公共渲染组件
└── types.ts              ← TabState、ChatMessage 类型定义
```

### 文件职责划分原则

| 层级 | 职责 | 状态持有 |
|------|------|---------|
| **容器** (`VibeCoding.tsx`) | Tab 增删、切换、确认弹窗 | `tabs[]`, `activeTabId` |
| **Tab 栏** (`TabBar.tsx`) | 纯展示 + 事件回调 | 无状态 (props only) |
| **实例** (`VibeSession.tsx`) | 后端连接、消息处理、UI 渲染 | WebSocket ref, msgId ref |
| **类型** (`types.ts`) | 共享类型定义 + 工厂函数 | 无 |
| **公共组件** (`VibeMarkdown.tsx`) | 可复用渲染逻辑 | 无 |

## Data Model

### TabState — 每个 Tab 的完整独立状态

```typescript
// types.ts
export interface TabState {
  id: string;                          // crypto.randomUUID()
  label: string;                       // Tab 显示名称
  workDir: string;                     // 项目工作目录
  modelName: string;                   // 模型名
  messages: ChatMessage[];             // 聊天消息
  userInput: string;                   // 输入框内容
  connectionStatus: 'disconnected' | 'connecting' | 'connected';
  processAlive: boolean;               // 后端进程是否活跃
  waiting: boolean;                    // 等待响应
  sessionId: string;                   // 后端会话 ID
  expandedItems: Set<number>;          // 展开的消息 ID
  pendingAttachments: AttachmentItem[];// 待发送的附件（图片/文件）
}

// 工厂函数 — 创建空白 Tab 初始状态
export function createTabState(id?: string): TabState {
  return {
    id: id || crypto.randomUUID(),
    label: '',
    workDir: '',
    modelName: '',
    messages: [],
    userInput: '',
    connectionStatus: 'disconnected',
    processAlive: false,
    waiting: false,
    sessionId: '',
    expandedItems: new Set(),
    pendingAttachments: [],
  };
}
```

### 为什么用工厂函数而非默认值展开

直接 `{ ...defaultTab }` 会导致所有 Tab 共享同一个 `messages` 数组和 `expandedItems` Set
的引用。工厂函数每次调用都创建全新的实例，确保隔离。

## Key Design Decisions

### 1. 状态提升：Tab 状态由容器持有

```typescript
// VibeCoding.tsx (容器)
const [tabs, setTabs] = useState<TabState[]>(() => [createTabState()]);
const [activeTabId, setActiveTabId] = useState(() => tabs[0].id);

// 传给子组件的更新回调
const handleUpdateTab = useCallback((tabId: string, updates: Partial<TabState>) => {
  setTabs(prev => prev.map(tab =>
    tab.id !== tabId ? tab : { ...tab, ...updates }
  ));
}, []);
```

**为什么不在 VibeSession 内部用 useState？**

因为 TabBar 需要读取每个 Tab 的 `processAlive` 和 `connectionStatus` 来显示状态指示器。
如果状态在子组件内部，TabBar 无法访问。状态提升到容器后，所有子组件都能通过 props 获取。

### 2. Hidden 而非 Unmount：后台 Tab 保活

```tsx
// VibeCoding.tsx
{tabs.map(tab => (
  <VibeSession
    key={tab.id}
    tab={tab}
    onUpdateTab={handleUpdateTab}
    visible={tab.id === activeTabId}  // 控制可见性
  />
))}

// VibeSession.tsx
return (
  <div className={cn('flex flex-col h-full', !visible && 'hidden')}>
    {/* ... */}
  </div>
);
```

**为什么用 `hidden` 而非条件渲染 `{activeTabId === tab.id && <VibeSession />}`？**

| 方案 | WebSocket 连接 | 后台消息接收 | 切换延迟 |
|------|---------------|-------------|---------|
| 条件渲染 | 切走时断开 | 丢失 | 重连延迟 |
| `display: none` | 始终保持 | 持续累积 | 零延迟 |

`hidden`（等价于 `display: none`）让组件保持挂载，WebSocket 连接不中断，
后台 Tab 持续接收消息。用户切回时看到完整的历史记录，体验流畅。

### 3. Ref 在子组件内部管理

```typescript
// VibeSession.tsx — 每个实例独立的 Refs
const wsRef = useRef<WebSocket | null>(null);
const msgIdRef = useRef(0);
const currentTextMsgIdRef = useRef<number | null>(null);
```

**Refs 不需要提升到容器。** 它们是每个 VibeSession 实例的内部实现细节，
不需要被其他组件访问。React 的 `useRef` 在组件挂载时创建，卸载时回收，
每个实例自然独立。

### 4. 闭包过时问题的解决

WebSocket 的 `onmessage` 回调中引用 `tab` props 时，可能读到过时的值：

```typescript
// 问题：handleServerMessage 闭包捕获了旧的 tab 值
ws.onmessage = (event) => {
  handleServerMessage(JSON.parse(event.data));
  // 此时 handleServerMessage 中的 tab.messages 可能是旧的
};
```

**解决方案：用 Ref 追踪最新值**

```typescript
// VibeSession.tsx
const tabRef = useRef(tab);
tabRef.current = tab;  // 每次渲染更新

const handleServerMessage = useCallback((data) => {
  const currentTab = tabRef.current;  // 始终读到最新值
  updateTab({
    messages: [...currentTab.messages, newMessage],
  });
}, [updateTab]);

// 同样，用 Ref 包装 handleServerMessage 以避免 WebSocket 重连
const handleServerMessageRef = useRef(handleServerMessage);
handleServerMessageRef.current = handleServerMessage;

ws.onmessage = (event) => {
  handleServerMessageRef.current(JSON.parse(event.data));  // 始终调用最新版本
};
```

## Tab Bar Component

### 功能要点

```tsx
// TabBar.tsx
interface TabBarProps {
  tabs: TabState[];
  activeTabId: string;
  onSelectTab: (tabId: string) => void;
  onCloseTab: (tabId: string) => void;
  onNewTab: () => void;
}
```

| 特性 | 实现 |
|------|------|
| 状态指示灯 | `processAlive` → 绿色，`connecting` → 黄色，其他 → 灰色 |
| Tab 名称 | 选中项目后自动更新为项目名，未选则显示 "New Tab" |
| 关闭按钮 | 最后一个 Tab 时隐藏 `×`（至少保留 1 个 Tab） |
| 新建按钮 | 达到上限（10）时 disabled |
| 溢出处理 | `overflow-x: auto` 水平滚动 |

### 关闭确认弹窗

```typescript
// VibeCoding.tsx
const handleCloseTab = useCallback((tabId: string) => {
  const tab = tabs.find(t => t.id === tabId);
  if (tab?.processAlive) {
    setClosingTabId(tabId);  // 显示确认弹窗
    return;
  }
  removeTab(tabId);  // 无活跃进程，直接移除
}, [tabs]);
```

规则：
- `processAlive === true` → 弹确认框（"会话仍在运行，确认关闭？"）
- `processAlive === false` → 直接移除
- 确认关闭 → 组件卸载触发 `useEffect` cleanup → WebSocket 自动断开

### 确认弹窗复用模式

同一套弹窗 UI 样式在多处复用：

| 场景 | 位置 | 触发条件 | i18n Key |
|------|------|---------|----------|
| 关闭 Tab | `VibeCoding.tsx` | `processAlive && closeTab` | `vibe.closeTabConfirm` |
| 断开连接 | `VibeSession.tsx` | `processAlive && disconnect` | `vibe.disconnectConfirm` |

**通用弹窗结构**（可直接复制到新场景）：

```tsx
{showConfirm && (
  <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
    <div className="bg-white dark:bg-gray-900 rounded-xl shadow-2xl p-6 max-w-sm mx-4 border border-gray-200 dark:border-gray-700">
      <p className="text-sm text-gray-700 dark:text-gray-300 mb-4">
        {t('vibe.xxxConfirm')}
      </p>
      <div className="flex justify-end gap-3">
        <button onClick={cancel} className="...gray...">{t('vibe.cancel')}</button>
        <button onClick={confirm} className="...red...">{t('vibe.confirm')}</button>
      </div>
    </div>
  </div>
)}
```

**添加新的确认场景时**：
1. 添加 `showXxxConfirm` state
2. 在触发动作中判断 `processAlive`，设置 state
3. 复制上述弹窗 JSX，替换 i18n key
4. 添加 i18n 翻译到 5 个 locale 文件

## Edge Cases

| 场景 | 处理 |
|------|------|
| 达到 Tab 上限 | `+` 按钮 disabled + tooltip |
| 关闭最后一个 Tab | `×` 按钮隐藏 |
| 关闭活跃 Tab | 自动切换到最后一个剩余 Tab |
| 后台 Tab WebSocket 断开 | 只影响该 Tab 状态，Tab 栏指示灯变灰 |
| 页面刷新 | 所有 Tab/连接丢失（可接受） |
| 多 Tab 选同一个项目 | 允许，各自独立进程 |

## i18n Keys

| Key | EN | ZH |
|-----|----|----|
| `vibe.newTab` | `New Tab` | `新标签页` |
| `vibe.closeTabConfirm` | `Session is still running. Close this tab?` | `会话仍在运行，确认关闭此标签页？` |
| `vibe.maxTabsReached` | `Maximum 10 tabs` | `最多 10 个标签页` |
| `vibe.confirm` | `Confirm` | `确认` |
| `vibe.cancel` | `Cancel` | `取消` |

所有 5 个 locale 文件都需添加：`en.json`, `zh.json`, `zh-TW.json`, `ja.json`, `es.json`。

## 从单体组件到多 Tab 的重构步骤

### 通用 Checklist（可直接套用到类似场景）

**Step 1: 提取类型定义** (`types.ts`)

- 将组件内部的 interface/type 提取到独立文件
- 定义 `TabState`（或 `InstanceState`）接口，包含该实例的所有状态字段
- 提供工厂函数 `createTabState()` 创建初始值

**Step 2: 提取可复用组件** (如 `VibeMarkdown.tsx`)

- 识别与实例状态无关的纯渲染逻辑
- 提取为独立组件，减少实例组件的体积

**Step 3: 提取实例组件** (如 `VibeSession.tsx`)

- 将原单体组件的核心逻辑搬入
- `useState` 改为从 props (`tab`) 读取 + 通过 `onUpdateTab` 回调写入
- `useRef` 保留在实例内部（WebSocket ref、计数器等）
- 添加 `visible` prop 控制显示/隐藏
- `useEffect` cleanup 确保卸载时断开连接

**Step 4: 创建 Tab 栏组件** (`TabBar.tsx`)

- 纯展示组件，接收 props + 回调
- 状态指示灯、名称、关闭按钮、新建按钮
- 溢出滚动处理

**Step 5: 改写容器组件** (`VibeCoding.tsx`)

- 管理 `tabs[]` 和 `activeTabId`
- 渲染 TabBar + 所有实例（hidden 控制可见性）
- 关闭确认弹窗逻辑

**Step 6: 添加 i18n**

- Tab 相关文案：新建、关闭确认、上限提示等
- 所有语言文件都要更新

**Step 7: 验证**

- `npx tsc --noEmit` — 类型检查
- 手动测试：开 3 个 Tab，切换后验证状态隔离
- 后台 Tab 消息接收验证

## 举一反三：应用到其他场景

### 场景 1: 多终端面板

```typescript
interface TerminalTabState {
  id: string;
  label: string;
  command: string;       // 当前命令
  output: string[];      // 输出行
  isRunning: boolean;
  exitCode: number | null;
}
// 每个 Tab 一个 WebSocket 连接到后端 PTY
```

### 场景 2: 多数据库查询窗口

```typescript
interface QueryTabState {
  id: string;
  label: string;
  connectionString: string;
  sql: string;
  results: Row[];
  isExecuting: boolean;
  error: string | null;
}
// 每个 Tab 一个独立的 DB 连接
```

### 场景 3: 多 API 调试窗口（类 Postman）

```typescript
interface ApiTabState {
  id: string;
  label: string;
  method: 'GET' | 'POST' | 'PUT' | 'DELETE';
  url: string;
  headers: Record<string, string>;
  body: string;
  response: { status: number; body: string } | null;
  isLoading: boolean;
}
// 每个 Tab 独立的 fetch 请求
```

### 抽象核心：替换三个部分即可

| 组件 | 通用 | 场景特定 |
|------|------|---------|
| 容器 | `tabs[]` + `activeTabId` + TabBar + 确认弹窗 | 不变 |
| Tab 栏 | 状态指示灯 + 关闭 + 新建 | 名称显示规则 |
| 实例 | `visible` prop + `onUpdateTab` 回调 | 后端连接类型 + UI |
| 类型 | `id` + `label` | 其余字段按需定义 |

## Related Skills

- **`webui-vibe-coding`** — Vibe Coding 完整架构（WebSocket 协议、Go 后端、配置）
- **`webui-attachment-upload`** — 附件上传实现（pendingAttachments 状态管理、拖拽/粘贴/选择文件）
- **`add-new-feature`** — 通用功能添加 checklist（含 Management API 模式）
- **`message-flow-architecture`** — cc-connect 消息流管道详解
