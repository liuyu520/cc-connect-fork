# Ops Enhancement Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enhance cc-connect's operational capabilities with health checks, improved diagnostics, write reliability, and standard Unix signal handling.

**Architecture:** Four independent enhancements to existing files. Each task produces a working, testable change. No new packages — only additions to existing `core/` and `cmd/cc-connect/` packages.

**Tech Stack:** Go stdlib (`net/http`, `os/signal`, `syscall`, `database/sql`), existing project patterns.

---

## File Structure

| File | Change Type | Purpose |
|------|-------------|---------|
| `core/interfaces.go` | Modify | Add `HealthChecker` interface |
| `core/chatstore.go` | Modify | Add `Ping(ctx) error` to ChatStore interface |
| `core/chatstore_mysql.go` | Modify | Implement `Ping`, add retry logic to write functions |
| `core/management.go` | Modify | Add `/healthz` endpoint, fix graceful shutdown |
| `core/doctor.go` | Modify | Add MySQL and Platform health checks |
| `core/i18n.go` | Modify | Add i18n keys for new doctor checks |
| `cmd/cc-connect/main.go` | Modify | Add SIGHUP handler |
| `daemon/systemd.go` | Modify | Add ExecReload to systemd unit template |
| `core/engine_test.go` or relevant test files | Modify | Tests for all new functionality |

---

## Task 1: Add `/healthz` endpoint + graceful shutdown to ManagementServer

**Files:**
- Modify: `core/chatstore.go` (add Ping to interface, line 64-81)
- Modify: `core/chatstore_mysql.go` (implement Ping)
- Modify: `core/management.go` (add /healthz route, fix Stop)
- Test: `core/management_test.go` (may need to create if not exists)

- [ ] **Step 1.1: Add `Ping` method to ChatStore interface**

In `core/chatstore.go`, add to the ChatStore interface (after `Close() error` at line 80):
```go
// Ping checks if the underlying database connection is alive.
Ping(ctx context.Context) error
```

- [ ] **Step 1.2: Implement `Ping` on MySQLChatStore**

In `core/chatstore_mysql.go`, add after the struct definition area:
```go
// Ping checks the MySQL connection is alive.
func (s *MySQLChatStore) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}
```

- [ ] **Step 1.3: Verify build passes**

Run: `go build ./core/`
Expected: PASS

- [ ] **Step 1.4: Add `SetChatStore` and `chatStore` field to ManagementServer**

In `core/management.go`, add a `chatStore` field to the ManagementServer struct (line 17-30) and a setter method. The ManagementServer needs access to the chatStore for the healthz ping.

Add field to struct:
```go
chatStore   ChatStore
```

Add setter method:
```go
// SetChatStore sets the ChatStore for health checks.
func (m *ManagementServer) SetChatStore(cs ChatStore) {
	m.chatStore = cs
}
```

- [ ] **Step 1.5: Add `/healthz` route (unauthenticated)**

In `core/management.go` `Start()` method, add the `/healthz` route BEFORE the authenticated routes (before line 58). This route must NOT use the `wrap()` middleware since it needs to be unauthenticated:

```go
// Health check endpoint — no auth required (for k8s/Docker probes)
mux.HandleFunc("/healthz", m.handleHealthz)
```

Implement the handler:
```go
func (m *ManagementServer) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	type healthResponse struct {
		Status string   `json:"status"`
		Uptime string   `json:"uptime"`
		Issues []string `json:"issues,omitempty"`
	}

	var issues []string

	// Check engine count
	if len(m.engines) == 0 {
		issues = append(issues, "no engines running")
	}

	// Check ChatStore connectivity
	if m.chatStore != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := m.chatStore.Ping(ctx); err != nil {
			issues = append(issues, "chatstore unreachable: "+err.Error())
		}
	}

	resp := healthResponse{
		Uptime: time.Since(m.startedAt).Truncate(time.Second).String(),
	}

	w.Header().Set("Content-Type", "application/json")
	if len(issues) > 0 {
		resp.Status = "degraded"
		resp.Issues = issues
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		resp.Status = "ok"
		w.WriteHeader(http.StatusOK)
	}
	json.NewEncoder(w).Encode(resp)
}
```

Make sure `"context"` and `"time"` are in the imports.

- [ ] **Step 1.6: Fix ManagementServer.Stop() to use graceful shutdown**

Replace the current Stop() (lines 86-90):

Before:
```go
func (m *ManagementServer) Stop() {
	if m.server != nil {
		m.server.Close()
	}
}
```

After:
```go
func (m *ManagementServer) Stop() {
	if m.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		m.server.Shutdown(ctx)
	}
}
```

- [ ] **Step 1.7: Wire chatStore in main.go**

In `cmd/cc-connect/main.go`, find where ManagementServer is started (search for `NewManagementServer`). After creation, add:
```go
mgmtSrv.SetChatStore(chatStore)
```

- [ ] **Step 1.8: Write test for /healthz**

Add a test (in `core/management_test.go` or an existing test file):
```go
func TestHealthzEndpoint(t *testing.T) {
	// Create a ManagementServer with no auth token (skip auth)
	srv := NewManagementServer("", 0, nil)

	// Test healthy response
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	srv.handleHealthz(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("healthz status = %d, want 200", w.Code)
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "ok" {
		t.Errorf("healthz status = %q, want %q", resp["status"], "ok")
	}
}
```

- [ ] **Step 1.9: Verify build and tests pass**

Run: `go build ./... && go test ./core/ -run TestHealthz -v -count=1`
Expected: PASS

- [ ] **Step 1.10: Commit**

```bash
git add core/chatstore.go core/chatstore_mysql.go core/management.go cmd/cc-connect/main.go
git commit -m "feat(ops): add /healthz endpoint and graceful shutdown to ManagementServer"
```

---

## Task 2: Doctor — add MySQL and Platform health checks

**Files:**
- Modify: `core/interfaces.go` (add HealthChecker interface, after line 431)
- Modify: `core/doctor.go` (add checkChatStore, enhance checkPlatforms)
- Modify: `core/i18n.go` (add i18n keys for new checks)
- Test: existing doctor tests or new

- [ ] **Step 2.1: Add HealthChecker interface to interfaces.go**

Append after the last interface (BaseSessionKeyer, line 430):
```go

// HealthChecker is an optional interface for platforms that support
// runtime health/connectivity checks. Used by the /doctor command.
type HealthChecker interface {
	HealthCheck(ctx context.Context) error
}
```

- [ ] **Step 2.2: Add i18n keys for new doctor checks**

In `core/i18n.go`, add new MsgKey constants (before the closing `)` of the const block at line 537):
```go
MsgDoctorChatStore MsgKey = "doctor_chatstore"
MsgDoctorPlatformHealth MsgKey = "doctor_platform_health"
```

Add translations in the `messages` map. Find a doctor-related entry (like `MsgDoctorRunning`) and add nearby:
```go
MsgDoctorChatStore: {
	LangEnglish:            "ChatStore (MySQL)",
	LangChinese:            "ChatStore (MySQL)",
	LangTraditionalChinese: "ChatStore (MySQL)",
	LangJapanese:           "ChatStore (MySQL)",
	LangSpanish:            "ChatStore (MySQL)",
},
MsgDoctorPlatformHealth: {
	LangEnglish:            "Platform Health",
	LangChinese:            "平台连接健康",
	LangTraditionalChinese: "平台連線健康",
	LangJapanese:           "プラットフォーム接続",
	LangSpanish:            "Salud de plataforma",
},
```

- [ ] **Step 2.3: Update RunDoctorChecks signature to accept ChatStore**

Modify `RunDoctorChecks` (line 56) to accept an optional ChatStore:
```go
func RunDoctorChecks(ctx context.Context, agent Agent, platforms []Platform, chatStore ChatStore) []DoctorCheckResult {
```

Add the chatStore check to the checks slice (after `checkPlatforms`, around line 61):
```go
results = append(results, checkChatStore(ctx, chatStore)...)
```

- [ ] **Step 2.4: Implement checkChatStore**

Add new function in `core/doctor.go`:
```go
func checkChatStore(ctx context.Context, cs ChatStore) []DoctorCheckResult {
	if cs == nil {
		return []DoctorCheckResult{{
			Name:   "ChatStore (MySQL)",
			Status: DoctorWarn,
			Detail: "not configured (database.dsn is empty)",
		}}
	}

	start := time.Now()
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := cs.Ping(pingCtx); err != nil {
		return []DoctorCheckResult{{
			Name:    "ChatStore (MySQL)",
			Status:  DoctorFail,
			Detail:  err.Error(),
			Latency: time.Since(start),
		}}
	}
	return []DoctorCheckResult{{
		Name:    "ChatStore (MySQL)",
		Status:  DoctorPass,
		Detail:  "connected",
		Latency: time.Since(start),
	}}
}
```

- [ ] **Step 2.5: Enhance checkPlatforms to use HealthChecker interface**

Replace the current `checkPlatforms` function (lines 153-170). The new version checks for the optional `HealthChecker` interface:

```go
func checkPlatforms(ctx context.Context, platforms []Platform) []DoctorCheckResult {
	var results []DoctorCheckResult
	for _, p := range platforms {
		r := DoctorCheckResult{Name: "Platform: " + p.Name()}

		if hc, ok := p.(HealthChecker); ok {
			start := time.Now()
			checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := hc.HealthCheck(checkCtx)
			cancel()
			r.Latency = time.Since(start)
			if err != nil {
				r.Status = DoctorFail
				r.Detail = err.Error()
			} else {
				r.Status = DoctorPass
				r.Detail = "connected"
			}
		} else {
			r.Status = DoctorPass
			r.Detail = "configured (no health check available)"
		}
		results = append(results, r)
	}
	return results
}
```

- [ ] **Step 2.6: Update all callers of RunDoctorChecks**

Search for `RunDoctorChecks(` across the codebase and add the `chatStore` parameter. The primary caller is in `core/engine_cmd_system.go` (the `/doctor` command handler `cmdDoctor`). The engine has a `chatStore` field — pass it through.

Also check `core/management.go` if it calls RunDoctorChecks.

- [ ] **Step 2.7: Verify build and tests pass**

Run: `go build ./... && go test ./core/ -count=1`
Expected: PASS

- [ ] **Step 2.8: Commit**

```bash
git add core/interfaces.go core/doctor.go core/i18n.go core/engine_cmd_system.go
git commit -m "feat(ops): add MySQL and platform health checks to /doctor"
```

---

## Task 3: ChatStore retry logic

**Files:**
- Modify: `core/chatstore_mysql.go` (add retry to doSaveMessage, doEnsureSession)
- Test: `core/chatstore_mysql_test.go` (if exists, or create)

- [ ] **Step 3.1: Add retry helper function**

Add a private helper in `core/chatstore_mysql.go`:
```go
// retryWrite retries fn up to maxRetries times with a fixed delay between attempts.
// Returns the last error if all attempts fail.
func retryWrite(maxRetries int, delay time.Duration, fn func() error) error {
	var err error
	for i := 0; i <= maxRetries; i++ {
		err = fn()
		if err == nil {
			return nil
		}
		if i < maxRetries {
			time.Sleep(delay)
		}
	}
	return err
}
```

- [ ] **Step 3.2: Apply retry to doSaveMessage**

Wrap the core INSERT logic in `doSaveMessage` (lines 330-372) with retryWrite. The current function does:
1. Create context with 15s timeout
2. Execute INSERT
3. Log error if failed

Refactor to:
```go
func (s *MySQLChatStore) doSaveMessage(msg ChatMessage) {
	err := retryWrite(2, 1*time.Second, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		_, err := s.db.ExecContext(ctx,
			// ... existing INSERT SQL stays exactly the same ...
		)
		return err
	})
	if err != nil {
		slog.Error("chatstore: save message failed after retries",
			"session_id", msg.SessionID,
			"role", msg.Role,
			"error", err)
	}
}
```

Keep the exact same SQL and parameters. Only change: wrap in retryWrite, update error log message to indicate retries were attempted.

- [ ] **Step 3.3: Apply retry to doEnsureSession**

Same pattern for `doEnsureSession` (lines 375-413):
```go
func (s *MySQLChatStore) doEnsureSession(info ChatSessionInfo) {
	err := retryWrite(2, 1*time.Second, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		_, err := s.db.ExecContext(ctx,
			// ... existing UPSERT SQL stays exactly the same ...
		)
		return err
	})
	if err != nil {
		slog.Error("chatstore: ensure session failed after retries",
			"session_id", info.SessionID,
			"error", err)
	}
}
```

- [ ] **Step 3.4: Write test for retryWrite**

```go
func TestRetryWrite(t *testing.T) {
	// Test: succeeds on first try
	calls := 0
	err := retryWrite(2, 0, func() error {
		calls++
		return nil
	})
	if err != nil || calls != 1 {
		t.Errorf("succeed first try: err=%v, calls=%d", err, calls)
	}

	// Test: fails then succeeds
	calls = 0
	err = retryWrite(2, 0, func() error {
		calls++
		if calls < 2 {
			return fmt.Errorf("transient error")
		}
		return nil
	})
	if err != nil || calls != 2 {
		t.Errorf("retry success: err=%v, calls=%d", err, calls)
	}

	// Test: all retries fail
	calls = 0
	err = retryWrite(2, 0, func() error {
		calls++
		return fmt.Errorf("persistent error")
	})
	if err == nil || calls != 3 {
		t.Errorf("all fail: err=%v, calls=%d (want 3)", err, calls)
	}
}
```

- [ ] **Step 3.5: Verify build and tests pass**

Run: `go build ./core/ && go test ./core/ -run TestRetryWrite -v -count=1`
Expected: PASS

- [ ] **Step 3.6: Commit**

```bash
git add core/chatstore_mysql.go core/chatstore_mysql_test.go
git commit -m "feat(ops): add retry logic to ChatStore write operations"
```

---

## Task 4: SIGHUP hot reload + systemd ExecReload

**Files:**
- Modify: `cmd/cc-connect/main.go` (add SIGHUP handling)
- Modify: `daemon/systemd.go` (add ExecReload to unit template)

- [ ] **Step 4.1: Add SIGHUP to signal.Notify**

In `cmd/cc-connect/main.go`, modify the signal.Notify line (line 856):

Before:
```go
signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
```

After:
```go
signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
```

- [ ] **Step 4.2: Update the select block to handle SIGHUP**

Replace the select block (lines 859-864) with one that distinguishes HUP from INT/TERM:

```go
for {
	select {
	case sig := <-sigCh:
		if sig == syscall.SIGHUP {
			slog.Info("received SIGHUP, reloading config")
			for _, e := range engines {
				if result, err := reloadConfig(configPath, e.Name(), e); err != nil {
					slog.Error("config reload failed", "project", e.Name(), "error", err)
				} else {
					slog.Info("config reloaded", "project", e.Name(), "changed", result.Changed)
				}
			}
			continue // don't exit, keep running
		}
		// SIGINT or SIGTERM — proceed to shutdown
	case req := <-core.RestartCh:
		restartReq = &req
		slog.Info("restart requested via /restart command", "session", req.SessionKey, "platform", req.Platform)
	}
	break
}
```

Key detail: SIGHUP reloads config and `continue`s the loop. SIGINT/SIGTERM `break`s and proceeds to shutdown.

- [ ] **Step 4.3: Add ExecReload to systemd unit template**

In `daemon/systemd.go`, find the `buildUnit` method (line 162-187). After the `ExecStart` line (line 171), add:
```go
fmt.Fprintf(&sb, "ExecReload=/bin/kill -HUP $MAINPID\n")
```

This enables `systemctl reload cc-connect` to trigger config reload via SIGHUP.

- [ ] **Step 4.4: Verify build passes**

Run: `go build ./...`
Expected: PASS

- [ ] **Step 4.5: Commit**

```bash
git add cmd/cc-connect/main.go daemon/systemd.go
git commit -m "feat(ops): add SIGHUP hot reload and systemd ExecReload support"
```

---

## Task 5: Final verification

- [ ] **Step 5.1: Full build**

Run: `go build ./...`
Expected: PASS

- [ ] **Step 5.2: Full test suite**

Run: `go test ./... -count=1`
Expected: ALL PASS (except pre-existing `TestWecomInboundFileMime`)

- [ ] **Step 5.3: Race detector**

Run: `go test -race ./core/ -count=1`
Expected: PASS

- [ ] **Step 5.4: Manual healthz test (if server is running)**

```bash
curl http://localhost:9820/healthz
```
Expected: `{"status":"ok","uptime":"..."}` with HTTP 200
