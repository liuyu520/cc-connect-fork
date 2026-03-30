# ProjectRouter Architecture Reference

## Dependency Direction

```
cmd/cc-connect/main.go  → core/, agent/*, platform/*
core/project_router.go  → core/ only (same package)
core/engine.go          → stdlib only
agent/*                 → core/
platform/*              → core/
```

ProjectRouter lives in `core/` and only uses core interfaces. It never imports
agent or platform packages.

## Core Interfaces Used by ProjectRouter

### Platform (core/interfaces.go)

```go
type Platform interface {
    Name() string
    Start(handler MessageHandler) error
    Reply(ctx context.Context, replyCtx any, content string) error
    Send(ctx context.Context, replyCtx any, content string) error
    Stop() error
}
```

### Optional Interfaces

```go
// InlineButtonSender — used for button-based project selection
type InlineButtonSender interface {
    SendWithButtons(ctx context.Context, replyCtx any, content string, buttons [][]ButtonOption) error
}

// AsyncRecoverablePlatform — used for lifecycle propagation (feishu, telegram)
type AsyncRecoverablePlatform interface {
    SetLifecycleHandler(PlatformLifecycleHandler)
}

// PlatformLifecycleHandler — ProjectRouter implements this
type PlatformLifecycleHandler interface {
    OnPlatformReady(p Platform)
    OnPlatformUnavailable(p Platform, err error)
}

// BaseSessionKeyer — used for thread-isolation fallback binding lookup
// Platforms with thread isolation (Feishu, Discord) implement this to provide
// a broader user-in-chat key so /project switches apply across threads.
type BaseSessionKeyer interface {
    BaseSessionKey(msg *Message) string
}
```

## Engine External Platform Integration

### Fields

```go
type Engine struct {
    // ...
    externalPlatforms map[Platform]bool  // platforms managed by ProjectRouter
    // ...
}
```

### Methods

```go
// SetExternalPlatform marks platform as router-managed (skip Start/Stop)
func (e *Engine) SetExternalPlatform(p Platform)

// HandleIncomingMessage allows router to inject messages into engine
func (e *Engine) HandleIncomingMessage(p Platform, msg *Message)

// OnPlatformReady / OnPlatformUnavailable — public wrappers for lifecycle
func (e *Engine) OnPlatformReady(p Platform)
func (e *Engine) OnPlatformUnavailable(p Platform, err error)
```

### Start/Stop Guard

Both `Engine.Start()` and `Engine.Stop()` skip platforms in `externalPlatforms`:
```go
for _, p := range e.platforms {
    if e.externalPlatforms[p] {
        continue // managed by ProjectRouter
    }
    // ... normal start/stop logic
}
```

The "all platforms failed" check uses `attemptedCount` (excludes external)
instead of `len(e.platforms)`.

## Platform Deduplication in main.go

### Cache Key Generation

```go
func platformConfigKey(platType string, options map[string]any) string {
    optBytes, _ := json.Marshal(options) // json.Marshal sorts map keys
    return platType + ":" + string(optBytes)
}
```

Uses `pc.Options` (original config options, excluding `cc_data_dir`/`cc_project`)
so identical credentials produce the same key across projects.

### Shared Group Detection

```go
sharedGroups := make(map[string][]sharedGroupEntry)
// After all engines created:
for pkey, group := range sharedGroups {
    if len(group) <= 1 { continue } // not shared
    // Create ProjectRouter for this group
}
```

### Lifecycle Order

1. Create all engines (platform instances cached/shared)
2. Create ProjectRouters for shared groups, call `engine.SetExternalPlatform()`
3. Start engines (skip external platforms)
4. Start ProjectRouters (starts shared platforms)
5. On shutdown: stop engines first, then stop ProjectRouters

## Concurrency Model

- `ProjectRouter.mu` (sync.RWMutex) protects `bindings` and `pending` maps
- `RLock` for reads (handleMessage routing, handleSelection check)
- `Lock` for writes (binding update, pending add/remove)
- `engineMap` and `engines` are write-once during init, read-only after Start()
- `saveBindings()` copies data under RLock, then writes file outside lock

## i18n Integration

All user-facing strings use MsgKey constants:

| Key | Usage |
|-----|-------|
| `MsgProjectSelect` | "Please select a project:" |
| `MsgProjectCurrent` | "Current project: **%s**" (Tf with project name) |
| `MsgProjectSwitched` | "Switched to project: **%s**" (Tf with project name) |
| `MsgProjectInvalid` | "Invalid selection, please try again." |
| `MsgProjectList` | "Available projects:" |
| `MsgProjectHelp` | "`/project [name]` — switch project" |

Translations exist for: EN, ZH, ZH-TW, JA, ES.

## Test Stub Patterns

Tests in `core/project_router_test.go` use package-internal stubs:

```go
// Basic platform stub (records sent messages, captures handler)
type stubRouterPlatform struct { ... }

// Platform with InlineButtonSender support
type stubButtonRouterPlatform struct { ... }

// Platform with AsyncRecoverablePlatform support
type stubAsyncRouterPlatform struct { ... }

// Platform with BaseSessionKeyer support (thread isolation fallback)
type stubBaseSessionKeyPlatform struct { ... }
```

Helper function `newTestRouter(t, platform, projectNames...)` creates a router
with N engines for quick test setup.

## BaseSessionKeyer Integration

### Router Methods

```go
// baseSessionKey queries the platform for a broader key.
// Returns msg.SessionKey if platform doesn't implement BaseSessionKeyer.
func (r *ProjectRouter) baseSessionKey(msg *Message) string

// setBinding stores both exact and base key bindings.
// Called by handleProjectCommand, handleButtonCallback, handleSelection.
func (r *ProjectRouter) setBinding(msg *Message, projectName string)
```

### Binding Lookup Fallback Chain

```
handleMessage():
  1. Check bindings[msg.SessionKey]     → exact thread match
  2. Check bindings[baseSessionKey(msg)] → user-in-chat fallback
  3. Check pending[msg.SessionKey]       → pending selection
  4. Auto-bind (single project) or show selection (multi-project)
```

### Storage

Base key bindings live in the same `bindings` map and persist in the same
JSON file as exact bindings. No schema change required.
