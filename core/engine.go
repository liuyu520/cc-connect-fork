package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const maxPlatformMessageLen = 4000
const maxQueuedMessages = 5 // cap queued messages to bound memory usage

const (
	defaultThinkingMaxLen = 300
	defaultToolMaxLen     = 500
)

// Slow-operation thresholds. Operations exceeding these durations produce a
// slog.Warn so operators can quickly pinpoint bottlenecks.
const (
	slowPlatformSend    = 2 * time.Second  // platform Reply / Send
	slowAgentStart      = 5 * time.Second  // agent.StartSession
	slowAgentClose      = 3 * time.Second  // agentSession.Close
	slowAgentSend       = 2 * time.Second  // agentSession.Send
	slowAgentFirstEvent = 15 * time.Second // time from send to first agent event
)

// VersionInfo is set by main at startup so that /version works.
var VersionInfo string

// CurrentVersion is the semver tag (e.g. "v1.2.0-beta.1"), set by main.
var CurrentVersion string

// ErrAttachmentSendDisabled indicates that side-channel image/file delivery is disabled by config.
var ErrAttachmentSendDisabled = errors.New("attachment send is disabled by config")

// RestartRequest carries info needed to send a post-restart notification.
type RestartRequest struct {
	SessionKey string `json:"session_key"`
	Platform   string `json:"platform"`
}

// SaveRestartNotify persists restart info so the new process can send
// a "restart successful" message after startup.
func SaveRestartNotify(dataDir string, req RestartRequest) error {
	dir := filepath.Join(dataDir, "run")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Warn("SaveRestartNotify: mkdir failed", "dir", dir, "error", err)
	}
	data, _ := json.Marshal(req)
	return os.WriteFile(filepath.Join(dir, "restart_notify"), data, 0o644)
}

// ConsumeRestartNotify reads and deletes the restart notification file.
// Returns nil if no notification is pending.
func ConsumeRestartNotify(dataDir string) *RestartRequest {
	p := filepath.Join(dataDir, "run", "restart_notify")
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	os.Remove(p)
	var req RestartRequest
	if json.Unmarshal(data, &req) != nil {
		return nil
	}
	return &req
}

// SendRestartNotification sends a "restart successful" message to the
// platform/session that initiated the restart.
func (e *Engine) SendRestartNotification(platformName, sessionKey string) {
	for _, p := range e.platforms {
		if p.Name() != platformName {
			continue
		}
		rc, ok := p.(ReplyContextReconstructor)
		if !ok {
			slog.Debug("restart notify: platform does not support ReconstructReplyCtx", "platform", platformName)
			return
		}
		rctx, err := rc.ReconstructReplyCtx(sessionKey)
		if err != nil {
			slog.Debug("restart notify: reconstruct failed", "error", err)
			return
		}
		text := e.i18n.T(MsgRestartSuccess)
		if CurrentVersion != "" {
			text += fmt.Sprintf(" (%s)", CurrentVersion)
		}
		if err := p.Send(e.ctx, rctx, text); err != nil {
			slog.Debug("restart notify: send failed", "error", err)
		}
		return
	}
}

// RestartCh is signaled when /restart is invoked. main listens on it
// to perform a graceful shutdown followed by syscall.Exec.
var RestartCh = make(chan RestartRequest, 1)

// DisplayCfg controls truncation of intermediate messages.
// A value of -1 means "use default", 0 means "no truncation".
type DisplayCfg struct {
	ThinkingMaxLen int // max runes for thinking preview; 0 = no truncation
	ToolMaxLen     int // max runes for tool use preview; 0 = no truncation
}

// RateLimitCfg controls per-session message rate limiting.
type RateLimitCfg struct {
	MaxMessages int           // max messages per window; 0 = disabled
	Window      time.Duration // sliding window size
}

// Engine routes messages between platforms and the agent for a single project.
// sessionListCacheEntry holds a cached ListSessions result with expiry.
type sessionListCacheEntry struct {
	sessions []AgentSessionInfo
	at       time.Time
}

const sessionListCacheTTL = 30 * time.Second

type Engine struct {
	name                  string
	agent                 Agent
	platforms             []Platform
	sessions              *SessionManager
	ctx                   context.Context
	cancel                context.CancelFunc
	i18n                  *I18n
	speech                SpeechCfg
	tts                   *TTSCfg
	display               DisplayCfg
	defaultQuiet          bool
	injectSender          bool
	attachmentSendEnabled bool
	startedAt             time.Time

	providerSaveFunc       func(providerName string) error
	providerAddSaveFunc    func(p ProviderConfig) error
	providerRemoveSaveFunc func(name string) error
	providerModelSaveFunc  func(providerName, model string) error
	modelSaveFunc          func(model string) error

	ttsSaveFunc func(mode string) error

	commandSaveAddFunc func(name, description, prompt, exec, workDir string) error
	commandSaveDelFunc func(name string) error

	displaySaveFunc  func(thinkingMaxLen, toolMaxLen *int) error
	configReloadFunc func() (*ConfigReloadResult, error)

	cronScheduler      *CronScheduler
	heartbeatScheduler *HeartbeatScheduler

	commands         *CommandRegistry
	skills           *SkillRegistry
	builtinCommandDefs []builtinCommandDef // 注册表式命令定义，NewEngine 中初始化
	aliases          map[string]string // trigger → command (e.g. "帮助" → "/help")
	aliasMu  sync.RWMutex

	// 快捷短语后缀：当消息长度超过阈值时自动追加后缀
	quickPhraseSuffixMinLen int
	quickPhraseSuffixText   string

	// 命令前缀：以指定前缀开头的消息自动展开为命令执行指令
	// 例如 prefix="!" template="执行命令: %s" → "!git status" → "执行命令: git status"
	quickPhraseCmdPrefix   string
	quickPhraseCmdTemplate string

	aliasSaveAddFunc func(name, command string) error
	aliasSaveDelFunc func(name string) error

	bannedWords []string
	bannedMu    sync.RWMutex

	disabledCmds map[string]bool
	adminFrom    string           // comma-separated user IDs for privileged commands; "*" = all allowed users; "" = deny
	userRoles    *UserRoleManager // nil = legacy mode (no per-user policies)
	userRolesMu  sync.RWMutex     // protects userRoles, disabledCmds, and adminFrom

	rateLimiter      *RateLimiter
	streamPreview    StreamPreviewCfg
	relayManager     *RelayManager
	eventIdleTimeout time.Duration
	dirHistory       *DirHistory
	baseWorkDir      string
	projectState     *ProjectStateStore

	// Auto-compress settings
	autoCompressEnabled   bool
	autoCompressMaxTokens int
	autoCompressMinGap    time.Duration

	// When true, append [ctx: ~N%] (or model self-report) to assistant replies shown on platforms.
	showContextIndicator bool

	// When true, send a "session complete" notification to IM after the agent finishes a turn with no pending messages.
	sessionCompleteNotify bool

	// When true, show tool_use/tool_result messages on IM platforms. When false, suppress them independently of quiet mode.
	showToolProcess bool

	// PatrolScheduler for recording IM user activity (nil if patrol not configured)
	patrolScheduler *PatrolScheduler

	// External platforms managed by ProjectRouter (Start/Stop skipped)
	externalPlatforms map[Platform]bool

	// Multi-workspace mode
	multiWorkspace    bool
	baseDir           string
	workspaceBindings *WorkspaceBindingManager
	workspacePool     *workspacePool
	initFlows         map[string]*workspaceInitFlow // workspace channel key → init state
	initFlowsMu       sync.Mutex

	// Interactive agent session management
	interactiveMu     sync.Mutex
	interactiveStates map[string]*interactiveState // key = sessionKey

	quietMu sync.RWMutex
	quiet   bool // when true, suppress thinking and tool progress messages globally

	// Session list cache to avoid expensive I/O on card page turns (3s timeout).
	sessionListCacheMu sync.RWMutex
	sessionListCache   map[Agent]*sessionListCacheEntry

	platformLifecycleMu sync.Mutex
	platformReady       map[Platform]bool
	stopping            bool

	// chatStore is an optional MySQL-backed chat history store.
	// When non-nil, chat messages are asynchronously persisted to MySQL alongside JSON files.
	chatStore ChatStore
}

// workspaceInitFlow tracks a channel that is being onboarded to a workspace.
type workspaceInitFlow struct {
	state       string // "awaiting_url", "awaiting_confirm"
	repoURL     string
	cloneTo     string
	channelName string
}

// listSessionsCached returns ListSessions result, using a short-lived cache
// to avoid expensive I/O on every card page turn (Feishu 3s callback timeout).
func (e *Engine) listSessionsCached(agent Agent) ([]AgentSessionInfo, error) {
	e.sessionListCacheMu.RLock()
	if entry, ok := e.sessionListCache[agent]; ok && time.Since(entry.at) < sessionListCacheTTL {
		e.sessionListCacheMu.RUnlock()
		return entry.sessions, nil
	}
	e.sessionListCacheMu.RUnlock()

	sessions, err := agent.ListSessions(e.ctx)
	if err != nil {
		return nil, err
	}

	e.sessionListCacheMu.Lock()
	if e.sessionListCache == nil {
		e.sessionListCache = make(map[Agent]*sessionListCacheEntry)
	}
	e.sessionListCache[agent] = &sessionListCacheEntry{sessions: sessions, at: time.Now()}
	e.sessionListCacheMu.Unlock()

	return sessions, nil
}

// invalidateSessionListCache clears the cached ListSessions result for the given agent.
func (e *Engine) invalidateSessionListCache(agent Agent) {
	e.sessionListCacheMu.Lock()
	delete(e.sessionListCache, agent)
	e.sessionListCacheMu.Unlock()
}


func NewEngine(name string, ag Agent, platforms []Platform, sessionStorePath string, lang Language) *Engine {
	ctx, cancel := context.WithCancel(context.Background())
	e := &Engine{
		name:                  name,
		agent:                 ag,
		platforms:             platforms,
		sessions:              NewSessionManager(sessionStorePath),
		ctx:                   ctx,
		cancel:                cancel,
		i18n:                  NewI18n(lang),
		attachmentSendEnabled: true,
		display:               DisplayCfg{ThinkingMaxLen: defaultThinkingMaxLen, ToolMaxLen: defaultToolMaxLen},
		commands:              NewCommandRegistry(),
		skills:                NewSkillRegistry(),
		aliases:               make(map[string]string),
		interactiveStates:     make(map[string]*interactiveState),
		platformReady:         make(map[Platform]bool),
		externalPlatforms:     make(map[Platform]bool),
		startedAt:             time.Now(),
		streamPreview:         DefaultStreamPreviewCfg(),
		eventIdleTimeout:      defaultEventIdleTimeout,
		showContextIndicator:  true,
		showToolProcess:       true,
	}

	if ag != nil {
		e.sessions.InvalidateForAgent(ag.Name())
	}

	if cp, ok := ag.(CommandProvider); ok {
		e.commands.SetAgentDirs(cp.CommandDirs())
	}
	if sp, ok := ag.(SkillProvider); ok {
		e.skills.SetDirs(sp.SkillDirs())
	}

	e.initBuiltinCommands()

	return e
}

// SetChatStore sets the optional ChatStore for MySQL chat history persistence.
func (e *Engine) SetChatStore(cs ChatStore) {
	e.chatStore = cs
}

// SetMultiWorkspace enables multi-workspace mode for the engine.
func (e *Engine) SetMultiWorkspace(baseDir, bindingStorePath string) {
	e.multiWorkspace = true
	e.baseDir = baseDir
	e.workspaceBindings = NewWorkspaceBindingManager(bindingStorePath)
	e.workspacePool = newWorkspacePool(15 * time.Minute)
	e.initFlows = make(map[string]*workspaceInitFlow)
	go e.runIdleReaper()
}

func (e *Engine) runIdleReaper() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			if e.workspacePool == nil {
				continue
			}
			reaped := e.workspacePool.ReapIdle()
			for _, ws := range reaped {
				e.interactiveMu.Lock()
				for key, state := range e.interactiveStates {
					if state.workspaceDir == ws {
						if state.agentSession != nil {
							state.agentSession.Close()
						}
						delete(e.interactiveStates, key)
					}
				}
				e.interactiveMu.Unlock()
				slog.Info("workspace idle-reaped", "workspace", ws)
			}
		}
	}
}

// SetSpeechConfig configures the speech-to-text subsystem.
func (e *Engine) SetSpeechConfig(cfg SpeechCfg) {
	e.speech = cfg
}

// SetTTSConfig configures the text-to-speech subsystem.
func (e *Engine) SetTTSConfig(cfg *TTSCfg) {
	e.tts = cfg
}

// SetTTSSaveFunc registers a callback that persists TTS mode changes.
func (e *Engine) SetTTSSaveFunc(fn func(mode string) error) {
	e.ttsSaveFunc = fn
}

// SetDisplayConfig overrides the default truncation settings.
func (e *Engine) SetDisplayConfig(cfg DisplayCfg) {
	e.display = cfg
}

// SetDefaultQuiet sets whether new sessions start in quiet mode.
func (e *Engine) SetDefaultQuiet(q bool) {
	e.defaultQuiet = q
}

// estimateTokens provides a rough token estimate for a set of history entries.
func estimateTokens(entries []HistoryEntry) int {
	return estimateTokensWithPendingAssistant(entries, "")
}

// estimateTokensWithPendingAssistant is like estimateTokens but includes an assistant
// message not yet written to history (used at EventResult before AddHistory).
func estimateTokensWithPendingAssistant(entries []HistoryEntry, pendingAssistant string) int {
	// Heuristic: ~1 token per 4 characters in mixed English/Chinese.
	count := 0
	for _, h := range entries {
		count += len([]rune(h.Content))
	}
	if pendingAssistant != "" {
		count += len([]rune(pendingAssistant))
	}
	if count == 0 {
		return 0
	}
	return (count + 3) / 4
}

// SetAutoCompressConfig configures automatic context compression.
func (e *Engine) SetAutoCompressConfig(enabled bool, maxTokens int, minGap time.Duration) {
	e.autoCompressEnabled = enabled
	e.autoCompressMaxTokens = maxTokens
	if minGap <= 0 {
		minGap = 30 * time.Minute
	}
	e.autoCompressMinGap = minGap
}

// SetShowContextIndicator controls whether assistant replies include the [ctx: ~N%] suffix.
func (e *Engine) SetShowContextIndicator(show bool) {
	e.showContextIndicator = show
}

// SetSessionCompleteNotify controls whether a "session complete" message is sent
// to IM after the agent finishes a turn with no pending messages in the queue.
func (e *Engine) SetSessionCompleteNotify(enabled bool) {
	e.sessionCompleteNotify = enabled
}

// SetShowToolProcess controls whether tool_use/tool_result messages are shown
// on IM platforms. This is independent of quiet mode which also hides thinking.
func (e *Engine) SetShowToolProcess(show bool) {
	e.showToolProcess = show
}

// SetPatrolScheduler injects the patrol scheduler for recording IM user activity.
func (e *Engine) SetPatrolScheduler(ps *PatrolScheduler) {
	e.patrolScheduler = ps
}

// SetInjectSender controls whether sender identity (platform and user ID) is
// prepended to each message before forwarding it to the agent. When enabled,
// the agent receives a preamble line like:
//
//	[cc-connect sender_id=ou_abc123 platform=feishu]
//
// This allows the agent to identify who sent the message and adjust behavior
// accordingly (e.g. personal task views, role-based access control).
func (e *Engine) SetInjectSender(v bool) {
	e.injectSender = v
}

// SetAttachmentSendEnabled controls whether side-channel image/file delivery is allowed.
func (e *Engine) SetAttachmentSendEnabled(v bool) {
	e.attachmentSendEnabled = v
}

func (e *Engine) SetLanguageSaveFunc(fn func(Language) error) {
	e.i18n.SetSaveFunc(fn)
}

func (e *Engine) SetProviderSaveFunc(fn func(providerName string) error) {
	e.providerSaveFunc = fn
}

func (e *Engine) SetProviderAddSaveFunc(fn func(ProviderConfig) error) {
	e.providerAddSaveFunc = fn
}

func (e *Engine) SetProviderRemoveSaveFunc(fn func(string) error) {
	e.providerRemoveSaveFunc = fn
}

func (e *Engine) SetProviderModelSaveFunc(fn func(providerName, model string) error) {
	e.providerModelSaveFunc = fn
}

func (e *Engine) SetModelSaveFunc(fn func(model string) error) {
	e.modelSaveFunc = fn
}

// AddPlatform appends a platform to the engine after construction.
// The platform is started and wired during the next Engine.Start call,
// or if the engine is already running, it is started immediately.
func (e *Engine) AddPlatform(p Platform) {
	e.platforms = append(e.platforms, p)
}

// SetExternalPlatform marks a platform as externally managed by a ProjectRouter.
// External platforms are skipped during Engine.Start() and Engine.Stop() since
// the ProjectRouter handles their lifecycle.
func (e *Engine) SetExternalPlatform(p Platform) {
	e.externalPlatforms[p] = true
}

// HandleIncomingMessage allows a ProjectRouter to route a message into this engine.
func (e *Engine) HandleIncomingMessage(p Platform, msg *Message) {
	e.handleMessage(p, msg)
}

func (e *Engine) SetCronScheduler(cs *CronScheduler) {
	e.cronScheduler = cs
}

func (e *Engine) SetHeartbeatScheduler(hs *HeartbeatScheduler) {
	e.heartbeatScheduler = hs
}

func (e *Engine) SetCommandSaveAddFunc(fn func(name, description, prompt, exec, workDir string) error) {
	e.commandSaveAddFunc = fn
}

func (e *Engine) SetCommandSaveDelFunc(fn func(name string) error) {
	e.commandSaveDelFunc = fn
}

func (e *Engine) SetDisplaySaveFunc(fn func(thinkingMaxLen, toolMaxLen *int) error) {
	e.displaySaveFunc = fn
}

// ConfigReloadResult describes what was updated by a config reload.
type ConfigReloadResult struct {
	DisplayUpdated   bool
	ProvidersUpdated int
	CommandsUpdated  int
}

func (e *Engine) SetConfigReloadFunc(fn func() (*ConfigReloadResult, error)) {
	e.configReloadFunc = fn
}

// GetAgent returns the engine's agent (for type assertions like ProviderSwitcher).
func (e *Engine) GetAgent() Agent {
	return e.agent
}

// AddCommand registers a custom slash command.
func (e *Engine) AddCommand(name, description, prompt, exec, workDir, source string) {
	e.commands.Add(name, description, prompt, exec, workDir, source)
}

// ClearCommands removes all commands from the given source.
func (e *Engine) ClearCommands(source string) {
	e.commands.ClearSource(source)
}

// AddAlias registers a command alias.
func (e *Engine) AddAlias(name, command string) {
	e.aliasMu.Lock()
	defer e.aliasMu.Unlock()
	e.aliases[name] = command
}

func (e *Engine) SetAliasSaveAddFunc(fn func(name, command string) error) {
	e.aliasSaveAddFunc = fn
}

func (e *Engine) SetAliasSaveDelFunc(fn func(name string) error) {
	e.aliasSaveDelFunc = fn
}

// ClearAliases removes all aliases (for config reload).
func (e *Engine) ClearAliases() {
	e.aliasMu.Lock()
	defer e.aliasMu.Unlock()
	e.aliases = make(map[string]string)
}

// SetQuickPhraseSuffix configures auto-append suffix for messages exceeding minLen.
// 设置快捷短语后缀规则：消息长度超过 minLen 时自动追加 suffix。
func (e *Engine) SetQuickPhraseSuffix(minLen int, suffix string) {
	e.quickPhraseSuffixMinLen = minLen
	e.quickPhraseSuffixText = suffix
}

// SetQuickPhraseCmdPrefix configures command prefix expansion.
// 设置命令前缀规则：以 prefix 开头的消息自动展开为 template 格式的命令执行指令。
func (e *Engine) SetQuickPhraseCmdPrefix(prefix, template string) {
	e.quickPhraseCmdPrefix = prefix
	e.quickPhraseCmdTemplate = template
}

// resolveDisabledCmds resolves a list of command names (including "*" wildcard)
// to a set of canonical command IDs.
func resolveDisabledCmds(cmds []string) map[string]bool {
	m := make(map[string]bool, len(cmds))
	for _, c := range cmds {
		c = strings.ToLower(strings.TrimPrefix(c, "/"))
		if c == "*" {
			for _, bc := range builtinCommandNames {
				m[bc.id] = true
			}
			return m
		}
		if id := matchPrefix(c, builtinCommandNames); id != "" {
			m[id] = true
		} else {
			m[c] = true
		}
	}
	return m
}

// SetDisabledCommands sets the list of command IDs that are disabled for this project.
func (e *Engine) SetDisabledCommands(cmds []string) {
	e.userRolesMu.Lock()
	defer e.userRolesMu.Unlock()
	e.disabledCmds = resolveDisabledCmds(cmds)
}

// SetUserRoles configures per-user role-based policies. Pass nil to disable.
func (e *Engine) SetUserRoles(urm *UserRoleManager) {
	e.userRolesMu.Lock()
	defer e.userRolesMu.Unlock()
	if e.userRoles != nil {
		e.userRoles.Stop()
	}
	e.userRoles = urm
}

// SetAdminFrom sets the admin allowlist for privileged commands.
// "*" means all users who pass allow_from are admins.
// Empty string means privileged commands are denied for everyone.
func (e *Engine) SetAdminFrom(adminFrom string) {
	e.userRolesMu.Lock()
	e.adminFrom = strings.TrimSpace(adminFrom)
	af := e.adminFrom
	shellDisabled := e.disabledCmds["shell"]
	e.userRolesMu.Unlock()
	if af == "" && !shellDisabled {
		slog.Warn("admin_from is not set — privileged commands (/shell, /dir, /restart, /upgrade) are blocked. "+
			"Set admin_from in config to enable them, or use disabled_commands to hide them.",
			"project", e.name)
	}
}

// isAdmin checks whether the given user ID is authorized for privileged commands.
// Unlike AllowList, empty adminFrom means deny-all (fail-closed).
func (e *Engine) isAdmin(userID string) bool {
	e.userRolesMu.RLock()
	af := e.adminFrom
	e.userRolesMu.RUnlock()
	if af == "" {
		return false
	}
	if af == "*" {
		return true
	}
	for _, id := range strings.Split(af, ",") {
		if strings.EqualFold(strings.TrimSpace(id), userID) {
			return true
		}
	}
	return false
}

// SetBannedWords replaces the banned words list.
func (e *Engine) SetBannedWords(words []string) {
	e.bannedMu.Lock()
	defer e.bannedMu.Unlock()
	lower := make([]string, len(words))
	for i, w := range words {
		lower[i] = strings.ToLower(w)
	}
	e.bannedWords = lower
}

// SetRateLimitCfg configures per-session message rate limiting.
// It stops the previous rate limiter's background goroutine before replacing it.
func (e *Engine) SetRateLimitCfg(cfg RateLimitCfg) {
	if e.rateLimiter != nil {
		e.rateLimiter.Stop()
	}
	e.rateLimiter = NewRateLimiter(cfg.MaxMessages, cfg.Window)
}

// checkRateLimit returns true if the message is allowed, false if rate-limited.
// It checks per-user role-based limits first, then falls back to the global limiter.
func (e *Engine) checkRateLimit(msg *Message) bool {
	e.userRolesMu.RLock()
	urm := e.userRoles
	e.userRolesMu.RUnlock()

	// Try role-specific rate limit first
	if urm != nil {
		// Use userID if available, else fall back to sessionKey for unidentified users.
		// NOTE: sessionKey fallback means anonymous users get separate buckets per
		// session, which is less strict than per-user limiting. Platforms should
		// provide UserID for effective rate limiting.
		rateKey := msg.UserID
		if rateKey == "" {
			rateKey = msg.SessionKey
			slog.Debug("rate limit: no UserID, falling back to sessionKey", "session_key", msg.SessionKey)
		}
		allowed, handled := urm.AllowRate(rateKey)
		if handled {
			return allowed
		}
		// Role has no rate_limit config — fall through to global, keyed by user
	}
	// Global rate limiter
	if e.rateLimiter == nil {
		return true
	}
	// When users config active: key by userID (per-user); otherwise sessionKey (legacy)
	key := msg.SessionKey
	if urm != nil && msg.UserID != "" {
		key = msg.UserID
	}
	return e.rateLimiter.Allow(key)
}

// SetStreamPreviewCfg configures the streaming preview behavior.
func (e *Engine) SetStreamPreviewCfg(cfg StreamPreviewCfg) {
	e.streamPreview = cfg
}

// SetEventIdleTimeout sets the maximum time to wait between consecutive agent events.
// 0 disables the timeout entirely.
func (e *Engine) SetEventIdleTimeout(d time.Duration) {
	e.eventIdleTimeout = d
}

func (e *Engine) SetRelayManager(rm *RelayManager) {
	e.relayManager = rm
}

func (e *Engine) RelayManager() *RelayManager {
	return e.relayManager
}

func (e *Engine) SetDirHistory(dh *DirHistory) {
	e.dirHistory = dh
}

func (e *Engine) SetBaseWorkDir(dir string) {
	e.baseWorkDir = dir
}

func (e *Engine) SetProjectStateStore(store *ProjectStateStore) {
	e.projectState = store
}

// RemoveCommand removes a custom command by name. Returns false if not found.
func (e *Engine) RemoveCommand(name string) bool {
	return e.commands.Remove(name)
}

func (e *Engine) ProjectName() string {
	return e.name
}

// ActiveSessionKeys returns the session keys of all active interactive sessions.
func (e *Engine) ActiveSessionKeys() []string {
	e.interactiveMu.Lock()
	defer e.interactiveMu.Unlock()
	var keys []string
	for key, state := range e.interactiveStates {
		if state.platform != nil {
			keys = append(keys, key)
		}
	}
	return keys
}

// SessionInfo holds status information about a session within an engine.
// Used by ProjectRouter to display enriched /project output.
type SessionInfo struct {
	HasActiveSession bool
	AgentSessionID   string
	LastActivity     time.Time
	WorkDir          string
}

// CleanupSession gracefully closes the interactive state for a session key.
// Called by ProjectRouter when a user switches to a different project.
func (e *Engine) CleanupSession(sessionKey string) {
	e.interactiveMu.Lock()
	// Find all matching keys (multi-workspace mode uses workspace:sessionKey)
	var toCleanup []string
	for key := range e.interactiveStates {
		if key == sessionKey || strings.HasSuffix(key, ":"+sessionKey) {
			toCleanup = append(toCleanup, key)
		}
	}
	e.interactiveMu.Unlock()

	// Reuse cleanupInteractiveState for each key — it handles agent session
	// close, queued message notification, and map deletion safely.
	for _, key := range toCleanup {
		e.cleanupInteractiveState(key)
	}

	if len(toCleanup) > 0 {
		slog.Info("session cleaned up for project switch",
			"project", e.name, "session", sessionKey, "cleaned", len(toCleanup))
	}
}

// GetSessionInfo returns status information about a session key within this engine.
// Used by ProjectRouter to display enriched /project output.
func (e *Engine) GetSessionInfo(sessionKey string) *SessionInfo {
	info := &SessionInfo{}

	// Get work_dir from agent (if it supports GetWorkDir)
	if wd, ok := e.agent.(interface{ GetWorkDir() string }); ok {
		info.WorkDir = wd.GetWorkDir()
	}

	// Check interactiveState for active agent session
	e.interactiveMu.Lock()
	for key, state := range e.interactiveStates {
		if key == sessionKey || strings.HasSuffix(key, ":"+sessionKey) {
			if state.agentSession != nil && state.agentSession.Alive() {
				info.HasActiveSession = true
				info.AgentSessionID = state.agentSession.CurrentSessionID()
			}
			break
		}
	}
	e.interactiveMu.Unlock()

	// Get last activity time from session store
	session := e.sessions.FindByKey(sessionKey)
	if session != nil {
		info.LastActivity = session.UpdatedAt
	}

	return info
}

// ExecuteCronJob runs a cron job by injecting a synthetic message into the engine.
// It finds the platform that owns the session key, reconstructs a reply context,
// and processes the message as if the user sent it.
func (e *Engine) ExecuteCronJob(job *CronJob) error {
	sessionKey := job.SessionKey
	platformName := ""
	if idx := strings.Index(sessionKey, ":"); idx > 0 {
		platformName = sessionKey[:idx]
	}

	var targetPlatform Platform
	for _, p := range e.platforms {
		if p.Name() == platformName {
			targetPlatform = p
			break
		}
	}
	if targetPlatform == nil {
		return fmt.Errorf("platform %q not found for session %q", platformName, sessionKey)
	}

	rc, ok := targetPlatform.(ReplyContextReconstructor)
	if !ok {
		return fmt.Errorf("platform %q does not support proactive messaging (cron)", platformName)
	}

	runSessionKey := sessionKey
	var replyCtx any
	var err error
	if !job.Mute {
		if resolver, ok := targetPlatform.(CronReplyTargetResolver); ok {
			resolvedSessionKey, resolvedReplyCtx, err := resolver.ResolveCronReplyTarget(sessionKey, cronRunTitle(job))
			if err != nil {
				if !errors.Is(err, ErrNotSupported) {
					return fmt.Errorf("resolve cron reply target: %w", err)
				}
			} else {
				if resolvedSessionKey != "" {
					runSessionKey = resolvedSessionKey
				}
				if resolvedReplyCtx != nil {
					replyCtx = resolvedReplyCtx
				}
			}
		}
	}
	if replyCtx == nil {
		replyCtx, err = rc.ReconstructReplyCtx(runSessionKey)
		if err != nil {
			return fmt.Errorf("reconstruct reply context: %w", err)
		}
	}

	// Wrap platform to discard all outgoing messages when muted
	effectivePlatform := targetPlatform
	if job.Mute {
		effectivePlatform = &mutePlatform{targetPlatform}
	}

	// Notify user that a cron job is executing (unless silent/muted)
	if !job.Mute {
		silent := false
		if e.cronScheduler != nil {
			silent = e.cronScheduler.IsSilent(job)
		}
		if !silent {
			desc := job.Description
			if desc == "" {
				if job.IsShellJob() {
					desc = truncateStr(job.Exec, 40)
				} else {
					desc = truncateStr(job.Prompt, 40)
				}
			}
			e.send(targetPlatform, replyCtx, fmt.Sprintf("⏰ %s", desc))
		}
	}

	if job.IsShellJob() {
		return e.executeCronShell(effectivePlatform, replyCtx, job)
	}

	msg := &Message{
		SessionKey: sessionKey,
		Platform:   platformName,
		UserID:     "cron",
		UserName:   "cron",
		Content:    job.Prompt,
		ReplyCtx:   replyCtx,
	}

	if job.UsesNewSessionPerRun() {
		msg.SessionKey = runSessionKey
		session := e.sessions.NewSideSession(runSessionKey, "cron-"+job.ID)
		if !session.TryLock() {
			return fmt.Errorf("session %q is busy", runSessionKey)
		}
		iKey := fmt.Sprintf("%s#cron:%s", runSessionKey, session.ID)
		e.processInteractiveMessageWith(effectivePlatform, msg, session, e.agent, e.sessions, iKey, "", runSessionKey)
		return nil
	}

	session := e.sessions.GetOrCreateActive(sessionKey)
	if !session.TryLock() {
		return fmt.Errorf("session %q is busy", sessionKey)
	}

	e.processInteractiveMessageWith(effectivePlatform, msg, session, e.agent, e.sessions, sessionKey, "", sessionKey)
	return nil
}

func cronRunTitle(job *CronJob) string {
	if job == nil {
		return "cron"
	}
	if desc := strings.TrimSpace(job.Description); desc != "" {
		return truncateStr(desc, 60)
	}
	if job.IsShellJob() {
		if cmd := strings.TrimSpace(job.Exec); cmd != "" {
			return truncateStr(cmd, 60)
		}
		return "cron"
	}
	if prompt := strings.TrimSpace(job.Prompt); prompt != "" {
		return truncateStr(prompt, 60)
	}
	return "cron"
}

// executeCronShell runs a shell command for a cron job and sends the output.
func (e *Engine) executeCronShell(p Platform, replyCtx any, job *CronJob) error {
	workDir := job.WorkDir
	if workDir == "" {
		if wd, ok := e.agent.(interface{ GetWorkDir() string }); ok {
			workDir = wd.GetWorkDir()
		}
	}
	if workDir == "" {
		workDir, _ = os.Getwd()
	}
	workDir = normalizeWorkspacePath(workDir)

	timeout := job.ExecutionTimeout()
	var ctx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(e.ctx, timeout)
	} else {
		ctx, cancel = context.WithCancel(e.ctx)
	}
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", job.Exec)
	cmd.Dir = workDir
	output, err := cmd.CombinedOutput()

	if ctx.Err() == context.DeadlineExceeded {
		e.send(p, replyCtx, fmt.Sprintf("⏰ ⚠️ timeout: `%s`", truncateStr(job.Exec, 60)))
		return fmt.Errorf("shell command timed out")
	}

	result := strings.TrimSpace(string(output))
	if err != nil {
		if result != "" {
			e.send(p, replyCtx, fmt.Sprintf("⏰ ❌ `%s`\n\n%s\n\nerror: %v", truncateStr(job.Exec, 60), truncateStr(result, 3000), err))
		} else {
			e.send(p, replyCtx, fmt.Sprintf("⏰ ❌ `%s`\nerror: %v", truncateStr(job.Exec, 60), err))
		}
		return fmt.Errorf("shell: %w", err)
	}

	if result == "" {
		result = "(no output)"
	}
	e.send(p, replyCtx, fmt.Sprintf("⏰ ✅ `%s`\n\n%s", truncateStr(job.Exec, 60), truncateStr(result, 3000)))
	return nil
}

// ExecuteHeartbeat runs a heartbeat check by injecting a synthetic message
// into the main session, similar to cron but designed for periodic awareness.
func (e *Engine) ExecuteHeartbeat(sessionKey, prompt string, silent bool) error {
	platformName := ""
	if idx := strings.Index(sessionKey, ":"); idx > 0 {
		platformName = sessionKey[:idx]
	}

	var targetPlatform Platform
	for _, p := range e.platforms {
		if p.Name() == platformName {
			targetPlatform = p
			break
		}
	}
	if targetPlatform == nil {
		return fmt.Errorf("platform %q not found for session %q", platformName, sessionKey)
	}

	rc, ok := targetPlatform.(ReplyContextReconstructor)
	if !ok {
		return fmt.Errorf("platform %q does not support proactive messaging (heartbeat)", platformName)
	}

	replyCtx, err := rc.ReconstructReplyCtx(sessionKey)
	if err != nil {
		return fmt.Errorf("reconstruct reply context: %w", err)
	}

	if !silent {
		e.send(targetPlatform, replyCtx, "💓 heartbeat")
	}

	msg := &Message{
		SessionKey: sessionKey,
		Platform:   platformName,
		UserID:     "heartbeat",
		UserName:   "heartbeat",
		Content:    prompt,
		ReplyCtx:   replyCtx,
	}

	session := e.sessions.GetOrCreateActive(sessionKey)
	if !session.TryLock() {
		return fmt.Errorf("session %q is busy", sessionKey)
	}

	e.processInteractiveMessage(targetPlatform, msg, session)
	return nil
}

func (e *Engine) Start() error {
	var startErrs []error
	readyCount := 0
	pendingCount := 0
	attemptedCount := 0
	externalCount := 0
	for _, p := range e.platforms {
		if e.externalPlatforms[p] {
			externalCount++
			continue // managed by ProjectRouter
		}
		attemptedCount++
		_, isAsync := p.(AsyncRecoverablePlatform)
		if async, ok := p.(AsyncRecoverablePlatform); ok {
			async.SetLifecycleHandler(e)
		}
		if err := p.Start(e.handleMessage); err != nil {
			slog.Warn("platform start failed", "project", e.name, "platform", p.Name(), "error", err)
			startErrs = append(startErrs, fmt.Errorf("[%s] start platform %s: %w", e.name, p.Name(), err))
			continue
		}
		if isAsync {
			pendingCount++
			slog.Info("platform recovery loop started", "project", e.name, "platform", p.Name())
			continue
		}
		e.onPlatformReady(p)
		readyCount++
	}

	// Log summary
	if len(startErrs) > 0 || pendingCount > 0 {
		slog.Warn("engine started with partial readiness",
			"project", e.name,
			"agent", e.agent.Name(),
			"ready", readyCount,
			"pending", pendingCount,
			"external", externalCount,
			"failed", len(startErrs))
	} else {
		slog.Info("engine started", "project", e.name, "agent", e.agent.Name(),
			"platforms", attemptedCount, "external", externalCount)
	}

	// Only return error if ALL locally-managed platforms failed
	if len(startErrs) == attemptedCount && attemptedCount > 0 {
		return startErrs[0] // Return first error
	}
	return nil
}

func (e *Engine) Stop() error {
	e.platformLifecycleMu.Lock()
	e.stopping = true
	e.platformLifecycleMu.Unlock()

	// Cancel first so late lifecycle callbacks observe shutdown immediately.
	e.cancel()

	// Stop platforms after cancellation so they can unwind against the closed context.
	var errs []error
	for _, p := range e.platforms {
		if e.externalPlatforms[p] {
			continue // managed by ProjectRouter
		}
		if err := p.Stop(); err != nil {
			errs = append(errs, fmt.Errorf("stop platform %s: %w", p.Name(), err))
		}
	}

	e.interactiveMu.Lock()
	states := make(map[string]*interactiveState, len(e.interactiveStates))
	for k, v := range e.interactiveStates {
		states[k] = v
		delete(e.interactiveStates, k)
	}
	e.interactiveMu.Unlock()

	for key, state := range states {
		if state.agentSession != nil {
			slog.Debug("engine.Stop: closing agent session", "session", key)
			state.agentSession.Close()
		}
	}

	if e.rateLimiter != nil {
		e.rateLimiter.Stop()
	}
	e.userRolesMu.Lock()
	if e.userRoles != nil {
		e.userRoles.Stop()
	}
	e.userRolesMu.Unlock()

	if err := e.agent.Stop(); err != nil {
		errs = append(errs, fmt.Errorf("stop agent %s: %w", e.agent.Name(), err))
	}

	if len(errs) > 0 {
		return fmt.Errorf("engine stop errors: %v", errs)
	}
	return nil
}

// OnPlatformReady marks an async platform as ready and initializes platform-level
// capabilities once per ready cycle.
func (e *Engine) OnPlatformReady(p Platform) {
	e.onPlatformReady(p)
}

// OnPlatformUnavailable marks an async platform as unavailable.
func (e *Engine) OnPlatformUnavailable(p Platform, err error) {
	if !e.markPlatformUnavailable(p) {
		return
	}
	slog.Warn("platform unavailable", "project", e.name, "platform", p.Name(), "error", err)
}

// ReceiveMessage delivers a message from a platform to the engine.
// This is a public wrapper for use in integration tests and external callers.
func (e *Engine) ReceiveMessage(p Platform, msg *Message) {
	e.handleMessage(p, msg)
}

func (e *Engine) onPlatformReady(p Platform) {
	if !e.markPlatformReady(p) {
		return
	}
	slog.Info("platform ready", "project", e.name, "platform", p.Name())
	e.initPlatformCapabilities(p)
}

func (e *Engine) markPlatformReady(p Platform) bool {
	e.platformLifecycleMu.Lock()
	defer e.platformLifecycleMu.Unlock()

	if e.stopping || e.ctx.Err() != nil {
		return false
	}
	if e.platformReady[p] {
		return false
	}
	e.platformReady[p] = true
	return true
}

func (e *Engine) markPlatformUnavailable(p Platform) bool {
	e.platformLifecycleMu.Lock()
	defer e.platformLifecycleMu.Unlock()

	if e.stopping || e.ctx.Err() != nil {
		return false
	}
	if !e.platformReady[p] {
		return false
	}
	e.platformReady[p] = false
	return true
}

func (e *Engine) initPlatformCapabilities(p Platform) {
	if registrar, ok := p.(CommandRegistrar); ok {
		commands := e.GetAllCommands()
		if err := registrar.RegisterCommands(commands); err != nil {
			slog.Error("platform command registration failed", "project", e.name, "platform", p.Name(), "error", err)
		} else {
			slog.Debug("platform commands registered", "project", e.name, "platform", p.Name(), "count", len(commands))
		}
	}

	if nav, ok := p.(CardNavigable); ok {
		nav.SetCardNavigationHandler(e.handleCardNav)
	}
}

// matchBannedWord returns the first banned word found in content, or "".
func (e *Engine) matchBannedWord(content string) string {
	e.bannedMu.RLock()
	defer e.bannedMu.RUnlock()
	if len(e.bannedWords) == 0 {
		return ""
	}
	lower := strings.ToLower(content)
	for _, w := range e.bannedWords {
		if strings.Contains(lower, w) {
			return w
		}
	}
	return ""
}

// resolveAlias checks if the content (or its first word) matches an alias and replaces it.
func (e *Engine) resolveAlias(content string) string {
	e.aliasMu.RLock()
	defer e.aliasMu.RUnlock()

	if len(e.aliases) == 0 {
		return content
	}

	// Exact match on full content
	if cmd, ok := e.aliases[content]; ok {
		return cmd
	}

	// Match first word, append remaining args
	parts := strings.SplitN(content, " ", 2)
	if cmd, ok := e.aliases[parts[0]]; ok {
		if len(parts) > 1 {
			return cmd + " " + parts[1]
		}
		return cmd
	}
	return content
}

func (e *Engine) handleMessage(p Platform, msg *Message) {
	slog.Info("message received",
		"platform", msg.Platform, "msg_id", msg.MessageID,
		"session", msg.SessionKey, "user", msg.UserName,
		"content_len", len(msg.Content),
		"has_images", len(msg.Images) > 0, "has_audio", msg.Audio != nil, "has_files", len(msg.Files) > 0,
	)

	// Voice message: transcribe to text first
	if msg.Audio != nil {
		// If STT is configured, use it for transcription (more accurate)
		if e.speech.Enabled && e.speech.STT != nil {
			e.handleVoiceMessage(p, msg)
			return
		}
		// Fallback: use platform-provided recognition text if available
		if msg.Content == "" {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgVoiceNotEnabled))
			return
		}
		// Use platform recognition with a hint, then continue processing
		slog.Info("using platform-provided voice recognition",
			"platform", msg.Platform, "content_len", len(msg.Content))
		if msg.FromVoice {
			// Use platform name as parameter for the message
			// Capitalize first letter for better presentation
			if platformName := msg.Platform; len(platformName) > 0 {
				// Safe capitalization that handles multi-word names
				r := []rune(platformName)
				if len(r) > 0 {
					r[0] = []rune(strings.ToUpper(string(r[0])))[0]
				}
				platformName = string(r)
				e.send(p, msg.ReplyCtx, e.i18n.Tf(MsgVoiceUsingPlatformRecognition, platformName))
			}
		}
		// Continue processing with the platform-provided text content
	}

	content := strings.TrimSpace(msg.Content)
	if content == "" && len(msg.Images) == 0 && len(msg.Files) == 0 {
		return
	}

	// Resolve aliases: check if the first word (or whole content) matches an alias
	// 别名解析（同时包含快捷短语，因为快捷短语复用 alias 机制）
	content = e.resolveAlias(content)

	// Command prefix expansion (e.g. "!git status" → "执行命令: git status")
	// 命令前缀展开：以指定前缀开头的消息自动转换为命令执行指令
	if e.quickPhraseCmdPrefix != "" && e.quickPhraseCmdTemplate != "" &&
		strings.HasPrefix(content, e.quickPhraseCmdPrefix) {
		cmd := strings.TrimSpace(content[len(e.quickPhraseCmdPrefix):])
		if cmd != "" {
			content = fmt.Sprintf(e.quickPhraseCmdTemplate, cmd)
		}
	}

	// Auto-append suffix for long messages (quick phrase suffix rule)
	// 快捷短语后缀规则：消息长度超过阈值且非斜杠命令时，自动追加后缀
	if e.quickPhraseSuffixMinLen > 0 && e.quickPhraseSuffixText != "" &&
		!strings.HasPrefix(content, "/") &&
		len([]rune(content)) > e.quickPhraseSuffixMinLen {
		content = content + "\n" + e.quickPhraseSuffixText
	}

	msg.Content = content

	// Rate limit check (per-user role-based, then global fallback)
	if !e.checkRateLimit(msg) {
		slog.Info("message rate limited",
			"session", msg.SessionKey, "user_id", msg.UserID, "user", msg.UserName)
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgRateLimited))
		return
	}

	// Banned words check (skip for slash commands)
	if !strings.HasPrefix(content, "/") {
		if word := e.matchBannedWord(content); word != "" {
			slog.Info("message blocked by banned word", "word", word, "user", msg.UserName)
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgBannedWordBlocked))
			return
		}
	}

	// Multi-workspace resolution
	var wsAgent Agent
	var wsSessions *SessionManager
	var resolvedWorkspace string
	if e.multiWorkspace {
		channelID := extractChannelID(msg.SessionKey)
		channelKey := extractWorkspaceChannelKey(msg.SessionKey)
		workspace, channelName, err := e.resolveWorkspace(p, channelID)
		if err != nil {
			slog.Error("workspace resolution failed", "err", err)
			e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsResolutionError, err))
			return
		}
		if workspace == "" {
			// No workspace — handle init flow (unless it's a /workspace command)
			if !strings.HasPrefix(content, "/workspace") && !strings.HasPrefix(content, "/ws ") {
				if e.handleWorkspaceInitFlow(p, msg, channelName) {
					return
				}
			} else {
				// Workspace command bypassed the init flow; clean up any stale flow
				// so it doesn't interfere if the channel becomes unbound again later.
				e.initFlowsMu.Lock()
				delete(e.initFlows, channelKey)
				e.initFlowsMu.Unlock()
			}
			// If init flow didn't consume, only workspace commands work
			if !strings.HasPrefix(content, "/") {
				return
			}
		} else {
			resolvedWorkspace = workspace

			// Touch for idle tracking
			if ws := e.workspacePool.Get(workspace); ws != nil {
				ws.Touch()
			}

			// Get or create the workspace's agent and session manager
			wsAgent, wsSessions, err = e.getOrCreateWorkspaceAgent(workspace)
			if err != nil {
				slog.Error("failed to create workspace agent", "workspace", workspace, "err", err)
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("Failed to initialize workspace: %v", err))
				return
			}
		}
	}

	if len(msg.Images) == 0 && strings.HasPrefix(content, "/") {
		if e.handleCommand(p, msg, content) {
			return
		}
		// Unrecognized slash command — fall through to agent as normal message
	}

	// Permission responses bypass the session lock
	if e.handlePendingPermission(p, msg, content) {
		return
	}

	// Select session manager and agent based on workspace mode
	sessions := e.sessions
	agent := e.agent
	interactiveKey := msg.SessionKey
	if e.multiWorkspace && wsSessions != nil {
		sessions = wsSessions
		agent = wsAgent
		interactiveKey = resolvedWorkspace + ":" + msg.SessionKey
	}

	session := sessions.GetOrCreateActive(msg.SessionKey)
	sessions.UpdateUserMeta(msg.SessionKey, msg.UserName, msg.ChatName)
	// 记录 IM 用户活动（供 PatrolScheduler 查询最近活跃用户）
	if e.patrolScheduler != nil {
		e.patrolScheduler.RecordUserActivity(e.name, msg.SessionKey, msg.Platform, msg.UserID, msg.UserName)
	}
	if !session.TryLock() {
		// Check for /btw — inject into the running session mid-turn
		trimmed := strings.TrimSpace(content)
		if isBtwCommand(trimmed) {
			btw := strings.TrimSpace(trimmed[len(matchBtwPrefix(trimmed)):])
			if btw != "" {
				e.interactiveMu.Lock()
				state, ok := e.interactiveStates[interactiveKey]
				e.interactiveMu.Unlock()
				if ok && state.agentSession != nil && state.agentSession.Alive() {
					if err := state.agentSession.Send(btw, nil, nil); err != nil {
						slog.Error("btw: send failed", "error", err)
						e.reply(p, msg.ReplyCtx, e.i18n.T(MsgBtwSendFailed))
					} else {
						e.reply(p, msg.ReplyCtx, e.i18n.T(MsgBtwSent))
					}
					return
				}
			}
		}
		// Session is busy — try to queue the message for the running turn
		// so the agent processes it immediately after the current turn ends.
		if e.queueMessageForBusySession(p, msg, interactiveKey) {
			// Race guard: the drain loop in processInteractiveMessageWith may
			// have just finished (session unlocked) between our TryLock failure
			// and the queue append. Re-try TryLock — if it succeeds, no one is
			// draining the queue so we must start a processor ourselves.
			if session.TryLock() {
				go e.drainOrphanedQueue(session, sessions, interactiveKey, agent, resolvedWorkspace)
			}
			return
		}
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgPreviousProcessing))
		return
	}

	slog.Info("processing message",
		"platform", msg.Platform,
		"user", msg.UserName,
		"session", session.ID,
	)

	go e.processInteractiveMessageWith(p, msg, session, agent, sessions, interactiveKey, resolvedWorkspace, msg.SessionKey)
}
