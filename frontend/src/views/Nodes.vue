<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
<script setup>
import { ref, onMounted } from 'vue'
const nodes = ref([])
const error = ref('')
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
    <h2>Nodes</h2>
    <div v-if="error" class="state--error">{{ error }}</div>
    <div v-else-if="nodes.length === 0" class="state--empty">No nodes</div>
    <table v-else class="table"><thead><tr><th>ID</th><th>Addr</th><th>Status</th></tr></thead><tbody><tr v-for="n in nodes" :key="n.ID"><td>{{ n.ID }}</td><td>{{ n.Addr }}</td><td>{{ n.Status }}</td></tr></tbody></table>
  </main>
</template>
