package core

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (e *Engine) handleWorkspaceCommand(p Platform, msg *Message, args []string) {
	channelID := extractChannelID(msg.SessionKey)
	channelKey := extractWorkspaceChannelKey(msg.SessionKey)
	projectKey := "project:" + e.name
	resolveChannelName := func() func() string {
		resolved := false
		channelName := ""
		return func() string {
			if resolved {
				return channelName
			}
			resolved = true
			if resolver, ok := p.(ChannelNameResolver); ok {
				channelName, _ = resolver.ResolveChannelName(channelID)
			}
			return channelName
		}
	}()
	replyWorkspaceInfo := func(b *WorkspaceBinding, bindingKey string) {
		if bindingKey == sharedWorkspaceBindingsKey {
			e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsInfoShared, b.Workspace, b.BoundAt.Format(time.RFC3339)))
			return
		}
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsInfo, b.Workspace, b.BoundAt.Format(time.RFC3339)))
	}
	routeWorkspace := func(bindingKey string, pathParts []string, usageKey, successKey MsgKey) bool {
		routePath := strings.TrimSpace(strings.Join(pathParts, " "))
		if routePath == "" {
			e.reply(p, msg.ReplyCtx, e.i18n.T(usageKey))
			return false
		}
		if !filepath.IsAbs(routePath) {
			e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsRouteAbsoluteRequired, routePath))
			return false
		}

		info, err := os.Stat(routePath)
		if err != nil {
			if os.IsNotExist(err) {
				e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsRouteNotFound, routePath))
			} else {
				e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsResolutionError, err))
			}
			return false
		}
		if !info.IsDir() {
			e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsRouteNotDirectory, routePath))
			return false
		}

		normalizedPath := normalizeWorkspacePath(routePath)
		e.workspaceBindings.Bind(bindingKey, channelKey, resolveChannelName(), normalizedPath)
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(successKey, normalizedPath))
		return true
	}
	bindWorkspace := func(bindingKey, wsName string, successKey MsgKey) bool {
		wsPath := filepath.Join(e.baseDir, wsName)

		// Check if workspace directory exists
		if _, err := os.Stat(wsPath); os.IsNotExist(err) {
			e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsBindNotFound, wsName))
			return false
		}

		e.workspaceBindings.Bind(bindingKey, channelKey, resolveChannelName(), normalizeWorkspacePath(wsPath))
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(successKey, wsName))
		return true
	}
	initWorkspace := func(bindingKey, repoURL string, successKey MsgKey) bool {
		if !looksLikeGitURL(repoURL) {
			e.reply(p, msg.ReplyCtx, "That doesn't look like a git URL.")
			return false
		}

		repoName := extractRepoName(repoURL)
		cloneTo := filepath.Join(e.baseDir, repoName)

		if _, err := os.Stat(cloneTo); err == nil {
			e.workspaceBindings.Bind(bindingKey, channelKey, resolveChannelName(), normalizeWorkspacePath(cloneTo))
			e.reply(p, msg.ReplyCtx, e.i18n.Tf(successKey, cloneTo))
			return true
		}

		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsCloneProgress, repoURL))

		if err := gitClone(repoURL, cloneTo); err != nil {
			e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsCloneFailed, err))
			return false
		}

		e.workspaceBindings.Bind(bindingKey, channelKey, resolveChannelName(), normalizeWorkspacePath(cloneTo))
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(successKey, cloneTo))
		return true
	}
	listBindings := func(bindingKey string, emptyKey, titleKey MsgKey) {
		bindings := e.workspaceBindings.ListByProject(bindingKey)
		if len(bindings) == 0 {
			e.reply(p, msg.ReplyCtx, e.i18n.T(emptyKey))
			return
		}
		var sb strings.Builder
		sb.WriteString(e.i18n.T(titleKey) + "\n")
		for chID, b := range bindings {
			name := b.ChannelName
			if name == "" {
				name = chID
			}
			sb.WriteString(fmt.Sprintf("• #%s → `%s`\n", name, b.Workspace))
		}
		e.reply(p, msg.ReplyCtx, sb.String())
	}

	subCmd := ""
	if len(args) > 0 {
		subCmd = matchSubCommand(args[0], []string{"init", "bind", "route", "unbind", "list", "shared"})
	}

	switch subCmd {
	case "":
		b, bindingKey, usable := e.lookupEffectiveWorkspaceBinding(channelKey)
		if !usable {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsNoBinding))
		} else {
			replyWorkspaceInfo(b, bindingKey)
		}

	case "bind":
		if len(args) < 2 {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsBindUsage))
			return
		}
		bindWorkspace(projectKey, args[1], MsgWsBindSuccess)

	case "route":
		if len(args) < 2 {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsRouteUsage))
			return
		}
		routeWorkspace(projectKey, args[1:], MsgWsRouteUsage, MsgWsRouteSuccess)

	case "init":
		if len(args) < 2 {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsInitUsage))
			return
		}
		initWorkspace(projectKey, args[1], MsgWsCloneSuccess)

	case "shared":
		sharedSubCmd := ""
		if len(args) > 1 {
			sharedSubCmd = matchSubCommand(args[1], []string{"init", "bind", "route", "unbind", "list"})
		}
		switch sharedSubCmd {
		case "":
			b := e.workspaceBindings.Lookup(sharedWorkspaceBindingsKey, channelKey)
			if b == nil {
				e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsSharedNoBinding))
			} else {
				replyWorkspaceInfo(b, sharedWorkspaceBindingsKey)
			}
			return
		case "bind":
			if len(args) < 3 {
				e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsSharedUsage))
				return
			}
			bindWorkspace(sharedWorkspaceBindingsKey, args[2], MsgWsSharedBindSuccess)
			return
		case "route":
			if len(args) < 3 {
				e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsSharedUsage))
				return
			}
			routeWorkspace(sharedWorkspaceBindingsKey, args[2:], MsgWsSharedUsage, MsgWsSharedRouteSuccess)
			return
		case "init":
			if len(args) < 3 {
				e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsSharedUsage))
				return
			}
			initWorkspace(sharedWorkspaceBindingsKey, args[2], MsgWsSharedBindSuccess)
			return
		case "unbind":
			if e.workspaceBindings.Lookup(sharedWorkspaceBindingsKey, channelKey) == nil {
				e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsSharedNoBinding))
				return
			}
			e.workspaceBindings.Unbind(sharedWorkspaceBindingsKey, channelKey)
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsSharedUnbindSuccess))
			return
		case "list":
			listBindings(sharedWorkspaceBindingsKey, MsgWsSharedListEmpty, MsgWsSharedListTitle)
			return
		default:
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsSharedUsage))
			return
		}

	case "unbind":
		if e.workspaceBindings.Lookup(projectKey, channelKey) == nil {
			if e.workspaceBindings.Lookup(sharedWorkspaceBindingsKey, channelKey) != nil {
				e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsSharedOnlyHint))
			} else {
				e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsNoBinding))
			}
			return
		}
		e.workspaceBindings.Unbind(projectKey, channelKey)
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsUnbindSuccess))

	case "list":
		listBindings(projectKey, MsgWsListEmpty, MsgWsListTitle)

	default:
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsUsage))
	}
}

// dirCardPageSize is the max directory history rows per card page (Feishu / other card UIs).
const dirCardPageSize = 20

func (e *Engine) cmdShell(p Platform, msg *Message, raw string) {
	// Strip the command prefix ("/shell ", "/sh ", "/exec ", "/run ")
	shellCmd := raw
	for _, prefix := range []string{"/shell ", "/sh ", "/exec ", "/run "} {
		if strings.HasPrefix(strings.ToLower(raw), prefix) {
			shellCmd = raw[len(prefix):]
			break
		}
	}
	shellCmd = strings.TrimSpace(shellCmd)

	if shellCmd == "" {
		e.reply(p, msg.ReplyCtx, "Usage: /shell <command>\nExample: /shell ls -la")
		return
	}

	// In multi-workspace mode, resolve workspace directory for this channel
	var workDir string
	if e.multiWorkspace {
		channelKey := extractWorkspaceChannelKey(msg.SessionKey)
		if b, _, usable := e.lookupEffectiveWorkspaceBinding(channelKey); usable {
			workDir = b.Workspace
		}
	}
	if workDir == "" {
		if wd, ok := e.agent.(interface{ GetWorkDir() string }); ok {
			workDir = wd.GetWorkDir()
		}
	}
	if workDir == "" {
		workDir, _ = os.Getwd()
	}
	// Normalize all path sources consistently (resolves symlinks like /var → /private/var on macOS)
	workDir = normalizeWorkspacePath(workDir)

	go func() {
		ctx, cancel := context.WithTimeout(e.ctx, 60*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, "sh", "-c", shellCmd)
		cmd.Dir = workDir
		output, err := cmd.CombinedOutput()

		if ctx.Err() == context.DeadlineExceeded {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCommandTimeout), shellCmd))
			return
		}

		result := strings.TrimSpace(string(output))
		if err != nil && result == "" {
			result = err.Error()
		}
		if result == "" {
			result = "(no output)"
		}
		if runes := []rune(result); len(runes) > 4000 {
			result = string(runes[:3997]) + "..."
		}

		e.reply(p, msg.ReplyCtx, fmt.Sprintf("$ %s\n```\n%s\n```", shellCmd, result))
	}()
}

// dirApply applies /dir mutations (same semantics as cmdDir). sessionKey is used for GetOrCreateActive.
// On failure returns a non-empty errMsg; on success returns ("", successMsg) for plain-text replies.
func (e *Engine) dirApply(agent Agent, sessions *SessionManager, interactiveKey, sessionKey string, args []string) (errMsg, successMsg string) {
	switcher, ok := agent.(WorkDirSwitcher)
	if !ok {
		return e.i18n.T(MsgDirNotSupported), ""
	}
	currentDir := switcher.GetWorkDir()

	if len(args) == 1 {
		switch strings.ToLower(strings.TrimSpace(args[0])) {
		case "reset":
			baseDir := strings.TrimSpace(e.baseWorkDir)
			if baseDir == "" {
				baseDir = currentDir
			}
			if baseDir == "" {
				baseDir, _ = os.Getwd()
			}
			if absDir, err := filepath.Abs(baseDir); err == nil {
				baseDir = absDir
			}

			switcher.SetWorkDir(baseDir)
			e.cleanupInteractiveState(interactiveKey)

			s := sessions.GetOrCreateActive(sessionKey)
			s.SetAgentSessionID("", "")
			s.ClearHistory()
			sessions.Save()

			if e.projectState != nil {
				e.projectState.ClearWorkDirOverride()
				e.projectState.Save()
			}
			if e.dirHistory != nil {
				e.dirHistory.Add(e.name, baseDir)
			}

			return "", e.i18n.Tf(MsgDirReset, baseDir)
		}
	}

	arg := strings.Join(args, " ")
	var newDir string

	if idx, err := strconv.Atoi(strings.TrimSpace(arg)); err == nil && idx > 0 {
		if e.dirHistory != nil {
			newDir = e.dirHistory.Get(e.name, idx)
			if newDir == "" {
				return e.i18n.Tf(MsgDirInvalidIndex, idx), ""
			}
		} else {
			return e.i18n.T(MsgDirNoHistory), ""
		}
	} else if arg == "-" {
		if e.dirHistory != nil {
			newDir = e.dirHistory.Previous(e.name)
			if newDir == "" {
				return e.i18n.T(MsgDirNoPrevious), ""
			}
		} else {
			return e.i18n.T(MsgDirNoHistory), ""
		}
	} else {
		newDir = filepath.Clean(arg)
		if !filepath.IsAbs(newDir) {
			baseDir := currentDir
			if baseDir == "" {
				baseDir, _ = os.Getwd()
			}
			newDir = filepath.Join(baseDir, newDir)
		}
	}
	if absDir, err := filepath.Abs(newDir); err == nil {
		newDir = absDir
	}

	info, err := os.Stat(newDir)
	if err != nil || !info.IsDir() {
		return e.i18n.Tf(MsgDirInvalidPath, newDir), ""
	}

	switcher.SetWorkDir(newDir)
	e.cleanupInteractiveState(interactiveKey)

	s := sessions.GetOrCreateActive(sessionKey)
	s.SetAgentSessionID("", "")
	s.ClearHistory()
	sessions.Save()

	if e.dirHistory != nil {
		e.dirHistory.Add(e.name, newDir)
	}
	if e.projectState != nil {
		e.projectState.SetWorkDirOverride(newDir)
		e.projectState.Save()
	}

	return "", e.i18n.Tf(MsgDirChanged, newDir)
}

func (e *Engine) cmdDir(p Platform, msg *Message, args []string) {
	agent, sessions, interactiveKey, err := e.commandContext(p, msg)
	if err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsResolutionError, err))
		return
	}
	switcher, ok := agent.(WorkDirSwitcher)
	if !ok {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgDirNotSupported))
		return
	}

	currentDir := switcher.GetWorkDir()

	if len(args) == 0 {
		if supportsCards(p) {
			e.replyWithCard(p, msg.ReplyCtx, e.renderDirCardSafe(msg.SessionKey, 1))
			return
		}
		var sb strings.Builder
		sb.WriteString(e.i18n.Tf(MsgDirCurrent, currentDir))

		if e.dirHistory != nil {
			history := e.dirHistory.List(e.name)
			if len(history) > 0 {
				sb.WriteString("\n\n")
				sb.WriteString(e.i18n.T(MsgDirHistoryTitle))
				for i, dir := range history {
					marker := "◻"
					if dir == currentDir {
						marker = "▶"
					}
					sb.WriteString(fmt.Sprintf("\n  %s %d. %s", marker, i+1, dir))
				}
				sb.WriteString("\n\n")
				sb.WriteString(e.i18n.T(MsgDirHistoryHint))
			}
		}
		e.reply(p, msg.ReplyCtx, sb.String())
		return
	}

	if len(args) == 1 {
		switch strings.ToLower(strings.TrimSpace(args[0])) {
		case "help", "-h", "--help":
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgDirUsage))
			return
		}
	}

	errMsg, successMsg := e.dirApply(agent, sessions, interactiveKey, msg.SessionKey, args)
	if errMsg != "" {
		e.reply(p, msg.ReplyCtx, errMsg)
		return
	}
	if supportsCards(p) {
		e.replyWithCard(p, msg.ReplyCtx, e.renderDirCardSafe(msg.SessionKey, 1))
		return
	}
	e.reply(p, msg.ReplyCtx, successMsg)
}

func (e *Engine) cmdLang(p Platform, msg *Message, args []string) {
	if len(args) == 0 {
		cur := e.i18n.CurrentLang()
		name := langDisplayName(cur)
		text := e.i18n.Tf(MsgLangCurrent, name)
		buttons := [][]ButtonOption{
			{
				{Text: "English", Data: "cmd:/lang en"},
				{Text: "中文", Data: "cmd:/lang zh"},
				{Text: "繁體中文", Data: "cmd:/lang zh-TW"},
			},
			{
				{Text: "日本語", Data: "cmd:/lang ja"},
				{Text: "Español", Data: "cmd:/lang es"},
				{Text: "Auto", Data: "cmd:/lang auto"},
			},
		}
		if supportsCards(p) {
			e.replyWithCard(p, msg.ReplyCtx, e.renderLangCard())
			return
		}
		if _, ok := p.(InlineButtonSender); ok {
			e.replyWithButtons(p, msg.ReplyCtx, text, buttons)
			return
		}
		var sb strings.Builder
		sb.WriteString(text)
		sb.WriteString("\n\n")
		sb.WriteString("- English: `/lang en`\n")
		sb.WriteString("- 中文: `/lang zh`\n")
		sb.WriteString("- 繁體中文: `/lang zh-TW`\n")
		sb.WriteString("- 日本語: `/lang ja`\n")
		sb.WriteString("- Español: `/lang es`\n")
		sb.WriteString("- Auto: `/lang auto`")
		e.reply(p, msg.ReplyCtx, sb.String())
		return
	}

	target := strings.ToLower(strings.TrimSpace(args[0]))
	var lang Language
	switch target {
	case "en", "english":
		lang = LangEnglish
	case "zh", "cn", "chinese", "中文":
		lang = LangChinese
	case "zh-tw", "zh_tw", "zhtw", "繁體", "繁体":
		lang = LangTraditionalChinese
	case "ja", "jp", "japanese", "日本語":
		lang = LangJapanese
	case "es", "spanish", "español":
		lang = LangSpanish
	case "auto":
		lang = LangAuto
	default:
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgLangInvalid))
		return
	}

	e.i18n.SetLang(lang)
	name := langDisplayName(lang)
	e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgLangChanged, name))
}

func langDisplayName(lang Language) string {
	switch lang {
	case LangEnglish:
		return "English"
	case LangChinese:
		return "中文"
	case LangTraditionalChinese:
		return "繁體中文"
	case LangJapanese:
		return "日本語"
	case LangSpanish:
		return "Español"
	default:
		return "Auto"
	}
}

func (e *Engine) cmdHelp(p Platform, msg *Message) {
	if !supportsCards(p) {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgHelp))
		return
	}
	e.replyWithCard(p, msg.ReplyCtx, e.renderHelpCard())
}



func (e *Engine) cmdQuiet(p Platform, msg *Message, args []string) {
	// /quiet global — toggle global quiet for all sessions
	if len(args) > 0 && args[0] == "global" {
		e.quietMu.Lock()
		e.quiet = !e.quiet
		quiet := e.quiet
		e.quietMu.Unlock()

		if quiet {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgQuietGlobalOn))
		} else {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgQuietGlobalOff))
		}
		return
	}

	// /quiet — toggle per-session quiet
	iKey := e.interactiveKeyForSessionKey(msg.SessionKey)
	e.interactiveMu.Lock()
	state, ok := e.interactiveStates[iKey]
	e.interactiveMu.Unlock()

	if !ok || state == nil {
		state = &interactiveState{platform: p, replyCtx: msg.ReplyCtx, quiet: true}
		e.interactiveMu.Lock()
		e.interactiveStates[iKey] = state
		e.interactiveMu.Unlock()
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgQuietOn))
		return
	}

	state.mu.Lock()
	state.quiet = !state.quiet
	quiet := state.quiet
	state.mu.Unlock()

	if quiet {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgQuietOn))
	} else {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgQuietOff))
	}
}

func (e *Engine) cmdTTS(p Platform, msg *Message, args []string) {
	if e.tts == nil || !e.tts.Enabled || e.tts.TTS == nil {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgTTSNotEnabled))
		return
	}
	if len(args) == 0 {
		providerStr := e.tts.Provider
		if providerStr == "" {
			providerStr = "unknown"
		}
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgTTSStatus), e.tts.GetTTSMode(), providerStr))
		return
	}
	switch args[0] {
	case "always", "voice_only":
		mode := args[0]
		e.tts.SetTTSMode(mode)
		if e.ttsSaveFunc != nil {
			if err := e.ttsSaveFunc(mode); err != nil {
				slog.Warn("tts: failed to persist mode", "error", err)
			}
		}
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgTTSSwitched), mode))
	default:
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgTTSUsage))
	}
}


func (e *Engine) cmdAllow(p Platform, msg *Message, args []string) {
	if len(args) == 0 {
		if auth, ok := e.agent.(ToolAuthorizer); ok {
			tools := auth.GetAllowedTools()
			if len(tools) == 0 {
				e.reply(p, msg.ReplyCtx, e.i18n.T(MsgNoToolsAllowed))
			} else {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCurrentTools), strings.Join(tools, ", ")))
			}
		} else {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgToolAuthNotSupported))
		}
		return
	}

	toolName := strings.TrimSpace(args[0])
	if auth, ok := e.agent.(ToolAuthorizer); ok {
		if err := auth.AddAllowedTools(toolName); err != nil {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgToolAllowFailed), err))
			return
		}
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgToolAllowedNew), toolName))
	} else {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgToolAuthNotSupported))
	}
}


// ──────────────────────────────────────────────────────────────
// Card navigation (in-place card updates)

// ──────────────────────────────────────────────────────────────
// /memory command
// ──────────────────────────────────────────────────────────────

func (e *Engine) cmdMemory(p Platform, msg *Message, args []string) {
	mp, ok := e.agent.(MemoryFileProvider)
	if !ok {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgMemoryNotSupported))
		return
	}

	if len(args) == 0 {
		// /memory — show project memory
		e.showMemoryFile(p, msg, mp.ProjectMemoryFile(), false)
		return
	}

	sub := matchSubCommand(strings.ToLower(args[0]), []string{"add", "global", "show", "help"})
	switch sub {
	case "add":
		text := strings.TrimSpace(strings.Join(args[1:], " "))
		if text == "" {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgMemoryAddUsage))
			return
		}
		e.appendMemoryFile(p, msg, mp.ProjectMemoryFile(), text)

	case "global":
		if len(args) == 1 {
			// /memory global — show global memory
			e.showMemoryFile(p, msg, mp.GlobalMemoryFile(), true)
			return
		}
		if strings.ToLower(args[1]) == "add" {
			text := strings.TrimSpace(strings.Join(args[2:], " "))
			if text == "" {
				e.reply(p, msg.ReplyCtx, e.i18n.T(MsgMemoryAddUsage))
				return
			}
			e.appendMemoryFile(p, msg, mp.GlobalMemoryFile(), text)
		} else {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgMemoryAddUsage))
		}

	case "show":
		e.showMemoryFile(p, msg, mp.ProjectMemoryFile(), false)

	case "help", "--help", "-h":
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgMemoryAddUsage))

	default:
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgMemoryAddUsage))
	}
}

func (e *Engine) showMemoryFile(p Platform, msg *Message, filePath string, isGlobal bool) {
	if filePath == "" {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgMemoryNotSupported))
		return
	}

	data, err := os.ReadFile(filePath)
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgMemoryEmpty), filePath))
		return
	}

	content := string(data)
	if len([]rune(content)) > 2000 {
		content = string([]rune(content)[:2000]) + "\n\n... (truncated)"
	}

	if isGlobal {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgMemoryShowGlobal), filePath, content))
	} else {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgMemoryShowProject), filePath, content))
	}
}

func (e *Engine) appendMemoryFile(p Platform, msg *Message, filePath, text string) {
	if filePath == "" {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgMemoryNotSupported))
		return
	}

	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgMemoryAddFailed), err))
		return
	}

	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgMemoryAddFailed), err))
		return
	}
	defer f.Close()

	entry := "\n- " + text + "\n"
	if _, err := f.WriteString(entry); err != nil {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgMemoryAddFailed), err))
		return
	}

	e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgMemoryAdded), filePath))
}




// ── /config command ──────────────────────────────────────────

// configItem describes a configurable runtime parameter.
type configItem struct {
	key     string
	desc    string // en description
	descZh  string // zh description
	getFunc func() string
	setFunc func(string) error
}

func (ci configItem) description(isZh bool) string {
	if isZh && ci.descZh != "" {
		return ci.descZh
	}
	return ci.desc
}

func (e *Engine) configItems() []configItem {
	return []configItem{
		{
			key:    "thinking_max_len",
			desc:   "Max chars for thinking messages (0=no truncation)",
			descZh: "思考消息最大长度 (0=不截断)",
			getFunc: func() string {
				return fmt.Sprintf("%d", e.display.ThinkingMaxLen)
			},
			setFunc: func(v string) error {
				n, err := strconv.Atoi(v)
				if err != nil {
					return fmt.Errorf("invalid integer: %s", v)
				}
				if n < 0 {
					return fmt.Errorf("value must be >= 0")
				}
				e.display.ThinkingMaxLen = n
				if e.displaySaveFunc != nil {
					return e.displaySaveFunc(&n, nil)
				}
				return nil
			},
		},
		{
			key:    "tool_max_len",
			desc:   "Max chars for tool use messages (0=no truncation)",
			descZh: "工具消息最大长度 (0=不截断)",
			getFunc: func() string {
				return fmt.Sprintf("%d", e.display.ToolMaxLen)
			},
			setFunc: func(v string) error {
				n, err := strconv.Atoi(v)
				if err != nil {
					return fmt.Errorf("invalid integer: %s", v)
				}
				if n < 0 {
					return fmt.Errorf("value must be >= 0")
				}
				e.display.ToolMaxLen = n
				if e.displaySaveFunc != nil {
					return e.displaySaveFunc(nil, &n)
				}
				return nil
			},
		},
	}
}

func (e *Engine) cmdConfig(p Platform, msg *Message, args []string) {
	if len(args) == 0 {
		if !supportsCards(p) {
			items := e.configItems()
			isZh := e.i18n.IsZhLike()
			var sb strings.Builder
			sb.WriteString(e.i18n.T(MsgConfigTitle))
			for _, item := range items {
				sb.WriteString(fmt.Sprintf("`%s` = `%s`\n  %s\n\n", item.key, item.getFunc(), item.description(isZh)))
			}
			sb.WriteString(e.i18n.T(MsgConfigHint))
			e.reply(p, msg.ReplyCtx, sb.String())
			return
		}

		e.replyWithCard(p, msg.ReplyCtx, e.renderConfigCard())
		return
	}

	items := e.configItems()
	isZh := e.i18n.IsZhLike()
	sub := matchSubCommand(strings.ToLower(args[0]), []string{"get", "set", "reload"})

	switch sub {
	case "reload":
		e.cmdConfigReload(p, msg)
		return
	case "get":
		if len(args) < 2 {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgConfigGetUsage))
			return
		}
		key := strings.ToLower(args[1])
		for _, item := range items {
			if item.key == key {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("`%s` = `%s`\n  %s", key, item.getFunc(), item.description(isZh)))
				return
			}
		}
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgConfigKeyNotFound, key))

	case "set":
		if len(args) < 3 {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgConfigSetUsage))
			return
		}
		key := strings.ToLower(args[1])
		value := args[2]
		for _, item := range items {
			if item.key == key {
				if err := item.setFunc(value); err != nil {
					e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgError, err))
					return
				}
				e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgConfigUpdated, key, item.getFunc()))
				return
			}
		}
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgConfigKeyNotFound, key))

	default:
		key := strings.ToLower(sub)
		for _, item := range items {
			if item.key == key {
				if len(args) >= 2 {
					if err := item.setFunc(args[1]); err != nil {
						e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgError, err))
						return
					}
					e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgConfigUpdated, key, item.getFunc()))
				} else {
					e.reply(p, msg.ReplyCtx, fmt.Sprintf("`%s` = `%s`\n  %s", key, item.getFunc(), item.description(isZh)))
				}
				return
			}
		}
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgConfigKeyNotFound, key))
	}
}

// ── /whoami command ─────────────────────────────────────────

func (e *Engine) cmdWhoami(p Platform, msg *Message) {
	if supportsCards(p) {
		e.replyWithCard(p, msg.ReplyCtx, e.renderWhoamiCard(msg))
		return
	}
	e.reply(p, msg.ReplyCtx, e.formatWhoamiText(msg))
}

func (e *Engine) formatWhoamiText(msg *Message) string {
	var sb strings.Builder
	sb.WriteString(e.i18n.T(MsgWhoamiTitle))
	sb.WriteString("\n")

	if msg.UserID != "" {
		sb.WriteString(fmt.Sprintf("User ID: `%s`\n", msg.UserID))
	} else {
		sb.WriteString("User ID: (unknown)\n")
	}
	if msg.UserName != "" {
		sb.WriteString(fmt.Sprintf("Name: %s\n", msg.UserName))
	}
	if msg.Platform != "" {
		sb.WriteString(fmt.Sprintf("Platform: %s\n", msg.Platform))
	}

	chatID := extractChannelID(msg.SessionKey)
	if chatID != "" {
		sb.WriteString(fmt.Sprintf("Chat ID: `%s`\n", chatID))
	}
	sb.WriteString(fmt.Sprintf("Session Key: `%s`\n", msg.SessionKey))

	sb.WriteString("\n")
	sb.WriteString(e.i18n.T(MsgWhoamiUsage))
	return sb.String()
}

// ── /doctor command ─────────────────────────────────────────

func (e *Engine) cmdDoctor(p Platform, msg *Message) {
	results := RunDoctorChecks(e.ctx, e.agent, e.platforms, e.chatStore)
	report := FormatDoctorResults(results, e.i18n)
	e.reply(p, msg.ReplyCtx, report)
}

func (e *Engine) cmdUpgrade(p Platform, msg *Message, args []string) {
	subCmd := ""
	if len(args) > 0 {
		subCmd = matchSubCommand(args[0], []string{"confirm", "check"})
	}

	if subCmd == "confirm" {
		e.cmdUpgradeConfirm(p, msg)
		return
	}

	// Default: check for updates
	e.reply(p, msg.ReplyCtx, e.i18n.T(MsgUpgradeChecking))

	cur := CurrentVersion
	if cur == "" || cur == "dev" {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgUpgradeDevBuild))
		return
	}

	useGitee := e.i18n.IsZhLike()
	release, err := CheckForUpdate(cur, useGitee)
	if err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgError, err))
		return
	}
	if release == nil {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgUpgradeUpToDate), cur))
		return
	}

	body := release.Body
	if len([]rune(body)) > 300 {
		body = string([]rune(body)[:300]) + "…"
	}

	e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgUpgradeAvailable), cur, release.TagName, body))
}

func (e *Engine) cmdUpgradeConfirm(p Platform, msg *Message) {
	cur := CurrentVersion
	if cur == "" || cur == "dev" {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgUpgradeDevBuild))
		return
	}

	useGitee := e.i18n.IsZhLike()
	release, err := CheckForUpdate(cur, useGitee)
	if err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgError, err))
		return
	}
	if release == nil {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgUpgradeUpToDate), cur))
		return
	}

	e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgUpgradeDownloading), release.TagName))

	if err := SelfUpdate(release.TagName, useGitee); err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgError, err))
		return
	}

	e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgUpgradeSuccess), release.TagName))

	// Auto-restart to apply the update
	select {
	case RestartCh <- RestartRequest{
		SessionKey: msg.SessionKey,
		Platform:   p.Name(),
	}:
	default:
	}
}

func (e *Engine) cmdConfigReload(p Platform, msg *Message) {
	if e.configReloadFunc == nil {
		e.reply(p, msg.ReplyCtx, "❌ Config reload not available")
		return
	}
	result, err := e.configReloadFunc()
	if err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgError, err))
		return
	}
	e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgConfigReloaded),
		result.DisplayUpdated, result.ProvidersUpdated, result.CommandsUpdated))
}

func (e *Engine) cmdRestart(p Platform, msg *Message) {
	e.reply(p, msg.ReplyCtx, e.i18n.T(MsgRestarting))
	select {
	case RestartCh <- RestartRequest{
		SessionKey: msg.SessionKey,
		Platform:   p.Name(),
	}:
	default:
	}
}



// cmdBind handles /bind — establishes a relay binding between bots in a group chat.
//
// Usage:
//
//	/bind <project>           — bind current bot with another project in this group
//	/bind remove              — remove all bindings for this group
//	/bind -<project>          — remove specific project from binding
//	/bind                     — show current binding status
//
// The <project> argument is the project name from config.toml [[projects]].
// Multiple projects can be bound together for relay.
func (e *Engine) cmdBind(p Platform, msg *Message, args []string) {
	if e.relayManager == nil {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgRelayNotAvailable))
		return
	}

	_, chatID, err := parseSessionKeyParts(msg.SessionKey)
	if err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgRelayNotAvailable))
		return
	}

	if len(args) == 0 {
		e.cmdBindStatus(p, msg.ReplyCtx, chatID)
		return
	}

	otherProject := args[0]

	// Handle removal commands
	if otherProject == "remove" || otherProject == "rm" || otherProject == "unbind" || otherProject == "del" || otherProject == "clear" {
		e.relayManager.Unbind(chatID)
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgRelayUnbound))
		return
	}

	if otherProject == "setup" {
		e.cmdBindSetup(p, msg)
		return
	}

	if otherProject == "help" || otherProject == "-h" || otherProject == "--help" {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgRelayUsage))
		return
	}

	// Handle removal with - prefix: /bind -project
	if strings.HasPrefix(otherProject, "-") {
		projectToRemove := strings.TrimPrefix(otherProject, "-")
		if e.relayManager.RemoveFromBind(chatID, projectToRemove) {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgRelayBindRemoved), projectToRemove))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgRelayBindNotFound), projectToRemove))
		}
		return
	}

	if otherProject == e.name {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgRelayBindSelf))
		return
	}

	// Validate the target project exists
	if !e.relayManager.HasEngine(otherProject) {
		available := e.relayManager.ListEngineNames()
		var others []string
		for _, n := range available {
			if n != e.name {
				others = append(others, n)
			}
		}
		if len(others) == 0 {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgRelayNoTarget), otherProject))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgRelayNotFound), otherProject, strings.Join(others, ", ")))
		}
		return
	}

	// Add current project and target project to binding
	e.relayManager.AddToBind(p.Name(), chatID, e.name)
	e.relayManager.AddToBind(p.Name(), chatID, otherProject)

	// Get all bound projects for status message
	binding := e.relayManager.GetBinding(chatID)
	var boundProjects []string
	for proj := range binding.Bots {
		boundProjects = append(boundProjects, proj)
	}

	reply := fmt.Sprintf(e.i18n.T(MsgRelayBindSuccess), strings.Join(boundProjects, " ↔ "), otherProject, otherProject)

	if _, ok := e.agent.(SystemPromptSupporter); !ok {
		if mp, ok := e.agent.(MemoryFileProvider); ok {
			reply += fmt.Sprintf(e.i18n.T(MsgRelaySetupHint), filepath.Base(mp.ProjectMemoryFile()))
		}
	}

	e.reply(p, msg.ReplyCtx, reply)
}

func (e *Engine) cmdBindStatus(p Platform, replyCtx any, chatID string) {
	binding := e.relayManager.GetBinding(chatID)
	if binding == nil {
		e.reply(p, replyCtx, e.i18n.T(MsgRelayNoBinding))
		return
	}
	var parts []string
	for proj := range binding.Bots {
		parts = append(parts, proj)
	}
	e.reply(p, replyCtx, fmt.Sprintf(e.i18n.T(MsgRelayBound), strings.Join(parts, " ↔ ")))
}

const ccConnectInstructionMarker = "<!-- cc-connect-instructions -->"

type setupResult int

const (
	setupOK       setupResult = iota // instructions written successfully
	setupExists                      // instructions already present
	setupNative                      // agent supports system prompt natively
	setupNoMemory                    // agent has no memory file support
	setupError                       // write error
)

// setupMemoryFile appends AgentSystemPrompt() to the agent's project memory
// file. It returns the result, the filename (for messages), and any error.
func (e *Engine) setupMemoryFile() (setupResult, string, error) {
	if _, ok := e.agent.(SystemPromptSupporter); ok {
		return setupNative, "", nil
	}

	mp, ok := e.agent.(MemoryFileProvider)
	if !ok {
		return setupNoMemory, "", nil
	}

	filePath := mp.ProjectMemoryFile()
	if filePath == "" {
		return setupNoMemory, "", nil
	}

	baseName := filepath.Base(filePath)

	existing, _ := os.ReadFile(filePath)
	existingText := string(existing)
	block := "\n" + ccConnectInstructionMarker + "\n" + AgentSystemPrompt() + "\n"
	if idx := strings.Index(existingText, ccConnectInstructionMarker); idx >= 0 {
		if strings.Contains(existingText[idx:], AgentSystemPrompt()) {
			return setupExists, baseName, nil
		}
		updated := strings.TrimRight(existingText[:idx], "\n") + block
		if err := os.WriteFile(filePath, []byte(updated), 0o644); err != nil {
			return setupError, baseName, err
		}
		return setupOK, baseName, nil
	}

	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return setupError, baseName, err
	}

	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return setupError, baseName, err
	}
	defer f.Close()

	if _, err := f.WriteString(block); err != nil {
		return setupError, baseName, err
	}

	return setupOK, baseName, nil
}

func (e *Engine) cmdBindSetup(p Platform, msg *Message) {
	result, baseName, err := e.setupMemoryFile()
	switch result {
	case setupNative:
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgSetupNative))
	case setupNoMemory:
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgRelaySetupNoMemory))
	case setupExists:
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgRelaySetupExists), baseName))
	case setupError:
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgError, err))
	case setupOK:
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgRelaySetupOK), baseName))
	}
}
