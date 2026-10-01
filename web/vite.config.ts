/// <reference types="vitest/config" />
import path from 'node:path'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { defineConfig, type Plugin } from 'vite'
import { writeFileSync } from 'node:fs'

// 开发模式下把后端接口代理到本机 hub，生产构建产物由 hub 通过 go:embed 提供
const hubTarget = process.env.PIMON_HUB_URL ?? 'http://127.0.0.1:31415'

// emptyOutDir 会清掉 dist 目录里受版本控制的占位文件，构建结束后补回
function keepDistPlaceholder(): Plugin {
  return {
    name: 'keep-dist-placeholder',
    apply: 'build',
    closeBundle() {
      writeFileSync(path.resolve(import.meta.dirname, '../src/internal/hub/webui/dist/.gitkeep'), '')
    },
  }
}

export default defineConfig({
  plugins: [react(), tailwindcss(), keepDistPlaceholder()],
  resolve: {
    alias: { '@': path.resolve(import.meta.dirname, './src') },
  },
  build: {
    outDir: path.resolve(import.meta.dirname, '../src/internal/hub/webui/dist'),
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/api': { target: hubTarget, changeOrigin: false },
      '/ws': { target: hubTarget, ws: true, changeOrigin: false },
      '/screen/auth': { target: hubTarget, changeOrigin: false },
    },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test-setup.ts'],
    include: ['src/**/*.test.{ts,tsx}', 'lint-fixtures/**/*.test.ts'],
    css: false,
  },
})
