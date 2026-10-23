<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
<script setup>
import { ref, watch, nextTick, onBeforeUnmount } from 'vue'
import Icon from './Icon.vue'

const props = defineProps({
  modelValue: { type: Boolean, default: false },
  title: { type: String, default: '' },
  wide: { type: Boolean, default: false },
  role: { type: String, default: 'dialog' },
})
const emit = defineEmits(['update:modelValue', 'close'])

const panel = ref(null)
const titleId = `modal-title-${Math.random().toString(36).slice(2, 8)}`
let lastFocus = null

function close() {
  emit('update:modelValue', false)
  emit('close')
}

function focusables() {
  if (!panel.value) return []
  const sel = 'a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"])'
  return Array.from(panel.value.querySelectorAll(sel)).filter((el) => !el.hasAttribute('hidden'))
}

function onKey(e) {
  if (e.key === 'Escape') {
    e.stopPropagation()
    close()
    return
  }
  if (e.key !== 'Tab') return
  const f = focusables()
  if (!f.length) {
    e.preventDefault()
    return
  }
  const first = f[0]
  const last = f[f.length - 1]
  if (e.shiftKey && document.activeElement === first) {
    e.preventDefault()
    last.focus()
  } else if (!e.shiftKey && document.activeElement === last) {
    e.preventDefault()
    first.focus()
  }
}

watch(
  () => props.modelValue,
  async (open) => {
    if (open) {
      lastFocus = document.activeElement
      document.body.style.overflow = 'hidden'
      await nextTick()
      const target = (panel.value && panel.value.querySelector('[data-autofocus]')) || focusables()[0] || panel.value
      if (target) target.focus()
    } else {
      document.body.style.overflow = ''
      if (lastFocus && typeof lastFocus.focus === 'function') lastFocus.focus()
      lastFocus = null
    }
  },
  { immediate: true },
)

onBeforeUnmount(() => {
  document.body.style.overflow = ''
})
</script>

<template>
  <Teleport to="body">
    <div v-if="modelValue" class="modal-backdrop" @mousedown.self="close">
      <div
        ref="panel"
        class="modal"
        :class="{ 'modal--wide': wide }"
        :role="role"
        aria-modal="true"
        :aria-labelledby="titleId"
        tabindex="-1"
        @keydown="onKey"
      >
        <div class="modal__head">
          <h3 :id="titleId">{{ title }}</h3>
          <button type="button" class="icon-btn" aria-label="閉じる" @click="close"><Icon name="x" /></button>
        </div>
        <div class="modal__body"><slot /></div>
        <div v-if="$slots.footer" class="modal__foot"><slot name="footer" /></div>
      </div>
    </div>
  </Teleport>
</template>