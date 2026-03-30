package core

import (
	"context"
	"sync"
	"testing"
	"time"
)

// stubChatStore is a test-only ChatStore that records all calls in-memory.
type stubChatStore struct {
	mu       sync.Mutex
	messages []ChatMessage
	sessions []ChatSessionInfo
	closed   bool
}

func (s *stubChatStore) SaveMessage(_ context.Context, msg ChatMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, msg)
}

func (s *stubChatStore) EnsureSession(_ context.Context, info ChatSessionInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = append(s.sessions, info)
}

func (s *stubChatStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

// Ping always returns nil for the stub (healthy).
func (s *stubChatStore) Ping(_ context.Context) error {
	return nil
}

func (s *stubChatStore) ListSessions(_ context.Context, bizType string, limit int) ([]ChatSessionRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []ChatSessionRecord
	for _, sess := range s.sessions {
		if sess.BizType == bizType || bizType == "" {
			result = append(result, ChatSessionRecord{
				SessionID: sess.SessionID,
				Name:      sess.Name,
				BizType:   sess.BizType,
			})
		}
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result, nil
}

func (s *stubChatStore) GetMessages(_ context.Context, sessionID string, limit int) ([]ChatMessageRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []ChatMessageRecord
	for _, msg := range s.messages {
		if msg.SessionID == sessionID {
			result = append(result, ChatMessageRecord{
				SessionID: msg.SessionID,
				Role:      msg.Role,
				Content:   msg.Content,
				BizType:   msg.BizType,
				CreatedAt: msg.CreatedAt,
			})
		}
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result, nil
}

func (s *stubChatStore) getMessages() []ChatMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]ChatMessage, len(s.messages))
	copy(cp, s.messages)
	return cp
}

func (s *stubChatStore) getSessions() []ChatSessionInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]ChatSessionInfo, len(s.sessions))
	copy(cp, s.sessions)
	return cp
}

func TestStubChatStore_SaveAndEnsure(t *testing.T) {
	store := &stubChatStore{}

	store.SaveMessage(context.Background(), ChatMessage{
		SessionID: "s1",
		Role:      "user",
		Content:   "hello",
		Platform:  "telegram",
		UserID:    "u1",
		UserName:  "Alice",
		MessageID: "m1",
		CreatedAt: time.Now(),
	})

	store.SaveMessage(context.Background(), ChatMessage{
		SessionID: "s1",
		Role:      "assistant",
		Content:   "hi there",
	})

	store.EnsureSession(context.Background(), ChatSessionInfo{
		SessionID:      "s1",
		SessionKey:     "telegram:chat1:u1",
		Project:        "myproject",
		AgentType:      "claudecode",
		AgentSessionID: "agent-s1",
		Name:           "test session",
	})

	msgs := store.getMessages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content != "hello" {
		t.Errorf("unexpected first message: %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || msgs[1].Content != "hi there" {
		t.Errorf("unexpected second message: %+v", msgs[1])
	}

	sessions := store.getSessions()
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].Project != "myproject" {
		t.Errorf("unexpected session project: %s", sessions[0].Project)
	}
}

func TestStubChatStore_Close(t *testing.T) {
	store := &stubChatStore{}
	if err := store.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !store.closed {
		t.Error("expected closed to be true")
	}
}

func TestRedactDSN(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "hw3:Y8CVZTCbjqRYG#@tcp(172.17.6.38:3309)/pus20181107?charset=utf8mb4",
			expected: "hw3:***@tcp(172.17.6.38:3309)/pus20181107?charset=utf8mb4",
		},
		{
			input:    "root:password@tcp(localhost:3306)/test",
			expected: "root:***@tcp(localhost:3306)/test",
		},
		{
			input:    "tcp(localhost:3306)/test", // no user:pass
			expected: "tcp(localhost:3306)/test",
		},
		{
			input:    "", // empty
			expected: "",
		},
	}
	for _, tt := range tests {
		got := redactDSN(tt.input)
		if got != tt.expected {
			t.Errorf("redactDSN(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestEngine_SetChatStore(t *testing.T) {
	agent := &stubAgent{}
	p := &stubPlatform{}
	e := NewEngine("test-project", agent, []Platform{p}, t.TempDir()+"/sessions.json", LangEnglish)

	store := &stubChatStore{}
	e.SetChatStore(store)

	if e.chatStore != store {
		t.Error("expected chatStore to be set on engine")
	}
}
