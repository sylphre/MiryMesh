<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
<script setup>
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import Icon from '../components/Icon.vue'
import Modal from '../components/Modal.vue'
import { api } from '../lib/api.js'
import { toastOk, toastErr } from '../lib/toast.js'
import { confirmDialog } from '../lib/dialog.js'

const router = useRouter()

const buckets = ref([])
const placements = ref({})
const loading = ref(true)
const error = ref('')
const filter = ref('')

const showCreate = ref(false)
const newName = ref('')
const creating = ref(false)
const createError = ref('')

const filtered = computed(() => {
  const q = filter.value.trim().toLowerCase()
  return q ? buckets.value.filter((b) => b.toLowerCase().includes(q)) : buckets.value
})

async function loadPlacement(name) {
  try {
    const p = await api(`/api/admin/buckets/${encodeURIComponent(name)}/policy`)
    return [name, (p && p.policy) || 'single']
  } catch {
    return [name, 'single']
  }
}

async function load() {
  try {
    const j = await api('/api/admin/buckets')
    const names = ((j && j.buckets) || []).slice().sort()
    buckets.value = names
    error.value = ''
    placements.value = Object.fromEntries(await Promise.all(names.map(loadPlacement)))
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

onMounted(load)

function openCreate() {
  newName.value = ''
  createError.value = ''
  showCreate.value = true
}

async function create() {
  if (creating.value) return
  creating.value = true
  createError.value = ''
  const name = newName.value.trim()
  try {
    await api('/api/admin/buckets', { method: 'POST', body: { name } })
    showCreate.value = false
    toastOk(`Bucket「${name}」を作成しました`)
    await router.push(`/buckets/${encodeURIComponent(name)}`)
  } catch (e) {
    createError.value = e.message
  } finally {
    creating.value = false
  }
}

async function remove(name) {
  const ok = await confirmDialog({
    title: 'Bucket を削除',
    message: `Bucket「${name}」を削除します。この操作は取り消せません。オブジェクトが残っている場合は削除できません。`,
    confirmLabel: '削除',
    danger: true,
    requireText: name,
  })
  if (!ok) return
  try {
    await api(`/api/admin/buckets/${encodeURIComponent(name)}`, { method: 'DELETE' })
    toastOk(`Bucket「${name}」を削除しました`)
    await load()
  } catch (e) {
    toastErr(e)
  }
}
</script>

<template>
  <div class="stack stack--lg">
    <div class="page-head" style="margin-bottom: 0">
      <div>
        <h1 class="page-head__title">Buckets</h1>
        <p class="page-head__desc">オブジェクトを保存する Bucket を管理します。</p>
      </div>
      <div class="page-head__actions">
        <button type="button" class="btn" @click="load"><Icon name="refresh" :size="16" />Refresh</button>
        <button type="button" class="btn btn--primary" @click="openCreate"><Icon name="plus" :size="16" />Bucket を作成</button>
      </div>
    </div>

    <div v-if="error" class="alert alert--error" role="alert">
      <Icon name="alert" /><div class="alert__body">{{ error }}</div>
    </div>

    <section class="card">
      <div v-if="loading" class="card__body" aria-busy="true"><div class="skeleton" style="height: 120px"></div></div>

      <div v-else-if="buckets.length === 0" class="empty">
        <span class="empty__icon"><Icon name="bucket" :size="22" /></span>
        <span class="empty__title">Bucket がありません</span>
        <p>Bucket を作成すると、オブジェクトのアップロードやアクセス設定ができます。</p>
        <button type="button" class="btn btn--primary" @click="openCreate"><Icon name="plus" :size="16" />Bucket を作成</button>
      </div>

      <template v-else>
        <div class="toolbar">
          <div class="input-group" style="flex: 1; max-width: 320px">
            <input v-model="filter" class="input" type="search" placeholder="Bucket を検索" aria-label="Bucket を検索" />
          </div>
          <span class="toolbar__grow"></span>
          <span class="muted small">{{ filtered.length }} / {{ buckets.length }} 件</span>
        </div>

        <div v-if="filtered.length === 0" class="empty">
          <span class="empty__icon"><Icon name="search" :size="22" /></span>
          <span class="empty__title">該当する Bucket がありません</span>
        </div>

        <div v-else class="table-wrap">
          <table class="table">
            <thead>
              <tr>
                <th>Name</th>
                <th>Placement</th>
                <th class="actions"><span class="sr-only">操作</span></th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="b in filtered" :key="b">
                <td>
                  <div class="cell-main">
                    <span class="ico-folder"><Icon name="bucket" :size="18" /></span>
                    <router-link class="path__seg" :to="`/buckets/${encodeURIComponent(b)}`">{{ b }}</router-link>
                  </div>
                </td>
                <td><span class="badge badge--policy">{{ placements[b] || 'single' }}</span></td>
                <td class="actions">
                  <router-link class="btn btn--sm" :to="`/buckets/${encodeURIComponent(b)}`">開く</router-link>
                  <button type="button" class="icon-btn icon-btn--danger" :aria-label="`${b} を削除`" @click="remove(b)">
                    <Icon name="trash" :size="16" />
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </template>
    </section>

    <Modal v-model="showCreate" title="Bucket を作成">
      <form id="create-bucket-form" class="stack" @submit.prevent="create">
        <div v-if="createError" class="alert alert--error" role="alert">
          <Icon name="alert" /><div class="alert__body">{{ createError }}</div>
        </div>
        <label class="field">
          <span class="field__label">Bucket 名</span>
          <input
            v-model="newName"
            class="input input--mono"
            required
            minlength="3"
            maxlength="63"
            pattern="[a-z0-9][a-z0-9.\-]{1,61}[a-z0-9]"
            autocomplete="off"
            spellcheck="false"
            placeholder="my-bucket"
            data-autofocus
          />
          <span class="field__hint">3〜63 文字の小文字英数字、ハイフン、ドットが使えます。</span>
        </label>
      </form>
      <template #footer>
        <button type="button" class="btn" @click="showCreate = false">キャンセル</button>
        <button type="submit" form="create-bucket-form" class="btn btn--primary" :disabled="creating">
          <span v-if="creating" class="spinner" aria-hidden="true"></span>作成
        </button>
      </template>
    </Modal>
  </div>
</template>