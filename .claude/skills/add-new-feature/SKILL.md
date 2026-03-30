---
name: add-new-feature
description: >
  This skill should be used when the user asks to "add a feature",
  "implement a new command", "add a new platform", "add a new agent",
  "create a new module", "extend the engine", "add i18n strings",
  "add cross-cutting functionality", or needs guidance on the cc-connect
  project architecture, coding conventions, and development workflow.
---

# Adding New Features to CC-Connect

## Purpose

Provide procedural guidance for implementing new features in cc-connect,
ensuring adherence to the plugin architecture, dependency rules, and coding
conventions documented in CLAUDE.md.

## Architecture Quick Reference

```
cmd/cc-connect/     → entry point, CLI, wiring
config/             → TOML config parsing
core/               → engine, interfaces, i18n, cards, sessions, registry
agent/X/            → per-agent adapter (claudecode, codex, gemini, ...)
platform/X/         → per-platform adapter (feishu, telegram, discord, ...)
daemon/             → systemd/launchd service management
```

### Engine File Layout

The Engine logic is split across multiple files for maintainability:

| File | Content |
|------|---------|
| `core/engine.go` | Engine struct, `NewEngine()`, `Set*()` methods, lifecycle (`Start`/`Stop`), `handleMessage()` entry point |
| `core/engine_commands.go` | Command registry (`builtinCommandNames`, `builtinCommandDef`, `initBuiltinCommands()`), `handleCommand()` dispatch, all `cmd*()` handlers, `GetAllCommands()` |
| `core/engine_cards.go` | `handleCardNav()`, `executeCardAction()`, all `render*Card()` functions, delete mode, help card groups |
| `core/engine_session.go` | Type definitions (`interactiveState`, `pendingPermission`, `queuedMessage`), session management (`processInteractiveMessage`, `getOrCreateInteractiveStateWith`, etc.) |
| `core/engine_events.go` | `processInteractiveEvents()` event loop, `drainPendingMessages()` |
| `core/engine_util.go` | `send()`, `reply()`, `sendPermissionPrompt()`, `HandleRelay()`, `splitMessage()`, message utilities |
| `core/engine_workspace.go` | `buildSenderPrompt()`, `commandContext()`, `resolveWorkspace()`, workspace helpers |

### Dependency Rules (Critical)

```
cmd/       → config/, core/, agent/*, platform/*
agent/*    → core/   (NEVER other agents or platforms)
platform/* → core/   (NEVER other platforms or agents)
core/      → stdlib  (NEVER agent/ or platform/)
```

### Plugin Registration Pattern

Agents and platforms register via `init()` in their packages:
```go
func init() {
    core.RegisterAgent("myagent", func(opts map[string]any) (core.Agent, error) { ... })
    core.RegisterPlatform("myplatform", func(opts map[string]any) (core.Platform, error) { ... })
}
```

Import controlled by build-tag files in `cmd/cc-connect/plugin_*.go`.

## Feature Implementation Checklist

### Step 1: Identify Feature Scope

Determine which layer the feature touches:

| Scope | Files to modify |
|-------|----------------|
| New slash command | `core/engine_commands.go` (handler + `initBuiltinCommands` + `builtinCommandNames`) |
| New i18n strings | `core/i18n.go` (MsgKey + 5 translations) |
| New platform | `platform/X/`, `cmd/cc-connect/plugin_platform_X.go`, Makefile |
| New agent | `agent/X/`, `cmd/cc-connect/plugin_agent_X.go`, Makefile |
| New optional capability | `core/interfaces.go` (interface), implementing packages |
| Cross-cutting concern | `core/` (new file), `cmd/cc-connect/main.go` (wiring) |
| Engine behavior change | `core/engine.go` or relevant `engine_*.go`, `core/engine_test.go` |
| Card rendering | `core/engine_cards.go` |
| Session/event logic | `core/engine_session.go` or `core/engine_events.go` |
| WebUI / Vibe Coding | `core/webui.go`, `web/src/pages/VibeCoding/`, `config/config.go` |
| Management API field | `core/management.go` (`handleProjects` / `handleProjectDetail`), `web/src/api/projects.ts` |

### Step 2: Follow Interface-Based Design

Never hardcode platform or agent names in `core/`. Use capability interfaces:

```go
// BAD — hardcodes platform knowledge in core
if p.Name() == "feishu" { ... }

// GOOD — capability-based check
if sender, ok := p.(InlineButtonSender); ok { ... }
```

To add a new optional capability:
1. Define interface in `core/interfaces.go`
2. Implement in the relevant platform/agent package
3. Use type assertion in core with graceful fallback

### Step 3: Add i18n Strings

All user-facing text must go through `core/i18n.go`:

1. Add `MsgKey` constant after existing constants
2. Add translations in the `messages` map for all 5 languages:
   - `LangEnglish`, `LangChinese`, `LangTraditionalChinese`, `LangJapanese`, `LangSpanish`
3. Use `e.i18n.T(MsgKey)` or `e.i18n.Tf(MsgKey, args...)` in engine code

### Step 4: Wire in main.go

For cross-cutting features (like ProjectRouter):
1. Add detection/creation logic after engine creation loop
2. Add lifecycle start after engines start
3. Add lifecycle stop in shutdown section (correct order matters)
4. Add any new config fields in `config/config.go`

For per-engine features:
1. Add `Set*()` method on Engine
2. Call it in the engine creation loop in main.go
3. Add config reload support in `reloadConfig()` if hot-reloadable

### Step 5: Write Tests

Follow existing test patterns in `core/engine_test.go`:

- Use `stubPlatformEngine` for basic platform stubs
- Use `stubInlineButtonPlatform` for button-capable platforms
- Use `stubAgent` / `stubAgentSession` for agent stubs
- Use `newResultAgentSession(result)` for sessions that return a specific result
- Tests are in the `core` package (internal, can access unexported fields)

Test naming convention: `TestFeatureName_ScenarioDescription`

### Step 6: Verify

```bash
go build ./...           # compilation
go test ./...            # all tests pass
go test ./core/ -v -run "TestMyFeature"  # specific tests
```

## Common Patterns

### Adding a Slash Command (Registry Pattern)

**Only 2 places to modify** (previously 3 with the old switch-case pattern):

1. **Add entry in `initBuiltinCommands()`** (in `core/engine_commands.go`):
   ```go
   {names: []string{"mycommand", "alias"}, id: "mycommand", handler: e.cmdMyCommand},
   ```
   - Set `privileged: true` if admin authorization required
   - Use closure adapter if handler signature differs from `commandHandler`

2. **Add entry in `builtinCommandNames`** (same file, package-level var):
   ```go
   {[]string{"mycommand", "alias"}, "mycommand"},
   ```

3. **Implement handler** `func (e *Engine) cmdMyCommand(p Platform, msg *Message, args []string)`

4. **Add i18n**: `MsgBuiltinCmdMyCommand MsgKey = "mycommand"` constant + 5-language translations

5. **Add tests**

See **`slash-command-system`** skill for the full registry pattern, closure adapters,
and composite command examples.

### Adding Engine Configuration

```go
// 1. Add field to Engine struct (in engine.go)
type Engine struct {
    myFeatureEnabled bool
    // ...
}

// 2. Add setter (in engine.go)
func (e *Engine) SetMyFeature(enabled bool) {
    e.myFeatureEnabled = enabled
}

// 3. Wire in main.go engine loop
engine.SetMyFeature(proj.MyFeature)

// 4. Add to reloadConfig() if hot-reloadable
```

### Adding a Card Render Function

New card rendering functions go in `core/engine_cards.go`:

```go
func (e *Engine) renderMyCard(sessionKey string) *Card {
    // Use listSessionsCached() for session data in card callbacks (3s timeout!)
    // Use NewCard() builder for card construction
}
```

Remember the card callback performance constraint — see **`card-callback-performance`** skill.

### Cross-Cutting Feature (like ProjectRouter)

1. Create new file in `core/` (e.g., `core/my_feature.go`)
2. Define struct with necessary dependencies (Platform, I18n, etc.)
3. Implement lifecycle: `New*()`, `Start()`, `Stop()`
4. Add Engine integration methods if needed
5. Wire creation and lifecycle in `main.go`
6. Create `core/my_feature_test.go` with dedicated stubs

### Adding a Management API Field

When the frontend needs data from config/engine (e.g., project `work_dir`):

1. **Backend** (`core/management.go`): Add field to `handleProjects()` or
   `handleProjectDetail()` response map, using capability interface check:
   ```go
   workDir := ""
   if wd, ok := e.agent.(interface{ GetWorkDir() string }); ok {
       workDir = wd.GetWorkDir()
   }
   projects = append(projects, map[string]any{
       "work_dir": workDir,
       // ... existing fields ...
   })
   ```
2. **API type** (`web/src/api/projects.ts`): Add field to `ProjectSummary`
   or `ProjectDetail` interface
3. **Frontend** (`web/src/pages/*/`): Call API via `listProjects()` or
   `getProject()`, render the data
4. **i18n**: Add any new user-facing labels to all 5 locale files

See **`webui-vibe-coding`** skill for a complete worked example (project dropdown).

## Error Handling Conventions

- Wrap errors with context: `fmt.Errorf("feishu: reply card: %w", err)`
- Use `slog.Error` / `slog.Warn` for logging (never `log.Printf`)
- Redact tokens: `core.RedactToken(token)`
- Never silently swallow errors

## Concurrency Conventions

- Protect shared state with `sync.Mutex` or `sync.RWMutex`
- Use `context.Context` for cancellation
- Use `sync.Once` for one-time teardown
- Document channel ownership (who closes)

## Additional Resources

### Reference Files

- **`references/interfaces.md`** — Complete list of core interfaces and when to use them

### Related Skills

- **`session-key-architecture`** — How session keys are constructed across platforms
- **`project-router`** — Multi-project shared platform routing system
- **`message-flow-architecture`** — End-to-end message processing pipeline, stdio protocol, auth
- **`slash-command-system`** — Command registry pattern, routing, dispatch, closure adapters
- **`card-callback-performance`** — IM card callback 3s timeout constraint and caching pattern
- **`webui-vibe-coding`** — Browser-based Vibe Coding interface (WebSocket protocol, Go backend, React frontend)
