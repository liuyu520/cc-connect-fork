package core

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ---------------------------------------------------------------------------
// WebUIServer — serves the Vue SPA and provides a WebSocket endpoint for
// Vibe Coding (direct Claude Code CLI interaction from the browser).
// ---------------------------------------------------------------------------

// WebUIServer serves a Vue single-page application and provides
// a WebSocket endpoint for real-time interaction with Claude Code CLI.
type WebUIServer struct {
	port        int
	token       string
	corsOrigins []string
	staticDir   string    // optional: serve files from disk instead of embed
	staticFS    fs.FS     // embedded static files (web/dist)
	chatStore   ChatStore // optional: 聊天记录持久化（MySQL）
	prompts         []WebUIPrompt // 常用提示词（从配置加载）
	showToolProcess bool          // true = forward tool_use/tool_result to frontend; false = hide
	server          *http.Server
}

// WebUIPrompt is a quick prompt entry shown in the Vibe Coding UI.
type WebUIPrompt struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

// NewWebUIServer creates a new WebUI server.
func NewWebUIServer(port int, token string, corsOrigins []string, staticDir string, staticFS fs.FS) *WebUIServer {
	if port <= 0 {
		port = 9830
	}
	return &WebUIServer{
		port:            port,
		token:           token,
		corsOrigins:     corsOrigins,
		staticDir:       staticDir,
		staticFS:        staticFS,
		showToolProcess: true, // 默认显示工具调用过程
	}
}

// SetChatStore 注入聊天记录持久化存储。
func (s *WebUIServer) SetChatStore(cs ChatStore) {
	s.chatStore = cs
}

// SetPrompts 注入常用提示词（从配置加载）。
func (s *WebUIServer) SetPrompts(prompts []WebUIPrompt) {
	s.prompts = prompts
}

// SetShowToolProcess controls whether tool_use/tool_result messages are forwarded to the frontend.
func (s *WebUIServer) SetShowToolProcess(show bool) {
	s.showToolProcess = show
}

// Start launches the HTTP server.
func (s *WebUIServer) Start() {
	mux := http.NewServeMux()

	// WebSocket endpoint for Vibe Coding
	mux.HandleFunc("/api/vibe/ws", s.handleVibeWS)

	// Vibe Coding 聊天历史 REST API
	mux.HandleFunc("/api/vibe/sessions", s.handleVibeSessions)
	mux.HandleFunc("/api/vibe/sessions/", s.handleVibeSessionMessages)

	// 常用提示词 REST API
	mux.HandleFunc("/api/vibe/prompts", s.handleVibePrompts)

	// 导出 Markdown REST API
	mux.HandleFunc("/api/vibe/export", s.handleVibeExportMarkdown)

	// Serve static files (Vue SPA)
	var fileServer http.Handler
	if s.staticDir != "" {
		// 从磁盘目录提供静态文件（开发模式）
		fileServer = http.FileServer(http.Dir(s.staticDir))
	} else if s.staticFS != nil {
		// 从 embed.FS 提供静态文件（生产模式）
		fileServer = http.FileServer(http.FS(s.staticFS))
	}

	if fileServer != nil {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			s.setCORS(w, r)
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			// SPA fallback: 对于非静态文件的请求，返回 index.html
			path := r.URL.Path
			if path != "/" && !strings.Contains(path, ".") {
				// 不含扩展名的路径视为 SPA 路由，返回 index.html
				r.URL.Path = "/"
			}
			fileServer.ServeHTTP(w, r)
		})
	}

	s.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.port),
		Handler: mux,
	}

	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("webui: server error", "error", err)
		}
	}()
	slog.Info("webui: server started", "port", s.port)
}

// Stop shuts down the server.
func (s *WebUIServer) Stop() {
	if s.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.server.Shutdown(ctx); err != nil && err != http.ErrServerClosed {
			slog.Debug("webui: server shutdown failed", "error", err)
		}
	}
}

// setCORS sets CORS headers for the response.
func (s *WebUIServer) setCORS(w http.ResponseWriter, r *http.Request) {
	if len(s.corsOrigins) == 0 {
		return
	}
	origin := r.Header.Get("Origin")
	for _, o := range s.corsOrigins {
		if o == "*" || o == origin {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Max-Age", "86400")
			break
		}
	}
}

// authenticate checks the token for WebSocket connections.
func (s *WebUIServer) authenticate(r *http.Request) bool {
	if s.token == "" {
		return true
	}
	// Query param token (for WebSocket connections)
	if t := r.URL.Query().Get("token"); t != "" {
		return t == s.token
	}
	// Bearer token
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ") == s.token
	}
	return false
}

// ---------------------------------------------------------------------------
// WebSocket handler for Vibe Coding
// ---------------------------------------------------------------------------

// WebSocket upgrader (allow all origins, token auth is handled separately)
var webuiUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// handleVibeWS handles the WebSocket connection for Vibe Coding.
// Protocol:
//
//	Client → Server:
//	  {"type": "start", "workDir": "/path", "model": ""}
//	  {"type": "send", "message": "...", "attachments": [{"type":"image|file","name":"...","mime_type":"...","data":"base64..."}]}
//	  {"type": "permission", "request_id": "xxx", "behavior": "allow"}
//	  {"type": "abort"}
//	Server → Client:
//	  {"type": "connected", "status": "ok"}
//	  {"type": "session_id", "session_id": "xxx"}
//	  {"type": "text", "content": "..."}
//	  {"type": "tool_use", "tool_name": "...", "tool_input": "...", "tool_input_full": {...}}
//	  {"type": "thinking", "content": "..."}
//	  {"type": "result", "content": "...", "input_tokens": N, "output_tokens": N}
//	  {"type": "permission_request", "request_id": "...", "tool_name": "...", ...}
//	  {"type": "tool_result", "tool_name": "...", "content": "..."}
//	  {"type": "error", "message": "..."}
//	  {"type": "status", "alive": bool}
func (s *WebUIServer) handleVibeWS(w http.ResponseWriter, r *http.Request) {
	if !s.authenticate(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := webuiUpgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("webui: websocket upgrade failed", "error", err)
		return
	}
	defer conn.Close()

	// 增大 WebSocket 读取限制，支持 base64 编码的附件（~10MB 文件 ≈ ~14MB base64）
	conn.SetReadLimit(20 * 1024 * 1024)

	slog.Info("webui: new vibe coding connection", "remote", conn.RemoteAddr())

	var session *webuiSession
	var readCancel context.CancelFunc
	var writeMu sync.Mutex

	// sendJSON 向前端发送 JSON 消息（带写超时，防止网络异常时无限阻塞）
	sendJSON := func(data map[string]any) {
		writeMu.Lock()
		defer writeMu.Unlock()
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := conn.WriteJSON(data); err != nil {
			slog.Debug("webui: ws send failed", "error", err)
		}
	}

	// 发送连接成功消息
	sendJSON(map[string]any{"type": "connected", "status": "ok"})

	// 清理函数
	defer func() {
		if readCancel != nil {
			readCancel()
		}
		if session != nil {
			session.stop()
		}
		slog.Info("webui: vibe coding connection closed")
	}()

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				slog.Debug("webui: ws read error", "error", err)
			}
			return
		}

		var msg map[string]any
		if err := json.Unmarshal(raw, &msg); err != nil {
			sendJSON(map[string]any{"type": "error", "message": "无效的 JSON 消息"})
			continue
		}

		msgType, _ := msg["type"].(string)

		switch msgType {
		case "ping":
			// 心跳检测：前端每 30 秒发送 ping，后端回复 pong
			sendJSON(map[string]any{"type": "pong"})
			continue

		case "start":
			// 启动新的 Claude Code 会话
			workDir, _ := msg["workDir"].(string)
			model, _ := msg["model"].(string)

			// 验证工作目录
			if workDir == "" {
				sendJSON(map[string]any{"type": "error", "message": "工作目录不能为空"})
				continue
			}
			info, statErr := os.Stat(workDir)
			if statErr != nil || !info.IsDir() {
				sendJSON(map[string]any{"type": "error", "message": fmt.Sprintf("无效的工作目录: %s", workDir)})
				continue
			}

			// 关闭已有会话
			if readCancel != nil {
				readCancel()
			}
			if session != nil {
				session.stop()
			}

			// 创建并启动新会话
			slog.Info("webui: creating vibe session", "chatStore_nil", s.chatStore == nil, "work_dir", workDir)
			session = newWebuiSession(workDir, model, s.chatStore, s.showToolProcess)
			if err := session.start(); err != nil {
				if strings.Contains(err.Error(), "executable file not found") {
					sendJSON(map[string]any{
						"type":    "error",
						"message": "Claude Code CLI 未安装，请先安装: npm install -g @anthropic-ai/claude-code",
					})
				} else {
					sendJSON(map[string]any{
						"type":    "error",
						"message": fmt.Sprintf("启动 Claude Code 失败: %s", err),
					})
				}
				session = nil
				continue
			}

			// 启动后台事件转发任务
			var readCtx context.Context
			readCtx, readCancel = context.WithCancel(context.Background())
			go session.forwardEvents(readCtx, sendJSON)

			sendJSON(map[string]any{
				"type":    "status",
				"alive":   true,
				"message": "Claude Code 已启动",
			})

			// 持久化会话元信息
			if session.chatStore != nil {
				slog.Info("webui: persisting vibe session", "vibe_id", session.vibeID, "work_dir", workDir)
				session.chatStore.EnsureSession(context.Background(), ChatSessionInfo{
					SessionID:  session.vibeID,
					SessionKey: "vibe:" + workDir,
					Project:    workDir,
					AgentType:  "claudecode",
					Name:       workDir,
					BizType:    "vibe",
				})
			}

		case "send":
			// 发送用户消息（支持附件）
			if session == nil || !session.alive() {
				sendJSON(map[string]any{"type": "error", "message": "Claude Code 未连接，请先启动会话"})
				continue
			}
			message, _ := msg["message"].(string)

			// 解析附件
			var images []ImageAttachment
			var files []FileAttachment
			if rawAttach, ok := msg["attachments"].([]any); ok {
				for _, item := range rawAttach {
					att, _ := item.(map[string]any)
					if att == nil {
						continue
					}
					attType, _ := att["type"].(string)
					name, _ := att["name"].(string)
					mimeType, _ := att["mime_type"].(string)
					dataStr, _ := att["data"].(string)
					data, decErr := base64.StdEncoding.DecodeString(dataStr)
					if decErr != nil {
						slog.Warn("webui: base64 decode failed", "name", name, "error", decErr)
						sendJSON(map[string]any{"type": "error", "message": fmt.Sprintf("附件解码失败: %s", name)})
						continue
					}
					if len(data) > 10*1024*1024 {
						sendJSON(map[string]any{"type": "error", "message": fmt.Sprintf("文件过大（>10MB）：%s", name)})
						continue
					}
					if attType == "image" {
						images = append(images, ImageAttachment{MimeType: mimeType, Data: data, FileName: name})
					} else {
						files = append(files, FileAttachment{MimeType: mimeType, Data: data, FileName: name})
					}
				}
			}

			if strings.TrimSpace(message) == "" && len(images) == 0 && len(files) == 0 {
				continue
			}

			// 统一调用 sendWithAttachments（无附件时内部走纯文本路径）
			if err := session.sendWithAttachments(message, images, files); err != nil {
				sendJSON(map[string]any{"type": "error", "message": fmt.Sprintf("发送消息失败: %s", err)})
			}
			// 持久化用户消息
			if session.chatStore != nil {
				slog.Info("webui: saving user message", "vibe_id", session.vibeID, "content_len", len(message))
				session.chatStore.SaveMessage(context.Background(), ChatMessage{
					SessionID: session.vibeID,
					Role:      "user",
					Content:   message,
					Platform:  "vibe",
					BizType:   "vibe",
				})
			}

		case "permission":
			// 响应权限请求
			if session == nil || !session.alive() {
				sendJSON(map[string]any{"type": "error", "message": "Claude Code 未连接"})
				continue
			}
			requestID, _ := msg["request_id"].(string)
			behavior, _ := msg["behavior"].(string)
			if err := session.respondPermission(requestID, behavior); err != nil {
				sendJSON(map[string]any{"type": "error", "message": fmt.Sprintf("权限响应失败: %s", err)})
			}

		case "abort":
			// 中断当前执行
			if session != nil && session.alive() {
				session.abort()
			}

		default:
			sendJSON(map[string]any{"type": "error", "message": fmt.Sprintf("未知消息类型: %s", msgType)})
		}
	}
}

// ---------------------------------------------------------------------------
// webuiSession — manages a single Claude Code CLI process for Vibe Coding
// ---------------------------------------------------------------------------

// webuiSession manages a single Claude Code CLI subprocess.
type webuiSession struct {
	workDir         string
	model           string
	cmd             *exec.Cmd
	stdin           *json.Encoder
	stdout          *bufio.Scanner
	sessionID       string
	vibeID          string    // 持久化用的 Vibe 会话 ID（UUID）
	chatStore       ChatStore // 可选：聊天记录持久化
	showToolProcess bool      // true = forward tool_use/tool_result to frontend
	pendingInputs   map[string]map[string]any // 缓存权限请求的原始工具输入，key 为 request_id
	mu              sync.Mutex // 保护状态字段 (isAlive, pendingInputs, sessionID)
	stdinMu         sync.Mutex // 保护 stdin 写操作，避免与 mu 嵌套导致死锁
	isAlive         bool
}

// newWebuiSession creates a new session configuration.
func newWebuiSession(workDir, model string, chatStore ChatStore, showToolProcess bool) *webuiSession {
	return &webuiSession{
		workDir:         workDir,
		model:           model,
		vibeID:          fmt.Sprintf("vibe-%d", time.Now().UnixNano()),
		chatStore:       chatStore,
		showToolProcess: showToolProcess,
		pendingInputs:   make(map[string]map[string]any),
	}
}

// start launches the Claude Code CLI subprocess.
func (s *webuiSession) start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 构建命令参数
	// --permission-prompt-tool stdio: 让 Claude Code 通过 stdin/stdout 传递权限请求，
	// 而非内部 TTY 交互，否则 control_request 事件不会输出到 stdout。
	// --permission-mode bypassPermissions: 参考 IM 端（agent/claudecode/session.go），
	// 使用 auto/YOLO 模式，所有工具调用自动通过，不需要用户确认。
	args := []string{
		"--output-format", "stream-json",
		"--input-format", "stream-json",
		"--permission-prompt-tool", "stdio",
		"--permission-mode", "bypassPermissions",
		"--verbose",
	}
	if s.model != "" {
		args = append(args, "--model", s.model)
	}

	s.cmd = exec.Command("claude", args...)
	s.cmd.Dir = s.workDir

	// 过滤环境变量，移除 CLAUDECODE 以防止嵌套检测
	env := os.Environ()
	filteredEnv := make([]string, 0, len(env))
	for _, e := range env {
		if !strings.HasPrefix(e, "CLAUDECODE") {
			filteredEnv = append(filteredEnv, e)
		}
	}
	s.cmd.Env = filteredEnv

	// 设置 stdin/stdout 管道
	stdinPipe, err := s.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("webui: stdin pipe: %w", err)
	}
	stdoutPipe, err := s.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("webui: stdout pipe: %w", err)
	}
	// stderr 丢弃（避免阻塞）
	s.cmd.Stderr = nil

	if err := s.cmd.Start(); err != nil {
		return fmt.Errorf("webui: start claude: %w", err)
	}

	s.stdin = json.NewEncoder(stdinPipe)
	s.stdout = bufio.NewScanner(stdoutPipe)
	// 增大 scanner buffer 以应对大的 JSON 行
	s.stdout.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	s.isAlive = true

	slog.Info("webui: claude code started", "pid", s.cmd.Process.Pid, "work_dir", s.workDir)
	return nil
}

// send sends a user message to the Claude Code process.
// mu 只用于检查状态，stdinMu 保护实际的 stdin 写操作，避免死锁。
func (s *webuiSession) send(message string) error {
	s.mu.Lock()
	alive := s.isAlive
	encoder := s.stdin
	s.mu.Unlock()

	if !alive || encoder == nil {
		return fmt.Errorf("process not running")
	}

	// 构建 stream-json 输入消息
	payload := map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": message,
		},
	}
	s.stdinMu.Lock()
	defer s.stdinMu.Unlock()
	return encoder.Encode(payload)
}

// sendWithAttachments sends a user message with optional image/file attachments to the Claude Code process.
// Without attachments it falls back to the simple text path (content: string).
// With attachments it builds a multimodal content array mirroring claudeSession.Send():
//   - Images: saved to disk + base64 image content block
//   - Files: saved to disk via SaveFilesToDisk + text references via AppendFileRefs
//   - Text: {"type":"text","text": promptWithRefs}
//
// mu 只用于检查状态，stdinMu 保护实际的 stdin 写操作，避免死锁。
func (s *webuiSession) sendWithAttachments(message string, images []ImageAttachment, files []FileAttachment) error {
	// 无附件走纯文本快速路径
	if len(images) == 0 && len(files) == 0 {
		return s.send(message)
	}

	s.mu.Lock()
	alive := s.isAlive
	encoder := s.stdin
	workDir := s.workDir
	s.mu.Unlock()

	if !alive || encoder == nil {
		return fmt.Errorf("process not running")
	}

	attachDir := filepath.Join(workDir, ".cc-connect", "attachments")
	if err := os.MkdirAll(attachDir, 0o755); err != nil {
		slog.Warn("webui: mkdir attachments failed", "error", err, "path", attachDir)
	}

	var parts []map[string]any
	var savedImagePaths []string

	// 编码图片为 base64 content block 并保存到磁盘
	for i, img := range images {
		ext := ExtFromMime(img.MimeType)
		fname := fmt.Sprintf("img_%d_%d%s", time.Now().UnixMilli(), i, ext)
		fpath := filepath.Join(attachDir, fname)
		if err := os.WriteFile(fpath, img.Data, 0o644); err != nil {
			slog.Error("webui: save image failed", "error", err)
			continue
		}
		savedImagePaths = append(savedImagePaths, fpath)
		slog.Debug("webui: image saved", "path", fpath, "size", len(img.Data))

		mimeType := img.MimeType
		if mimeType == "" {
			mimeType = "image/png"
		}
		parts = append(parts, map[string]any{
			"type": "image",
			"source": map[string]any{
				"type":       "base64",
				"media_type": mimeType,
				"data":       base64.StdEncoding.EncodeToString(img.Data),
			},
		})
	}

	// 保存文件到磁盘，获取路径引用
	filePaths := SaveFilesToDisk(workDir, files)

	// 构建文本部分：用户 prompt + 文件路径引用
	textPart := message
	if textPart == "" && len(filePaths) > 0 {
		textPart = "Please analyze the attached file(s)."
	} else if textPart == "" {
		textPart = "Please analyze the attached image(s)."
	}
	if len(savedImagePaths) > 0 {
		textPart += "\n\n(Images also saved locally: " + strings.Join(savedImagePaths, ", ") + ")"
	}
	if len(filePaths) > 0 {
		textPart += "\n\n(Files saved locally, please read them: " + strings.Join(filePaths, ", ") + ")"
	}
	parts = append(parts, map[string]any{"type": "text", "text": textPart})

	s.stdinMu.Lock()
	defer s.stdinMu.Unlock()
	return encoder.Encode(map[string]any{
		"type":    "user",
		"message": map[string]any{"role": "user", "content": parts},
	})
}

// respondPermission sends a permission response to the Claude Code process.
// mu 只用于检查状态和读取 pendingInputs，stdinMu 保护实际的 stdin 写操作。
func (s *webuiSession) respondPermission(requestID, behavior string) error {
	s.mu.Lock()
	alive := s.isAlive
	encoder := s.stdin

	if !alive || encoder == nil {
		s.mu.Unlock()
		return fmt.Errorf("process not running")
	}

	// 构建权限响应消息（参照 agent/claudecode/session.go RespondPermission）
	var permResponse map[string]any
	if behavior == "allow" {
		// 回传原始工具输入作为 updatedInput
		updatedInput := s.pendingInputs[requestID]
		if updatedInput == nil {
			updatedInput = make(map[string]any)
		}
		delete(s.pendingInputs, requestID)
		permResponse = map[string]any{
			"behavior":     "allow",
			"updatedInput": updatedInput,
		}
	} else {
		delete(s.pendingInputs, requestID)
		permResponse = map[string]any{
			"behavior": "deny",
			"message":  "The user denied this tool use. Stop and wait for the user's instructions.",
		}
	}
	s.mu.Unlock() // 释放 mu 后再做 I/O，避免与 forwardEvents 争锁

	payload := map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "success",
			"request_id": requestID,
			"response":   permResponse,
		},
	}
	s.stdinMu.Lock()
	defer s.stdinMu.Unlock()
	return encoder.Encode(payload)
}

// forwardEvents reads events from stdout and forwards them to the WebSocket client.
func (s *webuiSession) forwardEvents(ctx context.Context, sendJSON func(map[string]any)) {
	defer func() {
		s.mu.Lock()
		s.isAlive = false
		s.mu.Unlock()
		sendJSON(map[string]any{"type": "status", "alive": false})
	}()

	for s.stdout.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		line := strings.TrimSpace(s.stdout.Text())
		if line == "" {
			continue
		}

		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			slog.Debug("webui: invalid JSON line", "line", line[:min(200, len(line))])
			continue
		}

		// 解析事件并转换为前端消息
		msgs := s.parseEvent(event)
		for _, msg := range msgs {
			sendJSON(msg)
		}
	}

	if err := s.stdout.Err(); err != nil {
		slog.Debug("webui: stdout read error", "error", err)
	}
}

// parseEvent converts a Claude Code stream-json event into frontend messages.
func (s *webuiSession) parseEvent(event map[string]any) []map[string]any {
	var msgs []map[string]any
	eventType, _ := event["type"].(string)

	switch eventType {
	case "system":
		// 系统初始化事件，包含 session_id
		if sid, ok := event["session_id"].(string); ok {
			s.sessionID = sid
			msgs = append(msgs, map[string]any{
				"type":       "session_id",
				"session_id": sid,
			})
		}

	case "assistant":
		// AI 助手回复，content 是一个数组
		messageData, _ := event["message"].(map[string]any)
		if messageData == nil {
			break
		}
		contentList, _ := messageData["content"].([]any)
		for _, item := range contentList {
			block, _ := item.(map[string]any)
			if block == nil {
				continue
			}
			blockType, _ := block["type"].(string)
			switch blockType {
			case "text":
				text, _ := block["text"].(string)
				msgs = append(msgs, map[string]any{
					"type":    "text",
					"content": text,
				})
			case "tool_use":
				toolName, _ := block["name"].(string)
				// AskUserQuestion 走权限请求通道（control_request），跳过 tool_use 事件避免前端收到多余消息
				if toolName == "AskUserQuestion" {
					continue
				}
				// 配置关闭工具调用过程显示时，跳过 tool_use 消息
				if !s.showToolProcess {
					continue
				}
				toolInput, _ := block["input"].(map[string]any)
				msgs = append(msgs, map[string]any{
					"type":            "tool_use",
					"tool_name":       toolName,
					"tool_input":      summarizeToolInput(toolInput),
					"tool_input_full": toolInput,
				})
			case "thinking":
				thinking, _ := block["thinking"].(string)
				msgs = append(msgs, map[string]any{
					"type":    "thinking",
					"content": thinking,
				})
			}
		}

	case "result":
		// 最终结果 + token 统计
		content := ""
		resultData := event["result"]
		switch v := resultData.(type) {
		case string:
			content = v
		case map[string]any:
			if t, ok := v["text"].(string); ok {
				content = t
			}
		}

		inputTokens, _ := event["input_tokens"].(float64)
		outputTokens, _ := event["output_tokens"].(float64)
		sessionID, _ := event["session_id"].(string)

		msgs = append(msgs, map[string]any{
			"type":          "result",
			"content":       content,
			"input_tokens":  int(inputTokens),
			"output_tokens": int(outputTokens),
			"session_id":    sessionID,
		})

		// 持久化 assistant 回复（result 事件包含最终完整回复）
		if s.chatStore != nil {
			slog.Info("webui: saving assistant result", "vibe_id", s.vibeID, "content_len", len(content), "session_id", sessionID)
			if content != "" {
				s.chatStore.SaveMessage(context.Background(), ChatMessage{
					SessionID: s.vibeID,
					Role:      "assistant",
					Content:   content,
					Platform:  "vibe",
					BizType:   "vibe",
				})
			}
			// 更新 agent_session_id
			if sessionID != "" {
				s.chatStore.EnsureSession(context.Background(), ChatSessionInfo{
					SessionID:      s.vibeID,
					SessionKey:     "vibe:" + s.workDir,
					Project:        s.workDir,
					AgentType:      "claudecode",
					AgentSessionID: sessionID,
					Name:           s.workDir,
					BizType:        "vibe",
				})
			}
		}

	case "control_request":
		// 权限请求 — 参考 IM 端 bypassPermissions 模式（agent/claudecode/session.go handleControlRequest），
		// WebUI 采用 auto 模式，所有工具调用自动批准，不弹出确认弹窗。
		// Claude Code CLI 格式: {"type":"control_request","request_id":"...","request":{"subtype":"can_use_tool","tool_name":"...","input":{...}}}
		request, _ := event["request"].(map[string]any)
		if request == nil {
			break
		}
		subtype, _ := request["subtype"].(string)
		if subtype != "can_use_tool" {
			slog.Debug("webui: unknown control request subtype", "subtype", subtype)
			break
		}
		toolInput, _ := request["input"].(map[string]any)
		requestID, _ := event["request_id"].(string)

		// 自动批准：缓存输入后立即调用 respondPermission 回传 allow，
		// 不再将 permission_request 转发到前端（与 IM 端 autoApprove 逻辑一致）
		if requestID != "" {
			s.mu.Lock()
			s.pendingInputs[requestID] = toolInput
			s.mu.Unlock()
			if err := s.respondPermission(requestID, "allow"); err != nil {
				slog.Warn("webui: auto-approve permission failed", "request_id", requestID, "err", err)
			}
		}

	case "control_cancel_request":
		// Claude Code 取消了之前的权限请求，通知前端移除悬挂的权限弹窗
		requestID, _ := event["request_id"].(string)
		slog.Debug("webui: permission cancelled", "request_id", requestID)
		// 清理缓存的工具输入
		if requestID != "" {
			s.mu.Lock()
			delete(s.pendingInputs, requestID)
			s.mu.Unlock()
		}
		msgs = append(msgs, map[string]any{
			"type":       "permission_cancelled",
			"request_id": requestID,
		})

	case "user":
		// 工具执行结果（user 事件中的 tool_result）
		messageData, _ := event["message"].(map[string]any)
		if messageData == nil {
			break
		}
		contentList, _ := messageData["content"].([]any)
		for _, item := range contentList {
			block, _ := item.(map[string]any)
			if block == nil {
				continue
			}
			blockType, _ := block["type"].(string)
			if blockType == "tool_result" {
				// 配置关闭工具调用过程显示时，跳过 tool_result 消息
				if !s.showToolProcess {
					continue
				}
				toolUseID, _ := block["tool_use_id"].(string)
				resultContent := extractToolResultContent(block)
				msgs = append(msgs, map[string]any{
					"type":      "tool_result",
					"tool_name": toolUseID,
					"content":   resultContent,
				})
			}
		}
	}

	return msgs
}

// alive returns true if the process is still running.
func (s *webuiSession) alive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.isAlive
}

// abort sends SIGINT to the Claude Code process.
func (s *webuiSession) abort() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Signal(os.Interrupt)
		slog.Info("webui: sent SIGINT to claude code process")
	}
}

// stop terminates the Claude Code process.
func (s *webuiSession) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.isAlive = false
	if s.cmd == nil || s.cmd.Process == nil {
		return
	}

	// 先尝试 terminate
	_ = s.cmd.Process.Signal(os.Interrupt)
	// 等待 3 秒
	done := make(chan error, 1)
	go func() { done <- s.cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = s.cmd.Process.Kill()
		<-done
	}
	slog.Info("webui: claude code process stopped")
}

// ---------------------------------------------------------------------------
// Helper functions
// ---------------------------------------------------------------------------

// summarizeToolInput converts tool input to a human-readable summary string.
func summarizeToolInput(input map[string]any) string {
	if input == nil {
		return ""
	}
	var parts []string
	if cmd, ok := input["command"].(string); ok {
		if len(cmd) > 200 {
			cmd = cmd[:200]
		}
		parts = append(parts, "命令: "+cmd)
	}
	if fp, ok := input["file_path"].(string); ok {
		parts = append(parts, "文件: "+fp)
	}
	if pattern, ok := input["pattern"].(string); ok {
		parts = append(parts, "模式: "+pattern)
	}
	if content, ok := input["content"].(string); ok {
		if len(content) > 100 {
			content = content[:100] + "..."
		}
		parts = append(parts, "内容: "+content)
	}
	if query, ok := input["query"].(string); ok {
		if len(query) > 200 {
			query = query[:200]
		}
		parts = append(parts, "查询: "+query)
	}
	if url, ok := input["url"].(string); ok {
		parts = append(parts, "URL: "+url)
	}
	if len(parts) > 0 {
		return strings.Join(parts, " | ")
	}
	// 回退：JSON 序列化
	b, err := json.Marshal(input)
	if err != nil {
		return fmt.Sprintf("%v", input)
	}
	s := string(b)
	if len(s) > 500 {
		s = s[:500]
	}
	return s
}

// extractToolResultContent extracts text content from a tool_result block.
func extractToolResultContent(block map[string]any) string {
	content := block["content"]
	switch v := content.(type) {
	case string:
		if len(v) > 2000 {
			return v[:2000]
		}
		return v
	case []any:
		var texts []string
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				if t, ok := m["type"].(string); ok && t == "text" {
					if text, ok := m["text"].(string); ok {
						texts = append(texts, text)
					}
				}
			}
		}
		result := strings.Join(texts, "\n")
		if len(result) > 2000 {
			result = result[:2000]
		}
		return result
	default:
		s := fmt.Sprintf("%v", content)
		if len(s) > 2000 {
			s = s[:2000]
		}
		return s
	}
}

// ---------------------------------------------------------------------------
// Vibe Coding 聊天历史 REST API
// ---------------------------------------------------------------------------

// handleVibeSessions 返回 Vibe Coding 的历史会话列表。
// GET /api/vibe/sessions?limit=50
func (s *WebUIServer) handleVibeSessions(w http.ResponseWriter, r *http.Request) {
	if !s.authenticate(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.chatStore == nil {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true,"data":{"sessions":[]}}`)) //nolint:errcheck
		return
	}

	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	// biz_type 过滤：默认为空（查所有类型，包括 vibe 和 im）
	bizType := r.URL.Query().Get("biz_type")

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	sessions, err := s.chatStore.ListSessions(ctx, bizType, limit)
	if err != nil {
		slog.Error("webui: list vibe sessions failed", "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()}) //nolint:errcheck
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"ok": true,
		"data": map[string]any{
			"sessions": sessions,
		},
	})
}

// handleVibeSessionMessages 返回指定 Vibe 会话的消息列表。
// GET /api/vibe/sessions/{session_id}/messages?limit=200
func (s *WebUIServer) handleVibeSessionMessages(w http.ResponseWriter, r *http.Request) {
	if !s.authenticate(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 解析 URL: /api/vibe/sessions/{session_id}/messages
	path := strings.TrimPrefix(r.URL.Path, "/api/vibe/sessions/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 2 || parts[0] == "" || parts[1] != "messages" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	sessionID := parts[0]

	if s.chatStore == nil {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true,"data":{"messages":[]}}`)) //nolint:errcheck
		return
	}

	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	messages, err := s.chatStore.GetMessages(ctx, sessionID, limit)
	if err != nil {
		slog.Error("webui: get vibe messages failed", "error", err, "session_id", sessionID)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()}) //nolint:errcheck
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"ok": true,
		"data": map[string]any{
			"messages": messages,
		},
	})
}

// handleVibePrompts 返回配置中的常用提示词列表。
// GET /api/vibe/prompts
func (s *WebUIServer) handleVibePrompts(w http.ResponseWriter, r *http.Request) {
	if !s.authenticate(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	prompts := s.prompts
	if prompts == nil {
		prompts = []WebUIPrompt{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"ok": true,
		"data": map[string]any{
			"prompts": prompts,
		},
	})
}

// ---------------------------------------------------------------------------
// Vibe Coding 导出 Markdown
// ---------------------------------------------------------------------------

// ExportRequest 是导出 Markdown 的请求体。
type ExportRequest struct {
	SessionName string          `json:"session_name"`
	Project     string          `json:"project"`
	AgentType   string          `json:"agent_type"`
	SessionID   string          `json:"session_id"`
	Messages    []ExportMessage `json:"messages"`
}

// ExportMessage 是导出请求中的单条消息。
type ExportMessage struct {
	Role      string `json:"role"`                // "user" or "assistant"
	Type      string `json:"type"`                // "text", "tool_use", "result", "tool_result", "error"
	Content   string `json:"content"`
	ToolName  string `json:"tool_name,omitempty"` // 仅 type="tool_use" 时有值
	Timestamp int64  `json:"timestamp"`           // 毫秒级 Unix 时间戳
}

// sanitizeFilename 清洗文件名，只保留安全字符。
var filenameRe = regexp.MustCompile(`[^a-zA-Z0-9_\-]`)

func sanitizeFilename(name string) string {
	s := filenameRe.ReplaceAllString(name, "_")
	// 去除首尾下划线
	s = strings.Trim(s, "_")
	if s == "" {
		s = "export"
	}
	return s
}

// handleVibeExportMarkdown 将聊天消息导出为 Markdown 文件。
// POST /api/vibe/export
func (s *WebUIServer) handleVibeExportMarkdown(w http.ResponseWriter, r *http.Request) {
	if !s.authenticate(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 限制请求体大小为 20MB
	r.Body = http.MaxBytesReader(w, r.Body, 20<<20)

	var req ExportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if len(req.Messages) == 0 {
		http.Error(w, "no messages to export", http.StatusBadRequest)
		return
	}

	// 生成 Markdown 内容
	now := time.Now()
	md := s.buildExportMarkdown(&req, now)

	// 生成文件名：{项目名}_{YYYYMMDD}_{HHmmss}.md
	projectName := req.SessionName
	if projectName == "" && req.Project != "" {
		parts := strings.Split(strings.TrimRight(req.Project, "/"), "/")
		projectName = parts[len(parts)-1]
	}
	if projectName == "" {
		projectName = "chat"
	}
	safeName := sanitizeFilename(projectName)
	filename := fmt.Sprintf("%s_%s.md", safeName, now.Format("20060102_150405"))

	// 设置响应头
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	// ASCII fallback + RFC 5987 UTF-8 编码
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, filename, url.PathEscape(filename)))
	w.Write([]byte(md)) //nolint:errcheck
}

// buildExportMarkdown 根据请求数据构建 Markdown 字符串。
func (s *WebUIServer) buildExportMarkdown(req *ExportRequest, exportTime time.Time) string {
	var b strings.Builder

	// 从路径中提取项目显示名
	displayName := req.SessionName
	if displayName == "" && req.Project != "" {
		parts := strings.Split(strings.TrimRight(req.Project, "/"), "/")
		displayName = parts[len(parts)-1]
	}
	if displayName == "" {
		displayName = "Chat Export"
	}

	// 标题
	b.WriteString(fmt.Sprintf("# Chat Export: %s\n\n", displayName))

	// 元信息表格
	zone, _ := exportTime.Zone()
	b.WriteString("| Field | Value |\n")
	b.WriteString("|-------|-------|\n")
	if req.Project != "" {
		b.WriteString(fmt.Sprintf("| Project | %s |\n", req.Project))
	}
	if req.AgentType != "" {
		b.WriteString(fmt.Sprintf("| Agent | %s |\n", req.AgentType))
	}
	if req.SessionID != "" {
		b.WriteString(fmt.Sprintf("| Session ID | %s |\n", req.SessionID))
	}
	// 统计实际导出的消息数（排除跳过的类型）
	exportedCount := 0
	for _, msg := range req.Messages {
		switch msg.Type {
		case "thinking", "permission_request", "system":
			// 跳过的类型不计数
		default:
			exportedCount++
		}
	}
	b.WriteString(fmt.Sprintf("| Messages | %d |\n", exportedCount))
	b.WriteString(fmt.Sprintf("| Exported At | %s (%s) |\n", exportTime.Format("2006-01-02 15:04:05"), zone))
	b.WriteString("\n---\n\n")

	// 逐条消息转换为 Markdown
	for _, msg := range req.Messages {
		mdBlock := s.renderMessageMarkdown(&msg)
		if mdBlock != "" {
			b.WriteString(mdBlock)
			b.WriteString("\n---\n\n")
		}
	}

	return b.String()
}

// renderMessageMarkdown 将单条消息渲染为 Markdown 块。
func (s *WebUIServer) renderMessageMarkdown(msg *ExportMessage) string {
	// 格式化时间
	timeStr := ""
	if msg.Timestamp > 0 {
		t := time.UnixMilli(msg.Timestamp)
		timeStr = t.Format("15:04:05")
	}

	// 构建标题中的角色名
	role := strings.Title(msg.Role) //nolint:staticcheck

	switch msg.Type {
	case "text":
		// 普通文本消息
		if timeStr != "" {
			return fmt.Sprintf("## %s (%s)\n\n%s\n", role, timeStr, msg.Content)
		}
		return fmt.Sprintf("## %s\n\n%s\n", role, msg.Content)

	case "tool_use":
		// 工具调用，以引用块格式展示
		toolLabel := msg.ToolName
		if toolLabel == "" {
			toolLabel = "Tool"
		}
		header := fmt.Sprintf("## %s - Tool: %s", role, toolLabel)
		if timeStr != "" {
			header += fmt.Sprintf(" (%s)", timeStr)
		}
		// 将内容转为引用块
		quotedContent := ""
		if msg.Content != "" {
			lines := strings.Split(msg.Content, "\n")
			for _, line := range lines {
				quotedContent += "> " + line + "\n"
			}
		}
		return header + "\n\n" + quotedContent

	case "tool_result":
		// 工具执行结果，以代码块展示
		header := fmt.Sprintf("## %s - Tool Result", role)
		if timeStr != "" {
			header += fmt.Sprintf(" (%s)", timeStr)
		}
		return header + "\n\n```\n" + msg.Content + "\n```\n"

	case "result":
		// 最终回复结果，当作普通文本处理
		if timeStr != "" {
			return fmt.Sprintf("## %s (%s)\n\n%s\n", role, timeStr, msg.Content)
		}
		return fmt.Sprintf("## %s\n\n%s\n", role, msg.Content)

	case "error":
		// 错误消息，以警告引用块展示
		header := fmt.Sprintf("## %s - Error", role)
		if timeStr != "" {
			header += fmt.Sprintf(" (%s)", timeStr)
		}
		return header + "\n\n> Error: " + msg.Content + "\n"

	case "thinking", "permission_request", "system":
		// 跳过不导出
		return ""

	default:
		// 未知类型按文本处理
		if msg.Content != "" {
			if timeStr != "" {
				return fmt.Sprintf("## %s (%s)\n\n%s\n", role, timeStr, msg.Content)
			}
			return fmt.Sprintf("## %s\n\n%s\n", role, msg.Content)
		}
		return ""
	}
}
