// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
import { ref } from 'vue'

const KEY = 'mirymesh_theme'
export const theme = ref('dark')

function apply(t) {
  document.documentElement.dataset.theme = t
  theme.value = t
}

export function initTheme() {
  let t = null
  try { t = localStorage.getItem(KEY) } catch { /* storage unavailable */ }
  if (t !== 'light' && t !== 'dark') {
    const light = window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches
    t = light ? 'light' : 'dark'
  }
  apply(t)
}

export function toggleTheme() {
  const t = theme.value === 'dark' ? 'light' : 'dark'
  apply(t)
  try { localStorage.setItem(KEY, t) } catch { /* storage unavailable */ }
}