<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
<script setup>
import { ref, computed, watch } from 'vue'
import Modal from './Modal.vue'
import { dialog, closeDialog } from '../lib/dialog.js'

const typed = ref('')
const open = computed({
  get: () => !!dialog.value,
  set: (v) => { if (!v) closeDialog(false) },
})
const canConfirm = computed(() => !dialog.value || !dialog.value.requireText || typed.value === dialog.value.requireText)

watch(dialog, () => { typed.value = '' })

function confirm() {
  if (canConfirm.value) closeDialog(true)
}
</script>

<template>
  <Modal v-model="open" :title="dialog ? dialog.title : ''" role="alertdialog">
    <p class="confirm__msg">{{ dialog ? dialog.message : '' }}</p>
    <div v-if="dialog && dialog.requireText" class="field mt-2">
      <label class="field__label" for="confirm-text">確認のため <code>{{ dialog.requireText }}</code> を入力してください</label>
      <input id="confirm-text" v-model="typed" class="input input--mono" autocomplete="off" data-autofocus @keydown.enter.prevent="confirm" />
    </div>
    <template #footer>
      <button type="button" class="btn" :data-autofocus="dialog && dialog.requireText ? null : ''" @click="closeDialog(false)">{{ dialog ? dialog.cancelLabel : '' }}</button>
      <button type="button" class="btn" :class="dialog && dialog.danger ? 'btn--danger-solid' : 'btn--primary'" :disabled="!canConfirm" @click="confirm">{{ dialog ? dialog.confirmLabel : '' }}</button>
    </template>
  </Modal>
</template>