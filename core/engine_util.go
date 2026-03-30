package core

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ──────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────

// SendToSession sends a message to an active session from an external caller (API/CLI).
// If sessionKey is empty, it picks the first active session.
func (e *Engine) SendToSession(sessionKey, message string) error {
	return e.SendToSessionWithAttachments(sessionKey, message, nil, nil)
}

func (e *Engine) SendToSessionWithAttachments(sessionKey, message string, images []ImageAttachment, files []FileAttachment) error {
	e.interactiveMu.Lock()

	var state *interactiveState
	if sessionKey != "" {
		state = e.interactiveStates[sessionKey]
	} else if len(e.interactiveStates) == 1 {
		// Single session: use it when no sessionKey is provided (backward compatible)
		for _, s := range e.interactiveStates {
			state = s
			break
		}
	} else if len(e.interactiveStates) > 1 && (len(images) > 0 || len(files) > 0) {
		// Multiple sessions with attachments but no explicit sessionKey: ambiguous
		e.interactiveMu.Unlock()
		return fmt.Errorf("multiple active sessions; must specify --session to send attachments")
	} else {
		// Multiple sessions but text-only: pick the first (legacy behavior)
		for _, s := range e.interactiveStates {
			state = s
			break
		}
	}
	e.interactiveMu.Unlock()

	if state == nil {
		return fmt.Errorf("no active session found (key=%q)", sessionKey)
	}

	state.mu.Lock()
	p := state.platform
	replyCtx := state.replyCtx
	state.mu.Unlock()

	if p == nil {
		return fmt.Errorf("no active session found (key=%q)", sessionKey)
	}

	if message == "" && len(images) == 0 && len(files) == 0 {
		return fmt.Errorf("message or attachment is required")
	}
	if (len(images) > 0 || len(files) > 0) && !e.attachmentSendEnabled {
		return ErrAttachmentSendDisabled
	}

	var imageSender ImageSender
	if len(images) > 0 {
		var ok bool
		imageSender, ok = p.(ImageSender)
		if !ok {
			return fmt.Errorf("platform %s: %w", p.Name(), ErrNotSupported)
		}
	}

	var fileSender FileSender
	if len(files) > 0 {
		var ok bool
		fileSender, ok = p.(FileSender)
		if !ok {
			return fmt.Errorf("platform %s: %w", p.Name(), ErrNotSupported)
		}
	}

	if message != "" {
		if err := p.Send(e.ctx, replyCtx, message); err != nil {
			return err
		}
		if len(images) > 0 || len(files) > 0 {
			state.mu.Lock()
			state.sideText = strings.TrimSpace(message)
			state.mu.Unlock()
		}
	}
	for _, img := range images {
		if err := imageSender.SendImage(e.ctx, replyCtx, img); err != nil {
			return err
		}
	}
	for _, file := range files {
		if err := fileSender.SendFile(e.ctx, replyCtx, file); err != nil {
			return err
		}
	}
	return nil
}

// sendPermissionPrompt sends a permission prompt with interactive buttons when
// the platform supports them. Fallback chain: InlineButtonSender → CardSender → plain text.
func (e *Engine) sendPermissionPrompt(p Platform, replyCtx any, prompt, toolName, toolInput string) {
	// Try inline buttons first (Telegram)
	if bs, ok := p.(InlineButtonSender); ok {
		buttons := [][]ButtonOption{
			{
				{Text: e.i18n.T(MsgPermBtnAllow), Data: "perm:allow"},
				{Text: e.i18n.T(MsgPermBtnDeny), Data: "perm:deny"},
			},
			{
				{Text: e.i18n.T(MsgPermBtnAllowAll), Data: "perm:allow_all"},
			},
		}
		if err := bs.SendWithButtons(e.ctx, replyCtx, prompt, buttons); err == nil {
			return
		} else {
			slog.Warn("sendPermissionPrompt: inline buttons failed, falling back", "error", err)
		}
	}

	// Try card with buttons (Feishu/Lark)
	if supportsCards(p) {
		body := fmt.Sprintf(e.i18n.T(MsgPermCardBody), toolName, toolInput)
		extra := func(label, color string) map[string]string {
			return map[string]string{
				"perm_label": label,
				"perm_color": color,
				"perm_body":  body,
			}
		}
		allowBtn := CardButton{Text: e.i18n.T(MsgPermBtnAllow), Type: "primary", Value: "perm:allow",
			Extra: extra("✅ "+e.i18n.T(MsgPermBtnAllow), "green")}
		denyBtn := CardButton{Text: e.i18n.T(MsgPermBtnDeny), Type: "danger", Value: "perm:deny",
			Extra: extra("❌ "+e.i18n.T(MsgPermBtnDeny), "red")}
		allowAllBtn := CardButton{Text: e.i18n.T(MsgPermBtnAllowAll), Type: "default", Value: "perm:allow_all",
			Extra: extra("✅ "+e.i18n.T(MsgPermBtnAllowAll), "green")}

		card := NewCard().
			Title(e.i18n.T(MsgPermCardTitle), "orange").
			Markdown(body).
			ButtonsEqual(allowBtn, denyBtn).
			Buttons(allowAllBtn).
			Note(e.i18n.T(MsgPermCardNote)).
			Build()
		e.sendWithCard(p, replyCtx, card)
		return
	}

	e.send(p, replyCtx, prompt)
}

// sendAskQuestionPrompt renders one question (by index) from the AskUserQuestion list.
// qIdx is the 0-based index of the question to display.
func (e *Engine) sendAskQuestionPrompt(p Platform, replyCtx any, questions []UserQuestion, qIdx int) {
	if qIdx >= len(questions) {
		return
	}
	q := questions[qIdx]
	total := len(questions)

	titleSuffix := ""
	if total > 1 {
		titleSuffix = fmt.Sprintf(" (%d/%d)", qIdx+1, total)
	}

	// Try card (Feishu/Lark)
	if supportsCards(p) {
		cb := NewCard().Title(e.i18n.T(MsgAskQuestionTitle)+titleSuffix, "blue")
		body := "**" + q.Question + "**"
		if q.MultiSelect {
			body += e.i18n.T(MsgAskQuestionMulti)
		}
		cb.Markdown(body)
		for i, opt := range q.Options {
			desc := opt.Label
			if opt.Description != "" {
				desc += " — " + opt.Description
			}
			answerData := fmt.Sprintf("askq:%d:%d", qIdx, i+1)
			cb.ListItemBtnExtra(desc, opt.Label, "default", answerData, map[string]string{
				"askq_label":    opt.Label,
				"askq_question": q.Question,
			})
		}
		cb.Note(e.i18n.T(MsgAskQuestionNote))
		e.sendWithCard(p, replyCtx, cb.Build())
		return
	}

	// Try inline buttons (Telegram)
	if bs, ok := p.(InlineButtonSender); ok {
		var textBuf strings.Builder
		textBuf.WriteString("❓ *")
		textBuf.WriteString(q.Question)
		textBuf.WriteString("*")
		textBuf.WriteString(titleSuffix)
		if q.MultiSelect {
			textBuf.WriteString(e.i18n.T(MsgAskQuestionMulti))
		}
		hasDesc := false
		for _, opt := range q.Options {
			if opt.Description != "" {
				hasDesc = true
				break
			}
		}
		if hasDesc {
			textBuf.WriteString("\n")
			for i, opt := range q.Options {
				textBuf.WriteString(fmt.Sprintf("\n*%d. %s*", i+1, opt.Label))
				if opt.Description != "" {
					textBuf.WriteString(" — ")
					textBuf.WriteString(opt.Description)
				}
			}
			textBuf.WriteString("\n")
		}
		var rows [][]ButtonOption
		for i, opt := range q.Options {
			rows = append(rows, []ButtonOption{{Text: opt.Label, Data: fmt.Sprintf("askq:%d:%d", qIdx, i+1)}})
		}
		if err := bs.SendWithButtons(e.ctx, replyCtx, textBuf.String(), rows); err == nil {
			return
		}
	}

	// Plain text fallback
	var sb strings.Builder
	sb.WriteString("❓ **")
	sb.WriteString(q.Question)
	sb.WriteString("**")
	sb.WriteString(titleSuffix)
	if q.MultiSelect {
		sb.WriteString(e.i18n.T(MsgAskQuestionMulti))
	}
	sb.WriteString("\n\n")
	for i, opt := range q.Options {
		sb.WriteString(fmt.Sprintf("%d. **%s**", i+1, opt.Label))
		if opt.Description != "" {
			sb.WriteString(" — ")
			sb.WriteString(opt.Description)
		}
		sb.WriteString("\n")
	}
	sb.WriteString(fmt.Sprintf("\n%s", e.i18n.T(MsgAskQuestionNote)))
	e.send(p, replyCtx, sb.String())
}

// send wraps p.Send with error logging and slow-operation warnings.
func (e *Engine) send(p Platform, replyCtx any, content string) {
	start := time.Now()
	if err := p.Send(e.ctx, replyCtx, content); err != nil {
		slog.Error("platform send failed", "platform", p.Name(), "error", err, "content_len", len(content))
	}
	if elapsed := time.Since(start); elapsed >= slowPlatformSend {
		slog.Warn("slow platform send", "platform", p.Name(), "elapsed", elapsed, "content_len", len(content))
	}
}

// drainEvents discards any buffered events from the channel.
// Called before a new turn to prevent stale events from a previous turn's
// agent process from being mistaken for the new turn's response.
func drainEvents(ch <-chan Event) {
	drained := 0
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				// Channel is closed; stop immediately to avoid an infinite loop.
				return
			}
			drained++
		default:
			if drained > 0 {
				slog.Warn("drained stale events from previous turn", "count", drained)
			}
			return
		}
	}
}

// reply wraps p.Reply with error logging and slow-operation warnings.
func (e *Engine) reply(p Platform, replyCtx any, content string) {
	start := time.Now()
	if err := p.Reply(e.ctx, replyCtx, content); err != nil {
		slog.Error("platform reply failed", "platform", p.Name(), "error", err, "content_len", len(content))
	}
	if elapsed := time.Since(start); elapsed >= slowPlatformSend {
		slog.Warn("slow platform reply", "platform", p.Name(), "elapsed", elapsed, "content_len", len(content))
	}
}

// replyWithButtons sends a reply with inline buttons if the platform supports it,
// otherwise falls back to plain text reply.
func (e *Engine) replyWithButtons(p Platform, replyCtx any, content string, buttons [][]ButtonOption) {
	if bs, ok := p.(InlineButtonSender); ok {
		if err := bs.SendWithButtons(e.ctx, replyCtx, content, buttons); err == nil {
			return
		}
	}
	e.reply(p, replyCtx, content)
}

func supportsCards(p Platform) bool {
	_, ok := p.(CardSender)
	return ok
}

// replyWithCard sends a structured card via CardSender.
// For platforms without card support, renders as plain text (no intermediate fallback).
func (e *Engine) replyWithCard(p Platform, replyCtx any, card *Card) {
	if card == nil {
		slog.Error("replyWithCard: nil card", "platform", p.Name())
		return
	}
	if cs, ok := p.(CardSender); ok {
		if err := cs.ReplyCard(e.ctx, replyCtx, card); err != nil {
			slog.Error("card reply failed", "platform", p.Name(), "error", err)
		}
		return
	}
	e.reply(p, replyCtx, card.RenderText())
}

// sendWithCard sends a card as a new message (not a reply).
func (e *Engine) sendWithCard(p Platform, replyCtx any, card *Card) {
	if card == nil {
		slog.Error("sendWithCard: nil card", "platform", p.Name())
		return
	}
	if cs, ok := p.(CardSender); ok {
		if err := cs.SendCard(e.ctx, replyCtx, card); err != nil {
			slog.Error("card send failed", "platform", p.Name(), "error", err)
		}
		return
	}
	e.send(p, replyCtx, card.RenderText())
}

// toolCodeLang picks the code block language hint for tool display.
func toolCodeLang(toolName, input string) string {
	switch toolName {
	case "shell", "run_shell_command", "Bash":
		return "bash"
	case "write_file", "WriteFile", "replace", "ReplaceInFile":
		if strings.Contains(input, "\n- ") || strings.Contains(input, "\n+ ") {
			return "diff"
		}
	}
	// Fallback: detect diff-like content
	if strings.Contains(input, "\n- ") && strings.Contains(input, "\n+ ") {
		return "diff"
	}
	return ""
}

// truncateIf truncates s to maxLen runes. 0 means no truncation.
func truncateIf(s string, maxLen int) string {
	if maxLen <= 0 {
		return s
	}
	if utf8.RuneCountInString(s) <= maxLen {
		return s
	}
	return string([]rune(s)[:maxLen]) + "..."
}

func splitMessage(text string, maxLen int) []string {
	runes := []rune(text)
	if len(runes) <= maxLen {
		return []string{text}
	}
	var chunks []string

	for len(runes) > 0 {
		if len(runes) <= maxLen {
			chunks = append(chunks, string(runes))
			break
		}

		end := maxLen

		// Try to split at newline boundary within the rune window.
		// Convert the candidate chunk back to a string for newline search.
		candidate := string(runes[:end])
		if idx := strings.LastIndex(candidate, "\n"); idx > 0 {
			// idx is a byte offset within candidate; convert to rune offset.
			runeIdx := utf8.RuneCountInString(candidate[:idx])
			if runeIdx >= end/2 {
				end = runeIdx + 1
			}
		}

		chunks = append(chunks, string(runes[:end]))
		runes = runes[end:]
	}
	return chunks
}

// sendTTSReply synthesizes fullResponse text and sends audio to the platform.
// Called asynchronously after EventResult; text reply is always sent first.
func (e *Engine) sendTTSReply(p Platform, replyCtx any, text string) {
	slog.Debug("tts: sendTTSReply called", "platform", p.Name(), "text_len", len(text))
	if e.tts == nil {
		slog.Warn("tts: e.tts is nil, skipping")
		return
	}
	if e.tts.TTS == nil {
		slog.Warn("tts: e.tts.TTS is nil, skipping")
		return
	}
	if e.tts.MaxTextLen > 0 && utf8.RuneCountInString(text) > e.tts.MaxTextLen {
		slog.Warn("tts: text exceeds max_text_len, skipping synthesis", "len", utf8.RuneCountInString(text), "max", e.tts.MaxTextLen)
		return
	}
	slog.Info("tts: starting synthesis", "voice", e.tts.Voice, "text_len", len(text))
	opts := TTSSynthesisOpts{Voice: e.tts.Voice}
	audioData, format, err := e.tts.TTS.Synthesize(e.ctx, text, opts)
	if err != nil {
		slog.Error("tts: synthesis failed", "error", err)
		return
	}
	slog.Info("tts: synthesis successful", "format", format, "audio_size", len(audioData))
	as, ok := p.(AudioSender)
	if !ok {
		slog.Warn("tts: platform does not support audio sending", "platform", p.Name())
		return
	}
	if err := as.SendAudio(e.ctx, replyCtx, audioData, format); err != nil {
		slog.Error("tts: platform audio send failed", "platform", p.Name(), "error", err)
		return
	}
	slog.Info("tts: audio sent successfully", "platform", p.Name())
}

// ──────────────────────────────────────────────────────────────
// Bot-to-bot relay
// ──────────────────────────────────────────────────────────────

// HandleRelay processes a relay message synchronously: starts or resumes a
// dedicated relay session, sends the message to the agent, and blocks until
// the complete response is collected.
func (e *Engine) HandleRelay(ctx context.Context, fromProject, chatID, message string) (string, error) {
	relaySessionKey := "relay:" + fromProject + ":" + chatID
	session := e.sessions.GetOrCreateActive(relaySessionKey)

	if inj, ok := e.agent.(SessionEnvInjector); ok {
		envVars := []string{
			"CC_PROJECT=" + e.name,
			"CC_SESSION_KEY=" + relaySessionKey,
		}
		if exePath, err := os.Executable(); err == nil {
			binDir := filepath.Dir(exePath)
			if curPath := os.Getenv("PATH"); curPath != "" {
				envVars = append(envVars, "PATH="+binDir+string(filepath.ListSeparator)+curPath)
			}
		}
		inj.SetSessionEnv(envVars)
	}

	agentSession, err := e.agent.StartSession(ctx, session.GetAgentSessionID())
	if err != nil {
		return "", fmt.Errorf("start relay session: %w", err)
	}
	defer agentSession.Close()

	if session.CompareAndSetAgentSessionID(agentSession.CurrentSessionID(), e.agent.Name()) {
		e.sessions.Save()
	}

	if err := agentSession.Send(message, nil, nil); err != nil {
		return "", fmt.Errorf("send relay message: %w", err)
	}

	var textParts []string
	for event := range agentSession.Events() {
		if ctx.Err() != nil {
			return relayPartialResponseOrError(ctx.Err(), textParts, fromProject, e.name)
		}
		switch event.Type {
		case EventText:
			if event.Content != "" {
				textParts = append(textParts, event.Content)
			}
			if event.SessionID != "" {
				if session.CompareAndSetAgentSessionID(event.SessionID, e.agent.Name()) {
					e.sessions.Save()
				}
			}
		case EventToolResult:
			out := strings.TrimSpace(event.Content)
			if out == "" {
				out = strings.TrimSpace(event.ToolResult)
			}
			if out != "" {
				tn := strings.TrimSpace(event.ToolName)
				if tn == "" {
					tn = "tool"
				}
				textParts = append(textParts, fmt.Sprintf(e.i18n.T(MsgToolResult), tn, out)+"\n\n")
			}
		case EventResult:
			if event.SessionID != "" {
				session.SetAgentSessionID(event.SessionID, e.agent.Name())
				e.sessions.Save()
			}
			resp := event.Content
			if resp == "" && len(textParts) > 0 {
				resp = strings.Join(textParts, "")
			}
			if resp == "" {
				resp = "(empty response)"
			}
			slog.Info("relay: turn complete", "from", fromProject, "to", e.name, "response_len", len(resp))
			return resp, nil
		case EventError:
			if event.Error != nil {
				return "", event.Error
			}
			return "", fmt.Errorf("agent error (no details)")
		case EventPermissionRequest:
			// Auto-approve all permissions in relay mode
			_ = agentSession.RespondPermission(event.RequestID, PermissionResult{
				Behavior:     "allow",
				UpdatedInput: event.ToolInputRaw,
			})
		}
	}

	if ctx.Err() != nil {
		return relayPartialResponseOrError(ctx.Err(), textParts, fromProject, e.name)
	}

	if len(textParts) > 0 {
		return strings.Join(textParts, ""), nil
	}
	return "", fmt.Errorf("relay: agent process exited without response")
}

func relayPartialResponseOrError(ctxErr error, textParts []string, fromProject, toProject string) (string, error) {
	if len(textParts) == 0 {
		return "", ctxErr
	}

	resp := strings.Join(textParts, "")
	slog.Warn("relay: context done before final result; returning partial response",
		"from", fromProject,
		"to", toProject,
		"error", ctxErr,
		"response_len", len(resp),
	)
	return resp, nil
}

// ── Context usage indicator ──────────────────────────────────

const modelContextWindow = 200_000 // Claude's context window size in tokens

// contextIndicator returns a suffix like "\n[ctx: ~42%]" based on SDK-reported input tokens.
func contextIndicator(inputTokens int) string {
	if inputTokens <= 0 {
		return ""
	}
	pct := inputTokens * 100 / modelContextWindow
	if pct > 100 {
		pct = 100
	}
	return fmt.Sprintf("\n[ctx: ~%d%%]", pct)
}

// ctxSelfReportRe matches agent self-reported context lines like "[ctx: ~42%]".
var ctxSelfReportRe = regexp.MustCompile(`(?m)\n?\[ctx: ~\d+%\]`)

// parseSelfReportedCtx extracts the percentage from a self-reported "[ctx: ~XX%]" line.
func parseSelfReportedCtx(s string) int {
	m := ctxSelfReportRe.FindString(s)
	if m == "" {
		return 0
	}
	start := strings.Index(m, "~") + 1
	end := strings.Index(m, "%")
	if start <= 0 || end <= start {
		return 0
	}
	v, _ := strconv.Atoi(m[start:end])
	return v
}
