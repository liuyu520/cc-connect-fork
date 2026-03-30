package core

import (
	"log/slog"
	"strings"
	"sync"
	"time"
)

// PatrolRuntimeConfig holds runtime patrol settings for a single project.
type PatrolRuntimeConfig struct {
	Enabled      bool
	IntervalMins int    // minutes between patrols; default 60
	Message      string // fixed message to send; empty = use i18n default
}

// PatrolScheduler manages periodic idle-user notification across projects.
// When a project's agent is idle, it sends a fixed message to the most recent IM user.
type PatrolScheduler struct {
	mu      sync.Mutex
	entries map[string]*patrolEntry // project name → entry
	store   IMUserStore
	stopCh  chan struct{}
	stopped bool
}

type patrolEntry struct {
	project string
	config  PatrolRuntimeConfig
	engine  *Engine
	ticker  *time.Ticker
	stopCh  chan struct{}

	// Runtime stats
	runCount    int
	skippedBusy int
	lastRun     time.Time
	lastError   string
}

// NewPatrolScheduler creates a new patrol scheduler backed by the given user store.
func NewPatrolScheduler(store IMUserStore) *PatrolScheduler {
	return &PatrolScheduler{
		entries: make(map[string]*patrolEntry),
		store:   store,
		stopCh:  make(chan struct{}),
	}
}

// Register adds a project to the patrol scheduler.
func (ps *PatrolScheduler) Register(project string, cfg PatrolRuntimeConfig, engine *Engine) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if cfg.IntervalMins <= 0 {
		cfg.IntervalMins = 60
	}

	ps.entries[project] = &patrolEntry{
		project: project,
		config:  cfg,
		engine:  engine,
		stopCh:  make(chan struct{}),
	}
	slog.Info("patrol: registered", "project", project, "interval_mins", cfg.IntervalMins)
}

// Start launches all registered patrol goroutines.
func (ps *PatrolScheduler) Start() {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	for _, entry := range ps.entries {
		if entry.config.Enabled {
			entry.ticker = time.NewTicker(time.Duration(entry.config.IntervalMins) * time.Minute)
			go ps.run(entry)
		}
	}
}

// Stop stops all patrol goroutines and closes the user store.
func (ps *PatrolScheduler) Stop() {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.stopped {
		return
	}
	ps.stopped = true
	close(ps.stopCh)
	for _, entry := range ps.entries {
		if entry.ticker != nil {
			entry.ticker.Stop()
		}
		close(entry.stopCh)
	}
	if ps.store != nil {
		ps.store.Close()
	}
}

// RecordUserActivity delegates to the underlying IMUserStore.
// Safe to call from any goroutine.
func (ps *PatrolScheduler) RecordUserActivity(project, sessionKey, platform, userID, userName string) {
	if ps.store != nil {
		ps.store.RecordActivity(project, sessionKey, platform, userID, userName)
	}
}

// run is the ticker loop for a single project.
func (ps *PatrolScheduler) run(entry *patrolEntry) {
	for {
		select {
		case <-ps.stopCh:
			return
		case <-entry.stopCh:
			return
		case <-entry.ticker.C:
			ps.execute(entry)
		}
	}
}

// execute performs one patrol check for a project:
// 1. Query most recent IM user from SQLite
// 2. Check if that user's session is idle (TryLock)
// 3. If idle, send a fixed message via platform
func (ps *PatrolScheduler) execute(entry *patrolEntry) {
	// 1. 查询最近活跃的 IM 用户
	activity := ps.store.MostRecentUser(entry.project)
	if activity == nil {
		slog.Debug("patrol: no recent user found", "project", entry.project)
		return
	}

	// 2. 检查是否空闲
	session := entry.engine.sessions.GetOrCreateActive(activity.SessionKey)
	if !session.TryLock() {
		slog.Debug("patrol: session busy, skipping", "project", entry.project, "session_key", activity.SessionKey)
		ps.mu.Lock()
		entry.skippedBusy++
		ps.mu.Unlock()
		return
	}
	session.Unlock() // 只是检查空闲，立即释放

	// 3. 从 sessionKey 提取 platform 名称（格式: "feishu:chatID:userID"）
	platformName := activity.Platform
	if platformName == "" {
		// fallback：从 sessionKey 提取前缀
		if idx := strings.Index(activity.SessionKey, ":"); idx > 0 {
			platformName = activity.SessionKey[:idx]
		}
	}
	if platformName == "" {
		slog.Warn("patrol: cannot determine platform", "project", entry.project, "session_key", activity.SessionKey)
		return
	}

	// 4. 查找平台实例
	var targetPlatform Platform
	for _, p := range entry.engine.platforms {
		if p.Name() == platformName {
			targetPlatform = p
			break
		}
	}
	if targetPlatform == nil {
		slog.Warn("patrol: platform not found", "project", entry.project, "platform", platformName)
		return
	}

	// 5. 检查平台是否支持 ReplyContextReconstructor
	rc, ok := targetPlatform.(ReplyContextReconstructor)
	if !ok {
		slog.Debug("patrol: platform does not support ReconstructReplyCtx", "project", entry.project, "platform", platformName)
		return
	}

	// 6. 重建回复上下文
	replyCtx, err := rc.ReconstructReplyCtx(activity.SessionKey)
	if err != nil {
		slog.Warn("patrol: reconstruct reply ctx failed", "project", entry.project, "error", err)
		ps.mu.Lock()
		entry.lastError = err.Error()
		ps.mu.Unlock()
		return
	}

	// 7. 确定消息内容
	message := entry.config.Message
	if message == "" {
		message = entry.engine.i18n.T(MsgPatrolIdle)
	}

	// 8. 发送消息（不经过 agent，直接发送到 IM）
	entry.engine.send(targetPlatform, replyCtx, message)
	slog.Info("patrol: idle notification sent",
		"project", entry.project,
		"platform", platformName,
		"session_key", activity.SessionKey,
		"user", activity.UserName,
	)

	ps.mu.Lock()
	entry.runCount++
	entry.lastRun = time.Now()
	entry.lastError = ""
	ps.mu.Unlock()
}
