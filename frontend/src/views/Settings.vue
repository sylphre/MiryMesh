<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
<script setup>
import { ref, computed, onMounted } from 'vue'
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

// Update notice: which releases the banner offers. Stored on the Master, applies at once.
const CHANNELS = [
  { value: 'release', title: '安定版のみ', desc: 'Release だけ通知' },
  { value: 'prerelease', title: 'プレリリースも', desc: 'rc などの Pre-release も通知' },
  { value: 'auto', title: 'インストールに従う', desc: 'install.sh のチャンネルと現在のビルドで決定' },
]
const notify = ref('auto')
const notifyLoading = ref(true)
const notifySaving = ref(false)
const updateInfo = ref(null)

async function loadNotify() {
  try {
    const j = await api('/api/admin/update', { redirectOn401: false })
    updateInfo.value = j
    notify.value = (j && j.notify_channel) || 'auto'
  } catch (e) {
    toastErr(e)
  } finally {
    notifyLoading.value = false
  }
}

async function saveNotify() {
  notifySaving.value = true
  try {
    const j = await api('/api/admin/update/settings', { method: 'PUT', body: { notify_channel: notify.value } })
    updateInfo.value = j
    notify.value = (j && j.notify_channel) || notify.value
    window.dispatchEvent(new Event('mirymesh:update-changed'))
    toastOk('更新のお知らせの設定を保存しました')
  } catch (e) {
    toastErr(e)
  } finally {
    notifySaving.value = false
  }
}

onMounted(loadNotify)

// S3 region: reported in X-Amz-Bucket-Region and GetBucketLocation. Stored on the Master, applies at once.
const REGION_PRESETS = [
  'us-east-1',
  'us-east-2',
  'us-west-1',
  'us-west-2',
  'ap-northeast-1',
  'ap-northeast-2',
  'ap-northeast-3',
  'ap-southeast-1',
  'ap-southeast-2',
  'ap-south-1',
  'eu-central-1',
  'eu-west-1',
  'eu-west-2',
  'eu-north-1',
  'ca-central-1',
  'sa-east-1',
]
const REGION_RE = /^[a-z0-9-]{1,64}$/
const region = ref('us-east-1')
const regionSaved = ref('us-east-1')
const regionDefault = ref('us-east-1')
const regionPersisted = ref(true)
const regionLoading = ref(true)
const regionSaving = ref(false)
const regionValid = computed(() => REGION_RE.test(region.value.trim()))
const regionDirty = computed(() => region.value.trim() !== regionSaved.value)

function applyRegion(j) {
  regionSaved.value = (j && j.region) || regionSaved.value
  region.value = regionSaved.value
  regionDefault.value = (j && j.default) || regionDefault.value
  regionPersisted.value = !j || j.persisted !== false
}

async function loadRegion() {
  try {
    applyRegion(await api('/api/admin/region'))
  } catch (e) {
    toastErr(e)
  } finally {
    regionLoading.value = false
  }
}

async function saveRegion() {
  if (!regionValid.value) return
  regionSaving.value = true
  try {
    applyRegion(await api('/api/admin/region', { method: 'PUT', body: { region: region.value.trim() } }))
    toastOk('リージョンを保存しました')
  } catch (e) {
    toastErr(e)
  } finally {
    regionSaving.value = false
  }
}

onMounted(loadRegion)

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

    <section class="card" aria-labelledby="upd-title">
      <div class="card__head"><h2 id="upd-title" class="card__title">更新のお知らせ</h2></div>
      <div class="card__body stack">
        <p class="muted">画面上部のお知らせに、どのリリースを出すかを選びます。DataNode の更新先は変わりません。</p>
        <div v-if="notifyLoading" class="skeleton" style="height: 64px"></div>
        <template v-else>
          <fieldset style="border: 0; margin: 0; padding: 0">
            <legend class="sr-only">更新のお知らせの対象</legend>
            <div class="choices">
              <label v-for="c in CHANNELS" :key="c.value" class="choice" :class="{ 'choice--on': notify === c.value }">
                <input v-model="notify" type="radio" name="notify-channel" :value="c.value" />
                <span class="choice__title">{{ c.title }}</span>
                <span class="choice__desc">{{ c.desc }}</span>
              </label>
            </div>
          </fieldset>
          <p v-if="updateInfo" class="muted">
            現在 {{ updateInfo.current || '-' }}
            <template v-if="updateInfo.latest"> / 最新 {{ updateInfo.latest }}</template>
            ・プレリリース: {{ updateInfo.include_prerelease ? '通知する' : '通知しない' }}
          </p>
          <p v-if="updateInfo && updateInfo.enabled === false" class="muted">
            更新の確認は環境変数 MIRYMESH_UPDATE_CHECK で無効になっているため、設定は保存されますが通知は出ません。
          </p>
        </template>
      </div>
      <div class="card__foot" style="justify-content: flex-end">
        <button type="button" class="btn btn--primary" :disabled="notifyLoading || notifySaving" @click="saveNotify">
          <span v-if="notifySaving" class="spinner" aria-hidden="true"></span>Save
        </button>
      </div>
    </section>

    <section class="card" aria-labelledby="region-title">
      <div class="card__head"><h2 id="region-title" class="card__title">S3 リージョン</h2></div>
      <div class="card__body stack">
        <p class="muted">
          S3 API が <code>X-Amz-Bucket-Region</code> と GetBucketLocation で返すリージョンです。保存するとすぐに反映され、再起動は不要です。
          署名 (SigV4) はクライアントが指定したリージョンのまま受け付けるため、既存のクライアントは止まりません。
          クライアント側にも同じリージョンを設定してください。
        </p>
        <div v-if="regionLoading" class="skeleton" style="height: 64px"></div>
        <template v-else>
          <div class="field">
            <label class="field__label" for="region-input">リージョン</label>
            <input
              id="region-input"
              v-model="region"
              class="input input--mono"
              list="region-presets"
              maxlength="64"
              autocomplete="off"
              spellcheck="false"
              :aria-invalid="!regionValid"
              placeholder="us-east-1"
              @keyup.enter="saveRegion"
            />
            <datalist id="region-presets">
              <option v-for="r in REGION_PRESETS" :key="r" :value="r"></option>
            </datalist>
          </div>
          <p v-if="!regionValid" class="muted" role="alert">英小文字・数字・ハイフンのみ、64 文字以内で入力してください。</p>
          <p class="muted">
            現在 {{ regionSaved }}
            <template v-if="regionSaved !== regionDefault"> (既定: {{ regionDefault }})</template>
          </p>
          <p v-if="!regionPersisted" class="muted">データベースが未接続のため、この設定は再起動すると元に戻ります。</p>
        </template>
      </div>
      <div class="card__foot" style="justify-content: flex-end">
        <button
          type="button"
          class="btn"
          :disabled="regionLoading || regionSaving || region.trim() === regionDefault"
          @click="region = regionDefault"
        >
          既定に戻す
        </button>
        <button
          type="button"
          class="btn btn--primary"
          :disabled="regionLoading || regionSaving || !regionValid || !regionDirty"
          @click="saveRegion"
        >
          <span v-if="regionSaving" class="spinner" aria-hidden="true"></span>Save
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