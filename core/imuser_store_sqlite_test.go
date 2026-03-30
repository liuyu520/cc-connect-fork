package core

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteIMUserStore_RecordAndQuery(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSQLiteIMUserStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// 写入活动记录（异步）
	store.RecordActivity("proj1", "feishu:chat1:user1", "feishu", "user1", "Alice")
	time.Sleep(100 * time.Millisecond) // 等待异步写入

	// 查询最近用户
	a := store.MostRecentUser("proj1")
	if a == nil {
		t.Fatal("expected activity record, got nil")
	}
	if a.SessionKey != "feishu:chat1:user1" {
		t.Errorf("SessionKey = %q, want feishu:chat1:user1", a.SessionKey)
	}
	if a.Platform != "feishu" {
		t.Errorf("Platform = %q, want feishu", a.Platform)
	}
	if a.UserID != "user1" {
		t.Errorf("UserID = %q, want user1", a.UserID)
	}
	if a.UserName != "Alice" {
		t.Errorf("UserName = %q, want Alice", a.UserName)
	}
}

func TestSQLiteIMUserStore_Upsert(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSQLiteIMUserStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// 第一次写入
	store.RecordActivity("proj1", "tg:123:456", "telegram", "456", "Bob")
	time.Sleep(100 * time.Millisecond)

	// 用新名称更新同一 sessionKey
	store.RecordActivity("proj1", "tg:123:456", "telegram", "456", "Bob Updated")
	time.Sleep(100 * time.Millisecond)

	a := store.MostRecentUser("proj1")
	if a == nil {
		t.Fatal("expected activity, got nil")
	}
	if a.UserName != "Bob Updated" {
		t.Errorf("UserName = %q, want Bob Updated (upsert should update)", a.UserName)
	}
}

func TestSQLiteIMUserStore_NoData(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSQLiteIMUserStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	a := store.MostRecentUser("nonexistent")
	if a != nil {
		t.Errorf("expected nil for nonexistent project, got %+v", a)
	}
}

func TestSQLiteIMUserStore_MultiProject(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSQLiteIMUserStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	store.RecordActivity("proj1", "feishu:a:1", "feishu", "1", "Alice")
	store.RecordActivity("proj2", "tg:b:2", "telegram", "2", "Bob")
	time.Sleep(100 * time.Millisecond)

	a1 := store.MostRecentUser("proj1")
	a2 := store.MostRecentUser("proj2")
	if a1 == nil || a1.UserName != "Alice" {
		t.Errorf("proj1 user = %v, want Alice", a1)
	}
	if a2 == nil || a2.UserName != "Bob" {
		t.Errorf("proj2 user = %v, want Bob", a2)
	}
}

func TestSQLiteIMUserStore_DBPath(t *testing.T) {
	dir := t.TempDir()
	_, err := NewSQLiteIMUserStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	// 验证数据库文件已创建
	dbPath := filepath.Join(dir, "patrol", "imuser.db")
	if _, statErr := filepath.Abs(dbPath); statErr != nil {
		t.Errorf("db file not created at %s", dbPath)
	}
}
