package core

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite" // 纯 Go SQLite 驱动，无 CGO 依赖
)

// SQLiteIMUserStore 使用 SQLite 记录 IM 用户活动。
// 写入通过 channel 异步执行，不阻塞调用方。
type SQLiteIMUserStore struct {
	db      *sql.DB
	writeCh chan imUserWriteReq
	once    sync.Once
	doneCh  chan struct{}
}

type imUserWriteReq struct {
	project, sessionKey, platform, userID, userName string
}

// NewSQLiteIMUserStore 创建基于 SQLite 的用户活动记录存储。
// dbDir 为数据目录路径，数据库文件位于 {dbDir}/patrol/imuser.db。
func NewSQLiteIMUserStore(dbDir string) (*SQLiteIMUserStore, error) {
	dir := filepath.Join(dbDir, "patrol")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("patrol: mkdir %s: %w", dir, err)
	}

	dbPath := filepath.Join(dir, "imuser.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("patrol: open sqlite %s: %w", dbPath, err)
	}

	// SQLite 优化：WAL 模式 + busy timeout
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("patrol: %s: %w", pragma, err)
		}
	}

	// 建表
	createSQL := `
	CREATE TABLE IF NOT EXISTS im_user_activity (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		project     TEXT NOT NULL,
		session_key TEXT NOT NULL,
		platform    TEXT NOT NULL DEFAULT '',
		user_id     TEXT NOT NULL DEFAULT '',
		user_name   TEXT NOT NULL DEFAULT '',
		last_seen   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(project, session_key)
	);
	CREATE INDEX IF NOT EXISTS idx_im_user_project_last_seen
		ON im_user_activity(project, last_seen DESC);
	`
	if _, err := db.Exec(createSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("patrol: create table: %w", err)
	}

	s := &SQLiteIMUserStore{
		db:      db,
		writeCh: make(chan imUserWriteReq, 256),
		doneCh:  make(chan struct{}),
	}
	go s.writeLoop()
	return s, nil
}

// RecordActivity 异步记录用户活动（通过 channel 投递，不阻塞调用方）。
func (s *SQLiteIMUserStore) RecordActivity(project, sessionKey, platform, userID, userName string) {
	select {
	case s.writeCh <- imUserWriteReq{project, sessionKey, platform, userID, userName}:
	default:
		slog.Debug("patrol: imuser write channel full, dropping", "project", project, "session_key", sessionKey)
	}
}

// MostRecentUser 返回指定项目最近一次活动的用户信息。
func (s *SQLiteIMUserStore) MostRecentUser(project string) *IMUserActivity {
	row := s.db.QueryRow(
		`SELECT project, session_key, platform, user_id, user_name, last_seen
		 FROM im_user_activity WHERE project = ? ORDER BY last_seen DESC LIMIT 1`,
		project,
	)
	var a IMUserActivity
	var lastSeen string
	if err := row.Scan(&a.Project, &a.SessionKey, &a.Platform, &a.UserID, &a.UserName, &lastSeen); err != nil {
		return nil
	}
	a.LastSeenAt, _ = time.Parse("2006-01-02 15:04:05", lastSeen)
	return &a
}

// Close 停止写入 goroutine 并关闭数据库连接。
func (s *SQLiteIMUserStore) Close() error {
	s.once.Do(func() {
		close(s.writeCh)
		<-s.doneCh // 等待 writeLoop 退出
	})
	return s.db.Close()
}

// writeLoop 从 channel 读取写入请求并执行 upsert。
func (s *SQLiteIMUserStore) writeLoop() {
	defer close(s.doneCh)

	upsertSQL := `
	INSERT INTO im_user_activity (project, session_key, platform, user_id, user_name, last_seen)
	VALUES (?, ?, ?, ?, ?, datetime('now'))
	ON CONFLICT(project, session_key) DO UPDATE SET
		platform  = excluded.platform,
		user_id   = excluded.user_id,
		user_name = excluded.user_name,
		last_seen = datetime('now')
	`

	cleanupTicker := time.NewTicker(6 * time.Hour)
	defer cleanupTicker.Stop()

	for {
		select {
		case req, ok := <-s.writeCh:
			if !ok {
				return // channel 关闭
			}
			if _, err := s.db.Exec(upsertSQL, req.project, req.sessionKey, req.platform, req.userID, req.userName); err != nil {
				slog.Warn("patrol: imuser upsert failed", "error", err, "project", req.project)
			}
		case <-cleanupTicker.C:
			// 定期清理 30 天前的记录
			if _, err := s.db.Exec(`DELETE FROM im_user_activity WHERE last_seen < datetime('now', '-30 days')`); err != nil {
				slog.Debug("patrol: cleanup old records failed", "error", err)
			}
		}
	}
}
