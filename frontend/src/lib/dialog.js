// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
import { ref } from 'vue'

// Single global confirm dialog, rendered by <ConfirmHost/> in App.vue.
export const dialog = ref(null)

export function closeDialog(result) {
  const d = dialog.value
  dialog.value = null
  if (d) d.resolve(result)
}

/**
 * confirmDialog({ title, message, confirmLabel, cancelLabel, danger, requireText }) -> Promise<boolean>
 * requireText: the user must type this exact text before the confirm button is enabled.
 */
export function confirmDialog(opts = {}) {
  if (dialog.value) closeDialog(false)
  return new Promise((resolve) => {
    dialog.value = {
      title: '確認',
      message: '',
      confirmLabel: 'OK',
      cancelLabel: 'キャンセル',
      danger: false,
      requireText: '',
      ...opts,
      resolve,
    }
  })
}