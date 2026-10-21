<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
<script setup>
import { ref, onMounted } from 'vue'
const policy = ref('single')
const status = ref('')
async function load() {
  const token = localStorage.getItem('mirymesh_admin_token') || ''
  const res = await fetch('/api/admin/config', { headers: { 'X-Admin-Token': token } })
  if (res.ok) { const j = await res.json(); policy.value = j.default_policy || 'single' }
}
async function save() {
  const token = localStorage.getItem('mirymesh_admin_token') || ''
  const res = await fetch('/api/admin/config/policy', { method: 'PUT', headers: { 'Content-Type': 'application/json', 'X-Admin-Token': token }, body: JSON.stringify({ policy: policy.value }) })
  status.value = res.ok ? 'Saved' : await res.text()
}
async function genCreds() {
  const token = localStorage.getItem('mirymesh_admin_token') || ''
  const res = await fetch('/api/admin/generate-credentials', { method: 'POST', headers: { 'X-Admin-Token': token } })
  if (res.ok) { const j = await res.json(); status.value = `AccessKey: ${j.access_key} SecretKey: ${j.secret_key}` }
}
onMounted(load)
</script>
<template>
  <main class="container">
    <h2>Settings</h2>
    <div class="card" style="margin-top: 16px;">
      <label>Default Policy
        <select v-model="policy" class="input" style="max-width:200px;">
          <option value="single">single</option><option value="dual">dual</option><option value="triple">triple</option><option value="ALL">ALL</option>
        </select>
      </label>
      <button class="btn btn--primary" @click="save" style="margin-top:12px;">Save Policy</button>
    </div>
    <div class="card" style="margin-top: 16px;">
      <button class="btn" @click="genCreds">Generate Credentials</button>
      <p v-if="status" class="caption" style="margin-top:8px; word-break: break-all;">{{ status }}</p>
    </div>
  </main>
</template>
