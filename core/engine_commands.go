package core

import (
	"fmt"
	"log/slog"
	"strings"
)


// ──────────────────────────────────────────────────────────────
// Command handling
// ──────────────────────────────────────────────────────────────

// commandHandler 是内建命令的统一处理函数签名。
// p 为来源平台，msg 为原始消息，args 为命令参数（不含命令名本身）。
type commandHandler func(p Platform, msg *Message, args []string)

// builtinCommandDef 定义一个内建命令及其处理器。
type builtinCommandDef struct {
	names      []string       // 命令名（第一个为主名称，其余为别名）
	id         string         // 命令唯一 ID（用于 disabled 检查等）
	handler    commandHandler // 命令处理函数
	privileged bool           // 需要 admin_from 授权
}

// builtinCommandNames 保留命令名/ID 列表，供 resolveDisabledCmds 和 matchPrefix 使用。
var builtinCommandNames = []struct {
	names []string
	id    string
}{
	{[]string{"new"}, "new"},
	{[]string{"list", "sessions"}, "list"},
	{[]string{"resume"}, "resume"},
	{[]string{"switch"}, "switch"},
	{[]string{"name", "rename"}, "name"},
	{[]string{"current"}, "current"},
	{[]string{"status"}, "status"},
	{[]string{"usage", "quota"}, "usage"},
	{[]string{"history"}, "history"},
	{[]string{"allow"}, "allow"},
	{[]string{"model"}, "model"},
	{[]string{"reasoning", "effort"}, "reasoning"},
	{[]string{"mode"}, "mode"},
	{[]string{"lang"}, "lang"},
	{[]string{"quiet"}, "quiet"},
	{[]string{"provider"}, "provider"},
	{[]string{"memory"}, "memory"},
	{[]string{"cron"}, "cron"},
	{[]string{"heartbeat", "hb"}, "heartbeat"},
	{[]string{"compress", "compact"}, "compress"},
	{[]string{"stop"}, "stop"},
	{[]string{"help"}, "help"},
	{[]string{"version"}, "version"},
	{[]string{"commands", "command", "cmd"}, "commands"},
	{[]string{"skills", "skill"}, "skills"},
	{[]string{"config"}, "config"},
	{[]string{"doctor"}, "doctor"},
	{[]string{"upgrade", "update"}, "upgrade"},
	{[]string{"restart"}, "restart"},
	{[]string{"alias"}, "alias"},
	{[]string{"delete", "del", "rm"}, "delete"},
	{[]string{"bind"}, "bind"},
	{[]string{"search", "find"}, "search"},
	{[]string{"shell", "sh", "exec", "run"}, "shell"},
	{[]string{"dir", "cd", "chdir", "workdir"}, "dir"},
	{[]string{"tts"}, "tts"},
	{[]string{"workspace", "ws"}, "workspace"},
	{[]string{"whoami", "myid"}, "whoami"},
}

// isBtwCommand checks if a trimmed message starts with a /btw command.
func isBtwCommand(trimmed string) bool {
	return matchBtwPrefix(trimmed) != ""
}

// matchBtwPrefix returns the prefix portion (e.g. "/btw ") if the
// message starts with a btw command, or "" if it doesn't match.
func matchBtwPrefix(trimmed string) string {
	lower := strings.ToLower(trimmed)
	for _, prefix := range []string{"/btw"} {
		if strings.HasPrefix(lower, prefix) {
			rest := trimmed[len(prefix):]
			if rest == "" || rest[0] == ' ' {
				return trimmed[:len(prefix)]
			}
		}
	}
	return ""
}

// matchPrefix finds a unique command matching the given prefix.
// Returns the command id or "" if no match / ambiguous.
func matchPrefix(prefix string, candidates []struct {
	names []string
	id    string
}) string {
	// Exact match first
	for _, c := range candidates {
		for _, n := range c.names {
			if prefix == n {
				return c.id
			}
		}
	}
	// Prefix match
	var matched string
	for _, c := range candidates {
		for _, n := range c.names {
			if strings.HasPrefix(n, prefix) {
				if matched != "" && matched != c.id {
					return "" // ambiguous
				}
				matched = c.id
				break
			}
		}
	}
	return matched
}

// matchSubCommand does prefix matching against a flat list of subcommand names.
func matchSubCommand(input string, candidates []string) string {
	for _, c := range candidates {
		if input == c {
			return c
		}
	}
	var matched string
	for _, c := range candidates {
		if strings.HasPrefix(c, input) {
			if matched != "" {
				return input // ambiguous → return raw input (will hit default)
			}
			matched = c
		}
	}
	if matched != "" {
		return matched
	}
	return input
}

// initBuiltinCommands 初始化命令注册表，将每个内建命令绑定到对应的处理函数。
// 在 NewEngine 末尾调用。
func (e *Engine) initBuiltinCommands() {
	e.builtinCommandDefs = []builtinCommandDef{
		{names: []string{"new"}, id: "new", handler: e.cmdNew},
		{names: []string{"list", "sessions"}, id: "list", handler: e.cmdList},
		{names: []string{"resume"}, id: "resume", handler: func(p Platform, msg *Message, args []string) {
			// /resume (no args) → show session list; /resume <arg> → switch to session
			if len(args) == 0 {
				e.cmdList(p, msg, args)
			} else {
				e.cmdSwitch(p, msg, args)
			}
		}},
		{names: []string{"switch"}, id: "switch", handler: e.cmdSwitch},
		{names: []string{"name", "rename"}, id: "name", handler: e.cmdName},
		{names: []string{"current"}, id: "current", handler: func(p Platform, msg *Message, _ []string) {
			e.cmdCurrent(p, msg)
		}},
		{names: []string{"status"}, id: "status", handler: func(p Platform, msg *Message, _ []string) {
			e.cmdStatus(p, msg)
		}},
		{names: []string{"usage", "quota"}, id: "usage", handler: func(p Platform, msg *Message, _ []string) {
			e.cmdUsage(p, msg)
		}},
		{names: []string{"history"}, id: "history", handler: e.cmdHistory},
		{names: []string{"allow"}, id: "allow", handler: e.cmdAllow},
		{names: []string{"model"}, id: "model", handler: e.cmdModel},
		{names: []string{"reasoning", "effort"}, id: "reasoning", handler: e.cmdReasoning},
		{names: []string{"mode"}, id: "mode", handler: e.cmdMode},
		{names: []string{"lang"}, id: "lang", handler: e.cmdLang},
		{names: []string{"quiet"}, id: "quiet", handler: e.cmdQuiet},
		{names: []string{"provider"}, id: "provider", handler: e.cmdProvider},
		{names: []string{"memory"}, id: "memory", handler: e.cmdMemory},
		{names: []string{"cron"}, id: "cron", handler: e.cmdCron},
		{names: []string{"heartbeat", "hb"}, id: "heartbeat", handler: e.cmdHeartbeat},
		{names: []string{"compress", "compact"}, id: "compress", handler: func(p Platform, msg *Message, _ []string) {
			e.cmdCompress(p, msg)
		}},
		{names: []string{"stop"}, id: "stop", handler: func(p Platform, msg *Message, _ []string) {
			e.cmdStop(p, msg)
		}},
		{names: []string{"help"}, id: "help", handler: func(p Platform, msg *Message, _ []string) {
			e.cmdHelp(p, msg)
		}},
		{names: []string{"version"}, id: "version", handler: func(p Platform, msg *Message, _ []string) {
			e.reply(p, msg.ReplyCtx, VersionInfo)
		}},
		{names: []string{"commands", "command", "cmd"}, id: "commands", handler: e.cmdCommands},
		{names: []string{"skills", "skill"}, id: "skills", handler: func(p Platform, msg *Message, _ []string) {
			e.cmdSkills(p, msg)
		}},
		{names: []string{"config"}, id: "config", handler: e.cmdConfig},
		{names: []string{"doctor"}, id: "doctor", handler: func(p Platform, msg *Message, _ []string) {
			e.cmdDoctor(p, msg)
		}},
		{names: []string{"upgrade", "update"}, id: "upgrade", handler: e.cmdUpgrade, privileged: true},
		{names: []string{"restart"}, id: "restart", handler: func(p Platform, msg *Message, _ []string) {
			e.cmdRestart(p, msg)
		}, privileged: true},
		{names: []string{"alias"}, id: "alias", handler: e.cmdAlias},
		{names: []string{"delete", "del", "rm"}, id: "delete", handler: e.cmdDelete},
		{names: []string{"bind"}, id: "bind", handler: e.cmdBind},
		{names: []string{"search", "find"}, id: "search", handler: e.cmdSearch},
		{names: []string{"retry", "redo"}, id: "retry", handler: e.cmdRetry},
		{names: []string{"shell", "sh", "exec", "run"}, id: "shell", handler: func(p Platform, msg *Message, _ []string) {
			e.cmdShell(p, msg, msg.Content)
		}, privileged: true},
		{names: []string{"dir", "cd", "chdir", "workdir"}, id: "dir", handler: e.cmdDir, privileged: true},
		{names: []string{"tts"}, id: "tts", handler: e.cmdTTS},
		{names: []string{"workspace", "ws"}, id: "workspace", handler: func(p Platform, msg *Message, args []string) {
			if !e.multiWorkspace {
				e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsNotEnabled))
				return
			}
			e.handleWorkspaceCommand(p, msg, args)
		}},
		{names: []string{"whoami", "myid"}, id: "whoami", handler: func(p Platform, msg *Message, _ []string) {
			e.cmdWhoami(p, msg)
		}},
	}
}

// matchBuiltinPrefix 从 Engine 实例的 builtinCommandDefs 中匹配命令前缀。
// 返回匹配到的命令 ID 或 ""。
func (e *Engine) matchBuiltinPrefix(prefix string) string {
	// Exact match first
	for _, def := range e.builtinCommandDefs {
		for _, n := range def.names {
			if prefix == n {
				return def.id
			}
		}
	}
	// Prefix match
	var matched string
	for _, def := range e.builtinCommandDefs {
		for _, n := range def.names {
			if strings.HasPrefix(n, prefix) {
				if matched != "" && matched != def.id {
					return "" // ambiguous
				}
				matched = def.id
				break
			}
		}
	}
	return matched
}

func (e *Engine) handleCommand(p Platform, msg *Message, raw string) bool {
	parts := strings.Fields(raw)
	cmd := strings.ToLower(strings.TrimPrefix(parts[0], "/"))
	args := parts[1:]

	cmdID := e.matchBuiltinPrefix(cmd)

	// Resolve effective disabled commands: role-based if available, else project-level
	e.userRolesMu.RLock()
	disabledCmds := e.disabledCmds
	urm := e.userRoles
	e.userRolesMu.RUnlock()
	if urm != nil {
		if role := urm.ResolveRole(msg.UserID); role != nil {
			disabledCmds = role.DisabledCmds
		}
	}

	if cmdID != "" && disabledCmds[cmdID] {
		slog.Info("audit: command_blocked",
			"user_id", msg.UserID, "platform", msg.Platform,
			"project", e.name, "command", cmdID, "reason", "disabled")
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCommandDisabled), "/"+cmdID))
		return true
	}

	// 通过注册表查找命令定义
	var entry *builtinCommandDef
	if cmdID != "" {
		for i := range e.builtinCommandDefs {
			if e.builtinCommandDefs[i].id == cmdID {
				entry = &e.builtinCommandDefs[i]
				break
			}
		}
	}

	// 权限检查：通过注册表的 privileged 字段替代 privilegedCommands map
	if entry != nil && entry.privileged && !e.isAdmin(msg.UserID) {
		slog.Info("audit: command_blocked",
			"user_id", msg.UserID, "platform", msg.Platform,
			"project", e.name, "command", cmdID, "reason", "unauthorized")
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgAdminRequired), "/"+cmdID))
		return true
	}

	if entry != nil {
		slog.Info("audit: command_executed",
			"user_id", msg.UserID, "platform", msg.Platform,
			"project", e.name, "command", cmdID)
		entry.handler(p, msg, args)
		return true
	}

	// Not a builtin command — try custom commands and skills
	if custom, ok := e.commands.Resolve(cmd); ok {
		if disabledCmds[strings.ToLower(custom.Name)] {
			slog.Info("audit: command_blocked",
				"user_id", msg.UserID, "platform", msg.Platform,
				"project", e.name, "command", custom.Name, "reason", "disabled")
			e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCommandDisabled), "/"+custom.Name))
			return true
		}
		slog.Info("audit: command_executed",
			"user_id", msg.UserID, "platform", msg.Platform,
			"project", e.name, "command", custom.Name, "type", "custom")
		e.executeCustomCommand(p, msg, custom, args)
		return true
	}
	if skill := e.skills.Resolve(cmd); skill != nil {
		if disabledCmds[strings.ToLower(skill.Name)] {
			slog.Info("audit: command_blocked",
				"user_id", msg.UserID, "platform", msg.Platform,
				"project", e.name, "command", skill.Name, "reason", "disabled")
			e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCommandDisabled), "/"+skill.Name))
			return true
		}
		slog.Info("audit: command_executed",
			"user_id", msg.UserID, "platform", msg.Platform,
			"project", e.name, "command", skill.Name, "type", "skill")
		e.executeSkill(p, msg, skill, args)
		return true
	}

	// Not a cc-connect command — notify user, then fall through to agent
	e.send(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgUnknownCommand), "/"+cmd))
	return false
}

// GetAllCommands returns all available commands for bot menu registration.
// It includes built-in commands (with localized descriptions) and custom commands.
func (e *Engine) GetAllCommands() []BotCommandInfo {
	var commands []BotCommandInfo

	e.userRolesMu.RLock()
	disabledCmds := e.disabledCmds
	e.userRolesMu.RUnlock()

	// Collect built-in commands (use primary name, first in names list)
	seenCmds := make(map[string]bool)
	for _, c := range e.builtinCommandDefs {
		if len(c.names) == 0 {
			continue
		}
		// Use id as primary
		primaryName := c.id
		if seenCmds[primaryName] {
			continue
		}
		seenCmds[primaryName] = true

		// Skip disabled commands
		if disabledCmds[c.id] {
			continue
		}

		commands = append(commands, BotCommandInfo{
			Command:     primaryName,
			Description: e.i18n.T(MsgKey(primaryName)),
		})
	}

	// Collect custom commands from CommandRegistry
	for _, c := range e.commands.ListAll() {
		if seenCmds[strings.ToLower(c.Name)] {
			continue
		}
		seenCmds[strings.ToLower(c.Name)] = true

		desc := c.Description
		if desc == "" {
			desc = "Custom command"
		}

		commands = append(commands, BotCommandInfo{
			Command:     c.Name,
			Description: desc,
		})
	}

	// Collect skills
	for _, s := range e.skills.ListAll() {
		if seenCmds[strings.ToLower(s.Name)] {
			continue
		}
		seenCmds[strings.ToLower(s.Name)] = true

		desc := s.Description
		if desc == "" {
			desc = "Skill"
		}

		commands = append(commands, BotCommandInfo{
			Command:     s.Name,
			Description: desc,
		})
	}

	return commands
}


