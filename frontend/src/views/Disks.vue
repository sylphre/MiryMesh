<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
<script setup>
import { ref, onMounted } from 'vue'
const disks = ref([])
const error = ref('')
onMounted(async () => {
  try {
    const token = localStorage.getItem('mirymesh_admin_token') || ''
    const res = await fetch('/api/admin/disks', { headers: { 'X-Admin-Token': token } })
    if (!res.ok) throw new Error(await res.text())
    const j = await res.json()
    disks.value = j.disks || []
  } catch (e) { error.value = e.message }
})
</script>
<template>
  <main class="container">
    <h2>Disks</h2>
    <div v-if="error" class="state--error">{{ error }}</div>
    <div v-else-if="disks.length === 0" class="state--empty">No disks</div>
    <table v-else class="table"><thead><tr><th>Node</th><th>Path</th><th>Capacity</th><th>Used</th></tr></thead><tbody><tr v-for="d in disks" :key="d.Path"><td>{{ d.NodeID }}</td><td>{{ d.Path }}</td><td>{{ d.Capacity }}</td><td>{{ d.Used }}</td></tr></tbody></table>
  </main>
</template>
