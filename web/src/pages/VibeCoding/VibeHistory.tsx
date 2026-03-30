import { useState, useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { History, X, MessageSquare, Clock, ArrowRight, Download, Loader2 } from 'lucide-react';
import { cn } from '@/lib/utils';
import { listVibeSessions, getVibeMessages, exportVibeSession, type VibeSessionRecord } from '@/api/vibe';

interface VibeHistoryProps {
  open: boolean;
  onClose: () => void;
  onLoadSession: (session: VibeSessionRecord) => void;
}

// Vibe Coding 历史会话侧边面板
export default function VibeHistory({ open, onClose, onLoadSession }: VibeHistoryProps) {
  const { t } = useTranslation();
  const [sessions, setSessions] = useState<VibeSessionRecord[]>([]);
  const [loading, setLoading] = useState(false);
  // 正在导出的会话 ID
  const [exportingId, setExportingId] = useState<string | null>(null);

  // 导出某个历史会话为 Markdown
  const handleExportSession = async (e: React.MouseEvent, session: VibeSessionRecord) => {
    e.stopPropagation(); // 阻止触发 onLoadSession
    if (session.message_count === 0) return;

    setExportingId(session.session_id);
    try {
      // 获取会话消息
      const data = await getVibeMessages(session.session_id, 500);
      const messages = (data.messages || []).map((msg) => ({
        role: msg.role,
        type: 'text' as const,
        content: msg.content,
        timestamp: new Date(msg.created_at).getTime(),
      }));

      // 提取项目名
      const pathParts = (session.name || session.project || '').replace(/\/+$/, '').split('/');
      const sessionName = pathParts[pathParts.length - 1] || 'chat';

      await exportVibeSession({
        session_name: sessionName,
        project: session.project || session.name || '',
        agent_type: session.agent_type || 'claudecode',
        session_id: session.session_id,
        messages,
      });
    } catch (err) {
      console.error('Export failed:', err);
      alert(t('vibe.exportFailed'));
    } finally {
      setExportingId(null);
    }
  };

  // 打开面板时加载历史会话列表
  useEffect(() => {
    if (!open) return;
    setLoading(true);
    listVibeSessions(50)
      .then((data) => {
        setSessions(data.sessions || []);
      })
      .catch((err) => {
        console.error('Failed to load vibe history:', err);
        setSessions([]);
      })
      .finally(() => setLoading(false));
  }, [open]);

  // 格式化时间为相对时间
  const formatTime = (dateStr: string) => {
    const date = new Date(dateStr);
    const now = new Date();
    const diff = now.getTime() - date.getTime();
    const mins = Math.floor(diff / 60000);
    if (mins < 1) return t('vibe.justNow');
    if (mins < 60) return `${mins}m`;
    const hours = Math.floor(mins / 60);
    if (hours < 24) return `${hours}h`;
    const days = Math.floor(hours / 24);
    if (days < 30) return `${days}d`;
    return date.toLocaleDateString();
  };

  // 从 work_dir 路径中提取项目名
  const projectName = (session: VibeSessionRecord) => {
    const name = session.name || session.project || '';
    // 取路径最后一段作为显示名
    const parts = name.replace(/\/+$/, '').split('/');
    return parts[parts.length - 1] || name;
  };

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-40 flex">
      {/* 遮罩 */}
      <div className="absolute inset-0 bg-black/30" onClick={onClose} />

      {/* 侧边面板 */}
      <div className="relative ml-auto w-80 max-w-[85vw] bg-white dark:bg-gray-900 border-l border-gray-200 dark:border-gray-800 shadow-xl flex flex-col animate-slide-in-right">
        {/* 头部 */}
        <div className="flex items-center justify-between px-4 py-3 border-b border-gray-200 dark:border-gray-800">
          <div className="flex items-center gap-2 text-sm font-medium text-gray-700 dark:text-gray-300">
            <History size={16} />
            {t('vibe.history')}
          </div>
          <button
            onClick={onClose}
            className="p-1 rounded hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors"
          >
            <X size={16} className="text-gray-500" />
          </button>
        </div>

        {/* 会话列表 */}
        <div className="flex-1 overflow-y-auto">
          {loading && (
            <div className="flex items-center justify-center py-8 text-gray-400 text-sm">
              {t('vibe.loading')}
            </div>
          )}

          {!loading && sessions.length === 0 && (
            <div className="flex flex-col items-center justify-center py-12 text-gray-400 text-sm gap-2">
              <MessageSquare size={24} className="opacity-50" />
              {t('vibe.noHistory')}
            </div>
          )}

          {!loading &&
            sessions.map((session) => (
              <button
                key={session.session_id}
                onClick={() => onLoadSession(session)}
                className="w-full text-left px-4 py-3 border-b border-gray-100 dark:border-gray-800 hover:bg-gray-50 dark:hover:bg-gray-800/50 transition-colors group"
              >
                <div className="flex items-center justify-between mb-1">
                  <span className="text-xs font-medium text-gray-700 dark:text-gray-300 truncate max-w-[180px]">
                    {projectName(session)}
                  </span>
                  <span className="flex items-center gap-1 text-[10px] text-gray-400">
                    <Clock size={10} />
                    {formatTime(session.updated_at)}
                  </span>
                </div>
                <div className="flex items-center justify-between">
                  <p className="text-xs text-gray-500 dark:text-gray-400 truncate max-w-[220px]">
                    {session.last_message || t('vibe.emptySession')}
                  </p>
                  <ArrowRight
                    size={12}
                    className="text-gray-400 opacity-0 group-hover:opacity-100 transition-opacity shrink-0"
                  />
                </div>
                <div className="flex items-center gap-2 mt-1">
                  <span className="text-[10px] text-gray-400 bg-gray-100 dark:bg-gray-800 px-1.5 py-0.5 rounded">
                    {session.message_count} {t('vibe.messages')}
                  </span>
                  <span
                    className={cn(
                      'text-[10px] px-1.5 py-0.5 rounded',
                      session.biz_type === 'vibe'
                        ? 'text-emerald-600 dark:text-emerald-400 bg-emerald-50 dark:bg-emerald-900/20'
                        : 'text-blue-600 dark:text-blue-400 bg-blue-50 dark:bg-blue-900/20'
                    )}
                  >
                    {session.biz_type === 'vibe' ? 'Vibe' : 'IM'}
                  </span>
                  {/* 导出 Markdown 按钮 */}
                  {session.message_count > 0 && (
                    <button
                      onClick={(e) => handleExportSession(e, session)}
                      disabled={exportingId === session.session_id}
                      className="ml-auto p-0.5 rounded opacity-0 group-hover:opacity-100 hover:bg-gray-200 dark:hover:bg-gray-700 transition-all disabled:opacity-50"
                      title={t('vibe.exportMarkdown')}
                    >
                      {exportingId === session.session_id ? (
                        <Loader2 size={12} className="text-gray-400 animate-spin" />
                      ) : (
                        <Download size={12} className="text-gray-400" />
                      )}
                    </button>
                  )}
                </div>
              </button>
            ))}
        </div>
      </div>
    </div>
  );
}
