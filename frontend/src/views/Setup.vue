<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
<script setup>
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import BrandMark from '../components/BrandMark.vue'
import Icon from '../components/Icon.vue'
import { useClusterStore } from '../stores/cluster'
import { api, getToken, setToken } from '../lib/api.js'
import { toastOk } from '../lib/toast.js'

const router = useRouter()
const store = useClusterStore()

const loading = ref(true)
const busy = ref(false)
const error = ref('')
const notice = ref('')

// 1: Admin 作成 / 2: ログイン / 3: S3 · DB · Storage
const step = ref(1)
const dbConfigured = ref(false)
const mode = ref('standalone')
const hasDatanode = ref(false)

const adminUser = ref('')
const adminPass = ref('')
const accessKey = ref('')
const secretKey = ref('')
const showSecret = ref(false)
const region = ref('us-east-1')
const gateway = ref(false)
const defaultPolicy = ref('single')
const dbDriver = ref('')
const dbDsn = ref('')
const datanodePaths = ref('')

const isGateway = computed(() => mode.value === 'gateway')

const stepLabels = ['Admin 作成', 'ログイン', 'S3 / DB / Storage']

const policies = [
  { value: 'single', title: 'single', desc: '1 ノードに保存' },
  { value: 'dual', title: 'dual', desc: '2 ノードに複製' },
  { value: 'triple', title: 'triple', desc: '3 ノードに複製' },
  { value: 'ALL', title: 'ALL', desc: '全ノードに複製' },
]

const drivers = [
  { value: '', title: 'auto', desc: '自動判定' },
  { value: 'postgres', title: 'postgres', desc: 'PostgreSQL' },
  { value: 'mysql', title: 'mysql', desc: 'MySQL / MariaDB' },
  { value: 'sqlite', title: 'sqlite', desc: '開発用' },
]

function fail(e) {
  error.value = e && e.message ? e.message : String(e)
}

async function loadCluster() {
  try {
    const cj = await api('/api/admin/cluster', { redirectOn401: false })
    hasDatanode.value = !!((cj.nodes && cj.nodes.length > 0) || (cj.disks && cj.disks.length > 0))
  } catch {
    hasDatanode.value = false
  }
}

onMounted(async () => {
  try {
    try {
      const cfg = await api('/api/admin/config', { redirectOn401: false })
      dbConfigured.value = !!cfg.db_configured
      mode.value = cfg.mode || 'standalone'
      gateway.value = cfg.mode === 'gateway'
    } catch {
      // 初期化済みで未ログインの場合は 401 になる。状態判定は下の status で行う。
    }
    if (getToken()) await loadCluster()

    const st = await api('/api/admin/setup/status', { auth: false })
    if (st.initialized) {
      notice.value = '既に初期化済みです。ログインして設定を続けてください。'
      step.value = 2
    } else {
      step.value = 1
    }
  } catch (e) {
    error.value = '状態の取得に失敗しました: ' + (e.message || e)
  } finally {
    loading.value = false
  }
})

async function submitStep1() {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    const j = await api('/api/admin/setup', {
      method: 'POST',
      body: { username: adminUser.value, password: adminPass.value },
      auth: false,
    })
    if (j && j.admin_token) setToken(j.admin_token)
    notice.value = 'Admin を作成しました。ログインしてください。'
    step.value = 2
  } catch (e) {
    fail(e)
  } finally {
    busy.value = false
  }
}

async function submitLogin() {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    const j = await api('/api/admin/login', {
      method: 'POST',
      body: { username: adminUser.value, password: adminPass.value },
      auth: false,
    })
    setToken(j.admin_token)
    await loadCluster()
    notice.value = ''
    step.value = 3
  } catch (e) {
    error.value = e.status === 401 || e.status === 403 ? 'ユーザー名またはパスワードが正しくありません' : e.message
  } finally {
    busy.value = false
  }
}

function randomString(len, alphabet) {
  const a = new Uint8Array(len)
  crypto.getRandomValues(a)
  return Array.from(a, (b) => alphabet[b % alphabet.length]).join('')
}

async function genCreds() {
  error.value = ''
  try {
    const j = await api('/api/admin/generate-credentials', { method: 'POST', redirectOn401: false })
    if (j && j.access_key) accessKey.value = j.access_key
    if (j && j.secret_key) secretKey.value = j.secret_key
    showSecret.value = true
    return
  } catch {
    // サーバー側で生成できない場合はブラウザの CSPRNG で生成する
  }
  accessKey.value = randomString(20, 'ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789')
  secretKey.value = randomString(40, 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789')
  showSecret.value = true
}

async function copy(text) {
  if (!text) return
  try {
    await navigator.clipboard.writeText(text)
    toastOk('コピーしました')
  } catch {
    error.value = 'クリップボードにコピーできませんでした'
  }
}

async function saveConfig() {
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
    await api('/api/admin/setup', { method: 'POST', body: payload })
  } catch (e) {
    // Gateway で DataNode が 0 台の場合はポリシー検証で弾かれるため、ポリシーのみで再試行する
    const text = e && e.message ? e.message : ''
    if (text.includes('requires') && text.includes('nodes')) {
      await api('/api/admin/setup', {
        method: 'POST',
        body: { default_policy: defaultPolicy.value, gateway: gateway.value, region: region.value },
      })
    } else {
      throw e
    }
  }
  store.initialized = true
  toastOk('セットアップが完了しました')
  await router.push('/')
}

async function submitStep3() {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    await saveConfig()
  } catch (e) {
    error.value = '保存に失敗しました: ' + (e.message || e)
  } finally {
    busy.value = false
  }
}

async function skipS3Keys() {
  accessKey.value = ''
  secretKey.value = ''
  await submitStep3()
}
</script>

<template>
  <div class="auth">
    <div class="auth__card" style="max-width: 600px; width: 100%">
      <div class="auth__brand">
        <BrandMark :size="36" />
        <div>
          <div class="brand__name">MiryMesh</div>
          <div class="brand__sub">Initial Setup</div>
        </div>
      </div>

      <h1 class="auth__title">初期設定</h1>
      <p class="muted small mt-1">Admin 作成 → ログイン → S3 / DB / Storage の順に設定します。</p>

      <ol class="steps mt-2" style="list-style: none; padding: 0; margin-bottom: 0" aria-label="進行状況">
        <template v-for="(label, i) in stepLabels" :key="label">
          <li class="step" :class="{ 'step--on': step >= i + 1 }" :aria-current="step === i + 1 ? 'step' : undefined">
            <span class="step__num">
              <Icon v-if="step > i + 1" name="check" :size="14" />
              <template v-else>{{ i + 1 }}</template>
            </span>
            <span>{{ label }}</span>
          </li>
          <li v-if="i < stepLabels.length - 1" class="step__bar" aria-hidden="true"></li>
        </template>
      </ol>

      <div v-if="error" class="alert alert--error mt-3" role="alert">
        <Icon name="alert" />
        <div class="alert__body">{{ error }}</div>
      </div>
      <p v-else-if="notice" class="muted small mt-3" role="status">{{ notice }}</p>

      <p v-if="loading" class="muted mt-3">読み込み中…</p>

      <!-- Step 1: Admin -->
      <form v-else-if="step === 1" class="stack mt-3" @submit.prevent="submitStep1">
        <h2 class="card__title">Admin アカウント作成</h2>
        <label class="field">
          <span class="field__label">Username</span>
          <input v-model="adminUser" class="input" required minlength="3" autocomplete="username" autofocus placeholder="admin" />
        </label>
        <label class="field">
          <span class="field__label">Password</span>
          <input v-model="adminPass" type="password" class="input" required minlength="8" autocomplete="new-password" placeholder="8 文字以上" />
        </label>
        <button type="submit" class="btn btn--primary btn--block" :disabled="busy">
          <span v-if="busy" class="spinner" aria-hidden="true"></span>
          作成して次へ
        </button>
      </form>

      <!-- Step 2: Login -->
      <form v-else-if="step === 2" class="stack mt-3" @submit.prevent="submitLogin">
        <h2 class="card__title">ログイン</h2>
        <label class="field">
          <span class="field__label">Username</span>
          <input v-model="adminUser" class="input" required autocomplete="username" autofocus />
        </label>
        <label class="field">
          <span class="field__label">Password</span>
          <input v-model="adminPass" type="password" class="input" required autocomplete="current-password" />
        </label>
        <button type="submit" class="btn btn--primary btn--block" :disabled="busy">
          <span v-if="busy" class="spinner" aria-hidden="true"></span>
          ログイン
        </button>
      </form>

      <!-- Step 3: S3 / DB / Storage -->
      <form v-else class="stack stack--lg mt-3" @submit.prevent="submitStep3">
        <section class="stack">
          <h2 class="card__title">S3 認証情報</h2>
          <p v-if="isGateway && !hasDatanode" class="muted small">
            Gateway で DataNode が未登録のため、S3 Keys は後から作成できます。「スキップして完了」で進んでください。
          </p>
          <label class="field">
            <span class="field__label">Access Key</span>
            <span class="input-group">
              <input v-model="accessKey" class="input input--mono" autocomplete="off" spellcheck="false" placeholder="未入力なら後で作成" />
              <button type="button" class="icon-btn" aria-label="Access Key をコピー" :disabled="!accessKey" @click="copy(accessKey)">
                <Icon name="copy" />
              </button>
            </span>
          </label>
          <label class="field">
            <span class="field__label">Secret Key</span>
            <span class="input-group">
              <input
                v-model="secretKey"
                :type="showSecret ? 'text' : 'password'"
                class="input input--mono"
                autocomplete="new-password"
                spellcheck="false"
                placeholder="8 文字以上"
              />
              <button type="button" class="btn" @click="showSecret = !showSecret">{{ showSecret ? '隠す' : '表示' }}</button>
              <button type="button" class="icon-btn" aria-label="Secret Key をコピー" :disabled="!secretKey" @click="copy(secretKey)">
                <Icon name="copy" />
              </button>
            </span>
          </label>
          <div>
            <button type="button" class="btn btn--sm" @click="genCreds"><Icon name="key" :size="14" />自動生成</button>
          </div>
        </section>

        <section class="stack">
          <h2 class="card__title">Storage</h2>
          <div class="grid grid--2">
            <label class="field">
              <span class="field__label">Region</span>
              <input v-model="region" class="input" placeholder="us-east-1" />
            </label>
            <label class="field">
              <span class="field__label">Mode</span>
              <select v-model="gateway" class="input" :disabled="isGateway">
                <option :value="false">standalone</option>
                <option :value="true">gateway</option>
              </select>
              <span v-if="isGateway" class="muted small">install.sh で Gateway が指定されています</span>
            </label>
          </div>

          <fieldset class="field" style="border: 0; padding: 0; margin: 0; min-width: 0">
            <legend class="field__label">Default Policy</legend>
            <div class="choices">
              <label v-for="p in policies" :key="p.value" class="choice" :class="{ 'choice--on': defaultPolicy === p.value }">
                <input v-model="defaultPolicy" type="radio" name="default-policy" :value="p.value" />
                <span class="choice__title">{{ p.title }}</span>
                <span class="choice__desc">{{ p.desc }}</span>
              </label>
            </div>
          </fieldset>

          <label v-if="!isGateway" class="field">
            <span class="field__label">Datanode Paths</span>
            <input
              v-model="datanodePaths"
              class="input input--mono"
              placeholder="/mnt/disk1,/mnt/disk2"
              :required="!dbConfigured"
            />
            <span class="muted small">カンマ区切りで複数指定できます</span>
          </label>
        </section>

        <section v-if="!dbConfigured" class="stack">
          <h2 class="card__title">Database</h2>
          <fieldset class="field" style="border: 0; padding: 0; margin: 0; min-width: 0">
            <legend class="field__label">Driver</legend>
            <div class="choices">
              <label v-for="d in drivers" :key="d.title" class="choice" :class="{ 'choice--on': dbDriver === d.value }">
                <input v-model="dbDriver" type="radio" name="db-driver" :value="d.value" />
                <span class="choice__title">{{ d.title }}</span>
                <span class="choice__desc">{{ d.desc }}</span>
              </label>
            </div>
          </fieldset>
          <label class="field">
            <span class="field__label">DSN</span>
            <input v-model="dbDsn" class="input input--mono" required spellcheck="false" placeholder="postgres://user:pass@host:5432/mirymesh" />
          </label>
        </section>

        <div class="row">
          <button type="submit" class="btn btn--primary" :disabled="busy">
            <span v-if="busy" class="spinner" aria-hidden="true"></span>
            保存して完了
          </button>
          <button type="button" class="btn" :disabled="busy" @click="skipS3Keys">スキップして完了</button>
        </div>
      </form>
    </div>
  </div>
</template>