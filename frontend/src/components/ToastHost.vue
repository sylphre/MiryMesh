<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
<script setup>
import Icon from './Icon.vue'
import { toasts, dismissToast } from '../lib/toast.js'

const ICONS = { success: 'check', error: 'alert', info: 'info' }
</script>

<template>
  <div class="toasts" role="region" aria-label="通知" aria-live="polite">
    <div v-for="t in toasts" :key="t.id" class="toast" :class="`toast--${t.kind}`" :role="t.kind === 'error' ? 'alert' : 'status'">
      <Icon class="toast__icon" :name="ICONS[t.kind] || 'info'" />
      <span class="toast__msg">{{ t.message }}</span>
      <button type="button" class="icon-btn" aria-label="通知を閉じる" @click="dismissToast(t.id)"><Icon name="x" :size="14" /></button>
    </div>
  </div>
</template>