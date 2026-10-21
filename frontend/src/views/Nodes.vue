<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
<script setup>
import { ref, onMounted } from 'vue'
const nodes = ref([])
const error = ref('')
const pairCmd = ref('')
const pairToken = ref('')
const pairError = ref('')
const origin = window.location.origin

async function createPairToken() {
  pairError.value = ''
  try {
    const token = localStorage.getItem('mirymesh_admin_token') || ''
    const res = await fetch('/api/admin/pair-token', { method: 'POST', headers: { 'X-Admin-Token': token } })
    if (!res.ok) throw new Error(await res.text())
    const j = await res.json()
    pairToken.value = j.pair_token
    pairCmd.value = `mirymesh-datanode --controller-url ${origin} --pair-token ${j.pair_token} --path /mnt/disk1`
  } catch (e) {
    pairError.value = e.message
  }
}

onMounted(async () => {
  try {
    const token = localStorage.getItem('mirymesh_admin_token') || ''
    const res = await fetch('/api/admin/nodes', { headers: { 'X-Admin-Token': token } })
    if (!res.ok) throw new Error(await res.text())
    const j = await res.json()
    nodes.value = j.nodes || []
  } catch (e) { error.value = e.message }
})
</script>
<template>
  <main class="container">
    <div style="display:flex;justify-content:space-between;align-items:center;">
      <h2>Nodes</h2>
      <button class="btn btn--primary" @click="createPairToken">Node を追加</button>
    </div>
    <div v-if="pairCmd" class="card" style="margin-top:12px;">
      <p class="caption">下記を DataNode ホストで実行（pair_token は 15分で失効、1回限り）:</p>
      <code style="display:block;padding:12px;background:var(--color-bg-soft);border-radius:var(--radius);word-break:break-all;">{{ pairCmd }}</code>
      <p class="caption" style="margin-top:8px;">install.sh: <code>install.sh --client --master-url {{ origin }} --pair-token {{ pairToken }}</code></p>
    </div>
    <div v-if="pairError" class="state--error" style="margin-top:12px;">{{ pairError }}</div>
    <div v-if="error" class="state--error" style="margin-top:12px;">{{ error }}</div>
    <div v-else-if="nodes.length === 0" class="state--empty" style="margin-top:12px;">No nodes — 「Node を追加」で pair_token を発行し DataNode を登録してください。</div>
    <table v-else class="table" style="margin-top:12px;"><thead><tr><th>ID</th><th>Addr</th><th>Status</th></tr></thead><tbody><tr v-for="n in nodes" :key="n.ID"><td>{{ n.ID }}</td><td>{{ n.Addr }}</td><td>{{ n.Status }}</td></tr></tbody></table>
  </main>
</template>
