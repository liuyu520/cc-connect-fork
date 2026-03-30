# 2026-03-30 会话增强与 WebUI 优化

本次迭代包含 **4 项功能改进**，围绕 agent 会话生命周期通知、WebUI 权限流程简化、以及工具调用过程的可控显示展开。

---

## 目录

1. [Session Complete 通知](#1-session-complete-通知)
2. [WebUI 权限自动批准](#2-webui-权限自动批准)
3. [工具调用过程显示开关](#3-工具调用过程显示开关)
4. [举一反三：设计模式与经验总结](#4-举一反三设计模式与经验总结)

---

## 1. Session Complete 通知

### 背景

用户在 IM 端与 agent 交互时，agent 回复可能由多条消息组成（thinking → tool_use → tool_result → text → result）。用户无法直观判断 agent 是否已经完成当前轮次的回答，容易误以为还在处理中。

### 功能描述

当 agent session 完全回答完毕（`EventResult` 处理完毕且无排队消息）时，额外发送一条 IM 通知：

```
->->->-> session complete
```

### 配置方式

```toml
[[projects]]
name = "my-project"
session_complete_notify = true   # 默认 true，设为 false 关闭
```

### 修改文件

| 文件 | 变更说明 |
|------|----------|
| `config/config.go` | `ProjectConfig` 新增 `SessionCompleteNotify *bool` 字段 |
| `core/engine.go` | Engine 新增 `sessionCompleteNotify` 字段 + `SetSessionCompleteNotify()` setter |
| `core/engine_events.go` | 在 `processInteractiveEvents` 中 `EventResult` + 队列为空的 `return` 前发送通知 |
| `cmd/cc-connect/main.go` | 初始化（第 271-278 行）和热重载（第 1308-1313 行）两处布线 |
| `config.example.toml` | 添加配置示例和中英文注释 |

### 关键实现细节

**触发时机的精确控制**（`core/engine_events.go`）：

```go
case EventResult:
    // ... 正常处理 EventResult ...

    // 检查是否有排队消息需要继续处理
    state.mu.Lock()
    if len(state.pendingMessages) > 0 {
        // 有排队消息，继续处理，不发通知
        next := state.pendingMessages[0]
        state.pendingMessages = state.pendingMessages[1:]
        state.mu.Unlock()
        // ... 处理下一条消息 ...
        continue
    }
    state.mu.Unlock()

    // Session turn fully complete with no pending messages — send completion notification
    if e.sessionCompleteNotify {
        state.mu.Lock()
        notifyP := state.platform
        notifyCtx := state.replyCtx
        state.mu.Unlock()
        e.send(notifyP, notifyCtx, "->->->-> session complete")
    }
    return
```

**设计考量**：

1. **仅正常完成时触发** — 不在 `EventError`、channel 关闭、idle timeout 等异常退出路径触发
2. **排队消息感知** — 多条排队消息在同一事件循环中处理，只有最后一条完成后才发通知，避免中间轮次重复通知
3. **线程安全** — 从 `state.mu` 锁内读取 `platform` 和 `replyCtx`，确保使用最新的上下文
4. **热重载支持** — 在 `reloadConfig` 中也布线了，修改配置无需重启

### Commit

```
4469b26 feat: add session complete notification to IM
```

---

## 2. WebUI 权限自动批准

### 背景

WebUI 的 Vibe Coding 模式此前会将 Claude Code CLI 的 `control_request`（权限请求）转发到前端，弹出 Allow/Deny 弹窗让用户确认。但在 Vibe Coding 场景下，用户期望的是全自动执行，频繁弹出权限确认影响体验。

IM 端（`agent/claudecode/session.go`）早已使用 `autoApprove` 模式自动批准所有工具调用，WebUI 端需要对齐。

### 功能描述

1. 启动 Claude Code CLI 时添加 `--permission-mode bypassPermissions` 参数
2. 收到 `control_request` 后立即回传 `allow`，不再转发 `permission_request` 到前端

### 修改文件

| 文件 | 变更说明 |
|------|----------|
| `core/webui.go` | CLI 参数添加 `--permission-mode bypassPermissions`；`parseEvent` 中 `control_request` 自动批准 |

### 关键实现细节

**CLI 启动参数**（`core/webui.go` `start()` 方法）：

```go
args := []string{
    "--output-format", "stream-json",
    "--input-format", "stream-json",
    "--permission-prompt-tool", "stdio",
    "--permission-mode", "bypassPermissions",  // 新增：自动批准所有工具
    "--verbose",
}
```

**自动批准逻辑**（`core/webui.go` `parseEvent()` 方法）：

```go
case "control_request":
    // 自动批准：缓存输入后立即调用 respondPermission 回传 allow
    request, _ := event["request"].(map[string]any)
    if request == nil { break }
    subtype, _ := request["subtype"].(string)
    if subtype != "can_use_tool" { break }

    toolInput, _ := request["input"].(map[string]any)
    requestID, _ := event["request_id"].(string)
    if requestID != "" {
        s.mu.Lock()
        s.pendingInputs[requestID] = toolInput  // 缓存用于 updatedInput
        s.mu.Unlock()
        if err := s.respondPermission(requestID, "allow"); err != nil {
            slog.Warn("webui: auto-approve permission failed", ...)
        }
    }
    // 不再向前端发送 permission_request 消息
```

**变更前 vs 变更后**：

| 方面 | 变更前 | 变更后 |
|------|--------|--------|
| CLI 参数 | 无 `--permission-mode` | `--permission-mode bypassPermissions` |
| control_request 处理 | 转发到前端弹窗 → 等待用户点击 | 自动回传 allow |
| 前端 permission_request | 渲染 Allow/Deny 按钮 | 不再触发（代码保留但不执行） |
| 用户体验 | 频繁弹窗打断工作流 | 全自动执行 |

### Commit

```
dc3facf feat(webui): auto-approve permissions with bypassPermissions mode
```

---

## 3. 工具调用过程显示开关

### 背景

agent 执行过程中会产生大量 `tool_use`（调用工具）和 `tool_result`（工具返回结果）消息。在某些场景下，用户只关心最终结果，不需要看到中间的工具调用过程。现有的 `quiet` 模式会同时隐藏 thinking 和 tool 消息，缺少独立控制工具调用显示的能力。

### 功能描述

新增 `show_tool_process` 配置项，**独立于 quiet 模式**，仅控制 `tool_use` / `tool_result` 消息的显示与隐藏。同时支持 IM 端和 WebUI 端。

### 配置方式

```toml
# IM 端 — 项目级配置
[[projects]]
name = "my-project"
show_tool_process = false   # 默认 true，设为 false 隐藏工具调用过程

# WebUI 端 — 全局配置
[webui]
show_tool_process = false   # 默认 true，设为 false 隐藏工具调用过程
```

### 修改文件

| 文件 | 变更说明 |
|------|----------|
| `config/config.go` | `WebUIConfig` 新增 `ShowToolProcess *bool`；`ProjectConfig` 新增 `ShowToolProcess *bool` |
| `core/engine.go` | Engine 新增 `showToolProcess` 字段 + `SetShowToolProcess()` setter |
| `core/engine_events.go` | `EventToolUse` 和 `EventToolResult` 分支添加 `e.showToolProcess` 条件检查 |
| `core/webui.go` | `WebUIServer` 新增 `showToolProcess` 字段 + setter；`webuiSession` 传递配置；`parseEvent` 中过滤 |
| `cmd/cc-connect/main.go` | IM 端初始化 + 热重载布线；WebUI 端初始化布线 |
| `config.example.toml` | `[webui]` 和 `[[projects]]` 两处添加配置示例 |

### 关键实现细节

**IM 端过滤**（`core/engine_events.go`）：

```go
case EventToolUse:
    toolCount++
    if !quiet && e.showToolProcess {  // 新增 e.showToolProcess 条件
        // ... 格式化并发送工具调用消息 ...
    }

case EventToolResult:
    if !quiet && e.showToolProcess {  // 新增 e.showToolProcess 条件
        // ... 格式化并发送工具结果消息 ...
    }
```

**WebUI 端过滤**（`core/webui.go` `parseEvent()` 方法）：

```go
case "tool_use":
    toolName, _ := block["name"].(string)
    if toolName == "AskUserQuestion" { continue }
    if !s.showToolProcess { continue }  // 新增：跳过 tool_use
    // ... 构建并发送消息 ...

// tool_result 同理
if blockType == "tool_result" {
    if !s.showToolProcess { continue }  // 新增：跳过 tool_result
    // ... 构建并发送消息 ...
}
```

**配置传递链路**（WebUI 端）：

```
config.toml [webui] show_tool_process
    → main.go: cfg.WebUI.ShowToolProcess
    → WebUIServer.SetShowToolProcess(show)
    → WebUIServer.showToolProcess
    → newWebuiSession(..., s.showToolProcess)
    → webuiSession.showToolProcess
    → parseEvent() 中条件判断
```

**配置传递链路**（IM 端）：

```
config.toml [[projects]] show_tool_process
    → main.go: proj.ShowToolProcess
    → Engine.SetShowToolProcess(show)
    → Engine.showToolProcess
    → processInteractiveEvents() 中条件判断
```

### quiet 模式 vs show_tool_process 的关系

| 配置组合 | thinking | tool_use / tool_result | text / result |
|----------|----------|----------------------|---------------|
| `quiet=false, show_tool_process=true` | 显示 | 显示 | 显示 |
| `quiet=false, show_tool_process=false` | 显示 | **隐藏** | 显示 |
| `quiet=true, show_tool_process=true` | 隐藏 | 隐藏（quiet 优先） | 显示 |
| `quiet=true, show_tool_process=false` | 隐藏 | 隐藏 | 显示 |

### Commit

```
6d22bde feat: add show_tool_process config to control tool call display
```

---

## 4. 举一反三：设计模式与经验总结

### 4.1 配置驱动的功能开关模式

本次三个功能都遵循了同一套配置驱动模式，形成了可复用的模板：

```
1. config/config.go    → 定义 *bool 字段（nil 表示使用默认值）
2. core/engine.go      → Engine 新增字段 + Setter
3. core/engine_*.go    → 在事件处理中根据字段值决定行为
4. cmd/main.go         → 初始化 + 热重载两处布线
5. config.example.toml → 添加中英文注释的示例
```

**为什么用 `*bool` 而不是 `bool`**：

- `*bool` 的 nil 状态可以区分「用户未配置」和「用户显式设为 false」
- 未配置时使用代码中的默认值，不需要在 TOML 中强制声明
- 向后兼容：旧配置文件不含新字段时自动使用默认值，不会报错

**新增配置项的标准流程**：

```go
// 1. config.go — 定义字段
type ProjectConfig struct {
    MyNewToggle *bool `toml:"my_new_toggle,omitempty"`
}

// 2. engine.go — 添加字段和 setter
type Engine struct {
    myNewToggle bool
}
func (e *Engine) SetMyNewToggle(v bool) {
    e.myNewToggle = v
}

// 3. main.go — 初始化布线（两处！）
val := true  // 默认值
if proj.MyNewToggle != nil {
    val = *proj.MyNewToggle
}
engine.SetMyNewToggle(val)

// 4. engine_events.go 或其他处理逻辑中使用
if e.myNewToggle {
    // ...
}
```

### 4.2 WebUI 与 IM 端的架构差异

| 维度 | IM 端 | WebUI 端 |
|------|-------|----------|
| 配置层级 | `[[projects]]`（项目级） | `[webui]`（全局级） |
| 事件处理 | `Engine.processInteractiveEvents()` | `webuiSession.parseEvent()` |
| 会话管理 | `Engine` + `interactiveState` | `webuiSession`（独立进程） |
| 配置传递 | `Engine.SetXxx()` setter | `WebUIServer.SetXxx()` → `webuiSession` 构造参数 |
| 热重载 | 支持（`reloadConfig`） | 新会话生效（已运行会话不变） |

**启示**：当一个功能需要同时影响 IM 和 WebUI 时，必须在两条独立的代码路径中分别实现。不能假设共享同一套配置和处理逻辑。

### 4.3 事件过滤的层次化设计

当前系统有三层消息过滤机制，各自职责不同：

```
第 1 层：quiet 模式（粗粒度）
  ├── 隐藏 EventThinking
  ├── 隐藏 EventToolUse
  └── 隐藏 EventToolResult

第 2 层：show_tool_process（细粒度）
  ├── 隐藏 EventToolUse
  └── 隐藏 EventToolResult
  └── 不影响 EventThinking

第 3 层：DisplayConfig 截断（展示层）
  ├── thinking_max_len → 截断 thinking 内容
  └── tool_max_len → 截断 tool 内容
```

**设计原则**：

- **粗粒度开关先判断**（`quiet`），再判断细粒度开关（`showToolProcess`）
- 条件用 `&&` 组合：`if !quiet && e.showToolProcess`
- 截断是展示层的关注点，与是否显示无关
- 未来如需添加 `show_thinking` 等更细粒度的开关，遵循同样的模式即可

### 4.4 权限模式的安全性权衡

WebUI 从「前端弹窗确认」改为「自动批准」，是一个安全性与易用性的权衡：

| 场景 | 推荐模式 | 原因 |
|------|----------|------|
| 本地开发（Vibe Coding） | bypassPermissions | 用户在自己机器上，全自动更高效 |
| 生产环境 IM | autoApprove（现行） | 受限于 IM 交互限制，无法弹窗 |
| 多用户共享环境 | 需要权限确认 | 防止未授权操作 |

**未来扩展方向**：可以将 WebUI 的权限模式也做成配置项（`permission_mode = "bypass" | "prompt"`），让部署者根据安全需求自行选择。

### 4.5 热重载的注意事项

本次所有 IM 端配置都支持热重载（`SIGHUP` 信号触发），但需注意：

1. **正在运行的会话不受影响** — 配置变更只对新的事件循环生效
2. **WebUI 端不支持热重载** — `WebUIServer` 的配置在启动时注入，新会话才使用新配置
3. **布线必须两处** — `main()` 初始化 + `reloadConfig()` 热重载，漏掉任何一处都会导致配置不生效或重载后回退

**检查清单**（每次添加新的可热重载配置时）：

- [ ] `main()` 中初始化布线
- [ ] `reloadConfig()` 中重载布线
- [ ] 两处的默认值逻辑一致
- [ ] Setter 是线程安全的（或确认调用时机不会并发）

---

## 完整 Commit 历史

| Commit | 类型 | 说明 |
|--------|------|------|
| `4469b26` | feat | 添加 session complete 通知到 IM |
| `dc3facf` | feat(webui) | WebUI 自动批准权限，使用 bypassPermissions 模式 |
| `6d22bde` | feat | 添加 show_tool_process 配置，控制工具调用过程显示 |
| `bb0afa2` | docs | 添加多平台配置和 session complete 通知说明 |
