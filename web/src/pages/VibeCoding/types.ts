// 附件类型
export interface AttachmentItem {
  id: string;           // crypto.randomUUID()
  type: 'image' | 'file';
  name: string;
  mimeType: string;
  size: number;         // 原始字节大小
  data: string;         // 纯 base64（不含 data: 前缀）
  previewUrl?: string;  // 图片预览用 data URL
}

// 聊天消息类型
export interface ChatMessage {
  id: number;
  role: 'user' | 'assistant' | 'system';
  type: 'text' | 'tool_use' | 'thinking' | 'result' | 'permission_request' | 'error' | 'tool_result';
  content: string;
  toolName?: string;
  toolInput?: string;
  toolInputFull?: Record<string, unknown>;
  requestId?: string;
  inputTokens?: number;
  outputTokens?: number;
  timestamp: number;
  attachments?: AttachmentItem[];
}

// 单个 Tab 的完整独立状态
export interface TabState {
  id: string;
  label: string;
  workDir: string;
  modelName: string;
  messages: ChatMessage[];
  userInput: string;
  connectionStatus: 'disconnected' | 'connecting' | 'connected';
  processAlive: boolean;
  waiting: boolean;
  sessionId: string;
  expandedItems: Set<number>;
  pendingAttachments: AttachmentItem[];
}

// 创建一个空白的 Tab 初始状态
export function createTabState(id?: string): TabState {
  return {
    id: id || crypto.randomUUID(),
    label: '',
    workDir: '',
    modelName: '',
    messages: [],
    userInput: '',
    connectionStatus: 'disconnected',
    processAlive: false,
    waiting: false,
    sessionId: '',
    expandedItems: new Set(),
    pendingAttachments: [],
  };
}
