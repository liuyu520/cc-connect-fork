import { create } from 'zustand';
import { api } from '@/api/client';

interface AuthState {
  token: string;
  serverUrl: string;
  isAuthenticated: boolean;
  login: (token: string, serverUrl?: string) => void;
  logout: () => void;
  init: () => void;
}

export const useAuthStore = create<AuthState>((set) => ({
  token: '',
  serverUrl: '',
  isAuthenticated: false,
  login: (token: string, serverUrl?: string) => {
    api.setToken(token);
    api.setBaseUrl(serverUrl || '');
    localStorage.setItem('cc_token', token);
    // 标记已通过认证（区分"从未登录"和"空 token 登录"）
    localStorage.setItem('cc_authenticated', '1');
    if (serverUrl) {
      localStorage.setItem('cc_server_url', serverUrl);
    } else {
      localStorage.removeItem('cc_server_url');
    }
    set({ token, serverUrl: serverUrl || '', isAuthenticated: true });
  },
  logout: () => {
    api.setToken('');
    api.setBaseUrl('');
    // 只清除认证标记，保留 token 和 serverUrl 以便下次登录自动填充
    localStorage.removeItem('cc_authenticated');
    set({ token: '', serverUrl: '', isAuthenticated: false });
  },
  init: () => {
    const token = localStorage.getItem('cc_token') || '';
    const serverUrl = localStorage.getItem('cc_server_url') || '';
    const authenticated = localStorage.getItem('cc_authenticated') === '1';
    if (authenticated) {
      api.setToken(token);
      api.setBaseUrl(serverUrl);
      set({ token, serverUrl, isAuthenticated: true });
    }
  },
}));
