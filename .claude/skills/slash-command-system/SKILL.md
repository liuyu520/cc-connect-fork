---
name: slash-command-system
description: >
  This skill should be used when the user asks about "slash command",
  "builtin command", "builtinCommandNames", "builtinCommandDefs",
  "handleCommand", "command routing", "command registration",
  "command dispatch", "add a command", "add slash command",
  "composite command", "command aliases", "/help", "command i18n",
  "disabled commands", "command prefix matching", "matchPrefix",
  "matchBuiltinPrefix", "initBuiltinCommands", "commandHandler",
  "builtinCommandDef", "command registry pattern",
  or needs to debug, extend, or understand how built-in slash commands are
  registered, routed, and rendered in the help menu.
---

# Slash Command System

## Purpose

Document the architecture of cc-connect's built-in slash command system:
how commands are registered via the registry pattern, how user input is matched
and routed to handlers, how command descriptions are internationalized, and
common patterns for adding new commands.

## Key Files

| File | Role |
|------|------|
| `core/engine_commands.go` | `builtinCommandNames`, `builtinCommandDef` type, `commandHandler` type, `initBuiltinCommands()`, `handleCommand()` dispatch, `matchPrefix()`, `matchBuiltinPrefix()`, all `cmd*()` handlers, `GetAllCommands()` |
| `core/engine.go` | Engine struct (`builtinCommandDefs` field), `NewEngine()` calls `initBuiltinCommands()`, `resolveDisabledCmds()` |
| `core/i18n.go` | `MsgBuiltinCmd*` constants + 5-language translations for command descriptions |
| `core/engine_cards.go` | `renderHelpCard()`, `renderHelpGroupCard()`, `helpCardGroups()` — help menu card rendering |

## Command Registration — Registry Pattern

Commands are registered in two layers:

### Layer 1: Name/ID Registry (`builtinCommandNames`)

A package-level variable used by `resolveDisabledCmds` and `matchPrefix`:

```go
// core/engine_commands.go
var builtinCommandNames = []struct {
    names []string  // aliases, first = canonical name
    id    string    // unique command identifier
}{
    {[]string{"new"}, "new"},
    {[]string{"list", "sessions"}, "list"},
    {[]string{"resume"}, "resume"},
    // ...
}
```

### Layer 2: Handler Registry (`builtinCommandDefs`)

An Engine instance field initialized in `initBuiltinCommands()`, binding each command
to its handler function:

```go
// core/engine_commands.go

// commandHandler 是内建命令的统一处理函数签名。
type commandHandler func(p Platform, msg *Message, args []string)

// builtinCommandDef 定义一个内建命令及其处理器。
type builtinCommandDef struct {
    names      []string       // 命令名（第一个为主名称，其余为别名）
    id         string         // 命令唯一 ID
    handler    commandHandler // 命令处理函数
    privileged bool           // 需要 admin_from 授权
}
```

`initBuiltinCommands()` is called at the end of `NewEngine()`:

```go
func (e *Engine) initBuiltinCommands() {
    e.builtinCommandDefs = []builtinCommandDef{
        {names: []string{"new"}, id: "new", handler: e.cmdNew},
        {names: []string{"list", "sessions"}, id: "list", handler: e.cmdList},
        {names: []string{"resume"}, id: "resume", handler: func(p Platform, msg *Message, args []string) {
            if len(args) == 0 { e.cmdList(p, msg, args) } else { e.cmdSwitch(p, msg, args) }
        }},
        // ... all commands ...
        {names: []string{"shell", "sh", "exec", "run"}, id: "shell",
            handler: func(p Platform, msg *Message, _ []string) { e.cmdShell(p, msg, msg.Content) },
            privileged: true},
    }
}
```

### Registration Rules

- **`names`**: First entry is the canonical name; additional entries are aliases (e.g., `"list"` + `"sessions"`)
- **`id`**: Used for: command lookup, i18n lookup (`MsgKey(primaryName)`), disabled commands check
- **`handler`**: Unified `commandHandler` signature; commands with different signatures use closure adapters
- **`privileged`**: Replaces the old `privilegedCommands` map; checked in `handleCommand()` before dispatch
- **Order matters**: Commands appear in `/help` in registration order

### Closure Adapters for Special Signatures

Commands that don't take `args` or need special parameters use closures:

```go
// No-args command (signature: p, msg)
{names: []string{"stop"}, id: "stop", handler: func(p Platform, msg *Message, _ []string) {
    e.cmdStop(p, msg)
}},

// Needs raw message content instead of args
{names: []string{"shell", "sh", "exec", "run"}, id: "shell",
    handler: func(p Platform, msg *Message, _ []string) {
        e.cmdShell(p, msg, msg.Content)
    }, privileged: true},

// Conditional dispatch (composite command)
{names: []string{"resume"}, id: "resume", handler: func(p Platform, msg *Message, args []string) {
    if len(args) == 0 { e.cmdList(p, msg, args) } else { e.cmdSwitch(p, msg, args) }
}},

// Extra pre-check
{names: []string{"workspace", "ws"}, id: "workspace", handler: func(p Platform, msg *Message, args []string) {
    if !e.multiWorkspace { e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsNotEnabled)); return }
    e.handleWorkspaceCommand(p, msg, args)
}},
```

## Command Routing

`handleCommand()` uses registry lookup instead of a switch statement:

```
User types: "/resume 1"
  → parts = ["/resume", "1"]
  → cmd = "resume"
  → args = ["1"]
  → cmdID = e.matchBuiltinPrefix("resume") → "resume"
  → find entry in e.builtinCommandDefs where id == "resume"
  → check disabled, check privileged
  → entry.handler(p, msg, args)
```

### matchBuiltinPrefix Algorithm

`matchBuiltinPrefix()` is an Engine method that searches `e.builtinCommandDefs`:

1. **Exact match first**: Check all `names` in all entries for exact string match
2. **Prefix match**: If no exact match, find entries where any name starts with the input
3. **Ambiguity**: If multiple entries match the prefix, return `""` (no match)

### Privileged Commands

Privileged commands (requiring `admin_from` authorization) are marked with
`privileged: true` in `builtinCommandDef`:

```go
// Currently privileged: shell, dir, restart, upgrade
{names: []string{"shell", ...}, id: "shell", handler: ..., privileged: true},
```

This replaces the old `privilegedCommands` map. The check happens in `handleCommand()`:

```go
if entry != nil && entry.privileged && !e.isAdmin(msg.UserID) {
    e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgAdminRequired), "/"+cmdID))
    return true
}
```

### Disabled Commands

Commands can be disabled per-project or per-role. `resolveDisabledCmds()` in
`engine.go` uses `builtinCommandNames` (the name-only registry) to resolve
wildcards:

```go
if c == "*" {
    for _, bc := range builtinCommandNames {
        m[bc.id] = true
    }
}
```

## Command Patterns

### Simple Command (Direct Handler)

```go
// In initBuiltinCommands:
{names: []string{"model"}, id: "model", handler: e.cmdModel},

// Handler (signature matches commandHandler):
func (e *Engine) cmdModel(p Platform, msg *Message, args []string) { ... }
```

### Composite Command (Delegate Pattern)

```go
{names: []string{"resume"}, id: "resume", handler: func(p Platform, msg *Message, args []string) {
    if len(args) == 0 { e.cmdList(p, msg, args) } else { e.cmdSwitch(p, msg, args) }
}},
```

### Subcommand Pattern

```go
{names: []string{"provider"}, id: "provider", handler: e.cmdProvider},

func (e *Engine) cmdProvider(p Platform, msg *Message, args []string) {
    sub := matchSubCommand(strings.ToLower(args[0]), []string{
        "list", "add", "remove", "switch", ...
    })
    switch sub {
    case "list": ...
    case "add": ...
    }
}
```

### Command with Aliases

```go
{names: []string{"compress", "compact"}, id: "compress", handler: ...},
{names: []string{"delete", "del", "rm"}, id: "delete", handler: e.cmdDelete},
```

## i18n for Command Descriptions

Each command needs a `MsgKey` constant and 5-language translations:

```go
// 1. Constant in i18n.go (MsgKey value MUST equal the command id)
MsgBuiltinCmdResume MsgKey = "resume"

// 2. Translations in the messages map
MsgBuiltinCmdResume: {
    LangEnglish:            "Resume a session: ...",
    LangChinese:            "恢复会话：...",
    LangTraditionalChinese: "恢復會話：...",
    LangJapanese:           "セッション復元：...",
    LangSpanish:            "Reanudar sesión: ...",
},
```

**Critical**: The `MsgKey` value must match the command's `id`, because the help
system uses `MsgKey(primaryName)` for lookup.

## Adding a New Built-in Command — Checklist

**Only 1 place to modify** (was 3 before the registry refactoring):

1. **Add entry in `initBuiltinCommands()`** in `core/engine_commands.go`:
   ```go
   {names: []string{"mycommand", "alias"}, id: "mycommand", handler: e.cmdMyCommand},
   ```
   - Set `privileged: true` if it requires admin authorization
   - Use closure adapter if the handler signature doesn't match `commandHandler`
2. **Add entry in `builtinCommandNames`** (same file, package-level var) for disabled-command resolution
3. **Implement handler** `func (e *Engine) cmdMyCommand(p Platform, msg *Message, args []string)`
4. **Add i18n MsgKey** constant + 5-language translations in `core/i18n.go`
5. **Verify**: `go build ./...` and `go test ./core/ -v`

## Session Management Commands

| Command | Behavior | Handler |
|---------|----------|---------|
| `/new [name]` | Start a new session | `cmdNew` |
| `/list` | Show all sessions | `cmdList` |
| `/resume [arg]` | No arg = list; with arg = switch | closure → `cmdList` / `cmdSwitch` |
| `/switch <num\|id\|name>` | Switch to a specific session | `cmdSwitch` |
| `/search <keyword>` | Search sessions by name/ID | `cmdSearch` |
| `/delete <num>` | Delete session(s) | `cmdDelete` |
| `/name [num] <text>` | Name a session | `cmdName` |
| `/current` | Show current active session | `cmdCurrent` |

## Debugging

| Symptom | Check |
|---------|-------|
| Command not recognized | Is it in `builtinCommandNames`? In `initBuiltinCommands()`? Prefix collision? |
| Command description missing in help | Is `MsgKey` value == command `id`? Are translations added? |
| Command disabled unexpectedly | Check `disabled_commands` in config, check user role permissions |
| Alias not working | Verify alias is in the `names` slice, not just the `id` |
| Privileged check not working | Is `privileged: true` set in the `builtinCommandDef` entry? |

## Additional Resources

### Related Skills

- **`add-new-feature`** — General feature addition workflow (Step 3: Add i18n Strings)
- **`message-flow-architecture`** — How messages reach `handleCommand()` in the first place
