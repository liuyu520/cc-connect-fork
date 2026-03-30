package core

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"log/slog"
	"strings"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
)

func init() {
	// 将 go-sql-driver 的内部日志（如 "unexpected EOF"）桥接到 slog，
	// 确保 MySQL 底层错误在 cc-connect 日志体系中可见。
	_ = mysqldriver.SetLogger(log.New(slogWriter{}, "[mysql] ", 0))
}

// slogWriter 将 go-sql-driver 的 log.Logger 输出转发到 slog.Error。
type slogWriter struct{}

func (slogWriter) Write(p []byte) (int, error) {
	msg := strings.TrimSpace(string(p))
	if msg != "" {
		slog.Error("chatstore: mysql driver", "detail", msg)
	}
	return len(p), nil
}

// redactDSN masks the password in a MySQL DSN for safe logging.
// e.g. "user:pass@tcp(host:port)/db" → "user:***@tcp(host:port)/db"
func redactDSN(dsn string) string {
	at := strings.Index(dsn, "@")
	if at < 0 {
		return dsn
	}
	colon := strings.Index(dsn[:at], ":")
	if colon < 0 {
		return dsn
	}
	return dsn[:colon+1] + "***" + dsn[at:]
}

// MySQLChatStoreConfig holds MySQL connection parameters.
type MySQLChatStoreConfig struct {
	DSN          string // go-sql-driver/mysql DSN 格式
	MaxOpenConns int    // 最大打开连接数, 默认 10
	MaxIdleConns int    // 最大空闲连接数, 默认 5
}

// MySQLChatStore implements ChatStore backed by MySQL.
// 所有写入操作都是异步的，通过内部 channel 缓冲后批量执行。
type MySQLChatStore struct {
	db      *sql.DB
	msgCh   chan ChatMessage  // 异步消息写入队列
	sessCh  chan ChatSessionInfo  // 异步会话写入队列
	closeCh chan struct{}     // 关闭信号
	doneCh  chan struct{}     // 写入协程退出完成信号
}

const (
	// 异步写入队列大小
	chatStoreQueueSize = 1024

	// 建表 DDL: 聊天会话表
	createSessionsTableSQL = `
CREATE TABLE IF NOT EXISTS cc_sessions (
    id               BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    session_id       VARCHAR(64)  NOT NULL COMMENT 'cc-connect 内部会话ID',
    session_key      VARCHAR(255) NOT NULL COMMENT '用户上下文键',
    project          VARCHAR(128) NOT NULL DEFAULT '' COMMENT '项目名称',
    agent_type       VARCHAR(64)  NOT NULL DEFAULT '' COMMENT 'agent类型',
    agent_session_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT 'agent端的会话ID',
    name             VARCHAR(255) NOT NULL DEFAULT '' COMMENT '会话名称',
    biz_type         VARCHAR(32)  NOT NULL DEFAULT 'im' COMMENT '业务类型: im=IM平台, vibe=Vibe Coding',
    created_at       DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at       DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    UNIQUE KEY uk_session_id (session_id),
    KEY idx_session_key (session_key),
    KEY idx_project (project),
    KEY idx_biz_type (biz_type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT='聊天会话表'`

	// 建表 DDL: 聊天记录表
	createMessagesTableSQL = `
CREATE TABLE IF NOT EXISTS cc_chat_messages (
    id          BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    session_id  VARCHAR(64)  NOT NULL COMMENT '关联的会话ID',
    role        VARCHAR(16)  NOT NULL COMMENT 'user 或 assistant',
    content     LONGTEXT     NOT NULL COMMENT '消息内容',
    platform    VARCHAR(32)  NOT NULL DEFAULT '' COMMENT '来源平台',
    user_id     VARCHAR(128) NOT NULL DEFAULT '' COMMENT '平台用户ID',
    user_name   VARCHAR(128) NOT NULL DEFAULT '' COMMENT '用户显示名',
    message_id  VARCHAR(128) NOT NULL DEFAULT '' COMMENT '平台消息ID',
    biz_type    VARCHAR(32)  NOT NULL DEFAULT 'im' COMMENT '业务类型: im=IM平台, vibe=Vibe Coding',
    created_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    KEY idx_session_id (session_id),
    KEY idx_created_at (created_at),
    KEY idx_user_id (user_id),
    KEY idx_biz_type (biz_type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT='聊天记录表'`

	// 插入聊天记录 SQL
	insertMessageSQL = `
INSERT INTO cc_chat_messages (session_id, role, content, platform, user_id, user_name, message_id, biz_type, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

	// upsert 会话 SQL (INSERT ... ON DUPLICATE KEY UPDATE)
	upsertSessionSQL = `
INSERT INTO cc_sessions (session_id, session_key, project, agent_type, agent_session_id, name, biz_type)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
    session_key      = VALUES(session_key),
    agent_type       = VALUES(agent_type),
    agent_session_id = VALUES(agent_session_id),
    name             = VALUES(name),
    biz_type         = VALUES(biz_type)`

	// 升级已有表：为旧表添加 biz_type 字段（如果不存在）
	alterSessionsAddBizTypeSQL = `
ALTER TABLE cc_sessions ADD COLUMN biz_type VARCHAR(32) NOT NULL DEFAULT 'im'
  COMMENT '业务类型: im=IM平台, vibe=Vibe Coding'`

	alterMessagesAddBizTypeSQL = `
ALTER TABLE cc_chat_messages ADD COLUMN biz_type VARCHAR(32) NOT NULL DEFAULT 'im'
  COMMENT '业务类型: im=IM平台, vibe=Vibe Coding'`

	// 查询会话列表 SQL（按 biz_type 过滤，按更新时间倒序）
	listSessionsSQL = `
SELECT s.session_id, s.session_key, s.project, s.agent_type, s.agent_session_id,
       s.name, s.biz_type, s.created_at, s.updated_at,
       COALESCE(mc.cnt, 0) AS message_count,
       COALESCE(mc.last_content, '') AS last_message
FROM cc_sessions s
LEFT JOIN (
    SELECT session_id, COUNT(*) AS cnt,
           SUBSTRING(MAX(CONCAT(LPAD(id, 20, '0'), content)), 21) AS last_content
    FROM cc_chat_messages
    GROUP BY session_id
) mc ON mc.session_id = s.session_id
WHERE s.biz_type = ?
ORDER BY s.updated_at DESC
LIMIT ?`

	// 查询所有会话列表 SQL（不过滤 biz_type，按更新时间倒序）
	listAllSessionsSQL = `
SELECT s.session_id, s.session_key, s.project, s.agent_type, s.agent_session_id,
       s.name, s.biz_type, s.created_at, s.updated_at,
       COALESCE(mc.cnt, 0) AS message_count,
       COALESCE(mc.last_content, '') AS last_message
FROM cc_sessions s
LEFT JOIN (
    SELECT session_id, COUNT(*) AS cnt,
           SUBSTRING(MAX(CONCAT(LPAD(id, 20, '0'), content)), 21) AS last_content
    FROM cc_chat_messages
    GROUP BY session_id
) mc ON mc.session_id = s.session_id
ORDER BY s.updated_at DESC
LIMIT ?`

	// 查询会话消息 SQL（按创建时间正序）
	getMessagesSQL = `
SELECT id, session_id, role, content, platform, user_id, user_name, biz_type, created_at
FROM cc_chat_messages
WHERE session_id = ?
ORDER BY created_at ASC
LIMIT ?`
)

// NewMySQLChatStore creates a new MySQL-backed ChatStore.
// 自动建表，连接失败时返回错误（调用方可据此降级）。
func NewMySQLChatStore(cfg MySQLChatStoreConfig) (*MySQLChatStore, error) {
	if cfg.DSN == "" {
		return nil, fmt.Errorf("chatstore: MySQL DSN is empty")
	}

	slog.Info("chatstore: connecting to MySQL...", "dsn", redactDSN(cfg.DSN))

	db, err := sql.Open("mysql", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("chatstore: open MySQL: %w", err)
	}

	// 配置连接池
	maxOpen := cfg.MaxOpenConns
	if maxOpen <= 0 {
		maxOpen = 10
	}
	maxIdle := cfg.MaxIdleConns
	if maxIdle <= 0 {
		maxIdle = 5
	}
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(5 * time.Minute)

	// 测试连接
	pingStart := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		slog.Error("chatstore: MySQL ping failed",
			"dsn", redactDSN(cfg.DSN),
			"elapsed", time.Since(pingStart).String(),
			"error", err,
			"hint", "check: 1) DSN format user:pass@tcp(host:port)/db 2) network connectivity 3) MySQL server status 4) user privileges",
		)
		return nil, fmt.Errorf("chatstore: ping MySQL (%s): %w", redactDSN(cfg.DSN), err)
	}
	slog.Info("chatstore: MySQL ping ok", "elapsed", time.Since(pingStart).String())

	// 自动建表
	if _, err := db.ExecContext(ctx, createSessionsTableSQL); err != nil {
		db.Close()
		slog.Error("chatstore: create sessions table failed", "error", err, "sql", "CREATE TABLE IF NOT EXISTS cc_sessions ...")
		return nil, fmt.Errorf("chatstore: create sessions table: %w", err)
	}
	if _, err := db.ExecContext(ctx, createMessagesTableSQL); err != nil {
		db.Close()
		slog.Error("chatstore: create messages table failed", "error", err, "sql", "CREATE TABLE IF NOT EXISTS cc_chat_messages ...")
		return nil, fmt.Errorf("chatstore: create messages table: %w", err)
	}
	slog.Info("chatstore: tables ensured (cc_sessions, cc_chat_messages)")

	// 升级旧表：添加 biz_type 字段（如果不存在则忽略错误）
	if _, err := db.ExecContext(ctx, alterSessionsAddBizTypeSQL); err != nil {
		if !strings.Contains(err.Error(), "Duplicate column") {
			slog.Info("chatstore: alter cc_sessions add biz_type (may already exist)", "error", err)
		}
	}
	if _, err := db.ExecContext(ctx, alterMessagesAddBizTypeSQL); err != nil {
		if !strings.Contains(err.Error(), "Duplicate column") {
			slog.Info("chatstore: alter cc_chat_messages add biz_type (may already exist)", "error", err)
		}
	}

	store := &MySQLChatStore{
		db:      db,
		msgCh:   make(chan ChatMessage, chatStoreQueueSize),
		sessCh:  make(chan ChatSessionInfo, chatStoreQueueSize),
		closeCh: make(chan struct{}),
		doneCh:  make(chan struct{}),
	}

	// 启动异步写入协程
	go store.writeLoop()

	slog.Info("chatstore: MySQL connected", "dsn", redactDSN(cfg.DSN))
	return store, nil
}

// SaveMessage 将消息投递到异步写入队列。
// 如果队列已满则丢弃并记录警告日志，绝不阻塞调用方。
func (s *MySQLChatStore) SaveMessage(_ context.Context, msg ChatMessage) {
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now()
	}
	select {
	case s.msgCh <- msg:
	default:
		slog.Error("chatstore: message queue full, dropping message",
			"session_id", msg.SessionID, "role", msg.Role)
	}
}

// EnsureSession 将会话信息投递到异步写入队列。
func (s *MySQLChatStore) EnsureSession(_ context.Context, info ChatSessionInfo) {
	select {
	case s.sessCh <- info:
	default:
		slog.Error("chatstore: session queue full, dropping session info",
			"session_id", info.SessionID)
	}
}

// Close 停止写入协程并关闭数据库连接。
// 会等待队列中剩余消息写入完成（最多 5 秒）。
func (s *MySQLChatStore) Close() error {
	close(s.closeCh)

	// 等待写入协程退出，最多等 5 秒
	select {
	case <-s.doneCh:
	case <-time.After(5 * time.Second):
		slog.Error("chatstore: close timed out waiting for write loop")
	}

	return s.db.Close()
}

// Ping checks if the underlying database connection is alive.
func (s *MySQLChatStore) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// writeLoop 是异步写入协程，从 channel 消费数据并写入 MySQL。
// 收到 closeCh 信号后会排空队列再退出。
func (s *MySQLChatStore) writeLoop() {
	defer close(s.doneCh)

	for {
		select {
		case msg := <-s.msgCh:
			s.doSaveMessage(msg)
		case info := <-s.sessCh:
			s.doEnsureSession(info)
		case <-s.closeCh:
			// 排空队列
			s.drain()
			return
		}
	}
}

// drain 排空队列中剩余的数据。
func (s *MySQLChatStore) drain() {
	for {
		select {
		case msg := <-s.msgCh:
			s.doSaveMessage(msg)
		case info := <-s.sessCh:
			s.doEnsureSession(info)
		default:
			return
		}
	}
}

// retryWrite retries fn up to maxRetries times with a fixed delay between attempts.
// If done is non-nil, the delay is interruptible by closing the done channel,
// so the writeLoop can exit promptly during shutdown.
// Returns the last error if all attempts fail.
func retryWrite(maxRetries int, delay time.Duration, done <-chan struct{}, fn func() error) error {
	var err error
	for i := 0; i <= maxRetries; i++ {
		err = fn()
		if err == nil {
			return nil
		}
		if i < maxRetries {
			if done != nil {
				select {
				case <-time.After(delay):
				case <-done:
					return err
				}
			} else {
				time.Sleep(delay)
			}
		}
	}
	return err
}

// doSaveMessage 执行实际的消息插入操作，失败时自动重试。
func (s *MySQLChatStore) doSaveMessage(msg ChatMessage) {
	// biz_type 默认为 "im"
	bizType := msg.BizType
	if bizType == "" {
		bizType = "im"
	}

	start := time.Now()
	err := retryWrite(2, 1*time.Second, s.closeCh, func() error {
		// 每次重试都创建新的 context，避免超时累积
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		_, execErr := s.db.ExecContext(ctx, insertMessageSQL,
			msg.SessionID,
			msg.Role,
			msg.Content,
			msg.Platform,
			msg.UserID,
			msg.UserName,
			msg.MessageID,
			bizType,
			msg.CreatedAt,
		)
		return execErr
	})
	elapsed := time.Since(start)
	if err != nil {
		slog.Error("chatstore: INSERT cc_chat_messages failed after retries",
			"session_id", msg.SessionID,
			"role", msg.Role,
			"biz_type", bizType,
			"platform", msg.Platform,
			"content_len", len(msg.Content),
			"elapsed", elapsed.String(),
			"error", err,
		)
	} else {
		slog.Info("chatstore: message saved",
			"session_id", msg.SessionID,
			"role", msg.Role,
			"biz_type", bizType,
			"content_len", len(msg.Content),
			"elapsed", elapsed.String(),
		)
	}
}

// doEnsureSession 执行实际的会话 upsert 操作，失败时自动重试。
func (s *MySQLChatStore) doEnsureSession(info ChatSessionInfo) {
	// biz_type 默认为 "im"
	bizType := info.BizType
	if bizType == "" {
		bizType = "im"
	}

	start := time.Now()
	err := retryWrite(2, 1*time.Second, s.closeCh, func() error {
		// 每次重试都创建新的 context，避免超时累积
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		_, execErr := s.db.ExecContext(ctx, upsertSessionSQL,
			info.SessionID,
			info.SessionKey,
			info.Project,
			info.AgentType,
			info.AgentSessionID,
			info.Name,
			bizType,
		)
		return execErr
	})
	elapsed := time.Since(start)
	if err != nil {
		slog.Error("chatstore: UPSERT cc_sessions failed after retries",
			"session_id", info.SessionID,
			"session_key", info.SessionKey,
			"project", info.Project,
			"biz_type", bizType,
			"elapsed", elapsed.String(),
			"error", err,
		)
	} else {
		slog.Info("chatstore: session ensured",
			"session_id", info.SessionID,
			"project", info.Project,
			"biz_type", bizType,
			"elapsed", elapsed.String(),
		)
	}
}

// ListSessions 按 biz_type 列出会话，按更新时间倒序。
func (s *MySQLChatStore) ListSessions(ctx context.Context, bizType string, limit int) ([]ChatSessionRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	start := time.Now()
	var rows *sql.Rows
	var err error
	if bizType == "" {
		// 不过滤 biz_type，查询所有会话
		rows, err = s.db.QueryContext(ctx, listAllSessionsSQL, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, listSessionsSQL, bizType, limit)
	}
	if err != nil {
		slog.Error("chatstore: SELECT cc_sessions failed",
			"biz_type", bizType,
			"limit", limit,
			"elapsed", time.Since(start).String(),
			"error", err,
		)
		return nil, fmt.Errorf("chatstore: list sessions: %w", err)
	}
	defer rows.Close()

	var result []ChatSessionRecord
	for rows.Next() {
		var r ChatSessionRecord
		if err := rows.Scan(
			&r.SessionID, &r.SessionKey, &r.Project, &r.AgentType, &r.AgentSessionID,
			&r.Name, &r.BizType, &r.CreatedAt, &r.UpdatedAt,
			&r.MessageCount, &r.LastMessage,
		); err != nil {
			slog.Error("chatstore: scan cc_sessions row failed", "error", err)
			return nil, fmt.Errorf("chatstore: scan session row: %w", err)
		}
		// 截取 last_message 预览
		if len(r.LastMessage) > 200 {
			r.LastMessage = r.LastMessage[:200]
		}
		result = append(result, r)
	}
	slog.Info("chatstore: list sessions ok",
		"biz_type", bizType,
		"count", len(result),
		"elapsed", time.Since(start).String(),
	)
	return result, rows.Err()
}

// GetMessages 读取指定会话的消息列表，按创建时间正序。
func (s *MySQLChatStore) GetMessages(ctx context.Context, sessionID string, limit int) ([]ChatMessageRecord, error) {
	if limit <= 0 {
		limit = 200
	}
	start := time.Now()
	rows, err := s.db.QueryContext(ctx, getMessagesSQL, sessionID, limit)
	if err != nil {
		slog.Error("chatstore: SELECT cc_chat_messages failed",
			"session_id", sessionID,
			"limit", limit,
			"elapsed", time.Since(start).String(),
			"error", err,
		)
		return nil, fmt.Errorf("chatstore: get messages: %w", err)
	}
	defer rows.Close()

	var result []ChatMessageRecord
	for rows.Next() {
		var r ChatMessageRecord
		if err := rows.Scan(
			&r.ID, &r.SessionID, &r.Role, &r.Content, &r.Platform,
			&r.UserID, &r.UserName, &r.BizType, &r.CreatedAt,
		); err != nil {
			slog.Error("chatstore: scan cc_chat_messages row failed", "session_id", sessionID, "error", err)
			return nil, fmt.Errorf("chatstore: scan message row: %w", err)
		}
		result = append(result, r)
	}
	slog.Info("chatstore: get messages ok",
		"session_id", sessionID,
		"count", len(result),
		"elapsed", time.Since(start).String(),
	)
	return result, rows.Err()
}
