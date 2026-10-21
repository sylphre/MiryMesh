<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
<script setup>
import { ref, onMounted } from 'vue'
const buckets = ref([])
const name = ref('')
const error = ref('')
async function load() {
  const token = localStorage.getItem('mirymesh_admin_token') || ''
  const res = await fetch('/api/admin/buckets', { headers: { 'X-Admin-Token': token } })
  if (res.ok) { const j = await res.json(); buckets.value = j.buckets || [] }
}
async function create() {
  const token = localStorage.getItem('mirymesh_admin_token') || ''
  const res = await fetch('/api/admin/buckets', { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Admin-Token': token }, body: JSON.stringify({ name: name.value }) })
  if (!res.ok) { error.value = await res.text(); return }
  name.value = ''; await load()
}
onMounted(load)
</script>
<template>
  <main class="container">
    <h2>Buckets</h2>
    <div v-if="error" class="state--error">{{ error }}</div>
    <form @submit.prevent="create" style="display:flex;gap:8px;margin:16px 0;">
      <input v-model="name" class="input" placeholder="bucket name" required style="max-width:300px;" />
      <button type="submit" class="btn btn--primary">Create</button>
    </form>
    <div v-if="buckets.length === 0" class="state--empty">No buckets</div>
    <ul v-else><li v-for="b in buckets" :key="b">{{ b }}</li></ul>
  </main>
</template>
