# CC-Connect Core Interfaces Reference

## Required Interfaces

### Platform (core/interfaces.go)

Every messaging platform adapter must implement this interface.

```go
type Platform interface {
    Name() string
    Start(handler MessageHandler) error
    Reply(ctx context.Context, replyCtx any, content string) error
    Send(ctx context.Context, replyCtx any, content string) error
    Stop() error
}
```

### Agent (core/interfaces.go)

Every AI coding agent adapter must implement this interface.

```go
type Agent interface {
    Name() string
    StartSession(ctx context.Context, sessionID string) (AgentSession, error)
    ListSessions(ctx context.Context) ([]AgentSessionInfo, error)
    Stop() error
}
```

### AgentSession

A running bidirectional session with an agent.

```go
type AgentSession interface {
    Send(prompt string, images []ImageAttachment, files []FileAttachment) error
    RespondPermission(requestID string, result PermissionResult) error
    Events() <-chan Event
    CurrentSessionID() string
    Alive() bool
    Close() error
}
```

## Optional Platform Interfaces

Implement only when the platform supports the capability. Core code uses type
assertions with fallback:

```go
if sender, ok := p.(CardSender); ok {
    sender.ReplyCard(ctx, replyCtx, card)
} else {
    p.Reply(ctx, replyCtx, card.ToText())
}
```

### CardSender — Rich card messages

```go
type CardSender interface {
    ReplyCard(ctx context.Context, replyCtx any, card *Card) error
    SendCard(ctx context.Context, replyCtx any, card *Card) error
}
```

Implemented by: feishu, telegram, discord, slack, dingtalk, wecom

### InlineButtonSender — Inline keyboard buttons

```go
type InlineButtonSender interface {
    SendWithButtons(ctx context.Context, replyCtx any, content string, buttons [][]ButtonOption) error
}
```

Implemented by: telegram, discord, feishu

### AsyncRecoverablePlatform — Auto-reconnecting platforms

```go
type AsyncRecoverablePlatform interface {
    SetLifecycleHandler(PlatformLifecycleHandler)
}

type PlatformLifecycleHandler interface {
    OnPlatformReady(p Platform)
    OnPlatformUnavailable(p Platform, err error)
}
```

Implemented by: feishu (WebSocket), telegram (long-polling)

Engine and ProjectRouter both implement PlatformLifecycleHandler.

### CommandRegistrar — Platform-native command registration

```go
type CommandRegistrar interface {
    RegisterCommands(commands []BotCommandInfo) error
}
```

Implemented by: telegram (BotFather commands), discord (slash commands)

### CardNavigable — Card pagination/navigation

```go
type CardNavigable interface {
    SetCardNavigationHandler(handler func(p Platform, sessionKey string, replyCtx any, action string))
}
```

Implemented by: feishu

### ChannelNameResolver — Resolve channel IDs to names

```go
type ChannelNameResolver interface {
    ResolveChannelName(channelID string) (string, error)
}
```

Used by multi-workspace mode for convention-based auto-binding.

### ReplyContextReconstructor — Recreate reply context from session key

```go
type ReplyContextReconstructor interface {
    ReconstructReplyCtx(sessionKey string) (any, error)
}
```

Used by cron jobs and restart notifications to send proactive messages.

## Optional Agent Interfaces

### ProviderSwitcher — Multi-model provider support

```go
type ProviderSwitcher interface {
    SetProviders(providers []ProviderConfig)
    SetActiveProvider(name string) error
    ActiveProvider() string
    ListProviders() []ProviderConfig
}
```

### WorkDirSwitcher — Dynamic working directory changes

```go
type WorkDirSwitcher interface {
    SetWorkDir(dir string)
    GetWorkDir() string
}
```

### AgentDoctorInfo — Health check metadata

```go
type AgentDoctorInfo interface {
    CLIBinaryName() string
    CLIDisplayName() string
}
```

### CommandProvider — Agent-provided custom commands

```go
type CommandProvider interface {
    CommandDirs() []string
}
```

### SkillProvider — Agent-provided skills

```go
type SkillProvider interface {
    SkillDirs() []string
}
```

## Registration Pattern

### Platform Registration

```go
// In platform/myplatform/myplatform.go
package myplatform

import "github.com/chenhg5/cc-connect/core"

func init() {
    core.RegisterPlatform("myplatform", func(opts map[string]any) (core.Platform, error) {
        return New(opts)
    })
}
```

### Agent Registration

```go
// In agent/myagent/myagent.go
package myagent

import "github.com/chenhg5/cc-connect/core"

func init() {
    core.RegisterAgent("myagent", func(opts map[string]any) (core.Agent, error) {
        return New(opts)
    })
}
```

### Build Tag File

```go
// In cmd/cc-connect/plugin_platform_myplatform.go
//go:build !no_myplatform

package main

import _ "github.com/chenhg5/cc-connect/platform/myplatform"
```

## Message Types

### Message (incoming)

```go
type Message struct {
    SessionKey string           // "feishu:{chatID}:{userID}"
    Platform   string
    MessageID  string
    UserID     string
    UserName   string
    ChatName   string
    Content    string
    Images     []ImageAttachment
    Files      []FileAttachment
    Audio      *AudioAttachment
    ReplyCtx   any              // platform-specific, opaque to core
    FromVoice  bool
}
```

### Event (outgoing from agent)

```go
type Event struct {
    Type    EventType
    Content string
    Done    bool
    // ... additional fields per type
}
```

EventTypes: `EventText`, `EventToolUse`, `EventToolResult`, `EventResult`,
`EventError`, `EventPermissionRequest`, `EventThinking`

## Test Stub Reference

Stubs in `core/engine_test.go` (package-internal):

| Stub | Interfaces |
|------|-----------|
| `stubAgent` | Agent |
| `stubAgentSession` | AgentSession |
| `stubPlatformEngine` | Platform |
| `stubInlineButtonPlatform` | Platform + InlineButtonSender |
| `stubCardPlatform` | Platform + CardSender |
| `resultAgent` | Agent (returns specific session) |
| `resultAgentSession` | AgentSession (returns specific result) |
| `stubWorkDirAgent` | Agent + WorkDirSwitcher |

Stubs in `core/project_router_test.go`:

| Stub | Interfaces |
|------|-----------|
| `stubRouterPlatform` | Platform (with simulateMessage helper) |
| `stubButtonRouterPlatform` | Platform + InlineButtonSender |
| `stubAsyncRouterPlatform` | Platform + AsyncRecoverablePlatform |
