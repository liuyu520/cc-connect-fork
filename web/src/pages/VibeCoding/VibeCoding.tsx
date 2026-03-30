import { useState, useCallback, useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { History, MessageSquareText, Download, Loader2 } from 'lucide-react';
import TabBar from './TabBar';
import VibeSession from './VibeSession';
import VibeHistory from './VibeHistory';
import { createTabState } from './types';
import { getVibeMessages, listVibePrompts, exportVibeSession, type VibeSessionRecord, type VibePrompt, type ExportMessage } from '@/api/vibe';
import type { TabState, ChatMessage } from './types';

// ── 多 Tab Vibe Coding 容器 ───────────────────────────────────

export default function VibeCoding() {
  const { t } = useTranslation();

  // Tab 管理状态
  const [tabs, setTabs] = useState<TabState[]>(() => [createTabState()]);
  const [activeTabId, setActiveTabId] = useState(() => tabs[0].id);

  // 关闭确认弹窗状态
  const [closingTabId, setClosingTabId] = useState<string | null>(null);

  // 历史面板状态
  const [historyOpen, setHistoryOpen] = useState(false);

  // 常用提示词状态
  const [prompts, setPrompts] = useState<VibePrompt[]>([]);
  const [promptsOpen, setPromptsOpen] = useState(false);

  // 导出 Markdown 状态
  const [exporting, setExporting] = useState(false);

  // 页面加载时获取常用提示词
  useEffect(() => {
    listVibePrompts()
      .then((data) => setPrompts(data.prompts || []))
      .catch(() => {}); // 获取失败静默处理
  }, []);

  // ── Tab 操作回调 ──────────────────────────────────

  // 新建 Tab
  const handleNewTab = useCallback(() => {
    const newTab = createTabState();
    setTabs((prev) => [...prev, newTab]);
    setActiveTabId(newTab.id);
  }, []);

  // 选择 Tab
  const handleSelectTab = useCallback((tabId: string) => {
    setActiveTabId(tabId);
  }, []);

  // 关闭 Tab（带确认逻辑）
  const handleCloseTab = useCallback(
    (tabId: string) => {
      const tab = tabs.find((t) => t.id === tabId);
      if (!tab) return;

      // 如果会话活跃，显示确认弹窗
      if (tab.processAlive) {
        setClosingTabId(tabId);
        return;
      }

      // 直接移除 Tab
      removeTab(tabId);
    },
    [tabs] // eslint-disable-line react-hooks/exhaustive-deps
  );

  // 确认关闭 Tab
  const confirmCloseTab = useCallback(() => {
    if (closingTabId) {
      removeTab(closingTabId);
      setClosingTabId(null);
    }
  }, [closingTabId]); // eslint-disable-line react-hooks/exhaustive-deps

  // 取消关闭
  const cancelCloseTab = useCallback(() => {
    setClosingTabId(null);
  }, []);

  // 移除 Tab 并切换活跃 Tab
  const removeTab = useCallback(
    (tabId: string) => {
      setTabs((prev) => {
        const next = prev.filter((t) => t.id !== tabId);
        // 如果关闭的是当前活跃 Tab，切换到最后一个
        if (activeTabId === tabId && next.length > 0) {
          setActiveTabId(next[next.length - 1].id);
        }
        return next;
      });
    },
    [activeTabId]
  );

  // 更新指定 Tab 的状态（供 VibeSession 回调使用）
  const handleUpdateTab = useCallback((tabId: string, updates: Partial<TabState>) => {
    setTabs((prev) =>
      prev.map((tab) => {
        if (tab.id !== tabId) return tab;
        return { ...tab, ...updates };
      })
    );
  }, []);

  // ── 历史加载回调 ──────────────────────────────────

  // 从历史面板加载某个会话到新 Tab
  const handleLoadHistorySession = useCallback(
    async (session: VibeSessionRecord) => {
      setHistoryOpen(false);

      try {
        // 从后端加载消息
        const data = await getVibeMessages(session.session_id, 500);
        const historyMessages: ChatMessage[] = (data.messages || []).map((msg, idx) => ({
          id: idx + 1,
          role: msg.role as 'user' | 'assistant',
          type: 'text' as const,
          content: msg.content,
          timestamp: new Date(msg.created_at).getTime(),
        }));

        // 从路径中提取项目名
        const pathParts = (session.name || session.project || '').replace(/\/+$/, '').split('/');
        const label = pathParts[pathParts.length - 1] || session.name;

        // 创建新 Tab 并加载历史消息
        const newTab = createTabState();
        newTab.label = label;
        newTab.workDir = session.project || session.name || '';
        newTab.messages = historyMessages;
        // 存储历史 session ID，用于后续"继续对话"功能
        newTab.sessionId = session.agent_session_id || '';

        setTabs((prev) => [...prev, newTab]);
        setActiveTabId(newTab.id);
      } catch (err) {
        console.error('Failed to load history session:', err);
      }
    },
    []
  );

  // 选择提示词 → 填入当前活跃 Tab 的输入框
  const handleSelectPrompt = useCallback(
    (prompt: VibePrompt) => {
      setPromptsOpen(false);
      handleUpdateTab(activeTabId, { userInput: prompt.content });
    },
    [activeTabId, handleUpdateTab]
  );

  // 将前端 ChatMessage 转换为导出用的 ExportMessage
  const chatToExportMessages = (messages: ChatMessage[]): ExportMessage[] => {
    return messages
      .filter((m) => !['thinking', 'permission_request', 'system'].includes(m.type))
      .map((m) => ({
        role: m.role,
        type: m.type,
        content: m.content,
        tool_name: m.toolName,
        timestamp: m.timestamp,
      }));
  };

  // 导出当前活跃 Tab 的聊天记录为 Markdown
  const handleExportCurrentTab = useCallback(async () => {
    const tab = tabs.find((t) => t.id === activeTabId);
    if (!tab || tab.messages.length === 0) return;

    setExporting(true);
    try {
      // 从路径中提取项目名
      const pathParts = (tab.workDir || '').replace(/\/+$/, '').split('/');
      const sessionName = pathParts[pathParts.length - 1] || tab.label || 'chat';

      await exportVibeSession({
        session_name: sessionName,
        project: tab.workDir || '',
        agent_type: 'claudecode',
        session_id: tab.sessionId || '',
        messages: chatToExportMessages(tab.messages),
      });
    } catch (err) {
      console.error('Export failed:', err);
      alert(t('vibe.exportFailed'));
    } finally {
      setExporting(false);
    }
  }, [tabs, activeTabId, t]);

  // ── Render ─────────────────────────────────────────

  return (
    <div className="flex flex-col h-[calc(100vh-8rem)] animate-fade-in">
      {/* Tab 栏 + 历史按钮 */}
      <div className="mb-3 flex items-center gap-2">
        <div className="flex-1 min-w-0">
          <TabBar
            tabs={tabs}
            activeTabId={activeTabId}
            onSelectTab={handleSelectTab}
            onCloseTab={handleCloseTab}
            onNewTab={handleNewTab}
          />
        </div>
        {/* 历史记录按钮 */}
        <button
          onClick={() => setHistoryOpen(true)}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium text-gray-500 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors shrink-0"
          title={t('vibe.history')}
        >
          <History size={14} />
          <span className="hidden sm:inline">{t('vibe.history')}</span>
        </button>
        {/* 导出 Markdown 按钮 */}
        <button
          onClick={handleExportCurrentTab}
          disabled={exporting || !(tabs.find((t) => t.id === activeTabId)?.messages?.length)}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium text-gray-500 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors shrink-0 disabled:opacity-40 disabled:cursor-not-allowed"
          title={t('vibe.exportMarkdown')}
        >
          {exporting ? <Loader2 size={14} className="animate-spin" /> : <Download size={14} />}
          <span className="hidden sm:inline">{t('vibe.exportMarkdown')}</span>
        </button>
        {/* 常用提示词按钮 */}
        <button
          onClick={() => setPromptsOpen(true)}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium text-gray-500 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors shrink-0"
          title={t('vibe.prompts')}
        >
          <MessageSquareText size={14} />
          <span className="hidden sm:inline">{t('vibe.prompts')}</span>
        </button>
      </div>

      {/* 所有 Tab 的 VibeSession（隐藏非活跃的，保持 WebSocket 连接） */}
      <div className="flex-1 min-h-0">
        {tabs.map((tab) => (
          <VibeSession
            key={tab.id}
            tab={tab}
            onUpdateTab={handleUpdateTab}
            visible={tab.id === activeTabId}
          />
        ))}
      </div>

      {/* 历史会话侧边面板 */}
      <VibeHistory
        open={historyOpen}
        onClose={() => setHistoryOpen(false)}
        onLoadSession={handleLoadHistorySession}
      />

      {/* 常用提示词弹窗 — 复用关闭确认弹窗的样式模式 */}
      {promptsOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40" onClick={() => setPromptsOpen(false)}>
          <div
            className="bg-white dark:bg-gray-900 rounded-xl shadow-2xl p-6 max-w-md w-full mx-4 border border-gray-200 dark:border-gray-700 max-h-[70vh] flex flex-col"
            onClick={(e) => e.stopPropagation()}
          >
            <h3 className="text-sm font-semibold text-gray-800 dark:text-gray-200 mb-4 flex items-center gap-2">
              <MessageSquareText size={16} />
              {t('vibe.prompts')}
            </h3>
            {prompts.length === 0 ? (
              <p className="text-xs text-gray-400 dark:text-gray-500 py-4 text-center">
                {t('vibe.noPrompts')}
              </p>
            ) : (
              <div className="overflow-y-auto space-y-2 flex-1">
                {prompts.map((p, idx) => (
                  <button
                    key={idx}
                    onClick={() => handleSelectPrompt(p)}
                    className="w-full text-left px-4 py-3 rounded-lg border border-gray-200 dark:border-gray-700 hover:bg-gray-50 dark:hover:bg-gray-800 hover:border-accent/50 transition-colors group"
                  >
                    <div className="text-sm font-medium text-gray-800 dark:text-gray-200 group-hover:text-accent transition-colors">
                      {p.title}
                    </div>
                    <div className="text-xs text-gray-400 dark:text-gray-500 mt-1 line-clamp-2">
                      {p.content}
                    </div>
                  </button>
                ))}
              </div>
            )}
            <div className="flex justify-end mt-4 pt-3 border-t border-gray-200 dark:border-gray-700">
              <button
                onClick={() => setPromptsOpen(false)}
                className="px-4 py-2 text-xs font-medium rounded-lg bg-gray-100 dark:bg-gray-800 text-gray-600 dark:text-gray-400 hover:bg-gray-200 dark:hover:bg-gray-700 transition-colors"
              >
                {t('vibe.cancel')}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* 关闭确认弹窗 */}
      {closingTabId && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
          <div className="bg-white dark:bg-gray-900 rounded-xl shadow-2xl p-6 max-w-sm mx-4 border border-gray-200 dark:border-gray-700">
            <p className="text-sm text-gray-700 dark:text-gray-300 mb-4">
              {t('vibe.closeTabConfirm')}
            </p>
            <div className="flex justify-end gap-3">
              <button
                onClick={cancelCloseTab}
                className="px-4 py-2 text-xs font-medium rounded-lg bg-gray-100 dark:bg-gray-800 text-gray-600 dark:text-gray-400 hover:bg-gray-200 dark:hover:bg-gray-700 transition-colors"
              >
                {t('vibe.cancel')}
              </button>
              <button
                onClick={confirmCloseTab}
                className="px-4 py-2 text-xs font-medium rounded-lg bg-red-500 text-white hover:bg-red-600 transition-colors"
              >
                {t('vibe.confirm')}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
