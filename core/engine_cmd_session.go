package core

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

func (e *Engine) cmdNew(p Platform, msg *Message, args []string) {
	agent, sessions, interactiveKey, err := e.commandContext(p, msg)
	if err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsResolutionError, err))
		return
	}

	slog.Info("cmdNew: cleaning up old session", "session_key", msg.SessionKey)
	e.cleanupInteractiveState(interactiveKey)
	slog.Info("cmdNew: cleanup done, creating new session", "session_key", msg.SessionKey)

	// Clear old session's agent session ID so it cannot be resumed
	s := sessions.GetOrCreateActive(msg.SessionKey)
	s.SetAgentSessionID("", "")
	s.ClearHistory()
	sessions.Save()

	name := ""
	if len(args) > 0 {
		name = strings.Join(args, " ")
	}
	s = sessions.NewSession(msg.SessionKey, name)
	// Invalidate cached session list so /list reflects the new session.
	e.invalidateSessionListCache(agent)
	if name != "" {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgNewSessionCreatedName), name))
	} else {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgNewSessionCreated))
	}
}

const listPageSize = 20

func (e *Engine) cmdList(p Platform, msg *Message, args []string) {
	agent, sessions, _, err := e.commandContext(p, msg)
	if err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsResolutionError, err))
		return
	}

	if !supportsCards(p) {
		agentSessions, err := agent.ListSessions(e.ctx)
		if err != nil {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgListError), err))
			return
		}
		if len(agentSessions) == 0 {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgListEmpty))
			return
		}

		total := len(agentSessions)
		totalPages := (total + listPageSize - 1) / listPageSize

		page := 1
		if len(args) > 0 {
			if n, err := strconv.Atoi(args[0]); err == nil && n > 0 {
				page = n
			}
		}
		if page > totalPages {
			page = totalPages
		}

		start := (page - 1) * listPageSize
		end := start + listPageSize
		if end > total {
			end = total
		}

		agentName := agent.Name()
		activeSession := sessions.GetOrCreateActive(msg.SessionKey)
		activeAgentID := activeSession.GetAgentSessionID()

		var sb strings.Builder
		if totalPages > 1 {
			sb.WriteString(fmt.Sprintf(e.i18n.T(MsgListTitlePaged), agentName, total, page, totalPages))
		} else {
			sb.WriteString(fmt.Sprintf(e.i18n.T(MsgListTitle), agentName, total))
		}
		for i := start; i < end; i++ {
			s := agentSessions[i]
			marker := "◻"
			if s.ID == activeAgentID {
				marker = "▶"
			}
			displayName := sessions.GetSessionName(s.ID)
			if displayName != "" {
				displayName = "📌 " + displayName
			} else {
				displayName = strings.ReplaceAll(s.Summary, "\n", " ")
				displayName = strings.Join(strings.Fields(displayName), " ")
				if displayName == "" {
					displayName = "(empty)"
				}
				if len([]rune(displayName)) > 40 {
					displayName = string([]rune(displayName)[:40]) + "…"
				}
			}
			sb.WriteString(fmt.Sprintf("%s **%d.** %s · **%d** msgs · %s\n",
				marker, i+1, displayName, s.MessageCount, s.ModifiedAt.Format("01-02 15:04")))
		}
		if totalPages > 1 {
			sb.WriteString(fmt.Sprintf(e.i18n.T(MsgListPageHint), page, totalPages))
		}
		sb.WriteString(e.i18n.T(MsgListSwitchHint))
		e.reply(p, msg.ReplyCtx, sb.String())
		return
	}

	page := 1
	if len(args) > 0 {
		if n, err := strconv.Atoi(args[0]); err == nil && n > 0 {
			page = n
		}
	}
	card, err := e.renderListCard(msg.SessionKey, page)
	if err != nil {
		e.reply(p, msg.ReplyCtx, err.Error())
		return
	}
	e.replyWithCard(p, msg.ReplyCtx, card)
}

func (e *Engine) cmdSwitch(p Platform, msg *Message, args []string) {
	if len(args) == 0 {
		e.reply(p, msg.ReplyCtx, "Usage: /switch <number | id_prefix | name>")
		return
	}
	query := strings.TrimSpace(strings.Join(args, " "))

	slog.Info("cmdSwitch: listing agent sessions", "session_key", msg.SessionKey)
	agent, sessions, interactiveKey, err := e.commandContext(p, msg)
	if err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsResolutionError, err))
		return
	}
	agentSessions, err := agent.ListSessions(e.ctx)
	if err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgError, err))
		return
	}

	matched := e.matchSession(agentSessions, sessions, query)
	if matched == nil {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgSwitchNoMatch), query))
		return
	}

	slog.Info("cmdSwitch: cleaning up old session", "session_key", msg.SessionKey)
	e.cleanupInteractiveState(interactiveKey)
	slog.Info("cmdSwitch: cleanup done", "session_key", msg.SessionKey)

	session := sessions.GetOrCreateActive(msg.SessionKey)
	session.SetAgentInfo(matched.ID, agent.Name(), matched.Summary)
	session.ClearHistory()
	sessions.Save()

	shortID := matched.ID
	if len(shortID) > 12 {
		shortID = shortID[:12]
	}
	displayName := sessions.GetSessionName(matched.ID)
	if displayName == "" {
		displayName = matched.Summary
	}
	e.reply(p, msg.ReplyCtx,
		e.i18n.Tf(MsgSwitchSuccess, displayName, shortID, matched.MessageCount))
}

// matchSession resolves a user query to an agent session. Priority:
//  1. Numeric index (1-based, matching /list output)
//  2. Exact custom name match (case-insensitive)
//  3. Session ID prefix match
//  4. Custom name prefix match (case-insensitive)
//  5. Summary substring match (case-insensitive)
func (e *Engine) matchSession(sessions []AgentSessionInfo, manager *SessionManager, query string) *AgentSessionInfo {
	if len(sessions) == 0 {
		return nil
	}

	// 1. Numeric index
	if idx, err := strconv.Atoi(query); err == nil && idx >= 1 && idx <= len(sessions) {
		return &sessions[idx-1]
	}

	queryLower := strings.ToLower(query)

	// 2. Exact custom name match
	for i := range sessions {
		name := manager.GetSessionName(sessions[i].ID)
		if name != "" && strings.ToLower(name) == queryLower {
			return &sessions[i]
		}
	}

	// 3. Session ID prefix match
	for i := range sessions {
		if strings.HasPrefix(sessions[i].ID, query) {
			return &sessions[i]
		}
	}

	// 4. Custom name prefix match
	for i := range sessions {
		name := manager.GetSessionName(sessions[i].ID)
		if name != "" && strings.HasPrefix(strings.ToLower(name), queryLower) {
			return &sessions[i]
		}
	}

	// 5. Summary substring match
	for i := range sessions {
		if sessions[i].Summary != "" && strings.Contains(strings.ToLower(sessions[i].Summary), queryLower) {
			return &sessions[i]
		}
	}

	return nil
}

// cmdSearch searches sessions by name or message content.
// Usage: /search <keyword>
func (e *Engine) cmdSearch(p Platform, msg *Message, args []string) {
	if len(args) == 0 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgSearchUsage))
		return
	}

	keyword := strings.ToLower(strings.Join(args, " "))

	// Get all agent sessions
	agent, sessions, _, err := e.commandContext(p, msg)
	if err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsResolutionError, err))
		return
	}
	agentSessions, err := agent.ListSessions(e.ctx)
	if err != nil {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgSearchError), err))
		return
	}

	type searchResult struct {
		id           string
		name         string
		summary      string
		matchType    string // "name" or "message"
		messageCount int
	}

	var results []searchResult

	for _, s := range agentSessions {
		// Check session name (custom name or summary)
		customName := sessions.GetSessionName(s.ID)
		displayName := customName
		if displayName == "" {
			displayName = s.Summary
		}

		// Match by name/summary
		if strings.Contains(strings.ToLower(displayName), keyword) {
			results = append(results, searchResult{
				id:           s.ID,
				name:         displayName,
				summary:      s.Summary,
				matchType:    "name",
				messageCount: s.MessageCount,
			})
			continue
		}

		// Match by session ID prefix
		if strings.HasPrefix(strings.ToLower(s.ID), keyword) {
			results = append(results, searchResult{
				id:           s.ID,
				name:         displayName,
				summary:      s.Summary,
				matchType:    "id",
				messageCount: s.MessageCount,
			})
			continue
		}
	}

	if len(results) == 0 {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgSearchNoResult), keyword))
		return
	}

	// Build result message
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(e.i18n.T(MsgSearchResult), len(results), keyword))

	for i, r := range results {
		shortID := r.id
		if len(shortID) > 12 {
			shortID = shortID[:12]
		}
		sb.WriteString(fmt.Sprintf("\n%d. [%s] %s", i+1, shortID, r.name))
	}
	sb.WriteString("\n\n" + e.i18n.T(MsgSearchHint))

	e.reply(p, msg.ReplyCtx, sb.String())
}

func (e *Engine) cmdName(p Platform, msg *Message, args []string) {
	if len(args) == 0 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgNameUsage))
		return
	}

	agent, sessions, _, err := e.commandContext(p, msg)
	if err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsResolutionError, err))
		return
	}

	// Check if first arg is a number → naming a specific session by list index
	var targetID string
	var name string

	if idx, err := strconv.Atoi(args[0]); err == nil && idx >= 1 {
		// /name <number> <name...>
		if len(args) < 2 {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgNameUsage))
			return
		}
		agentSessions, err := agent.ListSessions(e.ctx)
		if err != nil {
			e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgError, err))
			return
		}
		if idx > len(agentSessions) {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgSwitchNoSession), idx))
			return
		}
		targetID = agentSessions[idx-1].ID
		name = strings.Join(args[1:], " ")
	} else {
		// /name <name...> → current session
		session := sessions.GetOrCreateActive(msg.SessionKey)
		targetID = session.GetAgentSessionID()
		if targetID == "" {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgNameNoSession))
			return
		}
		name = strings.Join(args, " ")
	}

	name = strings.TrimSpace(name)
	if name == "" {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgNameUsage))
		return
	}

	sessions.SetSessionName(targetID, name)

	shortID := targetID
	if len(shortID) > 12 {
		shortID = shortID[:12]
	}
	e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgNameSet), name, shortID))
}

func (e *Engine) cmdCurrent(p Platform, msg *Message) {
	if !supportsCards(p) {
		_, sessions, _, err := e.commandContext(p, msg)
		if err != nil {
			e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsResolutionError, err))
			return
		}
		s := sessions.GetOrCreateActive(msg.SessionKey)
		agentID := s.GetAgentSessionID()
		if agentID == "" {
			agentID = e.i18n.T(MsgSessionNotStarted)
		}
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCurrentSession), s.Name, agentID, len(s.History)))
		return
	}

	e.replyWithCard(p, msg.ReplyCtx, e.renderCurrentCard(msg.SessionKey))
}

func (e *Engine) cmdStatus(p Platform, msg *Message) {
	if !supportsCards(p) {
		agent, sessions, _, err := e.commandContext(p, msg)
		if err != nil {
			e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsResolutionError, err))
			return
		}
		platNames := make([]string, len(e.platforms))
		for i, pl := range e.platforms {
			platNames[i] = pl.Name()
		}
		platformStr := strings.Join(platNames, ", ")
		if len(platNames) == 0 {
			platformStr = "-"
		}

		uptimeStr := formatDurationI18n(time.Since(e.startedAt), e.i18n.CurrentLang())

		cur := e.i18n.CurrentLang()
		langStr := fmt.Sprintf("%s (%s)", string(cur), langDisplayName(cur))

		var modeStr string
		if ms, ok := agent.(ModeSwitcher); ok {
			mode := ms.GetMode()
			if mode != "" {
				modeStr = e.i18n.Tf(MsgStatusMode, mode)
			}
		}

		e.quietMu.RLock()
		globalQuiet := e.quiet
		e.quietMu.RUnlock()

		iKey := e.interactiveKeyForSessionKey(msg.SessionKey)
		e.interactiveMu.Lock()
		state, hasState := e.interactiveStates[iKey]
		e.interactiveMu.Unlock()

		sessionQuiet := false
		if hasState && state != nil {
			state.mu.Lock()
			sessionQuiet = state.quiet
			state.mu.Unlock()
		}

		quietStr := e.i18n.T(MsgQuietOffShort)
		if globalQuiet || sessionQuiet {
			quietStr = e.i18n.T(MsgQuietOnShort)
		}
		modeStr += e.i18n.Tf(MsgStatusQuiet, quietStr)

		s := sessions.GetOrCreateActive(msg.SessionKey)
		sessionDisplayName := sessions.GetSessionName(s.GetAgentSessionID())
		if sessionDisplayName == "" {
			sessionDisplayName = s.Name
		}
		sessionStr := e.i18n.Tf(MsgStatusSession, sessionDisplayName, len(s.History))

		var cronStr string
		if e.cronScheduler != nil {
			if jobs := e.cronScheduler.Store().ListBySessionKey(msg.SessionKey); len(jobs) > 0 {
				enabledCount := 0
				for _, j := range jobs {
					if j.Enabled {
						enabledCount++
					}
				}
				cronStr = e.i18n.Tf(MsgStatusCron, len(jobs), enabledCount)
			}
		}

		sessionKeyStr := e.i18n.Tf(MsgStatusSessionKey, msg.SessionKey)

		userIDStr := ""
		if msg.UserID != "" {
			userIDStr = e.i18n.Tf(MsgStatusUserID, msg.UserID)
		}

		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgStatusTitle,
			e.name,
			agent.Name(),
			platformStr,
			uptimeStr,
			langStr,
			modeStr,
			sessionStr,
			cronStr,
			sessionKeyStr,
			userIDStr,
		))
		return
	}

	e.replyWithCard(p, msg.ReplyCtx, e.renderStatusCard(msg.SessionKey, msg.UserID))
}

func (e *Engine) cmdUsage(p Platform, msg *Message) {
	reporter, ok := e.agent.(UsageReporter)
	if !ok {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgUsageNotSupported))
		return
	}

	fetchCtx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
	defer cancel()

	report, err := reporter.GetUsage(fetchCtx)
	if err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgUsageFetchFailed, err))
		return
	}

	if supportsCards(p) {
		e.replyWithCard(p, msg.ReplyCtx, e.renderUsageCard(report))
		return
	}

	e.reply(p, msg.ReplyCtx, formatUsageReport(report, e.i18n.CurrentLang()))
}

func formatUsageReport(report *UsageReport, lang Language) string {
	if report == nil {
		return usageUnavailableText(lang)
	}

	var sb strings.Builder
	sb.WriteString(usageAccountLabel(lang))
	sb.WriteString(accountDisplay(report))
	sb.WriteString(formatUsageBlocks(report, lang))

	return strings.TrimSpace(sb.String())
}

func formatUsageBlocks(report *UsageReport, lang Language) string {
	primary, secondary := selectUsageWindows(report)
	var sections []string
	if primary != nil {
		sections = append(sections, formatUsageBlock(lang, primary))
	}
	if secondary != nil {
		sections = append(sections, formatUsageBlock(lang, secondary))
	}
	if len(sections) == 0 {
		return ""
	}
	return "\n\n" + strings.Join(sections, "\n\n")
}

func accountDisplay(report *UsageReport) string {
	var base string
	if report.Email != "" {
		base = report.Email
	} else if report.AccountID != "" {
		base = report.AccountID
	} else if report.UserID != "" {
		base = report.UserID
	} else {
		base = "-"
	}
	if report.Plan != "" {
		return fmt.Sprintf("%s (%s)", base, report.Plan)
	}
	return base
}

func selectUsageWindows(report *UsageReport) (*UsageWindow, *UsageWindow) {
	for _, bucket := range report.Buckets {
		if len(bucket.Windows) == 0 {
			continue
		}
		var primary, secondary *UsageWindow
		for i := range bucket.Windows {
			window := &bucket.Windows[i]
			switch window.WindowSeconds {
			case 18000:
				primary = window
			case 604800:
				if secondary == nil {
					secondary = window
				}
			}
		}
		if primary == nil && len(bucket.Windows) > 0 {
			primary = &bucket.Windows[0]
		}
		if secondary == nil && len(bucket.Windows) > 1 {
			secondary = &bucket.Windows[1]
		}
		if primary != nil || secondary != nil {
			return primary, secondary
		}
	}
	return nil, nil
}

func formatUsageBlock(lang Language, window *UsageWindow) string {
	remaining := 100 - window.UsedPercent
	if remaining < 0 {
		remaining = 0
	}
	var sb strings.Builder
	sb.WriteString(usageWindowLabel(lang, window.WindowSeconds))
	sb.WriteString("\n")
	sb.WriteString(usageRemainingLabel(lang))
	sb.WriteString(usageColon(lang))
	sb.WriteString(fmt.Sprintf("%d%%", remaining))
	sb.WriteString("\n")
	sb.WriteString(usageResetLabel(lang))
	sb.WriteString(usageColon(lang))
	sb.WriteString(formatUsageResetTime(lang, window.ResetAfterSeconds))
	return sb.String()
}

func (e *Engine) cmdHistory(p Platform, msg *Message, args []string) {
	if len(args) == 0 && supportsCards(p) {
		e.replyWithCard(p, msg.ReplyCtx, e.renderHistoryCard(msg.SessionKey))
		return
	}
	if len(args) == 0 {
		args = []string{"10"}
	}

	agent, sessions, _, err := e.commandContext(p, msg)
	if err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsResolutionError, err))
		return
	}
	s := sessions.GetOrCreateActive(msg.SessionKey)
	n := 10
	if v, err := strconv.Atoi(args[0]); err == nil && v > 0 {
		n = v
	}

	entries := s.GetHistory(n)
	agentSID := s.GetAgentSessionID()
	if len(entries) == 0 && agentSID != "" {
		if hp, ok := agent.(HistoryProvider); ok {
			if agentEntries, err := hp.GetSessionHistory(e.ctx, agentSID, n); err == nil {
				entries = agentEntries
			}
		}
	}

	if len(entries) == 0 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgHistoryEmpty))
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📜 History (last %d):\n\n", len(entries)))
	for _, h := range entries {
		icon := "👤"
		if h.Role == "assistant" {
			icon = "🤖"
		}
		content := h.Content
		if len([]rune(content)) > 200 {
			content = string([]rune(content)[:200]) + "..."
		}
		sb.WriteString(fmt.Sprintf("%s [%s]\n%s\n\n", icon, h.Timestamp.Format("15:04:05"), content))
	}
	e.reply(p, msg.ReplyCtx, sb.String())
}

func (e *Engine) cmdStop(p Platform, msg *Message) {
	iKey := e.interactiveKeyForSessionKey(msg.SessionKey)
	e.interactiveMu.Lock()
	state, ok := e.interactiveStates[iKey]
	e.interactiveMu.Unlock()

	if !ok || state == nil {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgNoExecution))
		return
	}

	// Cancel pending permission if any
	state.mu.Lock()
	pending := state.pending
	quietMode := state.quiet
	if pending != nil {
		state.pending = nil
	}
	state.mu.Unlock()
	if pending != nil {
		pending.resolve()
	}

	e.cleanupInteractiveState(iKey)

	// Preserve quiet preference across stop
	if quietMode {
		e.interactiveMu.Lock()
		if s, ok := e.interactiveStates[iKey]; ok {
			s.mu.Lock()
			s.quiet = quietMode
			s.mu.Unlock()
		} else {
			e.interactiveStates[iKey] = &interactiveState{platform: p, replyCtx: msg.ReplyCtx, quiet: quietMode}
		}
		e.interactiveMu.Unlock()
	}

	e.reply(p, msg.ReplyCtx, e.i18n.T(MsgExecutionStopped))
}

func (e *Engine) cmdDelete(p Platform, msg *Message, args []string) {
	agent, _, _, err := e.commandContext(p, msg)
	if err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsResolutionError, err))
		return
	}
	deleter, ok := agent.(SessionDeleter)
	if !ok {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgDeleteNotSupported))
		return
	}

	if len(args) == 0 {
		if supportsCards(p) {
			_ = e.getOrCreateDeleteModeState(msg.SessionKey, p, msg.ReplyCtx)
			e.replyWithCard(p, msg.ReplyCtx, e.renderDeleteModeCard(msg.SessionKey))
			return
		}
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgDeleteUsage))
		return
	}
	if len(args) > 1 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgDeleteUsage))
		return
	}

	agentSessions, err := agent.ListSessions(e.ctx)
	if err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgError, err))
		return
	}

	prefix := strings.TrimSpace(args[0])
	if isExplicitDeleteBatchArg(prefix) {
		indices, err := parseDeleteBatchIndices(prefix, len(agentSessions))
		if err != nil {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgDeleteUsage))
			return
		}
		e.cmdDeleteBatch(p, msg, deleter, agentSessions, indices)
		return
	}
	var matched *AgentSessionInfo

	if idx, err := strconv.Atoi(prefix); err == nil && idx >= 1 && idx <= len(agentSessions) {
		matched = &agentSessions[idx-1]
	} else {
		for i := range agentSessions {
			if strings.HasPrefix(agentSessions[i].ID, prefix) {
				matched = &agentSessions[i]
				break
			}
		}
	}

	if matched == nil {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgSwitchNoMatch), prefix))
		return
	}

	e.deleteSingleSession(p, msg, deleter, matched)
}

func isExplicitDeleteBatchArg(arg string) bool {
	if strings.Contains(arg, ",") {
		return true
	}
	if !strings.Contains(arg, "-") {
		return false
	}
	for _, r := range arg {
		if (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

func parseDeleteBatchIndices(spec string, max int) ([]int, error) {
	parts := strings.Split(spec, ",")
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty batch spec")
	}
	seen := make(map[int]struct{}, len(parts))
	indices := make([]int, 0, len(parts))

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("empty batch item")
		}

		if strings.Contains(part, "-") {
			bounds := strings.Split(part, "-")
			if len(bounds) != 2 || bounds[0] == "" || bounds[1] == "" {
				return nil, fmt.Errorf("invalid range %q", part)
			}
			start, err := strconv.Atoi(bounds[0])
			if err != nil {
				return nil, err
			}
			end, err := strconv.Atoi(bounds[1])
			if err != nil {
				return nil, err
			}
			if start < 1 || end < 1 || start > end || end > max {
				return nil, fmt.Errorf("range %q out of bounds", part)
			}
			for idx := start; idx <= end; idx++ {
				if _, ok := seen[idx]; ok {
					continue
				}
				seen[idx] = struct{}{}
				indices = append(indices, idx)
			}
			continue
		}

		idx, err := strconv.Atoi(part)
		if err != nil {
			return nil, err
		}
		if idx < 1 || idx > max {
			return nil, fmt.Errorf("index %d out of bounds", idx)
		}
		if _, ok := seen[idx]; ok {
			continue
		}
		seen[idx] = struct{}{}
		indices = append(indices, idx)
	}

	return indices, nil
}

func (e *Engine) cmdDeleteBatch(p Platform, msg *Message, deleter SessionDeleter, sessions []AgentSessionInfo, indices []int) {
	lines := make([]string, 0, len(indices))
	for _, idx := range indices {
		matched := &sessions[idx-1]
		if line := e.deleteSingleSessionReply(msg, deleter, matched); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgDeleteUsage))
		return
	}
	e.reply(p, msg.ReplyCtx, strings.Join(lines, "\n"))
}

func (e *Engine) deleteSingleSession(p Platform, msg *Message, deleter SessionDeleter, matched *AgentSessionInfo) {
	e.reply(p, msg.ReplyCtx, e.deleteSingleSessionReply(msg, deleter, matched))
}

func (e *Engine) deleteSingleSessionReply(msg *Message, deleter SessionDeleter, matched *AgentSessionInfo) string {
	if matched == nil {
		return ""
	}

	// Prevent deleting the currently active session
	agent, sessions := e.sessionContextForKey(msg.SessionKey)
	activeSession := sessions.GetOrCreateActive(msg.SessionKey)
	if activeSession.GetAgentSessionID() == matched.ID {
		return e.i18n.T(MsgDeleteActiveDenied)
	}

	displayName := e.deleteSessionDisplayName(sessions, matched)

	if err := deleter.DeleteSession(e.ctx, matched.ID); err != nil {
		return e.i18n.Tf(MsgFailedToDeleteSession, displayName, err)
	}

	// Keep local session snapshot aligned with agent-side deletion.
	sessions.DeleteByAgentSessionID(matched.ID)
	sessions.SetSessionName(matched.ID, "")
	// Invalidate cached session list so subsequent card renders see the change.
	e.invalidateSessionListCache(agent)
	return fmt.Sprintf(e.i18n.T(MsgDeleteSuccess), displayName)
}

func (e *Engine) deleteSessionDisplayName(sessions *SessionManager, matched *AgentSessionInfo) string {
	displayName := sessions.GetSessionName(matched.ID)
	if displayName == "" {
		displayName = matched.Summary
	}
	if displayName == "" {
		shortID := matched.ID
		if len(shortID) > 12 {
			shortID = shortID[:12]
		}
		displayName = shortID
	}
	return displayName
}
