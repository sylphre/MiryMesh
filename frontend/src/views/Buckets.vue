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
const access = ref('private')
const placementPolicies = ref({}) // bucket -> policy string

function accessToPolicy(bucket, acc) {
  if (acc === 'private') return null
  if (acc === 'public-read') return {
    Version: "2012-10-17",
    Statement: [{ Sid: "PublicRead", Effect: "Allow", Principal: "*", Action: ["s3:GetObject"], Resource: [`arn:aws:s3:::${bucket}/*`] }]
  }
  if (acc === 'public-read-write') return {
    Version: "2012-10-17",
    Statement: [
      { Sid: "PublicReadWrite", Effect: "Allow", Principal: "*", Action: ["s3:GetObject", "s3:PutObject", "s3:DeleteObject"], Resource: [`arn:aws:s3:::${bucket}/*`] },
      { Sid: "PublicList", Effect: "Allow", Principal: "*", Action: ["s3:ListBucket"], Resource: [`arn:aws:s3:::${bucket}`] }
    ]
  }
  return null
}

function detectAccess(policyText) {
  if (!policyText || policyText.trim() === '') return 'private'
  try {
    const doc = JSON.parse(policyText)
    const stmts = doc.Statement || []
    // Check for public-read pattern
    const hasPublicRead = stmts.some(s => s.Principal === "*" && Array.isArray(s.Action) && s.Action.length === 1 && s.Action[0] === "s3:GetObject")
    const hasPublicWrite = stmts.some(s => Array.isArray(s.Action) && s.Action.includes("s3:PutObject"))
    if (hasPublicWrite) return 'public-read-write'
    if (hasPublicRead && stmts.length === 1) return 'public-read'
    return 'custom'
  } catch { return 'custom' }
}

async function load() {
  const token = localStorage.getItem('mirymesh_admin_token') || ''
  const res = await fetch('/api/admin/buckets', { headers: { 'X-Admin-Token': token } })
  if (res.ok) {
    const j = await res.json()
    buckets.value = j.buckets || []
    // Fetch placement policy per bucket
    for (const b of buckets.value) {
      try {
        const r = await fetch(`/api/admin/buckets/${b}/policy`, { headers: { 'X-Admin-Token': token } })
        if (r.ok) { const pj = await r.json(); placementPolicies.value[b] = pj.policy || 'single' }
        else placementPolicies.value[b] = 'single'
      } catch { placementPolicies.value[b] = 'single' }
    }
  }
}
async function create() {
  const token = localStorage.getItem('mirymesh_admin_token') || ''
  const res = await fetch('/api/admin/buckets', { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Admin-Token': token }, body: JSON.stringify({ name: name.value }) })
  if (!res.ok) { error.value = await res.text(); return }
  name.value = ''; await load()
}
async function selectBucket(b) {
  selected.value = b
  objectMsg.value = ''
  objects.value = []
  policyJSON.value = ''
  policyError.value = ''
  access.value = 'private'
  const token = localStorage.getItem('mirymesh_admin_token') || ''
  try {
    const r1 = await fetch(`/api/admin/buckets/${b}/objects`, { headers: { 'X-Admin-Token': token } })
    if (r1.ok) { const j = await r1.json(); objects.value = j.objects || [] }
  } catch {}
  try {
    const r2 = await fetch(`/api/admin/buckets/${b}/s3-policy`, { headers: { 'X-Admin-Token': token } })
    if (r2.ok) { policyJSON.value = await r2.text(); access.value = detectAccess(policyJSON.value) }
    else if (r2.status === 404) { access.value = 'private'; policyJSON.value = '' }
    else if (r2.status !== 404) policyError.value = await r2.text()
  } catch (e) { policyError.value = e.message }
}
async function saveAccess() {
  policyError.value = ''
  const token = localStorage.getItem('mirymesh_admin_token') || ''
  try {
    if (access.value === 'private') {
      const res = await fetch(`/api/admin/buckets/${selected.value}/s3-policy`, { method: 'DELETE', headers: { 'X-Admin-Token': token } })
      if (!res.ok && res.status !== 404) throw new Error(await res.text())
      policyJSON.value = ''
      policyError.value = 'Private に設定しました'
    } else if (access.value === 'custom') {
      const res = await fetch(`/api/admin/buckets/${selected.value}/s3-policy`, { method: 'PUT', headers: { 'Content-Type': 'application/json', 'X-Admin-Token': token }, body: policyJSON.value })
      if (!res.ok) throw new Error(await res.text())
      policyError.value = '保存しました'
    } else {
      const pol = accessToPolicy(selected.value, access.value)
      const res = await fetch(`/api/admin/buckets/${selected.value}/s3-policy`, { method: 'PUT', headers: { 'Content-Type': 'application/json', 'X-Admin-Token': token }, body: JSON.stringify(pol) })
      if (!res.ok) throw new Error(await res.text())
      policyJSON.value = JSON.stringify(pol, null, 2)
      policyError.value = '保存しました'
    }
  } catch (e) { policyError.value = e.message }
}
async function deletePolicy() {
  const token = localStorage.getItem('mirymesh_admin_token') || ''
  const res = await fetch(`/api/admin/buckets/${selected.value}/s3-policy`, { method: 'DELETE', headers: { 'X-Admin-Token': token } })
  if (res.ok) { policyJSON.value = ''; access.value = 'private'; policyError.value = '削除しました' }
  else policyError.value = await res.text()
}
const objectMsg = ref('')
const uploading = ref(false)

function adminToken() { return localStorage.getItem('mirymesh_admin_token') || '' }
function objectURL(b, key) {
  return `/api/admin/buckets/${encodeURIComponent(b)}/object?key=${encodeURIComponent(key)}`
}
function formatSize(n) {
  if (!Number.isFinite(n)) return ''
  if (n < 1024) return `${n} B`
  const units = ['KiB', 'MiB', 'GiB', 'TiB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(1)} ${units[i]}`
}
async function refreshObjects() {
  if (!selected.value) return
  try {
    const r = await fetch(`/api/admin/buckets/${encodeURIComponent(selected.value)}/objects`, { headers: { 'X-Admin-Token': adminToken() } })
    if (r.ok) { const j = await r.json(); objects.value = j.objects || [] }
  } catch (e) { objectMsg.value = e.message }
}
async function uploadFiles(ev) {
  const input = ev.target
  const files = Array.from(input.files || [])
  if (!files.length || !selected.value) return
  const bucket = selected.value
  uploading.value = true
  objectMsg.value = ''
  const failed = []
  for (const f of files) {
    try {
      const res = await fetch(objectURL(bucket, f.name), {
        method: 'PUT',
        headers: { 'Content-Type': f.type || 'application/octet-stream', 'X-Admin-Token': adminToken() },
        body: f
      })
      if (!res.ok) failed.push(`${f.name}: ${await res.text()}`)
    } catch (e) { failed.push(`${f.name}: ${e.message}`) }
  }
  uploading.value = false
  input.value = ''
  objectMsg.value = failed.length
    ? `失敗 ${failed.length} 件 — ${failed.join(' / ')}`
    : `${files.length} 件をアップロードしました`
  if (selected.value === bucket) await refreshObjects()
}
async function downloadObject(key) {
  objectMsg.value = ''
  try {
    const res = await fetch(objectURL(selected.value, key), { headers: { 'X-Admin-Token': adminToken() } })
    if (!res.ok) throw new Error(await res.text())
    const blob = await res.blob()
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = key.split('/').pop() || key
    document.body.appendChild(a)
    a.click()
    a.remove()
    URL.revokeObjectURL(url)
  } catch (e) { objectMsg.value = `ダウンロード失敗: ${e.message}` }
}
async function deleteObject(key) {
  if (!window.confirm(`「${key}」を削除しますか？`)) return
  objectMsg.value = ''
  try {
    const res = await fetch(objectURL(selected.value, key), { method: 'DELETE', headers: { 'X-Admin-Token': adminToken() } })
    if (!res.ok) throw new Error(await res.text())
    objectMsg.value = `削除しました: ${key}`
    await refreshObjects()
  } catch (e) { objectMsg.value = `削除失敗: ${e.message}` }
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
        <table class="table"><thead><tr><th>Bucket</th><th>Placement</th></tr></thead><tbody><tr v-for="b in buckets" :key="b" @click="selectBucket(b)" :class="selected===b ? 'selected' : ''" style="cursor:pointer;"><td>{{ b }}</td><td><span class="badge badge--policy">{{ placementPolicies[b] || 'single' }}</span></td></tr></tbody></table>
      </div>
      <div v-if="selected" class="card" style="flex:1.5;">
        <h3>{{ selected }}</h3>
        <p class="caption">Objects: {{ objects.length }}</p>
        <div style="margin:8px 0;">
          <label for="object-upload" class="caption" style="display:block;margin-bottom:4px;">Upload</label>
          <input id="object-upload" type="file" multiple :disabled="uploading" @change="uploadFiles" />
          <span v-if="uploading" class="caption" role="status"> アップロード中…</span>
        </div>
        <p v-if="objectMsg" class="caption" role="status" style="margin:8px 0;word-break:break-word;">{{ objectMsg }}</p>
        <div v-if="objects.length" style="max-height:240px;overflow:auto;margin:8px 0;">
          <table class="table">
            <thead><tr><th>Key</th><th>Size</th><th>Actions</th></tr></thead>
            <tbody>
              <tr v-for="o in objects" :key="o.Key">
                <td style="word-break:break-all;">{{ o.Key }}</td>
                <td>{{ formatSize(o.Size) }}</td>
                <td style="white-space:nowrap;">
                  <button type="button" class="btn" :aria-label="`${o.Key} をダウンロード`" @click="downloadObject(o.Key)">Download</button>
                  <button type="button" class="btn" :aria-label="`${o.Key} を削除`" @click="deleteObject(o.Key)">Delete</button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <div v-else class="state--empty">No objects</div>

        <div style="margin-top:16px;">
          <h4>Access</h4>
          <select v-model="access" class="input" style="max-width:220px;">
            <option value="private">Private</option>
            <option value="public-read">Public Read</option>
            <option value="public-read-write">Public Read/Write</option>
            <option value="custom">Custom</option>
          </select>
          <div v-if="access === 'custom'" style="margin-top:8px;">
            <textarea v-model="policyJSON" class="input" style="min-height:200px;font-family:monospace;font-size:12px;" placeholder='{"Version":"2012-10-17","Statement":[...]}'></textarea>
          </div>
          <div v-else-if="access !== 'private'" class="caption" style="margin-top:8px;">{{ access === 'public-read' ? 'Anyone can read objects' : 'Anyone can read/write objects' }}</div>
          <div style="display:flex;gap:8px;margin-top:8px;">
            <button class="btn btn--primary" @click="saveAccess">保存</button>
            <button v-if="access !== 'private'" class="btn" @click="deletePolicy">削除 (Privateに戻す)</button>
          </div>
          <p v-if="policyError" class="caption" style="margin-top:8px;">{{ policyError }}</p>
        </div>

        <div style="margin-top:16px;">
          <h4>Advanced</h4>
          <p class="caption">Versioning: <span class="badge">Not implemented</span> — S3 ?versioning は 501</p>
          <p class="caption">Tags: <span class="badge">Not implemented</span> — S3 ?tagging は 501</p>
          <p class="caption">CORS: <span class="badge">Not implemented</span> — S3 ?cors は 501</p>
          <p class="caption">Encryption / Object Lock / Retention: <span class="badge">Not implemented</span></p>
        </div>
      </div>
    </div>
  </main>
</template>
