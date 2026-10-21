<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
<script setup>
import { ref, onMounted, computed } from 'vue'
import { useRouter } from 'vue-router'

const router = useRouter()
const dbConfigured = ref(false)
const mode = ref('standalone')
const hasDatanode = ref(false)
const step = ref(1)
const status = ref('読み込み中…')
const statusClass = ref('state--loading')
const adminUser = ref('')
const adminPass = ref('')
const accessKey = ref('')
const secretKey = ref('')
const region = ref('us-east-1')
const gateway = ref(false)
const defaultPolicy = ref('single')
const dbDriver = ref('')
const dbDsn = ref('')
const datanodePaths = ref('')

const isGateway = computed(() => mode.value === 'gateway')

onMounted(async () => {
  try {
    const token = localStorage.getItem('mirymesh_admin_token') || ''
    const headers = token ? { 'X-Admin-Token': token } : {}
    const res = await fetch('/api/admin/config', { headers })
    const j = await res.json()
    dbConfigured.value = !!j.db_configured
    mode.value = j.mode || 'standalone'
    gateway.value = j.mode === 'gateway'
    // Check hasDatanode via cluster
    if (token) {
      try {
        const cr = await fetch('/api/admin/cluster', { headers })
        if (cr.ok) {
          const cj = await cr.json()
          hasDatanode.value = (cj.nodes && cj.nodes.length > 0) || (cj.disks && cj.disks.length > 0)
        }
      } catch {}
    }
    // Check initialized
    const sr = await fetch('/api/admin/setup/status')
    const sj = await sr.json()
    if (sj.initialized) {
      status.value = '既に初期化済みです。ログインしてください。'
      step.value = 2
    } else {
      status.value = ''
      step.value = 1
    }
  } catch (e) {
    status.value = '状態取得に失敗: ' + e.message
    statusClass.value = 'state--error'
  }
})

async function submitStep1(e) {
  e.preventDefault()
  try {
    const res = await fetch('/api/admin/setup', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: adminUser.value, password: adminPass.value }),
    })
    if (!res.ok) throw new Error(await res.text())
    const j = await res.json()
    localStorage.setItem('mirymesh_admin_token', j.admin_token)
    status.value = 'Admin 作成完了。ログインしてください。'
    statusClass.value = 'state--success'
    step.value = 2
  } catch (err) {
    status.value = '失敗: ' + err.message
    statusClass.value = 'state--error'
  }
}

async function submitLogin(e) {
  e.preventDefault()
  try {
    const res = await fetch('/api/admin/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: adminUser.value, password: adminPass.value }),
    })
    if (!res.ok) throw new Error(await res.text())
    const j = await res.json()
    localStorage.setItem('mirymesh_admin_token', j.admin_token)
    // Re-check config for hasDatanode
    try {
      const headers = { 'X-Admin-Token': j.admin_token }
      const cr = await fetch('/api/admin/cluster', { headers })
      if (cr.ok) {
        const cj = await cr.json()
        hasDatanode.value = (cj.nodes && cj.nodes.length > 0)
      }
    } catch {}
    step.value = 3
    status.value = 'ログイン成功'
    statusClass.value = 'state--success'
  } catch (err) {
    status.value = 'ログイン失敗: ' + err.message
    statusClass.value = 'state--error'
  }
}

async function genCreds() {
  const token = localStorage.getItem('mirymesh_admin_token') || ''
  try {
    const r = await fetch('/api/admin/generate-credentials', { method: 'POST', headers: { 'X-Admin-Token': token } })
    if (r.ok) {
      const j = await r.json()
      if (j.access_key) accessKey.value = j.access_key
      if (j.secret_key) secretKey.value = j.secret_key
      return
    }
  } catch {}
  if (!accessKey.value) {
    const a = new Uint8Array(20)
    crypto.getRandomValues(a)
    accessKey.value = Array.from(a, b => 'ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789'[b % 36]).join('').slice(0, 20)
  }
  if (!secretKey.value) {
    const s = new Uint8Array(16)
    crypto.getRandomValues(s)
    secretKey.value = Array.from(s, b => ('0' + b.toString(16)).slice(-2)).join('').slice(0, 32)
  }
}

async function submitStep2(e) {
  e.preventDefault()
  const token = localStorage.getItem('mirymesh_admin_token') || ''
  const payload = {
    access_key: accessKey.value,
    secret_key: secretKey.value,
    default_policy: defaultPolicy.value,
    region: region.value,
    gateway: gateway.value,
    datanode_paths: datanodePaths.value,
    db_driver: dbDriver.value,
    db_dsn: dbDsn.value,
  }
  try {
    // Gateway with no datanode: allow empty S3 keys (skip)
    const res = await fetch('/api/admin/setup', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Admin-Token': token },
      body: JSON.stringify(payload),
    })
    if (!res.ok) {
      const r2 = await fetch('/api/admin/config/policy', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json', 'X-Admin-Token': token },
        body: JSON.stringify({ policy: defaultPolicy.value }),
      })
      if (!r2.ok) throw new Error(await r2.text())
    }
    step.value = 4
    status.value = '完了しました'
    statusClass.value = 'state--success'
  } catch (err) {
    status.value = '保存失敗: ' + err.message
    statusClass.value = 'state--error'
  }
}

async function skipS3Keys() {
  accessKey.value = ''
  secretKey.value = ''
  await submitStep2(new Event('submit'))
}
</script>

<template>
  <main class="container container--narrow">
    <div class="card" id="setup-card">
      <h2>初期設定</h2>
      <p class="caption" style="margin-top: 4px;">Admin 作成 → ログイン → S3 / DB / Storage の2段階で完了します。</p>
      <div class="stepper" style="margin-top: 16px;">
        <div :class="step >= 1 ? (step > 1 ? 'stepper__step stepper__step--done' : 'stepper__step stepper__step--active') : 'stepper__step'"></div>
        <div :class="step === 2 ? 'stepper__step stepper__step--active' : step > 2 ? 'stepper__step stepper__step--done' : 'stepper__step'"></div>
        <div :class="step === 3 ? 'stepper__step stepper__step--active' : step > 3 ? 'stepper__step stepper__step--done' : 'stepper__step'"></div>
      </div>
      <p :class="statusClass" v-if="status" style="margin-top: 12px;">{{ status }}</p>

      <!-- Step 1: Admin -->
      <form v-if="step === 1" @submit="submitStep1" class="form form--stack" style="margin-top: 16px;">
        <h3>Step 1 — Admin アカウント作成</h3>
        <label>Admin Username <input v-model="adminUser" class="input" required minlength="3" placeholder="admin" /></label>
        <label>Admin Password <input v-model="adminPass" class="input" type="password" required minlength="8" placeholder="8文字以上" /></label>
        <button type="submit" class="btn btn--primary">作成して次へ</button>
      </form>

      <!-- Step 2: Login -->
      <form v-if="step === 2" @submit="submitLogin" class="form form--stack" style="margin-top: 16px;">
        <h3>ログイン</h3>
        <p class="caption">Step 1 で作成したユーザーでログインしてください。</p>
        <label>Username <input v-model="adminUser" class="input" required /></label>
        <label>Password <input v-model="adminPass" class="input" type="password" required /></label>
        <button type="submit" class="btn btn--primary">ログイン</button>
      </form>

      <!-- Step 3: S3 / DB / Storage -->
      <form v-if="step === 3" @submit="submitStep2" class="form form--stack" style="margin-top: 16px;">
        <h3>Step 2 — S3 / DB / Storage</h3>
        <div style="display: grid; grid-template-columns: 1fr auto; gap: 8px; align-items: end;">
          <label>AccessKey <input v-model="accessKey" class="input" placeholder="TESTKEY" /></label>
          <button type="button" class="btn btn--sm" @click="genCreds" style="height: 40px;">生成</button>
        </div>
        <div style="display: grid; grid-template-columns: 1fr auto; gap: 8px; align-items: end;">
          <label>SecretKey <input v-model="secretKey" class="input" type="password" placeholder="8文字以上" /></label>
          <button type="button" class="btn btn--sm" @click="genCreds" style="height: 40px;">生成</button>
        </div>
        <p v-if="isGateway && !hasDatanode" class="caption" style="color: var(--color-warning);">Gateway で DataNode 未登録のため S3 Keys は後で作成できます。「スキップして完了」で進んでください。</p>
        <div class="grid grid--2" style="margin-top: 0;">
          <label>Region <input v-model="region" class="input" placeholder="us-east-1" /></label>
          <label>Gateway
            <select v-model="gateway" class="input" :disabled="isGateway">
              <option :value="false">standalone</option><option :value="true">gateway</option>
            </select>
            <span v-if="isGateway" class="caption">Gateway mode (from install.sh) — disabled</span>
          </label>
        </div>
        <label>Default Policy
          <select v-model="defaultPolicy" class="input">
            <option value="single">single</option>
            <option value="dual">dual</option>
            <option value="triple">triple</option>
            <option value="ALL">ALL</option>
          </select>
        </label>
        <!-- DB fields: v-if/v-else で DOM 除去 -->
        <template v-if="!dbConfigured">
          <div class="grid grid--2" style="margin-top: 0;">
            <label>DB Driver <select v-model="dbDriver" required><option value="">auto</option><option value="postgres">postgres</option><option value="mysql">mysql</option><option value="sqlite">sqlite</option></select></label>
            <label v-if="!isGateway">Datanode Paths <input v-model="datanodePaths" class="input" placeholder="/mnt/disk1,/mnt/disk2" required /></label>
          </div>
          <div>
            <label>DB DSN <input v-model="dbDsn" class="input" placeholder="postgres://..." required /></label>
            <p class="caption">DB が install.sh で作成済みなら非表示になります。</p>
          </div>
        </template>
        <template v-else-if="!isGateway">
          <div>
            <label>Datanode Paths <input v-model="datanodePaths" class="input" placeholder="/mnt/disk1,/mnt/disk2" /></label>
          </div>
        </template>
        <div style="display: flex; gap: 8px;">
          <button type="submit" class="btn btn--primary">保存して完了</button>
          <button type="button" class="btn" @click="skipS3Keys">スキップして完了</button>
        </div>
      </form>

      <div v-if="step === 4" style="margin-top: 16px;">
        <p class="state--success">セットアップが完了しました。ダッシュボードへ進んでください。</p>
        <p style="margin-top: 16px;"><router-link to="/" class="btn btn--primary">ダッシュボードへ</router-link></p>
      </div>
    </div>
  </main>
</template>
