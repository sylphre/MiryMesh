// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  server: {
    proxy: {
      '/api': 'http://127.0.0.1:9001',
      '/internal': 'http://127.0.0.1:9001',
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
})
