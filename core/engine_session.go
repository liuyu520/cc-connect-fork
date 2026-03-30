package core

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// queuedMessage holds a message that arrived while the session was busy.
// The message is NOT sent to agent stdin at queue time; the event loop
// sends it after the current turn completes to avoid mid-turn interference.
type queuedMessage struct {
	platform      Platform
	replyCtx      any
	content       string
	images        []ImageAttachment
	files         []FileAttachment
	fromVoice     bool
	userID        string
	msgPlatform   string // platform name for sender injection
	msgSessionKey string // session key for extracting chat ID
}

// interactiveState tracks a running interactive agent session and its permission state.
type interactiveState struct {
	agentSession           AgentSession
	platform               Platform
	replyCtx               any
	workspaceDir           string
	mu                     sync.Mutex
	pending                *pendingPermission
	pendingMessages        []queuedMessage // messages queued while session was busy
	approveAll             bool            // when true, auto-approve all permission requests for this session
	quiet                  bool            // when true, suppress thinking and tool progress for this session
	fromVoice              bool            // true if current turn originated from voice transcription
	sideText               string
	deleteMode             *deleteModeState
	lastAutoCompressAt     time.Time
	lastAutoCompressTokens int
}

type deleteModeState struct {
	page        int
	selectedIDs map[string]struct{}
	phase       string
	hint        string
	result      string
}

// pendingPermission represents a permission request waiting for user response.
type pendingPermission struct {
	RequestID       string
	ToolName        string
	ToolInput       map[string]any
	InputPreview    string
	Questions       []UserQuestion // non-nil for AskUserQuestion
	Answers         map[int]string // collected answers keyed by question index
	CurrentQuestion int            // index of the question currently being asked
	Resolved        chan struct{}  // closed when user responds
	resolveOnce     sync.Once
}

// resolve safely closes the Resolved channel exactly once.
func (pp *pendingPermission) resolve() {
	pp.resolveOnce.Do(func() { close(pp.Resolved) })
}

// queueMessageForBusySession queues a message for later delivery when the
// session is busy. The message is NOT sent to agent stdin at queue time;
// the event loop sends it after the current turn's EventResult is received.
// Returns true if the message was successfully queued, false otherwise.
func (e *Engine) queueMessageForBusySession(p Platform, msg *Message, interactiveKey string) bool {
	e.interactiveMu.Lock()
	state, hasState := e.interactiveStates[interactiveKey]
	e.interactiveMu.Unlock()

	if !hasState || state == nil || state.agentSession == nil || !state.agentSession.Alive() {
		return false
	}

	// Only queue metadata — do NOT send to agent stdin yet.
	// The agent CLI may treat a mid-turn stdin message as part of the
	// current turn, causing the event loop to hang waiting for a second
	// EventResult that never arrives. Instead, the event loop sends the
	// message after the current turn's EventResult is received.
	state.mu.Lock()
	if len(state.pendingMessages) >= maxQueuedMessages {
		state.mu.Unlock()
		return false // fall back to "previous processing" reply
	}
	state.pendingMessages = append(state.pendingMessages, queuedMessage{
		platform:      p,
		replyCtx:      msg.ReplyCtx,
		content:       msg.Content,
		images:        msg.Images,
		files:         msg.Files,
		fromVoice:     msg.FromVoice,
		userID:        msg.UserID,
		msgPlatform:   msg.Platform,
		msgSessionKey: msg.SessionKey,
	})
	queueDepth := len(state.pendingMessages)
	state.mu.Unlock()

	slog.Info("message queued for busy session",
		"session", msg.SessionKey,
		"user", msg.UserName,
		"queue_depth", queueDepth,
	)
	e.reply(p, msg.ReplyCtx, e.i18n.T(MsgMessageQueued))
	return true
}

// drainOrphanedQueue is called when a message was queued but the drain loop
// has already exited. It processes all pending messages in the state, similar
// to the drain loop in processInteractiveMessageWith but as a standalone
// goroutine.
func (e *Engine) drainOrphanedQueue(session *Session, sessions *SessionManager, interactiveKey string, agent Agent, workspaceDir string) {
	unlocked := false
	defer func() {
		if !unlocked {
			session.Unlock()
		}
	}()

	e.interactiveMu.Lock()
	state, hasState := e.interactiveStates[interactiveKey]
	e.interactiveMu.Unlock()

	if !hasState || state == nil || state.agentSession == nil || !state.agentSession.Alive() {
		if hasState && state != nil {
			e.notifyDroppedQueuedMessages(state, fmt.Errorf("agent session ended"))
		}
		return
	}

	unlocked = e.drainPendingMessages(state, session, sessions, interactiveKey)
}

// ──────────────────────────────────────────────────────────────
// Voice message handling
// ──────────────────────────────────────────────────────────────

func (e *Engine) handleVoiceMessage(p Platform, msg *Message) {
	if !e.speech.Enabled || e.speech.STT == nil {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgVoiceNotEnabled))
		return
	}

	audio := msg.Audio
	if NeedsConversion(audio.Format) && !HasFFmpeg() {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgVoiceNoFFmpeg))
		return
	}

	slog.Info("transcribing voice message",
		"platform", msg.Platform, "user", msg.UserName,
		"format", audio.Format, "size", len(audio.Data),
	)
	e.send(p, msg.ReplyCtx, e.i18n.T(MsgVoiceTranscribing))

	text, err := TranscribeAudio(e.ctx, e.speech.STT, audio, e.speech.Language)
	if err != nil {
		slog.Error("speech transcription failed", "error", err)
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgVoiceTranscribeFailed), err))
		return
	}

	text = strings.TrimSpace(text)
	if text == "" {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgVoiceEmpty))
		return
	}

	slog.Info("voice transcribed", "text_len", len(text))
	e.send(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgVoiceTranscribed), text))

	// Replace audio with transcribed text and re-dispatch
	msg.Audio = nil
	msg.Content = text
	msg.FromVoice = true
	e.handleMessage(p, msg)
}

// ──────────────────────────────────────────────────────────────
// Permission handling
// ──────────────────────────────────────────────────────────────

func (e *Engine) handlePendingPermission(p Platform, msg *Message, content string) bool {
	iKey := e.interactiveKeyForSessionKey(msg.SessionKey)
	e.interactiveMu.Lock()
	state, ok := e.interactiveStates[iKey]
	e.interactiveMu.Unlock()
	if !ok || state == nil {
		return false
	}

	state.mu.Lock()
	pending := state.pending
	state.mu.Unlock()
	if pending == nil {
		return false
	}

	// AskUserQuestion: interpret user response as an answer, not a permission decision
	if len(pending.Questions) > 0 {
		curIdx := pending.CurrentQuestion
		q := pending.Questions[curIdx]
		answer := e.resolveAskQuestionAnswer(q, content)

		if pending.Answers == nil {
			pending.Answers = make(map[int]string)
		}
		pending.Answers[curIdx] = answer

		// More questions remaining — advance to next and send new card
		if curIdx+1 < len(pending.Questions) {
			pending.CurrentQuestion = curIdx + 1
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("✅ %s: **%s**", q.Question, answer))
			e.sendAskQuestionPrompt(p, msg.ReplyCtx, pending.Questions, curIdx+1)
			return true
		}

		// All questions answered — build response and resolve
		updatedInput := buildAskQuestionResponse(pending.ToolInput, pending.Questions, pending.Answers)

		if err := state.agentSession.RespondPermission(pending.RequestID, PermissionResult{
			Behavior:     "allow",
			UpdatedInput: updatedInput,
		}); err != nil {
			slog.Error("failed to send AskUserQuestion response", "error", err)
			e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgError), err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("✅ %s: **%s**", q.Question, answer))
		}

		state.mu.Lock()
		state.pending = nil
		state.mu.Unlock()
		pending.resolve()
		return true
	}

	lower := strings.ToLower(strings.TrimSpace(content))

	if isApproveAllResponse(lower) {
		state.mu.Lock()
		state.approveAll = true
		state.mu.Unlock()

		if err := state.agentSession.RespondPermission(pending.RequestID, PermissionResult{
			Behavior:     "allow",
			UpdatedInput: pending.ToolInput,
		}); err != nil {
			slog.Error("failed to send permission response", "error", err)
			e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgError), err))
		} else {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgPermissionApproveAll))
		}
	} else if isAllowResponse(lower) {
		if err := state.agentSession.RespondPermission(pending.RequestID, PermissionResult{
			Behavior:     "allow",
			UpdatedInput: pending.ToolInput,
		}); err != nil {
			slog.Error("failed to send permission response", "error", err)
			e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgError), err))
		} else {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgPermissionAllowed))
		}
	} else if isDenyResponse(lower) {
		if err := state.agentSession.RespondPermission(pending.RequestID, PermissionResult{
			Behavior: "deny",
			Message:  "User denied this tool use.",
		}); err != nil {
			slog.Error("failed to send deny response", "error", err)
		}
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgPermissionDenied))
	} else {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgPermissionHint))
		return true
	}

	state.mu.Lock()
	state.pending = nil
	state.mu.Unlock()
	pending.resolve()

	return true
}

// resolveAskQuestionAnswer converts user input into answer text.
// It handles button callbacks ("askq:qIdx:optIdx"), numeric selections ("1", "1,3"), and free text.
func (e *Engine) resolveAskQuestionAnswer(q UserQuestion, input string) string {
	input = strings.TrimSpace(input)

	// Handle card button callback: "askq:qIdx:optIdx"
	if strings.HasPrefix(input, "askq:") {
		parts := strings.SplitN(input, ":", 3)
		if len(parts) == 3 {
			if idx, err := strconv.Atoi(parts[2]); err == nil && idx >= 1 && idx <= len(q.Options) {
				return q.Options[idx-1].Label
			}
		}
		// Legacy format "askq:N"
		if len(parts) == 2 {
			if idx, err := strconv.Atoi(parts[1]); err == nil && idx >= 1 && idx <= len(q.Options) {
				return q.Options[idx-1].Label
			}
		}
	}

	// Try numeric index(es)
	if q.MultiSelect {
		parts := strings.FieldsFunc(input, func(r rune) bool { return r == ',' || r == '，' || r == ' ' })
		var labels []string
		allNumeric := true
		for _, p := range parts {
			p = strings.TrimSpace(p)
			idx, err := strconv.Atoi(p)
			if err != nil || idx < 1 || idx > len(q.Options) {
				allNumeric = false
				break
			}
			labels = append(labels, q.Options[idx-1].Label)
		}
		if allNumeric && len(labels) > 0 {
			return strings.Join(labels, ", ")
		}
	} else {
		if idx, err := strconv.Atoi(input); err == nil && idx >= 1 && idx <= len(q.Options) {
			return q.Options[idx-1].Label
		}
	}

	return input
}

// buildAskQuestionResponse constructs the updatedInput for AskUserQuestion control_response.
func buildAskQuestionResponse(originalInput map[string]any, questions []UserQuestion, collected map[int]string) map[string]any {
	result := make(map[string]any)
	for k, v := range originalInput {
		result[k] = v
	}
	answers := make(map[string]any)
	for idx, ans := range collected {
		answers[strconv.Itoa(idx)] = ans
	}
	result["answers"] = answers
	return result
}

func isApproveAllResponse(s string) bool {
	for _, w := range []string{
		"allow all", "allowall", "approve all", "yes all",
		"允许所有", "允许全部", "全部允许", "所有允许", "都允许", "全部同意",
	} {
		if s == w {
			return true
		}
	}
	return false
}

func isAllowResponse(s string) bool {
	for _, w := range []string{"allow", "yes", "y", "ok", "允许", "同意", "可以", "好", "好的", "是", "确认", "approve"} {
		if s == w {
			return true
		}
	}
	return false
}

func isDenyResponse(s string) bool {
	for _, w := range []string{"deny", "no", "n", "reject", "拒绝", "不允许", "不行", "不", "否", "取消", "cancel"} {
		if s == w {
			return true
		}
	}
	return false
}

// ──────────────────────────────────────────────────────────────
// Interactive agent processing
// ──────────────────────────────────────────────────────────────

func (e *Engine) processInteractiveMessage(p Platform, msg *Message, session *Session) {
	e.processInteractiveMessageWith(p, msg, session, e.agent, e.sessions, msg.SessionKey, "", "")
}

// processInteractiveMessageWith is the core interactive processing loop.
// It accepts an explicit agent, interactiveKey (for the interactiveStates map),
// and workspaceDir so that multi-workspace mode can route to per-workspace agents.
// ccSessionKey, when non-empty, is used for CC_SESSION_KEY in the agent env; otherwise interactiveKey is used.
func (e *Engine) processInteractiveMessageWith(p Platform, msg *Message, session *Session, agent Agent, sessions *SessionManager, interactiveKey string, workspaceDir string, ccSessionKey string) {
	// session.Unlock() is NOT deferred here — it is called explicitly in
	// the drain loop below while holding state.mu to close the race window
	// between "queue is empty" and "session unlocked". A deferred fallback
	// ensures the lock is released on early-return paths.
	unlocked := false
	defer func() {
		if !unlocked {
			session.Unlock()
		}
	}()

	if e.ctx.Err() != nil {
		return
	}

	turnStart := time.Now()

	e.i18n.DetectAndSet(msg.Content)
	session.AddHistory("user", msg.Content)

	// 异步持久化用户消息到 MySQL
	if e.chatStore != nil {
		e.chatStore.EnsureSession(e.ctx, ChatSessionInfo{
			SessionID:  session.ID,
			SessionKey: msg.SessionKey,
			Project:    e.name,
			AgentType:  session.AgentType,
			AgentSessionID: session.GetAgentSessionID(),
			Name:       session.Name,
		})
		e.chatStore.SaveMessage(e.ctx, ChatMessage{
			SessionID: session.ID,
			Role:      "user",
			Content:   msg.Content,
			Platform:  msg.Platform,
			UserID:    msg.UserID,
			UserName:  msg.UserName,
			MessageID: msg.MessageID,
		})
	}

	// Use the agent override when available (multi-workspace mode)
	var agentOverride Agent
	if agent != e.agent {
		agentOverride = agent
	}
	state := e.getOrCreateInteractiveStateWith(interactiveKey, p, msg.ReplyCtx, session, sessions, agentOverride, ccSessionKey)

	// Set workspaceDir on the state for idle reaper identification
	if workspaceDir != "" {
		state.mu.Lock()
		state.workspaceDir = workspaceDir
		state.mu.Unlock()
	}

	// Update reply context for this turn
	state.mu.Lock()
	state.platform = p
	state.replyCtx = msg.ReplyCtx
	state.mu.Unlock()

	if state.agentSession == nil {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgFailedToStartAgentSession))
		return
	}

	// Start typing indicator if platform supports it.
	// Ownership is transferred to processInteractiveEvents which manages
	// stopping/restarting it across queued message turns.
	var stopTyping func()
	if ti, ok := p.(TypingIndicator); ok {
		stopTyping = ti.StartTyping(e.ctx, msg.ReplyCtx)
	}
	defer func() {
		// Stop typing if ownership was NOT transferred to processInteractiveEvents
		// (i.e. an early return before that call).
		if stopTyping != nil {
			stopTyping()
		}
	}()

	// Drain any stale events left in the channel from a previous turn.
	// This prevents the next processInteractiveEvents from reading an old
	// EventResult that was pushed after the previous turn already returned.
	drainEvents(state.agentSession.Events())

	promptContent := e.buildSenderPrompt(msg.Content, msg.UserID, msg.Platform, msg.SessionKey)

	sendStart := time.Now()
	state.mu.Lock()
	state.fromVoice = msg.FromVoice
	state.sideText = ""
	state.mu.Unlock()

	// Run Send concurrently with processInteractiveEvents. Some agents block inside
	// Send until the prompt turn finishes (e.g. ACP session/prompt); they may emit
	// EventPermissionRequest while blocked — the event loop must run in parallel.
	sendDone := make(chan error, 1)
	go func() {
		sendDone <- state.agentSession.Send(promptContent, msg.Images, msg.Files)
	}()

	e.processInteractiveEvents(state, session, sessions, interactiveKey, msg.MessageID, turnStart, stopTyping, sendDone, msg.ReplyCtx)
	if elapsed := time.Since(sendStart); elapsed >= slowAgentSend {
		slog.Warn("slow agent send", "elapsed", elapsed, "session", msg.SessionKey, "content_len", len(msg.Content))
	}
	stopTyping = nil // ownership transferred; prevent defer from double-stopping

	// Guard against a narrow race: a message may have been queued between
	// processInteractiveEvents observing an empty queue and returning here
	// (session is still locked, so handleMessage's TryLock fails and routes
	// the message to queueMessageForBusySession). Drain any such orphans.
	if e.drainPendingMessages(state, session, sessions, interactiveKey) {
		unlocked = true
	}
}

// getOrCreateWorkspaceAgent returns (or creates) a per-workspace agent and session manager.
// workspace must be a normalized path (from resolveWorkspace or normalizeWorkspacePath).
func (e *Engine) getOrCreateWorkspaceAgent(workspace string) (Agent, *SessionManager, error) {
	ws := e.workspacePool.GetOrCreate(workspace)
	ws.mu.Lock()
	defer ws.mu.Unlock()

	if ws.agent != nil {
		return ws.agent, ws.sessions, nil
	}

	// Create a new agent instance with this workspace's work_dir
	opts := make(map[string]any)
	opts["work_dir"] = workspace

	// Copy model from original agent if possible
	if ma, ok := e.agent.(interface{ GetModel() string }); ok {
		if m := ma.GetModel(); m != "" {
			opts["model"] = m
		}
	}
	// Copy permission mode
	if ma, ok := e.agent.(interface{ GetMode() string }); ok {
		if m := ma.GetMode(); m != "" {
			opts["mode"] = m
		}
	}

	agent, err := CreateAgent(e.agent.Name(), opts)
	if err != nil {
		return nil, nil, fmt.Errorf("create workspace agent for %s: %w", workspace, err)
	}

	// Wire providers if original agent has them
	if ps, ok := e.agent.(ProviderSwitcher); ok {
		if ps2, ok2 := agent.(ProviderSwitcher); ok2 {
			ps2.SetProviders(ps.ListProviders())
		}
	}

	// Create per-workspace session manager
	h := sha256.Sum256([]byte(workspace))
	sessionFile := filepath.Join(filepath.Dir(e.sessions.StorePath()),
		fmt.Sprintf("%s_ws_%s.json", e.name, hex.EncodeToString(h[:4])))
	sessions := NewSessionManager(sessionFile)

	ws.agent = agent
	ws.sessions = sessions
	return agent, sessions, nil
}

// getOrCreateInteractiveStateWith accepts an optional agent override for multi-workspace mode.
// When agentOverride is non-nil it is used instead of e.agent to start the session.
// ccSessionKey, when non-empty, is used for CC_SESSION_KEY env injection; otherwise sessionKey is used.
func (e *Engine) getOrCreateInteractiveStateWith(sessionKey string, p Platform, replyCtx any, session *Session, sessions *SessionManager, agentOverride Agent, ccSessionKey string) *interactiveState {
	e.interactiveMu.Lock()
	defer e.interactiveMu.Unlock()

	state, ok := e.interactiveStates[sessionKey]
	if ok && state.agentSession != nil && state.agentSession.Alive() {
		// Verify the running agent session matches the current active session.
		// After /new or /switch the active session changes, but the old agent
		// process may still be alive. Reusing it would send messages to the
		// wrong conversation context.
		wantID := session.GetAgentSessionID()
		currentID := state.agentSession.CurrentSessionID()
		// Reuse only when the live process matches what the Session expects:
		// - IDs match (same Claude session), or
		// - the process has not reported an ID yet (startup; empty want is OK).
		// If wantID is empty (/new, cleared session) but the process already has
		// a concrete ID, reusing would keep --resume context — recycle (#238).
		needRecycle := currentID != "" && (wantID == "" || wantID != currentID)
		if !needRecycle {
			return state
		}
		// Tear down the stale agent so we start one that matches the Session below.
		slog.Info("interactive session mismatch, recycling",
			"session_key", sessionKey,
			"want_agent_session", wantID,
			"have_agent_session", currentID,
		)
		go state.agentSession.Close()
		delete(e.interactiveStates, sessionKey)
		ok = false // prevent reading stale settings below
	}

	// Preserve quiet setting from existing state (e.g. set via /quiet before session started)
	quietMode := e.defaultQuiet
	if ok && state != nil {
		state.mu.Lock()
		quietMode = state.quiet
		state.mu.Unlock()
	}

	// Select the agent to use for this session
	agent := e.agent
	if agentOverride != nil {
		agent = agentOverride
	}

	ccKey := sessionKey
	if ccSessionKey != "" {
		ccKey = ccSessionKey
	}

	// Inject per-session env vars so the agent subprocess can call `cc-connect cron add` etc.
	if inj, ok := agent.(SessionEnvInjector); ok {
		envVars := []string{
			"CC_PROJECT=" + e.name,
			"CC_SESSION_KEY=" + ccKey,
		}
		if exePath, err := os.Executable(); err == nil {
			binDir := filepath.Dir(exePath)
			if curPath := os.Getenv("PATH"); curPath != "" {
				envVars = append(envVars, "PATH="+binDir+string(filepath.ListSeparator)+curPath)
			} else {
				envVars = append(envVars, "PATH="+binDir)
			}
		}
		inj.SetSessionEnv(envVars)
	}

	// Inject platform-specific formatting instructions into the agent's system prompt.
	// Clear the prompt first so instructions from a previous platform don't leak
	// into sessions for platforms that don't provide their own instructions.
	if ppi, ok := agent.(PlatformPromptInjector); ok {
		prompt := ""
		if fip, ok := p.(FormattingInstructionProvider); ok {
			prompt = fip.FormattingInstructions()
		}
		ppi.SetPlatformPrompt(prompt)
	}

	// Check if context is already canceled (e.g. during shutdown/restart)
	if e.ctx.Err() != nil {
		slog.Debug("skipping session start: context canceled", "session_key", sessionKey)
		state = &interactiveState{platform: p, replyCtx: replyCtx, quiet: quietMode}
		e.interactiveStates[sessionKey] = state
		return state
	}

	// Resume only when we have a concrete saved agent session ID. If the session
	// is unbound, force a fresh start instead of attaching to whichever CLI
	// conversation happens to be "latest" in this workspace.
	startSessionID := session.GetAgentSessionID()
	isResume := startSessionID != ""
	startAt := time.Now()
	agentSession, err := agent.StartSession(e.ctx, startSessionID)
	startElapsed := time.Since(startAt)
	if err != nil {
		// If resume/continue failed, try a fresh session as fallback.
		if startSessionID != "" {
			slog.Error("session resume failed, falling back to fresh session",
				"session_key", sessionKey, "failed_session_id", startSessionID,
				"error", err, "elapsed", startElapsed)
			startAt = time.Now()
			agentSession, err = agent.StartSession(e.ctx, "")
			startElapsed = time.Since(startAt)
			if err == nil {
				slog.Info("fresh session started after resume failure",
					"session_key", sessionKey, "elapsed", startElapsed)
			}
		}
		if err != nil {
			slog.Error("failed to start interactive session", "error", err, "elapsed", startElapsed)
			state = &interactiveState{platform: p, replyCtx: replyCtx, quiet: quietMode}
			e.interactiveStates[sessionKey] = state
			return state
		}
	}
	if startElapsed >= slowAgentStart {
		slog.Warn("slow agent session start", "elapsed", startElapsed, "agent", agent.Name(), "session_id", startSessionID)
	}

	if newID := agentSession.CurrentSessionID(); newID != "" {
		if session.CompareAndSetAgentSessionID(newID, agent.Name()) {
			sessions.Save()
		}
	}

	state = &interactiveState{
		agentSession: agentSession,
		platform:     p,
		replyCtx:     replyCtx,
		quiet:        quietMode,
	}
	e.interactiveStates[sessionKey] = state

	slog.Info("session spawned", "session_key", sessionKey, "agent_session", session.GetAgentSessionID(), "is_resume", isResume, "elapsed", startElapsed)
	return state
}

// cleanupInteractiveState removes the interactive state for the given session key
// and closes its agent session. When an expected state is provided, cleanup is
// skipped if the map entry has been replaced by a different state — this prevents
// a stale goroutine (still running after /new created a fresh Session object and
// a new turn started on it) from accidentally destroying the replacement state.
func (e *Engine) cleanupInteractiveState(sessionKey string, expected ...*interactiveState) {
	e.interactiveMu.Lock()
	state, ok := e.interactiveStates[sessionKey]
	if len(expected) > 0 && expected[0] != nil && state != expected[0] {
		// Another turn has already replaced the state — skip cleanup.
		e.interactiveMu.Unlock()
		return
	}
	delete(e.interactiveStates, sessionKey)
	e.interactiveMu.Unlock()

	// Notify senders of any queued messages that will never be processed.
	if ok && state != nil {
		e.notifyDroppedQueuedMessages(state, fmt.Errorf("session reset"))
	}

	if ok && state != nil && state.agentSession != nil {
		slog.Debug("cleanupInteractiveState: closing agent session", "session", sessionKey)
		closeStart := time.Now()

		done := make(chan struct{})
		go func() {
			state.agentSession.Close()
			close(done)
		}()

		select {
		case <-done:
			if elapsed := time.Since(closeStart); elapsed >= slowAgentClose {
				slog.Warn("slow agent session close", "elapsed", elapsed, "session", sessionKey)
			}
		case <-time.After(10 * time.Second):
			slog.Error("agent session close timed out (10s), abandoning", "session", sessionKey)
		}
	}
}
