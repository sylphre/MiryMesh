<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
<script setup>
import { ref, onMounted, onUnmounted, computed } from 'vue'
import { useClusterStore } from '../stores/cluster'

const store = useClusterStore()
let timer = null

onMounted(() => {
  store.fetchCluster()
  timer = setInterval(() => store.fetchCluster(), 5000)
})
onUnmounted(() => { if (timer) clearInterval(timer) })

const buckets = computed(() => store.cluster?.buckets?.length ?? 0)
const serversOnline = computed(() => store.cluster?.onlineNodes ?? 0)
const serversTotal = computed(() => store.cluster?.totalNodes ?? 0)
const drivesOnline = computed(() => store.cluster?.disks?.length ?? 0)
const capacity = computed(() => {
  const c = store.cluster
  if (!c) return { totalGiB: 0, usedGiB: 0, pct: 0 }
  const total = (c.totalCap || 0) / 1024 / 1024 / 1024
  const used = (c.totalUsed || 0) / 1024 / 1024 / 1024
  const pct = total > 0 ? Math.round(used / total * 100) : 0
  return { totalGiB: total.toFixed(1), usedGiB: used.toFixed(1), pct }
})
const gaugeDash = computed(() => {
  const pct = capacity.value.pct
  const circ = 2 * Math.PI * 42
  return `${(pct / 100 * circ).toFixed(1)} ${circ.toFixed(1)}`
})
const getGib = computed(() => store.metrics?.get_gib_s?.toFixed(3) ?? '0.000')
const putGib = computed(() => store.metrics?.put_gib_s?.toFixed(3) ?? '0.000')
const uptime = computed(() => store.metrics?.uptime ?? '—')
</script>

<template>
  <main class="container">
    <div style="display: flex; align-items: baseline; justify-content: space-between; margin-bottom: 16px;">
      <h2 style="font-size: 28px;">Metrics</h2>
      <button class="btn btn--sm" @click="store.fetchCluster()">Sync</button>
    </div>
    <div class="grid grid--4">
      <div class="card card--stat">
        <p class="card__title">Buckets</p>
        <p class="card__metric">{{ buckets }}</p>
        <p class="card__metric-sub">{{ buckets }} total</p>
      </div>
      <div class="card card--stat">
        <p class="card__title">Servers</p>
        <div style="display:flex;gap:16px;align-items:baseline;">
          <span class="card__metric">{{ serversOnline }}</span>
          <span class="caption"><span style="color:#22c55e;">●</span> {{ serversOnline }} Online</span>
          <span class="caption"><span style="color:#ef4444;">●</span> {{ (serversTotal - serversOnline) }} Offline</span>
        </div>
      </div>
      <div class="card card--stat">
        <p class="card__title">Drives</p>
        <div style="display:flex;gap:16px;align-items:baseline;">
          <span class="card__metric">{{ drivesOnline }}</span>
          <span class="caption"><span style="color:#22c55e;">●</span> {{ drivesOnline }} Online</span>
        </div>
      </div>
      <div class="card card--stat">
        <p class="card__title">Network</p>
        <p style="font-size:13px;">GET {{ getGib }} GiB/s · PUT {{ putGib }} GiB/s</p>
        <p class="caption">Uptime {{ uptime }}</p>
      </div>
    </div>
    <div class="grid grid--2" style="margin-top: 24px;">
      <div class="card" style="display:flex;gap:24px;align-items:center;">
        <svg class="gauge" viewBox="0 0 100 100" style="width:96px;height:96px;">
          <circle class="gauge__track" cx="50" cy="50" r="42" fill="none" stroke="rgba(255,255,255,0.08)" stroke-width="8"></circle>
          <circle class="gauge__value" cx="50" cy="50" r="42" fill="none" stroke="#38bdf8" stroke-width="8" :stroke-dasharray="gaugeDash" stroke-linecap="round" transform="rotate(-90 50 50)"></circle>
        </svg>
        <div>
          <p class="card__title">Capacity</p>
          <p style="font-size:13px;">{{ capacity.usedGiB }} GiB / {{ capacity.totalGiB }} GiB used</p>
          <p class="caption">{{ capacity.pct }}% (95% watermark)</p>
        </div>
      </div>
      <div class="card">
        <p class="card__title">Policy</p>
        <p class="caption">Default policy and replication status visible in cluster API.</p>
      </div>
    </div>
    <div v-if="store.error" class="card state--error" style="margin-top: 16px;">{{ store.error }}</div>
  </main>
</template>
