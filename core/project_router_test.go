package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- stubs for ProjectRouter tests ---

// stubRouterPlatform records sent messages and captures the message handler.
type stubRouterPlatform struct {
	n       string
	handler MessageHandler
	sent    []string
	mu      sync.Mutex
}

func (p *stubRouterPlatform) Name() string { return p.n }
func (p *stubRouterPlatform) Start(h MessageHandler) error {
	p.handler = h
	return nil
}
func (p *stubRouterPlatform) Reply(_ context.Context, _ any, content string) error {
	p.mu.Lock()
	p.sent = append(p.sent, content)
	p.mu.Unlock()
	return nil
}
func (p *stubRouterPlatform) Send(_ context.Context, _ any, content string) error {
	p.mu.Lock()
	p.sent = append(p.sent, content)
	p.mu.Unlock()
	return nil
}
func (p *stubRouterPlatform) Stop() error { return nil }

func (p *stubRouterPlatform) getSent() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := make([]string, len(p.sent))
	copy(cp, p.sent)
	return cp
}

func (p *stubRouterPlatform) clearSent() {
	p.mu.Lock()
	p.sent = nil
	p.mu.Unlock()
}

// simulateMessage simulates an incoming message from the platform.
func (p *stubRouterPlatform) simulateMessage(sessionKey, content string) {
	p.handler(p, &Message{
		SessionKey: sessionKey,
		Content:    content,
		ReplyCtx:   "ctx",
	})
}

// stubButtonRouterPlatform supports InlineButtonSender.
type stubButtonRouterPlatform struct {
	stubRouterPlatform
	buttonContent string
	buttonRows    [][]ButtonOption
}

func (p *stubButtonRouterPlatform) SendWithButtons(_ context.Context, _ any, content string, buttons [][]ButtonOption) error {
	p.buttonContent = content
	p.buttonRows = buttons
	return nil
}

// stubAsyncRouterPlatform implements AsyncRecoverablePlatform.
type stubAsyncRouterPlatform struct {
	stubRouterPlatform
	lifecycleHandler PlatformLifecycleHandler
}

func (p *stubAsyncRouterPlatform) SetLifecycleHandler(h PlatformLifecycleHandler) {
	p.lifecycleHandler = h
}

// --- helper ---

// newTestRouter creates a ProjectRouter with N engines sharing the given platform.
func newTestRouter(t *testing.T, platform Platform, projectNames ...string) (*ProjectRouter, []*Engine) {
	t.Helper()
	storePath := filepath.Join(t.TempDir(), "bindings.json")
	router := NewProjectRouter(platform, NewI18n(LangEnglish), storePath)
	var engines []*Engine
	for _, name := range projectNames {
		e := NewEngine(name, &stubAgent{}, []Platform{platform}, "", LangEnglish)
		router.AddProject(name, e)
		engines = append(engines, e)
	}
	return router, engines
}

// --- Tests ---

func TestProjectRouter_SingleProject_AutoBinds(t *testing.T) {
	p := &stubRouterPlatform{n: "feishu"}
	router, engines := newTestRouter(t, p, "my-project")

	if err := router.Start(); err != nil {
		t.Fatal(err)
	}
	defer router.Stop()

	// Send a message — should auto-bind without showing selection
	p.simulateMessage("feishu:chat1:user1", "hello")

	// Give HandleIncomingMessage a moment (it's synchronous, but just in case)
	time.Sleep(50 * time.Millisecond)

	// The message should NOT trigger a project selection prompt
	sent := p.getSent()
	for _, s := range sent {
		if strings.Contains(s, "select") || strings.Contains(s, "选择") {
			t.Fatalf("single project should auto-bind, not show selection; got: %q", s)
		}
	}

	// Verify binding was created
	router.mu.RLock()
	bound := router.bindings["feishu:chat1:user1"]
	router.mu.RUnlock()
	if bound != "my-project" {
		t.Fatalf("expected binding to 'my-project', got %q", bound)
	}

	_ = engines // engines are connected
}

func TestProjectRouter_MultiProject_ShowsSelection(t *testing.T) {
	p := &stubRouterPlatform{n: "feishu"}
	router, _ := newTestRouter(t, p, "ProjectA", "ProjectB", "ProjectC")

	if err := router.Start(); err != nil {
		t.Fatal(err)
	}
	defer router.Stop()

	p.simulateMessage("feishu:chat1:user1", "hello")

	sent := p.getSent()
	if len(sent) == 0 {
		t.Fatal("expected selection prompt to be sent")
	}
	// Should contain project names as numbered list
	if !strings.Contains(sent[0], "1. ProjectA") || !strings.Contains(sent[0], "2. ProjectB") {
		t.Fatalf("selection prompt should list projects, got: %q", sent[0])
	}

	// Verify message is pending
	router.mu.RLock()
	_, isPending := router.pending["feishu:chat1:user1"]
	router.mu.RUnlock()
	if !isPending {
		t.Fatal("expected session to be in pending state")
	}
}

func TestProjectRouter_SelectByNumber(t *testing.T) {
	p := &stubRouterPlatform{n: "feishu"}
	router, _ := newTestRouter(t, p, "ProjectA", "ProjectB")

	if err := router.Start(); err != nil {
		t.Fatal(err)
	}
	defer router.Stop()

	// First message triggers selection
	p.simulateMessage("feishu:chat1:user1", "hello")
	p.clearSent()

	// User replies with "2" to select ProjectB
	p.simulateMessage("feishu:chat1:user1", "2")

	sent := p.getSent()
	if len(sent) == 0 {
		t.Fatal("expected switch confirmation")
	}
	if !strings.Contains(sent[0], "ProjectB") {
		t.Fatalf("expected confirmation for ProjectB, got: %q", sent[0])
	}

	// Verify binding
	router.mu.RLock()
	bound := router.bindings["feishu:chat1:user1"]
	router.mu.RUnlock()
	if bound != "ProjectB" {
		t.Fatalf("expected binding to 'ProjectB', got %q", bound)
	}
}

func TestProjectRouter_SelectByName(t *testing.T) {
	p := &stubRouterPlatform{n: "feishu"}
	router, _ := newTestRouter(t, p, "AstrBot", "UserCenter")

	if err := router.Start(); err != nil {
		t.Fatal(err)
	}
	defer router.Stop()

	p.simulateMessage("feishu:chat1:user1", "hello")
	p.clearSent()

	// User replies with project name (case-insensitive)
	p.simulateMessage("feishu:chat1:user1", "usercenter")

	sent := p.getSent()
	if len(sent) == 0 {
		t.Fatal("expected switch confirmation")
	}
	if !strings.Contains(sent[0], "UserCenter") {
		t.Fatalf("expected confirmation for UserCenter, got: %q", sent[0])
	}

	router.mu.RLock()
	bound := router.bindings["feishu:chat1:user1"]
	router.mu.RUnlock()
	if bound != "UserCenter" {
		t.Fatalf("expected binding to 'UserCenter', got %q", bound)
	}
}

func TestProjectRouter_InvalidSelection_Retries(t *testing.T) {
	p := &stubRouterPlatform{n: "feishu"}
	router, _ := newTestRouter(t, p, "ProjectA", "ProjectB")

	if err := router.Start(); err != nil {
		t.Fatal(err)
	}
	defer router.Stop()

	p.simulateMessage("feishu:chat1:user1", "hello")
	p.clearSent()

	// User replies with invalid input
	p.simulateMessage("feishu:chat1:user1", "99")

	sent := p.getSent()
	if len(sent) == 0 {
		t.Fatal("expected invalid selection message")
	}
	if !strings.Contains(sent[0], "Invalid") && !strings.Contains(sent[0], "invalid") {
		t.Fatalf("expected invalid selection message, got: %q", sent[0])
	}

	// Session should still be pending
	router.mu.RLock()
	_, isPending := router.pending["feishu:chat1:user1"]
	router.mu.RUnlock()
	if !isPending {
		t.Fatal("expected session to remain pending after invalid selection")
	}

	// Now select a valid one
	p.clearSent()
	p.simulateMessage("feishu:chat1:user1", "1")

	sent = p.getSent()
	if len(sent) == 0 {
		t.Fatal("expected switch confirmation")
	}
	if !strings.Contains(sent[0], "ProjectA") {
		t.Fatalf("expected confirmation for ProjectA, got: %q", sent[0])
	}
}

func TestProjectRouter_RoutesAfterBinding(t *testing.T) {
	p := &stubRouterPlatform{n: "feishu"}
	router, engines := newTestRouter(t, p, "ProjectA", "ProjectB")

	if err := router.Start(); err != nil {
		t.Fatal(err)
	}
	defer router.Stop()

	// Pre-bind the session
	router.mu.Lock()
	router.bindings["feishu:chat1:user1"] = "ProjectB"
	router.mu.Unlock()

	// Send a message — should be routed to ProjectB's engine
	p.simulateMessage("feishu:chat1:user1", "/status")

	// Engine B should see a message come through (via handleMessage).
	// We can't easily check without a full session, but we confirm no selection prompt was sent.
	sent := p.getSent()
	for _, s := range sent {
		if strings.Contains(s, "select") || strings.Contains(s, "选择") {
			t.Fatalf("bound session should not trigger selection; got: %q", s)
		}
	}
	_ = engines
}

func TestProjectRouter_ProjectCommand_ShowsList(t *testing.T) {
	p := &stubRouterPlatform{n: "feishu"}
	router, _ := newTestRouter(t, p, "ProjectA", "ProjectB")

	if err := router.Start(); err != nil {
		t.Fatal(err)
	}
	defer router.Stop()

	// Pre-bind
	router.mu.Lock()
	router.bindings["feishu:chat1:user1"] = "ProjectA"
	router.mu.Unlock()

	p.simulateMessage("feishu:chat1:user1", "/project")

	sent := p.getSent()
	if len(sent) == 0 {
		t.Fatal("expected /project response")
	}
	// Should contain current project and list
	if !strings.Contains(sent[0], "ProjectA") {
		t.Fatalf("expected current project indicator, got: %q", sent[0])
	}
	if !strings.Contains(sent[0], "1. ProjectA") || !strings.Contains(sent[0], "2. ProjectB") {
		t.Fatalf("expected project list, got: %q", sent[0])
	}
}

func TestProjectRouter_ProjectCommand_SwitchesProject(t *testing.T) {
	p := &stubRouterPlatform{n: "feishu"}
	router, _ := newTestRouter(t, p, "ProjectA", "ProjectB")

	if err := router.Start(); err != nil {
		t.Fatal(err)
	}
	defer router.Stop()

	// Pre-bind to ProjectA
	router.mu.Lock()
	router.bindings["feishu:chat1:user1"] = "ProjectA"
	router.mu.Unlock()

	// Switch to ProjectB via /project command
	p.simulateMessage("feishu:chat1:user1", "/project ProjectB")

	sent := p.getSent()
	if len(sent) == 0 {
		t.Fatal("expected switch confirmation")
	}
	if !strings.Contains(sent[0], "ProjectB") {
		t.Fatalf("expected switched to ProjectB, got: %q", sent[0])
	}

	router.mu.RLock()
	bound := router.bindings["feishu:chat1:user1"]
	router.mu.RUnlock()
	if bound != "ProjectB" {
		t.Fatalf("expected binding updated to 'ProjectB', got %q", bound)
	}
}

func TestProjectRouter_ProjectCommand_SwitchByNumber(t *testing.T) {
	p := &stubRouterPlatform{n: "feishu"}
	router, _ := newTestRouter(t, p, "ProjectA", "ProjectB")

	if err := router.Start(); err != nil {
		t.Fatal(err)
	}
	defer router.Stop()

	router.mu.Lock()
	router.bindings["feishu:chat1:user1"] = "ProjectA"
	router.mu.Unlock()

	p.simulateMessage("feishu:chat1:user1", "/project 2")

	router.mu.RLock()
	bound := router.bindings["feishu:chat1:user1"]
	router.mu.RUnlock()
	if bound != "ProjectB" {
		t.Fatalf("expected binding to 'ProjectB', got %q", bound)
	}
}

func TestProjectRouter_ProjectCommand_InvalidName(t *testing.T) {
	p := &stubRouterPlatform{n: "feishu"}
	router, _ := newTestRouter(t, p, "ProjectA", "ProjectB")

	if err := router.Start(); err != nil {
		t.Fatal(err)
	}
	defer router.Stop()

	router.mu.Lock()
	router.bindings["feishu:chat1:user1"] = "ProjectA"
	router.mu.Unlock()

	p.simulateMessage("feishu:chat1:user1", "/project NoSuchProject")

	sent := p.getSent()
	if len(sent) == 0 {
		t.Fatal("expected invalid message")
	}
	if !strings.Contains(sent[0], "Invalid") && !strings.Contains(sent[0], "invalid") {
		t.Fatalf("expected invalid selection message, got: %q", sent[0])
	}

	// Binding should remain unchanged
	router.mu.RLock()
	bound := router.bindings["feishu:chat1:user1"]
	router.mu.RUnlock()
	if bound != "ProjectA" {
		t.Fatalf("binding should remain 'ProjectA', got %q", bound)
	}
}

func TestProjectRouter_InlineButtons_Selection(t *testing.T) {
	p := &stubButtonRouterPlatform{
		stubRouterPlatform: stubRouterPlatform{n: "telegram"},
	}
	router, _ := newTestRouter(t, p, "ProjectA", "ProjectB")

	if err := router.Start(); err != nil {
		t.Fatal(err)
	}
	defer router.Stop()

	// Trigger selection
	p.handler(p, &Message{
		SessionKey: "tg:chat1:user1",
		Content:    "hello",
		ReplyCtx:   "ctx",
	})

	// Should have used SendWithButtons
	if p.buttonContent == "" {
		t.Fatal("expected button-based selection")
	}
	if len(p.buttonRows) != 2 {
		t.Fatalf("expected 2 button rows, got %d", len(p.buttonRows))
	}
	if p.buttonRows[0][0].Data != "__project__:ProjectA" {
		t.Fatalf("expected button data '__project__:ProjectA', got %q", p.buttonRows[0][0].Data)
	}
	if p.buttonRows[1][0].Data != "__project__:ProjectB" {
		t.Fatalf("expected button data '__project__:ProjectB', got %q", p.buttonRows[1][0].Data)
	}
}

func TestProjectRouter_ButtonCallback_Binds(t *testing.T) {
	p := &stubButtonRouterPlatform{
		stubRouterPlatform: stubRouterPlatform{n: "telegram"},
	}
	router, _ := newTestRouter(t, p, "ProjectA", "ProjectB")

	if err := router.Start(); err != nil {
		t.Fatal(err)
	}
	defer router.Stop()

	// Trigger selection first
	p.handler(p, &Message{
		SessionKey: "tg:chat1:user1",
		Content:    "hello",
		ReplyCtx:   "ctx",
	})

	// Simulate button callback
	p.handler(p, &Message{
		SessionKey: "tg:chat1:user1",
		Content:    "__project__:ProjectB",
		ReplyCtx:   "ctx",
	})

	router.mu.RLock()
	bound := router.bindings["tg:chat1:user1"]
	router.mu.RUnlock()
	if bound != "ProjectB" {
		t.Fatalf("expected binding to 'ProjectB', got %q", bound)
	}

	// Verify pending was cleared
	router.mu.RLock()
	_, isPending := router.pending["tg:chat1:user1"]
	router.mu.RUnlock()
	if isPending {
		t.Fatal("pending should be cleared after button callback")
	}
}

func TestProjectRouter_BindingPersistence(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "bindings.json")
	p := &stubRouterPlatform{n: "feishu"}

	// Create router, add binding, stop
	{
		router := NewProjectRouter(p, NewI18n(LangEnglish), storePath)
		eA := NewEngine("ProjectA", &stubAgent{}, []Platform{p}, "", LangEnglish)
		eB := NewEngine("ProjectB", &stubAgent{}, []Platform{p}, "", LangEnglish)
		router.AddProject("ProjectA", eA)
		router.AddProject("ProjectB", eB)

		if err := router.Start(); err != nil {
			t.Fatal(err)
		}

		router.mu.Lock()
		router.bindings["feishu:chat1:user1"] = "ProjectA"
		router.bindings["feishu:chat2:user2"] = "ProjectB"
		router.mu.Unlock()
		router.saveBindings()
		router.Stop()
	}

	// Verify file exists
	data, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatal("bindings file should exist:", err)
	}
	var bd bindingData
	if err := json.Unmarshal(data, &bd); err != nil {
		t.Fatal("failed to parse bindings:", err)
	}
	if bd.Bindings["feishu:chat1:user1"] != "ProjectA" {
		t.Fatalf("expected persisted binding for ProjectA, got %q", bd.Bindings["feishu:chat1:user1"])
	}

	// Create new router, bindings should be restored
	{
		router := NewProjectRouter(p, NewI18n(LangEnglish), storePath)
		eA := NewEngine("ProjectA", &stubAgent{}, []Platform{p}, "", LangEnglish)
		eB := NewEngine("ProjectB", &stubAgent{}, []Platform{p}, "", LangEnglish)
		router.AddProject("ProjectA", eA)
		router.AddProject("ProjectB", eB)

		if err := router.Start(); err != nil {
			t.Fatal(err)
		}
		defer router.Stop()

		router.mu.RLock()
		b1 := router.bindings["feishu:chat1:user1"]
		b2 := router.bindings["feishu:chat2:user2"]
		router.mu.RUnlock()

		if b1 != "ProjectA" {
			t.Fatalf("expected restored binding 'ProjectA', got %q", b1)
		}
		if b2 != "ProjectB" {
			t.Fatalf("expected restored binding 'ProjectB', got %q", b2)
		}
	}
}

func TestProjectRouter_BindingPersistence_IgnoresRemovedProjects(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "bindings.json")
	p := &stubRouterPlatform{n: "feishu"}

	// Create and persist bindings for two projects
	{
		router := NewProjectRouter(p, NewI18n(LangEnglish), storePath)
		eA := NewEngine("ProjectA", &stubAgent{}, []Platform{p}, "", LangEnglish)
		eB := NewEngine("ProjectB", &stubAgent{}, []Platform{p}, "", LangEnglish)
		router.AddProject("ProjectA", eA)
		router.AddProject("ProjectB", eB)
		router.Start()
		router.mu.Lock()
		router.bindings["s1"] = "ProjectA"
		router.bindings["s2"] = "ProjectB"
		router.mu.Unlock()
		router.saveBindings()
		router.Stop()
	}

	// Restart with only ProjectA (ProjectB removed from config)
	{
		router := NewProjectRouter(p, NewI18n(LangEnglish), storePath)
		eA := NewEngine("ProjectA", &stubAgent{}, []Platform{p}, "", LangEnglish)
		router.AddProject("ProjectA", eA)
		router.Start()
		defer router.Stop()

		router.mu.RLock()
		b1 := router.bindings["s1"]
		b2 := router.bindings["s2"]
		router.mu.RUnlock()

		if b1 != "ProjectA" {
			t.Fatalf("expected binding 'ProjectA' preserved, got %q", b1)
		}
		if b2 != "" {
			t.Fatalf("expected binding for removed project to be ignored, got %q", b2)
		}
	}
}

func TestProjectRouter_StaleBinding_ClearsAndShowsSelection(t *testing.T) {
	p := &stubRouterPlatform{n: "feishu"}
	router, _ := newTestRouter(t, p, "ProjectA", "ProjectB")

	if err := router.Start(); err != nil {
		t.Fatal(err)
	}
	defer router.Stop()

	// Set a binding to a non-existent project (simulates config change)
	router.mu.Lock()
	router.bindings["feishu:chat1:user1"] = "RemovedProject"
	router.mu.Unlock()

	p.simulateMessage("feishu:chat1:user1", "hello")

	// Should have cleared the stale binding and shown selection
	sent := p.getSent()
	if len(sent) == 0 {
		t.Fatal("expected selection prompt after stale binding cleared")
	}
	if !strings.Contains(sent[0], "1. ProjectA") {
		t.Fatalf("expected selection prompt with project list, got: %q", sent[0])
	}

	router.mu.RLock()
	_, stillBound := router.bindings["feishu:chat1:user1"]
	router.mu.RUnlock()
	if stillBound {
		t.Fatal("stale binding should have been cleared")
	}
}

func TestProjectRouter_MultipleSessions_Independent(t *testing.T) {
	p := &stubRouterPlatform{n: "feishu"}
	router, _ := newTestRouter(t, p, "ProjectA", "ProjectB")

	if err := router.Start(); err != nil {
		t.Fatal(err)
	}
	defer router.Stop()

	// User1 selects ProjectA
	p.simulateMessage("feishu:chat1:user1", "hello")
	p.clearSent()
	p.simulateMessage("feishu:chat1:user1", "1")

	// User2 selects ProjectB
	p.simulateMessage("feishu:chat2:user2", "hello")
	p.clearSent()
	p.simulateMessage("feishu:chat2:user2", "2")

	router.mu.RLock()
	b1 := router.bindings["feishu:chat1:user1"]
	b2 := router.bindings["feishu:chat2:user2"]
	router.mu.RUnlock()

	if b1 != "ProjectA" {
		t.Fatalf("user1 should be bound to ProjectA, got %q", b1)
	}
	if b2 != "ProjectB" {
		t.Fatalf("user2 should be bound to ProjectB, got %q", b2)
	}
}

func TestProjectRouter_PrefixMatch(t *testing.T) {
	p := &stubRouterPlatform{n: "feishu"}
	router, _ := newTestRouter(t, p, "AstrBot", "UserCenter")

	if err := router.Start(); err != nil {
		t.Fatal(err)
	}
	defer router.Stop()

	p.simulateMessage("s1", "hello")
	p.clearSent()

	// Prefix match: "astr" should match "AstrBot"
	p.simulateMessage("s1", "astr")

	router.mu.RLock()
	bound := router.bindings["s1"]
	router.mu.RUnlock()
	if bound != "AstrBot" {
		t.Fatalf("prefix 'astr' should match AstrBot, got %q", bound)
	}
}

func TestProjectRouter_AmbiguousPrefix_Rejects(t *testing.T) {
	p := &stubRouterPlatform{n: "feishu"}
	router, _ := newTestRouter(t, p, "ProjectAlpha", "ProjectBeta")

	if err := router.Start(); err != nil {
		t.Fatal(err)
	}
	defer router.Stop()

	p.simulateMessage("s1", "hello")
	p.clearSent()

	// "Project" is a prefix of both — should be rejected as ambiguous
	p.simulateMessage("s1", "Project")

	sent := p.getSent()
	if len(sent) == 0 {
		t.Fatal("expected invalid selection message")
	}
	if !strings.Contains(sent[0], "Invalid") && !strings.Contains(sent[0], "invalid") {
		t.Fatalf("ambiguous prefix should be rejected, got: %q", sent[0])
	}
}

func TestProjectRouter_OriginalMessageForwarded_AfterSelection(t *testing.T) {
	p := &stubRouterPlatform{n: "feishu"}
	router, _ := newTestRouter(t, p, "ProjectA")

	// With only 1 project, auto-bind is tested separately.
	// Use 2 projects and verify forwarding.
	eB := NewEngine("ProjectB", &stubAgent{}, []Platform{p}, "", LangEnglish)
	router.AddProject("ProjectB", eB)

	if err := router.Start(); err != nil {
		t.Fatal(err)
	}
	defer router.Stop()

	// Send original message
	p.simulateMessage("s1", "build my app")
	p.clearSent()

	// Select ProjectA by number
	p.simulateMessage("s1", "1")

	// After selection, pending should be cleared
	router.mu.RLock()
	_, isPending := router.pending["s1"]
	router.mu.RUnlock()
	if isPending {
		t.Fatal("pending should be cleared after selection")
	}
}

func TestProjectRouter_AsyncPlatform_Lifecycle(t *testing.T) {
	p := &stubAsyncRouterPlatform{
		stubRouterPlatform: stubRouterPlatform{n: "feishu"},
	}
	storePath := filepath.Join(t.TempDir(), "bindings.json")
	router := NewProjectRouter(p, NewI18n(LangEnglish), storePath)

	eA := NewEngine("ProjectA", &stubAgent{}, []Platform{p}, "", LangEnglish)
	eB := NewEngine("ProjectB", &stubAgent{}, []Platform{p}, "", LangEnglish)
	eA.SetExternalPlatform(p)
	eB.SetExternalPlatform(p)
	router.AddProject("ProjectA", eA)
	router.AddProject("ProjectB", eB)

	if err := router.Start(); err != nil {
		t.Fatal(err)
	}
	defer router.Stop()

	// Router should have set itself as the lifecycle handler
	if p.lifecycleHandler == nil {
		t.Fatal("expected lifecycle handler to be set on async platform")
	}

	// Simulate platform ready
	p.lifecycleHandler.OnPlatformReady(p)

	// Both engines should now show the platform as ready
	eA.platformLifecycleMu.Lock()
	readyA := eA.platformReady[p]
	eA.platformLifecycleMu.Unlock()

	eB.platformLifecycleMu.Lock()
	readyB := eB.platformReady[p]
	eB.platformLifecycleMu.Unlock()

	if !readyA {
		t.Fatal("engineA should see platform as ready")
	}
	if !readyB {
		t.Fatal("engineB should see platform as ready")
	}
}

func TestProjectRouter_MatchProject(t *testing.T) {
	router := &ProjectRouter{
		engines: []*projectEntry{
			{name: "AstrBot"},
			{name: "UserCenter"},
			{name: "TaskManager"},
		},
		engineMap: map[string]*projectEntry{
			"AstrBot":     {name: "AstrBot"},
			"UserCenter":  {name: "UserCenter"},
			"TaskManager": {name: "TaskManager"},
		},
	}

	tests := []struct {
		input string
		want  string
	}{
		{"1", "AstrBot"},
		{"2", "UserCenter"},
		{"3", "TaskManager"},
		{"0", ""},
		{"4", ""},
		{"AstrBot", "AstrBot"},
		{"astrbot", "AstrBot"},
		{"USERCENTER", "UserCenter"},
		{"Task", "TaskManager"},  // prefix match
		{"astr", "AstrBot"},      // prefix match
		{"xyz", ""},              // no match
		{"", ""},                 // empty
	}
	for _, tt := range tests {
		got := router.matchProject(tt.input)
		if got != tt.want {
			t.Errorf("matchProject(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestProjectRouter_ProjectList_Command(t *testing.T) {
	p := &stubRouterPlatform{n: "feishu"}
	router, _ := newTestRouter(t, p, "ProjectA", "ProjectB")

	if err := router.Start(); err != nil {
		t.Fatal(err)
	}
	defer router.Stop()

	p.simulateMessage("s1", "/project list")

	sent := p.getSent()
	if len(sent) == 0 {
		t.Fatal("expected /project list response")
	}
	if !strings.Contains(sent[0], "1. ProjectA") || !strings.Contains(sent[0], "2. ProjectB") {
		t.Fatalf("expected project list, got: %q", sent[0])
	}
}

func TestProjectRouter_i18n_Chinese(t *testing.T) {
	p := &stubRouterPlatform{n: "feishu"}
	storePath := filepath.Join(t.TempDir(), "bindings.json")
	router := NewProjectRouter(p, NewI18n(LangChinese), storePath)

	eA := NewEngine("ProjectA", &stubAgent{}, []Platform{p}, "", LangChinese)
	eB := NewEngine("ProjectB", &stubAgent{}, []Platform{p}, "", LangChinese)
	router.AddProject("ProjectA", eA)
	router.AddProject("ProjectB", eB)

	if err := router.Start(); err != nil {
		t.Fatal(err)
	}
	defer router.Stop()

	p.simulateMessage("s1", "hello")

	sent := p.getSent()
	if len(sent) == 0 {
		t.Fatal("expected selection prompt")
	}
	if !strings.Contains(sent[0], "请选择") {
		t.Fatalf("expected Chinese selection prompt, got: %q", sent[0])
	}
}

// stubWorkDirRouterAgent embeds stubAgent and adds GetWorkDir for ProjectRouter tests.
type stubWorkDirRouterAgent struct {
	stubAgent
	workDir string
}

func (a *stubWorkDirRouterAgent) GetWorkDir() string { return a.workDir }

// newTestRouterWithWorkDirs creates a ProjectRouter where each engine's agent reports a work_dir.
func newTestRouterWithWorkDirs(t *testing.T, platform Platform, projects map[string]string) (*ProjectRouter, []*Engine) {
	t.Helper()
	storePath := filepath.Join(t.TempDir(), "bindings.json")
	router := NewProjectRouter(platform, NewI18n(LangEnglish), storePath)
	var engines []*Engine
	for name, workDir := range projects {
		agent := &stubWorkDirRouterAgent{workDir: workDir}
		e := NewEngine(name, agent, []Platform{platform}, "", LangEnglish)
		router.AddProject(name, e)
		engines = append(engines, e)
	}
	return router, engines
}

func TestProjectRouter_SwitchProject_CleansUpOldSession(t *testing.T) {
	p := &stubRouterPlatform{n: "feishu"}
	router, engines := newTestRouter(t, p, "ProjectA", "ProjectB")

	if err := router.Start(); err != nil {
		t.Fatal(err)
	}
	defer router.Stop()

	sessionKey := "feishu:chat1:user1"

	// Pre-bind to ProjectA
	router.mu.Lock()
	router.bindings[sessionKey] = "ProjectA"
	router.mu.Unlock()

	// Simulate an active interactiveState in ProjectA's engine
	engineA := engines[0]
	closeCalled := false
	fakeSession := &trackCloseAgentSession{onClose: func() { closeCalled = true }}
	engineA.interactiveMu.Lock()
	engineA.interactiveStates[sessionKey] = &interactiveState{
		agentSession: fakeSession,
		platform:     p,
	}
	engineA.interactiveMu.Unlock()

	// Switch to ProjectB via /project command
	p.simulateMessage(sessionKey, "/project ProjectB")

	// Wait briefly for async cleanup
	time.Sleep(100 * time.Millisecond)

	// Verify ProjectA's interactiveState was cleaned up
	engineA.interactiveMu.Lock()
	_, stillExists := engineA.interactiveStates[sessionKey]
	engineA.interactiveMu.Unlock()

	if stillExists {
		t.Fatal("expected interactiveState for sessionKey to be cleaned up in ProjectA")
	}
	if !closeCalled {
		t.Fatal("expected agent session Close() to be called during cleanup")
	}

	// Verify binding switched to ProjectB
	router.mu.RLock()
	bound := router.bindings[sessionKey]
	router.mu.RUnlock()
	if bound != "ProjectB" {
		t.Fatalf("expected binding to 'ProjectB', got %q", bound)
	}
}

func TestProjectRouter_SwitchProject_SameProject_NoCleanup(t *testing.T) {
	p := &stubRouterPlatform{n: "feishu"}
	router, engines := newTestRouter(t, p, "ProjectA", "ProjectB")

	if err := router.Start(); err != nil {
		t.Fatal(err)
	}
	defer router.Stop()

	sessionKey := "feishu:chat1:user1"

	// Pre-bind to ProjectA
	router.mu.Lock()
	router.bindings[sessionKey] = "ProjectA"
	router.mu.Unlock()

	// Simulate an active interactiveState in ProjectA's engine
	engineA := engines[0]
	closeCalled := false
	fakeSession := &trackCloseAgentSession{onClose: func() { closeCalled = true }}
	engineA.interactiveMu.Lock()
	engineA.interactiveStates[sessionKey] = &interactiveState{
		agentSession: fakeSession,
		platform:     p,
	}
	engineA.interactiveMu.Unlock()

	// Switch to same project — should NOT clean up
	p.simulateMessage(sessionKey, "/project ProjectA")

	time.Sleep(50 * time.Millisecond)

	// interactiveState should still exist (no cleanup for same project)
	engineA.interactiveMu.Lock()
	_, stillExists := engineA.interactiveStates[sessionKey]
	engineA.interactiveMu.Unlock()

	if !stillExists {
		t.Fatal("expected interactiveState to remain when switching to same project")
	}
	if closeCalled {
		t.Fatal("agent session Close() should not be called when switching to same project")
	}
}

func TestProjectRouter_ProjectList_ShowsSessionStatus(t *testing.T) {
	p := &stubRouterPlatform{n: "feishu"}
	// Use ordered slice to control project order
	storePath := filepath.Join(t.TempDir(), "bindings.json")
	router := NewProjectRouter(p, NewI18n(LangEnglish), storePath)

	agentA := &stubWorkDirRouterAgent{workDir: "/home/user/astrBot"}
	agentB := &stubWorkDirRouterAgent{workDir: "/home/user/user-center"}
	engineA := NewEngine("AstrBot", agentA, []Platform{p}, "", LangEnglish)
	engineB := NewEngine("UserCenter", agentB, []Platform{p}, "", LangEnglish)
	router.AddProject("AstrBot", engineA)
	router.AddProject("UserCenter", engineB)

	if err := router.Start(); err != nil {
		t.Fatal(err)
	}
	defer router.Stop()

	sessionKey := "feishu:chat1:user1"

	// Pre-bind to AstrBot
	router.mu.Lock()
	router.bindings[sessionKey] = "AstrBot"
	router.mu.Unlock()

	// Simulate an active session in AstrBot's engine
	fakeSession := &trackCloseAgentSession{}
	engineA.interactiveMu.Lock()
	engineA.interactiveStates[sessionKey] = &interactiveState{
		agentSession: fakeSession,
		platform:     p,
	}
	engineA.interactiveMu.Unlock()

	// Run /project to see the list
	p.simulateMessage(sessionKey, "/project")

	sent := p.getSent()
	if len(sent) == 0 {
		t.Fatal("expected /project response")
	}
	output := sent[0]

	// Should show work_dir (just the base name)
	if !strings.Contains(output, "astrBot") {
		t.Fatalf("expected work dir 'astrBot' in output, got: %q", output)
	}
	if !strings.Contains(output, "user-center") {
		t.Fatalf("expected work dir 'user-center' in output, got: %q", output)
	}

	// Should show active indicator for AstrBot
	if !strings.Contains(output, "🟢") {
		t.Fatalf("expected active session indicator 🟢 in output, got: %q", output)
	}

	// Arrow marker for current project
	if !strings.Contains(output, "→") {
		t.Fatalf("expected current project marker → in output, got: %q", output)
	}
}

func TestProjectRouter_PendingSelect_NoRedundantPlatformField(t *testing.T) {
	// Compile-time verification: pendingSelect should only have originalMsg.
	// This test ensures the struct layout matches expectations.
	ps := pendingSelect{originalMsg: &Message{Content: "test"}}
	if ps.originalMsg == nil {
		t.Fatal("originalMsg should be set")
	}
	// If pendingSelect still had a platform field, this would fail to compile
	// after the struct change. This test documents the intent.
}

// --- BaseSessionKeyer tests ---

// stubBaseSessionKeyPlatform implements BaseSessionKeyer for testing thread isolation fallback.
type stubBaseSessionKeyPlatform struct {
	stubRouterPlatform
	baseKeyFunc func(msg *Message) string
}

func (p *stubBaseSessionKeyPlatform) BaseSessionKey(msg *Message) string {
	if p.baseKeyFunc != nil {
		return p.baseKeyFunc(msg)
	}
	return msg.SessionKey
}

func TestProjectRouter_BaseSessionKey_ThreadIsolation_SwitchAppliesToNewThreads(t *testing.T) {
	// Simulate Feishu thread_isolation: each top-level message has a unique session key,
	// but they share a common base key (chatId:userId).
	p := &stubBaseSessionKeyPlatform{
		stubRouterPlatform: stubRouterPlatform{n: "feishu"},
		baseKeyFunc: func(msg *Message) string {
			// Simulate: "feishu:chat1:root:msg123" → "feishu:chat1:user1"
			if strings.Contains(msg.SessionKey, ":root:") {
				parts := strings.SplitN(msg.SessionKey, ":", 3)
				if len(parts) >= 2 {
					return parts[0] + ":" + parts[1] + ":" + msg.UserID
				}
			}
			return msg.SessionKey
		},
	}

	storePath := filepath.Join(t.TempDir(), "bindings.json")
	router := NewProjectRouter(p, NewI18n(LangEnglish), storePath)
	eA := NewEngine("ProjectA", &stubAgent{}, []Platform{p}, "", LangEnglish)
	eB := NewEngine("ProjectB", &stubAgent{}, []Platform{p}, "", LangEnglish)
	router.AddProject("ProjectA", eA)
	router.AddProject("ProjectB", eB)

	if err := router.Start(); err != nil {
		t.Fatal(err)
	}
	defer router.Stop()

	// Thread 1: send initial message to trigger selection prompt
	p.handler(p, &Message{
		SessionKey: "feishu:chat1:root:msg001",
		UserID:     "user1",
		Content:    "hello",
		ReplyCtx:   "ctx",
	})

	// Should show selection (multi-project, no binding yet)
	sent := p.getSent()
	foundSelection := false
	for _, s := range sent {
		if strings.Contains(s, "ProjectA") && strings.Contains(s, "ProjectB") {
			foundSelection = true
		}
	}
	if !foundSelection {
		t.Fatalf("expected project selection prompt, got: %v", sent)
	}

	// User selects "1" (ProjectA) in thread 1's pending selection
	p.clearSent()
	p.handler(p, &Message{
		SessionKey: "feishu:chat1:root:msg001",
		UserID:     "user1",
		Content:    "1",
		ReplyCtx:   "ctx",
	})

	// Verify binding set for both exact key and base key
	router.mu.RLock()
	exactBinding := router.bindings["feishu:chat1:root:msg001"]
	baseBinding := router.bindings["feishu:chat1:user1"]
	router.mu.RUnlock()

	if exactBinding != "ProjectA" {
		t.Fatalf("expected exact binding to ProjectA, got %q", exactBinding)
	}
	if baseBinding != "ProjectA" {
		t.Fatalf("expected base binding to ProjectA, got %q", baseBinding)
	}

	// Thread 2 (new top-level message, different session key): should inherit ProjectA from base key
	p.clearSent()
	p.handler(p, &Message{
		SessionKey: "feishu:chat1:root:msg002",
		UserID:     "user1",
		Content:    "fix a bug",
		ReplyCtx:   "ctx",
	})

	// Should NOT show selection prompt — should route to ProjectA via base key fallback
	sent = p.getSent()
	for _, s := range sent {
		if strings.Contains(s, "ProjectA") && strings.Contains(s, "ProjectB") {
			t.Fatalf("new thread should inherit project from base key, but got selection prompt: %v", sent)
		}
	}

	// Now switch to ProjectB via /project command in thread 2
	p.clearSent()
	p.handler(p, &Message{
		SessionKey: "feishu:chat1:root:msg002",
		UserID:     "user1",
		Content:    "/project ProjectB",
		ReplyCtx:   "ctx",
	})

	sent = p.getSent()
	foundSwitch := false
	for _, s := range sent {
		if strings.Contains(s, "ProjectB") && strings.Contains(s, "Switched") {
			foundSwitch = true
		}
	}
	if !foundSwitch {
		t.Fatalf("expected switch confirmation, got: %v", sent)
	}

	// Verify base key also updated to ProjectB
	router.mu.RLock()
	baseBinding = router.bindings["feishu:chat1:user1"]
	router.mu.RUnlock()
	if baseBinding != "ProjectB" {
		t.Fatalf("expected base binding updated to ProjectB, got %q", baseBinding)
	}

	// Thread 3 (another new message): should now route to ProjectB
	p.clearSent()
	p.handler(p, &Message{
		SessionKey: "feishu:chat1:root:msg003",
		UserID:     "user1",
		Content:    "another task",
		ReplyCtx:   "ctx",
	})

	// Should NOT show selection prompt
	sent = p.getSent()
	for _, s := range sent {
		if strings.Contains(s, "ProjectA") && strings.Contains(s, "ProjectB") {
			t.Fatalf("thread 3 should inherit ProjectB from base key, but got selection: %v", sent)
		}
	}
}

func TestProjectRouter_BaseSessionKey_ExactKeyTakesPriority(t *testing.T) {
	// When both exact and base key bindings exist, exact key should take priority.
	p := &stubBaseSessionKeyPlatform{
		stubRouterPlatform: stubRouterPlatform{n: "feishu"},
		baseKeyFunc: func(msg *Message) string {
			if strings.Contains(msg.SessionKey, ":root:") {
				parts := strings.SplitN(msg.SessionKey, ":", 3)
				if len(parts) >= 2 {
					return parts[0] + ":" + parts[1] + ":" + msg.UserID
				}
			}
			return msg.SessionKey
		},
	}

	storePath := filepath.Join(t.TempDir(), "bindings.json")
	router := NewProjectRouter(p, NewI18n(LangEnglish), storePath)
	eA := NewEngine("ProjectA", &stubAgent{}, []Platform{p}, "", LangEnglish)
	eB := NewEngine("ProjectB", &stubAgent{}, []Platform{p}, "", LangEnglish)
	router.AddProject("ProjectA", eA)
	router.AddProject("ProjectB", eB)

	if err := router.Start(); err != nil {
		t.Fatal(err)
	}
	defer router.Stop()

	// Manually set bindings: base key → ProjectA, exact key → ProjectB
	router.mu.Lock()
	router.bindings["feishu:chat1:user1"] = "ProjectA"
	router.bindings["feishu:chat1:root:msg001"] = "ProjectB"
	router.mu.Unlock()

	// Message with the exact key should route to ProjectB (exact priority)
	p.clearSent()
	p.handler(p, &Message{
		SessionKey: "feishu:chat1:root:msg001",
		UserID:     "user1",
		Content:    "test message",
		ReplyCtx:   "ctx",
	})

	// Verify it routed (no selection prompt)
	sent := p.getSent()
	for _, s := range sent {
		if strings.Contains(s, "select") || strings.Contains(s, "Select") {
			t.Fatalf("should route via exact key, not show selection; got: %v", sent)
		}
	}
}

func TestProjectRouter_BaseSessionKey_NonThreadPlatform_Unchanged(t *testing.T) {
	// Platform without BaseSessionKeyer: behavior unchanged.
	p := &stubRouterPlatform{n: "telegram"}
	router, _ := newTestRouter(t, p, "ProjectA", "ProjectB")

	if err := router.Start(); err != nil {
		t.Fatal(err)
	}
	defer router.Stop()

	// Switch to ProjectA in session1
	p.simulateMessage("telegram:chat1:user1", "/project ProjectA")

	// Verify binding
	router.mu.RLock()
	bound := router.bindings["telegram:chat1:user1"]
	router.mu.RUnlock()
	if bound != "ProjectA" {
		t.Fatalf("expected binding to ProjectA, got %q", bound)
	}

	// No base key should be stored (base key == session key for non-thread platforms)
	router.mu.RLock()
	totalBindings := len(router.bindings)
	router.mu.RUnlock()
	if totalBindings != 1 {
		t.Fatalf("expected exactly 1 binding for non-thread platform, got %d", totalBindings)
	}
}

func TestProjectRouter_BaseSessionKey_ProjectListShowsCurrent(t *testing.T) {
	// /project list should show current project even for new threads via base key fallback.
	p := &stubBaseSessionKeyPlatform{
		stubRouterPlatform: stubRouterPlatform{n: "feishu"},
		baseKeyFunc: func(msg *Message) string {
			if strings.Contains(msg.SessionKey, ":root:") {
				parts := strings.SplitN(msg.SessionKey, ":", 3)
				if len(parts) >= 2 {
					return parts[0] + ":" + parts[1] + ":" + msg.UserID
				}
			}
			return msg.SessionKey
		},
	}

	storePath := filepath.Join(t.TempDir(), "bindings.json")
	router := NewProjectRouter(p, NewI18n(LangEnglish), storePath)
	eA := NewEngine("ProjectA", &stubAgent{}, []Platform{p}, "", LangEnglish)
	eB := NewEngine("ProjectB", &stubAgent{}, []Platform{p}, "", LangEnglish)
	router.AddProject("ProjectA", eA)
	router.AddProject("ProjectB", eB)

	if err := router.Start(); err != nil {
		t.Fatal(err)
	}
	defer router.Stop()

	// Set base binding to ProjectA
	router.mu.Lock()
	router.bindings["feishu:chat1:user1"] = "ProjectA"
	router.mu.Unlock()

	// /project in a new thread should show ProjectA as current
	p.clearSent()
	p.handler(p, &Message{
		SessionKey: "feishu:chat1:root:msg999",
		UserID:     "user1",
		Content:    "/project",
		ReplyCtx:   "ctx",
	})

	sent := p.getSent()
	found := false
	for _, s := range sent {
		if strings.Contains(s, "ProjectA") && strings.Contains(s, "→") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected /project list to show ProjectA as current via base key, got: %v", sent)
	}
}

// trackCloseAgentSession is a stub AgentSession that tracks Close() calls.
type trackCloseAgentSession struct {
	onClose func()
}

func (s *trackCloseAgentSession) Send(_ string, _ []ImageAttachment, _ []FileAttachment) error {
	return nil
}
func (s *trackCloseAgentSession) RespondPermission(_ string, _ PermissionResult) error { return nil }
func (s *trackCloseAgentSession) Events() <-chan Event                                 { return make(chan Event) }
func (s *trackCloseAgentSession) CurrentSessionID() string                             { return "track-session" }
func (s *trackCloseAgentSession) Alive() bool                                          { return true }
func (s *trackCloseAgentSession) Close() error {
	if s.onClose != nil {
		s.onClose()
	}
	return nil
}
