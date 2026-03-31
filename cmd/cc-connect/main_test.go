package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhg5/cc-connect/config"
	"github.com/chenhg5/cc-connect/core"
)

type stubMainAgent struct {
	workDir string
}

func (a *stubMainAgent) Name() string { return "stub-main" }

func (a *stubMainAgent) StartSession(_ context.Context, _ string) (core.AgentSession, error) {
	return &stubMainAgentSession{}, nil
}

func (a *stubMainAgent) ListSessions(_ context.Context) ([]core.AgentSessionInfo, error) {
	return nil, nil
}

func (a *stubMainAgent) Stop() error { return nil }

func (a *stubMainAgent) SetWorkDir(dir string) {
	a.workDir = dir
}

func (a *stubMainAgent) GetWorkDir() string {
	return a.workDir
}

type stubMainAgentSession struct{}

func (s *stubMainAgentSession) Send(string, []core.ImageAttachment, []core.FileAttachment) error {
	return nil
}
func (s *stubMainAgentSession) RespondPermission(string, core.PermissionResult) error { return nil }
func (s *stubMainAgentSession) Events() <-chan core.Event                             { return nil }
func (s *stubMainAgentSession) Close() error                                          { return nil }
func (s *stubMainAgentSession) CurrentSessionID() string                              { return "" }
func (s *stubMainAgentSession) Alive() bool                                           { return true }

func TestProjectStatePath(t *testing.T) {
	dataDir := t.TempDir()
	got := projectStatePath(dataDir, "my/project:one")
	want := filepath.Join(dataDir, "projects", "my_project_one.state.json")
	if got != want {
		t.Fatalf("projectStatePath() = %q, want %q", got, want)
	}
}

func TestApplyProjectStateOverride(t *testing.T) {
	baseDir := t.TempDir()
	overrideDir := filepath.Join(t.TempDir(), "override")
	if err := os.Mkdir(overrideDir, 0o755); err != nil {
		t.Fatalf("mkdir override dir: %v", err)
	}

	store := core.NewProjectStateStore(filepath.Join(t.TempDir(), "projects", "demo.state.json"))
	store.SetWorkDirOverride(overrideDir)

	agent := &stubMainAgent{workDir: baseDir}
	got := applyProjectStateOverride("demo", agent, baseDir, store)

	if got != overrideDir {
		t.Fatalf("applyProjectStateOverride() = %q, want %q", got, overrideDir)
	}
	if agent.workDir != overrideDir {
		t.Fatalf("agent workDir = %q, want %q", agent.workDir, overrideDir)
	}
}

func TestBootstrapConfig_CreatesFileAndParentDir(t *testing.T) {
	// bootstrapConfig should create parent directories and write a valid config file
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "deep", "config.toml")

	if err := bootstrapConfig(path); err != nil {
		t.Fatalf("bootstrapConfig() error: %v", err)
	}

	// Verify file exists
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("config file not created: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("config file is empty")
	}
}

func TestBootstrapConfig_ContainsRequiredSections(t *testing.T) {
	// The generated config template should include all required sections
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := bootstrapConfig(path); err != nil {
		t.Fatalf("bootstrapConfig() error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	content := string(data)

	// Check that required sections are present
	requiredSections := []string{
		"[database]",
		"dsn =",
		"[management]",
		"port =",
		"[[projects]]",
		"[projects.agent]",
		"[[projects.platforms]]",
	}
	for _, section := range requiredSections {
		if !strings.Contains(content, section) {
			t.Errorf("bootstrap config missing required section: %s", section)
		}
	}
}

func TestBootstrapConfig_LoadableByConfigParser(t *testing.T) {
	// The generated config file should be parseable by config.Load()
	// (it may fail validate() because of placeholder values, but should parse)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := bootstrapConfig(path); err != nil {
		t.Fatalf("bootstrapConfig() error: %v", err)
	}

	// config.Load calls validate(), which is expected to pass with the template
	// values (dsn is set, port is set, project has name+agent+platform)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load() on bootstrap config failed: %v", err)
	}
	if len(cfg.Projects) == 0 {
		t.Fatal("bootstrap config has no projects")
	}
	if cfg.Projects[0].Name != "my-project" {
		t.Errorf("project name = %q, want %q", cfg.Projects[0].Name, "my-project")
	}
	if cfg.Database.DSN == "" {
		t.Error("bootstrap config has empty database.dsn")
	}
	if cfg.Management.Port <= 0 {
		t.Error("bootstrap config has invalid management.port")
	}
}

func TestResolveConfigPath_ExplicitFlag(t *testing.T) {
	got := resolveConfigPath("/custom/path/config.toml")
	if got != "/custom/path/config.toml" {
		t.Fatalf("resolveConfigPath(explicit) = %q, want %q", got, "/custom/path/config.toml")
	}
}

func TestResolveConfigPath_FallsBackToHome(t *testing.T) {
	// When no explicit flag and no ./config.toml, should resolve to ~/.cc-connect/config.toml
	origDir, _ := os.Getwd()
	tmpDir := t.TempDir()
	os.Chdir(tmpDir) // ensure no config.toml in cwd
	defer os.Chdir(origDir)

	got := resolveConfigPath("")
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".cc-connect", "config.toml")
	if got != want {
		t.Fatalf("resolveConfigPath(\"\") = %q, want %q", got, want)
	}
}
