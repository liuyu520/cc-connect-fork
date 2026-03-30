package core

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// ProjectRouter manages multiple engines sharing a single platform instance.
// When multiple [[projects]] in the config use the same IM platform credentials,
// only one connection is established. The router dispatches incoming messages
// to the correct engine based on per-session bindings.
//
// First-time users are prompted to select a project; subsequent messages are
// routed automatically. The /project command allows switching at any time.
type ProjectRouter struct {
	platform  Platform
	engines   []*projectEntry          // ordered project list
	engineMap map[string]*projectEntry // projectName → entry
	bindings  map[string]string        // sessionKey → projectName
	pending   map[string]*pendingSelect
	mu        sync.RWMutex
	i18n      *I18n
	storePath string // JSON file for persisting bindings
}

// projectEntry associates a project name with its engine.
type projectEntry struct {
	name   string
	engine *Engine
}

// pendingSelect tracks a session awaiting project selection.
type pendingSelect struct {
	originalMsg *Message
}

// NewProjectRouter creates a router for the given shared platform.
func NewProjectRouter(platform Platform, i18n *I18n, storePath string) *ProjectRouter {
	return &ProjectRouter{
		platform:  platform,
		engineMap: make(map[string]*projectEntry),
		bindings:  make(map[string]string),
		pending:   make(map[string]*pendingSelect),
		i18n:      i18n,
		storePath: storePath,
	}
}

// AddProject registers a project/engine pair with the router.
func (r *ProjectRouter) AddProject(name string, engine *Engine) {
	entry := &projectEntry{name: name, engine: engine}
	r.engines = append(r.engines, entry)
	r.engineMap[name] = entry
}

// Start loads persisted bindings and starts the underlying platform.
// It also wires the lifecycle handler for async-recoverable platforms.
func (r *ProjectRouter) Start() error {
	r.loadBindings()

	// Wire lifecycle handler for async platforms
	if async, ok := r.platform.(AsyncRecoverablePlatform); ok {
		async.SetLifecycleHandler(r)
	}

	// Start the platform with the router's message handler
	if err := r.platform.Start(r.handleMessage); err != nil {
		return fmt.Errorf("project_router: start platform %s: %w", r.platform.Name(), err)
	}

	// For synchronous platforms, notify all engines immediately
	if _, isAsync := r.platform.(AsyncRecoverablePlatform); !isAsync {
		r.onAllEnginesReady()
	}

	slog.Info("project_router started",
		"platform", r.platform.Name(),
		"projects", len(r.engines))
	return nil
}

// Stop stops the underlying platform.
func (r *ProjectRouter) Stop() error {
	return r.platform.Stop()
}

// OnPlatformReady implements PlatformLifecycleHandler for async platforms.
func (r *ProjectRouter) OnPlatformReady(p Platform) {
	slog.Info("project_router: platform ready", "platform", p.Name())
	r.onAllEnginesReady()
}

// OnPlatformUnavailable implements PlatformLifecycleHandler for async platforms.
func (r *ProjectRouter) OnPlatformUnavailable(p Platform, err error) {
	slog.Warn("project_router: platform unavailable", "platform", p.Name(), "error", err)
	for _, entry := range r.engines {
		entry.engine.OnPlatformUnavailable(p, err)
	}
}

// onAllEnginesReady notifies all engines that the shared platform is ready.
func (r *ProjectRouter) onAllEnginesReady() {
	for _, entry := range r.engines {
		entry.engine.OnPlatformReady(r.platform)
	}
}

// baseSessionKey returns a broader session key for project binding fallback.
// For platforms with thread isolation (e.g., Feishu), thread-specific session keys
// differ per top-level message. This method returns a user-in-chat level key so that
// /project switches apply across all threads in the same chat.
func (r *ProjectRouter) baseSessionKey(msg *Message) string {
	if baser, ok := r.platform.(BaseSessionKeyer); ok {
		return baser.BaseSessionKey(msg)
	}
	return msg.SessionKey
}

// setBinding stores the project binding for the given session key, and also
// stores a base-key binding so that new threads inherit the project selection.
func (r *ProjectRouter) setBinding(msg *Message, projectName string) {
	r.bindings[msg.SessionKey] = projectName
	// Also store a broader binding for thread-isolated platforms,
	// so new threads in the same chat inherit the project selection.
	if baseKey := r.baseSessionKey(msg); baseKey != msg.SessionKey {
		r.bindings[baseKey] = projectName
	}
}

// handleMessage is the core routing logic invoked by the platform on each message.
func (r *ProjectRouter) handleMessage(p Platform, msg *Message) {
	content := strings.TrimSpace(msg.Content)

	// Intercept /project command
	if strings.HasPrefix(content, "/project") {
		args := strings.TrimSpace(strings.TrimPrefix(content, "/project"))
		r.handleProjectCommand(p, msg, args)
		return
	}

	// Intercept button callbacks for project selection
	if strings.HasPrefix(content, "__project__:") {
		r.handleButtonCallback(p, msg, strings.TrimPrefix(content, "__project__:"))
		return
	}

	r.mu.RLock()
	projectName, bound := r.bindings[msg.SessionKey]
	// Fallback: check base session key for thread-isolated platforms.
	// This allows /project switches to apply across all threads in the same chat.
	if !bound {
		if baseKey := r.baseSessionKey(msg); baseKey != msg.SessionKey {
			projectName, bound = r.bindings[baseKey]
		}
	}
	_, isPending := r.pending[msg.SessionKey]
	r.mu.RUnlock()

	// Already bound → route to the target engine
	if bound {
		entry, ok := r.engineMap[projectName]
		if ok {
			entry.engine.HandleIncomingMessage(p, msg)
			return
		}
		// Binding references a removed project; clear it
		r.mu.Lock()
		delete(r.bindings, msg.SessionKey)
		r.mu.Unlock()
	}

	// Pending selection → try to parse the user's choice
	if isPending {
		r.handleSelection(p, msg)
		return
	}

	// If only one project, auto-bind without asking
	if len(r.engines) == 1 {
		entry := r.engines[0]
		r.mu.Lock()
		r.bindings[msg.SessionKey] = entry.name
		r.mu.Unlock()
		r.saveBindings()
		entry.engine.HandleIncomingMessage(p, msg)
		return
	}

	// No binding, not pending → show project selection
	r.showProjectSelection(p, msg)
}

// showProjectSelection sends a project picker UI to the user.
func (r *ProjectRouter) showProjectSelection(p Platform, msg *Message) {
	var names []string
	for _, e := range r.engines {
		names = append(names, e.name)
	}

	// Prefer inline buttons if the platform supports them
	if bs, ok := p.(InlineButtonSender); ok {
		var buttons [][]ButtonOption
		for i, name := range names {
			buttons = append(buttons, []ButtonOption{{
				Text: fmt.Sprintf("%d. %s", i+1, name),
				Data: "__project__:" + name,
			}})
		}
		ctx := context.Background()
		if err := bs.SendWithButtons(ctx, msg.ReplyCtx, r.i18n.T(MsgProjectSelect), buttons); err != nil {
			slog.Warn("project_router: send buttons failed, falling back to text", "error", err)
			r.sendTextSelection(p, msg, names)
		}
	} else {
		r.sendTextSelection(p, msg, names)
	}

	// Cache the original message so we can forward it after selection
	r.mu.Lock()
	r.pending[msg.SessionKey] = &pendingSelect{originalMsg: msg}
	r.mu.Unlock()
}

// sendTextSelection sends a plain-text numbered project list.
func (r *ProjectRouter) sendTextSelection(p Platform, msg *Message, names []string) {
	var lines []string
	for i, name := range names {
		lines = append(lines, fmt.Sprintf("%d. %s", i+1, name))
	}
	text := r.i18n.T(MsgProjectSelect) + "\n" + strings.Join(lines, "\n")
	ctx := context.Background()
	_ = p.Reply(ctx, msg.ReplyCtx, text)
}

// handleButtonCallback handles inline button clicks for project selection.
func (r *ProjectRouter) handleButtonCallback(p Platform, msg *Message, projectName string) {
	r.mu.RLock()
	ps, hasPending := r.pending[msg.SessionKey]
	oldProject := r.bindings[msg.SessionKey]
	r.mu.RUnlock()

	entry, ok := r.engineMap[projectName]
	if !ok {
		ctx := context.Background()
		_ = p.Reply(ctx, msg.ReplyCtx, r.i18n.T(MsgProjectInvalid))
		return
	}

	// Cleanup old engine's session if switching from an existing binding
	if oldProject != "" && oldProject != projectName {
		if oldEntry, ok := r.engineMap[oldProject]; ok {
			oldEntry.engine.CleanupSession(msg.SessionKey)
		}
	}

	r.mu.Lock()
	r.setBinding(msg, projectName)
	delete(r.pending, msg.SessionKey)
	r.mu.Unlock()
	r.saveBindings()

	ctx := context.Background()
	_ = p.Reply(ctx, msg.ReplyCtx, r.i18n.Tf(MsgProjectSwitched, projectName))

	// Forward the original cached message
	if hasPending && ps.originalMsg != nil && ps.originalMsg.Content != "" {
		entry.engine.HandleIncomingMessage(p, ps.originalMsg)
	}
}

// handleSelection processes a text response during project selection.
func (r *ProjectRouter) handleSelection(p Platform, msg *Message) {
	r.mu.RLock()
	ps, ok := r.pending[msg.SessionKey]
	oldProject := r.bindings[msg.SessionKey]
	if !ok {
		r.mu.RUnlock()
		return
	}
	r.mu.RUnlock()

	projectName := r.matchProject(strings.TrimSpace(msg.Content))
	if projectName == "" {
		ctx := context.Background()
		_ = p.Reply(ctx, msg.ReplyCtx, r.i18n.T(MsgProjectInvalid))
		return
	}

	// Cleanup old engine's session if switching from an existing binding
	if oldProject != "" && oldProject != projectName {
		if oldEntry, ok := r.engineMap[oldProject]; ok {
			oldEntry.engine.CleanupSession(msg.SessionKey)
		}
	}

	r.mu.Lock()
	r.setBinding(msg, projectName)
	delete(r.pending, msg.SessionKey)
	r.mu.Unlock()
	r.saveBindings()

	ctx := context.Background()
	_ = p.Reply(ctx, msg.ReplyCtx, r.i18n.Tf(MsgProjectSwitched, projectName))

	// Forward the original cached message
	if ps.originalMsg != nil && ps.originalMsg.Content != "" {
		if entry, ok := r.engineMap[projectName]; ok {
			entry.engine.HandleIncomingMessage(p, ps.originalMsg)
		}
	}
}

// handleProjectCommand handles /project and its subcommands.
//
//	/project           — show current project + list
//	/project <name|#>  — switch to a project
//	/project list      — list all projects
func (r *ProjectRouter) handleProjectCommand(p Platform, msg *Message, args string) {
	ctx := context.Background()

	if args == "" || strings.EqualFold(args, "list") {
		// Show current + list with session status
		r.mu.RLock()
		current := r.bindings[msg.SessionKey]
		// Fallback to base key for thread-isolated platforms
		if current == "" {
			if baseKey := r.baseSessionKey(msg); baseKey != msg.SessionKey {
				current = r.bindings[baseKey]
			}
		}
		r.mu.RUnlock()

		var sb strings.Builder
		if current != "" {
			sb.WriteString(r.i18n.Tf(MsgProjectCurrent, current))
			sb.WriteString("\n\n")
		}
		sb.WriteString(r.i18n.T(MsgProjectList))
		sb.WriteString("\n")
		for i, e := range r.engines {
			marker := "  "
			if e.name == current {
				marker = "→ "
			}
			// Query session status for enriched display
			info := e.engine.GetSessionInfo(msg.SessionKey)
			status := ""
			if info.HasActiveSession {
				status = " 🟢"
			}
			workDir := ""
			if info.WorkDir != "" {
				workDir = " (" + filepath.Base(info.WorkDir) + ")"
			}
			sb.WriteString(fmt.Sprintf("%s%d. %s%s%s\n", marker, i+1, e.name, workDir, status))
		}
		sb.WriteString("\n")
		sb.WriteString(r.i18n.T(MsgProjectHelp))
		_ = p.Reply(ctx, msg.ReplyCtx, sb.String())
		return
	}

	// Try to match project by name or number
	projectName := r.matchProject(args)
	if projectName == "" {
		_ = p.Reply(ctx, msg.ReplyCtx, r.i18n.T(MsgProjectInvalid))
		return
	}

	// Cleanup old engine's session before switching
	r.mu.RLock()
	oldProject := r.bindings[msg.SessionKey]
	r.mu.RUnlock()

	if oldProject != "" && oldProject != projectName {
		if oldEntry, ok := r.engineMap[oldProject]; ok {
			oldEntry.engine.CleanupSession(msg.SessionKey)
		}
	}

	r.mu.Lock()
	r.setBinding(msg, projectName)
	r.mu.Unlock()
	r.saveBindings()

	_ = p.Reply(ctx, msg.ReplyCtx, r.i18n.Tf(MsgProjectSwitched, projectName))
}

// matchProject resolves user input to a project name.
// Supports: exact name match (case-insensitive), numeric index (1-based).
func (r *ProjectRouter) matchProject(input string) string {
	// Try numeric index
	if n, err := strconv.Atoi(input); err == nil {
		if n >= 1 && n <= len(r.engines) {
			return r.engines[n-1].name
		}
		return ""
	}

	// Exact match (case-insensitive)
	for _, e := range r.engines {
		if strings.EqualFold(e.name, input) {
			return e.name
		}
	}

	// Prefix match (case-insensitive)
	lower := strings.ToLower(input)
	var match string
	for _, e := range r.engines {
		if strings.HasPrefix(strings.ToLower(e.name), lower) {
			if match != "" {
				return "" // ambiguous
			}
			match = e.name
		}
	}
	return match
}

// --- Binding persistence ---

// bindingData is the JSON structure for persisting session→project bindings.
type bindingData struct {
	Bindings map[string]string `json:"bindings"`
}

func (r *ProjectRouter) loadBindings() {
	if r.storePath == "" {
		return
	}
	data, err := os.ReadFile(r.storePath)
	if err != nil {
		return // file may not exist yet
	}
	var bd bindingData
	if err := json.Unmarshal(data, &bd); err != nil {
		slog.Warn("project_router: failed to parse bindings", "path", r.storePath, "error", err)
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Only load bindings for projects that still exist
	for k, v := range bd.Bindings {
		if _, ok := r.engineMap[v]; ok {
			r.bindings[k] = v
		}
	}
	slog.Info("project_router: loaded bindings", "count", len(r.bindings), "path", r.storePath)
}

func (r *ProjectRouter) saveBindings() {
	if r.storePath == "" {
		return
	}

	r.mu.RLock()
	bd := bindingData{Bindings: make(map[string]string, len(r.bindings))}
	for k, v := range r.bindings {
		bd.Bindings[k] = v
	}
	r.mu.RUnlock()

	data, err := json.MarshalIndent(bd, "", "  ")
	if err != nil {
		slog.Warn("project_router: failed to marshal bindings", "error", err)
		return
	}

	// Ensure parent directory exists
	if dir := filepath.Dir(r.storePath); dir != "" {
		os.MkdirAll(dir, 0o755)
	}

	if err := os.WriteFile(r.storePath, data, 0o644); err != nil {
		slog.Warn("project_router: failed to save bindings", "path", r.storePath, "error", err)
	}
}

// Platform returns the underlying shared platform instance.
func (r *ProjectRouter) Platform() Platform {
	return r.platform
}
