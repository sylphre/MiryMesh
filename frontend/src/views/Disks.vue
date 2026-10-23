<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import Icon from '../components/Icon.vue'
import { api } from '../lib/api.js'
import { formatBytes, percent } from '../lib/format.js'

const disks = ref([])
const error = ref('')
const loading = ref(true)
const query = ref('')
let timer = null

async function load() {
  try {
    const j = await api('/api/admin/disks')
    disks.value = j.disks || []
    error.value = ''
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  load()
  timer = setInterval(load, 10000)
})
onUnmounted(() => { if (timer) clearInterval(timer) })

const filtered = computed(() => {
  const q = query.value.trim().toLowerCase()
  if (!q) return disks.value
  return disks.value.filter((d) => `${d.NodeID} ${d.Path}`.toLowerCase().includes(q))
})

const totals = computed(() => {
  let cap = 0
  let used = 0
  for (const d of disks.value) { cap += d.Capacity || 0; used += d.Used || 0 }
  return { cap, used }
})

function barClass(p) {
  if (p >= 95) return 'progress--danger'
  if (p >= 80) return 'progress--warn'
  return ''
}
</script>

<template>
  <div class="stack stack--lg">
    <div class="page-head" style="margin-bottom: 0">
      <div>
        <h1 class="page-head__title">Disks</h1>
        <p class="page-head__desc">DataNode に登録されたストレージパスと使用量です。</p>
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
        <div class="search">
          <Icon name="search" :size="16" />
          <input v-model="query" class="input" type="search" placeholder="Node / Path で検索" aria-label="ディスクを検索" />
        </div>
        <span class="toolbar__grow"></span>
        <span class="muted small num">{{ filtered.length }} disks · {{ formatBytes(totals.used) }} / {{ totals.cap > 0 ? formatBytes(totals.cap) : '—' }}</span>
      </div>

      <div v-if="loading" class="card__body" aria-busy="true"><div class="skeleton" style="height: 120px"></div></div>

      <div v-else-if="filtered.length === 0" class="empty">
        <span class="empty__icon"><Icon name="disk" :size="22" /></span>
        <span class="empty__title">{{ disks.length === 0 ? 'ディスクがありません' : '一致するディスクがありません' }}</span>
        <p v-if="disks.length === 0">Nodes から DataNode を追加すると、ここにストレージパスが表示されます。</p>
      </div>

      <div v-else class="table-wrap">
        <table class="table">
          <thead>
            <tr>
              <th>Node</th>
              <th>Path</th>
              <th>Usage</th>
              <th class="num">Used</th>
              <th class="num">Capacity</th>
              <th class="num">Limit</th>
              <th class="num">Avail</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="d in filtered" :key="`${d.NodeID}:${d.Path}`">
              <td class="mono">{{ d.NodeID }}</td>
              <td class="mono">{{ d.Path }}</td>
              <td style="min-width: 180px">
                <div v-if="d.Capacity > 0" class="usage">
                  <div class="progress progress--thin" :class="barClass(percent(d.Used, d.Capacity))">
                    <div class="progress__bar" :style="{ width: percent(d.Used, d.Capacity) + '%' }"></div>
                  </div>
                  <div class="usage__text num"><span>{{ percent(d.Used, d.Capacity) }}%</span></div>
                </div>
                <span v-else class="muted">容量不明</span>
              </td>
              <td class="num">{{ formatBytes(d.Used) }}</td>
              <td class="num">{{ d.Capacity > 0 ? formatBytes(d.Capacity) : '—' }}</td>
              <td class="num">{{ d.Limit > 0 ? formatBytes(d.Limit) : '95%' }}</td>
              <td class="num">{{ d.Avail > 0 ? formatBytes(d.Avail) : '—' }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
  </div>
</template>