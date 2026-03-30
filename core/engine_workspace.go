package core

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// buildSenderPrompt prepends a sender identity header to content when
// injectSender is enabled and userID is non-empty.
func (e *Engine) buildSenderPrompt(content, userID, platform, sessionKey string) string {
	if !e.injectSender || userID == "" {
		return content
	}
	chatID := extractChannelID(sessionKey)
	return fmt.Sprintf("[cc-connect sender_id=%s platform=%s chat_id=%s]\n%s", userID, platform, chatID, content)
}

func extractChannelID(sessionKey string) string {
	// Format: "platform:channelID:userID" or "platform:channelID"
	parts := strings.SplitN(sessionKey, ":", 3)
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}

func extractUserID(sessionKey string) string {
	parts := strings.SplitN(sessionKey, ":", 3)
	if len(parts) >= 3 {
		return parts[2]
	}
	return ""
}

func extractPlatformName(sessionKey string) string {
	if i := strings.IndexByte(sessionKey, ':'); i >= 0 {
		return sessionKey[:i]
	}
	return sessionKey
}

func workspaceChannelKey(platformName, channelID string) string {
	if channelID == "" {
		return ""
	}
	if platformName == "" {
		return channelID
	}
	return platformName + ":" + channelID
}

func extractWorkspaceChannelKey(sessionKey string) string {
	return workspaceChannelKey(extractPlatformName(sessionKey), extractChannelID(sessionKey))
}

// commandContext resolves the appropriate agent, session manager, and interactive key
// for a command. In multi-workspace mode, it routes to the bound workspace if present.
func (e *Engine) commandContext(p Platform, msg *Message) (Agent, *SessionManager, string, error) {
	if !e.multiWorkspace {
		return e.agent, e.sessions, msg.SessionKey, nil
	}
	channelID := extractChannelID(msg.SessionKey)
	channelKey := extractWorkspaceChannelKey(msg.SessionKey)
	if channelKey == "" || channelID == "" {
		return e.agent, e.sessions, msg.SessionKey, nil
	}
	workspace, _, err := e.resolveWorkspace(p, channelID)
	if err != nil {
		return nil, nil, "", err
	}
	if workspace == "" {
		return e.agent, e.sessions, msg.SessionKey, nil
	}
	wsAgent, wsSessions, err := e.getOrCreateWorkspaceAgent(workspace)
	if err != nil {
		return nil, nil, "", err
	}
	return wsAgent, wsSessions, workspace + ":" + msg.SessionKey, nil
}

// sessionContextForKey resolves the agent and session manager for a sessionKey.
// It uses existing workspace bindings and falls back to global context if unresolved.
func (e *Engine) sessionContextForKey(sessionKey string) (Agent, *SessionManager) {
	if !e.multiWorkspace || e.workspaceBindings == nil {
		return e.agent, e.sessions
	}
	channelKey := extractWorkspaceChannelKey(sessionKey)
	if channelKey == "" {
		return e.agent, e.sessions
	}
	if b, _, usable := e.lookupEffectiveWorkspaceBinding(channelKey); usable {
		if wsAgent, wsSessions, err := e.getOrCreateWorkspaceAgent(normalizeWorkspacePath(b.Workspace)); err == nil {
			return wsAgent, wsSessions
		}
	}
	return e.agent, e.sessions
}

// interactiveKeyForSessionKey returns the interactive state key for a sessionKey.
// In multi-workspace mode, it prefixes with the bound workspace path when available.
func (e *Engine) interactiveKeyForSessionKey(sessionKey string) string {
	if !e.multiWorkspace || e.workspaceBindings == nil {
		return sessionKey
	}
	channelKey := extractWorkspaceChannelKey(sessionKey)
	if channelKey == "" {
		return sessionKey
	}
	if b, _, usable := e.lookupEffectiveWorkspaceBinding(channelKey); usable {
		return normalizeWorkspacePath(b.Workspace) + ":" + sessionKey
	}
	return sessionKey
}

// lookupEffectiveWorkspaceBinding returns the effective binding for a channel
// plus whether the bound workspace is currently usable.
func (e *Engine) lookupEffectiveWorkspaceBinding(channelKey string) (*WorkspaceBinding, string, bool) {
	if !e.multiWorkspace || e.workspaceBindings == nil || channelKey == "" {
		return nil, "", false
	}

	projectKey := "project:" + e.name
	b, bindingKey := e.workspaceBindings.LookupEffective(projectKey, channelKey)
	if b == nil {
		return nil, "", false
	}

	if _, err := os.Stat(b.Workspace); err != nil {
		slog.Warn("bound workspace directory missing",
			"workspace", b.Workspace, "channel_key", channelKey, "binding_scope", bindingKey)
		if bindingKey != sharedWorkspaceBindingsKey {
			e.workspaceBindings.Unbind(bindingKey, channelKey)
		}
		return b, bindingKey, false
	}

	return b, bindingKey, true
}

// resolveWorkspace resolves a channel to a workspace directory.
// Returns (workspacePath, channelName, error).
// If workspacePath is empty, the init flow should be triggered.
func (e *Engine) resolveWorkspace(p Platform, channelID string) (string, string, error) {
	channelKey := workspaceChannelKey(p.Name(), channelID)

	// Step 1: Check existing binding
	if b, _, usable := e.lookupEffectiveWorkspaceBinding(channelKey); b != nil {
		if !usable {
			return "", b.ChannelName, nil
		}
		return normalizeWorkspacePath(b.Workspace), b.ChannelName, nil
	}

	// Step 2: Resolve channel name for convention match
	channelName := ""
	if resolver, ok := p.(ChannelNameResolver); ok {
		name, err := resolver.ResolveChannelName(channelID)
		if err != nil {
			slog.Warn("failed to resolve channel name", "channel", channelID, "err", err)
		} else {
			channelName = name
		}
	}

	if channelName == "" {
		return "", "", nil
	}

	// Step 3: Convention match — check if base_dir/<channel-name> exists
	candidate := filepath.Join(e.baseDir, channelName)
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		// Auto-bind
		projectKey := "project:" + e.name
		normalized := normalizeWorkspacePath(candidate)
		e.workspaceBindings.Bind(projectKey, channelKey, channelName, normalized)
		slog.Info("workspace auto-bound by convention",
			"channel", channelName, "workspace", normalized)
		return normalized, channelName, nil
	}

	return "", channelName, nil
}

// handleWorkspaceInitFlow manages the conversational workspace setup.
// Returns true if the message was consumed by the init flow.
func (e *Engine) handleWorkspaceInitFlow(p Platform, msg *Message, channelName string) bool {
	channelKey := extractWorkspaceChannelKey(msg.SessionKey)

	e.initFlowsMu.Lock()
	flow, exists := e.initFlows[channelKey]
	e.initFlowsMu.Unlock()

	content := strings.TrimSpace(msg.Content)

	if !exists {
		if strings.HasPrefix(content, "/") {
			return false
		}
		e.initFlowsMu.Lock()
		e.initFlows[channelKey] = &workspaceInitFlow{
			state:       "awaiting_url",
			channelName: channelName,
		}
		e.initFlowsMu.Unlock()
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsNotFoundHint))
		return true
	}

	// Slash commands always take priority over the init flow — let them
	// pass through to handleCommand. Clean up the stale flow since the
	// user is issuing explicit commands instead of following the clone guide.
	if strings.HasPrefix(content, "/") {
		e.initFlowsMu.Lock()
		delete(e.initFlows, channelKey)
		e.initFlowsMu.Unlock()
		return false
	}

	switch flow.state {
	case "awaiting_url":
		if !looksLikeGitURL(content) {
			e.reply(p, msg.ReplyCtx, "That doesn't look like a git URL. Please provide a URL like `https://github.com/org/repo` or `git@github.com:org/repo.git`.")
			return true
		}
		repoName := extractRepoName(content)
		cloneTo := filepath.Join(e.baseDir, repoName)

		e.initFlowsMu.Lock()
		flow.repoURL = content
		flow.cloneTo = cloneTo
		flow.state = "awaiting_confirm"
		e.initFlowsMu.Unlock()

		e.reply(p, msg.ReplyCtx, fmt.Sprintf(
			"I'll clone `%s` to `%s` and bind it to this channel. OK? (yes/no)", content, cloneTo))
		return true

	case "awaiting_confirm":
		lower := strings.ToLower(content)
		if lower != "yes" && lower != "y" {
			e.initFlowsMu.Lock()
			delete(e.initFlows, channelKey)
			e.initFlowsMu.Unlock()
			e.reply(p, msg.ReplyCtx, "Cancelled. Send a repo URL anytime to try again.")
			return true
		}

		e.reply(p, msg.ReplyCtx, fmt.Sprintf("Cloning `%s` to `%s`...", flow.repoURL, flow.cloneTo))

		if err := gitClone(flow.repoURL, flow.cloneTo); err != nil {
			e.initFlowsMu.Lock()
			delete(e.initFlows, channelKey)
			e.initFlowsMu.Unlock()
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("Clone failed: %v\nSend a repo URL to try again.", err))
			return true
		}

		projectKey := "project:" + e.name
		e.workspaceBindings.Bind(projectKey, channelKey, flow.channelName, normalizeWorkspacePath(flow.cloneTo))

		e.initFlowsMu.Lock()
		delete(e.initFlows, channelKey)
		e.initFlowsMu.Unlock()

		e.reply(p, msg.ReplyCtx, fmt.Sprintf(
			"Clone complete. Bound workspace `%s` to this channel. Ready.", flow.cloneTo))
		return true
	}

	return false
}

func looksLikeGitURL(s string) bool {
	return strings.HasPrefix(s, "https://") ||
		strings.HasPrefix(s, "http://") ||
		strings.HasPrefix(s, "git@") ||
		strings.HasPrefix(s, "ssh://")
}

func extractRepoName(url string) string {
	url = strings.TrimSuffix(url, ".git")
	// Handle git@host:org/repo format
	if idx := strings.LastIndex(url, ":"); idx != -1 && strings.HasPrefix(url, "git@") {
		remainder := url[idx+1:]
		parts := strings.Split(remainder, "/")
		if len(parts) > 0 {
			return parts[len(parts)-1]
		}
	}
	// Handle https://host/org/repo format
	parts := strings.Split(url, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return "workspace"
}

func gitClone(repoURL, dest string) error {
	cmd := exec.Command("git", "clone", repoURL, dest)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}
