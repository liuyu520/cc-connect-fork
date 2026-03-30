package core

import (
	"fmt"
	"strings"
	"time"
)

func (e *Engine) cmdCompress(p Platform, msg *Message) {
	compressor, ok := e.agent.(ContextCompressor)
	if !ok || compressor.CompressCommand() == "" {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgCompressNotSupported))
		return
	}

	iKey := e.interactiveKeyForSessionKey(msg.SessionKey)
	e.interactiveMu.Lock()
	state, hasState := e.interactiveStates[iKey]
	e.interactiveMu.Unlock()

	if !hasState || state == nil || state.agentSession == nil || !state.agentSession.Alive() {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgCompressNoSession))
		return
	}

	_, sessions := e.sessionContextForKey(msg.SessionKey)
	session := sessions.GetOrCreateActive(msg.SessionKey)
	if !session.TryLock() {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgPreviousProcessing))
		return
	}

	e.send(p, msg.ReplyCtx, e.i18n.T(MsgCompressing))

	go e.runCompress(state, session, sessions, iKey, p, msg.ReplyCtx, false)
}

// runCompress sends the agent's compress command and handles results.
// If autoTriggered is true, suppress user-visible "compressing" and completion messages.
func (e *Engine) runCompress(state *interactiveState, session *Session, sessions *SessionManager, iKey string, p Platform, replyCtx any, auto bool) {
	// session.Unlock() is called inside drainQueuedMessagesAfterCompress
	// while holding state.mu to close the race window. Deferred fallback
	// ensures the lock is released on early-return paths.
	compressUnlocked := false
	defer func() {
		if !compressUnlocked {
			session.Unlock()
		}
	}()

	state.mu.Lock()
	state.platform = p
	state.replyCtx = replyCtx
	state.mu.Unlock()

	drainEvents(state.agentSession.Events())

	compressor, ok := e.agent.(ContextCompressor)
	if !ok || compressor.CompressCommand() == "" {
		if !auto {
			e.reply(p, replyCtx, e.i18n.T(MsgCompressNotSupported))
		}
		return
	}

	cmd := compressor.CompressCommand()
	if err := state.agentSession.Send(cmd, nil, nil); err != nil {
		if !auto {
			e.reply(p, replyCtx, fmt.Sprintf(e.i18n.T(MsgError), err))
		}
		if !state.agentSession.Alive() {
			e.cleanupInteractiveState(iKey)
		}
		return
	}

	e.processCompressEvents(state, session, sessions, iKey, p, replyCtx, &compressUnlocked, auto)
}

// processCompressEvents drains agent events after a compress command.
// Unlike processInteractiveEvents it does NOT record history and treats
// an empty result as success rather than "(empty response)".
func (e *Engine) processCompressEvents(state *interactiveState, session *Session, sessions *SessionManager, sessionKey string, p Platform, replyCtx any, unlocked *bool, auto bool) {

	var textParts []string
	events := state.agentSession.Events()

	var idleTimer *time.Timer
	var idleCh <-chan time.Time
	if e.eventIdleTimeout > 0 {
		idleTimer = time.NewTimer(e.eventIdleTimeout)
		defer idleTimer.Stop()
		idleCh = idleTimer.C
	}

	for {
		var event Event
		var ok bool

		select {
		case event, ok = <-events:
			if !ok {
				e.cleanupInteractiveState(sessionKey, state)
				if !auto {
					if len(textParts) > 0 {
						e.send(p, replyCtx, strings.Join(textParts, ""))
					} else {
						e.reply(p, replyCtx, e.i18n.T(MsgCompressDone))
					}
				}
				e.notifyDroppedQueuedMessages(state, fmt.Errorf("agent process exited during compress"))
				return
			}
		case <-idleCh:
			if !auto {
				e.send(p, replyCtx, fmt.Sprintf(e.i18n.T(MsgError), "compress timed out"))
			}
			e.cleanupInteractiveState(sessionKey, state)
			e.notifyDroppedQueuedMessages(state, fmt.Errorf("compress timed out"))
			return
		case <-e.ctx.Done():
			return
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

		switch event.Type {
		case EventText:
			if !auto && event.Content != "" {
				textParts = append(textParts, event.Content)
			}
		case EventToolResult:
			if !auto {
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
				textParts = append(textParts, fmt.Sprintf(e.i18n.T(MsgToolResult), tn, out)+"\n")
			}
		case EventResult:
			result := event.Content
			if result == "" && len(textParts) > 0 {
				result = strings.Join(textParts, "")
			}
			if !auto {
				if result != "" {
					e.send(p, replyCtx, result)
				} else {
					e.reply(p, replyCtx, e.i18n.T(MsgCompressDone))
				}
			}

			// After compress succeeds, process any queued messages instead of dropping them.
			e.drainQueuedMessagesAfterCompress(state, session, sessions, sessionKey, unlocked)
			return
		case EventError:
			if !auto && event.Error != nil {
				e.reply(p, replyCtx, fmt.Sprintf(e.i18n.T(MsgError), event.Error))
			}
			// Only drop queued messages if the agent is dead; some agents
			// emit per-turn EventError while staying alive.
			if !state.agentSession.Alive() {
				e.notifyDroppedQueuedMessages(state, event.Error)
			} else {
				// Agent survived — try to process queued messages.
				e.drainQueuedMessagesAfterCompress(state, session, sessions, sessionKey, unlocked)
			}
			return
		case EventPermissionRequest:
			_ = state.agentSession.RespondPermission(event.RequestID, PermissionResult{
				Behavior:     "allow",
				UpdatedInput: event.ToolInputRaw,
			})
		}
	}
}

// drainQueuedMessagesAfterCompress processes any messages that were queued
// during a /compress operation. It sends each one to the agent and runs the
// full interactive event loop for it.
func (e *Engine) drainQueuedMessagesAfterCompress(state *interactiveState, session *Session, sessions *SessionManager, sessionKey string, unlocked *bool) {
	if e.drainPendingMessages(state, session, sessions, sessionKey) {
		*unlocked = true
	}
}
