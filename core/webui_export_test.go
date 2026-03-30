package core

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"astrBot_hw", "astrBot_hw"},
		{"my-project", "my-project"},
		{"hello world", "hello_world"},
		{"项目/名称", "export"},
		{"a/b/c.go", "a_b_c_go"},
		{"", "export"},
		{"___", "export"},
		{"normal123", "normal123"},
	}
	for _, tt := range tests {
		got := sanitizeFilename(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBuildExportMarkdown(t *testing.T) {
	s := &WebUIServer{}
	now := time.Date(2026, 3, 28, 14, 30, 0, 0, time.Local)

	req := &ExportRequest{
		SessionName: "test-project",
		Project:     "/home/user/test-project",
		AgentType:   "claudecode",
		SessionID:   "session-123",
		Messages: []ExportMessage{
			{Role: "user", Type: "text", Content: "Hello", Timestamp: now.UnixMilli()},
			{Role: "assistant", Type: "text", Content: "Hi there!", Timestamp: now.Add(time.Second).UnixMilli()},
			{Role: "assistant", Type: "tool_use", Content: "Read file: main.go", ToolName: "Read", Timestamp: now.Add(2 * time.Second).UnixMilli()},
			{Role: "assistant", Type: "tool_result", Content: "package main\n\nfunc main() {}", Timestamp: now.Add(3 * time.Second).UnixMilli()},
			{Role: "assistant", Type: "error", Content: "something went wrong", Timestamp: now.Add(4 * time.Second).UnixMilli()},
			{Role: "assistant", Type: "thinking", Content: "Let me think...", Timestamp: now.Add(5 * time.Second).UnixMilli()},
			{Role: "assistant", Type: "result", Content: "All done!", Timestamp: now.Add(6 * time.Second).UnixMilli()},
		},
	}

	md := s.buildExportMarkdown(req, now)

	// 检查标题
	if !strings.Contains(md, "# Chat Export: test-project") {
		t.Error("missing title")
	}
	// 检查元信息
	if !strings.Contains(md, "| Project | /home/user/test-project |") {
		t.Error("missing project info")
	}
	if !strings.Contains(md, "| Agent | claudecode |") {
		t.Error("missing agent info")
	}
	if !strings.Contains(md, "| Session ID | session-123 |") {
		t.Error("missing session ID")
	}
	// 检查消息数量（thinking 被排除，共 6 条实际导出）
	if !strings.Contains(md, "| Messages | 6 |") {
		t.Errorf("wrong message count, md contains: %s", md)
	}
	// 检查 user 消息
	if !strings.Contains(md, "## User") && !strings.Contains(md, "Hello") {
		t.Error("missing user message")
	}
	// 检查 tool_use 格式
	if !strings.Contains(md, "Tool: Read") {
		t.Error("missing tool_use header")
	}
	if !strings.Contains(md, "> Read file: main.go") {
		t.Error("missing tool_use blockquote")
	}
	// 检查 tool_result 格式（代码块）
	if !strings.Contains(md, "```\npackage main") {
		t.Error("missing tool_result code block")
	}
	// 检查 error 格式
	if !strings.Contains(md, "> Error: something went wrong") {
		t.Error("missing error blockquote")
	}
	// 检查 thinking 被跳过
	if strings.Contains(md, "Let me think") {
		t.Error("thinking should be excluded")
	}
	// 检查 result 格式（当作普通文本）
	if !strings.Contains(md, "All done!") {
		t.Error("missing result message")
	}
}

func TestHandleVibeExportMarkdown_EmptyMessages(t *testing.T) {
	s := &WebUIServer{}

	body := `{"session_name":"test","project":"/test","messages":[]}`
	req := httptest.NewRequest(http.MethodPost, "/api/vibe/export", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleVibeExportMarkdown(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleVibeExportMarkdown_MethodNotAllowed(t *testing.T) {
	s := &WebUIServer{}

	req := httptest.NewRequest(http.MethodGet, "/api/vibe/export", nil)
	w := httptest.NewRecorder()

	s.handleVibeExportMarkdown(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleVibeExportMarkdown_Success(t *testing.T) {
	s := &WebUIServer{}

	exportReq := ExportRequest{
		SessionName: "my-project",
		Project:     "/home/user/my-project",
		AgentType:   "claudecode",
		SessionID:   "sess-1",
		Messages: []ExportMessage{
			{Role: "user", Type: "text", Content: "Hello", Timestamp: 1711612800000},
			{Role: "assistant", Type: "text", Content: "Hi!", Timestamp: 1711612801000},
		},
	}
	bodyBytes, _ := json.Marshal(exportReq)
	req := httptest.NewRequest(http.MethodPost, "/api/vibe/export", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleVibeExportMarkdown(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	// 检查 Content-Type
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/markdown") {
		t.Errorf("expected text/markdown content type, got %s", ct)
	}

	// 检查 Content-Disposition
	cd := w.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") {
		t.Errorf("expected attachment disposition, got %s", cd)
	}
	if !strings.Contains(cd, "my-project_") {
		t.Errorf("expected filename with project name, got %s", cd)
	}
	if !strings.Contains(cd, ".md") {
		t.Errorf("expected .md extension, got %s", cd)
	}

	// 检查返回的 Markdown 内容
	body, _ := io.ReadAll(w.Body)
	md := string(body)
	if !strings.Contains(md, "# Chat Export: my-project") {
		t.Error("missing title in response")
	}
	if !strings.Contains(md, "Hello") {
		t.Error("missing user message in response")
	}
	if !strings.Contains(md, "Hi!") {
		t.Error("missing assistant message in response")
	}
}

// ---------------------------------------------------------------------------
// parseEvent — showToolProcess filtering tests
// ---------------------------------------------------------------------------

// helper: build a minimal webuiSession with showToolProcess set
func newTestWebuiSession(showToolProcess bool) *webuiSession {
	return &webuiSession{
		showToolProcess: showToolProcess,
		pendingInputs:   make(map[string]map[string]any),
	}
}

// helper: build an "assistant" event containing a tool_use block
func makeToolUseEvent(toolName string) map[string]any {
	return map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"content": []any{
				map[string]any{
					"type":  "tool_use",
					"name":  toolName,
					"input": map[string]any{"command": "echo hello"},
				},
			},
		},
	}
}

// helper: build a "user" event containing a tool_result block
func makeToolResultEvent(toolUseID, content string) map[string]any {
	return map[string]any{
		"type": "user",
		"message": map[string]any{
			"content": []any{
				map[string]any{
					"type":        "tool_result",
					"tool_use_id": toolUseID,
					"content":     content,
				},
			},
		},
	}
}

func TestParseEvent_ShowToolProcess_True(t *testing.T) {
	s := newTestWebuiSession(true)

	// tool_use should be forwarded
	msgs := s.parseEvent(makeToolUseEvent("Bash"))
	found := false
	for _, m := range msgs {
		if m["type"] == "tool_use" {
			found = true
			if m["tool_name"] != "Bash" {
				t.Errorf("tool_name = %v, want Bash", m["tool_name"])
			}
		}
	}
	if !found {
		t.Error("showToolProcess=true: expected tool_use message, got none")
	}

	// tool_result should be forwarded
	msgs = s.parseEvent(makeToolResultEvent("toolu_123", "output text"))
	found = false
	for _, m := range msgs {
		if m["type"] == "tool_result" {
			found = true
		}
	}
	if !found {
		t.Error("showToolProcess=true: expected tool_result message, got none")
	}
}

func TestParseEvent_ShowToolProcess_False(t *testing.T) {
	s := newTestWebuiSession(false)

	// tool_use should be filtered out
	msgs := s.parseEvent(makeToolUseEvent("Bash"))
	for _, m := range msgs {
		if m["type"] == "tool_use" {
			t.Error("showToolProcess=false: tool_use should be filtered, but was forwarded")
		}
	}

	// tool_result should be filtered out
	msgs = s.parseEvent(makeToolResultEvent("toolu_123", "output text"))
	for _, m := range msgs {
		if m["type"] == "tool_result" {
			t.Error("showToolProcess=false: tool_result should be filtered, but was forwarded")
		}
	}
}

func TestParseEvent_ShowToolProcess_TextNotAffected(t *testing.T) {
	s := newTestWebuiSession(false)

	// text messages should still pass through even when showToolProcess=false
	event := map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"content": []any{
				map[string]any{
					"type": "text",
					"text": "Hello world",
				},
			},
		},
	}
	msgs := s.parseEvent(event)
	found := false
	for _, m := range msgs {
		if m["type"] == "text" && m["content"] == "Hello world" {
			found = true
		}
	}
	if !found {
		t.Error("showToolProcess=false should not affect text messages")
	}
}
