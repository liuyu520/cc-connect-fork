---
name: project-router
description: >
  This skill should be used when the user asks about "shared platform",
  "multi-project", "project router", "ProjectRouter", "platform deduplication",
  "/project command", "project selection", "project binding",
  "multiple projects sharing one bot", or needs to debug, extend, or understand
  the multi-project shared platform routing system.
---

# ProjectRouter — Multi-Project Shared Platform Routing

## Purpose

ProjectRouter enables multiple `[[projects]]` in config.toml to share the same
IM platform credentials (e.g. one Feishu bot serving three projects). It
maintains a single platform connection and routes incoming messages to the
correct Engine based on per-session bindings.

## Architecture Overview

```
Platform (1 connection)  →  ProjectRouter  →  Engine A (project-a)
                                           →  Engine B (project-b)
                                           →  Engine C (project-c)
```

### Key Files

| File | Role |
|------|------|
| `core/project_router.go` | Core routing logic, binding persistence, `/project` command, `baseSessionKey()`, `setBinding()` |
| `core/project_router_test.go` | Unit tests (26 test cases, including 4 BaseSessionKeyer tests) |
| `core/interfaces.go` | `BaseSessionKeyer` optional interface for thread isolation fallback |
| `core/engine.go` | `externalPlatforms` field, `SetExternalPlatform()`, `HandleIncomingMessage()` |
| `core/i18n.go` | `MsgProjectSelect`, `MsgProjectCurrent`, `MsgProjectSwitched`, `MsgProjectInvalid`, `MsgProjectList`, `MsgProjectHelp` |
| `cmd/cc-connect/main.go` | Platform cache deduplication (`platformConfigKey`), router creation, lifecycle |

### Message Flow

1. Platform delivers message to `ProjectRouter.handleMessage()`
2. Router checks for `/project` command → intercepts if matched
3. Router checks `bindings[sessionKey]` → routes to bound engine if found
4. If no exact binding, checks `bindings[baseSessionKey]` for fallback (thread isolation support)
5. Router checks `pending[sessionKey]` → handles selection response if pending
6. Single project → auto-bind; multiple projects → show selection UI

### Platform Deduplication (main.go)

`platformConfigKey(type, options)` generates a deterministic key from platform
type + JSON-marshaled options. Platforms with identical keys share one instance.
The `sharedGroups` map tracks which engines share each platform. Only groups
with `len > 1` create a ProjectRouter.

### Engine Integration

- `Engine.SetExternalPlatform(p)` marks a platform as router-managed
- `Engine.Start()` / `Engine.Stop()` skip external platforms
- `Engine.HandleIncomingMessage(p, msg)` allows the router to inject messages
- `Engine.OnPlatformReady(p)` / `Engine.OnPlatformUnavailable(p, err)` are called
  by the router's lifecycle handler to propagate async platform state

### Binding Persistence

Bindings are stored as JSON in `{dataDir}/project_bindings_{hash}.json`:
```json
{"bindings": {"feishu:chat1:user1": "project-a"}}
```
On startup, `loadBindings()` restores only bindings whose project still exists.

### `/project` Command

| Command | Behavior |
|---------|----------|
| `/project` | Show current project + numbered list |
| `/project list` | Same as above |
| `/project <name>` | Switch to named project (case-insensitive) |
| `/project <number>` | Switch by index (1-based) |

### BaseSessionKeyer (Thread Isolation Support)

When platforms use thread isolation (e.g., Feishu `thread_isolation`), each
top-level message gets a unique session key. Without special handling, `/project`
switches only apply to one thread. The `BaseSessionKeyer` optional interface
solves this:

- `baseSessionKey(msg)` calls `platform.BaseSessionKey(msg)` if implemented
- `setBinding(msg, project)` stores both exact key and base key bindings
- Lookup order: exact session key → base session key → show selection
- Feishu implements this: `feishu:chatId:root:msgId` → `feishu:chatId:userId`

### Project Selection UI

- Platforms implementing `InlineButtonSender` get button-based selection
  (button data: `__project__:<name>`)
- Fallback: plain-text numbered list
- After selection, the original cached message is forwarded to the chosen engine

## Extending the Router

### Adding new selection UI styles

To support card-based selection, check for `CardSender` in
`showProjectSelection()` before the `InlineButtonSender` check.

### Adding new subcommands

Extend `handleProjectCommand()` with additional `args` matching. Follow the
pattern: parse args → validate → update state → reply.

### Adding i18n strings

Define `MsgKey` constant in `core/i18n.go`, add translations for all 5
languages (EN, ZH, ZH-TW, JA, ES), then use `r.i18n.T(key)` or
`r.i18n.Tf(key, args...)`.

## Testing

Run ProjectRouter tests:
```bash
go test ./core/ -v -run "TestProjectRouter"
```

Test categories cover:
- Auto-bind (single project)
- Selection UI (multi-project, text + buttons)
- Selection by number / name / prefix
- Invalid selection retry
- `/project` command (list, switch, invalid)
- Binding persistence (save, restore, stale cleanup)
- Async platform lifecycle propagation
- matchProject table-driven tests
- i18n (Chinese locale)
- BaseSessionKeyer: thread isolation fallback, exact key priority, non-thread unchanged, project list via base key

## Additional Resources

### Reference Files

For detailed architecture patterns and conventions:
- **`references/architecture.md`** — Dependency rules, interface patterns, registration flow

### Related Skills

- **`session-key-architecture`** — How session keys are constructed and affect routing
- **`message-flow-architecture`** — What happens after ProjectRouter routes a message to an Engine
