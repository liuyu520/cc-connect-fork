import api from './client';

// Vibe Coding 历史会话记录
export interface VibeSessionRecord {
  session_id: string;
  session_key: string;
  project: string;
  agent_type: string;
  agent_session_id: string;
  name: string;
  biz_type: string;
  created_at: string;
  updated_at: string;
  message_count: number;
  last_message: string;
}

// Vibe Coding 历史消息记录
export interface VibeMessageRecord {
  id: number;
  session_id: string;
  role: string;
  content: string;
  platform: string;
  user_id: string;
  user_name: string;
  biz_type: string;
  created_at: string;
}

// Vibe 历史 API 使用 /api/vibe/ 前缀（由 WebUIServer 提供，非 Management API）
const VIBE_API_BASE = '/api/vibe';

// 获取 Vibe 历史会话列表
export const listVibeSessions = (limit?: number) => {
  const params: Record<string, string> = {};
  if (limit) params.limit = String(limit);
  // 直接 fetch 而非走 api client（因为 /api/vibe 不在 /api/v1 下）
  const qs = new URLSearchParams(params).toString();
  const url = `${VIBE_API_BASE}/sessions${qs ? '?' + qs : ''}`;
  return fetch(url).then(res => res.json()).then(json => {
    if (!json.ok) throw new Error(json.error || 'Failed to list vibe sessions');
    return json.data as { sessions: VibeSessionRecord[] };
  });
};

// 获取指定 Vibe 会话的消息列表
export const getVibeMessages = (sessionId: string, limit?: number) => {
  const params: Record<string, string> = {};
  if (limit) params.limit = String(limit);
  const qs = new URLSearchParams(params).toString();
  const url = `${VIBE_API_BASE}/sessions/${sessionId}/messages${qs ? '?' + qs : ''}`;
  return fetch(url).then(res => res.json()).then(json => {
    if (!json.ok) throw new Error(json.error || 'Failed to get vibe messages');
    return json.data as { messages: VibeMessageRecord[] };
  });
};

// 常用提示词
export interface VibePrompt {
  title: string;
  content: string;
}

// 获取常用提示词列表
export const listVibePrompts = () => {
  const url = `${VIBE_API_BASE}/prompts`;
  return fetch(url).then(res => res.json()).then(json => {
    if (!json.ok) throw new Error(json.error || 'Failed to list vibe prompts');
    return json.data as { prompts: VibePrompt[] };
  });
};

// 导出请求中的单条消息
export interface ExportMessage {
  role: string;
  type: string;
  content: string;
  tool_name?: string;
  timestamp: number;
}

// 导出请求体
export interface ExportRequest {
  session_name: string;
  project: string;
  agent_type: string;
  session_id: string;
  messages: ExportMessage[];
}

// 导出 Vibe 会话为 Markdown 文件并触发浏览器下载
export const exportVibeSession = async (data: ExportRequest): Promise<void> => {
  const url = `${VIBE_API_BASE}/export`;
  const res = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(data),
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text || 'Export failed');
  }
  // 从 Content-Disposition 头中提取文件名
  const disposition = res.headers.get('Content-Disposition') || '';
  let filename = 'chat_export.md';
  const match = disposition.match(/filename="([^"]+)"/);
  if (match) {
    filename = match[1];
  }
  // 触发浏览器下载
  const blob = await res.blob();
  const a = document.createElement('a');
  a.href = URL.createObjectURL(blob);
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(a.href);
};
