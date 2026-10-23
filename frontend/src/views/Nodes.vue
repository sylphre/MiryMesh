<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
<script setup>
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import Icon from '../components/Icon.vue'
import Modal from '../components/Modal.vue'
import { api } from '../lib/api.js'
import { formatBytes, timeAgo, formatDate, percent } from '../lib/format.js'
import { copyText } from '../lib/clipboard.js'
import { toastErr, toastOk } from '../lib/toast.js'

const nodes = ref([])
const disks = ref([])
const error = ref('')
const loading = ref(true)
const origin = window.location.origin
let timer = null

const showPair = ref(false)
const pairToken = ref('')
const pairing = ref(false)

const showAdopt = ref(false)
const adopting = ref(false)
const adopt = reactive({ addr: '', controllerUrl: origin })

async function load() {
  try {
    const [n, d] = await Promise.all([api('/api/admin/nodes'), api('/api/admin/disks')])
    nodes.value = n.nodes || []
    disks.value = d.disks || []
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

const rows = computed(() =>
  nodes.value.map((n) => {
    const own = disks.value.filter((d) => d.NodeID === n.ID)
    const cap = own.reduce((s, d) => s + (d.Capacity || 0), 0)
    const used = own.reduce((s, d) => s + (d.Used || 0), 0)
    return { ...n, diskCount: own.length, cap, used }
  }),
)

async function createPairToken() {
  pairing.value = true
  try {
    const j = await api('/api/admin/pair-token', { method: 'POST' })
    pairToken.value = j.pair_token
    showPair.value = true
  } catch (e) {
    toastErr(e)
  } finally {
    pairing.value = false
  }
}

async function adoptNode() {
  if (adopting.value) return
  adopting.value = true
  try {
    await api('/api/admin/nodes/adopt', {
      method: 'POST',
      body: { addr: adopt.addr.trim(), controller_url: adopt.controllerUrl.trim() },
    })
    toastOk('DataNode に登録を依頼しました。数秒後に一覧へ反映されます')
    showAdopt.value = false
    adopt.addr = ''
    setTimeout(load, 2000)
  } catch (e) {
    toastErr(e)
  } finally {
    adopting.value = false
  }
}

const installCmd = computed(
  () => `sudo bash packaging/scripts/install.sh --client --master-url ${origin} --pair-token ${pairToken.value} --path /mnt/disk1`,
)
const daemonCmd = computed(
  () => `mirymesh-datanode --controller-url ${origin} --pair-token ${pairToken.value} --path /mnt/disk1`,
)
</script>

<template>
  <div class="stack stack--lg">
    <div class="page-head" style="margin-bottom: 0">
      <div>
        <h1 class="page-head__title">Nodes</h1>
        <p class="page-head__desc">クラスタに参加している DataNode です。</p>
      </div>
      <div class="page-head__actions">
        <button type="button" class="btn" @click="load"><Icon name="refresh" :size="16" />Refresh</button>
        <button type="button" class="btn" @click="showAdopt = true">
          <Icon name="server" :size="16" />IP を指定して追加
        </button>
        <button type="button" class="btn btn--primary" :disabled="pairing" @click="createPairToken">
          <Icon name="plus" :size="16" />Node を追加
        </button>
      </div>
    </div>

    <div v-if="error" class="alert alert--error" role="alert">
      <Icon name="alert" /><div class="alert__body">{{ error }}</div>
    </div>

    <section class="card">
      <div v-if="loading" class="card__body" aria-busy="true"><div class="skeleton" style="height: 120px"></div></div>

      <div v-else-if="rows.length === 0" class="empty">
        <span class="empty__icon"><Icon name="server" :size="22" /></span>
        <span class="empty__title">Node がありません</span>
        <p>「Node を追加」で pair_token を発行し、DataNode ホストで表示されたコマンドを実行してください。</p>
        <button type="button" class="btn btn--primary" :disabled="pairing" @click="createPairToken"><Icon name="plus" :size="16" />Node を追加</button>
      </div>

      <div v-else class="table-wrap">
        <table class="table">
          <thead>
            <tr>
              <th>ID</th>
              <th>Address</th>
              <th>Status</th>
              <th class="num">Disks</th>
              <th>Usage</th>
              <th>Last seen</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="n in rows" :key="n.ID">
              <td class="mono">{{ n.ID }}</td>
              <td class="mono">{{ n.Addr }}</td>
              <td>
                <span class="badge" :class="n.Status === 'online' ? 'badge--success' : 'badge--error'">
                  <span class="badge__dot"></span>{{ n.Status }}
                </span>
              </td>
              <td class="num">{{ n.diskCount }}</td>
              <td style="min-width: 180px">
                <div v-if="n.cap > 0" class="usage">
                  <div class="progress progress--thin"><div class="progress__bar" :style="{ width: percent(n.used, n.cap) + '%' }"></div></div>
                  <div class="usage__text num"><span>{{ formatBytes(n.used) }} / {{ formatBytes(n.cap) }}</span></div>
                </div>
                <span v-else class="muted">—</span>
              </td>
              <td :title="formatDate(n.LastSeen)">{{ timeAgo(n.LastSeen) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <Modal v-model="showPair" title="Node を追加" wide>
      <div class="stack">
        <div class="alert alert--warning">
          <Icon name="alert" />
          <div class="alert__body">pair_token は 15 分で失効し、1 回限り使えます。</div>
        </div>

        <div class="field">
          <span class="field__label">install.sh（推奨）</span>
          <div class="codeblock">{{ installCmd }}
            <button type="button" class="icon-btn codeblock__copy" aria-label="コマンドをコピー" @click="copyText(installCmd)"><Icon name="copy" :size="16" /></button>
          </div>
        </div>

        <div class="field">
          <span class="field__label">mirymesh-datanode を直接起動</span>
          <div class="codeblock">{{ daemonCmd }}
            <button type="button" class="icon-btn codeblock__copy" aria-label="コマンドをコピー" @click="copyText(daemonCmd)"><Icon name="copy" :size="16" /></button>
          </div>
        </div>

        <p class="field__hint">--path は DataNode に使うディスクのマウント先に置き換えてください。</p>
      </div>
      <template #footer>
        <button type="button" class="btn" data-autofocus @click="showPair = false">閉じる</button>
      </template>
    </Modal>

    <Modal v-model="showAdopt" title="IP を指定して Node を追加">
      <form id="adopt-form" class="stack" @submit.prevent="adoptNode">
        <div class="alert alert--warning">
          <Icon name="alert" />
          <div class="alert__body">DataNode を <code>--allow-adopt</code> 付きで起動している必要があります。登録済みの DataNode には使えません。</div>
        </div>
        <label class="field">
          <span class="field__label">DataNode のアドレス</span>
          <input v-model="adopt.addr" class="input mono" required autocomplete="off" placeholder="192.168.1.20 または 192.168.1.20:9002" data-autofocus />
          <span class="field__hint">ポート省略時は 9002 です。</span>
        </label>
        <label class="field">
          <span class="field__label">DataNode から見た Master の URL</span>
          <input v-model="adopt.controllerUrl" class="input mono" required autocomplete="off" placeholder="http://192.168.1.10:9001" />
          <span class="field__hint">127.0.0.1 など DataNode から届かないアドレスは指定しないでください。</span>
        </label>
      </form>
      <template #footer>
        <button type="button" class="btn" @click="showAdopt = false">キャンセル</button>
        <button type="submit" form="adopt-form" class="btn btn--primary" :disabled="adopting">
          <span v-if="adopting" class="spinner" aria-hidden="true"></span>
          登録を依頼
        </button>
      </template>
    </Modal>
  </div>
</template>