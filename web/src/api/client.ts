const DEFAULT_API_BASE = '/api/v1';

class ApiClient {
  private token: string = '';
  private baseUrl: string = DEFAULT_API_BASE;

  setToken(token: string) {
    this.token = token;
  }

  getToken(): string {
    return this.token;
  }

  // 设置服务器地址（用于连接远程 Management API）
  // 传入完整地址如 "http://localhost:9820"，会自动拼接 /api/v1
  // 传入空字符串则恢复为相对路径（依赖 Vite 代理或同源部署）
  setBaseUrl(serverUrl: string) {
    if (serverUrl) {
      // 移除尾部斜杠后拼接 /api/v1
      this.baseUrl = serverUrl.replace(/\/+$/, '') + '/api/v1';
    } else {
      this.baseUrl = DEFAULT_API_BASE;
    }
  }

  getBaseUrl(): string {
    return this.baseUrl;
  }

  private headers(): HeadersInit {
    const h: HeadersInit = { 'Content-Type': 'application/json' };
    if (this.token) h['Authorization'] = `Bearer ${this.token}`;
    return h;
  }

  async request<T = any>(method: string, path: string, body?: any, params?: Record<string, string>): Promise<T> {
    let url = `${this.baseUrl}${path}`;
    if (params) {
      const qs = new URLSearchParams(params).toString();
      if (qs) url += `?${qs}`;
    }
    const res = await fetch(url, {
      method,
      headers: this.headers(),
      body: body ? JSON.stringify(body) : undefined,
    });
    const json = await res.json();
    if (!json.ok) {
      throw new ApiError(json.error || 'Unknown error', res.status);
    }
    return json.data as T;
  }

  get<T = any>(path: string, params?: Record<string, string>) { return this.request<T>('GET', path, undefined, params); }
  post<T = any>(path: string, body?: any) { return this.request<T>('POST', path, body); }
  patch<T = any>(path: string, body?: any) { return this.request<T>('PATCH', path, body); }
  delete<T = any>(path: string) { return this.request<T>('DELETE', path); }
}

export class ApiError extends Error {
  constructor(message: string, public status: number) {
    super(message);
    this.name = 'ApiError';
  }
}

export const api = new ApiClient();
export default api;
