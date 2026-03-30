---
name: config-startup-validation
description: >
  This skill should be used when the user asks about "config validation",
  "startup validation", "required config", "config.toml required fields",
  "validate()", "database.dsn required", "management.port required",
  "config error on startup", "Error loading config", "config test fixtures",
  "validBaseConfig", "baseConfigTOML", "config_test.go fixtures",
  or needs to add new required config fields, debug config loading errors,
  or understand how cc-connect validates configuration at startup.
---

# Config Startup Validation

## Purpose

Document how cc-connect validates `config.toml` at startup, how to add new
required fields, and how to keep test fixtures in sync. This is a reusable
pattern for any "fail fast on bad config" requirement.

## How It Works

```
config.Load(path)
  → toml.Unmarshal → cfg struct
  → cfg.validate() ← ALL CHECKS HERE
  → return cfg or error

main.go:
  cfg, err := config.Load(configPath)
  if err != nil {
      fmt.Fprintf(os.Stderr, "Error loading config (%s): %v\n", configPath, err)
      os.Exit(1)    ← process exits immediately with clear error
  }
```

Validation runs **before** any server starts. If validation fails, the process
prints the error and exits. No partial startup, no silent degradation.

## Key Files

| File | Role |
|------|------|
| `config/config.go` | `Config` struct, `Load()`, `validate()` |
| `config/config_test.go` | `TestConfigValidate`, TOML fixtures, `validBaseConfig()` |
| `cmd/cc-connect/main.go` | Calls `config.Load()`, exits on error |

## Current Required Fields

```go
// config/config.go — validate()
func (c *Config) validate() error {
    // 1. 数据库 DSN 必填（聊天记录持久化依赖）
    if c.Database.DSN == "" {
        return fmt.Errorf("config: [database].dsn is required (...)")
    }

    // 2. Management API 端口必填（WebUI 前端依赖）
    if c.Management.Port <= 0 {
        return fmt.Errorf("config: [management].port is required (e.g. 9820)")
    }

    // 3. 至少一个项目
    if len(c.Projects) == 0 {
        return fmt.Errorf("config: at least one [[projects]] entry is required")
    }

    // 4. 项目级校验（name, agent.type, platforms, multi-workspace）
    for i, proj := range c.Projects { ... }
}
```

**校验顺序很重要**：基础设施校验（database、management）在前，项目级校验在后。
这样缺 database.dsn 时不会报"缺少项目"的误导错误。

## 添加新的必填字段 — Checklist

### Step 1: 在 `validate()` 中添加检查

```go
// config/config.go — validate() 中添加
if c.NewSection.RequiredField == "" {
    return fmt.Errorf("config: [new_section].required_field is required (e.g. \"example_value\")")
}
```

错误信息规范：
- 前缀 `config:` 表明来源
- 包含配置路径 `[section].field`
- 包含示例值 `(e.g. ...)`

### Step 2: 更新测试 — `TestConfigValidate`

新增一个测试用例：

```go
{
    name: "requires new_section field",
    cfg: func() Config {
        c := validBaseConfig()
        c.NewSection.RequiredField = ""  // 置空触发校验
        // 但要保留其他必填字段
        c.Projects = []ProjectConfig{validProject("demo")}
        return c
    }(),
    wantErr: "[new_section].required_field is required",
},
```

### Step 3: 更新 `validBaseConfig()` 辅助函数

```go
func validBaseConfig() Config {
    return Config{
        Database:   DatabaseConfig{DSN: "test:test@tcp(127.0.0.1:3306)/test?..."},
        Management: ManagementConfig{Port: 9820},
        NewSection: NewSectionConfig{RequiredField: "test_value"},  // ← 新增
    }
}
```

### Step 4: 更新所有 TOML fixture 常量

所有 `const xxxTOML = \`...\`` 和 `const xxxFixture = \`...\`` 常量都必须包含
新的必填字段，否则 `TestLoad_*` 系列测试会因 `validate()` 失败而报错。

```go
const baseConfigTOML = `
[database]
dsn = "test:test@tcp(127.0.0.1:3306)/test?charset=utf8mb4&parseTime=true"

[management]
port = 9820

[new_section]
required_field = "test_value"

[[projects]]
...
`
```

**所有 fixture 都要更新**，包括：
`baseConfigTOML`, `providerConfigTOML`, `feishuConfigFixture`,
`projectWithoutFeishuFixture`, `weixinConfigFixture`, `preserveFormatFixture`,
`attachmentSendConfigFixture`, `relayConfigFixture`, `relayConfigNegativeFixture`

### Step 5: 验证

```bash
go test ./config/ -v -count=1   # config 测试
go test ./core/ -count=1        # core 测试（可能也用到 config）
go build ./...                  # 完整构建
```

## 常见坑

### 坑 1: 只改了 validate() 没改 fixture

**症状**：`TestLoad_*` 系列全部 FAIL，但 `TestConfigValidate` 部分通过。
**原因**：TOML fixture 缺少新必填字段 → `Load()` 调 `validate()` 报错。
**修复**：更新所有 `const xxxTOML/xxxFixture`。

### 坑 2: 校验顺序导致测试 wantErr 不匹配

**症状**：`TestConfigValidate/requires_at_least_one_project` 报错，但 wantErr
不匹配——因为先触发了新添加的校验。
**原因**：新校验写在 projects 校验之前，空 Config 先触发新校验。
**修复**：用 `validBaseConfig()` 构造测试 Config，确保只缺目标字段。

### 坑 3: DSN 格式错误不在 validate() 中检测

`validate()` 只检查 DSN 非空。DSN 格式错误（如缺 `tcp()`）在 `NewMySQLChatStore`
的 `PingContext` 阶段才会报错。这是有意的设计——`validate()` 做静态检查，
网络连通性检查在运行时。

## 举一反三

| 新需求 | validate() 中添加 |
|--------|-------------------|
| Redis 必须配置 | `if c.Redis.Addr == ""` |
| WebUI 启用时 port 必填 | `if c.WebUI.Enabled && c.WebUI.Port <= 0` |
| 至少配一个平台 token | 遍历 platforms 检查 |
| 日志级别只能是特定值 | `switch c.Log.Level` |

模式始终相同：`validate()` 中加 if + 描述性 error → 更新 `validBaseConfig()` →
更新所有 fixture → 运行测试。

## Related Skills

- **`add-new-feature`** — 通用功能添加 checklist
- **`vibe-chat-history`** — 聊天记录持久化（依赖 database.dsn 校验）
- **`webui-vibe-coding`** — WebUI 架构（依赖 management.port 校验）
