---
name: engine-file-layout
description: >
  This skill should be used when the user asks "where is function X",
  "which file contains", "engine file structure", "engine.go split",
  "engine_commands.go", "engine_cards.go", "engine_session.go",
  "engine_events.go", "engine_util.go", "engine_workspace.go",
  "find function in engine", "engine file layout", "engine file map",
  or needs to quickly locate a specific function, type, or constant
  across the engine's split file structure.
---

# Engine File Layout

## Purpose

After the engine.go refactoring, Engine logic is distributed across 7 files.
This skill provides a quick-reference map for locating any function, type,
or constant in the engine's split structure.

## File Overview

| File | Lines | Content Summary |
|------|-------|-----------------|
| `engine.go` | ~1,420 | Engine struct, NewEngine, Set* methods, lifecycle, handleMessage entry |
| `engine_commands.go` | ~4,100 | Command registry, handleCommand, all cmd* handlers |
| `engine_cards.go` | ~1,890 | Card navigation, card rendering, delete mode |
| `engine_session.go` | ~750 | Session types, interactive session management |
| `engine_events.go` | ~690 | Event processing loop, message draining |
| `engine_util.go` | ~600 | Send/reply helpers, message splitting, relay |
| `engine_workspace.go` | ~310 | Workspace resolution, session key parsing |

## engine.go — Core Structure & Lifecycle

**Constants & Types:**
- `maxPlatformMessageLen`, `maxQueuedMessages`, `defaultThinkingMaxLen`, `defaultToolMaxLen`
- `RestartRequest`, `DisplayCfg`, `RateLimitCfg`, `sessionListCacheEntry`, `ConfigReloadResult`
- `workspaceInitFlow`

**Variables:**
- `VersionInfo`, `CurrentVersion`, `ErrAttachmentSendDisabled`, `RestartCh`

**Engine struct** and all `Set*()` configuration methods (~25)

**Lifecycle:**
- `NewEngine()` — constructor, calls `initBuiltinCommands()`
- `Start()`, `Stop()`, `OnPlatformReady()`, `OnPlatformUnavailable()`

**Message entry:**
- `handleMessage()` — main message dispatch (~180 lines)
- `resolveAlias()`, `matchBannedWord()`, `checkRateLimit()`

**Session utilities:**
- `listSessionsCached()`, `invalidateSessionListCache()`
- `ActiveSessionKeys()`, `CleanupSession()`, `GetSessionInfo()`

**Cron/Heartbeat:**
- `ExecuteCronJob()`, `executeCronShell()`, `ExecuteHeartbeat()`

**Admin:**
- `isAdmin()`, `resolveDisabledCmds()`

## engine_commands.go — Command System

**Registry types:**
- `commandHandler` — unified handler signature `func(p Platform, msg *Message, args []string)`
- `builtinCommandDef` — command definition with names, id, handler, privileged flag
- `builtinCommandNames` — package-level name/id list for `resolveDisabledCmds` and `matchPrefix`

**Registry initialization:**
- `initBuiltinCommands()` — binds all 38 commands to handlers

**Dispatch:**
- `handleCommand()` — registry-based command dispatch (replaces old 38-case switch)
- `matchBuiltinPrefix()` — Engine method for prefix matching against `builtinCommandDefs`
- `matchPrefix()` — standalone prefix matching against name/id list
- `matchSubCommand()` — subcommand prefix matching
- `isBtwCommand()`, `matchBtwPrefix()` — /btw detection

**Workspace command:**
- `handleWorkspaceCommand()`

**Session commands:** `cmdNew`, `cmdList`, `cmdSwitch`, `matchSession`, `cmdSearch`, `cmdName`, `cmdCurrent`, `cmdStatus`

**Info commands:** `cmdUsage`, `formatUsageReport`, `formatUsageBlocks`, `accountDisplay`, `selectUsageWindows`, `formatUsageBlock`, `cmdHistory`

**Agent control:** `cmdModel`, `resolveModelAlias`, `parseModelSwitchArgs`, `switchModel`, `cmdReasoning`, `cmdMode`, `applyLiveModeChange`, `cmdQuiet`, `cmdTTS`, `cmdStop`, `cmdAllow`

**Compress:** `cmdCompress`, `runCompress`, `processCompressEvents`, `drainQueuedMessagesAfterCompress`

**Provider:** `cmdProvider`, `cmdProviderAdd`, `cmdProviderRemove`, `switchProvider`

**Memory/Cron/Heartbeat:** `cmdMemory`, `showMemoryFile`, `appendMemoryFile`, `cmdCron`, `cmdCronAdd`, `cmdCronAddExec`, `cmdCronList`, `cmdCronDel`, `cmdCronToggle`, `cmdCronMute`, `cmdCronSetup`, `cmdHeartbeat`, `cmdHeartbeatStatusText`, `heartbeatLocalizedHelpers`

**Custom commands & skills:** `executeCustomCommand`, `executeShellCommand`, `cmdCommands`, `cmdCommandsList`, `cmdCommandsAdd`, `cmdCommandsAddExec`, `cmdCommandsDel`, `executeSkill`, `cmdSkills`

**Config/System:** `configItem`, `configItems()`, `cmdConfig`, `cmdWhoami`, `formatWhoamiText`, `cmdDoctor`, `cmdUpgrade`, `cmdUpgradeConfirm`, `cmdConfigReload`, `cmdRestart`, `cmdAlias`, `cmdAliasList`, `cmdAliasAdd`, `cmdAliasDel`

**Delete:** `cmdDelete`, `isExplicitDeleteBatchArg`, `parseDeleteBatchIndices`, `cmdDeleteBatch`, `deleteSingleSession`, `deleteSingleSessionReply`, `deleteSessionDisplayName`

**Bind:** `cmdBind`, `cmdBindStatus`, `setupMemoryFile`, `cmdBindSetup`

**Lang/Help:** `cmdLang`, `langDisplayName`, `cmdHelp`, `cmdDir`, `dirApply`, `cmdShell`

**Public API:** `GetAllCommands()`

## engine_cards.go — Card Rendering

**Helpers:** `splitCardTitleBody`, `cardBackButton`, `cardPrevButton`, `cardNextButton`, `simpleCard`

**Safe wrappers:** `renderListCardSafe`, `renderDirCardSafe`

**Status/Time:** `renderStatusCard`, `cronTimeFormat`, `formatDurationI18n`

**Usage card:** `renderUsageCard`, `formatUsageResetTime`, `usageAccountLabel`, `usageWindowLabel`, `usageRemainingLabel`, `usageResetLabel`, `usageColon`, `usageCardTitle`, `usageUnavailableText`

**Help card:** `defaultHelpGroup`, `helpCardItem`, `helpCardGroup`, `helpCardGroups()`, `renderHelpCard`, `splitHelpTabRows`, `renderHelpGroupCard`

**Navigation:** `handleCardNav`, `executeCardAction`

**Delete mode:** `getOrCreateDeleteModeState`, `getDeleteModeState`, `renderDeleteModeCard`, `renderDeleteModeSelectCard`, `renderDeleteModeConfirmCard`, `renderDeleteModeResultCard`, `deleteModeSelectionNames`, `executeDeleteModeAction`, `parseDeleteModeSelectedIDs`, `submitDeleteModeSelection`

**Render cards:** `renderLangCard`, `renderModelCard`, `renderReasoningCard`, `renderModeCard`, `renderListCard`, `dirCardTruncPath`, `renderDirCard`, `renderCurrentCard`, `renderHistoryCard`, `renderProviderCard`, `renderCronCard`, `renderCommandsCard`, `renderAliasCard`, `renderConfigCard`, `renderSkillsCard`, `renderDoctorCard`, `renderVersionCard`, `renderUpgradeCard`, `renderHeartbeatCard`, `renderWhoamiCard`

## engine_session.go — Session Management

**Types:** `queuedMessage`, `interactiveState`, `deleteModeState`, `pendingPermission`, `resolve()`

**Queue management:** `queueMessageForBusySession`, `drainOrphanedQueue`

**Voice:** `handleVoiceMessage`

**Permission handling:** `handlePendingPermission`, `resolveAskQuestionAnswer`, `buildAskQuestionResponse`, `isApproveAllResponse`, `isAllowResponse`, `isDenyResponse`

**Interactive session:** `processInteractiveMessage`, `processInteractiveMessageWith`, `getOrCreateWorkspaceAgent`, `getOrCreateInteractiveStateWith`, `cleanupInteractiveState`

## engine_events.go — Event Loop

- `defaultEventIdleTimeout` constant
- `processInteractiveEvents()` — core select loop (~600 lines)
- `notifyDroppedQueuedMessages()` — notify queued senders on error
- `drainPendingMessages()` — process queued messages after turn completion

## engine_util.go — Messaging Utilities

**Send/Reply:** `send`, `reply`, `replyWithButtons`, `replyWithCard`, `sendWithCard`

**Capabilities:** `supportsCards` (standalone), `drainEvents` (standalone)

**Permission UI:** `sendPermissionPrompt`, `sendAskQuestionPrompt`

**External API:** `SendToSession`, `SendToSessionWithAttachments`

**Text processing:** `toolCodeLang`, `truncateIf`, `splitMessage` (all standalone)

**TTS:** `sendTTSReply`

**Relay:** `HandleRelay`, `relayPartialResponseOrError`

**Context indicator:** `modelContextWindow` constant, `contextIndicator`, `ctxSelfReportRe`, `parseSelfReportedCtx`

## engine_workspace.go — Workspace Helpers

**Sender injection:** `buildSenderPrompt`

**Key extraction (standalone):** `extractChannelID`, `extractUserID`, `extractPlatformName`, `workspaceChannelKey`, `extractWorkspaceChannelKey`

**Context resolution:** `commandContext`, `sessionContextForKey`, `interactiveKeyForSessionKey`

**Workspace binding:** `lookupEffectiveWorkspaceBinding`, `resolveWorkspace`, `handleWorkspaceInitFlow`

**Git utilities (standalone):** `looksLikeGitURL`, `extractRepoName`, `gitClone`

## Quick Lookup: "Where Do I Put New Code?"

| Adding... | Put it in... |
|-----------|-------------|
| New slash command handler | `engine_commands.go` |
| New card render function | `engine_cards.go` |
| New Engine `Set*()` method | `engine.go` |
| New Engine struct field | `engine.go` |
| New session/permission logic | `engine_session.go` |
| New event handling case | `engine_events.go` |
| New send/reply helper | `engine_util.go` |
| New workspace helper | `engine_workspace.go` |
