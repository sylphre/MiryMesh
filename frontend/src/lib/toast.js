// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
import { reactive } from 'vue'

export const toasts = reactive([])
let seq = 0

export function dismissToast(id) {
  const i = toasts.findIndex((t) => t.id === id)
  if (i >= 0) toasts.splice(i, 1)
}

export function toast(message, kind = 'info', timeout = 4500) {
  const id = ++seq
  toasts.push({ id, message: String(message), kind })
  if (timeout > 0) setTimeout(() => dismissToast(id), timeout)
  return id
}

export const toastOk = (m) => toast(m, 'success')
export const toastErr = (m) => toast(m instanceof Error ? m.message : m, 'error', 8000)