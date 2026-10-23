<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
<script setup>
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { useClusterStore } from '../stores/cluster'
import Icon from '../components/Icon.vue'
import Donut from '../components/Donut.vue'
import Sparkline from '../components/Sparkline.vue'
import { formatBytes, percent, shortDuration } from '../lib/format.js'

const store = useClusterStore()
const WINDOW = 30
const getHist = ref([])
const putHist = ref([])
let timer = null

onMounted(() => {
  store.fetchCluster()
  timer = setInterval(() => store.fetchCluster(), 5000)
})
onUnmounted(() => { if (timer) clearInterval(timer) })

// metrics is replaced on every poll, so this fires once per sample.
watch(() => store.metrics, (m) => {
  if (!m) return
  getHist.value = [...getHist.value, Number(m.get_gib_s) || 0].slice(-WINDOW)
  putHist.value = [...putHist.value, Number(m.put_gib_s) || 0].slice(-WINDOW)
})

const c = computed(() => store.cluster)
const bucketCount = computed(() => c.value?.totalBuckets ?? c.value?.buckets?.length ?? 0)
const nodesOnline = computed(() => c.value?.onlineNodes ?? 0)
const nodesTotal = computed(() => c.value?.totalNodes ?? 0)
const disks = computed(() => c.value?.disks ?? [])
const totalCap = computed(() => c.value?.totalCap ?? 0)
const totalUsed = computed(() => c.value?.totalUsed ?? 0)
const pct = computed(() => percent(totalUsed.value, totalCap.value))

const jobs = computed(() => c.value?.jobs ?? [])
const jobCounts = computed(() => {
  const out = { pending: 0, running: 0, verified: 0, failed: 0, other: 0 }
  for (const j of jobs.value) {
    const s = j.Status ?? j.status
    if (s in out) out[s]++
    else out.other++
  }
  return out
})

const topDisks = computed(() =>
  [...disks.value].sort((a, b) => percent(b.Used, b.Capacity) - percent(a.Used, a.Capacity)).slice(0, 8),
)

const metrics = computed(() => store.metrics)
const fmt3 = (v) => (Number.isFinite(Number(v)) ? Number(v).toFixed(3) : '0.000')
const loadingFirst = computed(() => store.loading && !store.cluster)

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
        <h1 class="page-head__title">Dashboard</h1>
        <p class="page-head__desc">クラスタの状態を 5 秒ごとに更新します。</p>
      </div>
      <div class="page-head__actions">
        <button type="button" class="btn" :disabled="store.loading" @click="store.fetchCluster()">
          <span v-if="store.loading" class="spinner" aria-hidden="true"></span>
          <Icon v-else name="refresh" :size="16" />
          Sync
        </button>
      </div>
    </div>

    <div v-if="store.error" class="alert alert--error" role="alert">
      <Icon name="alert" />
      <div class="alert__body">{{ store.error }}</div>
    </div>

    <div v-if="loadingFirst" class="grid grid--4" aria-busy="true">
      <div v-for="n in 4" :key="n" class="card"><div class="skeleton" style="height: 104px"></div></div>
    </div>

    <template v-else>
      <div class="grid grid--4">
        <div class="card stat">
          <div class="stat__top">
            <span class="stat__label">Buckets</span>
            <span class="stat__icon"><Icon name="bucket" :size="16" /></span>
          </div>
          <div class="stat__value">{{ bucketCount }}</div>
          <div class="stat__sub"><router-link class="link" to="/buckets">一覧を開く</router-link></div>
        </div>

        <div class="card stat">
          <div class="stat__top">
            <span class="stat__label">Nodes</span>
            <span class="stat__icon"><Icon name="server" :size="16" /></span>
          </div>
          <div class="stat__value">{{ nodesOnline }}<span class="muted" style="font-size: 16px"> / {{ nodesTotal }}</span></div>
          <div class="stat__sub">
            <span class="dot--ok"><span class="dot"></span>online {{ nodesOnline }}</span>
            <span :class="nodesTotal - nodesOnline > 0 ? 'dot--bad' : ''"><span class="dot"></span>offline {{ Math.max(0, nodesTotal - nodesOnline) }}</span>
          </div>
        </div>

        <div class="card stat">
          <div class="stat__top">
            <span class="stat__label">Disks</span>
            <span class="stat__icon"><Icon name="disk" :size="16" /></span>
          </div>
          <div class="stat__value">{{ disks.length }}</div>
          <div class="stat__sub"><router-link class="link" to="/disks">詳細を見る</router-link></div>
        </div>

        <div class="card stat">
          <div class="stat__top">
            <span class="stat__label">Usage</span>
            <span class="stat__icon"><Icon name="activity" :size="16" /></span>
          </div>
          <div class="stat__value">{{ formatBytes(totalUsed) }}</div>
          <div class="stat__sub"><span>/ {{ totalCap > 0 ? formatBytes(totalCap) : '容量不明' }}</span></div>
        </div>
      </div>

      <div class="grid grid--main">
        <section class="card" aria-labelledby="cap-title">
          <div class="card__head"><h2 id="cap-title" class="card__title">Capacity</h2></div>
          <div class="card__body">
            <div class="split">
              <Donut :pct="pct" caption="USED" />
              <div class="legend">
                <div class="legend__item">
                  <span class="legend__name"><span class="legend__swatch legend__swatch--a"></span>Used</span>
                  <span class="legend__value">{{ formatBytes(totalUsed) }}</span>
                </div>
                <div class="legend__item">
                  <span class="legend__name"><span class="legend__swatch legend__swatch--free"></span>Free</span>
                  <span class="legend__value">{{ totalCap > 0 ? formatBytes(Math.max(0, totalCap - totalUsed)) : '—' }}</span>
                </div>
                <div class="legend__item">
                  <span class="legend__name">Total</span>
                  <span class="legend__value">{{ totalCap > 0 ? formatBytes(totalCap) : '—' }}</span>
                </div>
              </div>
            </div>
          </div>
        </section>

        <section class="card" aria-labelledby="tp-title">
          <div class="card__head">
            <h2 id="tp-title" class="card__title">Throughput</h2>
            <div class="row" style="gap: 14px">
              <span class="legend__name small"><span class="legend__swatch legend__swatch--a"></span>GET</span>
              <span class="legend__name small"><span class="legend__swatch legend__swatch--b"></span>PUT</span>
            </div>
          </div>
          <div class="card__body">
            <Sparkline :a="getHist" :b="putHist" :size="WINDOW" />
          </div>
          <div class="card__foot">
            <span class="num">GET {{ fmt3(metrics?.get_gib_s) }} GiB/s · {{ metrics?.gets ?? 0 }} req</span>
            <span class="num">PUT {{ fmt3(metrics?.put_gib_s) }} GiB/s · {{ metrics?.puts ?? 0 }} req</span>
            <span>uptime {{ shortDuration(metrics?.uptime) }}</span>
          </div>
        </section>
      </div>

      <div class="grid grid--main">
        <section class="card" aria-labelledby="jobs-title">
          <div class="card__head">
            <h2 id="jobs-title" class="card__title">Replication jobs</h2>
            <router-link class="link small" to="/jobs">すべて表示</router-link>
          </div>
          <div class="card__body">
            <div class="chips">
              <span class="badge badge--success">verified <span class="chip__count">{{ jobCounts.verified }}</span></span>
              <span class="badge badge--info">running <span class="chip__count">{{ jobCounts.running }}</span></span>
              <span class="badge">pending <span class="chip__count">{{ jobCounts.pending }}</span></span>
              <span class="badge badge--error">failed <span class="chip__count">{{ jobCounts.failed }}</span></span>
              <span v-if="jobCounts.other" class="badge badge--warning">other <span class="chip__count">{{ jobCounts.other }}</span></span>
            </div>
            <p v-if="jobs.length === 0" class="muted small mt-2">ジョブはありません。</p>
          </div>
        </section>

        <section class="card" aria-labelledby="disk-title">
          <div class="card__head">
            <h2 id="disk-title" class="card__title">Disk usage</h2>
            <router-link class="link small" to="/disks">すべて表示</router-link>
          </div>
          <div v-if="topDisks.length === 0" class="empty">
            <span class="empty__icon"><Icon name="disk" :size="22" /></span>
            <span class="empty__title">ディスクがありません</span>
            <p>Nodes から DataNode を追加すると表示されます。</p>
          </div>
          <div v-else class="table-wrap">
            <table class="table">
              <thead>
                <tr><th>Node</th><th>Path</th><th>Usage</th></tr>
              </thead>
              <tbody>
                <tr v-for="d in topDisks" :key="`${d.NodeID}:${d.Path}`">
                  <td class="mono">{{ d.NodeID }}</td>
                  <td class="mono">{{ d.Path }}</td>
                  <td>
                    <div class="usage">
                      <div class="progress progress--thin" :class="barClass(percent(d.Used, d.Capacity))">
                        <div class="progress__bar" :style="{ width: percent(d.Used, d.Capacity) + '%' }"></div>
                      </div>
                      <div class="usage__text num">
                        <span>{{ formatBytes(d.Used) }}</span>
                        <span>{{ d.Capacity > 0 ? percent(d.Used, d.Capacity) + '%' : '—' }}</span>
                      </div>
                    </div>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>
      </div>
    </template>
  </div>
</template>