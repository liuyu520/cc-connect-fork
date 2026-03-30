---
name: card-callback-performance
description: >
  Use when encountering IM platform card callback timeouts (error 200340),
  "card interaction timeout", "card page turn slow", "renderListCard slow",
  "renderDeleteModeCard slow", "ListSessions slow", "3 second timeout",
  "session list cache", "sessionListCache", "listSessionsCached",
  or when adding new card rendering code that calls expensive I/O operations
  (ListSessions, file scanning, JSONL parsing) in card callback paths.
---

# Card Callback Performance

## Purpose

Document the 3-second timeout constraint on IM card interactions (Feishu/Lark
error 200340) and the established caching pattern used to keep card rendering
fast enough.

## The Constraint

**Feishu (and similar IM platforms) require card interaction callbacks to return
within 3 seconds.** If the server doesn't respond in time, the platform returns
error code `200340` to the user.

Card interactions include:
- Page turns (next/prev page buttons)
- Checkbox toggles (select/deselect items)
- Form submissions (confirm/cancel)
- Any `act:` prefixed callback

**Regular slash commands (e.g., `/list`, `/delete`) do NOT have this constraint**
— they can take as long as needed.

## Key Files

| File | Role |
|------|------|
| `core/engine.go` | `sessionListCacheEntry` type, `listSessionsCached()`, `invalidateSessionListCache()` |
| `core/engine_cards.go` | `renderListCard()`, `renderListCardSafe()`, `renderDeleteModeCard()`, `renderDeleteModeSelectCard()`, `handleCardNav()`, `executeCardAction()`, all `render*Card()` |
| `core/engine_commands.go` | `cmdList()`, `cmdDelete()` — slash command paths (no timeout constraint) |

## Affected Code Paths

| Function | File | Path Type | Constraint | Uses Cache? |
|----------|------|-----------|------------|-------------|
| `renderListCard()` | `engine_cards.go` | Card callback | 3s timeout | Yes |
| `renderDeleteModeCard()` | `engine_cards.go` | Card callback | 3s timeout | Yes |
| `submitDeleteModeSelection()` | `engine_cards.go` | Card callback | 3s timeout | No (mutates, then invalidates) |
| `cmdList()` | `engine_commands.go` | Slash command | No timeout | No (calls `agent.ListSessions` directly) |
| `cmdDelete()` | `engine_commands.go` | Slash command | No timeout | No |

## The Caching Pattern

### Structure

```go
// core/engine.go

// sessionListCacheEntry holds a cached ListSessions result with expiry.
type sessionListCacheEntry struct {
    sessions []AgentSessionInfo
    at       time.Time
}

const sessionListCacheTTL = 30 * time.Second

// Engine struct fields:
sessionListCacheMu sync.RWMutex
sessionListCache   map[Agent]*sessionListCacheEntry
```

### Read Path (Cache Hit / Miss)

```go
func (e *Engine) listSessionsCached(agent Agent) ([]AgentSessionInfo, error) {
    // 1. Read lock: check cache
    e.sessionListCacheMu.RLock()
    if entry, ok := e.sessionListCache[agent]; ok && time.Since(entry.at) < sessionListCacheTTL {
        e.sessionListCacheMu.RUnlock()
        return entry.sessions, nil
    }
    e.sessionListCacheMu.RUnlock()

    // 2. Cache miss: call expensive I/O
    sessions, err := agent.ListSessions(e.ctx)
    if err != nil {
        return nil, err
    }

    // 3. Write lock: store result
    e.sessionListCacheMu.Lock()
    if e.sessionListCache == nil {
        e.sessionListCache = make(map[Agent]*sessionListCacheEntry)
    }
    e.sessionListCache[agent] = &sessionListCacheEntry{sessions: sessions, at: time.Now()}
    e.sessionListCacheMu.Unlock()

    return sessions, nil
}
```

### Invalidation Path

```go
func (e *Engine) invalidateSessionListCache(agent Agent) {
    e.sessionListCacheMu.Lock()
    delete(e.sessionListCache, agent)
    e.sessionListCacheMu.Unlock()
}
```

### Invalidation Points

Cache must be invalidated whenever session list changes:

| Location | File | Trigger |
|----------|------|---------|
| `deleteSingleSessionReply()` | `engine_commands.go` | After `deleter.DeleteSession()` succeeds |
| `submitDeleteModeSelection()` | `engine_cards.go` | After batch deletion completes |
| `cmdNew()` | `engine_commands.go` | After creating a new session |

## When to Apply This Pattern

Use `listSessionsCached()` instead of `agent.ListSessions()` when:
1. The call is in a **card callback path** (subject to 3s timeout)
2. The result is used for **display only** (not for mutation decisions)

Use `agent.ListSessions()` directly when:
1. The call is in a **slash command path** (no timeout constraint)
2. The result will be used for **mutation** (delete, switch, etc.)
3. Freshness is critical

## Adding New Card Rendering Functions

When adding a new card function that needs session data:

1. **Identify if it's a card callback** — called from `handleCardNav()` or
   `executeCardAction()` in `engine_cards.go`
2. **If yes**, use `e.listSessionsCached(agent)` instead of `agent.ListSessions(e.ctx)`
3. **If the function mutates sessions**, call `e.invalidateSessionListCache(agent)`
   after the mutation

## Why 30-Second TTL?

- **Too short** (<5s): Cache misses on rapid page turns, defeating the purpose
- **Too long** (>60s): Stale data after session creation/deletion feels wrong
- **30 seconds**: Covers a typical user browsing/paging session while staying
  reasonably fresh. Explicit invalidation handles mutations immediately.

## Debugging

| Symptom | Check |
|---------|-------|
| Error 200340 on page turn | `renderListCard` or `renderDeleteModeCard` in `engine_cards.go` taking >3s — check if `listSessionsCached` is being used |
| Stale session list after delete | `invalidateSessionListCache` not called after mutation |
| Cache never hits | TTL too short, or `invalidateSessionListCache` called too aggressively |
| Race condition in cache | Ensure `sessionListCacheMu` is used correctly (RLock for read, Lock for write) |

## Extending to Other Expensive Operations

The same pattern can be applied to other card callbacks that do expensive I/O:

| Candidate | Function | File | Risk |
|-----------|----------|------|------|
| `/history` card | `renderHistoryCard` | `engine_cards.go` | Session history file reading |
| `/doctor` card | `renderDoctorCard` | `engine_cards.go` | CLI binary checks, version probing |

To add caching for a new operation:
1. Define a new cache entry type (or reuse if same data shape)
2. Add cache field + mutex to Engine struct
3. Create `<operation>Cached()` + `invalidate<Operation>Cache()` methods
4. Replace calls in card callback paths only

## Related Skills

- **`message-flow-architecture`** — How card actions reach the engine
- **`add-new-feature`** — General feature addition workflow
- **`slash-command-system`** — How commands are dispatched (non-card paths)
