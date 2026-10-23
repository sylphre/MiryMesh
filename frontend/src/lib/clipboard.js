// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
import { toastOk, toastErr } from './toast.js'

/** Copy text to the clipboard (falls back to execCommand on insecure origins). */
export async function copyText(text, message = 'コピーしました') {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text)
    } else {
      const ta = document.createElement('textarea')
      ta.value = text
      ta.setAttribute('readonly', '')
      ta.style.position = 'fixed'
      ta.style.opacity = '0'
      document.body.appendChild(ta)
      ta.select()
      const ok = document.execCommand('copy')
      ta.remove()
      if (!ok) throw new Error('copy failed')
    }
    toastOk(message)
    return true
  } catch {
    toastErr('コピーに失敗しました')
    return false
  }
}