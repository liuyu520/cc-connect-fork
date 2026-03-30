import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'path'

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    port: 9821,
    host: '0.0.0.0',
    proxy: {
      // Vibe Coding WebSocket 代理
      '/api/vibe/ws': {
        target: 'http://localhost:9830',
        ws: true,
        changeOrigin: true,
      },
      // Vibe Coding REST API 代理（历史记录等）
      '/api/vibe': {
        target: 'http://localhost:9830',
        changeOrigin: true,
      },
      // Management API 代理
      '/api': {
        target: 'http://localhost:9820',
        changeOrigin: true,
      },
      '/bridge': {
        target: 'http://localhost:9810',
        changeOrigin: true,
        ws: true,
      },
    },
  },
})
