package core

import (
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"
)

const defaultEventIdleTimeout = 2 * time.Hour

func (e *Engine) processInteractiveEvents(state *interactiveState, session *Session, sessions *SessionManager, sessionKey string, msgID string, turnStart time.Time, stopTypingFn func(), sendDone <-chan error, replyCtx any) {
	var textParts []string
	var segmentStart int // index into textParts: text before this has been sent/displayed
	toolCount := 0
	waitStart := time.Now()
	firstEventLogged := false
	triggerAutoCompress := false
	pendingSend := sendDone

	// stopTyping tracks the current turn's typing indicator so it can be
	// stopped when a queued message starts a new turn.
	stopTyping := stopTypingFn
	defer func() {
		if stopTyping != nil {
			stopTyping()
		}
	}()

	state.mu.Lock()
	sp := newStreamPreview(e.streamPreview, state.platform, state.replyCtx, e.ctx)
	state.mu.Unlock()

	// Idle timeout: 0 = disabled
	var idleTimer *time.Timer
	var idleCh <-chan time.Time
	if e.eventIdleTimeout > 0 {
		idleTimer = time.NewTimer(e.eventIdleTimeout)
		defer idleTimer.Stop()
		idleCh = idleTimer.C
	}

	events := state.agentSession.Events()

	// 长任务进度心跳：工具调用超过 30 秒时通知用户
	var toolProgressTimer *time.Timer
	var toolProgressCh <-chan time.Time
	var currentToolName string
	toolProgressTimeout := 30 * time.Second
	stopToolProgress := func() {
		if toolProgressTimer != nil {
			toolProgressTimer.Stop()
			toolProgressTimer = nil
			toolProgressCh = nil
		}
	}
	defer stopToolProgress()
	for {
		var event Event
		var ok bool

		select {
		case event, ok = <-events:
			if !ok {
				goto channelClosed
			}
		case err := <-pendingSend:
			pendingSend = nil
			if err != nil {
				slog.Error("failed to send prompt", "error", err, "session_key", sessionKey)
				sp.discard()
				if stopTyping != nil {
					stopTyping()
					stopTyping = nil
				}
				e.notifyDroppedQueuedMessages(state, err)
				if state.agentSession == nil || !state.agentSession.Alive() {
					e.cleanupInteractiveState(sessionKey, state)
				}
				state.mu.Lock()
				p := state.platform
				state.mu.Unlock()
				e.send(p, replyCtx, fmt.Sprintf(e.i18n.T(MsgError), err))
				return
			}
			continue
		case <-toolProgressCh:
			// 工具调用超过 30 秒，发送进度心跳通知用户
			toolName := currentToolName
			if toolName == "" {
				toolName = "tool"
			}
			e.quietMu.RLock()
			globalQuiet := e.quiet
			e.quietMu.RUnlock()
			state.mu.Lock()
			sessionQuiet := state.quiet
			progressP := state.platform
			state.mu.Unlock()
			if !globalQuiet && !sessionQuiet && e.showToolProcess {
				e.send(progressP, replyCtx, fmt.Sprintf(e.i18n.T(MsgToolProgress), toolName))
			}
			// 继续监听，每 30 秒再提醒一次
			toolProgressTimer.Reset(toolProgressTimeout)
			continue
		case <-idleCh:
			slog.Error("agent session idle timeout: no events for too long, killing session",
				"session_key", sessionKey, "timeout", e.eventIdleTimeout, "elapsed", time.Since(turnStart))
			sp.discard()
			state.mu.Lock()
			p := state.platform
			state.mu.Unlock()
			e.send(p, replyCtx, fmt.Sprintf(e.i18n.T(MsgError), "agent session timed out (no response)"))
			e.cleanupInteractiveState(sessionKey, state)
			return
		case <-e.ctx.Done():
			return
		}

		// Reset idle timer after receiving an event
		if idleTimer != nil {
			if !idleTimer.Stop() {
				select {
				case <-idleTimer.C:
				default:
				}
			}
			idleTimer.Reset(e.eventIdleTimeout)
		}

		if !firstEventLogged {
			firstEventLogged = true
			if elapsed := time.Since(waitStart); elapsed >= slowAgentFirstEvent {
				slog.Warn("slow agent first event", "elapsed", elapsed, "session", sessionKey, "event_type", event.Type)
			}
		}

		state.mu.Lock()
		p := state.platform
		sessionQuiet := state.quiet
		state.mu.Unlock()

		e.quietMu.RLock()
		globalQuiet := e.quiet
		e.quietMu.RUnlock()

		quiet := globalQuiet || sessionQuiet

		switch event.Type {
		case EventThinking:
			if !quiet && event.Content != "" {
				// Flush accumulated text segment before thinking display
				previewActive := sp.canPreview()
				if len(textParts) > segmentStart {
					if !previewActive {
						segment := strings.Join(textParts[segmentStart:], "")
						if segment != "" {
							for _, chunk := range splitMessage(segment, maxPlatformMessageLen) {
								e.send(p, replyCtx, chunk)
							}
						}
					}
					segmentStart = len(textParts)
				}
				sp.freeze()
				if previewActive {
					sp.detachPreview() // keep frozen preview visible as permanent message
				}
				preview := truncateIf(event.Content, e.display.ThinkingMaxLen)
				e.send(p, replyCtx, fmt.Sprintf(e.i18n.T(MsgThinking), preview))
			}

		case EventToolUse:
			toolCount++
			if !quiet && e.showToolProcess {
				// Flush accumulated text segment before tool display
				previewActive := sp.canPreview()
				if len(textParts) > segmentStart {
					if !previewActive {
						segment := strings.Join(textParts[segmentStart:], "")
						if segment != "" {
							for _, chunk := range splitMessage(segment, maxPlatformMessageLen) {
								e.send(p, replyCtx, chunk)
							}
						}
					}
					segmentStart = len(textParts)
				}
				sp.freeze()
				if previewActive {
					sp.detachPreview() // keep frozen preview visible as permanent message
				}
				toolInput := event.ToolInput
				var formattedInput string
				if toolInput == "" {
					formattedInput = ""
				} else if strings.Contains(toolInput, "```") {
					// Already contains code blocks (pre-formatted by agent) — use as-is
					formattedInput = toolInput
				} else if strings.Contains(toolInput, "\n") || utf8.RuneCountInString(toolInput) > 200 {
					lang := toolCodeLang(event.ToolName, toolInput)
					formattedInput = fmt.Sprintf("```%s\n%s\n```", lang, toolInput)
				} else {
					switch event.ToolName {
					case "shell", "run_shell_command", "Bash":
						formattedInput = fmt.Sprintf("```bash\n%s\n```", toolInput)
					default:
						formattedInput = fmt.Sprintf("`%s`", toolInput)
					}
				}
				toolMsg := fmt.Sprintf(e.i18n.T(MsgTool), toolCount, event.ToolName, formattedInput)
				for _, chunk := range SplitMessageCodeFenceAware(toolMsg, maxPlatformMessageLen) {
					e.send(p, replyCtx, chunk)
				}
			}
			// 启动工具进度心跳计时器（30s 后提醒用户工具仍在执行）
			currentToolName = event.ToolName
			stopToolProgress()
			toolProgressTimer = time.NewTimer(toolProgressTimeout)
			toolProgressCh = toolProgressTimer.C

		case EventToolResult:
			// 工具执行完成，停止进度心跳
			stopToolProgress()
			if !quiet && e.showToolProcess {
				out := strings.TrimSpace(event.Content)
				if out == "" {
					out = strings.TrimSpace(event.ToolResult)
				}
				if out == "" {
					break
				}
				tn := strings.TrimSpace(event.ToolName)
				if tn == "" {
					tn = "tool"
				}
				previewActive := sp.canPreview()
				if len(textParts) > segmentStart {
					if !previewActive {
						segment := strings.Join(textParts[segmentStart:], "")
						if segment != "" {
							for _, chunk := range splitMessage(segment, maxPlatformMessageLen) {
								e.send(p, replyCtx, chunk)
							}
						}
					}
					segmentStart = len(textParts)
				}
				sp.freeze()
				if previewActive {
					sp.detachPreview()
				}
				var formattedOut string
				if strings.Contains(out, "```") {
					formattedOut = out
				} else if strings.Contains(out, "\n") || utf8.RuneCountInString(out) > 200 {
					lang := toolCodeLang(tn, out)
					formattedOut = fmt.Sprintf("```%s\n%s\n```", lang, out)
				} else {
					switch tn {
					case "shell", "run_shell_command", "Bash":
						formattedOut = fmt.Sprintf("```bash\n%s\n```", out)
					default:
						formattedOut = fmt.Sprintf("`%s`", out)
					}
				}
				toolMsg := fmt.Sprintf(e.i18n.T(MsgToolResult), tn, formattedOut)
				for _, chunk := range SplitMessageCodeFenceAware(toolMsg, maxPlatformMessageLen) {
					e.send(p, replyCtx, chunk)
				}
			}

		case EventText:
			if event.Content != "" {
				textParts = append(textParts, event.Content)
				if sp.canPreview() {
					sp.appendText(event.Content)
				}
			}
			if event.SessionID != "" {
				if session.CompareAndSetAgentSessionID(event.SessionID, e.agent.Name()) {
					pendingName := session.GetName()
					if pendingName != "" && pendingName != "session" && pendingName != "default" {
						sessions.SetSessionName(event.SessionID, pendingName)
					}
					sessions.Save()
				}
			}

		case EventPermissionRequest:
			isAskQuestion := event.ToolName == "AskUserQuestion" && len(event.Questions) > 0

			state.mu.Lock()
			autoApprove := state.approveAll
			state.mu.Unlock()

			if autoApprove && !isAskQuestion {
				slog.Debug("auto-approving (approve-all)", "request_id", event.RequestID, "tool", event.ToolName)
				_ = state.agentSession.RespondPermission(event.RequestID, PermissionResult{
					Behavior:     "allow",
					UpdatedInput: event.ToolInputRaw,
				})
				continue
			}

			// Flush accumulated text segment before permission prompt
			previewActive := sp.canPreview()
			if len(textParts) > segmentStart {
				if !previewActive {
					segment := strings.Join(textParts[segmentStart:], "")
					if segment != "" {
						for _, chunk := range splitMessage(segment, maxPlatformMessageLen) {
							e.send(p, replyCtx, chunk)
						}
					}
				}
				segmentStart = len(textParts)
			}
			sp.freeze()
			if previewActive {
				sp.detachPreview() // keep frozen preview visible as permanent message
			}

			slog.Info("permission request",
				"request_id", event.RequestID,
				"tool", event.ToolName,
			)

			if isAskQuestion {
				e.sendAskQuestionPrompt(p, replyCtx, event.Questions, 0)
			} else {
				permLimit := e.display.ToolMaxLen
				if permLimit > 0 {
					permLimit = permLimit * 8 / 5
				}
				toolInput := truncateIf(event.ToolInput, permLimit)
				prompt := fmt.Sprintf(e.i18n.T(MsgPermissionPrompt), event.ToolName, toolInput)
				e.sendPermissionPrompt(p, replyCtx, prompt, event.ToolName, toolInput)
			}

			pending := &pendingPermission{
				RequestID:    event.RequestID,
				ToolName:     event.ToolName,
				ToolInput:    event.ToolInputRaw,
				InputPreview: event.ToolInput,
				Questions:    event.Questions,
				Resolved:     make(chan struct{}),
			}
			state.mu.Lock()
			state.pending = pending
			state.mu.Unlock()

			// Stop idle timer while waiting for user permission response;
			// the user may take a long time to decide, and we don't want
			// the idle timeout to kill the session during that wait.
			if idleTimer != nil {
				idleTimer.Stop()
			}

			<-pending.Resolved
			slog.Info("permission resolved", "request_id", event.RequestID)

			// Restart idle timer after permission is resolved
			if idleTimer != nil {
				idleTimer.Reset(e.eventIdleTimeout)
			}

		case EventResult:
			if event.SessionID != "" {
				session.SetAgentSessionID(event.SessionID, e.agent.Name())
			}

			fullResponse := event.Content
			if fullResponse == "" && len(textParts) > 0 {
				fullResponse = strings.Join(textParts, "")
			}
			if fullResponse == "" {
				fullResponse = e.i18n.T(MsgEmptyResponse)
			}

			// Context usage indicator: prefer SDK tokens, fall back to self-reported.
			sdkPlausible := event.InputTokens >= 100
			selfPct := parseSelfReportedCtx(fullResponse)
			cleanResponse := ctxSelfReportRe.ReplaceAllString(fullResponse, "")
			cleanResponse = strings.TrimRight(cleanResponse, "\n ")

			// Evaluate auto-compress trigger (token estimate on user+assistant text,
			// including this turn's assistant reply before it is appended to history).
			if e.autoCompressEnabled && e.autoCompressMaxTokens > 0 {
				estimate := estimateTokensWithPendingAssistant(session.GetHistory(0), cleanResponse)
				now := time.Now()
				state.mu.Lock()
				last := state.lastAutoCompressAt
				state.mu.Unlock()
				if estimate >= e.autoCompressMaxTokens && (last.IsZero() || now.Sub(last) >= e.autoCompressMinGap) {
					triggerAutoCompress = true
					state.mu.Lock()
					state.lastAutoCompressTokens = estimate
					state.mu.Unlock()
				}
			}

			session.AddHistory("assistant", cleanResponse)
			sessions.Save()

			// 异步持久化 assistant 回复到 MySQL
			if e.chatStore != nil {
				e.chatStore.SaveMessage(e.ctx, ChatMessage{
					SessionID: session.ID,
					Role:      "assistant",
					Content:   cleanResponse,
				})
			}

			if e.showContextIndicator {
				if sdkPlausible {
					cleanResponse += contextIndicator(event.InputTokens)
				} else if selfPct > 0 {
					cleanResponse += fmt.Sprintf("\n[ctx: ~%d%%]", selfPct)
				}
			}
			fullResponse = cleanResponse

			turnDuration := time.Since(turnStart)
			slog.Info("turn complete",
				"session", session.ID,
				"agent_session", session.GetAgentSessionID(),
				"msg_id", msgID,
				"tools", toolCount,
				"response_len", len(fullResponse),
				"turn_duration", turnDuration,
				"input_tokens", event.InputTokens,
				"output_tokens", event.OutputTokens,
			)

			replyStart := time.Now()
			normalizedResponse := strings.TrimSpace(fullResponse)
			state.mu.Lock()
			suppressDuplicate := normalizedResponse != "" && normalizedResponse == state.sideText
			state.sideText = ""
			state.mu.Unlock()

			// When tool calls happened and prior text was already surfaced in segments,
			// only send the unsent remainder. In quiet mode, tool events don't surface
			// side-channel messages and segmentStart stays 0, so keep normal finalize flow.
			if toolCount > 0 && segmentStart > 0 {
				sp.discard()
				if segmentStart < len(textParts) {
					unsent := strings.Join(textParts[segmentStart:], "")
					if unsent != "" {
						for _, chunk := range splitMessage(unsent, maxPlatformMessageLen) {
							if err := p.Send(e.ctx, replyCtx, chunk); err != nil {
								slog.Error("failed to send reply", "error", err, "msg_id", msgID)
								return
							}
						}
					}
				}
			} else if suppressDuplicate {
				sp.discard()
				slog.Debug("EventResult: suppressed duplicate side-channel text", "response_len", len(fullResponse))
			} else if sp.finish(fullResponse) {
				slog.Debug("EventResult: finalized via stream preview", "response_len", len(fullResponse))
			} else {
				slog.Debug("EventResult: sending via p.Send (preview inactive or failed)", "response_len", len(fullResponse), "chunks", len(splitMessage(fullResponse, maxPlatformMessageLen)))
				for _, chunk := range splitMessage(fullResponse, maxPlatformMessageLen) {
					if err := p.Send(e.ctx, replyCtx, chunk); err != nil {
						slog.Error("failed to send reply", "error", err, "msg_id", msgID)
						return
					}
				}
			}

			if elapsed := time.Since(replyStart); elapsed >= slowPlatformSend {
				slog.Warn("slow final reply send", "platform", p.Name(), "elapsed", elapsed, "response_len", len(fullResponse))
			}

			// TTS: async voice reply if enabled
			if e.tts != nil && e.tts.Enabled && e.tts.TTS != nil {
				state.mu.Lock()
				fromVoice := state.fromVoice
				state.mu.Unlock()
				mode := e.tts.GetTTSMode()
				slog.Debug("tts: checking conditions", "mode", mode, "fromVoice", fromVoice, "will_send", mode == "always" || (mode == "voice_only" && fromVoice))
				if mode == "always" || (mode == "voice_only" && fromVoice) {
					go e.sendTTSReply(p, replyCtx, fullResponse)
				}
			} else {
				slog.Debug("tts: not enabled", "tts_nil", e.tts == nil, "enabled", e.tts != nil && e.tts.Enabled, "tts_obj_nil", e.tts == nil || e.tts.TTS == nil)
			}

			// Auto-compress after finishing a turn, before sending any queued messages.
			if triggerAutoCompress {
				if pendingSend != nil {
					if err := <-pendingSend; err != nil {
						slog.Debug("async send error before compress", "error", err)
					}
				}
				state.mu.Lock()
				state.lastAutoCompressAt = time.Now()
				state.mu.Unlock()
				slog.Info("auto-compress: triggering", "session", sessionKey)

				// Run compress inline while the session is still locked.
				e.runCompress(state, session, sessions, sessionKey, state.platform, state.replyCtx, true)
				return
			}

			// Check for queued messages — if present, continue the event loop
			// for the next turn instead of returning.
			state.mu.Lock()
			if len(state.pendingMessages) > 0 {
				queued := state.pendingMessages[0]
				state.pendingMessages = state.pendingMessages[1:]
				remainingQueue := len(state.pendingMessages)
				state.platform = queued.platform
				state.replyCtx = queued.replyCtx
				state.fromVoice = queued.fromVoice
				state.mu.Unlock()

				// Stop the previous turn's typing indicator
				if stopTyping != nil {
					stopTyping()
					stopTyping = nil
				}
				// Start a new typing indicator for the queued message's context
				if ti, ok := queued.platform.(TypingIndicator); ok {
					stopTyping = ti.StartTyping(e.ctx, queued.replyCtx)
				}

				// Drain stale events before starting the next turn. Between
				// EventResult and Send(), the only buffered events would be
				// stale leftovers (e.g. a deferred EventError from cmd.Wait()).
				drainEvents(state.agentSession.Events())

				if pendingSend != nil {
					if err := <-pendingSend; err != nil {
						slog.Debug("async send error before queued turn", "error", err)
					}
				}

				queuedPrompt := e.buildSenderPrompt(queued.content, queued.userID, queued.msgPlatform, queued.msgSessionKey)

				nextSend := make(chan error, 1)
				go func() {
					nextSend <- state.agentSession.Send(queuedPrompt, queued.images, queued.files)
				}()
				pendingSend = nextSend

				// Detect language now (deferred from queue time to avoid
				// flipping locale while the previous turn is still running).
				e.i18n.DetectAndSet(queued.content)

				// Reset per-turn state for the next turn
				textParts = nil
				segmentStart = 0
				toolCount = 0
				turnStart = time.Now()
				firstEventLogged = false
				waitStart = time.Now()
				sp = newStreamPreview(e.streamPreview, queued.platform, queued.replyCtx, e.ctx)

				session.AddHistory("user", queued.content)

				// 异步持久化排队用户消息到 MySQL
				if e.chatStore != nil {
					e.chatStore.SaveMessage(e.ctx, ChatMessage{
						SessionID: session.ID,
						Role:      "user",
						Content:   queued.content,
						Platform:  queued.msgPlatform,
						UserID:    queued.userID,
					})
				}

				if idleTimer != nil {
					if !idleTimer.Stop() {
						select {
						case <-idleTimer.C:
						default:
						}
					}
					idleTimer.Reset(e.eventIdleTimeout)
				}

				slog.Info("processing queued message",
					"session", sessionKey,
					"remaining_queue", remainingQueue,
				)
				continue
			}
			state.mu.Unlock()

			if pendingSend != nil {
				if err := <-pendingSend; err != nil {
					slog.Debug("async send error after EventResult", "error", err)
				}
			}

			// Session turn fully complete with no pending messages — send completion notification to IM
			if e.sessionCompleteNotify {
				state.mu.Lock()
				notifyP := state.platform
				notifyCtx := state.replyCtx
				state.mu.Unlock()
				e.send(notifyP, notifyCtx, "->->->-> session complete")
			}
			return

		case EventError:
			sp.discard()
			if event.Error != nil {
				slog.Error("agent error", "error", event.Error)
				e.send(p, replyCtx, fmt.Sprintf(e.i18n.T(MsgError), event.Error))
			}
			// Only drop queued messages if the agent session is dead.
			// Some agents (e.g. Codex) emit EventError for per-turn failures
			// while keeping the session alive for subsequent turns.
			if state.agentSession == nil || !state.agentSession.Alive() {
				e.notifyDroppedQueuedMessages(state, event.Error)
			}
			return
		}
	}

channelClosed:
	// Channel closed - process exited unexpectedly
	slog.Warn("agent process exited", "session_key", sessionKey)
	e.notifyDroppedQueuedMessages(state, fmt.Errorf("agent process exited"))
	e.cleanupInteractiveState(sessionKey, state)

	// 通知用户 agent 进程意外退出，并提示可以继续发消息（下次会自动重建 session）
	state.mu.Lock()
	crashP := state.platform
	state.mu.Unlock()
	e.send(crashP, replyCtx, e.i18n.T(MsgAgentCrashed))

	if len(textParts) > 0 {
		state.mu.Lock()
		p := state.platform
		state.mu.Unlock()

		fullResponse := strings.Join(textParts, "")
		session.AddHistory("assistant", fullResponse)

		// 异步持久化 assistant 回复到 MySQL (agent 进程退出时的残留回复)
		if e.chatStore != nil {
			e.chatStore.SaveMessage(e.ctx, ChatMessage{
				SessionID: session.ID,
				Role:      "assistant",
				Content:   fullResponse,
			})
		}

		if toolCount > 0 && segmentStart > 0 {
			sp.discard()
			if segmentStart < len(textParts) {
				unsent := strings.Join(textParts[segmentStart:], "")
				if unsent != "" {
					for _, chunk := range splitMessage(unsent, maxPlatformMessageLen) {
						e.send(p, replyCtx, chunk)
					}
				}
			}
		} else if sp.finish(fullResponse) {
			slog.Debug("stream preview: finalized in-place (process exited)")
		} else {
			for _, chunk := range splitMessage(fullResponse, maxPlatformMessageLen) {
				e.send(p, replyCtx, chunk)
			}
		}
	}
}

// notifyDroppedQueuedMessages drains pendingMessages from the state and
// sends an error notification to each queued message's sender. Called when
// the event loop exits abnormally (EventError, channel closed) and queued
// messages can no longer be delivered to the agent.
func (e *Engine) notifyDroppedQueuedMessages(state *interactiveState, reason error) {
	state.mu.Lock()
	remaining := state.pendingMessages
	state.pendingMessages = nil
	state.mu.Unlock()
	for _, q := range remaining {
		e.send(q.platform, q.replyCtx, fmt.Sprintf(e.i18n.T(MsgError), reason))
	}
}

// drainPendingMessages processes all queued messages in the state's pendingMessages
// queue. It atomically unlocks the session when the queue is empty (while holding
// state.mu) to close the race window between "queue empty" and "session unlocked".
// Returns true if the session was unlocked by this call.
func (e *Engine) drainPendingMessages(state *interactiveState, session *Session, sessions *SessionManager, sessionKey string) bool {
	for {
		state.mu.Lock()
		if len(state.pendingMessages) == 0 {
			session.Unlock()
			state.mu.Unlock()
			return true
		}
		queued := state.pendingMessages[0]
		state.pendingMessages = state.pendingMessages[1:]
		state.platform = queued.platform
		state.replyCtx = queued.replyCtx
		state.fromVoice = queued.fromVoice
		state.mu.Unlock()

		e.i18n.DetectAndSet(queued.content)
		prompt := e.buildSenderPrompt(queued.content, queued.userID, queued.msgPlatform, queued.msgSessionKey)

		if state.agentSession == nil || !state.agentSession.Alive() {
			e.send(queued.platform, queued.replyCtx, fmt.Sprintf(e.i18n.T(MsgError), "agent session ended"))
			e.notifyDroppedQueuedMessages(state, fmt.Errorf("agent session ended"))
			return false
		}

		drainEvents(state.agentSession.Events())

		session.AddHistory("user", queued.content)

		// 异步持久化排队用户消息到 MySQL (drain 循环)
		if e.chatStore != nil {
			e.chatStore.SaveMessage(e.ctx, ChatMessage{
				SessionID: session.ID,
				Role:      "user",
				Content:   queued.content,
				Platform:  queued.msgPlatform,
				UserID:    queued.userID,
			})
		}

		sendDone := make(chan error, 1)
		go func() {
			sendDone <- state.agentSession.Send(prompt, queued.images, queued.files)
		}()

		var stopTyping func()
		if ti, ok := queued.platform.(TypingIndicator); ok {
			stopTyping = ti.StartTyping(e.ctx, queued.replyCtx)
		}

		slog.Info("processing queued message", "session", sessionKey)
		e.processInteractiveEvents(state, session, sessions, sessionKey, "", time.Now(), stopTyping, sendDone, queued.replyCtx)
	}
}
