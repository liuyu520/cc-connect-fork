package core

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (e *Engine) renderUsageCard(report *UsageReport) *Card {
	lang := e.i18n.CurrentLang()
	return NewCard().
		Title(usageCardTitle(lang), "indigo").
		Markdown(strings.TrimSpace(formatUsageReport(report, lang))).
		Buttons(e.cardBackButton()).
		Build()
}

func formatUsageResetTime(lang Language, resetAfterSeconds int) string {
	if resetAfterSeconds <= 0 {
		switch lang {
		case LangChinese, LangTraditionalChinese:
			return "-"
		case LangJapanese:
			return "-"
		case LangSpanish:
			return "-"
		default:
			return "-"
		}
	}
	return formatDurationI18n(time.Duration(resetAfterSeconds)*time.Second, lang)
}

func usageAccountLabel(lang Language) string {
	switch lang {
	case LangChinese:
		return "账号："
	case LangTraditionalChinese:
		return "帳號："
	case LangJapanese:
		return "アカウント: "
	case LangSpanish:
		return "Cuenta: "
	default:
		return "Account: "
	}
}

func usageWindowLabel(lang Language, seconds int) string {
	switch seconds {
	case 18000:
		switch lang {
		case LangChinese:
			return "5小时限额"
		case LangTraditionalChinese:
			return "5小時限額"
		case LangJapanese:
			return "5時間枠"
		case LangSpanish:
			return "Límite 5h"
		default:
			return "5h limit"
		}
	case 604800:
		switch lang {
		case LangChinese:
			return "7日限额"
		case LangTraditionalChinese:
			return "7日限額"
		case LangJapanese:
			return "7日枠"
		case LangSpanish:
			return "Límite 7d"
		default:
			return "7d limit"
		}
	default:
		switch lang {
		case LangChinese, LangTraditionalChinese:
			return formatDurationI18n(time.Duration(seconds)*time.Second, lang) + "限额"
		case LangJapanese:
			return formatDurationI18n(time.Duration(seconds)*time.Second, lang) + "枠"
		case LangSpanish:
			return "Límite " + formatDurationI18n(time.Duration(seconds)*time.Second, lang)
		default:
			return formatDurationI18n(time.Duration(seconds)*time.Second, lang) + " limit"
		}
	}
}

func usageRemainingLabel(lang Language) string {
	switch lang {
	case LangChinese:
		return "剩余"
	case LangTraditionalChinese:
		return "剩餘"
	case LangJapanese:
		return "残り"
	case LangSpanish:
		return "restante"
	default:
		return "Remaining"
	}
}

func usageResetLabel(lang Language) string {
	switch lang {
	case LangChinese:
		return "重置"
	case LangTraditionalChinese:
		return "重置"
	case LangJapanese:
		return "リセット"
	case LangSpanish:
		return "Reinicio"
	default:
		return "Resets"
	}
}

func usageColon(lang Language) string {
	switch lang {
	case LangChinese, LangTraditionalChinese:
		return "："
	default:
		return ": "
	}
}

func usageCardTitle(lang Language) string {
	switch lang {
	case LangChinese:
		return "Usage"
	case LangTraditionalChinese:
		return "Usage"
	case LangJapanese:
		return "Usage"
	case LangSpanish:
		return "Usage"
	default:
		return "Usage"
	}
}

func usageUnavailableText(lang Language) string {
	switch lang {
	case LangChinese:
		return "暂无 usage 信息。"
	case LangTraditionalChinese:
		return "暫無 usage 資訊。"
	case LangJapanese:
		return "usage 情報はありません。"
	case LangSpanish:
		return "No hay datos de usage."
	default:
		return "Usage unavailable."
	}
}

func splitCardTitleBody(content string) (string, string) {
	content = strings.TrimSpace(content)
	parts := strings.SplitN(content, "\n\n", 2)
	title := strings.TrimSpace(parts[0])
	if len(parts) == 1 {
		return title, ""
	}
	return title, strings.TrimSpace(parts[1])
}

func (e *Engine) cardBackButton() CardButton {
	return DefaultBtn(e.i18n.T(MsgCardBack), "nav:/help")
}

func (e *Engine) cardPrevButton(action string) CardButton {
	return DefaultBtn(e.i18n.T(MsgCardPrev), action)
}

func (e *Engine) cardNextButton(action string) CardButton {
	return DefaultBtn(e.i18n.T(MsgCardNext), action)
}

// simpleCard builds a card with a title, markdown body and a single Back button.
// Used to reduce repetition across render functions that share this pattern.
func (e *Engine) simpleCard(title, color, content string) *Card {
	return NewCard().Title(title, color).Markdown(content).Buttons(e.cardBackButton()).Build()
}

// renderListCardSafe wraps renderListCard and returns an error card on failure.
func (e *Engine) renderListCardSafe(sessionKey string, page int) *Card {
	card, err := e.renderListCard(sessionKey, page)
	if err != nil {
		agent, _ := e.sessionContextForKey(sessionKey)
		return e.simpleCard(e.i18n.Tf(MsgCardTitleSessions, agent.Name(), 0), "red", err.Error())
	}
	return card
}

// renderDirCardSafe wraps renderDirCard and returns an error card on failure.
func (e *Engine) renderDirCardSafe(sessionKey string, page int) *Card {
	card, err := e.renderDirCard(sessionKey, page)
	if err != nil {
		return e.simpleCard(e.i18n.T(MsgDirCardTitle), "red", err.Error())
	}
	return card
}

func (e *Engine) renderStatusCard(sessionKey string, userID string) *Card {
	agent, sessions := e.sessionContextForKey(sessionKey)
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

	e.interactiveMu.Lock()
	state, hasState := e.interactiveStates[sessionKey]
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

	s := sessions.GetOrCreateActive(sessionKey)
	sessionDisplayName := sessions.GetSessionName(s.GetAgentSessionID())
	if sessionDisplayName == "" {
		sessionDisplayName = s.GetName()
	}
	sessionStr := e.i18n.Tf(MsgStatusSession, sessionDisplayName, len(s.History))

	var cronStr string
	if e.cronScheduler != nil {
		if jobs := e.cronScheduler.Store().ListBySessionKey(sessionKey); len(jobs) > 0 {
			enabledCount := 0
			for _, j := range jobs {
				if j.Enabled {
					enabledCount++
				}
			}
			cronStr = e.i18n.Tf(MsgStatusCron, len(jobs), enabledCount)
		}
	}

	sessionKeyStr := e.i18n.Tf(MsgStatusSessionKey, sessionKey)

	userIDStr := ""
	if userID != "" {
		userIDStr = e.i18n.Tf(MsgStatusUserID, userID)
	}

	statusText := e.i18n.Tf(MsgStatusTitle,
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
	)
	title, body := splitCardTitleBody(statusText)

	return NewCard().
		Title(title, "green").
		Markdown(body).
		Buttons(e.cardBackButton()).
		Build()
}

func cronTimeFormat(t, now time.Time) string {
	if t.Year() != now.Year() {
		return "2006-01-02 15:04"
	}
	return "01-02 15:04"
}

func formatDurationI18n(d time.Duration, lang Language) string {
	d = d.Round(time.Second)
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60

	switch lang {
	case LangChinese, LangTraditionalChinese:
		if days > 0 {
			return fmt.Sprintf("%d天 %d小时 %d分钟", days, hours, minutes)
		}
		if hours > 0 {
			return fmt.Sprintf("%d小时 %d分钟", hours, minutes)
		}
		return fmt.Sprintf("%d分钟", minutes)
	case LangJapanese:
		if days > 0 {
			return fmt.Sprintf("%d日 %d時間 %d分", days, hours, minutes)
		}
		if hours > 0 {
			return fmt.Sprintf("%d時間 %d分", hours, minutes)
		}
		return fmt.Sprintf("%d分", minutes)
	case LangSpanish:
		if days > 0 {
			return fmt.Sprintf("%d días %dh %dm", days, hours, minutes)
		}
		if hours > 0 {
			return fmt.Sprintf("%dh %dm", hours, minutes)
		}
		return fmt.Sprintf("%dm", minutes)
	default:
		if days > 0 {
			return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
		}
		if hours > 0 {
			return fmt.Sprintf("%dh %dm", hours, minutes)
		}
		return fmt.Sprintf("%dm", minutes)
	}
}


const defaultHelpGroup = "session"

type helpCardItem struct {
	command string
	action  string
}

type helpCardGroup struct {
	key      string
	titleKey MsgKey
	items    []helpCardItem
}

func helpCardGroups() []helpCardGroup {
	return []helpCardGroup{
		{
			key:      "session",
			titleKey: MsgHelpSessionSection,
			items: []helpCardItem{
				{command: "/new", action: "act:/new"},
				{command: "/list", action: "nav:/list"},
				{command: "/current", action: "nav:/current"},
				{command: "/switch", action: "nav:/list"},
				{command: "/search", action: "cmd:/search"},
				{command: "/history", action: "nav:/history"},
				{command: "/delete", action: "cmd:/delete"},
				{command: "/name", action: "cmd:/name"},
			},
		},
		{
			key:      "agent",
			titleKey: MsgHelpAgentSection,
			items: []helpCardItem{
				{command: "/model", action: "nav:/model"},
				{command: "/reasoning", action: "nav:/reasoning"},
				{command: "/mode", action: "nav:/mode"},
				{command: "/lang", action: "nav:/lang"},
				{command: "/provider", action: "nav:/provider"},
				{command: "/memory", action: "cmd:/memory"},
				{command: "/allow", action: "cmd:/allow"},
				{command: "/quiet", action: "act:/quiet"},
				{command: "/tts", action: "cmd:/tts"},
			},
		},
		{
			key:      "tools",
			titleKey: MsgHelpToolsSection,
			items: []helpCardItem{
				{command: "/shell", action: "cmd:/shell"},
				{command: "/cron", action: "nav:/cron"},
				{command: "/heartbeat", action: "nav:/heartbeat"},
				{command: "/commands", action: "nav:/commands"},
				{command: "/alias", action: "nav:/alias"},
				{command: "/skills", action: "nav:/skills"},
				{command: "/compress", action: "cmd:/compress"},
				{command: "/stop", action: "act:/stop"},
			},
		},
		{
			key:      "system",
			titleKey: MsgHelpSystemSection,
			items: []helpCardItem{
				{command: "/status", action: "nav:/status"},
				{command: "/doctor", action: "nav:/doctor"},
				{command: "/usage", action: "cmd:/usage"},
				{command: "/config", action: "nav:/config"},
				{command: "/bind", action: "cmd:/bind"},
				{command: "/workspace", action: "cmd:/workspace"},
				{command: "/dir", action: "nav:/dir"},
				{command: "/version", action: "nav:/version"},
				{command: "/upgrade", action: "nav:/upgrade"},
				{command: "/restart", action: "cmd:/restart"},
			},
		},
	}
}

func (e *Engine) renderHelpCard() *Card {
	return e.renderHelpGroupCard(defaultHelpGroup)
}

// splitHelpTabRows splits tab buttons into rows. Card-based platforms
// get 2 buttons per row for better layout; others get all in one row.
func splitHelpTabRows(useMultiRow bool, tabs []CardButton) [][]CardButton {
	if useMultiRow {
		rows := make([][]CardButton, 0, (len(tabs)+1)/2)
		for i := 0; i < len(tabs); i += 2 {
			end := i + 2
			if end > len(tabs) {
				end = len(tabs)
			}
			rows = append(rows, tabs[i:end])
		}
		return rows
	}
	return [][]CardButton{tabs}
}

func (e *Engine) renderHelpGroupCard(groupKey string) *Card {
	sectionTitle := func(key MsgKey) string {
		section := e.i18n.T(key)
		if idx := strings.IndexByte(section, '\n'); idx >= 0 {
			return section[:idx]
		}
		return section
	}
	tabLabel := func(key MsgKey) string {
		return strings.Trim(sectionTitle(key), "* ")
	}
	commandText := func(command string) string {
		return "**" + command + "**  " + e.i18n.T(MsgKey(strings.TrimPrefix(command, "/")))
	}

	groups := helpCardGroups()
	current := groups[0]
	normalizedGroup := strings.ToLower(strings.TrimSpace(groupKey))
	for _, group := range groups {
		if group.key == normalizedGroup {
			current = group
			break
		}
	}

	cb := NewCard().Title(e.i18n.T(MsgHelpTitle), "blue")
	var tabs []CardButton
	for _, group := range groups {
		btnType := "default"
		if group.key == current.key {
			btnType = "primary"
		}
		tabs = append(tabs, Btn(tabLabel(group.titleKey), btnType, "nav:/help "+group.key))
	}
	for _, row := range splitHelpTabRows(true, tabs) {
		cb.ButtonsEqual(row...)
	}
	for _, item := range current.items {
		cb.ListItem(commandText(item.command), "▶", item.action)
	}
	cb.Note(e.i18n.T(MsgHelpTip))
	return cb.Build()
}

// ──────────────────────────────────────────────────────────────

// handleCardNav is called by platforms that support in-place card updates.
// It routes nav: and act: prefixed actions to the appropriate render function.
func (e *Engine) handleCardNav(action string, sessionKey string) *Card {
	var prefix, body string
	if i := strings.Index(action, ":"); i >= 0 {
		prefix = action[:i]
		body = action[i+1:]
	} else {
		return nil
	}

	cmd, args := body, ""
	if i := strings.IndexByte(body, ' '); i >= 0 {
		cmd = body[:i]
		args = strings.TrimSpace(body[i+1:])
	}

	if prefix == "act" {
		e.executeCardAction(cmd, args, sessionKey)
	}

	switch cmd {
	case "/help":
		return e.renderHelpGroupCard(args)
	case "/model":
		return e.renderModelCard()
	case "/reasoning":
		return e.renderReasoningCard()
	case "/mode":
		return e.renderModeCard()
	case "/lang":
		return e.renderLangCard()
	case "/status":
		return e.renderStatusCard(sessionKey, extractUserID(sessionKey))
	case "/list":
		page := 1
		if args != "" {
			if n, err := strconv.Atoi(args); err == nil && n > 0 {
				page = n
			}
		}
		return e.renderListCardSafe(sessionKey, page)
	case "/dir":
		page := 1
		if args != "" {
			if n, err := strconv.Atoi(args); err == nil && n > 0 {
				page = n
			}
		}
		return e.renderDirCardSafe(sessionKey, page)
	case "/current":
		return e.renderCurrentCard(sessionKey)
	case "/history":
		return e.renderHistoryCard(sessionKey)
	case "/provider":
		return e.renderProviderCard()
	case "/cron":
		return e.renderCronCard(sessionKey, extractUserID(sessionKey))
	case "/heartbeat":
		return e.renderHeartbeatCard()
	case "/commands":
		return e.renderCommandsCard()
	case "/alias":
		return e.renderAliasCard()
	case "/config":
		return e.renderConfigCard()
	case "/skills":
		return e.renderSkillsCard()
	case "/doctor":
		return e.renderDoctorCard()
	case "/whoami":
		return e.renderWhoamiCard(&Message{
			SessionKey: sessionKey,
			UserID:     extractUserID(sessionKey),
			Platform:   extractPlatformName(sessionKey),
		})
	case "/version":
		return e.renderVersionCard()
	case "/new":
		return e.renderCurrentCard(sessionKey)
	case "/quiet":
		return e.renderStatusCard(sessionKey, extractUserID(sessionKey))
	case "/switch":
		return e.renderListCardSafe(sessionKey, 1)
	case "/delete-mode":
		if strings.HasPrefix(args, "cancel") {
			return e.renderListCardSafe(sessionKey, 1)
		}
		return e.renderDeleteModeCard(sessionKey)
	case "/stop":
		return e.renderStatusCard(sessionKey, extractUserID(sessionKey))
	case "/upgrade":
		return e.renderUpgradeCard()
	}
	return nil
}

// executeCardAction performs the side-effect for act: prefixed actions
// (e.g. switching model/mode/lang) before the card is re-rendered.
func (e *Engine) executeCardAction(cmd, args, sessionKey string) {
	switch cmd {
	case "/model":
		if args == "" {
			return
		}
		switcher, ok := e.agent.(ModelSwitcher)
		if !ok {
			return
		}
		fetchCtx, cancel := context.WithTimeout(e.ctx, 3*time.Second)
		defer cancel()
		models := switcher.AvailableModels(fetchCtx)
		target, ok := parseModelSwitchArgs(strings.Fields(args))
		if !ok {
			return
		}
		if idx, err := strconv.Atoi(target); err == nil && idx >= 1 && idx <= len(models) {
			target = models[idx-1].Name
		} else {
			target = resolveModelAlias(models, target)
		}
		if _, err := e.switchModel(target); err != nil {
			slog.Error("failed to switch model from card action", "model", target, "error", err)
			return
		}
		interactiveKey := e.interactiveKeyForSessionKey(sessionKey)
		e.cleanupInteractiveState(interactiveKey)
		s := e.sessions.GetOrCreateActive(sessionKey)
		s.SetAgentSessionID("", "")
		s.ClearHistory()
		e.sessions.Save()

	case "/reasoning":
		if args == "" {
			return
		}
		switcher, ok := e.agent.(ReasoningEffortSwitcher)
		if !ok {
			return
		}
		efforts := switcher.AvailableReasoningEfforts()
		target := strings.ToLower(strings.TrimSpace(args))
		if idx, err := strconv.Atoi(target); err == nil && idx >= 1 && idx <= len(efforts) {
			target = efforts[idx-1]
		}
		for _, effort := range efforts {
			if effort == target {
				switcher.SetReasoningEffort(target)
				interactiveKey := e.interactiveKeyForSessionKey(sessionKey)
				e.cleanupInteractiveState(interactiveKey)
				s := e.sessions.GetOrCreateActive(sessionKey)
				s.SetAgentSessionID("", "")
				s.ClearHistory()
				e.sessions.Save()
				return
			}
		}

	case "/mode":
		if args == "" {
			return
		}
		switcher, ok := e.agent.(ModeSwitcher)
		if !ok {
			return
		}
		newMode := strings.ToLower(args)
		switcher.SetMode(newMode)
		interactiveKey := e.interactiveKeyForSessionKey(sessionKey)
		if e.applyLiveModeChange(sessionKey, switcher.GetMode()) {
			e.cleanupInteractiveState(interactiveKey)
			return
		}
		e.cleanupInteractiveState(interactiveKey)
		// Mode change requires a new session to take effect
		s := e.sessions.GetOrCreateActive(sessionKey)
		s.SetAgentSessionID("", "")
		s.ClearHistory()
		e.sessions.Save()

	case "/lang":
		if args == "" {
			return
		}
		target := strings.ToLower(strings.TrimSpace(args))
		var lang Language
		switch target {
		case "en", "english":
			lang = LangEnglish
		case "zh", "cn", "chinese":
			lang = LangChinese
		case "zh-tw", "zh_tw", "zhtw":
			lang = LangTraditionalChinese
		case "ja", "jp", "japanese":
			lang = LangJapanese
		case "es", "spanish":
			lang = LangSpanish
		case "auto":
			lang = LangAuto
		default:
			return
		}
		e.i18n.SetLang(lang)

	case "/provider":
		if args == "" {
			return
		}
		switcher, ok := e.agent.(ProviderSwitcher)
		if !ok {
			return
		}
		if switcher.SetActiveProvider(args) {
			interactiveKey := e.interactiveKeyForSessionKey(sessionKey)
			e.cleanupInteractiveState(interactiveKey)
			if e.providerSaveFunc != nil {
				_ = e.providerSaveFunc(args)
			}
		}

	case "/new":
		interactiveKey := e.interactiveKeyForSessionKey(sessionKey)
		_, sessions := e.sessionContextForKey(sessionKey)
		e.cleanupInteractiveState(interactiveKey)
		sessions.NewSession(sessionKey, "")

	case "/delete-mode":
		e.executeDeleteModeAction(sessionKey, args)

	case "/quiet":
		interactiveKey := e.interactiveKeyForSessionKey(sessionKey)
		e.interactiveMu.Lock()
		state, ok := e.interactiveStates[interactiveKey]
		if !ok || state == nil {
			state = &interactiveState{quiet: true}
			e.interactiveStates[interactiveKey] = state
			e.interactiveMu.Unlock()
		} else {
			e.interactiveMu.Unlock()
			state.mu.Lock()
			state.quiet = !state.quiet
			state.mu.Unlock()
		}

	case "/switch":
		if args == "" {
			return
		}
		agent, sessions := e.sessionContextForKey(sessionKey)
		agentSessions, err := agent.ListSessions(e.ctx)
		if err != nil || len(agentSessions) == 0 {
			return
		}
		matched := e.matchSession(agentSessions, sessions, args)
		if matched == nil {
			return
		}
		interactiveKey := e.interactiveKeyForSessionKey(sessionKey)
		e.cleanupInteractiveState(interactiveKey)
		session := sessions.GetOrCreateActive(sessionKey)
		session.SetAgentInfo(matched.ID, agent.Name(), matched.Summary)
		session.ClearHistory()
		sessions.Save()

	case "/dir":
		fields := strings.Fields(args)
		if len(fields) == 0 {
			return
		}
		agent, sessions := e.sessionContextForKey(sessionKey)
		ik := e.interactiveKeyForSessionKey(sessionKey)
		var applyArgs []string
		switch fields[0] {
		case "select":
			if len(fields) < 2 {
				return
			}
			applyArgs = []string{fields[1]}
		case "reset":
			applyArgs = []string{"reset"}
		case "prev":
			applyArgs = []string{"-"}
		default:
			return
		}
		errMsg, _ := e.dirApply(agent, sessions, ik, sessionKey, applyArgs)
		if errMsg != "" {
			slog.Debug("dir card action failed", "message", errMsg)
		}

	case "/stop":
		sessionKey = e.interactiveKeyForSessionKey(sessionKey)
		e.interactiveMu.Lock()
		state, ok := e.interactiveStates[sessionKey]
		if !ok || state == nil {
			e.interactiveMu.Unlock()
			return
		}
		state.mu.Lock()
		pending := state.pending
		quietMode := state.quiet
		agentSession := state.agentSession
		if pending != nil {
			state.pending = nil
		}
		state.agentSession = nil
		state.mu.Unlock()
		if quietMode {
			e.interactiveStates[sessionKey] = &interactiveState{quiet: true}
		} else {
			delete(e.interactiveStates, sessionKey)
		}
		e.interactiveMu.Unlock()
		if pending != nil {
			pending.resolve()
		}
		if agentSession != nil {
			slog.Debug("cleanupInteractiveState: closing agent session", "session", sessionKey)
			go agentSession.Close()
		}

	case "/heartbeat":
		if e.heartbeatScheduler == nil {
			return
		}
		switch args {
		case "pause", "stop":
			e.heartbeatScheduler.Pause(e.name)
		case "resume", "start":
			e.heartbeatScheduler.Resume(e.name)
		case "run", "trigger":
			e.heartbeatScheduler.TriggerNow(e.name)
		}

	case "/cron":
		if e.cronScheduler == nil || args == "" {
			return
		}
		subArgs := strings.Fields(args)
		if len(subArgs) < 2 {
			return
		}
		sub, id := subArgs[0], subArgs[1]
		switch sub {
		case "enable":
			_ = e.cronScheduler.EnableJob(id)
		case "disable":
			_ = e.cronScheduler.DisableJob(id)
		case "delete":
			e.cronScheduler.RemoveJob(id)
		case "mute":
			e.cronScheduler.Store().SetMute(id, true)
		case "unmute":
			e.cronScheduler.Store().SetMute(id, false)
		}
	}
}

func (e *Engine) getOrCreateDeleteModeState(sessionKey string, p Platform, replyCtx any) *deleteModeState {
	interactiveKey := e.interactiveKeyForSessionKey(sessionKey)
	e.interactiveMu.Lock()
	state, ok := e.interactiveStates[interactiveKey]
	if !ok || state == nil {
		state = &interactiveState{platform: p, replyCtx: replyCtx}
		e.interactiveStates[interactiveKey] = state
	} else {
		state.platform = p
		state.replyCtx = replyCtx
	}
	e.interactiveMu.Unlock()

	state.mu.Lock()
	defer state.mu.Unlock()
	if state.deleteMode == nil {
		state.deleteMode = &deleteModeState{}
	}
	dm := state.deleteMode
	dm.page = 1
	dm.phase = "select"
	dm.hint = ""
	dm.result = ""
	dm.selectedIDs = make(map[string]struct{})
	return dm
}

func (e *Engine) getDeleteModeState(sessionKey string) *deleteModeState {
	interactiveKey := e.interactiveKeyForSessionKey(sessionKey)
	e.interactiveMu.Lock()
	state := e.interactiveStates[interactiveKey]
	e.interactiveMu.Unlock()
	if state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.deleteMode == nil {
		return nil
	}
	cp := &deleteModeState{
		page:        state.deleteMode.page,
		selectedIDs: make(map[string]struct{}, len(state.deleteMode.selectedIDs)),
		phase:       state.deleteMode.phase,
		hint:        state.deleteMode.hint,
		result:      state.deleteMode.result,
	}
	for id := range state.deleteMode.selectedIDs {
		cp.selectedIDs[id] = struct{}{}
	}
	return cp
}

func (e *Engine) renderDeleteModeCard(sessionKey string) *Card {
	agent, sessions := e.sessionContextForKey(sessionKey)
	agentSessions, err := e.listSessionsCached(agent)
	if err != nil {
		return e.simpleCard(e.i18n.T(MsgDeleteModeTitle), "red", err.Error())
	}
	dm := e.getDeleteModeState(sessionKey)
	if dm == nil {
		return e.simpleCard(e.i18n.T(MsgDeleteModeTitle), "red", e.i18n.T(MsgDeleteUsage))
	}
	switch dm.phase {
	case "confirm":
		return e.renderDeleteModeConfirmCard(sessions, dm, agentSessions)
	case "result":
		return e.renderDeleteModeResultCard(dm)
	default:
		return e.renderDeleteModeSelectCard(sessionKey, sessions, dm, agentSessions)
	}
}

func (e *Engine) renderDeleteModeSelectCard(sessionKey string, sessions *SessionManager, dm *deleteModeState, agentSessions []AgentSessionInfo) *Card {
	if len(agentSessions) == 0 {
		return e.simpleCard(e.i18n.T(MsgDeleteModeTitle), "red", e.i18n.T(MsgListEmpty))
	}
	total := len(agentSessions)
	totalPages := (total + listPageSize - 1) / listPageSize
	page := dm.page
	if page < 1 {
		page = 1
	}
	if page > totalPages {
		page = totalPages
	}
	start := (page - 1) * listPageSize
	end := start + listPageSize
	if end > total {
		end = total
	}

	cb := NewCard().Title(e.i18n.T(MsgDeleteModeTitle), "carmine")
	activeAgentID := sessions.GetOrCreateActive(sessionKey).GetAgentSessionID()
	selectedCount := 0
	for i := start; i < end; i++ {
		s := agentSessions[i]
		isActive := activeAgentID == s.ID
		isSelected := false
		if !isActive {
			_, isSelected = dm.selectedIDs[s.ID]
		}
		marker := "◻"
		if isActive {
			marker = "▶"
		} else if isSelected {
			marker = "☑"
			selectedCount++
		}
		btnText := e.i18n.T(MsgDeleteModeSelect)
		btnType := "default"
		action := fmt.Sprintf("act:/delete-mode toggle %s", s.ID)
		if isActive {
			btnText = e.i18n.T(MsgCardTitleCurrentSession)
			btnType = "primary"
			action = fmt.Sprintf("act:/delete-mode noop %s", s.ID)
		} else if isSelected {
			btnText = e.i18n.T(MsgDeleteModeSelected)
			btnType = "primary"
		}
		cb.ListItemBtn(
			e.i18n.Tf(MsgListItem, marker, i+1, e.deleteSessionDisplayName(sessions, &s), s.MessageCount, s.ModifiedAt.Format("01-02 15:04")),
			btnText,
			btnType,
			action,
		)
	}
	cb.TaggedNote("delete-mode-selected-count", e.i18n.Tf(MsgDeleteModeSelectedCount, selectedCount))
	if dm.hint != "" {
		cb.Note(dm.hint)
	}
	cb.Buttons(
		DangerBtn(e.i18n.T(MsgDeleteModeDeleteSelected), "act:/delete-mode confirm"),
		DefaultBtn(e.i18n.T(MsgDeleteModeCancel), "act:/delete-mode cancel"),
	)

	var navBtns []CardButton
	if page > 1 {
		navBtns = append(navBtns, DefaultBtn(e.i18n.T(MsgCardPrev), fmt.Sprintf("act:/delete-mode page %d", page-1)))
	}
	if page < totalPages {
		navBtns = append(navBtns, DefaultBtn(e.i18n.T(MsgCardNext), fmt.Sprintf("act:/delete-mode page %d", page+1)))
	}
	if len(navBtns) > 0 {
		cb.Buttons(navBtns...)
	}
	return cb.Build()
}

func (e *Engine) renderDeleteModeConfirmCard(sessions *SessionManager, dm *deleteModeState, agentSessions []AgentSessionInfo) *Card {
	selectedNames := e.deleteModeSelectionNames(sessions, dm, agentSessions)
	body := strings.Join(selectedNames, "\n")
	if body == "" {
		body = e.i18n.T(MsgDeleteModeEmptySelection)
	}
	return NewCard().
		Title(e.i18n.T(MsgDeleteModeConfirmTitle), "carmine").
		Markdown(body).
		Buttons(
			DangerBtn(e.i18n.T(MsgDeleteModeConfirmButton), "act:/delete-mode submit"),
			DefaultBtn(e.i18n.T(MsgDeleteModeBackButton), "act:/delete-mode back"),
		).
		Build()
}

func (e *Engine) renderDeleteModeResultCard(dm *deleteModeState) *Card {
	return NewCard().
		Title(e.i18n.T(MsgDeleteModeResultTitle), "turquoise").
		Markdown(dm.result).
		Buttons(DefaultBtn(e.i18n.T(MsgCardBack), "nav:/list 1")).
		Build()
}

func (e *Engine) deleteModeSelectionNames(sessions *SessionManager, dm *deleteModeState, agentSessions []AgentSessionInfo) []string {
	names := make([]string, 0, len(dm.selectedIDs))
	for i := range agentSessions {
		if _, ok := dm.selectedIDs[agentSessions[i].ID]; ok {
			names = append(names, "- "+e.deleteSessionDisplayName(sessions, &agentSessions[i]))
		}
	}
	return names
}

func (e *Engine) executeDeleteModeAction(sessionKey, args string) {
	interactiveKey := e.interactiveKeyForSessionKey(sessionKey)
	e.interactiveMu.Lock()
	state := e.interactiveStates[interactiveKey]
	e.interactiveMu.Unlock()
	if state == nil {
		return
	}

	fields := strings.Fields(args)
	if len(fields) == 0 {
		return
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if state.deleteMode == nil {
		return
	}

	dm := state.deleteMode
	switch fields[0] {
	case "toggle":
		if len(fields) < 2 {
			return
		}
		id := fields[1]
		if _, ok := dm.selectedIDs[id]; ok {
			delete(dm.selectedIDs, id)
		} else {
			dm.selectedIDs[id] = struct{}{}
		}
		dm.phase = "select"
		dm.hint = ""
	case "page":
		if len(fields) < 2 {
			return
		}
		if n, err := strconv.Atoi(fields[1]); err == nil && n > 0 {
			dm.page = n
		}
		dm.phase = "select"
	case "confirm":
		if len(dm.selectedIDs) == 0 {
			dm.phase = "select"
			dm.hint = e.i18n.T(MsgDeleteModeEmptySelection)
			return
		}
		dm.phase = "confirm"
		dm.hint = ""
	case "back":
		dm.phase = "select"
	case "submit":
		lines := e.submitDeleteModeSelection(sessionKey, dm)
		dm.selectedIDs = make(map[string]struct{})
		dm.result = strings.Join(lines, "\n")
		dm.hint = ""
		dm.phase = "result"
	case "form-submit":
		dm.selectedIDs = parseDeleteModeSelectedIDs(fields[1:])
		if len(dm.selectedIDs) == 0 {
			dm.phase = "select"
			dm.hint = e.i18n.T(MsgDeleteModeEmptySelection)
			return
		}
		dm.phase = "confirm"
		dm.hint = ""
	case "cancel":
		state.deleteMode = nil
	}
}

func parseDeleteModeSelectedIDs(args []string) map[string]struct{} {
	ids := make(map[string]struct{})
	for _, arg := range args {
		for _, id := range strings.Split(arg, ",") {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			ids[id] = struct{}{}
		}
	}
	return ids
}

func (e *Engine) submitDeleteModeSelection(sessionKey string, dm *deleteModeState) []string {
	agent, _ := e.sessionContextForKey(sessionKey)
	deleter, ok := agent.(SessionDeleter)
	if !ok {
		return []string{e.i18n.T(MsgDeleteNotSupported)}
	}
	agentSessions, err := agent.ListSessions(e.ctx)
	if err != nil {
		return []string{e.i18n.Tf(MsgError, err)}
	}
	seen := make(map[string]struct{}, len(agentSessions))
	lines := make([]string, 0, len(dm.selectedIDs))
	for i := range agentSessions {
		seen[agentSessions[i].ID] = struct{}{}
		if _, ok := dm.selectedIDs[agentSessions[i].ID]; !ok {
			continue
		}
		if line := e.deleteSingleSessionReply(&Message{SessionKey: sessionKey}, deleter, &agentSessions[i]); line != "" {
			lines = append(lines, line)
		}
	}
	missingIDs := make([]string, 0)
	for id := range dm.selectedIDs {
		if _, ok := seen[id]; ok {
			continue
		}
		missingIDs = append(missingIDs, id)
	}
	sort.Strings(missingIDs)
	for _, id := range missingIDs {
		lines = append(lines, fmt.Sprintf(e.i18n.T(MsgDeleteModeMissingSession), id))
	}
	if len(lines) == 0 {
		lines = append(lines, e.i18n.T(MsgDeleteModeEmptySelection))
	}
	// Invalidate cached session list after batch deletion.
	e.invalidateSessionListCache(agent)
	return lines
}

func (e *Engine) renderLangCard() *Card {
	cur := e.i18n.CurrentLang()
	name := langDisplayName(cur)

	langs := []struct{ code, label string }{
		{"en", "English"}, {"zh", "中文"}, {"zh-TW", "繁體中文"},
		{"ja", "日本語"}, {"es", "Español"}, {"auto", "Auto"},
	}
	var opts []CardSelectOption
	initVal := ""
	for _, l := range langs {
		opts = append(opts, CardSelectOption{Text: l.label, Value: "act:/lang " + l.code})
		if string(cur) == l.code || (cur == LangAuto && l.code == "auto") {
			initVal = "act:/lang " + l.code
		}
	}

	return NewCard().
		Title(e.i18n.T(MsgCardTitleLanguage), "wathet").
		Markdown(e.i18n.Tf(MsgLangCurrent, name)).
		Select(e.i18n.T(MsgLangSelectPlaceholder), opts, initVal).
		Buttons(e.cardBackButton()).
		Build()
}

func (e *Engine) renderModelCard() *Card {
	switcher, ok := e.agent.(ModelSwitcher)
	if !ok {
		return e.simpleCard(e.i18n.T(MsgCardTitleModel), "indigo", e.i18n.T(MsgModelNotSupported))
	}

	fetchCtx, cancel := context.WithTimeout(e.ctx, 3*time.Second)
	defer cancel()
	models := switcher.AvailableModels(fetchCtx)
	current := switcher.GetModel()

	var sb strings.Builder
	if current == "" {
		sb.WriteString(e.i18n.T(MsgModelDefault))
	} else {
		sb.WriteString(e.i18n.Tf(MsgModelCurrent, current))
	}

	var opts []CardSelectOption
	initVal := ""
	for i, m := range models {
		label := m.Name
		if m.Alias != "" {
			label = m.Alias + " - " + m.Name
		} else if m.Desc != "" {
			label += " — " + m.Desc
		}
		val := fmt.Sprintf("act:/model switch %d", i+1)
		opts = append(opts, CardSelectOption{Text: label, Value: val})
		if m.Name == current {
			initVal = val
		}
	}

	cb := NewCard().Title(e.i18n.T(MsgCardTitleModel), "indigo").
		Markdown(sb.String()).
		Select(e.i18n.T(MsgModelSelectPlaceholder), opts, initVal).
		Buttons(e.cardBackButton())
	cb.Note(e.i18n.T(MsgModelUsage))
	return cb.Build()
}

func (e *Engine) renderReasoningCard() *Card {
	switcher, ok := e.agent.(ReasoningEffortSwitcher)
	if !ok {
		return e.simpleCard(e.i18n.T(MsgCardTitleReasoning), "orange", e.i18n.T(MsgReasoningNotSupported))
	}

	efforts := switcher.AvailableReasoningEfforts()
	current := switcher.GetReasoningEffort()

	var sb strings.Builder
	if current == "" {
		sb.WriteString(e.i18n.T(MsgReasoningDefault))
	} else {
		sb.WriteString(e.i18n.Tf(MsgReasoningCurrent, current))
	}

	var opts []CardSelectOption
	initVal := ""
	for i, effort := range efforts {
		val := fmt.Sprintf("act:/reasoning %d", i+1)
		opts = append(opts, CardSelectOption{Text: effort, Value: val})
		if effort == current {
			initVal = val
		}
	}

	cb := NewCard().Title(e.i18n.T(MsgCardTitleReasoning), "orange").
		Markdown(sb.String()).
		Select(e.i18n.T(MsgReasoningSelectPlaceholder), opts, initVal).
		Buttons(e.cardBackButton())
	cb.Note(e.i18n.T(MsgReasoningUsage))
	return cb.Build()
}

func (e *Engine) renderModeCard() *Card {
	switcher, ok := e.agent.(ModeSwitcher)
	if !ok {
		return e.simpleCard(e.i18n.T(MsgCardTitleMode), "violet", e.i18n.T(MsgModeNotSupported))
	}

	current := switcher.GetMode()
	modes := switcher.PermissionModes()
	zhLike := e.i18n.IsZhLike()

	var sb strings.Builder
	for _, m := range modes {
		marker := "◻"
		if m.Key == current {
			marker = "▶"
		}
		if zhLike {
			sb.WriteString(fmt.Sprintf("%s **%s** — %s\n", marker, m.NameZh, m.DescZh))
		} else {
			sb.WriteString(fmt.Sprintf("%s **%s** — %s\n", marker, m.Name, m.Desc))
		}
	}

	var opts []CardSelectOption
	initVal := ""
	for _, m := range modes {
		label := m.Name
		if zhLike {
			label = m.NameZh
		}
		val := "act:/mode " + m.Key
		opts = append(opts, CardSelectOption{Text: label, Value: val})
		if m.Key == current {
			initVal = val
		}
	}

	cb := NewCard().Title(e.i18n.T(MsgCardTitleMode), "violet").
		Markdown(sb.String()).
		Select(e.i18n.T(MsgModeSelectPlaceholder), opts, initVal).
		Buttons(e.cardBackButton())
	cb.Note(e.i18n.T(MsgModeUsage))
	return cb.Build()
}

func (e *Engine) renderListCard(sessionKey string, page int) (*Card, error) {
	agent, sessions := e.sessionContextForKey(sessionKey)
	agentSessions, err := e.listSessionsCached(agent)
	if err != nil {
		return nil, fmt.Errorf(e.i18n.T(MsgListError), err)
	}
	if len(agentSessions) == 0 {
		return e.simpleCard(e.i18n.Tf(MsgCardTitleSessions, agent.Name(), 0), "turquoise", e.i18n.T(MsgListEmpty)), nil
	}

	total := len(agentSessions)
	totalPages := (total + listPageSize - 1) / listPageSize
	if page > totalPages {
		page = totalPages
	}

	start := (page - 1) * listPageSize
	end := start + listPageSize
	if end > total {
		end = total
	}

	agentName := agent.Name()
	activeSession := sessions.GetOrCreateActive(sessionKey)
	activeAgentID := activeSession.GetAgentSessionID()

	var titleStr string
	if totalPages > 1 {
		titleStr = e.i18n.Tf(MsgCardTitleSessionsPaged, agentName, total, page, totalPages)
	} else {
		titleStr = e.i18n.Tf(MsgCardTitleSessions, agentName, total)
	}

	cb := NewCard().Title(titleStr, "turquoise")
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
				displayName = e.i18n.T(MsgListEmptySummary)
			}
			if len([]rune(displayName)) > 40 {
				displayName = string([]rune(displayName)[:40]) + "…"
			}
		}
		btnType := "default"
		if s.ID == activeAgentID {
			btnType = "primary"
		}
		cb.ListItemBtn(
			e.i18n.Tf(MsgListItem, marker, i+1, displayName, s.MessageCount, s.ModifiedAt.Format("01-02 15:04")),
			fmt.Sprintf("#%d", i+1),
			btnType,
			fmt.Sprintf("act:/switch %d", i+1),
		)
	}

	var navBtns []CardButton
	if page > 1 {
		navBtns = append(navBtns, e.cardPrevButton(fmt.Sprintf("nav:/list %d", page-1)))
	}
	navBtns = append(navBtns, e.cardBackButton())
	if page < totalPages {
		navBtns = append(navBtns, e.cardNextButton(fmt.Sprintf("nav:/list %d", page+1)))
	}
	cb.Buttons(navBtns...)

	if totalPages > 1 {
		cb.Note(fmt.Sprintf(e.i18n.T(MsgListPageHint), page, totalPages))
	}

	return cb.Build(), nil
}

// dirCardTruncPath shortens absolute paths for card list rows.
func dirCardTruncPath(absPath string) string {
	r := []rune(absPath)
	if len(r) <= 56 {
		return absPath
	}
	return string(r[:53]) + "…"
}

func (e *Engine) renderDirCard(sessionKey string, page int) (*Card, error) {
	agent, _ := e.sessionContextForKey(sessionKey)
	switcher, ok := agent.(WorkDirSwitcher)
	if !ok {
		return nil, fmt.Errorf("%s", e.i18n.T(MsgDirNotSupported))
	}
	currentDir := switcher.GetWorkDir()
	var history []string
	if e.dirHistory != nil {
		history = e.dirHistory.List(e.name)
	}
	total := len(history)
	totalPages := 1
	if total > 0 {
		totalPages = (total + dirCardPageSize - 1) / dirCardPageSize
	}
	if page < 1 {
		page = 1
	}
	if page > totalPages {
		page = totalPages
	}
	start := (page - 1) * dirCardPageSize
	end := start + dirCardPageSize
	if end > total {
		end = total
	}

	cb := NewCard().Title(e.i18n.T(MsgDirCardTitle), "turquoise")
	cb.Markdown(e.i18n.Tf(MsgDirCurrent, currentDir))
	if total == 0 {
		cb.Note(e.i18n.T(MsgDirCardEmptyHistory))
	} else {
		cb.Divider()
		for i := start; i < end; i++ {
			dir := history[i]
			marker := "◻"
			if dir == currentDir {
				marker = "▶"
			}
			btnType := "default"
			if dir == currentDir {
				btnType = "primary"
			}
			displayPath := dirCardTruncPath(dir)
			cb.ListItemBtn(
				fmt.Sprintf("%s **%d.** `%s`", marker, i+1, displayPath),
				fmt.Sprintf("#%d", i+1),
				btnType,
				fmt.Sprintf("act:/dir select %d", i+1),
			)
		}
	}

	var actionRow []CardButton
	if e.dirHistory != nil && len(history) >= 2 {
		actionRow = append(actionRow, DefaultBtn(e.i18n.T(MsgDirCardPrev), "act:/dir prev"))
	}
	actionRow = append(actionRow, DefaultBtn(e.i18n.T(MsgDirCardReset), "act:/dir reset"))
	cb.Buttons(actionRow...)

	var navBtns []CardButton
	if totalPages > 1 && page > 1 {
		navBtns = append(navBtns, e.cardPrevButton(fmt.Sprintf("nav:/dir %d", page-1)))
	}
	navBtns = append(navBtns, e.cardBackButton())
	if totalPages > 1 && page < totalPages {
		navBtns = append(navBtns, e.cardNextButton(fmt.Sprintf("nav:/dir %d", page+1)))
	}
	cb.Buttons(navBtns...)

	if totalPages > 1 {
		cb.Note(fmt.Sprintf(e.i18n.T(MsgDirCardPageHint), page, totalPages))
	}

	return cb.Build(), nil
}

// ──────────────────────────────────────────────────────────────
// Navigable sub-cards (for in-place card updates)
// ──────────────────────────────────────────────────────────────

func (e *Engine) renderCurrentCard(sessionKey string) *Card {
	_, sessions := e.sessionContextForKey(sessionKey)
	s := sessions.GetOrCreateActive(sessionKey)
	agentID := s.GetAgentSessionID()
	if agentID == "" {
		agentID = e.i18n.T(MsgSessionNotStarted)
	}
	content := fmt.Sprintf(e.i18n.T(MsgCurrentSession), s.Name, agentID, len(s.History))
	return NewCard().
		Title(e.i18n.T(MsgCardTitleCurrentSession), "turquoise").
		Markdown(content).
		Buttons(e.cardBackButton()).
		Build()
}

func (e *Engine) renderHistoryCard(sessionKey string) *Card {
	agent, sessions := e.sessionContextForKey(sessionKey)
	s := sessions.GetOrCreateActive(sessionKey)
	entries := s.GetHistory(10)

	agentSID := s.GetAgentSessionID()
	if len(entries) == 0 && agentSID != "" {
		if hp, ok := agent.(HistoryProvider); ok {
			if agentEntries, err := hp.GetSessionHistory(e.ctx, agentSID, 10); err == nil {
				entries = agentEntries
			}
		}
	}

	if len(entries) == 0 {
		return e.simpleCard(e.i18n.T(MsgCardTitleHistory), "turquoise", e.i18n.T(MsgHistoryEmpty))
	}

	var sb strings.Builder
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

	return NewCard().
		Title(e.i18n.Tf(MsgCardTitleHistoryLast, len(entries)), "turquoise").
		Markdown(sb.String()).
		Buttons(e.cardBackButton()).
		Build()
}

func (e *Engine) renderProviderCard() *Card {
	switcher, ok := e.agent.(ProviderSwitcher)
	if !ok {
		return e.simpleCard(e.i18n.T(MsgCardTitleProvider), "indigo", e.i18n.T(MsgProviderNotSupported))
	}

	current := switcher.GetActiveProvider()
	providers := switcher.ListProviders()

	if current == nil && len(providers) == 0 {
		return e.simpleCard(e.i18n.T(MsgCardTitleProvider), "indigo", e.i18n.T(MsgProviderNone))
	}

	var body strings.Builder
	if current != nil {
		body.WriteString(fmt.Sprintf(e.i18n.T(MsgProviderCurrent), current.Name))
		body.WriteString("\n\n")
	}

	cb := NewCard().Title(e.i18n.T(MsgCardTitleProvider), "indigo").Markdown(body.String())
	if len(providers) > 0 {
		var opts []CardSelectOption
		initVal := ""
		for _, prov := range providers {
			label := prov.Name
			if prov.BaseURL != "" {
				label += " (" + prov.BaseURL + ")"
			}
			val := "act:/provider " + prov.Name
			opts = append(opts, CardSelectOption{Text: label, Value: val})
			if current != nil && prov.Name == current.Name {
				initVal = val
			}
		}
		cb.Select(e.i18n.T(MsgProviderSelectPlaceholder), opts, initVal)
	}
	return cb.Buttons(e.cardBackButton()).Build()
}

func (e *Engine) renderCronCard(sessionKey string, userID string) *Card {
	if e.cronScheduler == nil {
		return e.simpleCard(e.i18n.T(MsgCardTitleCron), "orange", e.i18n.T(MsgCronNotAvailable))
	}

	jobs := e.cronScheduler.Store().ListBySessionKey(sessionKey)
	if len(jobs) == 0 {
		return e.simpleCard(e.i18n.T(MsgCardTitleCron), "orange", e.i18n.T(MsgCronEmpty))
	}

	lang := e.i18n.CurrentLang()
	now := time.Now()

	cb := NewCard().Title(e.i18n.T(MsgCardTitleCron), "orange")
	cb.Markdown(fmt.Sprintf(e.i18n.T(MsgCronListTitle), len(jobs)))

	for _, j := range jobs {
		status := "✅"
		if !j.Enabled {
			status = "⏸"
		}

		desc := j.Description
		if desc == "" {
			if j.IsShellJob() {
				desc = "🖥 " + truncateStr(j.Exec, 60)
			} else {
				desc = truncateStr(j.Prompt, 60)
			}
		}
		if j.Mute {
			desc += " [mute]"
		}

		human := CronExprToHuman(j.CronExpr, lang)

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("%s %s\n", status, desc))
		sb.WriteString(e.i18n.Tf(MsgCronIDLabel, j.ID))
		sb.WriteString(e.i18n.Tf(MsgCronScheduleLabel, human, j.CronExpr))
		nextRun := e.cronScheduler.NextRun(j.ID)
		if !nextRun.IsZero() {
			fmtStr := cronTimeFormat(nextRun, now)
			sb.WriteString(e.i18n.Tf(MsgCronNextRunLabel, nextRun.Format(fmtStr)))
		}
		if !j.LastRun.IsZero() {
			fmtStr := cronTimeFormat(j.LastRun, now)
			sb.WriteString(e.i18n.Tf(MsgCronLastRunLabel, j.LastRun.Format(fmtStr)))
			if j.LastError != "" {
				sb.WriteString(e.i18n.Tf(MsgCronFailedSuffix, truncateStr(j.LastError, 40)))
			}
			sb.WriteString("\n")
		}
		cb.Markdown(sb.String())

		var btns []CardButton
		if j.Enabled {
			btns = append(btns, DefaultBtn(e.i18n.T(MsgCronBtnDisable), fmt.Sprintf("act:/cron disable %s", j.ID)))
		} else {
			btns = append(btns, PrimaryBtn(e.i18n.T(MsgCronBtnEnable), fmt.Sprintf("act:/cron enable %s", j.ID)))
		}
		if j.Mute {
			btns = append(btns, DefaultBtn(e.i18n.T(MsgCronBtnUnmute), fmt.Sprintf("act:/cron unmute %s", j.ID)))
		} else {
			btns = append(btns, DefaultBtn(e.i18n.T(MsgCronBtnMute), fmt.Sprintf("act:/cron mute %s", j.ID)))
		}
		btns = append(btns, DangerBtn(e.i18n.T(MsgCronBtnDelete), fmt.Sprintf("act:/cron delete %s", j.ID)))
		cb.ButtonsEqual(btns...)
	}

	cb.Divider()
	cb.Note(e.i18n.T(MsgCronCardHint))
	cb.Buttons(e.cardBackButton())
	return cb.Build()
}

func (e *Engine) renderCommandsCard() *Card {
	cmds := e.commands.ListAll()
	if len(cmds) == 0 {
		return e.simpleCard(e.i18n.T(MsgCardTitleCommands), "purple", e.i18n.T(MsgCommandsEmpty))
	}

	var sb strings.Builder
	sb.WriteString(e.i18n.Tf(MsgCommandsTitle, len(cmds)))
	for _, c := range cmds {
		tag := ""
		if c.Source == "agent" {
			tag = e.i18n.T(MsgCommandsTagAgent)
		} else if c.Exec != "" {
			tag = e.i18n.T(MsgCommandsTagShell)
		}
		desc := c.Description
		if desc == "" {
			if c.Exec != "" {
				desc = "$ " + truncateStr(c.Exec, 60)
			} else {
				desc = truncateStr(c.Prompt, 60)
			}
		}
		sb.WriteString(fmt.Sprintf("/%s%s — %s\n", c.Name, tag, desc))
	}

	return NewCard().Title(e.i18n.T(MsgCardTitleCommands), "purple").
		Markdown(sb.String()).
		Note(e.i18n.T(MsgCommandsHint)).
		Buttons(e.cardBackButton()).
		Build()
}

func (e *Engine) renderAliasCard() *Card {
	e.aliasMu.RLock()
	defer e.aliasMu.RUnlock()

	if len(e.aliases) == 0 {
		return e.simpleCard(e.i18n.T(MsgCardTitleAlias), "purple", e.i18n.T(MsgAliasEmpty))
	}

	names := make([]string, 0, len(e.aliases))
	for n := range e.aliases {
		names = append(names, n)
	}
	sort.Strings(names)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(e.i18n.T(MsgAliasListHeader), len(e.aliases)))
	sb.WriteString("\n")
	for _, n := range names {
		sb.WriteString(fmt.Sprintf("`%s` → `%s`\n", n, e.aliases[n]))
	}

	return NewCard().Title(e.i18n.T(MsgCardTitleAlias), "purple").
		Markdown(sb.String()).
		Buttons(e.cardBackButton()).
		Build()
}

func (e *Engine) renderConfigCard() *Card {
	items := e.configItems()
	isZh := e.i18n.IsZhLike()

	var sb strings.Builder
	sb.WriteString(e.i18n.T(MsgConfigTitle))
	for _, item := range items {
		sb.WriteString(fmt.Sprintf("`%s` = `%s`\n  %s\n\n", item.key, item.getFunc(), item.description(isZh)))
	}

	return NewCard().Title(e.i18n.T(MsgCardTitleConfig), "grey").
		Markdown(sb.String()).
		Note(e.i18n.T(MsgConfigHint)).
		Buttons(e.cardBackButton()).
		Build()
}

func (e *Engine) renderSkillsCard() *Card {
	skills := e.skills.ListAll()
	if len(skills) == 0 {
		return e.simpleCard(e.i18n.T(MsgCardTitleSkills), "purple", e.i18n.T(MsgSkillsEmpty))
	}

	var sb strings.Builder
	sb.WriteString(e.i18n.Tf(MsgSkillsTitle, e.agent.Name(), len(skills)))
	for _, s := range skills {
		sb.WriteString(fmt.Sprintf("  /%s — %s\n", s.Name, s.Description))
	}

	return NewCard().Title(e.i18n.T(MsgCardTitleSkills), "purple").
		Markdown(sb.String()).
		Note(e.i18n.T(MsgSkillsHint)).
		Buttons(e.cardBackButton()).
		Build()
}

func (e *Engine) renderDoctorCard() *Card {
	results := RunDoctorChecks(e.ctx, e.agent, e.platforms, e.chatStore)
	report := FormatDoctorResults(results, e.i18n)
	return NewCard().
		Title(e.i18n.T(MsgCardTitleDoctor), "orange").
		Markdown(report).
		Buttons(e.cardBackButton()).
		Build()
}

func (e *Engine) renderVersionCard() *Card {
	return NewCard().
		Title(e.i18n.T(MsgCardTitleVersion), "grey").
		Markdown(VersionInfo).
		Buttons(e.cardBackButton()).
		Build()
}

func (e *Engine) renderUpgradeCard() *Card {
	title := e.i18n.T(MsgCardTitleUpgrade)
	cur := CurrentVersion
	if cur == "" || cur == "dev" {
		return e.simpleCard(title, "grey", e.i18n.T(MsgUpgradeDevBuild))
	}

	type result struct {
		release *ReleaseInfo
		err     error
	}
	ch := make(chan result, 1)
	useGitee := e.i18n.IsZhLike()
	go func() {
		r, err := CheckForUpdate(cur, useGitee)
		ch <- result{r, err}
	}()

	var content string
	select {
	case res := <-ch:
		if res.err != nil {
			content = e.i18n.Tf(MsgError, res.err)
		} else if res.release == nil {
			content = fmt.Sprintf(e.i18n.T(MsgUpgradeUpToDate), cur)
		} else {
			body := res.release.Body
			if len([]rune(body)) > 300 {
				body = string([]rune(body)[:300]) + "…"
			}
			content = fmt.Sprintf(e.i18n.T(MsgUpgradeAvailable), cur, res.release.TagName, body)
		}
	case <-time.After(8 * time.Second):
		content = "⏱ " + e.i18n.T(MsgUpgradeChecking) + e.i18n.T(MsgUpgradeTimeoutSuffix)
	}

	return NewCard().
		Title(title, "grey").
		Markdown(content).
		Buttons(e.cardBackButton()).
		Build()
}

func (e *Engine) renderHeartbeatCard() *Card {
	if e.heartbeatScheduler == nil {
		return e.simpleCard(e.i18n.T(MsgCardTitleHeartbeat), "purple", e.i18n.T(MsgHeartbeatNotAvailable))
	}
	st := e.heartbeatScheduler.Status(e.name)
	if st == nil {
		return e.simpleCard(e.i18n.T(MsgCardTitleHeartbeat), "purple", e.i18n.T(MsgHeartbeatNotAvailable))
	}

	stateStr, yesNo := e.heartbeatLocalizedHelpers()
	lang := e.i18n.CurrentLang()

	lastRunStr := ""
	if !st.LastRun.IsZero() {
		switch lang {
		case LangChinese, LangTraditionalChinese:
			lastRunStr = "上次执行: " + st.LastRun.Format("01-02 15:04:05") + "\n"
		case LangJapanese:
			lastRunStr = "最終実行: " + st.LastRun.Format("01-02 15:04:05") + "\n"
		default:
			lastRunStr = "Last run: " + st.LastRun.Format("01-02 15:04:05") + "\n"
		}
		if st.LastError != "" {
			lastRunStr += "⚠️ " + truncateStr(st.LastError, 80) + "\n"
		}
	}

	body := fmt.Sprintf(e.i18n.T(MsgHeartbeatStatus),
		stateStr(st.Paused),
		st.IntervalMins,
		yesNo(st.OnlyWhenIdle),
		yesNo(st.Silent),
		st.RunCount,
		st.ErrorCount,
		st.SkippedBusy,
		lastRunStr,
	)

	cb := NewCard().Title(e.i18n.T(MsgCardTitleHeartbeat), "purple").Markdown(body)

	var actionBtns []CardButton
	if st.Paused {
		actionBtns = append(actionBtns, PrimaryBtn("▶️ Resume", "act:/heartbeat resume"))
	} else {
		actionBtns = append(actionBtns, DefaultBtn("⏸ Pause", "act:/heartbeat pause"))
	}
	actionBtns = append(actionBtns, DefaultBtn("💓 Run Now", "act:/heartbeat run"))
	cb.Buttons(actionBtns...)

	cb.Buttons(e.cardBackButton())

	return cb.Build()
}

func (e *Engine) renderWhoamiCard(msg *Message) *Card {
	userID := msg.UserID
	if userID == "" {
		userID = "(unknown)"
	}

	var body strings.Builder
	body.WriteString(fmt.Sprintf("**User ID:**  `%s`\n", userID))
	if msg.UserName != "" {
		body.WriteString(fmt.Sprintf("**%s:**  %s\n", e.i18n.T(MsgWhoamiName), msg.UserName))
	}
	if msg.Platform != "" {
		body.WriteString(fmt.Sprintf("**%s:**  %s\n", e.i18n.T(MsgWhoamiPlatform), msg.Platform))
	}
	chatID := extractChannelID(msg.SessionKey)
	if chatID != "" {
		body.WriteString(fmt.Sprintf("**Chat ID:**  `%s`\n", chatID))
	}
	body.WriteString(fmt.Sprintf("**Session Key:**  `%s`\n", msg.SessionKey))

	return NewCard().
		Title(e.i18n.T(MsgWhoamiCardTitle), "blue").
		Markdown(body.String()).
		Divider().
		Note(e.i18n.T(MsgWhoamiUsage)).
		Buttons(e.cardBackButton()).
		Build()
}
