# Code Organization Refactoring Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Improve codebase maintainability by splitting oversized files, extracting repeated patterns, and reducing cognitive load — without changing any runtime behavior.

**Architecture:** Pure refactoring. All changes are file-level reorganization within existing packages. No new packages, no interface changes, no behavior changes. Every step must pass `go build ./...` and `go test ./...`.

**Tech Stack:** Go (same as existing)

---

## File Structure

### engine_commands.go split (core/)

Current: `core/engine_commands.go` (4102 lines, ~80 functions)

After split:

| New File | Content | Est. Lines |
|----------|---------|------------|
| `core/engine_commands.go` | Command infrastructure: types, `builtinCommandNames`, `initBuiltinCommands`, `handleCommand`, `matchBuiltinPrefix`, `matchPrefix`, `matchSubCommand`, `isBtwCommand`, `matchBtwPrefix`, `GetAllCommands` | ~300 |
| `core/engine_cmd_session.go` | Session commands: `cmdNew`, `cmdList`, `cmdSwitch`, `matchSession`, `cmdName`, `cmdCurrent`, `cmdStatus`, `cmdHistory`, `cmdSearch`, `cmdStop`, `cmdDelete` + delete helpers, `listPageSize` const | ~650 |
| `core/engine_cmd_provider.go` | Provider/Model/Mode commands: `cmdModel`, `resolveModelAlias`, `parseModelSwitchArgs`, `switchModel`, `cmdReasoning`, `cmdMode`, `applyLiveModeChange`, `cmdProvider`, `cmdProviderAdd`, `cmdProviderRemove`, `switchProvider` | ~500 |
| `core/engine_cmd_cron.go` | Cron + Heartbeat commands: `cmdCron` + 7 sub-handlers, `cmdHeartbeat`, `cmdHeartbeatStatusText`, `heartbeatLocalizedHelpers` | ~400 |
| `core/engine_cmd_compress.go` | Compress commands: `cmdCompress`, `runCompress`, `processCompressEvents`, `drainQueuedMessagesAfterCompress` | ~200 |
| `core/engine_cmd_custom.go` | Custom commands, skills, aliases: `executeCustomCommand`, `executeShellCommand`, `cmdCommands` + sub-handlers, `executeSkill`, `cmdSkills`, `cmdAlias` + sub-handlers | ~400 |
| `core/engine_cmd_system.go` | Config, workspace, bind, admin, misc: `configItem` type, `cmdConfig`, `cmdConfigReload`, `handleWorkspaceCommand`, `cmdBind` + helpers, `setupMemoryFile`, `setupResult` type, `cmdDoctor`, `cmdUpgrade`, `cmdUpgradeConfirm`, `cmdRestart`, `cmdShell`, `dirApply`, `cmdDir`, `cmdHelp`, `cmdLang`, `cmdQuiet`, `cmdTTS`, `cmdAllow`, `cmdMemory`, `showMemoryFile`, `appendMemoryFile`, `cmdUsage` + usage helpers, `cmdWhoami`, `formatWhoamiText`, `dirCardPageSize` const | ~1650 |

### config.go refactoring (config/)

Current: `config/config.go` (1731 lines)

After refactoring:
- Add `readAndParse() (*Config, []byte, error)` helper (~15 lines)
- Add `mutateAndSave(fn func(*Config)) error` helper (~15 lines)
- 12 standard-pattern Save/Add/Remove functions reduce from ~8-10 lines each to ~3-5 lines each
- Net reduction: ~120 lines

### Platform opts helpers (core/)

New file: `core/opts_helpers.go` (~40 lines)

- `OptsString(opts map[string]any, key string) string`
- `OptsBool(opts map[string]any, key string) bool`

---

## Task 1: Split engine_commands.go — Infrastructure file

**Files:**
- Modify: `core/engine_commands.go` (keep only infrastructure)
- Create: `core/engine_cmd_session.go`

- [ ] **Step 1.1: Create engine_cmd_session.go with session command functions**

Move these functions from `engine_commands.go` to new `engine_cmd_session.go`:
- `listPageSize` const (line 598)
- `cmdNew` (567-596)
- `cmdList` (603-692)
- `cmdSwitch` (694-738)
- `matchSession` (746-789)
- `cmdSearch` (1020-1103)
- `cmdName` (1105-1162)
- `cmdCurrent` (1164-1181)
- `cmdStatus` (1183-1277)
- `cmdHistory` (1396-1445)
- `cmdStop` (2032-2071)
- `cmdDelete` (3716-3777)
- `isExplicitDeleteBatchArg` (3779-3792)
- `parseDeleteBatchIndices` (3794-3849)
- `cmdDeleteBatch` (3851-3864)
- `deleteSingleSession` (3866-3868)
- `deleteSingleSessionReply` (3870-3894)
- `deleteSessionDisplayName` (3896-3909)

File header: `package core`

No imports should change since all functions are methods on `*Engine` or standalone funcs using types already in core/.

- [ ] **Step 1.2: Verify build and tests pass**

Run: `go build ./core/ && go test ./core/ -count=1`
Expected: PASS — all functions are in the same package, just different files.

- [ ] **Step 1.3: Commit**

```bash
git add core/engine_cmd_session.go core/engine_commands.go
git commit -m "refactor(core): extract session commands to engine_cmd_session.go"
```

---

## Task 2: Split engine_commands.go — Provider/Model commands

**Files:**
- Modify: `core/engine_commands.go`
- Create: `core/engine_cmd_provider.go`

- [ ] **Step 2.1: Create engine_cmd_provider.go**

Move these functions:
- `cmdModel` (1608-1705)
- `resolveModelAlias` (1710-1717)
- `parseModelSwitchArgs` (1719-1733)
- `switchModel` (1739-1781)
- `cmdReasoning` (1783-1862)
- `cmdMode` (1864-1944)
- `applyLiveModeChange` (1946-1959)
- `cmdProvider` (2293-2402)
- `cmdProviderAdd` (2404-2470)
- `cmdProviderRemove` (2472-2511)
- `switchProvider` (2513-2527)

- [ ] **Step 2.2: Verify build and tests pass**

Run: `go build ./core/ && go test ./core/ -count=1`

- [ ] **Step 2.3: Commit**

```bash
git add core/engine_cmd_provider.go core/engine_commands.go
git commit -m "refactor(core): extract provider/model commands to engine_cmd_provider.go"
```

---

## Task 3: Split engine_commands.go — Cron + Heartbeat commands

**Files:**
- Modify: `core/engine_commands.go`
- Create: `core/engine_cmd_cron.go`

- [ ] **Step 3.1: Create engine_cmd_cron.go**

Move these functions:
- `cmdCron` (2643-2683)
- `cmdCronAdd` (2685-2711)
- `cmdCronAddExec` (2713-2744)
- `cmdCronList` (2746-2805)
- `cmdCronDel` (2807-2818)
- `cmdCronToggle` (2820-2841)
- `cmdCronMute` (2843-2858)
- `cmdCronSetup` (2860-2874)
- `cmdHeartbeat` (2880-2942)
- `cmdHeartbeatStatusText` (2944-2973)
- `heartbeatLocalizedHelpers` (2975-3019)

- [ ] **Step 3.2: Verify build and tests pass**

Run: `go build ./core/ && go test ./core/ -count=1`

- [ ] **Step 3.3: Commit**

```bash
git add core/engine_cmd_cron.go core/engine_commands.go
git commit -m "refactor(core): extract cron/heartbeat commands to engine_cmd_cron.go"
```

---

## Task 4: Split engine_commands.go — Compress commands

**Files:**
- Modify: `core/engine_commands.go`
- Create: `core/engine_cmd_compress.go`

- [ ] **Step 4.1: Create engine_cmd_compress.go**

Move these functions:
- `cmdCompress` (2073-2100)
- `runCompress` (2104-2142)
- `processCompressEvents` (2147-2255)
- `drainQueuedMessagesAfterCompress` (2260-2264)

- [ ] **Step 4.2: Verify build and tests pass**

Run: `go build ./core/ && go test ./core/ -count=1`

- [ ] **Step 4.3: Commit**

```bash
git add core/engine_cmd_compress.go core/engine_commands.go
git commit -m "refactor(core): extract compress commands to engine_cmd_compress.go"
```

---

## Task 5: Split engine_commands.go — Custom commands, skills, aliases

**Files:**
- Modify: `core/engine_commands.go`
- Create: `core/engine_cmd_custom.go`

- [ ] **Step 5.1: Create engine_cmd_custom.go**

Move these functions:
- `executeCustomCommand` (3026-3054)
- `executeShellCommand` (3057-3112)
- `cmdCommands` (3114-3139)
- `cmdCommandsList` (3141-3175)
- `cmdCommandsAdd` (3177-3201)
- `cmdCommandsAddExec` (3203-3253)
- `cmdCommandsDel` (3255-3274)
- `executeSkill` (3280-3297)
- `cmdSkills` (3299-3320)
- `cmdAlias` (3616-3637)
- `cmdAliasList` (3639-3662)
- `cmdAliasAdd` (3664-3686)
- `cmdAliasDel` (3688-3714)

- [ ] **Step 5.2: Verify build and tests pass**

Run: `go build ./core/ && go test ./core/ -count=1`

- [ ] **Step 5.3: Commit**

```bash
git add core/engine_cmd_custom.go core/engine_commands.go
git commit -m "refactor(core): extract custom command/skill/alias handlers to engine_cmd_custom.go"
```

---

## Task 6: Split engine_commands.go — System/admin/misc commands

**Files:**
- Modify: `core/engine_commands.go`
- Create: `core/engine_cmd_system.go`

- [ ] **Step 6.1: Create engine_cmd_system.go**

Move ALL remaining command handler functions from `engine_commands.go`. After this step, `engine_commands.go` should contain ONLY:
- `commandHandler` type (line 24)
- `builtinCommandDef` type (27-32)
- `builtinCommandNames` var (35-77)
- `isBtwCommand` (80-82)
- `matchBtwPrefix` (86-97)
- `matchPrefix` (101-127)
- `matchSubCommand` (130-149)
- `initBuiltinCommands` (153-231)
- `matchBuiltinPrefix` (235-258)
- `handleCommand` (260-347)
- `GetAllCommands` (1538-1606)

Everything else moves to `engine_cmd_system.go`:
- `handleWorkspaceCommand` (349-565)
- `cmdShell` (791-852), `dirApply` (856-954), `cmdDir` (956-1016), `dirCardPageSize` const (601)
- `cmdUsage` + all usage helpers (1279-1394)
- `cmdLang`, `langDisplayName` (1447-1525)
- `cmdHelp` (1527-1533)
- `cmdQuiet` (1961-2002), `cmdTTS` (2004-2030)
- `cmdAllow` (2266-2291)
- `cmdMemory`, `showMemoryFile`, `appendMemoryFile` (2536-2637)
- `configItem` type + `configItems` + `cmdConfig` + `cmdConfigReload` (3325-3603)
- `cmdDoctor`, `cmdUpgrade`, `cmdUpgradeConfirm`, `cmdRestart` (3509-3614)
- `cmdBind` + helpers, `setupMemoryFile`, `setupResult` type/consts, `cmdBindSetup` (3922-4102)
- `cmdWhoami`, `formatWhoamiText` (3471-3505)

- [ ] **Step 6.2: Verify engine_commands.go is now ~300 lines (infrastructure only)**

Run: `wc -l core/engine_commands.go`
Expected: approximately 250-350 lines.

- [ ] **Step 6.3: Verify build and tests pass**

Run: `go build ./core/ && go test ./core/ -count=1`

- [ ] **Step 6.4: Commit**

```bash
git add core/engine_cmd_system.go core/engine_commands.go
git commit -m "refactor(core): extract system/admin/misc commands to engine_cmd_system.go"
```

---

## Task 7: Extract config.go mutateAndSave helper

**Files:**
- Modify: `config/config.go`
- Test: `config/config_test.go`

- [ ] **Step 7.1: Write test for the new readAndParse helper**

Add to `config/config_test.go`:
```go
func TestReadAndParse(t *testing.T) {
    dir := t.TempDir()
    path := filepath.Join(dir, "config.toml")
    ConfigPath = path
    defer func() { ConfigPath = "" }()

    // Write a valid minimal config
    content := validBaseConfigTOML()
    os.WriteFile(path, []byte(content), 0o644)

    cfg, raw, err := readAndParse()
    if err != nil {
        t.Fatalf("readAndParse() error: %v", err)
    }
    if cfg == nil {
        t.Fatal("readAndParse() returned nil config")
    }
    if len(raw) == 0 {
        t.Fatal("readAndParse() returned empty raw bytes")
    }
    if cfg.Database.DSN == "" {
        t.Error("parsed config has empty DSN")
    }
}
```

Where `validBaseConfigTOML()` returns the TOML string from `baseConfigTOML` or similar existing fixture.

- [ ] **Step 7.2: Run test to verify it fails**

Run: `go test ./config/ -run TestReadAndParse -v`
Expected: FAIL — `readAndParse` doesn't exist yet.

- [ ] **Step 7.3: Implement readAndParse and mutateAndSave**

Add to `config/config.go`:

```go
// readAndParse reads the config file and returns the parsed Config and raw bytes.
// Caller must hold configMu.
func readAndParse() (*Config, []byte, error) {
    if ConfigPath == "" {
        return nil, nil, fmt.Errorf("config path not set")
    }
    data, err := os.ReadFile(ConfigPath)
    if err != nil {
        return nil, nil, err
    }
    var cfg Config
    if err := toml.Unmarshal(data, &cfg); err != nil {
        return nil, nil, err
    }
    return &cfg, data, nil
}

// mutateAndSave reads the current config, applies fn to modify it, and
// atomically saves the result. This is the standard read-modify-write pattern
// for simple field mutations that don't need to preserve TOML comments.
func mutateAndSave(fn func(cfg *Config) error) error {
    configMu.Lock()
    defer configMu.Unlock()

    cfg, _, err := readAndParse()
    if err != nil {
        return err
    }
    if err := fn(cfg); err != nil {
        return err
    }
    return saveConfig(cfg)
}
```

- [ ] **Step 7.4: Run test to verify it passes**

Run: `go test ./config/ -run TestReadAndParse -v`
Expected: PASS

- [ ] **Step 7.5: Refactor SaveLanguage as the first migration**

Before (lines 589-605):
```go
func SaveLanguage(lang string) error {
    configMu.Lock()
    defer configMu.Unlock()
    if ConfigPath == "" { return fmt.Errorf("...") }
    data, err := os.ReadFile(ConfigPath)
    if err != nil { return err }
    var cfg Config
    if err := toml.Unmarshal(data, &cfg); err != nil { return err }
    cfg.Language = lang
    return saveConfig(&cfg)
}
```

After:
```go
func SaveLanguage(lang string) error {
    return mutateAndSave(func(cfg *Config) error {
        cfg.Language = lang
        return nil
    })
}
```

- [ ] **Step 7.6: Run existing SaveLanguage test to verify no regression**

Run: `go test ./config/ -run TestSaveLanguage -v`
Expected: PASS

- [ ] **Step 7.7: Migrate remaining 11 standard-pattern functions**

Apply the same `mutateAndSave` pattern to:
1. `SaveActiveProvider` — fn finds project by name, sets provider option
2. `SaveProviderModel` — fn finds project+provider, sets model
3. `SaveAgentModel` — fn finds project, sets model option
4. `AddProviderToConfig` — fn checks duplicate, appends provider
5. `RemoveProviderFromConfig` — fn filters out provider
6. `AddCommand` — fn checks duplicate, appends command
7. `RemoveCommand` — fn filters out command
8. `AddAlias` — fn upserts alias
9. `RemoveAlias` — fn filters out alias
10. `SaveDisplayConfig` — fn sets display fields
11. `SaveTTSMode` — fn sets TTS mode

Each function that currently returns a special value (like `AddProviderToConfig` returning error for duplicates) should return that error from the fn closure.

- [ ] **Step 7.8: Run full config test suite**

Run: `go test ./config/ -v -count=1`
Expected: ALL PASS

- [ ] **Step 7.9: Also update raw-mode functions to use readAndParse**

The 4 raw-mode functions (`EnsureProjectWithFeishuPlatform`, `SaveFeishuPlatformCredentials`, `EnsureProjectWithWeixinPlatform`, `SaveWeixinPlatformCredentials`) can't use `mutateAndSave` (they need raw bytes), but they CAN use `readAndParse` to replace their duplicate read+unmarshal boilerplate.

Pattern:
```go
// Before:
configMu.Lock()
defer configMu.Unlock()
if ConfigPath == "" { return ... }
data, err := os.ReadFile(ConfigPath)
if err != nil { return ... }
var cfg Config
if err := toml.Unmarshal(data, &cfg); err != nil { return ... }

// After:
configMu.Lock()
defer configMu.Unlock()
cfg, data, err := readAndParse()
if err != nil { return ... }
```

- [ ] **Step 7.10: Run full config test suite again**

Run: `go test ./config/ -v -count=1`
Expected: ALL PASS

- [ ] **Step 7.11: Commit**

```bash
git add config/config.go config/config_test.go
git commit -m "refactor(config): extract mutateAndSave helper to reduce Save function boilerplate"
```

---

## Task 8: Extract platform opts helpers

**Files:**
- Create: `core/opts_helpers.go`
- Create: `core/opts_helpers_test.go`
- Modify: 10 platform `New()` functions (optional, incremental)

- [ ] **Step 8.1: Write tests for opts helpers**

Create `core/opts_helpers_test.go`:
```go
package core

import "testing"

func TestOptsString(t *testing.T) {
    opts := map[string]any{"key": "value", "empty": "", "num": 42}
    if got := OptsString(opts, "key"); got != "value" {
        t.Errorf("OptsString(key) = %q, want %q", got, "value")
    }
    if got := OptsString(opts, "missing"); got != "" {
        t.Errorf("OptsString(missing) = %q, want empty", got)
    }
    if got := OptsString(opts, "num"); got != "" {
        t.Errorf("OptsString(num) = %q, want empty (wrong type)", got)
    }
}

func TestOptsBool(t *testing.T) {
    opts := map[string]any{"yes": true, "no": false, "str": "true"}
    if got := OptsBool(opts, "yes"); !got {
        t.Error("OptsBool(yes) = false, want true")
    }
    if got := OptsBool(opts, "no"); got {
        t.Error("OptsBool(no) = true, want false")
    }
    if got := OptsBool(opts, "missing"); got {
        t.Error("OptsBool(missing) = true, want false")
    }
    // string "true" should not be treated as bool
    if got := OptsBool(opts, "str"); got {
        t.Error("OptsBool(str='true') = true, want false (strict type)")
    }
}
```

- [ ] **Step 8.2: Run test to verify it fails**

Run: `go test ./core/ -run "TestOptsString|TestOptsBool" -v`
Expected: FAIL

- [ ] **Step 8.3: Implement opts helpers**

Create `core/opts_helpers.go`:
```go
package core

// OptsString extracts a string value from an opts map.
// Returns empty string if key is missing or not a string.
func OptsString(opts map[string]any, key string) string {
    v, _ := opts[key].(string)
    return v
}

// OptsBool extracts a bool value from an opts map.
// Returns false if key is missing or not a bool.
func OptsBool(opts map[string]any, key string) bool {
    v, _ := opts[key].(bool)
    return v
}
```

- [ ] **Step 8.4: Run tests to verify they pass**

Run: `go test ./core/ -run "TestOptsString|TestOptsBool" -v`
Expected: PASS

- [ ] **Step 8.5: Commit**

```bash
git add core/opts_helpers.go core/opts_helpers_test.go
git commit -m "refactor(core): add OptsString/OptsBool helpers for platform opts extraction"
```

- [ ] **Step 8.6: (Optional) Migrate one platform as proof of concept**

Pick the simplest platform (e.g., `platform/qq/qq.go`) and replace:
```go
// Before:
wsURL, _ := opts["ws_url"].(string)
token, _ := opts["token"].(string)
allowFrom, _ := opts["allow_from"].(string)
shareSession, _ := opts["share_session_in_channel"].(bool)

// After:
wsURL := core.OptsString(opts, "ws_url")
token := core.OptsString(opts, "token")
allowFrom := core.OptsString(opts, "allow_from")
shareSession := core.OptsBool(opts, "share_session_in_channel")
```

- [ ] **Step 8.7: Verify build and tests pass**

Run: `go build ./... && go test ./... -count=1`
Expected: ALL PASS

- [ ] **Step 8.8: Commit**

```bash
git add platform/qq/qq.go
git commit -m "refactor(platform/qq): use core.OptsString/OptsBool helpers"
```

---

## Task 9: Final verification

- [ ] **Step 9.1: Full build**

Run: `go build ./...`
Expected: PASS

- [ ] **Step 9.2: Full test suite**

Run: `go test ./... -count=1`
Expected: ALL PASS (except pre-existing `TestWecomInboundFileMime` failure)

- [ ] **Step 9.3: Verify engine_commands.go line count**

Run: `wc -l core/engine_commands.go core/engine_cmd_*.go`
Expected: `engine_commands.go` ~250-350 lines, total across all files ~4100 lines (same total, better distribution)

- [ ] **Step 9.4: Verify config.go line reduction**

Run: `wc -l config/config.go`
Expected: ~1600 lines (down from 1731)

- [ ] **Step 9.5: Verify no behavior changes with grep**

Run: `go test -race ./core/ ./config/ -count=1`
Expected: PASS — race detector clean
