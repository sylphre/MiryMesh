<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import Icon from '../components/Icon.vue'
import { api } from '../lib/api.js'
import { formatBytes, timeAgo, formatDate } from '../lib/format.js'

const jobs = ref([])
const error = ref('')
const loading = ref(true)
const filter = ref('all')
const query = ref('')
let timer = null

// The backend Job struct has no json tags (Go field names); accept lower-case too.
const norm = (j) => ({
  id: j.ID ?? j.id ?? '',
  bucket: j.Bucket ?? j.bucket ?? '',
  key: j.Key ?? j.key ?? '',
  size: j.Size ?? j.size ?? 0,
  targets: j.Targets ?? j.targets ?? [],
  status: j.Status ?? j.status ?? '',
  attempts: j.Attempts ?? j.attempts ?? 0,
  maxRetries: j.MaxRetries ?? j.max_retries ?? 0,
  lastError: j.LastError ?? j.last_error ?? '',
  updatedAt: j.UpdatedAt ?? j.updated_at ?? null,
})

async function load() {
  try {
    const j = await api('/api/admin/jobs')
    jobs.value = (j.jobs || []).map(norm).sort((a, b) => new Date(b.updatedAt) - new Date(a.updatedAt))
    error.value = ''
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  load()
  timer = setInterval(load, 5000)
})
onUnmounted(() => { if (timer) clearInterval(timer) })

const STATUSES = ['pending', 'running', 'verified', 'failed', 'stale', 'missing']
const counts = computed(() => {
  const m = { all: jobs.value.length }
  for (const s of STATUSES) m[s] = 0
  for (const j of jobs.value) if (j.status in m) m[j.status]++
  return m
})

const filtered = computed(() => {
  const q = query.value.trim().toLowerCase()
  return jobs.value.filter((j) => {
    if (filter.value !== 'all' && j.status !== filter.value) return false
    if (!q) return true
    return `${j.id} ${j.bucket} ${j.key}`.toLowerCase().includes(q)
  })
})

function badge(s) {
  if (s === 'verified') return 'badge--success'
  if (s === 'running') return 'badge--info'
  if (s === 'failed') return 'badge--error'
  if (s === 'stale' || s === 'missing') return 'badge--warning'
  return ''
}
</script>

<template>
  <div class="stack stack--lg">
    <div class="page-head" style="margin-bottom: 0">
      <div>
        <h1 class="page-head__title">Jobs</h1>
        <p class="page-head__desc">レプリケーションジョブの状態です（5 秒ごとに更新）。</p>
      </div>
      <div class="page-head__actions">
        <button type="button" class="btn" @click="load"><Icon name="refresh" :size="16" />Refresh</button>
      </div>
    </div>

    <div v-if="error" class="alert alert--error" role="alert">
      <Icon name="alert" /><div class="alert__body">{{ error }}</div>
    </div>

    <section class="card">
      <div class="toolbar">
        <div class="chips" role="group" aria-label="ステータスで絞り込み">
          <button type="button" class="chip" :aria-pressed="filter === 'all'" @click="filter = 'all'">
            all <span class="chip__count">{{ counts.all }}</span>
          </button>
          <button v-for="s in STATUSES" :key="s" type="button" class="chip" :aria-pressed="filter === s" @click="filter = s">
            {{ s }} <span class="chip__count">{{ counts[s] }}</span>
          </button>
        </div>
        <span class="toolbar__grow"></span>
        <div class="search">
          <Icon name="search" :size="16" />
          <input v-model="query" class="input" type="search" placeholder="ID / Bucket / Key" aria-label="ジョブを検索" />
        </div>
      </div>

      <div v-if="loading" class="card__body" aria-busy="true"><div class="skeleton" style="height: 120px"></div></div>

      <div v-else-if="filtered.length === 0" class="empty">
        <span class="empty__icon"><Icon name="activity" :size="22" /></span>
        <span class="empty__title">{{ jobs.length === 0 ? 'ジョブはありません' : '一致するジョブがありません' }}</span>
      </div>

      <div v-else class="table-wrap">
        <table class="table">
          <thead>
            <tr>
              <th>Status</th>
              <th>Object</th>
              <th>Targets</th>
              <th class="num">Size</th>
              <th class="num">Attempts</th>
              <th>Updated</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="j in filtered" :key="j.id">
              <td><span class="badge" :class="badge(j.status)">{{ j.status || '—' }}</span></td>
              <td style="max-width: 420px">
                <div class="truncate"><span class="mono">{{ j.bucket }}/{{ j.key }}</span></div>
                <div class="muted small mono truncate" :title="j.id">{{ j.id }}</div>
                <div v-if="j.lastError" class="small" style="color: var(--color-error); overflow-wrap: anywhere">{{ j.lastError }}</div>
              </td>
              <td class="mono small">{{ j.targets.length ? j.targets.join(', ') : '—' }}</td>
              <td class="num">{{ formatBytes(j.size) }}</td>
              <td class="num">{{ j.attempts }}<span v-if="j.maxRetries" class="muted"> / {{ j.maxRetries }}</span></td>
              <td :title="formatDate(j.updatedAt)">{{ timeAgo(j.updatedAt) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
  </div>
</template>