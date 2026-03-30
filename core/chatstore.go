package core

import (
	"context"
	"time"
)

// ChatMessage represents a single chat message to be persisted.
// 对应 cc_chat_messages 表的一行记录。
type ChatMessage struct {
	SessionID string    // cc-connect 内部会话ID
	Role      string    // "user" 或 "assistant"
	Content   string    // 消息内容
	Platform  string    // 来源平台 (feishu/telegram/discord等)
	UserID    string    // 平台用户ID
	UserName  string    // 用户显示名
	MessageID string    // 平台消息ID
	BizType   string    // 业务类型: "im"=IM平台, "vibe"=Vibe Coding
	CreatedAt time.Time // 消息时间戳
}

// ChatSessionInfo represents session metadata to be persisted.
// 对应 cc_sessions 表的一行记录 (upsert 语义)。
type ChatSessionInfo struct {
	SessionID      string // cc-connect 内部会话ID
	SessionKey     string // 用户上下文键, 如 "feishu:{chatID}:{userID}"
	Project        string // 项目名称
	AgentType      string // agent类型
	AgentSessionID string // agent端的会话ID
	Name           string // 会话名称
	BizType        string // 业务类型: "im"=IM平台, "vibe"=Vibe Coding
}

// ChatSessionRecord 是从数据库读取的完整会话记录（含时间戳等）。
type ChatSessionRecord struct {
	SessionID      string    `json:"session_id"`
	SessionKey     string    `json:"session_key"`
	Project        string    `json:"project"`
	AgentType      string    `json:"agent_type"`
	AgentSessionID string    `json:"agent_session_id"`
	Name           string    `json:"name"`
	BizType        string    `json:"biz_type"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	MessageCount   int       `json:"message_count"`   // 消息总数
	LastMessage    string    `json:"last_message"`     // 最后一条消息预览
}

// ChatMessageRecord 是从数据库读取的完整消息记录。
type ChatMessageRecord struct {
	ID        int64     `json:"id"`
	SessionID string    `json:"session_id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Platform  string    `json:"platform"`
	UserID    string    `json:"user_id"`
	UserName  string    `json:"user_name"`
	BizType   string    `json:"biz_type"`
	CreatedAt time.Time `json:"created_at"`
}

// ChatStore defines the interface for persisting chat history.
// 实现者需要保证线程安全。当 ChatStore 为 nil 时表示未启用持久化。
type ChatStore interface {
	// SaveMessage 保存一条聊天记录到持久化存储。
	// 实现应为异步、非阻塞的；失败时仅记录日志，不返回错误给调用方。
	SaveMessage(ctx context.Context, msg ChatMessage)

	// EnsureSession 确保会话元信息存在于持久化存储中 (upsert 语义)。
	// 如果记录已存在则更新 agent_session_id/name 等字段。
	EnsureSession(ctx context.Context, info ChatSessionInfo)

	// ListSessions 按 biz_type 列出会话，按更新时间倒序，最多返回 limit 条。
	ListSessions(ctx context.Context, bizType string, limit int) ([]ChatSessionRecord, error)

	// GetMessages 读取指定会话的消息列表，按创建时间正序，最多返回 limit 条。
	GetMessages(ctx context.Context, sessionID string, limit int) ([]ChatMessageRecord, error)

	// Close 关闭底层连接资源。
	Close() error

	// Ping checks if the underlying database connection is alive.
	Ping(ctx context.Context) error
}
