import { useState, useRef, useEffect, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Play,
  Square,
  Send,
  Wrench,
  Brain,
  ShieldCheck,
  ShieldX,
  ChevronDown,
  ChevronRight,
  AlertCircle,
  Circle,
  Plus,
  XCircle,
  Copy,
  Check,
  ChevronsUpDown,
  Paperclip,
  X,
  FileText,
} from 'lucide-react';
import { cn } from '@/lib/utils';
import { useAuthStore } from '@/store/auth';
import { listProjects, type ProjectSummary } from '@/api/projects';
import VibeMarkdown from './VibeMarkdown';
import type { ChatMessage, TabState, AttachmentItem } from './types';

interface VibeSessionProps {
  tab: TabState;
  // 更新 Tab 状态的回调，由父组件（VibeCoding）传入
  onUpdateTab: (tabId: string, updates: Partial<TabState>) => void;
  // 是否可见（活跃 Tab）
  visible: boolean;
}

// ── 附件相关工具函数 ─────────────────────────────────
const MAX_FILE_SIZE = 10 * 1024 * 1024; // 10MB

// 格式化消息时间戳显示
// - 1 分钟内：显示"刚刚"
// - 今天：显示 "HH:mm"
// - 今年：显示 "MM-DD HH:mm"
// - 更早：显示 "YYYY-MM-DD HH:mm"
function formatMessageTime(timestamp: number): string {
  const now = Date.now();
  const diff = now - timestamp;
  // 1 分钟内
  if (diff < 60 * 1000) return '';
  const date = new Date(timestamp);
  const today = new Date();
  const pad = (n: number) => n.toString().padStart(2, '0');
  const hhmm = `${pad(date.getHours())}:${pad(date.getMinutes())}`;
  // 今天
  if (
    date.getFullYear() === today.getFullYear() &&
    date.getMonth() === today.getMonth() &&
    date.getDate() === today.getDate()
  ) {
    return hhmm;
  }
  const mmdd = `${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
  // 今年
  if (date.getFullYear() === today.getFullYear()) {
    return `${mmdd} ${hhmm}`;
  }
  // 更早
  return `${date.getFullYear()}-${mmdd} ${hhmm}`;
}

// 判断是否为图片 MIME 类型
function isImageMime(mime: string): boolean {
  return mime.startsWith('image/');
}

// 格式化文件大小显示
function formatFileSize(bytes: number): string {
  if (bytes < 1024) return `${bytes}B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)}KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)}MB`;
}

// 将 File 对象转换为 AttachmentItem
function fileToAttachment(file: File): Promise<AttachmentItem> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      const dataUrl = reader.result as string;
      // dataUrl 格式: "data:<mime>;base64,<data>"
      const base64 = dataUrl.split(',')[1] || '';
      const item: AttachmentItem = {
        id: crypto.randomUUID(),
        type: isImageMime(file.type) ? 'image' : 'file',
        name: file.name,
        mimeType: file.type || 'application/octet-stream',
        size: file.size,
        data: base64,
      };
      // 图片需要预览 URL
      if (item.type === 'image') {
        item.previewUrl = dataUrl;
      }
      resolve(item);
    };
    reader.onerror = () => reject(reader.error);
    reader.readAsDataURL(file);
  });
}

// 单个 Tab 会话组件：管理独立的 WebSocket 连接、消息处理和聊天 UI
export default function VibeSession({ tab, onUpdateTab, visible }: VibeSessionProps) {
  const { t } = useTranslation();
  const token = useAuthStore((s) => s.token);

  // 项目列表（所有 Tab 共享同一份，但各自独立请求也没问题）
  const [projects, setProjects] = useState<ProjectSummary[]>([]);
  // 复制工作目录成功提示
  const [copySuccess, setCopySuccess] = useState(false);
  // 复制消息成功提示（追踪被复制的消息 ID）
  const [copiedMsgId, setCopiedMsgId] = useState<number | null>(null);
  // 断开连接确认弹窗
  const [showDisconnectConfirm, setShowDisconnectConfirm] = useState(false);
  // 工作目录下拉菜单展开状态
  const [showWorkDirDropdown, setShowWorkDirDropdown] = useState(false);
  const workDirDropdownRef = useRef<HTMLDivElement>(null);

  // Refs — 每个 VibeSession 实例独立
  const wsRef = useRef<WebSocket | null>(null);
  const heartbeatRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const messageListRef = useRef<HTMLDivElement>(null);
  const msgIdRef = useRef(0);
  const currentTextMsgIdRef = useRef<number | null>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);

  // 拖拽状态
  const [isDragging, setIsDragging] = useState(false);
  const dragCounterRef = useRef(0);

  // 简写：更新当前 tab 的状态
  const updateTab = useCallback(
    (updates: Partial<TabState>) => {
      onUpdateTab(tab.id, updates);
    },
    [tab.id, onUpdateTab]
  );

  // ── Helpers ──────────────────────────────────────

  const scrollToBottom = useCallback(() => {
    requestAnimationFrame(() => {
      if (messageListRef.current) {
        messageListRef.current.scrollTop = messageListRef.current.scrollHeight;
      }
    });
  }, []);

  // auto-scroll on new messages
  useEffect(() => {
    if (visible) {
      scrollToBottom();
    }
  }, [tab.messages, visible, scrollToBottom]);

  // 页面加载时获取项目列表，用于工作目录下拉选择
  useEffect(() => {
    listProjects()
      .then((res) => {
        const list = res?.projects || [];
        setProjects(list);
        // 如果只有一个项目且 workDir 为空，自动选中
        if (list.length === 1 && list[0].work_dir && !tab.workDir) {
          updateTab({
            workDir: list[0].work_dir,
            label: list[0].name,
          });
        }
      })
      .catch(() => {
        // 获取项目列表失败时静默处理
      });
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  // 点击外部关闭工作目录下拉菜单
  useEffect(() => {
    const handleClickOutside = (e: MouseEvent) => {
      if (workDirDropdownRef.current && !workDirDropdownRef.current.contains(e.target as Node)) {
        setShowWorkDirDropdown(false);
      }
    };
    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, []);

  // ── WebSocket ──────────────────────────────────────

  const sendWsMessage = useCallback((data: Record<string, unknown>) => {
    if (wsRef.current && wsRef.current.readyState === WebSocket.OPEN) {
      wsRef.current.send(JSON.stringify(data));
    }
  }, []);

  // 处理服务器消息 — 通过 ref 避免闭包过时问题
  const tabRef = useRef(tab);
  tabRef.current = tab;

  const handleServerMessage = useCallback(
    (data: Record<string, unknown>) => {
      const type = data.type as string;
      const currentTab = tabRef.current;

      switch (type) {
        case 'connected':
          break;

        case 'status':
          updateTab({
            processAlive: data.alive as boolean,
            ...(!(data.alive as boolean) ? { waiting: false } : {}),
          });
          if (data.message) {
            const id = ++msgIdRef.current;
            updateTab({
              messages: [
                ...currentTab.messages,
                { id, role: 'system', type: 'text', content: data.message as string, timestamp: Date.now() },
              ],
            });
          }
          break;

        case 'session_id':
          updateTab({ sessionId: data.session_id as string });
          break;

        case 'text': {
          const content = data.content as string;
          const curId = currentTextMsgIdRef.current;
          if (curId !== null) {
            // 流式追加到当前消息
            updateTab({
              messages: currentTab.messages.map((m) =>
                m.id === curId ? { ...m, content: m.content + content } : m
              ),
            });
          } else {
            // 创建新消息
            const id = ++msgIdRef.current;
            currentTextMsgIdRef.current = id;
            updateTab({
              messages: [
                ...currentTab.messages,
                { id, role: 'assistant', type: 'text', content, timestamp: Date.now() },
              ],
            });
          }
          break;
        }

        case 'tool_use':
          currentTextMsgIdRef.current = null;
          {
            const id = ++msgIdRef.current;
            updateTab({
              messages: [
                ...currentTab.messages,
                {
                  id,
                  role: 'assistant',
                  type: 'tool_use',
                  content: '',
                  toolName: data.tool_name as string,
                  toolInput: data.tool_input as string,
                  toolInputFull: data.tool_input_full as Record<string, unknown>,
                  timestamp: Date.now(),
                },
              ],
            });
          }
          break;

        case 'tool_result':
          currentTextMsgIdRef.current = null;
          {
            const id = ++msgIdRef.current;
            updateTab({
              messages: [
                ...currentTab.messages,
                {
                  id,
                  role: 'assistant',
                  type: 'tool_result',
                  content: (data.content as string) || '',
                  toolName: data.tool_name as string,
                  timestamp: Date.now(),
                },
              ],
            });
          }
          break;

        case 'thinking':
          currentTextMsgIdRef.current = null;
          {
            const id = ++msgIdRef.current;
            updateTab({
              messages: [
                ...currentTab.messages,
                { id, role: 'assistant', type: 'thinking', content: data.content as string, timestamp: Date.now() },
              ],
            });
          }
          break;

        case 'result':
          {
            const streamingMsgId = currentTextMsgIdRef.current;
            currentTextMsgIdRef.current = null;

            if (streamingMsgId !== null) {
              // 已有流式 text 消息 — 将 token 统计合并到该消息上，不创建新消息
              updateTab({
                waiting: false,
                messages: currentTab.messages.map((m) =>
                  m.id === streamingMsgId
                    ? {
                        ...m,
                        type: 'result' as const,
                        inputTokens: data.input_tokens as number,
                        outputTokens: data.output_tokens as number,
                      }
                    : m
                ),
              });
            } else {
              // 没有流式 text 消息（如纯工具调用后的结果） — 创建新的 result 消息
              const id = ++msgIdRef.current;
              updateTab({
                waiting: false,
                messages: [
                  ...currentTab.messages,
                  {
                    id,
                    role: 'assistant',
                    type: 'result',
                    content: (data.content as string) || t('vibe.taskDone'),
                    inputTokens: data.input_tokens as number,
                    outputTokens: data.output_tokens as number,
                    timestamp: Date.now(),
                  },
                ],
              });
            }
          }
          break;

        case 'permission_request':
          currentTextMsgIdRef.current = null;
          {
            const id = ++msgIdRef.current;
            updateTab({
              messages: [
                ...currentTab.messages,
                {
                  id,
                  role: 'assistant',
                  type: 'permission_request',
                  content: '',
                  requestId: data.request_id as string,
                  toolName: data.tool_name as string,
                  toolInput: data.tool_input as string,
                  toolInputFull: data.tool_input_full as Record<string, unknown>,
                  timestamp: Date.now(),
                },
              ],
            });
          }
          break;

        case 'error':
          currentTextMsgIdRef.current = null;
          {
            const id = ++msgIdRef.current;
            updateTab({
              waiting: false,
              messages: [
                ...currentTab.messages,
                { id, role: 'assistant', type: 'error', content: data.message as string, timestamp: Date.now() },
              ],
            });
          }
          break;

        case 'permission_cancelled':
          // Claude Code 取消了之前的权限请求，自动标记为已取消
          {
            const cancelledRequestId = data.request_id as string;
            if (cancelledRequestId) {
              updateTab({
                messages: currentTab.messages.map((m) =>
                  m.requestId === cancelledRequestId && !m.content
                    ? { ...m, content: t('vibe.cancelled', 'Cancelled') }
                    : m
                ),
              });
            }
          }
          break;

        default:
          break;
      }
    },
    [updateTab, t]
  );

  const handleServerMessageRef = useRef(handleServerMessage);
  handleServerMessageRef.current = handleServerMessage;

  const connectWebSocket = useCallback(() => {
    if (wsRef.current && wsRef.current.readyState === WebSocket.OPEN) return;
    updateTab({ connectionStatus: 'connecting' });

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    let wsUrl = `${protocol}//${window.location.host}/api/vibe/ws`;
    if (token) wsUrl += `?token=${encodeURIComponent(token)}`;

    const ws = new WebSocket(wsUrl);
    wsRef.current = ws;

    ws.onopen = () => {
      updateTab({ connectionStatus: 'connected' });
      const id = ++msgIdRef.current;
      const currentTab = tabRef.current;
      updateTab({
        messages: [
          ...currentTab.messages,
          { id, role: 'system', type: 'text', content: 'WebSocket ' + t('vibe.wsConnected'), timestamp: Date.now() },
        ],
      });
      // 启动心跳定时器：每 30 秒发送 ping，检测静默断开
      if (heartbeatRef.current) clearInterval(heartbeatRef.current);
      heartbeatRef.current = setInterval(() => {
        if (ws.readyState === WebSocket.OPEN) {
          try {
            ws.send(JSON.stringify({ type: 'ping' }));
          } catch {
            // 发送失败说明连接已断，触发 onclose
          }
        }
      }, 30000);
    };

    ws.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data);
        // 忽略 pong 响应（心跳回复）
        if (data.type === 'pong') return;
        handleServerMessageRef.current(data);
      } catch (e) {
        console.error('parse ws message failed:', e);
      }
    };

    ws.onclose = () => {
      // 清理心跳定时器
      if (heartbeatRef.current) {
        clearInterval(heartbeatRef.current);
        heartbeatRef.current = null;
      }
      updateTab({ connectionStatus: 'disconnected', processAlive: false, waiting: false });
      const id = ++msgIdRef.current;
      const currentTab = tabRef.current;
      updateTab({
        messages: [
          ...currentTab.messages,
          {
            id,
            role: 'system',
            type: 'text',
            content: 'WebSocket ' + t('vibe.wsDisconnected'),
            timestamp: Date.now(),
          },
        ],
      });
    };

    ws.onerror = () => {
      updateTab({ connectionStatus: 'disconnected' });
    };
  }, [token, updateTab, t]);

  const disconnectWebSocket = useCallback(() => {
    if (heartbeatRef.current) {
      clearInterval(heartbeatRef.current);
      heartbeatRef.current = null;
    }
    if (wsRef.current) {
      wsRef.current.close();
      wsRef.current = null;
    }
    updateTab({ connectionStatus: 'disconnected', processAlive: false, waiting: false });
  }, [updateTab]);

  // cleanup on unmount（Tab 关闭时断开 WebSocket 和心跳）
  useEffect(() => {
    return () => {
      if (heartbeatRef.current) {
        clearInterval(heartbeatRef.current);
        heartbeatRef.current = null;
      }
      if (wsRef.current) {
        wsRef.current.close();
        wsRef.current = null;
      }
    };
  }, []);

  // ── User Actions ──────────────────────────────────

  const startSession = () => {
    if (!tab.workDir.trim()) return;
    connectWebSocket();
    // 等 WebSocket 连接后发送 start
    const checkAndStart = () => {
      if (wsRef.current && wsRef.current.readyState === WebSocket.OPEN) {
        sendWsMessage({ type: 'start', workDir: tab.workDir.trim(), model: tab.modelName.trim() });
      } else {
        setTimeout(checkAndStart, 100);
      }
    };
    checkAndStart();
  };

  // ── 附件处理函数 ─────────────────────────────────

  // 通用添加附件函数（含大小校验）
  const addAttachments = useCallback(
    async (fileList: FileList | File[]) => {
      const files = Array.from(fileList);
      const items: AttachmentItem[] = [];
      for (const file of files) {
        if (file.size > MAX_FILE_SIZE) {
          // 超过 10MB 提示错误
          const id = ++msgIdRef.current;
          const currentTab = tabRef.current;
          updateTab({
            messages: [
              ...currentTab.messages,
              { id, role: 'system', type: 'error', content: t('vibe.fileTooLarge', { name: file.name }), timestamp: Date.now() },
            ],
          });
          continue;
        }
        try {
          const item = await fileToAttachment(file);
          items.push(item);
        } catch (err) {
          console.error('fileToAttachment failed:', err);
        }
      }
      if (items.length > 0) {
        const currentTab = tabRef.current;
        updateTab({ pendingAttachments: [...currentTab.pendingAttachments, ...items] });
      }
    },
    [updateTab, t]
  );

  // 删除指定附件
  const removeAttachment = useCallback(
    (id: string) => {
      updateTab({
        pendingAttachments: tab.pendingAttachments.filter((a) => a.id !== id),
      });
    },
    [tab.pendingAttachments, updateTab]
  );

  // file input onChange 回调
  const handleFileInputChange = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      if (e.target.files && e.target.files.length > 0) {
        addAttachments(e.target.files);
        e.target.value = ''; // 重置，以便重复选择同一个文件
      }
    },
    [addAttachments]
  );

  // textarea onPaste，提取剪贴板图片
  const handlePaste = useCallback(
    (e: React.ClipboardEvent) => {
      const items = e.clipboardData?.items;
      if (!items) return;
      const files: File[] = [];
      for (const item of Array.from(items)) {
        if (item.kind === 'file') {
          const file = item.getAsFile();
          if (file) files.push(file);
        }
      }
      if (files.length > 0) {
        e.preventDefault();
        addAttachments(files);
      }
    },
    [addAttachments]
  );

  // 拖拽事件处理
  const handleDragEnter = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    dragCounterRef.current++;
    if (dragCounterRef.current === 1) {
      setIsDragging(true);
    }
  }, []);

  const handleDragLeave = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    dragCounterRef.current--;
    if (dragCounterRef.current === 0) {
      setIsDragging(false);
    }
  }, []);

  const handleDragOver = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
  }, []);

  const handleDrop = useCallback(
    (e: React.DragEvent) => {
      e.preventDefault();
      e.stopPropagation();
      dragCounterRef.current = 0;
      setIsDragging(false);
      if (e.dataTransfer.files && e.dataTransfer.files.length > 0) {
        addAttachments(e.dataTransfer.files);
      }
    },
    [addAttachments]
  );

  const sendMessage = () => {
    const msg = tab.userInput.trim();
    const attachments = tab.pendingAttachments;
    if ((!msg && attachments.length === 0) || !tab.processAlive) return;

    // 添加用户消息（带附件信息用于渲染）
    const id = ++msgIdRef.current;
    updateTab({
      messages: [
        ...tab.messages,
        {
          id,
          role: 'user',
          type: 'text',
          content: msg,
          timestamp: Date.now(),
          attachments: attachments.length > 0 ? attachments : undefined,
        },
      ],
      userInput: '',
      pendingAttachments: [],
    });

    currentTextMsgIdRef.current = null;
    updateTab({ waiting: true });

    // WebSocket 消息：附件包含 base64 数据
    const wsMsg: Record<string, unknown> = { type: 'send', message: msg };
    if (attachments.length > 0) {
      wsMsg.attachments = attachments.map((a) => ({
        type: a.type,
        name: a.name,
        mime_type: a.mimeType,
        data: a.data,
      }));
    }
    sendWsMessage(wsMsg);
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      sendMessage();
    }
  };

  const allowPermission = (requestId: string) => {
    sendWsMessage({ type: 'permission', request_id: requestId, behavior: 'allow' });
    updateTab({
      messages: tab.messages.map((m) => (m.requestId === requestId ? { ...m, content: t('vibe.allowed') } : m)),
    });
  };

  const denyPermission = (requestId: string) => {
    sendWsMessage({ type: 'permission', request_id: requestId, behavior: 'deny' });
    updateTab({
      messages: tab.messages.map((m) => (m.requestId === requestId ? { ...m, content: t('vibe.denied') } : m)),
    });
  };

  const abortExecution = () => {
    sendWsMessage({ type: 'abort' });
    updateTab({ waiting: false });
  };

  const newSession = () => {
    disconnectWebSocket();
    currentTextMsgIdRef.current = null;
    msgIdRef.current = 0;
    updateTab({ messages: [], sessionId: '' });
  };

  const toggleExpand = (id: number) => {
    const next = new Set(tab.expandedItems);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    updateTab({ expandedItems: next });
  };

  const formatJson = (obj: unknown): string => {
    try {
      return JSON.stringify(obj, null, 2);
    } catch {
      return String(obj);
    }
  };

  // 从下拉列表选择项目
  const handleProjectSelect = (project: ProjectSummary) => {
    updateTab({
      workDir: project.work_dir,
      label: project.name,
    });
    setShowWorkDirDropdown(false);
  };

  // 手动输入工作目录路径
  const handleWorkDirInput = (value: string) => {
    // 如果输入的值匹配某个项目的 work_dir，自动关联项目名
    const matchedProject = projects.find((p) => p.work_dir === value);
    updateTab({
      workDir: value,
      label: matchedProject?.name || '',
    });
  };

  // 复制工作目录路径到剪贴板
  const copyWorkDir = () => {
    if (!tab.workDir) return;
    navigator.clipboard.writeText(tab.workDir).then(() => {
      setCopySuccess(true);
      setTimeout(() => setCopySuccess(false), 1500);
    });
  };

  // 复制消息内容到剪贴板（复用 copyWorkDir 的 Copy→Check 切换模式）
  const copyMessage = (msgId: number, content: string) => {
    if (!content) return;
    navigator.clipboard.writeText(content).then(() => {
      setCopiedMsgId(msgId);
      setTimeout(() => setCopiedMsgId(null), 1500);
    });
  };

  // 断开连接（带确认逻辑）
  const handleDisconnect = () => {
    if (tab.processAlive) {
      setShowDisconnectConfirm(true);
      return;
    }
    newSession();
  };

  const confirmDisconnect = () => {
    setShowDisconnectConfirm(false);
    newSession();
  };

  // ── Status helpers ─────────────────────────────────

  const statusText =
    tab.connectionStatus === 'connected' && tab.processAlive
      ? t('vibe.claudeConnected')
      : tab.connectionStatus === 'connected'
        ? 'WebSocket ' + t('vibe.wsConnected')
        : tab.connectionStatus === 'connecting'
          ? t('vibe.connecting')
          : t('vibe.disconnected');

  const statusColor =
    tab.connectionStatus === 'connected' && tab.processAlive
      ? 'text-emerald-600 dark:text-emerald-400 bg-emerald-50 dark:bg-emerald-900/20'
      : tab.connectionStatus === 'connecting'
        ? 'text-amber-600 dark:text-amber-400 bg-amber-50 dark:bg-amber-900/20'
        : 'text-gray-500 dark:text-gray-400 bg-gray-100 dark:bg-gray-800';

  // ── Render ─────────────────────────────────────────

  return (
    <div
      className={cn('flex flex-col h-full relative', !visible && 'hidden')}
      onDragEnter={handleDragEnter}
      onDragLeave={handleDragLeave}
      onDragOver={handleDragOver}
      onDrop={handleDrop}
    >
      {/* 拖拽覆盖层 */}
      {isDragging && (
        <div className="absolute inset-0 z-40 flex items-center justify-center bg-accent/10 border-2 border-dashed border-accent rounded-xl pointer-events-none">
          <div className="text-accent font-medium text-lg">{t('vibe.dropFiles')}</div>
        </div>
      )}

      {/* Header */}
      <div
        className={cn(
          'flex items-center justify-between pb-4 gap-4 flex-wrap',
          'border-b border-gray-200 dark:border-gray-800'
        )}
      >
        <div className="flex items-center gap-3 flex-1 min-w-0">
          <div className="flex-1 flex gap-3 min-w-0">
            <div className="flex-1 min-w-[200px]">
              <label className="block text-[10px] font-medium uppercase tracking-wider text-gray-400 mb-1">
                {t('vibe.workDir')}
              </label>
              <div className="flex gap-1.5">
                {/* Combobox: 支持下拉选择项目 + 手动输入路径 */}
                <div ref={workDirDropdownRef} className="relative flex-1 min-w-0">
                  <div className="flex">
                    <input
                      value={tab.workDir}
                      onChange={(e) => handleWorkDirInput(e.target.value)}
                      disabled={tab.processAlive}
                      placeholder={t('vibe.workDirPlaceholder')}
                      className="flex-1 min-w-0 px-3 py-2 text-sm rounded-l-lg border border-r-0 border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-800 text-gray-900 dark:text-white focus:outline-none focus:ring-2 focus:ring-accent/50 focus:border-accent transition-colors placeholder:text-gray-400 disabled:opacity-50"
                    />
                    {/* 展开/收起下拉列表按钮 */}
                    <button
                      onClick={() => !tab.processAlive && setShowWorkDirDropdown((v) => !v)}
                      disabled={tab.processAlive || projects.length === 0}
                      className="shrink-0 px-2 py-2 rounded-r-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-800 text-gray-500 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-700 transition-colors disabled:opacity-30 disabled:cursor-not-allowed"
                    >
                      <ChevronsUpDown size={16} />
                    </button>
                  </div>
                  {/* 下拉列表 */}
                  {showWorkDirDropdown && projects.length > 0 && (
                    <div className="absolute z-50 top-full left-0 right-0 mt-1 max-h-60 overflow-y-auto rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-800 shadow-lg">
                      {projects.map((p) => (
                        <button
                          key={p.name}
                          onClick={() => handleProjectSelect(p)}
                          className={cn(
                            'w-full text-left px-3 py-2 text-sm hover:bg-gray-100 dark:hover:bg-gray-700 transition-colors',
                            tab.workDir === p.work_dir
                              ? 'bg-accent/10 text-accent font-medium'
                              : 'text-gray-900 dark:text-white'
                          )}
                        >
                          <span className="font-medium">{p.name}</span>
                          <span className="text-gray-400 ml-1.5">— {p.work_dir}</span>
                        </button>
                      ))}
                    </div>
                  )}
                </div>
                {/* 复制工作目录路径按钮 */}
                <button
                  onClick={copyWorkDir}
                  disabled={!tab.workDir}
                  title={copySuccess ? t('vibe.copied') : t('vibe.workDir')}
                  className="shrink-0 px-2 py-2 rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-800 text-gray-500 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-700 hover:text-gray-700 dark:hover:text-gray-200 transition-colors disabled:opacity-30 disabled:cursor-not-allowed"
                >
                  {copySuccess ? <Check size={16} className="text-emerald-500" /> : <Copy size={16} />}
                </button>
              </div>
            </div>
          </div>
        </div>

        <div className="flex items-center gap-2 shrink-0">
          <span
            className={cn(
              'inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-medium',
              statusColor
            )}
          >
            <Circle size={6} className="fill-current" />
            {statusText}
          </span>
          {tab.sessionId && (
            <span className="text-[10px] text-gray-400 font-mono hidden lg:inline">
              {tab.sessionId.slice(0, 12)}
            </span>
          )}

          {!tab.processAlive ? (
            <button
              onClick={startSession}
              disabled={!tab.workDir.trim() || tab.connectionStatus === 'connecting'}
              className="inline-flex items-center gap-1.5 px-3.5 py-2 rounded-lg text-xs font-medium bg-accent text-black hover:bg-accent-dim transition-colors disabled:opacity-50"
            >
              <Play size={14} />
              {t('vibe.start')}
            </button>
          ) : (
            <button
              onClick={handleDisconnect}
              className="inline-flex items-center gap-1.5 px-3.5 py-2 rounded-lg text-xs font-medium bg-red-500/10 text-red-600 dark:text-red-400 hover:bg-red-500/20 transition-colors"
            >
              <Square size={14} />
              {t('vibe.disconnect')}
            </button>
          )}
          <button
            onClick={newSession}
            className="inline-flex items-center gap-1.5 px-3.5 py-2 rounded-lg text-xs font-medium bg-gray-100 dark:bg-gray-800 text-gray-600 dark:text-gray-400 hover:bg-gray-200 dark:hover:bg-gray-700 transition-colors"
          >
            <Plus size={14} />
            {t('vibe.newSession')}
          </button>
        </div>
      </div>

      {/* Messages */}
      <div ref={messageListRef} className="flex-1 overflow-y-auto py-4 space-y-3">
        {tab.messages.length === 0 && (
          <div className="flex items-center justify-center h-full text-gray-400 dark:text-gray-500 text-sm">
            {t('vibe.emptyHint')}
          </div>
        )}

        {tab.messages.map((msg) => (
          <div key={msg.id} className={cn('px-2', msg.role === 'user' && 'flex justify-end')}>
            {/* System message */}
            {msg.role === 'system' && (
              <div className="text-center text-xs text-gray-400 py-1">
                <Circle size={5} className="inline mr-1 fill-current" />
                {msg.content}
              </div>
            )}

            {/* User message */}
            {msg.role === 'user' && (
              <div className="flex flex-col items-end gap-0.5">
                <div className="group/msg max-w-[70%] px-4 py-2.5 rounded-2xl rounded-br-md bg-accent text-black text-sm relative">
                {/* 用户消息中的附件 */}
                {msg.attachments && msg.attachments.length > 0 && (
                  <div className="flex flex-wrap gap-2 mb-2">
                    {msg.attachments.map((att) =>
                      att.type === 'image' && att.previewUrl ? (
                        <img
                          key={att.id}
                          src={att.previewUrl}
                          alt={att.name}
                          className="max-w-[200px] max-h-[150px] rounded-lg object-cover border border-black/10"
                        />
                      ) : (
                        <span
                          key={att.id}
                          className="inline-flex items-center gap-1 px-2 py-1 rounded-lg bg-black/10 text-xs"
                        >
                          <FileText size={12} />
                          {att.name}
                          <span className="opacity-60">{formatFileSize(att.size)}</span>
                        </span>
                      )
                    )}
                  </div>
                )}
                {msg.content && (
                  <pre className="whitespace-pre-wrap font-[inherit] m-0 text-sm leading-relaxed">{msg.content}</pre>
                )}
                {/* 复制按钮 — hover 时显示 */}
                {msg.content && (
                  <button
                    onClick={() => copyMessage(msg.id, msg.content)}
                    className="absolute top-2 right-2 p-1 rounded-md bg-black/10 text-black/40 hover:text-black/70 opacity-0 group-hover/msg:opacity-100 transition-opacity"
                  >
                    {copiedMsgId === msg.id ? <Check size={14} className="text-emerald-700" /> : <Copy size={14} />}
                  </button>
                )}
              </div>
              {/* 用户消息时间戳 */}
              {formatMessageTime(msg.timestamp) && (
                <span className="text-[10px] text-gray-400 dark:text-gray-500 px-1">
                  {formatMessageTime(msg.timestamp)}
                </span>
              )}
              </div>
            )}

            {/* Assistant messages */}
            {msg.role === 'assistant' && (
              <>
                {/* Text */}
                {msg.type === 'text' && (
                  <div className="flex flex-col gap-0.5">
                    <div className="group/msg max-w-[85%] px-4 py-3 rounded-2xl rounded-bl-md bg-white dark:bg-gray-800/80 border border-gray-200 dark:border-gray-700/60 shadow-sm relative">
                      <VibeMarkdown content={msg.content} />
                      {/* 复制按钮 — hover 时显示 */}
                      <button
                        onClick={() => copyMessage(msg.id, msg.content)}
                        className="absolute top-2 right-2 p-1 rounded-md bg-gray-100 dark:bg-gray-700 text-gray-400 hover:text-gray-600 dark:hover:text-gray-200 opacity-0 group-hover/msg:opacity-100 transition-opacity"
                      >
                        {copiedMsgId === msg.id ? <Check size={14} className="text-emerald-500" /> : <Copy size={14} />}
                      </button>
                    </div>
                    {formatMessageTime(msg.timestamp) && (
                      <span className="text-[10px] text-gray-400 dark:text-gray-500 px-1">
                        {formatMessageTime(msg.timestamp)}
                      </span>
                    )}
                  </div>
                )}

                {/* Thinking */}
                {msg.type === 'thinking' && (
                  <div className="max-w-[85%] border border-dashed border-gray-300 dark:border-gray-700 rounded-xl overflow-hidden">
                    <button
                      onClick={() => toggleExpand(msg.id)}
                      className="flex items-center gap-2 w-full px-3 py-2 text-xs text-gray-500 dark:text-gray-400 italic hover:bg-gray-50 dark:hover:bg-gray-800/50 transition-colors"
                    >
                      <Brain size={14} />
                      <span>{t('vibe.thinking')}</span>
                      {tab.expandedItems.has(msg.id) ? (
                        <ChevronDown size={14} className="ml-auto" />
                      ) : (
                        <ChevronRight size={14} className="ml-auto" />
                      )}
                    </button>
                    {tab.expandedItems.has(msg.id) && (
                      <pre className="px-3 pb-3 text-xs text-gray-600 dark:text-gray-400 whitespace-pre-wrap max-h-72 overflow-y-auto border-t border-dashed border-gray-200 dark:border-gray-700">
                        {msg.content}
                      </pre>
                    )}
                  </div>
                )}

                {/* Tool Use */}
                {msg.type === 'tool_use' && (
                  <div className="max-w-[85%] border border-amber-200 dark:border-amber-800/50 bg-amber-50/50 dark:bg-amber-900/10 rounded-xl overflow-hidden">
                    <button
                      onClick={() => toggleExpand(msg.id)}
                      className="flex items-center gap-2 w-full px-3 py-2 text-xs hover:bg-amber-100/50 dark:hover:bg-amber-900/20 transition-colors"
                    >
                      <Wrench size={14} className="text-amber-600 dark:text-amber-400 shrink-0" />
                      <span className="font-medium text-amber-700 dark:text-amber-300">{msg.toolName}</span>
                      <span className="text-gray-500 dark:text-gray-400 truncate flex-1 text-left">
                        {msg.toolInput}
                      </span>
                      {tab.expandedItems.has(msg.id) ? (
                        <ChevronDown size={14} className="shrink-0 text-gray-400" />
                      ) : (
                        <ChevronRight size={14} className="shrink-0 text-gray-400" />
                      )}
                    </button>
                    {tab.expandedItems.has(msg.id) && msg.toolInputFull && (
                      <pre className="px-3 pb-3 text-xs text-gray-600 dark:text-gray-400 whitespace-pre-wrap max-h-96 overflow-y-auto border-t border-amber-200 dark:border-amber-800/50 bg-white/50 dark:bg-black/10">
                        {formatJson(msg.toolInputFull)}
                      </pre>
                    )}
                  </div>
                )}

                {/* Tool Result */}
                {msg.type === 'tool_result' && (
                  <div className="max-w-[85%] border border-emerald-200 dark:border-emerald-800/50 bg-emerald-50/50 dark:bg-emerald-900/10 rounded-xl overflow-hidden">
                    <button
                      onClick={() => toggleExpand(msg.id)}
                      className="flex items-center gap-2 w-full px-3 py-2 text-xs hover:bg-emerald-100/50 dark:hover:bg-emerald-900/20 transition-colors"
                    >
                      <Wrench size={14} className="text-emerald-600 dark:text-emerald-400 shrink-0" />
                      <span className="font-medium text-emerald-700 dark:text-emerald-300">
                        {msg.toolName ? `${msg.toolName} ${t('vibe.result')}` : t('vibe.toolResult')}
                      </span>
                      {tab.expandedItems.has(msg.id) ? (
                        <ChevronDown size={14} className="shrink-0 text-gray-400" />
                      ) : (
                        <ChevronRight size={14} className="shrink-0 text-gray-400" />
                      )}
                    </button>
                    {tab.expandedItems.has(msg.id) && (
                      <pre className="px-3 pb-3 text-xs text-gray-600 dark:text-gray-400 whitespace-pre-wrap max-h-96 overflow-y-auto border-t border-emerald-200 dark:border-emerald-800/50">
                        {msg.content}
                      </pre>
                    )}
                  </div>
                )}

                {/* Permission Request */}
                {msg.type === 'permission_request' && (
                  <div className="max-w-[85%] border border-red-200 dark:border-red-800/50 bg-red-50/50 dark:bg-red-900/10 rounded-xl overflow-hidden">
                    <div className="flex items-center gap-2 px-3 py-2 text-xs font-semibold text-red-600 dark:text-red-400">
                      <ShieldCheck size={14} />
                      {t('vibe.permissionRequest')}
                    </div>
                    <div className="px-3 pb-3 space-y-2">
                      <div className="text-xs">
                        <span className="font-medium text-gray-700 dark:text-gray-300">{t('vibe.tool')}: </span>
                        <span className="inline-block px-1.5 py-0.5 rounded bg-gray-100 dark:bg-gray-800 text-gray-700 dark:text-gray-300 font-mono">
                          {msg.toolName}
                        </span>
                      </div>
                      <pre className="text-xs text-gray-500 dark:text-gray-400 whitespace-pre-wrap bg-black/5 dark:bg-white/5 px-2 py-1.5 rounded">
                        {msg.toolInput}
                      </pre>
                      {!msg.content ? (
                        <div className="flex gap-2 pt-1">
                          <button
                            onClick={() => msg.requestId && allowPermission(msg.requestId)}
                            className="inline-flex items-center gap-1 px-3 py-1.5 rounded-lg text-xs font-medium bg-emerald-500 text-white hover:bg-emerald-600 transition-colors"
                          >
                            <ShieldCheck size={12} />
                            {t('vibe.allow')}
                          </button>
                          <button
                            onClick={() => msg.requestId && denyPermission(msg.requestId)}
                            className="inline-flex items-center gap-1 px-3 py-1.5 rounded-lg text-xs font-medium bg-red-500 text-white hover:bg-red-600 transition-colors"
                          >
                            <ShieldX size={12} />
                            {t('vibe.deny')}
                          </button>
                        </div>
                      ) : (
                        <span
                          className={cn(
                            'inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium',
                            msg.content === t('vibe.allowed')
                              ? 'bg-emerald-100 dark:bg-emerald-900/30 text-emerald-700 dark:text-emerald-300'
                              : 'bg-red-100 dark:bg-red-900/30 text-red-700 dark:text-red-300'
                          )}
                        >
                          {msg.content}
                        </span>
                      )}
                    </div>
                  </div>
                )}

                {/* Result */}
                {msg.type === 'result' && (
                  <div className="flex flex-col gap-0.5">
                    <div className="group/msg max-w-[85%] px-4 py-3 rounded-2xl rounded-bl-md bg-white dark:bg-gray-800/80 border border-gray-200 dark:border-gray-700/60 shadow-sm relative">
                      {msg.content && <VibeMarkdown content={msg.content} />}
                      {/* 复制按钮 — hover 时显示 */}
                      {msg.content && (
                        <button
                          onClick={() => copyMessage(msg.id, msg.content)}
                          className="absolute top-2 right-2 p-1 rounded-md bg-gray-100 dark:bg-gray-700 text-gray-400 hover:text-gray-600 dark:hover:text-gray-200 opacity-0 group-hover/msg:opacity-100 transition-opacity"
                        >
                          {copiedMsgId === msg.id ? <Check size={14} className="text-emerald-500" /> : <Copy size={14} />}
                        </button>
                      )}
                      {(msg.inputTokens || msg.outputTokens) && (
                        <div className="flex gap-2 mt-2 pt-2 border-t border-gray-200 dark:border-gray-700">
                          <span className="text-[10px] text-gray-400 bg-gray-100 dark:bg-gray-700 px-2 py-0.5 rounded-full">
                            IN: {msg.inputTokens?.toLocaleString()}
                          </span>
                          <span className="text-[10px] text-gray-400 bg-gray-100 dark:bg-gray-700 px-2 py-0.5 rounded-full">
                            OUT: {msg.outputTokens?.toLocaleString()}
                          </span>
                        </div>
                      )}
                    </div>
                    {formatMessageTime(msg.timestamp) && (
                      <span className="text-[10px] text-gray-400 dark:text-gray-500 px-1">
                        {formatMessageTime(msg.timestamp)}
                      </span>
                    )}
                  </div>
                )}

                {/* Error */}
                {msg.type === 'error' && (
                  <div className="flex flex-col gap-0.5">
                    <div className="max-w-[85%] flex items-start gap-2 px-4 py-3 rounded-xl bg-red-50 dark:bg-red-900/10 border border-red-200 dark:border-red-800/50 text-red-700 dark:text-red-300 text-sm">
                      <AlertCircle size={16} className="shrink-0 mt-0.5" />
                      {msg.content}
                    </div>
                    {formatMessageTime(msg.timestamp) && (
                      <span className="text-[10px] text-gray-400 dark:text-gray-500 px-1">
                        {formatMessageTime(msg.timestamp)}
                      </span>
                    )}
                  </div>
                )}
              </>
            )}
          </div>
        ))}

        {/* Waiting indicator */}
        {tab.waiting && (
          <div className="flex items-center gap-3 px-2">
            <div className="flex gap-1">
              <span className="w-2 h-2 bg-gray-400 rounded-full animate-bounce" style={{ animationDelay: '0ms' }} />
              <span
                className="w-2 h-2 bg-gray-400 rounded-full animate-bounce"
                style={{ animationDelay: '150ms' }}
              />
              <span
                className="w-2 h-2 bg-gray-400 rounded-full animate-bounce"
                style={{ animationDelay: '300ms' }}
              />
            </div>
            <button
              onClick={abortExecution}
              className="inline-flex items-center gap-1 px-2.5 py-1 rounded-lg text-xs font-medium bg-amber-100 dark:bg-amber-900/30 text-amber-700 dark:text-amber-300 hover:bg-amber-200 dark:hover:bg-amber-900/50 transition-colors"
            >
              <XCircle size={12} />
              {t('vibe.abort')}
            </button>
          </div>
        )}
      </div>

      {/* Input Area */}
      <div className="border-t border-gray-200 dark:border-gray-800 pt-4">
        {/* 附件预览区 */}
        {tab.pendingAttachments.length > 0 && (
          <div className="flex flex-wrap gap-2 mb-3 px-1">
            {tab.pendingAttachments.map((att) => (
              <div
                key={att.id}
                className="relative group flex items-center gap-2 px-2.5 py-1.5 rounded-lg bg-gray-100 dark:bg-gray-800 border border-gray-200 dark:border-gray-700"
              >
                {att.type === 'image' && att.previewUrl ? (
                  <img
                    src={att.previewUrl}
                    alt={att.name}
                    className="w-16 h-16 rounded object-cover"
                  />
                ) : (
                  <>
                    <FileText size={16} className="text-gray-500 shrink-0" />
                    <div className="text-xs text-gray-700 dark:text-gray-300 max-w-[120px] truncate">
                      {att.name}
                    </div>
                    <span className="text-[10px] text-gray-400">{formatFileSize(att.size)}</span>
                  </>
                )}
                {/* 删除按钮 */}
                <button
                  onClick={() => removeAttachment(att.id)}
                  className="absolute -top-1.5 -right-1.5 w-5 h-5 rounded-full bg-gray-500 text-white flex items-center justify-center opacity-0 group-hover:opacity-100 transition-opacity hover:bg-red-500"
                >
                  <X size={10} />
                </button>
              </div>
            ))}
          </div>
        )}
        <div className="flex gap-3 items-end">
          {/* 隐藏的 file input */}
          <input
            ref={fileInputRef}
            type="file"
            multiple
            className="hidden"
            onChange={handleFileInputChange}
          />
          {/* 附件按钮 */}
          <button
            onClick={() => fileInputRef.current?.click()}
            disabled={!tab.processAlive}
            title={t('vibe.attach')}
            className="px-3 py-3 rounded-xl border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-800 text-gray-500 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-700 hover:text-gray-700 dark:hover:text-gray-200 transition-colors disabled:opacity-50 disabled:cursor-not-allowed self-stretch flex items-center"
          >
            <Paperclip size={18} />
          </button>
          <textarea
            value={tab.userInput}
            onChange={(e) => updateTab({ userInput: e.target.value })}
            onKeyDown={handleKeyDown}
            onPaste={handlePaste}
            disabled={!tab.processAlive}
            placeholder={tab.processAlive ? t('vibe.inputPlaceholder') : t('vibe.startFirst')}
            rows={3}
            className="flex-1 px-4 py-3 text-sm rounded-xl border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-800 text-gray-900 dark:text-white focus:outline-none focus:ring-2 focus:ring-accent/50 focus:border-accent transition-colors placeholder:text-gray-400 resize-none disabled:opacity-50 disabled:cursor-not-allowed"
          />
          <button
            onClick={sendMessage}
            disabled={!tab.processAlive || (!tab.userInput.trim() && tab.pendingAttachments.length === 0)}
            className="px-4 py-3 rounded-xl bg-accent text-black hover:bg-accent-dim transition-colors disabled:opacity-50 flex items-center gap-2 self-stretch"
          >
            <Send size={18} />
          </button>
        </div>
      </div>

      {/* 断开连接确认弹窗 — 复用 VibeCoding.tsx 关闭 Tab 确认弹窗样式 */}
      {showDisconnectConfirm && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
          <div className="bg-white dark:bg-gray-900 rounded-xl shadow-2xl p-6 max-w-sm mx-4 border border-gray-200 dark:border-gray-700">
            <p className="text-sm text-gray-700 dark:text-gray-300 mb-4">
              {t('vibe.disconnectConfirm')}
            </p>
            <div className="flex justify-end gap-3">
              <button
                onClick={() => setShowDisconnectConfirm(false)}
                className="px-4 py-2 text-xs font-medium rounded-lg bg-gray-100 dark:bg-gray-800 text-gray-600 dark:text-gray-400 hover:bg-gray-200 dark:hover:bg-gray-700 transition-colors"
              >
                {t('vibe.cancel')}
              </button>
              <button
                onClick={confirmDisconnect}
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
