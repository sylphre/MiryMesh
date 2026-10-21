<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
<script setup>
import { ref, onMounted } from 'vue'
const buckets = ref([])
const name = ref('')
const error = ref('')
const selected = ref(null)
const objects = ref([])
const policyJSON = ref('')
const policyError = ref('')
const placementPolicy = ref('single')

const misskeySample = JSON.stringify({
  Version: "2012-10-17",
  Statement: [
    {
      Sid: "AddCannedAcl",
      Effect: "Allow",
      Principal: { AWS: "arn:aws:iam::123456789012:user/misskey" },
      Action: ["s3:GetObject", "s3:ListBucket", "s3:PutObject", "s3:PutObjectAcl", "s3:DeleteObject"],
      Resource: "arn:aws:s3:::BUCKET/*"
    },
    {
      Sid: "AllowPublicRead",
      Effect: "Allow",
      Principal: { AWS: "*" },
      Action: "s3:GetObject",
      Resource: "arn:aws:s3:::BUCKET/*"
    }
  ]
}, null, 2)

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
async function selectBucket(b) {
  selected.value = b
  objects.value = []
  policyJSON.value = ''
  policyError.value = ''
  const token = localStorage.getItem('mirymesh_admin_token') || ''
  try {
    const r1 = await fetch(`/api/admin/buckets/${b}/objects`, { headers: { 'X-Admin-Token': token } })
    if (r1.ok) { const j = await r1.json(); objects.value = j.objects || [] }
  } catch {}
  try {
    const r2 = await fetch(`/api/admin/buckets/${b}/s3-policy`, { headers: { 'X-Admin-Token': token } })
    if (r2.ok) { policyJSON.value = await r2.text() }
    else if (r2.status !== 404) policyError.value = await r2.text()
  } catch (e) { policyError.value = e.message }
  try {
    const r3 = await fetch(`/api/admin/buckets/${b}/policy`, { headers: { 'X-Admin-Token': token } })
    if (r3.ok) { const j = await r3.json(); placementPolicy.value = j.policy || 'single' }
  } catch {}
}
async function savePolicy() {
  policyError.value = ''
  const token = localStorage.getItem('mirymesh_admin_token') || ''
  try {
    const res = await fetch(`/api/admin/buckets/${selected.value}/s3-policy`, { method: 'PUT', headers: { 'Content-Type': 'application/json', 'X-Admin-Token': token }, body: policyJSON.value })
    if (!res.ok) throw new Error(await res.text())
    policyError.value = '保存しました'
  } catch (e) { policyError.value = e.message }
}
async function deletePolicy() {
  const token = localStorage.getItem('mirymesh_admin_token') || ''
  const res = await fetch(`/api/admin/buckets/${selected.value}/s3-policy`, { method: 'DELETE', headers: { 'X-Admin-Token': token } })
  if (res.ok) { policyJSON.value = ''; policyError.value = '削除しました' }
  else policyError.value = await res.text()
}
function fillMisskey() {
  if (selected.value) policyJSON.value = misskeySample.replace(/BUCKET/g, selected.value)
  else policyJSON.value = misskeySample
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
    <div v-else style="display:flex;gap:16px;">
      <div style="flex:1;">
        <table class="table"><thead><tr><th>Bucket</th><th>Policy</th></tr></thead><tbody><tr v-for="b in buckets" :key="b" @click="selectBucket(b)" :class="selected===b ? 'selected' : ''" style="cursor:pointer;"><td>{{ b }}</td><td><span class="badge badge--policy">{{ placementPolicy }}</span></td></tr></tbody></table>
      </div>
      <div v-if="selected" class="card" style="flex:1.5;">
        <h3>{{ selected }}</h3>
        <p class="caption">Objects: {{ objects.length }}</p>
        <ul v-if="objects.length" style="max-height:120px;overflow:auto;margin:8px 0;"><li v-for="o in objects" :key="o.Key">{{ o.Key }} ({{ o.Size }} bytes)</li></ul>
        <div v-else class="state--empty">No objects</div>
        <div style="margin-top:16px;">
          <h4>Bucket Policy (S3 ?policy)</h4>
          <textarea v-model="policyJSON" class="input" style="min-height:200px;font-family:monospace;font-size:12px;" placeholder='{"Version":"2012-10-17","Statement":[...]}'></textarea>
          <div style="display:flex;gap:8px;margin-top:8px;">
            <button class="btn btn--primary" @click="savePolicy">保存</button>
            <button class="btn" @click="deletePolicy">削除</button>
            <button class="btn" @click="fillMisskey">Fill sample (Misskey)</button>
          </div>
          <p v-if="policyError" class="caption" style="margin-top:8px;">{{ policyError }}</p>
        </div>
      </div>
    </div>
  </main>
</template>
