package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
)

// configMu serializes read-modify-write cycles to prevent lost updates.
var configMu sync.Mutex

// ConfigPath stores the path to the config file for saving
var ConfigPath string

type Config struct {
	DataDir         string              `toml:"data_dir"` // session store directory, default ~/.cc-connect
	AttachmentSend  string              `toml:"attachment_send"`
	Projects        []ProjectConfig     `toml:"projects"`
	Commands        []CommandConfig     `toml:"commands"`     // global custom slash commands
	Aliases         []AliasConfig       `toml:"aliases"`        // global command aliases
	QuickPhrases    []QuickPhraseConfig `toml:"quick_phrases"`  // global quick phrases (short trigger → full text)
	QuickPhraseSuffix    QuickPhraseSuffixConfig    `toml:"quick_phrase_suffix"`     // auto-append suffix rule
	QuickPhraseCmdPrefix QuickPhraseCmdPrefixConfig `toml:"quick_phrase_cmd_prefix"` // "!" prefix → command execution
	BannedWords          []string                   `toml:"banned_words"`            // messages containing any of these words are blocked
	Log             LogConfig           `toml:"log"`
	Language        string              `toml:"language"` // "en" or "zh", default is "en"
	Speech          SpeechConfig        `toml:"speech"`
	TTS             TTSConfig           `toml:"tts"`
	Display         DisplayConfig       `toml:"display"`
	StreamPreview   StreamPreviewConfig `toml:"stream_preview"`  // real-time streaming preview
	RateLimit       RateLimitConfig     `toml:"rate_limit"`      // per-session rate limiting
	Relay           RelayConfig         `toml:"relay"`           // bot-to-bot relay behavior
	Quiet           *bool               `toml:"quiet,omitempty"` // global default for quiet mode; project-level overrides this
	Cron            CronConfig          `toml:"cron"`
	Webhook         WebhookConfig       `toml:"webhook"`
	Bridge          BridgeConfig        `toml:"bridge"`
	Management      ManagementConfig    `toml:"management"`
	WebUI           WebUIConfig         `toml:"webui"`                       // WebUI server for Vibe Coding
	IdleTimeoutMins *int                `toml:"idle_timeout_mins,omitempty"` // max minutes between agent events; 0 = no timeout; default 120
	Database        DatabaseConfig      `toml:"database"`                    // MySQL chat history persistence
}

// DatabaseConfig holds MySQL connection parameters for chat history persistence.
// 当 DSN 为空时表示未启用 MySQL 持久化。
type DatabaseConfig struct {
	DSN          string `toml:"dsn"`            // go-sql-driver/mysql DSN 格式, 如 "user:pass@tcp(host:port)/db?charset=utf8mb4&parseTime=true"
	MaxOpenConns int    `toml:"max_open_conns"` // 最大打开连接数, 默认 10
	MaxIdleConns int    `toml:"max_idle_conns"` // 最大空闲连接数, 默认 5
}

// CronConfig controls cron job behavior.
type CronConfig struct {
	Silent *bool `toml:"silent"` // suppress cron start notification; default false
}

// WebhookConfig controls the external HTTP webhook endpoint.
type WebhookConfig struct {
	Enabled *bool  `toml:"enabled"`         // default false
	Port    int    `toml:"port,omitempty"`  // listen port; default 9111
	Token   string `toml:"token,omitempty"` // shared secret for authentication; empty = no auth
	Path    string `toml:"path,omitempty"`  // URL path prefix; default "/hook"
}

// BridgeConfig controls the WebSocket bridge for external platform adapters.
type BridgeConfig struct {
	Enabled     *bool    `toml:"enabled"`                // default false
	Port        int      `toml:"port,omitempty"`         // listen port; default 9810
	Token       string   `toml:"token,omitempty"`        // shared secret for authentication; required
	Path        string   `toml:"path,omitempty"`         // URL path; default "/bridge/ws"
	CORSOrigins []string `toml:"cors_origins,omitempty"` // allowed CORS origins; empty = no CORS
}

// ManagementConfig controls the HTTP Management API for external tools.
type ManagementConfig struct {
	Enabled     *bool    `toml:"enabled"`                // default false
	Port        int      `toml:"port,omitempty"`         // listen port; default 9820
	Token       string   `toml:"token,omitempty"`        // shared secret for authentication; required
	CORSOrigins []string `toml:"cors_origins,omitempty"` // allowed CORS origins; empty = no CORS
}

// WebUIConfig controls the WebUI server for Vibe Coding (browser-based Claude Code interaction).
type WebUIConfig struct {
	Enabled     *bool              `toml:"enabled"`                // default false
	Port        int                `toml:"port,omitempty"`         // listen port; default 9830
	Token       string             `toml:"token,omitempty"`        // shared secret for authentication; empty = no auth
	CORSOrigins []string           `toml:"cors_origins,omitempty"` // allowed CORS origins; empty = no CORS
	StaticDir   string             `toml:"static_dir,omitempty"`   // path to Vue build output; empty = use embedded files
	Prompts         []WebUIPromptConfig `toml:"prompts,omitempty"`          // quick prompts shown in Vibe Coding UI
	ShowToolProcess *bool               `toml:"show_tool_process,omitempty"` // nil/true = show tool_use/tool_result in Vibe Coding; false = hide
}

// WebUIPromptConfig defines a quick prompt entry shown in the Vibe Coding UI.
type WebUIPromptConfig struct {
	Title   string `toml:"title"`   // short display label, e.g. "代码审查"
	Content string `toml:"content"` // full prompt text to fill into the input box
}

// DisplayConfig controls how intermediate messages (thinking, tool output) are shown.
type DisplayConfig struct {
	ThinkingMaxLen *int `toml:"thinking_max_len"` // max chars for thinking messages; 0 = no truncation; default 300
	ToolMaxLen     *int `toml:"tool_max_len"`     // max chars for tool use messages; 0 = no truncation; default 500
}

// StreamPreviewConfig controls real-time streaming preview in IM.
type StreamPreviewConfig struct {
	Enabled           *bool    `toml:"enabled"`                      // default true
	DisabledPlatforms []string `toml:"disabled_platforms,omitempty"` // platforms where preview is disabled (e.g. ["feishu"])
	IntervalMs        *int     `toml:"interval_ms"`                  // min ms between updates; default 1500
	MinDeltaChars     *int     `toml:"min_delta_chars"`              // min new chars before update; default 30
	MaxChars          *int     `toml:"max_chars"`                    // max preview length; default 2000
}

// RateLimitConfig controls per-session message rate limiting.
type RateLimitConfig struct {
	MaxMessages *int `toml:"max_messages"` // max messages per window; 0 = disabled; default 20
	WindowSecs  *int `toml:"window_secs"`  // window size in seconds; default 60
}

// UsersConfig controls per-user role assignments and policies within a project.
type UsersConfig struct {
	DefaultRole string                `toml:"default_role,omitempty"` // role for unmatched users; default "member"
	Roles       map[string]RoleConfig `toml:"roles,omitempty"`
}

// RoleConfig defines policies for a user role.
type RoleConfig struct {
	UserIDs          []string         `toml:"user_ids"`
	DisabledCommands []string         `toml:"disabled_commands,omitempty"`
	RateLimit        *RateLimitConfig `toml:"rate_limit,omitempty"` // nil = inherit global
}

// RelayConfig controls bot-to-bot relay behavior.
type RelayConfig struct {
	TimeoutSecs *int `toml:"timeout_secs"` // max seconds to wait for relay response; 0 = disabled; default 120
}

// SpeechConfig configures speech-to-text for voice messages.
type SpeechConfig struct {
	Enabled  bool   `toml:"enabled"`
	Provider string `toml:"provider"` // "openai" | "groq" | "qwen"
	Language string `toml:"language"` // e.g. "zh", "en"; empty = auto-detect
	OpenAI   struct {
		APIKey  string `toml:"api_key"`
		BaseURL string `toml:"base_url"`
		Model   string `toml:"model"`
	} `toml:"openai"`
	Groq struct {
		APIKey string `toml:"api_key"`
		Model  string `toml:"model"`
	} `toml:"groq"`
	Qwen struct {
		APIKey  string `toml:"api_key"`
		BaseURL string `toml:"base_url"`
		Model   string `toml:"model"`
	} `toml:"qwen"`
}

// TTSConfig configures text-to-speech output (mirrors SpeechConfig style).
type TTSConfig struct {
	Enabled    bool   `toml:"enabled"`
	Provider   string `toml:"provider"`     // "qwen" | "openai" | "minimax" | "espeak" | "pico" | "edge"
	Voice      string `toml:"voice"`        // default voice name (for edge: "zh-CN-XiaoxiaoNeural"; for pico: "zh-CN"; for espeak: "zh")
	TTSMode    string `toml:"tts_mode"`     // "voice_only" (default) | "always"
	MaxTextLen int    `toml:"max_text_len"` // max rune count before skipping TTS; 0 = no limit
	OpenAI     struct {
		APIKey  string `toml:"api_key"`
		BaseURL string `toml:"base_url"`
		Model   string `toml:"model"`
	} `toml:"openai"`
	Qwen struct {
		APIKey  string `toml:"api_key"`
		BaseURL string `toml:"base_url"`
		Model   string `toml:"model"`
	} `toml:"qwen"`
	MiniMax struct {
		APIKey  string `toml:"api_key"`
		BaseURL string `toml:"base_url"`
		Model   string `toml:"model"`
	} `toml:"minimax"`
}

// HeartbeatConfig controls periodic heartbeat for a project.
type HeartbeatConfig struct {
	Enabled      *bool  `toml:"enabled"`                  // default false
	IntervalMins *int   `toml:"interval_mins,omitempty"`  // minutes between heartbeats; default 30
	OnlyWhenIdle *bool  `toml:"only_when_idle,omitempty"` // only fire when the session is not busy; default true
	SessionKey   string `toml:"session_key,omitempty"`    // target session key (e.g. "telegram:123:123"); required
	Prompt       string `toml:"prompt,omitempty"`         // explicit prompt; if empty, reads HEARTBEAT.md from work_dir
	Silent       *bool  `toml:"silent,omitempty"`         // suppress heartbeat notification; default true
	TimeoutMins  *int   `toml:"timeout_mins,omitempty"`   // max execution time; default 30
}

// PatrolConfig controls periodic idle-user notification for a project.
type PatrolConfig struct {
	Enabled      *bool  `toml:"enabled"`                 // default false
	IntervalMins *int   `toml:"interval_mins,omitempty"` // minutes between patrols; default 60
	Message      string `toml:"message,omitempty"`       // fixed message to send when idle; empty = i18n default
}

// AutoCompressConfig controls automatic context compression for a project.
type AutoCompressConfig struct {
	Enabled    *bool `toml:"enabled,omitempty"`      // default false
	MaxTokens  *int  `toml:"max_tokens,omitempty"`   // estimated token threshold to trigger /compress
	MinGapMins *int  `toml:"min_gap_mins,omitempty"` // minimum minutes between auto-compress runs (default 30)
}

// ProjectConfig binds one agent (with a specific work_dir) to one or more platforms.
type ProjectConfig struct {
	Name         string             `toml:"name"`
	Mode         string             `toml:"mode,omitempty"`     // "" or "multi-workspace"
	BaseDir      string             `toml:"base_dir,omitempty"` // parent dir for workspaces
	Agent        AgentConfig        `toml:"agent"`
	Platforms    []PlatformConfig   `toml:"platforms"`
	Heartbeat    HeartbeatConfig    `toml:"heartbeat"`
	Patrol       PatrolConfig       `toml:"patrol"`
	AutoCompress AutoCompressConfig `toml:"auto_compress"`
	// ShowContextIndicator: nil/true = append [ctx: ~N%] to assistant replies; false = hide.
	ShowContextIndicator *bool        `toml:"show_context_indicator,omitempty"`
	// SessionCompleteNotify: nil/true = send "->->->-> session complete" to IM when agent finishes a turn; false = disabled.
	SessionCompleteNotify *bool       `toml:"session_complete_notify,omitempty"`
	// ShowToolProcess: nil/true = show tool_use/tool_result messages on IM; false = hide.
	ShowToolProcess      *bool        `toml:"show_tool_process,omitempty"`
	// MaxQueuedMessages: max messages queued when session is busy; nil = default (5).
	MaxQueuedMessages    *int         `toml:"max_queued_messages,omitempty"`
	Quiet                *bool        `toml:"quiet,omitempty"`             // project-level quiet mode; overrides global setting
	InjectSender         *bool        `toml:"inject_sender,omitempty"`     // prepend sender identity (platform + user ID) to each message sent to the agent
	DisabledCommands     []string     `toml:"disabled_commands,omitempty"` // commands to disable for this project (e.g. ["restart", "upgrade"])
	AdminFrom            string       `toml:"admin_from,omitempty"`        // comma-separated user IDs allowed to run privileged commands; "*" = all allowed users
	Users                *UsersConfig `toml:"users,omitempty"`             // per-user role config; nil = legacy behavior
}

type AgentConfig struct {
	Type      string           `toml:"type"`
	Options   map[string]any   `toml:"options"`
	Providers []ProviderConfig `toml:"providers"`
}

// ProviderModelConfig defines a selectable model entry for a provider,
// with an optional short alias used by the /model command.
type ProviderModelConfig struct {
	Model string `toml:"model"`
	Alias string `toml:"alias,omitempty"`
}

type ProviderConfig struct {
	Name     string                `toml:"name"`
	APIKey   string                `toml:"api_key"`
	BaseURL  string                `toml:"base_url,omitempty"`
	Model    string                `toml:"model,omitempty"`
	Models   []ProviderModelConfig `toml:"models,omitempty"`
	Thinking string                `toml:"thinking,omitempty"`
	Env      map[string]string     `toml:"env,omitempty"`
}

type PlatformConfig struct {
	Type    string         `toml:"type"`
	Options map[string]any `toml:"options"`
}

// AliasConfig maps a trigger string to a command (e.g. "帮助" → "/help").
type AliasConfig struct {
	Name    string `toml:"name"`    // trigger text (e.g. "帮助")
	Command string `toml:"command"` // target command (e.g. "/help")
}

// QuickPhraseConfig maps a short trigger to a longer phrase.
// 快捷短语配置：将简短触发词映射为完整短语（例如 "sl" → "基于上文更新或新增 skills"）。
// 运行时复用 alias 机制，精确匹配或首词匹配后自动替换。
type QuickPhraseConfig struct {
	Trigger string `toml:"trigger"` // 触发词 (e.g. "sl")
	Text    string `toml:"text"`    // 替换文本 (e.g. "基于上文更新或新增 skills")
}

// QuickPhraseSuffixConfig controls automatic suffix appending based on message length.
// 快捷短语后缀配置：当消息（经别名/快捷短语展开后）字符长度超过阈值时，自动追加后缀文本。
type QuickPhraseSuffixConfig struct {
	MinLength int    `toml:"min_length"` // 触发追加的最小字符长度（默认 0 表示禁用）
	Text      string `toml:"text"`       // 追加的后缀文本 (e.g. "举一反三")
}

// QuickPhraseCmdPrefixConfig enables a shorthand for command execution.
// 命令前缀配置：以指定前缀（如 "!"）开头的消息自动展开为命令执行指令。
// 例如 "!git status" → "执行命令: git status"
type QuickPhraseCmdPrefixConfig struct {
	Prefix   string `toml:"prefix"`   // 前缀字符 (e.g. "!")
	Template string `toml:"template"` // 展开模板，%s 占位符替换为实际命令 (e.g. "执行命令: %s")
}

// CommandConfig defines a user-customizable slash command that expands a prompt template or executes a shell command.
type CommandConfig struct {
	Name        string `toml:"name"`
	Description string `toml:"description"`
	Prompt      string `toml:"prompt"`   // prompt template (mutually exclusive with Exec)
	Exec        string `toml:"exec"`     // shell command to execute (mutually exclusive with Prompt)
	WorkDir     string `toml:"work_dir"` // optional: working directory for exec command
}

type LogConfig struct {
	Level string `toml:"level"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	cfg := &Config{
		Log: LogConfig{Level: "info"},
	}
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if cfg.DataDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			cfg.DataDir = filepath.Join(home, ".cc-connect")
		} else {
			cfg.DataDir = ".cc-connect"
		}
	}
	cfg.AttachmentSend = strings.ToLower(strings.TrimSpace(cfg.AttachmentSend))
	if cfg.AttachmentSend == "" {
		cfg.AttachmentSend = "on"
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	switch strings.ToLower(strings.TrimSpace(c.AttachmentSend)) {
	case "", "on", "off":
	default:
		return fmt.Errorf("config: attachment_send must be \"on\" or \"off\"")
	}
	if c.Relay.TimeoutSecs != nil && *c.Relay.TimeoutSecs < 0 {
		return fmt.Errorf("config: relay.timeout_secs must be >= 0")
	}

	// 数据库配置校验：DSN 必填，用于聊天记录持久化
	if c.Database.DSN == "" {
		return fmt.Errorf("config: [database].dsn is required (e.g. \"user:pass@tcp(host:port)/db?charset=utf8mb4&parseTime=true\")")
	}

	// Management API 配置校验：port 必填，WebUI 前端依赖此端口获取项目列表
	if c.Management.Port <= 0 {
		return fmt.Errorf("config: [management].port is required (e.g. 9820)")
	}

	if len(c.Projects) == 0 {
		return fmt.Errorf("config: at least one [[projects]] entry is required")
	}
	for i, proj := range c.Projects {
		prefix := fmt.Sprintf("projects[%d]", i)
		if proj.Name == "" {
			return fmt.Errorf("config: %s.name is required", prefix)
		}
		if proj.Agent.Type == "" {
			return fmt.Errorf("config: %s.agent.type is required", prefix)
		}
		if len(proj.Platforms) == 0 {
			return fmt.Errorf("config: %s needs at least one [[projects.platforms]]", prefix)
		}
		for j, p := range proj.Platforms {
			if p.Type == "" {
				return fmt.Errorf("config: %s.platforms[%d].type is required", prefix, j)
			}
		}
		if proj.Mode == "multi-workspace" {
			if proj.BaseDir == "" {
				return fmt.Errorf("project %q: multi-workspace mode requires base_dir", proj.Name)
			}
			if _, ok := proj.Agent.Options["work_dir"]; ok {
				return fmt.Errorf("project %q: multi-workspace mode conflicts with agent work_dir (use base_dir instead)", proj.Name)
			}
		}
		if err := validateUsersConfig(prefix, proj.Users); err != nil {
			return err
		}
	}
	return nil
}

// validateUsersConfig checks the [projects.users] section for consistency.
func validateUsersConfig(prefix string, u *UsersConfig) error {
	if u == nil {
		return nil
	}
	if len(u.Roles) == 0 {
		return fmt.Errorf("config: %s.users has no roles defined", prefix)
	}
	wildcardCount := 0
	seenUserIDs := make(map[string]string) // userID → role name
	for roleName, rc := range u.Roles {
		if len(rc.UserIDs) == 0 {
			return fmt.Errorf("config: %s.users.roles.%s has empty user_ids", prefix, roleName)
		}
		for _, uid := range rc.UserIDs {
			if uid == "*" {
				wildcardCount++
				continue
			}
			lower := strings.ToLower(uid)
			if prev, dup := seenUserIDs[lower]; dup {
				return fmt.Errorf("config: %s.users: user %q appears in both role %q and %q", prefix, uid, prev, roleName)
			}
			seenUserIDs[lower] = roleName
		}
	}
	if wildcardCount > 1 {
		return fmt.Errorf("config: %s.users: wildcard user_ids=[\"*\"] appears in multiple roles", prefix)
	}
	if u.DefaultRole != "" {
		if _, ok := u.Roles[u.DefaultRole]; !ok {
			return fmt.Errorf("config: %s.users.default_role %q does not match any defined role", prefix, u.DefaultRole)
		}
	}
	return nil
}

// readAndParse reads the config file and returns the parsed Config and raw bytes.
// Caller must hold configMu.
func readAndParse() (*Config, []byte, error) {
	if ConfigPath == "" {
		return nil, nil, fmt.Errorf("config path not set")
	}
	data, err := os.ReadFile(ConfigPath)
	if err != nil {
		return nil, nil, err
	}
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, nil, err
	}
	return &cfg, data, nil
}

// mutateAndSave reads the current config, applies fn to modify it, and
// atomically saves the result. This is the standard read-modify-write pattern
// for simple field mutations that don't need to preserve TOML comments.
func mutateAndSave(fn func(cfg *Config) error) error {
	configMu.Lock()
	defer configMu.Unlock()

	cfg, _, err := readAndParse()
	if err != nil {
		return err
	}
	if err := fn(cfg); err != nil {
		return err
	}
	return saveConfig(cfg)
}

// SaveActiveProvider persists the active provider name for a project.
func SaveActiveProvider(projectName, providerName string) error {
	return mutateAndSave(func(cfg *Config) error {
		for i := range cfg.Projects {
			if cfg.Projects[i].Name == projectName {
				if cfg.Projects[i].Agent.Options == nil {
					cfg.Projects[i].Agent.Options = make(map[string]any)
				}
				cfg.Projects[i].Agent.Options["provider"] = providerName
				break
			}
		}
		return nil
	})
}

// SaveProviderModel persists the selected model for a provider in a project.
func SaveProviderModel(projectName, providerName, model string) error {
	return mutateAndSave(func(cfg *Config) error {
		for i := range cfg.Projects {
			if cfg.Projects[i].Name != projectName {
				continue
			}
			for j := range cfg.Projects[i].Agent.Providers {
				if cfg.Projects[i].Agent.Providers[j].Name == providerName {
					cfg.Projects[i].Agent.Providers[j].Model = model
					return nil
				}
			}
			return fmt.Errorf("provider %q not found in project %q", providerName, projectName)
		}
		return fmt.Errorf("project %q not found in config", projectName)
	})
}

// SaveAgentModel persists the selected default model for a project's agent.
func SaveAgentModel(projectName, model string) error {
	return mutateAndSave(func(cfg *Config) error {
		for i := range cfg.Projects {
			if cfg.Projects[i].Name != projectName {
				continue
			}
			if cfg.Projects[i].Agent.Options == nil {
				cfg.Projects[i].Agent.Options = make(map[string]any)
			}
			cfg.Projects[i].Agent.Options["model"] = model
			return nil
		}
		return fmt.Errorf("project %q not found in config", projectName)
	})
}

// AddProviderToConfig adds a provider to a project's agent config and saves.
func AddProviderToConfig(projectName string, provider ProviderConfig) error {
	return mutateAndSave(func(cfg *Config) error {
		for i := range cfg.Projects {
			if cfg.Projects[i].Name == projectName {
				for _, existing := range cfg.Projects[i].Agent.Providers {
					if existing.Name == provider.Name {
						return fmt.Errorf("provider %q already exists in project %q", provider.Name, projectName)
					}
				}
				cfg.Projects[i].Agent.Providers = append(cfg.Projects[i].Agent.Providers, provider)
				return nil
			}
		}
		return fmt.Errorf("project %q not found in config", projectName)
	})
}

// RemoveProviderFromConfig removes a provider from a project's agent config and saves.
func RemoveProviderFromConfig(projectName, providerName string) error {
	return mutateAndSave(func(cfg *Config) error {
		for i := range cfg.Projects {
			if cfg.Projects[i].Name == projectName {
				providers := cfg.Projects[i].Agent.Providers
				for j := range providers {
					if providers[j].Name == providerName {
						cfg.Projects[i].Agent.Providers = append(providers[:j], providers[j+1:]...)
						return nil
					}
				}
				return fmt.Errorf("provider %q not found in project %q", providerName, projectName)
			}
		}
		return fmt.Errorf("provider %q not found in project %q", providerName, projectName)
	})
}

func saveConfig(cfg *Config) error {
	dir := filepath.Dir(ConfigPath)
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpPath := tmp.Name()

	if err := toml.NewEncoder(tmp).Encode(cfg); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("encode config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, ConfigPath)
}

// SaveLanguage saves the language setting to the config file.
func SaveLanguage(lang string) error {
	return mutateAndSave(func(cfg *Config) error {
		cfg.Language = lang
		return nil
	})
}

// ListProjects returns project names from the config file.
func ListProjects() ([]string, error) {
	if ConfigPath == "" {
		return nil, fmt.Errorf("config path not set")
	}
	data, err := os.ReadFile(ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg := &Config{}
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	var names []string
	for _, p := range cfg.Projects {
		names = append(names, p.Name)
	}
	return names, nil
}

// AddCommand adds a global custom command and persists to config.
func AddCommand(cmd CommandConfig) error {
	return mutateAndSave(func(cfg *Config) error {
		for _, c := range cfg.Commands {
			if c.Name == cmd.Name {
				return fmt.Errorf("command %q already exists", cmd.Name)
			}
		}
		cfg.Commands = append(cfg.Commands, cmd)
		return nil
	})
}

// RemoveCommand removes a global custom command and persists to config.
func RemoveCommand(name string) error {
	return mutateAndSave(func(cfg *Config) error {
		found := false
		var remaining []CommandConfig
		for _, c := range cfg.Commands {
			if c.Name == name {
				found = true
			} else {
				remaining = append(remaining, c)
			}
		}
		if !found {
			return fmt.Errorf("command %q not found", name)
		}
		cfg.Commands = remaining
		return nil
	})
}

// AddAlias adds a global alias and persists to config.
func AddAlias(alias AliasConfig) error {
	return mutateAndSave(func(cfg *Config) error {
		for i, a := range cfg.Aliases {
			if a.Name == alias.Name {
				cfg.Aliases[i] = alias
				return nil
			}
		}
		cfg.Aliases = append(cfg.Aliases, alias)
		return nil
	})
}

// RemoveAlias removes a global alias and persists to config.
func RemoveAlias(name string) error {
	return mutateAndSave(func(cfg *Config) error {
		found := false
		var remaining []AliasConfig
		for _, a := range cfg.Aliases {
			if a.Name == name {
				found = true
			} else {
				remaining = append(remaining, a)
			}
		}
		if !found {
			return fmt.Errorf("alias %q not found", name)
		}
		cfg.Aliases = remaining
		return nil
	})
}

// SaveDisplayConfig persists the display truncation settings to the config file.
func SaveDisplayConfig(thinkingMaxLen, toolMaxLen *int) error {
	return mutateAndSave(func(cfg *Config) error {
		if thinkingMaxLen != nil {
			cfg.Display.ThinkingMaxLen = thinkingMaxLen
		}
		if toolMaxLen != nil {
			cfg.Display.ToolMaxLen = toolMaxLen
		}
		return nil
	})
}

// SaveTTSMode persists the TTS mode setting to the config file.
func SaveTTSMode(mode string) error {
	return mutateAndSave(func(cfg *Config) error {
		cfg.TTS.TTSMode = mode
		return nil
	})
}

// GetProjectProviders returns providers for a given project.
func GetProjectProviders(projectName string) ([]ProviderConfig, string, error) {
	if ConfigPath == "" {
		return nil, "", fmt.Errorf("config path not set")
	}
	data, err := os.ReadFile(ConfigPath)
	if err != nil {
		return nil, "", fmt.Errorf("read config: %w", err)
	}
	cfg := &Config{}
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, "", fmt.Errorf("parse config: %w", err)
	}
	for _, p := range cfg.Projects {
		if p.Name == projectName {
			active, _ := p.Agent.Options["provider"].(string)
			return p.Agent.Providers, active, nil
		}
	}
	return nil, "", fmt.Errorf("project %q not found", projectName)
}

// FeishuCredentialUpdateOptions controls how Feishu/Lark platform credentials
// are written back into config.toml for a specific project.
type FeishuCredentialUpdateOptions struct {
	ProjectName       string // required
	PlatformIndex     int    // 1-based index among feishu/lark platforms in the project; 0 = first
	PlatformType      string // optional target type: "feishu" or "lark"; empty keeps existing type
	AppID             string // required
	AppSecret         string // required
	OwnerOpenID       string // optional owner id from onboarding flow
	SetAllowFromEmpty bool   // when true, set allow_from=OwnerOpenID only if currently empty
}

// EnsureProjectWithFeishuOptions controls project auto-provisioning for Feishu/Lark setup.
type EnsureProjectWithFeishuOptions struct {
	ProjectName      string // required
	PlatformType     string // optional: "feishu" or "lark", default "feishu"
	CloneFromProject string // optional source project name to clone agent config from
	WorkDir          string // optional default work_dir when creating project
	AgentType        string // optional default agent type when no source project exists, default "codex"
}

// EnsureProjectWithFeishuResult describes whether project provisioning created a new project.
type EnsureProjectWithFeishuResult struct {
	Created          bool
	AddedPlatform    bool
	ProjectIndex     int
	PlatformAbsIndex int // first feishu/lark platform in project, -1 if absent
	PlatformType     string
}

// FeishuCredentialUpdateResult describes where credentials were written.
type FeishuCredentialUpdateResult struct {
	ProjectName      string
	ProjectIndex     int
	PlatformAbsIndex int // absolute index in projects[i].platforms
	PlatformType     string
	AllowFrom        string
}

// EnsureProjectWithFeishuPlatform ensures target project exists. If project does
// not exist, it creates one with a Feishu/Lark platform so credentials can be
// written immediately.
func EnsureProjectWithFeishuPlatform(opts EnsureProjectWithFeishuOptions) (*EnsureProjectWithFeishuResult, error) {
	configMu.Lock()
	defer configMu.Unlock()

	if ConfigPath == "" {
		return nil, fmt.Errorf("config path not set")
	}
	projectName := strings.TrimSpace(opts.ProjectName)
	if projectName == "" {
		return nil, fmt.Errorf("project name is required")
	}

	platformType := strings.ToLower(strings.TrimSpace(opts.PlatformType))
	if platformType == "" {
		platformType = "feishu"
	}
	if platformType != "feishu" && platformType != "lark" {
		return nil, fmt.Errorf("invalid platform type %q (want feishu or lark)", opts.PlatformType)
	}

	cfg, data, err := readAndParse()
	if err != nil {
		return nil, err
	}
	raw := string(data)

	for i := range cfg.Projects {
		if cfg.Projects[i].Name != projectName {
			continue
		}
		platformIdx := firstFeishuPlatformIndex(cfg.Projects[i].Platforms)
		added := false
		if platformIdx < 0 {
			lines, hadTrailing := splitConfigLines(raw)
			spans := buildRawProjectSpans(lines)
			if i >= len(spans) {
				return nil, fmt.Errorf("project %q located in parsed config but not raw file", projectName)
			}
			insertAt := spans[i].end + 1
			block := make([]string, 0, 7)
			if insertAt > 0 && strings.TrimSpace(lines[insertAt-1]) != "" {
				block = append(block, "")
			}
			block = append(block, "[[projects.platforms]]")
			block = append(block, fmt.Sprintf("type = %s", quoteTomlString(platformType)))
			block = append(block, "")
			block = append(block, "[projects.platforms.options]")
			if insertAt < len(lines) && strings.TrimSpace(lines[insertAt]) != "" {
				block = append(block, "")
			}
			lines = insertLines(lines, insertAt, block)
			if err := writeRawConfig(joinConfigLines(lines, hadTrailing)); err != nil {
				return nil, err
			}
			platformIdx = len(cfg.Projects[i].Platforms)
			added = true
		}
		return &EnsureProjectWithFeishuResult{
			Created:          false,
			AddedPlatform:    added,
			ProjectIndex:     i,
			PlatformAbsIndex: platformIdx,
			PlatformType:     platformType,
		}, nil
	}

	proj := ProjectConfig{
		Name:      projectName,
		Agent:     pickAgentTemplateForNewProject(cfg, opts),
		Platforms: []PlatformConfig{{Type: platformType, Options: map[string]any{}}},
	}
	if proj.Agent.Type == "" {
		proj.Agent.Type = "codex"
	}
	if proj.Agent.Options == nil {
		proj.Agent.Options = map[string]any{}
	}
	workDir := strings.TrimSpace(opts.WorkDir)
	if workDir != "" {
		proj.Agent.Options["work_dir"] = workDir
	}

	lines, hadTrailing := splitConfigLines(raw)
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
		lines = append(lines, "")
	}
	lines = append(lines, "[[projects]]")
	lines = append(lines, fmt.Sprintf("name = %s", quoteTomlString(proj.Name)))
	lines = append(lines, "")
	lines = append(lines, "[projects.agent]")
	lines = append(lines, fmt.Sprintf("type = %s", quoteTomlString(proj.Agent.Type)))
	lines = append(lines, "")
	lines = append(lines, "[projects.agent.options]")
	if wd, ok := proj.Agent.Options["work_dir"].(string); ok && strings.TrimSpace(wd) != "" {
		lines = append(lines, fmt.Sprintf("work_dir = %s", quoteTomlString(wd)))
	}
	lines = append(lines, "")
	lines = append(lines, "[[projects.platforms]]")
	lines = append(lines, fmt.Sprintf("type = %s", quoteTomlString(platformType)))
	lines = append(lines, "")
	lines = append(lines, "[projects.platforms.options]")
	if err := writeRawConfig(joinConfigLines(lines, hadTrailing)); err != nil {
		return nil, err
	}

	return &EnsureProjectWithFeishuResult{
		Created:          true,
		AddedPlatform:    false,
		ProjectIndex:     len(cfg.Projects) - 1,
		PlatformAbsIndex: len(cfg.Projects[len(cfg.Projects)-1].Platforms) - 1,
		PlatformType:     platformType,
	}, nil
}

// SaveFeishuPlatformCredentials updates app_id/app_secret for a project's
// Feishu/Lark platform and persists the config atomically.
func SaveFeishuPlatformCredentials(opts FeishuCredentialUpdateOptions) (*FeishuCredentialUpdateResult, error) {
	configMu.Lock()
	defer configMu.Unlock()

	if ConfigPath == "" {
		return nil, fmt.Errorf("config path not set")
	}
	if strings.TrimSpace(opts.ProjectName) == "" {
		return nil, fmt.Errorf("project name is required")
	}
	if strings.TrimSpace(opts.AppID) == "" || strings.TrimSpace(opts.AppSecret) == "" {
		return nil, fmt.Errorf("app_id and app_secret are required")
	}
	if opts.PlatformIndex < 0 {
		return nil, fmt.Errorf("platform index must be >= 0")
	}
	if opts.PlatformType != "" && opts.PlatformType != "feishu" && opts.PlatformType != "lark" {
		return nil, fmt.Errorf("invalid platform type %q (want feishu or lark)", opts.PlatformType)
	}

	cfg, data, err := readAndParse()
	if err != nil {
		return nil, err
	}
	raw := string(data)

	projectIdx := -1
	for i := range cfg.Projects {
		if cfg.Projects[i].Name == opts.ProjectName {
			projectIdx = i
			break
		}
	}
	if projectIdx < 0 {
		return nil, fmt.Errorf("project %q not found", opts.ProjectName)
	}

	proj := &cfg.Projects[projectIdx]
	candidates := make([]int, 0, len(proj.Platforms))
	for i := range proj.Platforms {
		t := strings.ToLower(strings.TrimSpace(proj.Platforms[i].Type))
		if t == "feishu" || t == "lark" {
			candidates = append(candidates, i)
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("project %q has no feishu/lark platform", opts.ProjectName)
	}

	targetPos := 0
	if opts.PlatformIndex > 0 {
		targetPos = opts.PlatformIndex - 1
	}
	if targetPos < 0 || targetPos >= len(candidates) {
		return nil, fmt.Errorf(
			"platform index %d out of range: project %q has %d feishu/lark platform(s)",
			opts.PlatformIndex, opts.ProjectName, len(candidates),
		)
	}

	absIdx := candidates[targetPos]
	platform := &proj.Platforms[absIdx]
	if opts.PlatformType != "" {
		platform.Type = opts.PlatformType
	}
	if platform.Options == nil {
		platform.Options = map[string]any{}
	}

	platform.Options["app_id"] = strings.TrimSpace(opts.AppID)
	platform.Options["app_secret"] = strings.TrimSpace(opts.AppSecret)

	allowFrom := strings.TrimSpace(stringOption(platform.Options["allow_from"]))
	if opts.SetAllowFromEmpty && allowFrom == "" && strings.TrimSpace(opts.OwnerOpenID) != "" {
		allowFrom = strings.TrimSpace(opts.OwnerOpenID)
	}

	lines, hadTrailing := splitConfigLines(raw)
	spans := buildRawProjectSpans(lines)
	if projectIdx >= len(spans) {
		return nil, fmt.Errorf("project %q located in parsed config but not raw file", opts.ProjectName)
	}
	if absIdx >= len(spans[projectIdx].platforms) {
		return nil, fmt.Errorf("feishu/lark platform located in parsed config but not raw file")
	}

	reloadSpan := func() rawPlatformSpan {
		spans = buildRawProjectSpans(lines)
		return spans[projectIdx].platforms[absIdx]
	}
	span := spans[projectIdx].platforms[absIdx]

	if opts.PlatformType != "" {
		if span.typeLine >= 0 {
			lines[span.typeLine] = replaceTomlStringKeyLine(lines[span.typeLine], "type", opts.PlatformType)
		} else {
			lines = insertLines(lines, span.start+1, []string{fmt.Sprintf("type = %s", quoteTomlString(opts.PlatformType))})
		}
		span = reloadSpan()
	}

	if span.optionsStart < 0 {
		insertAt := span.end + 1
		block := make([]string, 0, 4)
		if insertAt > 0 && strings.TrimSpace(lines[insertAt-1]) != "" {
			block = append(block, "")
		}
		block = append(block, "[projects.platforms.options]")
		if insertAt < len(lines) && strings.TrimSpace(lines[insertAt]) != "" {
			block = append(block, "")
		}
		lines = insertLines(lines, insertAt, block)
		span = reloadSpan()
	}

	lines = upsertTomlStringKey(lines, span.optionsStart+1, span.optionsEnd, "app_id", strings.TrimSpace(opts.AppID))
	span = reloadSpan()
	lines = upsertTomlStringKey(lines, span.optionsStart+1, span.optionsEnd, "app_secret", strings.TrimSpace(opts.AppSecret))
	span = reloadSpan()
	if opts.SetAllowFromEmpty && strings.TrimSpace(opts.OwnerOpenID) != "" {
		lines = upsertTomlStringKey(lines, span.optionsStart+1, span.optionsEnd, "allow_from", allowFrom)
		span = reloadSpan()
	}

	if err := writeRawConfig(joinConfigLines(lines, hadTrailing)); err != nil {
		return nil, err
	}

	return &FeishuCredentialUpdateResult{
		ProjectName:      opts.ProjectName,
		ProjectIndex:     projectIdx,
		PlatformAbsIndex: absIdx,
		PlatformType:     platform.Type,
		AllowFrom:        allowFrom,
	}, nil
}

func stringOption(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func firstFeishuPlatformIndex(platforms []PlatformConfig) int {
	for i := range platforms {
		t := strings.ToLower(strings.TrimSpace(platforms[i].Type))
		if t == "feishu" || t == "lark" {
			return i
		}
	}
	return -1
}

func firstWeixinPlatformIndex(platforms []PlatformConfig) int {
	for i := range platforms {
		t := strings.ToLower(strings.TrimSpace(platforms[i].Type))
		if t == "weixin" {
			return i
		}
	}
	return -1
}

// EnsureProjectWithWeixinOptions controls project auto-provisioning for Weixin (ilink) setup.
type EnsureProjectWithWeixinOptions struct {
	ProjectName      string
	CloneFromProject string
	WorkDir          string
	AgentType        string
}

// EnsureProjectWithWeixinResult describes whether project provisioning created a new project or platform block.
type EnsureProjectWithWeixinResult struct {
	Created          bool
	AddedPlatform    bool
	ProjectIndex     int
	PlatformAbsIndex int
}

// WeixinCredentialUpdateOptions updates token (and optional URLs) for a project's Weixin platform.
type WeixinCredentialUpdateOptions struct {
	ProjectName       string
	PlatformIndex     int // 1-based index among weixin platforms; 0 = first
	Token             string
	BaseURL           string // optional; empty = do not change in TOML
	CDNBaseURL        string // optional; empty = do not change
	AccountID         string // optional ilink_bot_id → options.account_id
	ScannedUserID     string // optional ilink_user_id for allow_from when SetAllowFromEmpty
	SetAllowFromEmpty bool
}

// WeixinCredentialUpdateResult describes where credentials were written.
type WeixinCredentialUpdateResult struct {
	ProjectName      string
	ProjectIndex     int
	PlatformAbsIndex int
	AllowFrom        string
}

// EnsureProjectWithWeixinPlatform ensures the target project exists and has a weixin platform entry.
func EnsureProjectWithWeixinPlatform(opts EnsureProjectWithWeixinOptions) (*EnsureProjectWithWeixinResult, error) {
	configMu.Lock()
	defer configMu.Unlock()

	if ConfigPath == "" {
		return nil, fmt.Errorf("config path not set")
	}
	projectName := strings.TrimSpace(opts.ProjectName)
	if projectName == "" {
		return nil, fmt.Errorf("project name is required")
	}

	cfg, data, err := readAndParse()
	if err != nil {
		return nil, err
	}
	raw := string(data)

	for i := range cfg.Projects {
		if cfg.Projects[i].Name != projectName {
			continue
		}
		platformIdx := firstWeixinPlatformIndex(cfg.Projects[i].Platforms)
		added := false
		if platformIdx < 0 {
			lines, hadTrailing := splitConfigLines(raw)
			spans := buildRawProjectSpans(lines)
			if i >= len(spans) {
				return nil, fmt.Errorf("project %q located in parsed config but not raw file", projectName)
			}
			insertAt := spans[i].end + 1
			block := make([]string, 0, 7)
			if insertAt > 0 && strings.TrimSpace(lines[insertAt-1]) != "" {
				block = append(block, "")
			}
			block = append(block, "[[projects.platforms]]")
			block = append(block, `type = "weixin"`)
			block = append(block, "")
			block = append(block, "[projects.platforms.options]")
			if insertAt < len(lines) && strings.TrimSpace(lines[insertAt]) != "" {
				block = append(block, "")
			}
			lines = insertLines(lines, insertAt, block)
			if err := writeRawConfig(joinConfigLines(lines, hadTrailing)); err != nil {
				return nil, err
			}
			platformIdx = len(cfg.Projects[i].Platforms)
			added = true
		}
		return &EnsureProjectWithWeixinResult{
			Created:          false,
			AddedPlatform:    added,
			ProjectIndex:     i,
			PlatformAbsIndex: platformIdx,
		}, nil
	}

	proj := ProjectConfig{
		Name:      projectName,
		Agent:     pickAgentTemplateForNewProject(cfg, EnsureProjectWithFeishuOptions{CloneFromProject: opts.CloneFromProject, WorkDir: opts.WorkDir, AgentType: opts.AgentType}),
		Platforms: []PlatformConfig{{Type: "weixin", Options: map[string]any{}}},
	}
	if proj.Agent.Type == "" {
		proj.Agent.Type = "codex"
	}
	if proj.Agent.Options == nil {
		proj.Agent.Options = map[string]any{}
	}
	workDir := strings.TrimSpace(opts.WorkDir)
	if workDir != "" {
		proj.Agent.Options["work_dir"] = workDir
	}

	lines, hadTrailing := splitConfigLines(raw)
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
		lines = append(lines, "")
	}
	lines = append(lines, "[[projects]]")
	lines = append(lines, fmt.Sprintf("name = %s", quoteTomlString(proj.Name)))
	lines = append(lines, "")
	lines = append(lines, "[projects.agent]")
	lines = append(lines, fmt.Sprintf("type = %s", quoteTomlString(proj.Agent.Type)))
	lines = append(lines, "")
	lines = append(lines, "[projects.agent.options]")
	if wd, ok := proj.Agent.Options["work_dir"].(string); ok && strings.TrimSpace(wd) != "" {
		lines = append(lines, fmt.Sprintf("work_dir = %s", quoteTomlString(wd)))
	}
	lines = append(lines, "")
	lines = append(lines, "[[projects.platforms]]")
	lines = append(lines, `type = "weixin"`)
	lines = append(lines, "")
	lines = append(lines, "[projects.platforms.options]")
	if err := writeRawConfig(joinConfigLines(lines, hadTrailing)); err != nil {
		return nil, err
	}

	return &EnsureProjectWithWeixinResult{
		Created:          true,
		AddedPlatform:    false,
		ProjectIndex:     len(cfg.Projects),
		PlatformAbsIndex: 0,
	}, nil
}

// SaveWeixinPlatformCredentials updates token (and optional fields) for a project's Weixin platform.
func SaveWeixinPlatformCredentials(opts WeixinCredentialUpdateOptions) (*WeixinCredentialUpdateResult, error) {
	configMu.Lock()
	defer configMu.Unlock()

	if ConfigPath == "" {
		return nil, fmt.Errorf("config path not set")
	}
	if strings.TrimSpace(opts.ProjectName) == "" {
		return nil, fmt.Errorf("project name is required")
	}
	if strings.TrimSpace(opts.Token) == "" {
		return nil, fmt.Errorf("token is required")
	}
	if opts.PlatformIndex < 0 {
		return nil, fmt.Errorf("platform index must be >= 0")
	}

	cfg, data, err := readAndParse()
	if err != nil {
		return nil, err
	}
	raw := string(data)

	projectIdx := -1
	for i := range cfg.Projects {
		if cfg.Projects[i].Name == opts.ProjectName {
			projectIdx = i
			break
		}
	}
	if projectIdx < 0 {
		return nil, fmt.Errorf("project %q not found", opts.ProjectName)
	}

	proj := &cfg.Projects[projectIdx]
	candidates := make([]int, 0, len(proj.Platforms))
	for i := range proj.Platforms {
		t := strings.ToLower(strings.TrimSpace(proj.Platforms[i].Type))
		if t == "weixin" {
			candidates = append(candidates, i)
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("project %q has no weixin platform", opts.ProjectName)
	}

	targetPos := 0
	if opts.PlatformIndex > 0 {
		targetPos = opts.PlatformIndex - 1
	}
	if targetPos < 0 || targetPos >= len(candidates) {
		return nil, fmt.Errorf(
			"platform index %d out of range: project %q has %d weixin platform(s)",
			opts.PlatformIndex, opts.ProjectName, len(candidates),
		)
	}

	absIdx := candidates[targetPos]
	platform := &proj.Platforms[absIdx]
	if platform.Options == nil {
		platform.Options = map[string]any{}
	}

	token := strings.TrimSpace(opts.Token)
	platform.Options["token"] = token

	if u := strings.TrimSpace(opts.BaseURL); u != "" {
		platform.Options["base_url"] = u
	}
	if u := strings.TrimSpace(opts.CDNBaseURL); u != "" {
		platform.Options["cdn_base_url"] = u
	}
	if id := strings.TrimSpace(opts.AccountID); id != "" {
		platform.Options["account_id"] = id
	}

	allowFrom := strings.TrimSpace(stringOption(platform.Options["allow_from"]))
	if opts.SetAllowFromEmpty && allowFrom == "" && strings.TrimSpace(opts.ScannedUserID) != "" {
		allowFrom = strings.TrimSpace(opts.ScannedUserID)
		platform.Options["allow_from"] = allowFrom
	}

	lines, hadTrailing := splitConfigLines(raw)
	spans := buildRawProjectSpans(lines)
	if projectIdx >= len(spans) {
		return nil, fmt.Errorf("project %q located in parsed config but not raw file", opts.ProjectName)
	}
	if absIdx >= len(spans[projectIdx].platforms) {
		return nil, fmt.Errorf("weixin platform located in parsed config but not raw file")
	}

	reloadSpan := func() rawPlatformSpan {
		spans = buildRawProjectSpans(lines)
		return spans[projectIdx].platforms[absIdx]
	}
	span := spans[projectIdx].platforms[absIdx]

	if span.optionsStart < 0 {
		insertAt := span.end + 1
		block := make([]string, 0, 4)
		if insertAt > 0 && strings.TrimSpace(lines[insertAt-1]) != "" {
			block = append(block, "")
		}
		block = append(block, "[projects.platforms.options]")
		if insertAt < len(lines) && strings.TrimSpace(lines[insertAt]) != "" {
			block = append(block, "")
		}
		lines = insertLines(lines, insertAt, block)
		span = reloadSpan()
	}

	lines = upsertTomlStringKey(lines, span.optionsStart+1, span.optionsEnd, "token", token)
	span = reloadSpan()

	if u := strings.TrimSpace(opts.BaseURL); u != "" {
		lines = upsertTomlStringKey(lines, span.optionsStart+1, span.optionsEnd, "base_url", u)
		span = reloadSpan()
	}
	if u := strings.TrimSpace(opts.CDNBaseURL); u != "" {
		lines = upsertTomlStringKey(lines, span.optionsStart+1, span.optionsEnd, "cdn_base_url", u)
		span = reloadSpan()
	}
	if id := strings.TrimSpace(opts.AccountID); id != "" {
		lines = upsertTomlStringKey(lines, span.optionsStart+1, span.optionsEnd, "account_id", id)
		span = reloadSpan()
	}
	if opts.SetAllowFromEmpty && strings.TrimSpace(opts.ScannedUserID) != "" {
		lines = upsertTomlStringKey(lines, span.optionsStart+1, span.optionsEnd, "allow_from", allowFrom)
		span = reloadSpan()
	}

	if err := writeRawConfig(joinConfigLines(lines, hadTrailing)); err != nil {
		return nil, err
	}

	return &WeixinCredentialUpdateResult{
		ProjectName:      opts.ProjectName,
		ProjectIndex:     projectIdx,
		PlatformAbsIndex: absIdx,
		AllowFrom:        allowFrom,
	}, nil
}

func pickAgentTemplateForNewProject(cfg *Config, opts EnsureProjectWithFeishuOptions) AgentConfig {
	cloneName := strings.TrimSpace(opts.CloneFromProject)
	if cloneName != "" {
		for i := range cfg.Projects {
			if cfg.Projects[i].Name == cloneName {
				return cloneAgentConfig(cfg.Projects[i].Agent)
			}
		}
	}
	if len(cfg.Projects) > 0 {
		return cloneAgentConfig(cfg.Projects[0].Agent)
	}

	agentType := strings.TrimSpace(opts.AgentType)
	if agentType == "" {
		agentType = "codex"
	}
	return AgentConfig{
		Type:    agentType,
		Options: map[string]any{},
	}
}

func cloneAgentConfig(in AgentConfig) AgentConfig {
	out := AgentConfig{
		Type:    in.Type,
		Options: cloneAnyMap(in.Options),
	}
	if len(in.Providers) > 0 {
		out.Providers = make([]ProviderConfig, len(in.Providers))
		for i := range in.Providers {
			out.Providers[i] = ProviderConfig{
				Name:     in.Providers[i].Name,
				APIKey:   in.Providers[i].APIKey,
				BaseURL:  in.Providers[i].BaseURL,
				Model:    in.Providers[i].Model,
				Models:   append([]ProviderModelConfig(nil), in.Providers[i].Models...),
				Thinking: in.Providers[i].Thinking,
				Env:      cloneStringMap(in.Providers[i].Env),
			}
		}
	}
	return out
}

func cloneAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

type rawProjectSpan struct {
	start     int
	end       int
	platforms []rawPlatformSpan
}

type rawPlatformSpan struct {
	start        int
	end          int
	typeLine     int
	optionsStart int
	optionsEnd   int
}

func splitConfigLines(raw string) ([]string, bool) {
	if raw == "" {
		return []string{}, false
	}
	hadTrailing := strings.HasSuffix(raw, "\n")
	raw = strings.TrimSuffix(raw, "\n")
	if raw == "" {
		return []string{}, hadTrailing
	}
	return strings.Split(raw, "\n"), hadTrailing
}

func joinConfigLines(lines []string, hadTrailing bool) string {
	out := strings.Join(lines, "\n")
	if hadTrailing || len(lines) > 0 {
		out += "\n"
	}
	return out
}

func buildRawProjectSpans(lines []string) []rawProjectSpan {
	projectStarts := make([]int, 0, 4)
	for i := range lines {
		if matchTableHeader(lines[i], "[[projects]]") {
			projectStarts = append(projectStarts, i)
		}
	}
	if len(projectStarts) == 0 {
		return nil
	}

	spans := make([]rawProjectSpan, 0, len(projectStarts))
	for i, start := range projectStarts {
		end := len(lines) - 1
		if i+1 < len(projectStarts) {
			end = projectStarts[i+1] - 1
		}
		span := rawProjectSpan{start: start, end: end}

		platformStarts := make([]int, 0, 2)
		for ln := start + 1; ln <= end; ln++ {
			if matchTableHeader(lines[ln], "[[projects.platforms]]") {
				platformStarts = append(platformStarts, ln)
			}
		}
		for p, pstart := range platformStarts {
			pend := end
			if p+1 < len(platformStarts) {
				pend = platformStarts[p+1] - 1
			}
			ps := rawPlatformSpan{
				start:        pstart,
				end:          pend,
				typeLine:     -1,
				optionsStart: -1,
				optionsEnd:   -1,
			}
			inMainPlatformTable := true
			for ln := pstart + 1; ln <= pend; ln++ {
				if isAnyTableHeader(lines[ln]) {
					inMainPlatformTable = false
				}
				if inMainPlatformTable && ps.typeLine < 0 && matchTomlStringKey(lines[ln], "type") {
					ps.typeLine = ln
				}
				if ps.optionsStart < 0 && matchTableHeader(lines[ln], "[projects.platforms.options]") {
					ps.optionsStart = ln
					ps.optionsEnd = pend
					for j := ln + 1; j <= pend; j++ {
						if isAnyTableHeader(lines[j]) {
							ps.optionsEnd = j - 1
							break
						}
					}
				}
			}
			span.platforms = append(span.platforms, ps)
		}

		spans = append(spans, span)
	}
	return spans
}

func matchTableHeader(line, header string) bool {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, header) {
		return false
	}
	if len(t) == len(header) {
		return true
	}
	next := t[len(header)]
	return next == ' ' || next == '\t' || next == '#'
}

func isAnyTableHeader(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "[")
}

func matchTomlStringKey(line, key string) bool {
	t := strings.TrimSpace(line)
	if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "[") {
		return false
	}
	if !strings.HasPrefix(t, key) {
		return false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(t, key))
	return strings.HasPrefix(rest, "=")
}

func insertLines(lines []string, at int, block []string) []string {
	if at < 0 {
		at = 0
	}
	if at > len(lines) {
		at = len(lines)
	}
	out := make([]string, 0, len(lines)+len(block))
	out = append(out, lines[:at]...)
	out = append(out, block...)
	out = append(out, lines[at:]...)
	return out
}

func upsertTomlStringKey(lines []string, start, end int, key, value string) []string {
	if start < 0 {
		start = 0
	}
	if end >= len(lines) {
		end = len(lines) - 1
	}
	for i := start; i <= end && i < len(lines); i++ {
		if matchTomlStringKey(lines[i], key) {
			lines[i] = replaceTomlStringKeyLine(lines[i], key, value)
			return lines
		}
	}
	insertAt := end + 1
	if insertAt < start {
		insertAt = start
	}
	return insertLines(lines, insertAt, []string{fmt.Sprintf("%s = %s", key, quoteTomlString(value))})
}

func replaceTomlStringKeyLine(line, key, value string) string {
	indent := leadingWhitespace(line)
	comment := extractLineComment(line)
	updated := fmt.Sprintf("%s%s = %s", indent, key, quoteTomlString(value))
	if comment != "" {
		updated += " " + comment
	}
	return updated
}

func quoteTomlString(value string) string {
	return strconv.Quote(value)
}

func leadingWhitespace(s string) string {
	i := 0
	for i < len(s) {
		if s[i] != ' ' && s[i] != '\t' {
			break
		}
		i++
	}
	return s[:i]
}

func extractLineComment(line string) string {
	inQuote := false
	escaped := false
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && inQuote {
			escaped = true
			continue
		}
		if ch == '"' {
			inQuote = !inQuote
			continue
		}
		if ch == '#' && !inQuote {
			return strings.TrimSpace(line[i:])
		}
	}
	return ""
}

func writeRawConfig(content string) error {
	dir := filepath.Dir(ConfigPath)
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, ConfigPath)
}
