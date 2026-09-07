import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 构建产物直接输出到 Go embed 目录
export default defineConfig({
  plugins: [vue()],
  base: './',
  build: {
    outDir: '../internal/server/webdist',
    emptyOutDir: true,
    chunkSizeWarningLimit: 1500
  },
  server: {
    port: 5190,
    proxy: {
      '/api': 'http://127.0.0.1:8790'
    }
  }
})
