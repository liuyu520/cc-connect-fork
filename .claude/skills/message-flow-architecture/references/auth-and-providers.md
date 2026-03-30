# Authentication and Provider Configuration

## Overview

cc-connect does not embed any API keys. It injects authentication credentials
into the claude CLI subprocess via environment variables. Three paths exist,
from simplest to most complex.

## Path 1: Inherited Authentication (Default)

When no provider or router is configured, the claude CLI subprocess inherits
**all** environment variables from the cc-connect process (only `CLAUDECODE`
is filtered out to prevent nested-session detection).

```go
// session.go:95
env := filterEnv(os.Environ(), "CLAUDECODE")
```

The claude CLI then authenticates using:
1. `ANTHROPIC_API_KEY` environment variable (if set)
2. `~/.claude/` OAuth login state (if user ran `claude login`)

**Setup:** Either set `export ANTHROPIC_API_KEY=sk-ant-xxx` before starting
cc-connect, or run `claude login` on the server.

## Path 2: Provider Configuration

Configure providers in `config.toml` per project:

```toml
[[projects.agent.providers]]
name = "anthropic"
api_key = "sk-ant-xxx"

[[projects.agent.providers]]
name = "siliconflow"
api_key = "sk-sf-xxx"
base_url = "https://api.siliconflow.cn/v1"
thinking = "disabled"

[projects.agent.options]
provider = "anthropic"    # active provider name
```

### Environment Variable Injection

`providerEnvLocked()` in `agent/claudecode/claudecode.go:654-690`:

**Case A: No base_url (direct Anthropic API)**
```
ANTHROPIC_API_KEY = "sk-ant-xxx"
```

**Case B: With base_url (third-party provider)**
```
ANTHROPIC_AUTH_TOKEN = "sk-sf-xxx"    # Bearer token auth
ANTHROPIC_API_KEY = ""                # clear to skip key format validation
ANTHROPIC_BASE_URL = "https://api.siliconflow.cn/v1"
NO_PROXY = "127.0.0.1"
```

**Why two different env vars?** Claude CLI validates `ANTHROPIC_API_KEY` format
(must start with `sk-ant-`). Third-party keys use a different format, so
`ANTHROPIC_AUTH_TOKEN` is used instead (Bearer auth, no format check).

**Case C: With base_url + thinking override**
```
ANTHROPIC_AUTH_TOKEN = "sk-sf-xxx"
ANTHROPIC_API_KEY = ""
ANTHROPIC_BASE_URL = "http://127.0.0.1:<random-port>"  # local proxy
NO_PROXY = "127.0.0.1"
```

A local reverse proxy (`core/providerproxy.go`) intercepts API requests and
rewrites `thinking.type` from `"adaptive"` to the configured value (e.g.,
`"disabled"`). Some third-party providers don't support Claude's `adaptive`
thinking mode.

### Provider Proxy Details

`core/providerproxy.go:36-83`:
- Listens on `127.0.0.1:0` (random port)
- Intercepts POST `/messages` requests
- Rewrites `thinking.type` in the request body
- Forwards to the actual provider `base_url`
- Returns response transparently

### Provider Switching at Runtime

The `/provider` slash command switches the active provider. When switching:
1. `providerEnvLocked()` computes new env vars
2. `Agent.SetSessionEnv()` updates the session env
3. Next `StartSession()` uses the new env
4. Existing sessions continue with old provider until restarted

## Path 3: Router Mode

Claude Code Router is a centralized API proxy that manages keys and rate
limiting for multiple users.

```toml
[projects.agent.options]
router_url = "http://127.0.0.1:3456"
router_api_key = "my-router-key"
```

**Environment injection** (`claudecode.go:250-260`):
```
ANTHROPIC_BASE_URL = "http://127.0.0.1:3456"
ANTHROPIC_API_KEY = "my-router-key"          # if router_api_key is set
NO_PROXY = "127.0.0.1"
DISABLE_TELEMETRY = "true"
DISABLE_COST_WARNINGS = "true"
```

Router mode also disables `--verbose` flag because verbose output includes
non-JSON text that breaks the stream-json protocol parsing.

## Per-Session Environment Injection

Beyond auth, Engine injects these env vars for every session
(`core/engine.go:1984-1997` via `SessionEnvInjector` interface):

| Variable | Value | Purpose |
|----------|-------|---------|
| `CC_PROJECT` | project name | Identify which project this session belongs to |
| `CC_SESSION_KEY` | session key string | Identify the IM conversation context |
| `PATH` | cc-connect dir prepended | Ensure subprocess can find `cc-connect` binary |

These are set via `Agent.SetSessionEnv()` before `StartSession()` is called.

## Complete Environment Build Order

```
os.Environ()                          # host environment
  │
  filterEnv("CLAUDECODE")             # remove nested-detection var
  │
  MergeEnv(sessionEnv):               # Engine-injected vars
  │  CC_PROJECT, CC_SESSION_KEY, PATH
  │
  MergeEnv(providerEnv):              # Provider auth vars
  │  ANTHROPIC_API_KEY, ANTHROPIC_AUTH_TOKEN,
  │  ANTHROPIC_BASE_URL, NO_PROXY, custom env
  │
  OR MergeEnv(routerEnv):             # Router auth vars (mutually exclusive)
  │  ANTHROPIC_BASE_URL, ANTHROPIC_API_KEY,
  │  NO_PROXY, DISABLE_TELEMETRY
  │
  ▼
cmd.Env = final environment           # passed to claude CLI subprocess
```

Later `MergeEnv` calls override earlier values with the same key.

## Provider Configuration Fields

```toml
[[projects.agent.providers]]
name = "provider-name"         # unique identifier
api_key = "key"                # API key / token
base_url = "https://..."       # optional: custom API endpoint
model = "model-name"           # optional: default model for this provider
thinking = "disabled"          # optional: override thinking mode
[projects.agent.providers.env]
CUSTOM_VAR = "value"           # optional: arbitrary extra env vars
```

## Debugging Authentication Issues

| Symptom | Likely Cause |
|---------|-------------|
| "Invalid API key" from claude | `ANTHROPIC_API_KEY` not set or wrong format |
| "Unauthorized" from third-party | `ANTHROPIC_AUTH_TOKEN` not set, or provider requires different auth |
| claude hangs on startup | Router unreachable, or proxy port conflict |
| "adaptive thinking not supported" | Provider needs `thinking = "disabled"` in config |
| Key works in terminal but not cc-connect | Check if env var is set in the shell that starts cc-connect |

**Diagnostic command:**
```bash
# Check what env vars the claude subprocess would see
env | grep -E "ANTHROPIC|CLAUDE|CC_"
```
