package core

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ──────────────────────────────────────────────────────────────
// /cron command
// ──────────────────────────────────────────────────────────────

func (e *Engine) cmdCron(p Platform, msg *Message, args []string) {
	if e.cronScheduler == nil {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgCronNotAvailable))
		return
	}

	if len(args) == 0 {
		if !supportsCards(p) {
			e.cmdCronList(p, msg)
			return
		}
		e.replyWithCard(p, msg.ReplyCtx, e.renderCronCard(msg.SessionKey, msg.UserID))
		return
	}

	sub := matchSubCommand(strings.ToLower(args[0]), []string{
		"add", "addexec", "list", "del", "delete", "rm", "remove", "enable", "disable", "mute", "unmute", "setup",
	})
	switch sub {
	case "add":
		e.cmdCronAdd(p, msg, args[1:])
	case "addexec":
		e.cmdCronAddExec(p, msg, args[1:])
	case "list":
		e.cmdCronList(p, msg)
	case "del", "delete", "rm", "remove":
		e.cmdCronDel(p, msg, args[1:])
	case "enable":
		e.cmdCronToggle(p, msg, args[1:], true)
	case "disable":
		e.cmdCronToggle(p, msg, args[1:], false)
	case "mute":
		e.cmdCronMute(p, msg, args[1:], true)
	case "unmute":
		e.cmdCronMute(p, msg, args[1:], false)
	case "setup":
		e.cmdCronSetup(p, msg)
	default:
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgCronUsage))
	}
}

func (e *Engine) cmdCronAdd(p Platform, msg *Message, args []string) {
	// /cron add <min> <hour> <day> <month> <weekday> <prompt...>
	if len(args) < 6 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgCronAddUsage))
		return
	}

	cronExpr := strings.Join(args[:5], " ")
	prompt := strings.Join(args[5:], " ")

	job := &CronJob{
		ID:         GenerateCronID(),
		Project:    e.name,
		SessionKey: msg.SessionKey,
		CronExpr:   cronExpr,
		Prompt:     prompt,
		Enabled:    true,
		CreatedAt:  time.Now(),
	}

	if err := e.cronScheduler.AddJob(job); err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgError, err))
		return
	}

	e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCronAdded), job.ID, cronExpr, truncateStr(prompt, 60)))
}

func (e *Engine) cmdCronAddExec(p Platform, msg *Message, args []string) {
	if !e.isAdmin(msg.UserID) {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgAdminRequired), "/cron addexec"))
		return
	}

	// /cron addexec <min> <hour> <day> <month> <weekday> <shell command...>
	if len(args) < 6 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgCronAddExecUsage))
		return
	}

	cronExpr := strings.Join(args[:5], " ")
	shellCmd := strings.Join(args[5:], " ")

	job := &CronJob{
		ID:         GenerateCronID(),
		Project:    e.name,
		SessionKey: msg.SessionKey,
		CronExpr:   cronExpr,
		Exec:       shellCmd,
		Enabled:    true,
		CreatedAt:  time.Now(),
	}

	if err := e.cronScheduler.AddJob(job); err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgError, err))
		return
	}

	e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCronAddedExec), job.ID, cronExpr, truncateStr(shellCmd, 60)))
}

func (e *Engine) cmdCronList(p Platform, msg *Message) {
	jobs := e.cronScheduler.Store().ListBySessionKey(msg.SessionKey)
	if len(jobs) == 0 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgCronEmpty))
		return
	}

	lang := e.i18n.CurrentLang()
	now := time.Now()
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(e.i18n.T(MsgCronListTitle), len(jobs)))
	sb.WriteString("\n")
	sb.WriteString("\n")

	for i, j := range jobs {
		if i > 0 {
			sb.WriteString("\n")
		}

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
		sb.WriteString(fmt.Sprintf("%s %s\n", status, desc))

		sb.WriteString(fmt.Sprintf("ID: %s\n", j.ID))

		human := CronExprToHuman(j.CronExpr, lang)
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
				sb.WriteString(fmt.Sprintf(" (failed: %s)", truncateStr(j.LastError, 40)))
			}
			sb.WriteString("\n")
		}
	}

	sb.WriteString(fmt.Sprintf("\n%s", e.i18n.T(MsgCronListFooter)))
	e.reply(p, msg.ReplyCtx, sb.String())
}

func (e *Engine) cmdCronDel(p Platform, msg *Message, args []string) {
	if len(args) == 0 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgCronDelUsage))
		return
	}
	id := args[0]
	if e.cronScheduler.RemoveJob(id) {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCronDeleted), id))
	} else {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCronNotFound), id))
	}
}

func (e *Engine) cmdCronToggle(p Platform, msg *Message, args []string, enable bool) {
	if len(args) == 0 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgCronDelUsage))
		return
	}
	id := args[0]
	var err error
	if enable {
		err = e.cronScheduler.EnableJob(id)
	} else {
		err = e.cronScheduler.DisableJob(id)
	}
	if err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgError, err))
		return
	}
	if enable {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCronEnabled), id))
	} else {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCronDisabled), id))
	}
}

func (e *Engine) cmdCronMute(p Platform, msg *Message, args []string, mute bool) {
	if len(args) == 0 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgCronDelUsage))
		return
	}
	id := args[0]
	if !e.cronScheduler.Store().SetMute(id, mute) {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCronNotFound), id))
		return
	}
	if mute {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCronMuted), id))
	} else {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCronUnmuted), id))
	}
}

func (e *Engine) cmdCronSetup(p Platform, msg *Message) {
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
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCronSetupOK), baseName))
	}
}

// ──────────────────────────────────────────────────────────────
// Heartbeat management commands
// ──────────────────────────────────────────────────────────────

func (e *Engine) cmdHeartbeat(p Platform, msg *Message, args []string) {
	if e.heartbeatScheduler == nil {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgHeartbeatNotAvailable))
		return
	}

	status := e.heartbeatScheduler.Status(e.name)
	if status == nil {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgHeartbeatNotAvailable))
		return
	}

	sub := "status"
	if len(args) > 0 {
		sub = matchSubCommand(strings.ToLower(args[0]), []string{
			"status", "pause", "stop", "resume", "start", "run", "trigger", "interval",
		})
	}

	switch sub {
	case "status", "":
		if supportsCards(p) {
			e.replyWithCard(p, msg.ReplyCtx, e.renderHeartbeatCard())
			return
		}
		e.cmdHeartbeatStatusText(p, msg, status)
	case "pause", "stop":
		e.heartbeatScheduler.Pause(e.name)
		if supportsCards(p) {
			e.replyWithCard(p, msg.ReplyCtx, e.renderHeartbeatCard())
		} else {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgHeartbeatPaused))
		}
	case "resume", "start":
		e.heartbeatScheduler.Resume(e.name)
		if supportsCards(p) {
			e.replyWithCard(p, msg.ReplyCtx, e.renderHeartbeatCard())
		} else {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgHeartbeatResumed))
		}
	case "run", "trigger":
		e.heartbeatScheduler.TriggerNow(e.name)
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgHeartbeatTriggered))
	case "interval":
		if len(args) < 2 {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgHeartbeatUsage))
			return
		}
		mins, err := strconv.Atoi(args[1])
		if err != nil || mins <= 0 {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgHeartbeatInvalidMins))
			return
		}
		e.heartbeatScheduler.SetInterval(e.name, mins)
		if supportsCards(p) {
			e.replyWithCard(p, msg.ReplyCtx, e.renderHeartbeatCard())
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgHeartbeatInterval), mins))
		}
	default:
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgHeartbeatUsage))
	}
}

func (e *Engine) cmdHeartbeatStatusText(p Platform, msg *Message, st *HeartbeatStatus) {
	stateStr, yesNo := e.heartbeatLocalizedHelpers()

	lastRunStr := ""
	if !st.LastRun.IsZero() {
		lang := e.i18n.CurrentLang()
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

	e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgHeartbeatStatus),
		stateStr(st.Paused),
		st.IntervalMins,
		yesNo(st.OnlyWhenIdle),
		yesNo(st.Silent),
		st.RunCount,
		st.ErrorCount,
		st.SkippedBusy,
		lastRunStr,
	))
}

func (e *Engine) heartbeatLocalizedHelpers() (stateStr func(paused bool) string, yesNo func(bool) string) {
	lang := e.i18n.CurrentLang()
	switch lang {
	case LangChinese, LangTraditionalChinese:
		stateStr = func(paused bool) string {
			if paused {
				return "⏸ 已暂停"
			}
			return "▶️ 运行中"
		}
		yesNo = func(b bool) string {
			if b {
				return "是"
			}
			return "否"
		}
	case LangJapanese:
		stateStr = func(paused bool) string {
			if paused {
				return "⏸ 一時停止"
			}
			return "▶️ 実行中"
		}
		yesNo = func(b bool) string {
			if b {
				return "はい"
			}
			return "いいえ"
		}
	default:
		stateStr = func(paused bool) string {
			if paused {
				return "⏸ paused"
			}
			return "▶️ running"
		}
		yesNo = func(b bool) string {
			if b {
				return "yes"
			}
			return "no"
		}
	}
	return
}
