<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
<script setup>
import { ref, onMounted } from 'vue'
import Icon from '../components/Icon.vue'
import { api } from '../lib/api.js'
import { copyText } from '../lib/clipboard.js'
import { confirmDialog } from '../lib/dialog.js'
import { toastOk, toastErr } from '../lib/toast.js'

const policy = ref('single')
const loading = ref(true)
const saving = ref(false)
const generating = ref(false)
const creds = ref(null)

const POLICIES = [
  { value: 'single', title: 'single', desc: '1 台に保存' },
  { value: 'dual', title: 'dual', desc: '2 台にレプリカ' },
  { value: 'triple', title: 'triple', desc: '3 台にレプリカ' },
  { value: 'ALL', title: 'ALL', desc: '全ノードに保存' },
]

onMounted(async () => {
  try {
    const j = await api('/api/admin/config')
    policy.value = j.default_policy || 'single'
  } catch (e) {
    toastErr(e)
  } finally {
    loading.value = false
  }
})

async function save() {
  saving.value = true
  try {
    await api('/api/admin/config/policy', { method: 'PUT', body: { policy: policy.value } })
    toastOk('デフォルトポリシーを保存しました')
  } catch (e) {
    toastErr(e)
  } finally {
    saving.value = false
  }
}

async function genCreds() {
  const ok = await confirmDialog({
    title: 'S3 クレデンシャルを生成',
    message: '新しい Access Key / Secret Key を生成します。\n現在のキーを使っているクライアントは影響を受ける可能性があります。',
    confirmLabel: '生成する',
    danger: true,
  })
  if (!ok) return
  generating.value = true
  try {
    const j = await api('/api/admin/generate-credentials', { method: 'POST' })
    creds.value = { accessKey: j.access_key, secretKey: j.secret_key }
    toastOk('クレデンシャルを生成しました')
  } catch (e) {
    toastErr(e)
  } finally {
    generating.value = false
  }
}
</script>

<template>
  <div class="stack stack--lg">
    <div class="page-head" style="margin-bottom: 0">
      <div>
        <h1 class="page-head__title">Settings</h1>
        <p class="page-head__desc">配置ポリシーと S3 クレデンシャルを管理します。</p>
      </div>
    </div>

    <section class="card" aria-labelledby="pol-title">
      <div class="card__head"><h2 id="pol-title" class="card__title">Default placement policy</h2></div>
      <div class="card__body">
        <div v-if="loading" class="skeleton" style="height: 64px"></div>
        <fieldset v-else style="border: 0; margin: 0; padding: 0">
          <legend class="sr-only">デフォルトの配置ポリシー</legend>
          <div class="choices">
            <label v-for="p in POLICIES" :key="p.value" class="choice" :class="{ 'choice--on': policy === p.value }">
              <input v-model="policy" type="radio" name="policy" :value="p.value" />
              <span class="choice__title">{{ p.title }}</span>
              <span class="choice__desc">{{ p.desc }}</span>
            </label>
          </div>
        </fieldset>
      </div>
      <div class="card__foot" style="justify-content: flex-end">
        <button type="button" class="btn btn--primary" :disabled="loading || saving" @click="save">
          <span v-if="saving" class="spinner" aria-hidden="true"></span>Save
        </button>
      </div>
    </section>

    <section class="card" aria-labelledby="cred-title">
      <div class="card__head"><h2 id="cred-title" class="card__title">S3 credentials</h2></div>
      <div class="card__body stack">
        <p class="muted">S3 クライアント（aws cli / rclone など）で使う Access Key と Secret Key を生成します。</p>
        <div v-if="creds" class="stack">
          <div class="field">
            <span class="field__label">Access Key</span>
            <div class="input-group">
              <input class="input input--mono" readonly :value="creds.accessKey" aria-label="Access Key" />
              <button type="button" class="btn" @click="copyText(creds.accessKey)"><Icon name="copy" :size="16" />Copy</button>
            </div>
          </div>
          <div class="field">
            <span class="field__label">Secret Key</span>
            <div class="input-group">
              <input class="input input--mono" readonly :value="creds.secretKey" aria-label="Secret Key" />
              <button type="button" class="btn" @click="copyText(creds.secretKey)"><Icon name="copy" :size="16" />Copy</button>
            </div>
          </div>
          <div class="alert alert--warning">
            <Icon name="alert" />
            <div class="alert__body">この画面を離れると Secret Key は再表示されません。安全な場所に保存してください。</div>
          </div>
        </div>
      </div>
      <div class="card__foot" style="justify-content: flex-end">
        <button type="button" class="btn" :disabled="generating" @click="genCreds">
          <span v-if="generating" class="spinner" aria-hidden="true"></span>
          <Icon v-else name="key" :size="16" />Generate credentials
        </button>
      </div>
    </section>
  </div>
</template>