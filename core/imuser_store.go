package core

import "time"

// IMUserStore 记录 IM 用户活动，供 PatrolScheduler 查询最近活跃用户。
// 实现者需保证线程安全。
type IMUserStore interface {
	// RecordActivity 记录用户的一次 IM 活动（发送消息时调用）。
	RecordActivity(project, sessionKey, platform, userID, userName string)

	// MostRecentUser 返回指定项目中最近一次活动的用户信息。
	// 若无记录则返回 nil。
	MostRecentUser(project string) *IMUserActivity

	// Close 关闭底层存储连接。
	Close() error
}

// IMUserActivity 表示一条用户活动记录。
type IMUserActivity struct {
	Project    string
	SessionKey string
	Platform   string
	UserID     string
	UserName   string
	LastSeenAt time.Time
}
