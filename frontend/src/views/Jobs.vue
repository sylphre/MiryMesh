<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
<script setup>
import { ref, onMounted } from 'vue'
const jobs = ref([])
const error = ref('')
onMounted(async () => {
  const token = localStorage.getItem('mirymesh_admin_token') || ''
  const res = await fetch('/api/admin/jobs', { headers: { 'X-Admin-Token': token } })
  if (res.ok) { const j = await res.json(); jobs.value = j.jobs || [] } else error.value = await res.text()
})
</script>
<template>
  <main class="container">
    <h2>Jobs</h2>
    <div v-if="error" class="state--error">{{ error }}</div>
    <div v-else-if="jobs.length === 0" class="state--empty">No jobs</div>
    <ul v-else><li v-for="j in jobs" :key="j.id">{{ j.id }} — {{ j.status }}</li></ul>
  </main>
</template>
