<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
<script setup>
import { ref, computed, onMounted } from 'vue'
import Icon from '../components/Icon.vue'
import Modal from '../components/Modal.vue'
import { api } from '../lib/api.js'
import { copyText } from '../lib/clipboard.js'
import { toastOk, toastErr } from '../lib/toast.js'
import { confirmDialog } from '../lib/dialog.js'

const ALL = '*'
const PERMS = [
  { value: 'read', label: 'Read（読み取り）' },
  { value: 'write', label: 'Write（書き込み）' },
  { value: 'readwrite', label: 'Read / Write（読み書き）' },
]
const PERM_LABEL = { read: 'Read', write: 'Write', readwrite: 'Read/Write' }

const users = ref([])
const buckets = ref([])
const loading = ref(true)
const error = ref('')
const filter = ref('')

const filtered = computed(() => {
  const q = filter.value.trim().toLowerCase()
  return q ? users.value.filter((u) => u.name.toLowerCase().includes(q)) : users.value
})

async function load() {
  try {
    const [u, b] = await Promise.all([api('/api/admin/users'), api('/api/admin/buckets')])
    users.value = (u && u.users) || []
    buckets.value = ((b && b.buckets) || []).slice().sort()
    error.value = ''
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

onMounted(load)

// ---- create / edit permissions -------------------------------------------------
const showForm = ref(false)
const form = ref({ mode: 'create', name: '', rows: [] })
const saving = ref(false)
const formError = ref('')

const bucketOptions = computed(() => {
  const set = new Set([ALL, ...buckets.value])
  for (const r of form.value.rows) if (r.bucket) set.add(r.bucket)
  return Array.from(set)
})

function openCreate() {
  form.value = { mode: 'create', name: '', rows: [{ bucket: '', permission: 'readwrite' }] }
  formError.value = ''
  showForm.value = true
}

function openEdit(u) {
  const rows = (u.permissions || []).map((g) => ({ bucket: g.bucket, permission: g.permission }))
  form.value = { mode: 'edit', name: u.name, rows }
  formError.value = ''
  showForm.value = true
}

function addRow() {
  form.value.rows.push({ bucket: '', permission: 'read' })
}

function removeRow(i) {
  form.value.rows.splice(i, 1)
}

function collectGrants() {
  const seen = new Set()
  const grants = []
  for (const r of form.value.rows) {
    if (!r.bucket) continue
    if (seen.has(r.bucket)) throw new Error(`Bucket「${r.bucket === ALL ? 'すべての Bucket' : r.bucket}」が重複しています`)
    seen.add(r.bucket)
    grants.push({ bucket: r.bucket, permission: r.permission })
  }
  return grants
}

async function submit() {
  if (saving.value) return
  formError.value = ''
  let grants
  try {
    grants = collectGrants()
  } catch (e) {
    formError.value = e.message
    return
  }
  saving.value = true
  try {
    if (form.value.mode === 'create') {
      const name = form.value.name.trim()
      const res = await api('/api/admin/users', { method: 'POST', body: { name, permissions: grants } })
      showForm.value = false
      showSecret(res, `User「${name}」を作成しました`)
    } else {
      await api(`/api/admin/users/${encodeURIComponent(form.value.name)}/permissions`, {
        method: 'PUT',
        body: { permissions: grants },
      })
      showForm.value = false
      toastOk(`User「${form.value.name}」の権限を更新しました`)
    }
    await load()
  } catch (e) {
    formError.value = e.message
  } finally {
    saving.value = false
  }
}

// ---- one-time secret key display --------------------------------------------------
const showKey = ref(false)
const keyInfo = ref({ title: '', accessKey: '', secretKey: '' })

function showSecret(res, title) {
  keyInfo.value = {
    title,
    accessKey: (res && res.user && res.user.access_key) || '',
    secretKey: (res && res.secret_key) || '',
  }
  showKey.value = true
}

function closeKey() {
  showKey.value = false
  keyInfo.value = { title: '', accessKey: '', secretKey: '' }
}

// ---- row actions ------------------------------------------------------------------
async function toggle(u) {
  try {
    await api(`/api/admin/users/${encodeURIComponent(u.name)}`, { method: 'PATCH', body: { enabled: !u.enabled } })
    toastOk(`User「${u.name}」を${u.enabled ? '無効' : '有効'}にしました`)
    await load()
  } catch (e) {
    toastErr(e)
  }
}

async function rotate(u) {
  const ok = await confirmDialog({
    title: 'キーを再発行',
    message: `User「${u.name}」の Access Key / Secret Key を再発行します。現在のキーはただちに使えなくなります。`,
    confirmLabel: '再発行',
    danger: true,
  })
  if (!ok) return
  try {
    const res = await api(`/api/admin/users/${encodeURIComponent(u.name)}/rotate-key`, { method: 'POST' })
    showSecret(res, `User「${u.name}」のキーを再発行しました`)
    await load()
  } catch (e) {
    toastErr(e)
  }
}

async function remove(u) {
  const ok = await confirmDialog({
    title: 'User を削除',
    message: `User「${u.name}」を削除します。キーはただちに使えなくなり、この操作は取り消せません。`,
    confirmLabel: '削除',
    danger: true,
    requireText: u.name,
  })
  if (!ok) return
  try {
    await api(`/api/admin/users/${encodeURIComponent(u.name)}`, { method: 'DELETE' })
    toastOk(`User「${u.name}」を削除しました`)
    await load()
  } catch (e) {
    toastErr(e)
  }
}

function bucketLabel(b) {
  return b === ALL ? 'すべての Bucket' : b
}
</script>

<template>
  <div class="stack stack--lg">
    <div class="page-head" style="margin-bottom: 0">
      <div>
        <h1 class="page-head__title">Users</h1>
        <p class="page-head__desc">S3 API 用のユーザーを管理します。User ごとに Access Key / Secret Key を発行し、Bucket 単位で読み書きの権限を付与できます。</p>
      </div>
      <div class="page-head__actions">
        <button type="button" class="btn" @click="load"><Icon name="refresh" :size="16" />Refresh</button>
        <button type="button" class="btn btn--primary" @click="openCreate"><Icon name="plus" :size="16" />User を作成</button>
      </div>
    </div>

    <div v-if="error" class="alert alert--error" role="alert">
      <Icon name="alert" /><div class="alert__body">{{ error }}</div>
    </div>

    <section class="card">
      <div v-if="loading" class="card__body" aria-busy="true"><div class="skeleton" style="height: 120px"></div></div>

      <div v-else-if="users.length === 0" class="empty">
        <span class="empty__icon"><Icon name="users" :size="22" /></span>
        <span class="empty__title">User がありません</span>
        <p>User を作成すると、Bucket ごとに権限を絞った S3 キーを発行できます。</p>
        <button type="button" class="btn btn--primary" @click="openCreate"><Icon name="plus" :size="16" />User を作成</button>
      </div>

      <template v-else>
        <div class="toolbar">
          <div class="input-group" style="flex: 1; max-width: 320px">
            <input v-model="filter" class="input" type="search" placeholder="User を検索" aria-label="User を検索" />
          </div>
          <span class="toolbar__grow"></span>
          <span class="muted small">{{ filtered.length }} / {{ users.length }} 件</span>
        </div>

        <div v-if="filtered.length === 0" class="empty">
          <span class="empty__icon"><Icon name="search" :size="22" /></span>
          <span class="empty__title">該当する User がありません</span>
        </div>

        <div v-else class="table-wrap">
          <table class="table">
            <thead>
              <tr>
                <th>Name</th>
                <th>Access Key</th>
                <th>Permissions</th>
                <th>Status</th>
                <th class="actions"><span class="sr-only">操作</span></th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="u in filtered" :key="u.name">
                <td>
                  <div class="cell-main">
                    <span class="ico-folder"><Icon name="users" :size="18" /></span>
                    <strong>{{ u.name }}</strong>
                  </div>
                </td>
                <td>
                  <div class="input-group">
                    <code>{{ u.access_key }}</code>
                    <button type="button" class="icon-btn" :aria-label="`${u.name} の Access Key をコピー`" @click="copyText(u.access_key)">
                      <Icon name="copy" :size="16" />
                    </button>
                  </div>
                </td>
                <td>
                  <span v-if="!u.permissions || u.permissions.length === 0" class="muted small">権限なし</span>
                  <div v-else style="display: flex; flex-wrap: wrap; gap: 4px">
                    <span v-for="g in u.permissions" :key="g.bucket" class="badge badge--policy">
                      {{ bucketLabel(g.bucket) }}: {{ PERM_LABEL[g.permission] || g.permission }}
                    </span>
                  </div>
                </td>
                <td><span class="badge">{{ u.enabled ? '有効' : '無効' }}</span></td>
                <td class="actions">
                  <button type="button" class="btn btn--sm" @click="openEdit(u)">権限</button>
                  <button type="button" class="btn btn--sm" @click="toggle(u)">{{ u.enabled ? '無効化' : '有効化' }}</button>
                  <button type="button" class="icon-btn" :aria-label="`${u.name} のキーを再発行`" @click="rotate(u)">
                    <Icon name="key" :size="16" />
                  </button>
                  <button type="button" class="icon-btn icon-btn--danger" :aria-label="`${u.name} を削除`" @click="remove(u)">
                    <Icon name="trash" :size="16" />
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </template>
    </section>

    <Modal v-model="showForm" :title="form.mode === 'create' ? 'User を作成' : `権限を編集: ${form.name}`" wide>
      <form id="user-form" class="stack" @submit.prevent="submit">
        <div v-if="formError" class="alert alert--error" role="alert">
          <Icon name="alert" /><div class="alert__body">{{ formError }}</div>
        </div>

        <label v-if="form.mode === 'create'" class="field">
          <span class="field__label">User 名</span>
          <input
            v-model="form.name"
            class="input input--mono"
            required
            maxlength="64"
            pattern="[A-Za-z0-9._\-]{1,64}"
            autocomplete="off"
            spellcheck="false"
            placeholder="alice"
            data-autofocus
          />
          <span class="field__hint">1〜64 文字の英数字、ピリオド、アンダースコア、ハイフンが使えます。</span>
        </label>

        <fieldset class="stack" style="border: 0; margin: 0; padding: 0">
          <legend class="field__label">Bucket ごとの権限</legend>
          <p class="field__hint">
            「すべての Bucket」を選ぶと、今後作成する Bucket にも適用されます。Bucket の作成・削除や ACL / Policy などの設定変更は、User には許可されません。
          </p>
          <div v-if="form.rows.length === 0" class="muted small">権限がありません。このままでは何もアクセスできません。</div>
          <div v-for="(row, i) in form.rows" :key="i" class="input-group" style="gap: 8px; flex-wrap: wrap">
            <select v-model="row.bucket" class="input" style="flex: 2; min-width: 160px" :aria-label="`権限 ${i + 1} の Bucket`">
              <option value="" disabled>Bucket を選択</option>
              <option v-for="b in bucketOptions" :key="b" :value="b">{{ bucketLabel(b) }}</option>
            </select>
            <select v-model="row.permission" class="input" style="flex: 1; min-width: 160px" :aria-label="`権限 ${i + 1} の種類`">
              <option v-for="p in PERMS" :key="p.value" :value="p.value">{{ p.label }}</option>
            </select>
            <button type="button" class="icon-btn icon-btn--danger" :aria-label="`権限 ${i + 1} を削除`" @click="removeRow(i)">
              <Icon name="trash" :size="16" />
            </button>
          </div>
          <div><button type="button" class="btn btn--sm" @click="addRow"><Icon name="plus" :size="16" />権限を追加</button></div>
        </fieldset>
      </form>
      <template #footer>
        <button type="button" class="btn" @click="showForm = false">キャンセル</button>
        <button type="submit" form="user-form" class="btn btn--primary" :disabled="saving">
          <span v-if="saving" class="spinner" aria-hidden="true"></span>{{ form.mode === 'create' ? '作成' : '保存' }}
        </button>
      </template>
    </Modal>

    <Modal v-model="showKey" :title="keyInfo.title" @close="closeKey">
      <div class="stack">
        <div class="field">
          <span class="field__label">Access Key</span>
          <div class="input-group">
            <input class="input input--mono" readonly :value="keyInfo.accessKey" aria-label="Access Key" />
            <button type="button" class="btn" @click="copyText(keyInfo.accessKey)"><Icon name="copy" :size="16" />Copy</button>
          </div>
        </div>
        <div class="field">
          <span class="field__label">Secret Key</span>
          <div class="input-group">
            <input class="input input--mono" readonly :value="keyInfo.secretKey" aria-label="Secret Key" data-autofocus />
            <button type="button" class="btn" @click="copyText(keyInfo.secretKey)"><Icon name="copy" :size="16" />Copy</button>
          </div>
        </div>
        <div class="alert alert--warning">
          <Icon name="alert" />
          <div class="alert__body">Secret Key はこの画面を閉じると再表示できません。安全な場所に保存してください。</div>
        </div>
      </div>
      <template #footer>
        <button type="button" class="btn btn--primary" @click="closeKey">閉じる</button>
      </template>
    </Modal>
  </div>
</template>