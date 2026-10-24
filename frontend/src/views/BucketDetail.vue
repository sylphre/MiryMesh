<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
<script setup>
import { ref, reactive, computed, watch, onMounted, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import Icon from '../components/Icon.vue'
import { api, uploadObject, downloadObject, objectUrl } from '../lib/api.js'
import { formatBytes, formatDate, timeAgo } from '../lib/format.js'
import { toastOk, toastErr } from '../lib/toast.js'
import { confirmDialog } from '../lib/dialog.js'

const route = useRoute()
const router = useRouter()

const bucket = computed(() => String(route.params.name || ''))
const encBucket = computed(() => encodeURIComponent(bucket.value))

const tabs = [
  { id: 'objects', label: 'Objects', icon: 'folder' },
  { id: 'access', label: 'Access', icon: 'lock' },
  { id: 'placement', label: 'Placement', icon: 'disk' },
  { id: 'quota', label: 'Quota', icon: 'disk' },
  { id: 'advanced', label: 'Advanced', icon: 'settings' },
]
const tab = ref('objects')

/* ───────────── Objects ───────────── */

const OBJECT_LIMIT = 1000
const allObjects = ref([])
const objectsLoading = ref(true)
const objectsError = ref('')
const filter = ref('')
const sortKey = ref('name')
const sortDir = ref(1)

const prefix = computed(() => {
  const p = route.query.prefix
  return typeof p === 'string' ? p : ''
})

const crumbs = computed(() => {
  const parts = prefix.value.split('/').filter(Boolean)
  return parts.map((name, i) => ({ name, prefix: parts.slice(0, i + 1).join('/') + '/' }))
})

function go(p) {
  router.push({ path: route.path, query: p ? { prefix: p } : {} })
  filter.value = ''
}

let loadSeq = 0
async function loadObjects(silent = false) {
  const seq = ++loadSeq
  const name = bucket.value
  if (!silent) objectsLoading.value = true
  try {
    const j = await api(`/api/admin/buckets/${encodeURIComponent(name)}/objects`)
    if (seq !== loadSeq) return
    allObjects.value = j && Array.isArray(j.objects) ? j.objects : []
    objectsError.value = ''
  } catch (e) {
    if (seq !== loadSeq) return
    objectsError.value = e.status === 404 ? 'Bucket が見つかりません' : e.message
  } finally {
    if (seq === loadSeq) objectsLoading.value = false
  }
}

function timeValue(v) {
  const t = Date.parse(v)
  return Number.isNaN(t) ? 0 : t
}

// ListObjectsV2 は delimiter 無しで呼ばれるため、フォルダ構造はクライアント側で組み立てる。
const entries = computed(() => {
  const p = prefix.value
  const folders = new Map()
  const files = []
  for (const o of allObjects.value) {
    if (!o.Key.startsWith(p)) continue
    const rest = o.Key.slice(p.length)
    if (!rest) continue
    const i = rest.indexOf('/')
    if (i === -1) {
      files.push({ type: 'file', name: rest, key: o.Key, size: o.Size || 0, mod: o.LastMod, count: 1 })
      continue
    }
    const name = rest.slice(0, i)
    let f = folders.get(name)
    if (!f) {
      f = { type: 'folder', name, key: `${p}${name}/`, size: 0, mod: null, count: 0 }
      folders.set(name, f)
    }
    // "dir/" というプレースホルダーキーはフォルダの存在だけを示す
    if (i === rest.length - 1) continue
    f.count++
    f.size += o.Size || 0
    if (!f.mod || timeValue(o.LastMod) > timeValue(f.mod)) f.mod = o.LastMod
  }

  const q = filter.value.trim().toLowerCase()
  const match = (e) => !q || e.name.toLowerCase().includes(q)
  const cmp = (a, b) => {
    let r
    if (sortKey.value === 'size') r = a.size - b.size
    else if (sortKey.value === 'mod') r = timeValue(a.mod) - timeValue(b.mod)
    else r = a.name.localeCompare(b.name, 'ja', { numeric: true })
    return r * sortDir.value
  }
  return [
    ...Array.from(folders.values()).filter(match).sort(cmp),
    ...files.filter(match).sort(cmp),
  ]
})

const folderCount = computed(() => entries.value.filter((e) => e.type === 'folder').length)
const fileCount = computed(() => entries.value.length - folderCount.value)
const totalSize = computed(() => entries.value.reduce((s, e) => s + e.size, 0))
const truncated = computed(() => allObjects.value.length >= OBJECT_LIMIT)

function setSort(key) {
  if (sortKey.value === key) sortDir.value = -sortDir.value
  else {
    sortKey.value = key
    sortDir.value = 1
  }
}

function ariaSort(key) {
  if (sortKey.value !== key) return 'none'
  return sortDir.value === 1 ? 'ascending' : 'descending'
}

async function download(entry) {
  try {
    await downloadObject(bucket.value, entry.key)
  } catch (e) {
    toastErr(e)
  }
}

async function removeEntry(entry) {
  const name = bucket.value
  if (entry.type === 'file') {
    const ok = await confirmDialog({
      title: 'オブジェクトを削除',
      message: `「${entry.key}」を削除します。この操作は取り消せません。`,
      confirmLabel: '削除',
      danger: true,
    })
    if (!ok) return
    try {
      await api(objectUrl(name, entry.key), { method: 'DELETE' })
      toastOk('削除しました')
    } catch (e) {
      toastErr(e)
    }
    await loadObjects(true)
    return
  }

  const targets = allObjects.value.filter((o) => o.Key.startsWith(entry.key)).map((o) => o.Key)
  const ok = await confirmDialog({
    title: 'フォルダを削除',
    message: `「${entry.key}」と配下の ${targets.length} 件のオブジェクトを削除します。この操作は取り消せません。`,
    confirmLabel: '削除',
    danger: true,
    requireText: entry.name,
  })
  if (!ok) return
  let failed = 0
  for (const key of targets) {
    try {
      await api(objectUrl(name, key), { method: 'DELETE' })
    } catch {
      failed++
    }
  }
  if (failed) toastErr(`${failed} 件の削除に失敗しました`)
  else toastOk(`${targets.length} 件を削除しました`)
  await loadObjects(true)
}

/* ───────────── Upload ───────────── */

const MAX_PARALLEL = 3
const uploads = ref([])
const uploadsOpen = ref(true)
const fileInput = ref(null)
const dirInput = ref(null)
let uploadSeq = 0
let running = 0

const doneCount = computed(() => uploads.value.filter((u) => u.status === 'done').length)

function pct(u) {
  if (u.status === 'done') return 100
  return u.size > 0 ? Math.min(100, Math.round((u.loaded / u.size) * 100)) : 0
}

function statusText(u) {
  switch (u.status) {
    case 'queued': return '待機中'
    case 'uploading': return `${pct(u)}%`
    case 'done': return '完了'
    case 'canceled': return 'キャンセル'
    default: return u.error || '失敗'
  }
}

function enqueue(items) {
  const base = prefix.value
  for (const it of items) {
    uploads.value.push(
      reactive({
        id: ++uploadSeq,
        bucket: bucket.value,
        key: base + it.path,
        file: it.file,
        size: it.file.size,
        loaded: 0,
        status: 'queued',
        error: '',
        controller: null,
      }),
    )
  }
  uploadsOpen.value = true
  pump()
}

function pump() {
  while (running < MAX_PARALLEL) {
    const next = uploads.value.find((u) => u.status === 'queued')
    if (!next) break
    run(next)
  }
}

async function run(u) {
  running++
  u.status = 'uploading'
  u.controller = new AbortController()
  try {
    await uploadObject(u.bucket, u.key, u.file, {
      signal: u.controller.signal,
      onProgress: (loaded) => { u.loaded = loaded },
    })
    u.loaded = u.size
    u.status = 'done'
  } catch (e) {
    if (e && e.name === 'AbortError') {
      u.status = 'canceled'
    } else {
      u.status = 'error'
      u.error = e && e.message ? e.message : '失敗'
    }
  } finally {
    u.controller = null
    running--
    pump()
    if (running === 0 && !uploads.value.some((x) => x.status === 'queued')) loadObjects(true)
  }
}

function cancelUpload(u) {
  if (u.status === 'uploading' && u.controller) u.controller.abort()
  else if (u.status === 'queued') u.status = 'canceled'
}

function retryUpload(u) {
  u.status = 'queued'
  u.loaded = 0
  u.error = ''
  pump()
}

function clearFinished() {
  uploads.value = uploads.value.filter((u) => u.status === 'queued' || u.status === 'uploading')
}

function closeUploads() {
  for (const u of uploads.value) cancelUpload(u)
  uploads.value = []
}

function pickFiles() { if (fileInput.value) fileInput.value.click() }
function pickDir() { if (dirInput.value) dirInput.value.click() }

function onPick(ev) {
  const input = ev.target
  const items = Array.from(input.files || []).map((f) => ({ file: f, path: f.webkitRelativePath || f.name }))
  input.value = ''
  if (items.length) enqueue(items)
}

/* Drag & drop（フォルダのドロップにも対応） */
const dragging = ref(false)
let dragDepth = 0

function hasFiles(ev) {
  return !!ev.dataTransfer && Array.from(ev.dataTransfer.types || []).includes('Files')
}
function onDragEnter(ev) {
  if (!hasFiles(ev)) return
  dragDepth++
  dragging.value = true
}
function onDragOver(ev) {
  if (hasFiles(ev)) ev.preventDefault()
}
function onDragLeave(ev) {
  if (!hasFiles(ev)) return
  dragDepth = Math.max(0, dragDepth - 1)
  if (dragDepth === 0) dragging.value = false
}

async function collectDropped(dt) {
  const list = dt.items ? Array.from(dt.items) : []
  // DataTransfer は await 後に無効になるため、エントリは同期的に取り出す
  const roots = list.map((i) => (i.kind === 'file' && i.webkitGetAsEntry ? i.webkitGetAsEntry() : null))
  if (roots.length === 0 || roots.some((r) => !r)) {
    return Array.from(dt.files || []).map((f) => ({ file: f, path: f.name }))
  }
  const out = []
  async function walk(entry, base) {
    if (entry.isFile) {
      const file = await new Promise((resolve, reject) => entry.file(resolve, reject))
      out.push({ file, path: base + entry.name })
    } else if (entry.isDirectory) {
      const reader = entry.createReader()
      let batch
      do {
        batch = await new Promise((resolve, reject) => reader.readEntries(resolve, reject))
        for (const child of batch) await walk(child, `${base}${entry.name}/`)
      } while (batch.length)
    }
  }
  for (const r of roots) await walk(r, '')
  return out
}

async function onDrop(ev) {
  dragDepth = 0
  dragging.value = false
  if (!hasFiles(ev)) return
  ev.preventDefault()
  try {
    const items = await collectDropped(ev.dataTransfer)
    if (items.length) enqueue(items)
  } catch (e) {
    toastErr(e)
  }
}

/* ───────────── Access ───────────── */

const accessOptions = [
  { value: 'private', title: 'Private', desc: '認証済みのリクエストのみ' },
  { value: 'public-read', title: 'Public Read', desc: '誰でもオブジェクトを読み取り可能' },
  { value: 'public-read-write', title: 'Public Read/Write', desc: '誰でも読み書き可能' },
  { value: 'custom', title: 'Custom', desc: 'Policy JSON を直接編集' },
]

const access = ref('private')
const policyText = ref('')
const accessLoading = ref(true)
const accessSaving = ref(false)
const accessError = ref('')

function presetPolicy(kind, b) {
  const arn = `arn:aws:s3:::${b}`
  if (kind === 'public-read') {
    return {
      Version: '2012-10-17',
      Statement: [{ Sid: 'PublicRead', Effect: 'Allow', Principal: '*', Action: ['s3:GetObject'], Resource: [`${arn}/*`] }],
    }
  }
  if (kind === 'public-read-write') {
    return {
      Version: '2012-10-17',
      Statement: [
        {
          Sid: 'PublicReadWrite',
          Effect: 'Allow',
          Principal: '*',
          Action: ['s3:GetObject', 's3:PutObject', 's3:DeleteObject'],
          Resource: [`${arn}/*`],
        },
        { Sid: 'PublicList', Effect: 'Allow', Principal: '*', Action: ['s3:ListBucket'], Resource: [arn] },
      ],
    }
  }
  return null
}

function detectAccess(text) {
  if (!text || text.trim() === '') return 'private'
  try {
    const doc = JSON.parse(text)
    const stmts = Array.isArray(doc.Statement) ? doc.Statement : []
    const hasPublicRead = stmts.some(
      (s) => s.Principal === '*' && Array.isArray(s.Action) && s.Action.length === 1 && s.Action[0] === 's3:GetObject',
    )
    const hasPublicWrite = stmts.some((s) => Array.isArray(s.Action) && s.Action.includes('s3:PutObject'))
    if (hasPublicWrite) return 'public-read-write'
    if (hasPublicRead && stmts.length === 1) return 'public-read'
    return 'custom'
  } catch {
    return 'custom'
  }
}

function pretty(text) {
  try {
    return JSON.stringify(JSON.parse(text), null, 2)
  } catch {
    return text
  }
}

const preview = computed(() => {
  const p = presetPolicy(access.value, bucket.value)
  return p ? JSON.stringify(p, null, 2) : ''
})

const isPublic = computed(() => access.value !== 'private')
// Public URL は Public Read が許可されているバケットだけに出す。
// 画面上の選択ではなく保存済みの Policy で判定するので、Custom の Policy でも対象になる。
const isPublicRead = ref(false)

function policyGrantsPublicRead(text) {
  if (!text || text.trim() === '') return false
  try {
    const doc = JSON.parse(text)
    const raw = doc && doc.Statement
    const stmts = Array.isArray(raw) ? raw : raw ? [raw] : []
    const list = (v) => (Array.isArray(v) ? v : v == null ? [] : [v])
    return stmts.some((s) => {
      if (!s || s.Effect !== 'Allow') return false
      if (s.Condition && Object.keys(s.Condition).length > 0) return false
      const principals = s.Principal && typeof s.Principal === 'object' && !Array.isArray(s.Principal) ? list(s.Principal.AWS) : list(s.Principal)
      if (!principals.includes('*')) return false
      return list(s.Action).some((a) => {
        const v = String(a).toLowerCase()
        return v === '*' || v === 's3:*' || v === 's3:getobject' || v === 's3:get*'
      })
    })
  } catch {
    return false
  }
}

watch(access, (v) => {
  if (v === 'custom' && !policyText.value.trim()) {
    policyText.value = JSON.stringify(presetPolicy('public-read', bucket.value), null, 2)
  }
})

async function loadAccess() {
  accessLoading.value = true
  accessError.value = ''
  try {
    const res = await api(`/api/admin/buckets/${encBucket.value}/s3-policy`, { raw: true })
    const text = await res.text()
    policyText.value = pretty(text)
    access.value = detectAccess(text)
    isPublicRead.value = policyGrantsPublicRead(text)
  } catch (e) {
    if (e.status === 404) {
      policyText.value = ''
      access.value = 'private'
      isPublicRead.value = false
    } else {
      accessError.value = e.message
    }
  } finally {
    accessLoading.value = false
  }
}

async function saveAccess() {
  if (accessSaving.value) return
  accessError.value = ''
  const url = `/api/admin/buckets/${encBucket.value}/s3-policy`
  accessSaving.value = true
  try {
    if (access.value === 'private') {
      try {
        await api(url, { method: 'DELETE' })
      } catch (e) {
        if (e.status !== 404) throw e
      }
      policyText.value = ''
      isPublicRead.value = false
    } else if (access.value === 'custom') {
      try {
        JSON.parse(policyText.value)
      } catch {
        throw new Error('Policy が正しい JSON ではありません')
      }
      await api(url, { method: 'PUT', body: policyText.value })
      isPublicRead.value = policyGrantsPublicRead(policyText.value)
    } else {
      const text = JSON.stringify(presetPolicy(access.value, bucket.value), null, 2)
      await api(url, { method: 'PUT', body: text })
      policyText.value = text
      isPublicRead.value = policyGrantsPublicRead(text)
    }
    toastOk('アクセス設定を保存しました')
  } catch (e) {
    accessError.value = e.message
  } finally {
    accessSaving.value = false
  }
}

/* ───────────── Placement ───────────── */

const basePlacements = [
  { value: 'single', title: 'single', desc: '1 ノードに保存' },
  { value: 'dual', title: 'dual', desc: '2 ノードに複製' },
  { value: 'triple', title: 'triple', desc: '3 ノードに複製' },
  { value: 'ALL', title: 'ALL', desc: '全ノードに複製' },
]

const placement = ref('single')
const savedPlacement = ref('single')
const placementLoading = ref(true)
const placementSaving = ref(false)
const placementError = ref('')

const placementOptions = computed(() =>
  basePlacements.some((p) => p.value === savedPlacement.value)
    ? basePlacements
    : [...basePlacements, { value: savedPlacement.value, title: savedPlacement.value, desc: '現在の設定' }],
)

async function loadPlacement() {
  placementLoading.value = true
  placementError.value = ''
  try {
    const j = await api(`/api/admin/buckets/${encBucket.value}/policy`)
    savedPlacement.value = (j && j.policy) || 'single'
    placement.value = savedPlacement.value
  } catch (e) {
    placementError.value = e.message
  } finally {
    placementLoading.value = false
  }
}

async function savePlacement() {
  if (placementSaving.value) return
  placementSaving.value = true
  placementError.value = ''
  try {
    const j = await api(`/api/admin/buckets/${encBucket.value}/policy`, { method: 'PUT', body: { policy: placement.value } })
    savedPlacement.value = (j && j.policy) || placement.value
    placement.value = savedPlacement.value
    toastOk('Placement を保存しました')
  } catch (e) {
    placementError.value = e.message
  } finally {
    placementSaving.value = false
  }
}

/* Placement rule: 配置モード / 使用するディスク / 専用ディスク */

const modeOptions = [
  { value: 'spread', title: 'spread', desc: 'レプリカを別々の Node に分散（既定）' },
  { value: 'same_node', title: 'same_node', desc: '同じ Node の別々のディスクに複製' },
  { value: 'any', title: 'any', desc: '別ディスクに複製。可能なら別 Node、足りなければ同じ Node も可' },
]

const ruleMode = ref('spread')
const ruleAllow = ref([])
const ruleReserved = ref([])
const ruleDisks = ref([])
const rawDisks = ref([])
const savedRuleSig = ref('')
const ruleLoading = ref(true)
const ruleSaving = ref(false)
const ruleError = ref('')

function cleanDiskPath(p) {
  const s = String(p || '').trim()
  return s.length > 1 ? s.replace(/\/+$/, '') || '/' : s
}

function diskKey(nodeId, path) {
  return `${nodeId}::${cleanDiskPath(path)}`
}

function ruleSignature() {
  return JSON.stringify([ruleMode.value, [...ruleAllow.value].sort(), [...ruleReserved.value].sort()])
}

const ruleDirty = computed(() => ruleSignature() !== savedRuleSig.value)

// doc: GET/PUT /placement の応答, disks: GET /api/admin/disks の disks
function applyRuleDoc(doc, disks) {
  const byKey = new Map()
  for (const d of disks) {
    const key = diskKey(d.NodeID, d.Path)
    byKey.set(key, {
      key,
      node_id: d.NodeID,
      path: cleanDiskPath(d.Path),
      capacity: d.Capacity || 0,
      used: d.Used || 0,
      missing: false,
      other: '',
    })
  }
  // 登録から消えたディスクがルールに残っていても、画面から外せるように行を作る。
  const keysOf = (list) =>
    (Array.isArray(list) ? list : []).map((r) => {
      const key = diskKey(r.node_id, r.path)
      if (!byKey.has(key)) {
        byKey.set(key, { key, node_id: r.node_id, path: cleanDiskPath(r.path), capacity: 0, used: 0, missing: true, other: '' })
      }
      return key
    })
  const allow = keysOf(doc && doc.disks)
  const reserved = keysOf(doc && doc.reserved_disks)
  const others = doc && Array.isArray(doc.reserved_by_others) ? doc.reserved_by_others : []
  for (const o of others) {
    const e = byKey.get(diskKey(o.node_id, o.path))
    if (e) e.other = o.bucket
  }
  ruleDisks.value = [...byKey.values()].sort(
    (a, b) => a.node_id.localeCompare(b.node_id) || a.path.localeCompare(b.path),
  )
  ruleMode.value = (doc && doc.mode) || 'spread'
  ruleAllow.value = allow
  ruleReserved.value = reserved
  savedRuleSig.value = ruleSignature()
}

let ruleSeq = 0
async function loadRule() {
  const seq = ++ruleSeq
  const name = encBucket.value
  ruleLoading.value = true
  ruleError.value = ''
  try {
    const [doc, dj] = await Promise.all([
      api(`/api/admin/buckets/${name}/placement`),
      api('/api/admin/disks'),
    ])
    if (seq !== ruleSeq) return
    rawDisks.value = dj && Array.isArray(dj.disks) ? dj.disks : []
    applyRuleDoc(doc, rawDisks.value)
  } catch (e) {
    if (seq === ruleSeq) ruleError.value = e.message
  } finally {
    if (seq === ruleSeq) ruleLoading.value = false
  }
}

function toggleAllow(key, on) {
  const set = new Set(ruleAllow.value)
  if (on) {
    set.add(key)
  } else {
    set.delete(key)
    // 使用対象から外したディスクは専用にもできない。
    ruleReserved.value = ruleReserved.value.filter((k) => k !== key)
  }
  ruleAllow.value = [...set]
}

function toggleReserved(key, on) {
  const set = new Set(ruleReserved.value)
  if (on) {
    set.add(key)
    // 使用するディスクを絞っている場合、専用ディスクもその一覧に含める必要がある。
    if (ruleAllow.value.length > 0 && !ruleAllow.value.includes(key)) {
      ruleAllow.value = [...ruleAllow.value, key]
    }
  } else {
    set.delete(key)
  }
  ruleReserved.value = [...set]
}

function diskRefs(keys) {
  const idx = new Map(ruleDisks.value.map((d) => [d.key, d]))
  return keys
    .map((k) => idx.get(k))
    .filter(Boolean)
    .map((d) => ({ node_id: d.node_id, path: d.path }))
}

async function saveRule() {
  if (ruleSaving.value) return
  ruleSaving.value = true
  ruleError.value = ''
  try {
    const doc = await api(`/api/admin/buckets/${encBucket.value}/placement`, {
      method: 'PUT',
      body: {
        mode: ruleMode.value,
        disks: diskRefs(ruleAllow.value),
        reserved_disks: diskRefs(ruleReserved.value),
      },
    })
    applyRuleDoc(doc, rawDisks.value)
    toastOk('配置ルールを保存しました')
  } catch (e) {
    ruleError.value = e.message
  } finally {
    ruleSaving.value = false
  }
}

/* ───────────── Quota ───────────── */

const quotaUnits = [
  { label: 'B', factor: 1 },
  { label: 'KiB', factor: 1024 },
  { label: 'MiB', factor: 1024 ** 2 },
  { label: 'GiB', factor: 1024 ** 3 },
  { label: 'TiB', factor: 1024 ** 4 },
]

const quotaValue = ref('')
const quotaUnit = ref(1024 ** 3)
const savedQuota = ref(0)
const quotaUsed = ref(null)
const quotaLoading = ref(true)
const quotaSaving = ref(false)
const quotaError = ref('')

// '' (empty) means unlimited; an invalid number yields NaN.
const quotaBytes = computed(() => {
  const raw = String(quotaValue.value ?? '').trim()
  if (raw === '') return 0
  const n = Number(raw)
  if (!Number.isFinite(n) || n < 0) return NaN
  return Math.round(n * quotaUnit.value)
})

const quotaPercent = computed(() => {
  if (!savedQuota.value || quotaUsed.value === null) return 0
  return Math.min(100, Math.round((quotaUsed.value / savedQuota.value) * 100))
})

function applyQuota(j) {
  const max = j && Number.isFinite(j.max_bytes) ? j.max_bytes : 0
  savedQuota.value = max
  quotaUsed.value = j && Number.isFinite(j.used_bytes) ? j.used_bytes : null
  if (max === 0) {
    quotaValue.value = ''
    return
  }
  // Show the value in the largest unit that divides it exactly.
  const unit = [...quotaUnits].reverse().find((u) => max % u.factor === 0) || quotaUnits[0]
  quotaUnit.value = unit.factor
  quotaValue.value = String(max / unit.factor)
}

async function loadQuota() {
  quotaLoading.value = true
  quotaError.value = ''
  try {
    applyQuota(await api(`/api/admin/buckets/${encBucket.value}/quota`))
  } catch (e) {
    quotaError.value = e.message
  } finally {
    quotaLoading.value = false
  }
}

async function saveQuota() {
  if (quotaSaving.value) return
  if (Number.isNaN(quotaBytes.value)) {
    quotaError.value = '容量の上限には 0 以上の数値を入力してください'
    return
  }
  quotaSaving.value = true
  quotaError.value = ''
  try {
    const j = await api(`/api/admin/buckets/${encBucket.value}/quota`, { method: 'PUT', body: { max_bytes: quotaBytes.value } })
    applyQuota(j)
    toastOk(savedQuota.value === 0 ? '容量の上限を解除しました' : '容量の上限を保存しました')
  } catch (e) {
    quotaError.value = e.message
  } finally {
    quotaSaving.value = false
  }
}

/* ───────────── Domains / Public URL ───────────── */

const domainList = ref([])
const domainsText = ref('')
const domainsMax = ref(10)
const domainsLoading = ref(true)
const domainsSaving = ref(false)
const domainsError = ref('')

function applyDomains(j) {
  domainList.value = Array.isArray(j.domains) ? j.domains : []
  domainsText.value = domainList.value.join('\n')
  if (Number.isFinite(j.max)) domainsMax.value = j.max
}

async function loadDomains() {
  domainsLoading.value = true
  domainsError.value = ''
  try {
    applyDomains(await api(`/api/admin/buckets/${encBucket.value}/domains`))
  } catch (e) {
    domainsError.value = e.message
  } finally {
    domainsLoading.value = false
  }
}

async function saveDomains() {
  if (domainsSaving.value) return
  const list = domainsText.value
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter(Boolean)
  if (list.length > domainsMax.value) {
    domainsError.value = `ドメインは最大 ${domainsMax.value} 個までです`
    return
  }
  domainsSaving.value = true
  domainsError.value = ''
  try {
    const j = await api(`/api/admin/buckets/${encBucket.value}/domains`, { method: 'PUT', body: { domains: list } })
    applyDomains(j)
    toastOk(domainList.value.length === 0 ? 'ドメイン制限を解除しました' : 'ドメインを保存しました')
  } catch (e) {
    domainsError.value = e.message
  } finally {
    domainsSaving.value = false
  }
}

// ドメインが設定済みなら先頭のドメイン（https）、未設定ならこの画面と同じホストの S3 ポート(9000)。
function publicUrl(entry) {
  const key = String(entry.key).split('/').map(encodeURIComponent).join('/')
  const first = domainList.value[0]
  const base = first ? `https://${first}` : `${window.location.protocol}//${window.location.hostname}:9000`
  return `${base}/${encBucket.value}/${key}`
}

/* ───────────── Bucket ───────────── */

async function deleteBucket() {
  const name = bucket.value
  const ok = await confirmDialog({
    title: 'Bucket を削除',
    message: `Bucket「${name}」を削除します。この操作は取り消せません。オブジェクトが残っている場合は削除できません。`,
    confirmLabel: '削除',
    danger: true,
    requireText: name,
  })
  if (!ok) return
  try {
    await api(`/api/admin/buckets/${encodeURIComponent(name)}`, { method: 'DELETE' })
    toastOk(`Bucket「${name}」を削除しました`)
    await router.push('/buckets')
  } catch (e) {
    toastErr(e)
  }
}

function loadAll() {
  filter.value = ''
  allObjects.value = []
  loadObjects()
  loadAccess()
  loadPlacement()
  loadRule()
  loadQuota()
  loadDomains()
}

onMounted(loadAll)
watch(bucket, () => {
  closeUploads()
  tab.value = 'objects'
  loadAll()
})
onBeforeUnmount(closeUploads)

const advancedItems = [
  { label: 'Versioning', badge: 'Not implemented', note: 'S3 ?versioning の有効化は 501 を返します' },
  { label: 'Tags', badge: 'S3 API', note: 'S3 ?tagging で設定（DB に永続化）' },
  { label: 'CORS', badge: 'S3 API', note: 'S3 ?cors で設定（DB に永続化）' },
  { label: 'Lifecycle', badge: 'S3 API', note: 'S3 ?lifecycle で設定（DB に永続化）' },
  { label: 'Object Lock / Retention', badge: 'S3 API', note: 'オブジェクト単位の ?retention / ?legal-hold で削除・上書きを保護' },
  { label: 'Encryption', badge: 'SSE-C のみ', note: 'SSE-S3 / SSE-KMS は未対応' },
]
</script>

<template>
  <div class="stack stack--lg">
    <div class="page-head" style="margin-bottom: 0">
      <div>
        <router-link to="/buckets" class="btn btn--ghost btn--sm" style="margin-left: -10px">
          <Icon name="chevron-right" :size="14" style="transform: rotate(180deg)" />Buckets
        </router-link>
        <h1 class="page-head__title mono" style="margin-top: 4px">{{ bucket }}</h1>
        <p class="page-head__desc">
          <span class="badge badge--policy">{{ savedPlacement }}</span>
          <span class="badge" :class="isPublic && !accessLoading ? 'badge--warning' : ''" style="margin-left: 6px">
            {{ accessLoading ? '…' : access === 'private' ? 'Private' : access === 'custom' ? 'Custom policy' : 'Public' }}
          </span>
        </p>
      </div>
      <div class="page-head__actions">
        <button type="button" class="btn btn--danger" @click="deleteBucket"><Icon name="trash" :size="16" />Bucket を削除</button>
      </div>
    </div>

    <div class="tabs" role="tablist" aria-label="Bucket 設定">
      <button
        v-for="t in tabs"
        :id="`tab-${t.id}`"
        :key="t.id"
        type="button"
        class="tab"
        role="tab"
        :aria-selected="tab === t.id"
        :aria-controls="`panel-${t.id}`"
        @click="tab = t.id"
      >
        <Icon :name="t.icon" :size="16" />{{ t.label }}
      </button>
    </div>

    <!-- Objects -->
    <section v-if="tab === 'objects'" id="panel-objects" role="tabpanel" aria-labelledby="tab-objects">
      <div
        class="card dropzone"
        @dragenter="onDragEnter"
        @dragover="onDragOver"
        @dragleave="onDragLeave"
        @drop="onDrop"
      >
        <div v-if="dragging" class="dropzone__overlay">ここにドロップしてアップロード</div>

        <div class="toolbar">
          <nav class="path" aria-label="パス">
            <button type="button" class="path__seg mono" :aria-current="!prefix ? 'page' : undefined" @click="go('')">{{ bucket }}</button>
            <template v-for="(c, i) in crumbs" :key="c.prefix">
              <span class="path__sep" aria-hidden="true">/</span>
              <button type="button" class="path__seg" :aria-current="i === crumbs.length - 1 ? 'page' : undefined" @click="go(c.prefix)">
                {{ c.name }}
              </button>
            </template>
          </nav>
          <span class="toolbar__grow"></span>
          <input v-model="filter" class="input" type="search" placeholder="名前で絞り込み" aria-label="名前で絞り込み" style="width: 200px" />
          <button type="button" class="icon-btn" aria-label="更新" @click="loadObjects()"><Icon name="refresh" :size="16" /></button>
          <button type="button" class="btn btn--sm" @click="pickDir"><Icon name="folder" :size="14" />フォルダ</button>
          <button type="button" class="btn btn--primary btn--sm" @click="pickFiles"><Icon name="upload" :size="14" />アップロード</button>
          <input ref="fileInput" type="file" multiple hidden @change="onPick" />
          <input ref="dirInput" type="file" webkitdirectory multiple hidden @change="onPick" />
        </div>

        <div v-if="objectsError" class="alert alert--error" role="alert" style="margin: 16px">
          <Icon name="alert" /><div class="alert__body">{{ objectsError }}</div>
        </div>

        <div v-else-if="objectsLoading" class="card__body" aria-busy="true"><div class="skeleton" style="height: 160px"></div></div>

        <div v-else-if="entries.length === 0" class="empty">
          <span class="empty__icon"><Icon :name="filter ? 'search' : 'inbox'" :size="22" /></span>
          <span class="empty__title">{{ filter ? '該当するオブジェクトがありません' : 'オブジェクトがありません' }}</span>
          <p v-if="!filter">ファイルやフォルダをここにドラッグ＆ドロップするか、「アップロード」を押してください。</p>
          <button v-if="!filter" type="button" class="btn btn--primary" @click="pickFiles"><Icon name="upload" :size="16" />アップロード</button>
        </div>

        <div v-else class="table-wrap">
          <table class="table">
            <thead>
              <tr>
                <th :aria-sort="ariaSort('name')">
                  <button type="button" class="th-sort" @click="setSort('name')">
                    Name<Icon v-if="sortKey === 'name'" :name="sortDir === 1 ? 'chevron-up' : 'chevron-down'" :size="12" />
                  </button>
                </th>
                <th class="num" :aria-sort="ariaSort('size')">
                  <button type="button" class="th-sort" @click="setSort('size')">
                    Size<Icon v-if="sortKey === 'size'" :name="sortDir === 1 ? 'chevron-up' : 'chevron-down'" :size="12" />
                  </button>
                </th>
                <th :aria-sort="ariaSort('mod')">
                  <button type="button" class="th-sort" @click="setSort('mod')">
                    Last modified<Icon v-if="sortKey === 'mod'" :name="sortDir === 1 ? 'chevron-up' : 'chevron-down'" :size="12" />
                  </button>
                </th>
                <th class="actions"><span class="sr-only">操作</span></th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="e in entries" :key="e.key">
                <td>
                  <div class="cell-main">
                    <span :class="e.type === 'folder' ? 'ico-folder' : 'ico-file'">
                      <Icon :name="e.type === 'folder' ? 'folder' : 'file'" :size="18" />
                    </span>
                    <button v-if="e.type === 'folder'" type="button" class="path__seg" @click="go(e.key)">{{ e.name }}</button>
                    <span v-else style="overflow-wrap: anywhere">{{ e.name }}</span>
                  </div>
                </td>
                <td class="num" :title="e.type === 'folder' ? `${e.count} 件` : `${e.size} B`">{{ e.type === 'folder' && e.count === 0 ? '—' : formatBytes(e.size) }}</td>
                <td :title="formatDate(e.mod)">{{ timeAgo(e.mod) }}</td>
                <td class="actions">
                  <button v-if="e.type === 'file'" type="button" class="icon-btn" :aria-label="`${e.name} をダウンロード`" @click="download(e)">
                    <Icon name="download" :size="16" />
                  </button>
                  <a v-if="e.type === 'file' && isPublicRead" class="icon-btn" :href="publicUrl(e)" target="_blank" rel="noopener noreferrer" :aria-label="`${e.name} の Public URL を開く`" :title="publicUrl(e)">
                    <Icon name="external" :size="16" />
                  </a>
                  <button type="button" class="icon-btn icon-btn--danger" :aria-label="`${e.name} を削除`" @click="removeEntry(e)">
                    <Icon name="trash" :size="16" />
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <div v-if="!objectsLoading && !objectsError && truncated" class="alert alert--warning" style="margin: 12px 16px">
          <Icon name="alert" /><div class="alert__body">先頭 {{ OBJECT_LIMIT }} 件までを表示しています。</div>
        </div>

        <div v-if="!objectsLoading && !objectsError" class="card__foot">
          <span class="muted small">{{ folderCount }} フォルダ · {{ fileCount }} ファイル</span>
          <span class="muted small">{{ formatBytes(totalSize) }}</span>
        </div>
      </div>
    </section>

    <!-- Access -->
    <section v-else-if="tab === 'access'" id="panel-access" role="tabpanel" aria-labelledby="tab-access" class="card">
      <div class="card__head"><h2 class="card__title">Access</h2></div>
      <div v-if="accessLoading" class="card__body" aria-busy="true"><div class="skeleton" style="height: 120px"></div></div>
      <form v-else class="card__body stack" @submit.prevent="saveAccess">
        <div v-if="accessError" class="alert alert--error" role="alert">
          <Icon name="alert" /><div class="alert__body">{{ accessError }}</div>
        </div>

        <fieldset class="field" style="border: 0; padding: 0; margin: 0; min-width: 0">
          <legend class="field__label">アクセス方針</legend>
          <div class="choices">
            <label v-for="o in accessOptions" :key="o.value" class="choice" :class="{ 'choice--on': access === o.value }">
              <input v-model="access" type="radio" name="bucket-access" :value="o.value" />
              <span class="choice__title">{{ o.title }}</span>
              <span class="choice__desc">{{ o.desc }}</span>
            </label>
          </div>
        </fieldset>

        <div v-if="isPublic" class="alert alert--warning">
          <Icon name="alert" />
          <div class="alert__body">認証なしでアクセスできる状態になります。公開してよい内容か確認してください。</div>
        </div>

        <label v-if="access === 'custom'" class="field">
          <span class="field__label">Policy JSON</span>
          <textarea v-model="policyText" class="input input--mono" rows="14" spellcheck="false" placeholder='{"Version":"2012-10-17","Statement":[]}'></textarea>
          <span class="field__hint">最大 20 KiB。</span>
        </label>
        <div v-else-if="preview" class="field">
          <span class="field__label">適用される Policy</span>
          <div class="codeblock">{{ preview }}</div>
        </div>

        <div class="row">
          <button type="submit" class="btn btn--primary" :disabled="accessSaving">
            <span v-if="accessSaving" class="spinner" aria-hidden="true"></span>保存
          </button>
        </div>
      </form>

      <form class="card__body stack" aria-labelledby="domains-title" @submit.prevent="saveDomains">
        <h3 id="domains-title" style="margin: 0; font-size: 1rem">許可するドメイン</h3>
        <p class="muted small">
          設定すると、このバケットへのリクエストは Host ヘッダーがここに書いたドメイン（またはそのサブドメイン）のときだけ受け付けます。
          空にすると制限しません。最大 {{ domainsMax }} 個。NGINX では <code>proxy_set_header Host $host;</code> が必要です。
        </p>
        <div v-if="domainsError" class="alert alert--error" role="alert">
          <Icon name="alert" /><div class="alert__body">{{ domainsError }}</div>
        </div>
        <div v-if="domainsLoading" class="skeleton" style="height: 80px" aria-busy="true"></div>
        <label v-else class="field">
          <span class="field__label">ドメイン（1 行に 1 つ）</span>
          <textarea v-model="domainsText" class="input input--mono" rows="5" spellcheck="false" placeholder="files.example.com&#10;cdn.example.org"></textarea>
        </label>
        <div class="row">
          <button type="submit" class="btn btn--primary" :disabled="domainsSaving || domainsLoading">
            <span v-if="domainsSaving" class="spinner" aria-hidden="true"></span>保存
          </button>
        </div>
      </form>
    </section>

    <!-- Placement -->
    <section v-else-if="tab === 'placement'" id="panel-placement" role="tabpanel" aria-labelledby="tab-placement" class="card">
      <div class="card__head"><h2 class="card__title">Placement</h2></div>
      <div v-if="placementLoading" class="card__body" aria-busy="true"><div class="skeleton" style="height: 100px"></div></div>
      <form v-else class="card__body stack" @submit.prevent="savePlacement">
        <p class="muted small">オブジェクトを DataNode に何台分複製して保存するかを指定します。オンラインの Node 数を超える設定は保存できません。</p>
        <div v-if="placementError" class="alert alert--error" role="alert">
          <Icon name="alert" /><div class="alert__body">{{ placementError }}</div>
        </div>
        <fieldset class="field" style="border: 0; padding: 0; margin: 0; min-width: 0">
          <legend class="sr-only">Placement Policy</legend>
          <div class="choices">
            <label v-for="p in placementOptions" :key="p.value" class="choice" :class="{ 'choice--on': placement === p.value }">
              <input v-model="placement" type="radio" name="bucket-placement" :value="p.value" />
              <span class="choice__title">{{ p.title }}</span>
              <span class="choice__desc">{{ p.desc }}</span>
            </label>
          </div>
        </fieldset>
        <div class="row">
          <button type="submit" class="btn btn--primary" :disabled="placementSaving || placement === savedPlacement">
            <span v-if="placementSaving" class="spinner" aria-hidden="true"></span>保存
          </button>
        </div>
      </form>

      <form class="card__body stack" aria-labelledby="rule-title" @submit.prevent="saveRule">
        <h3 id="rule-title" style="margin: 0; font-size: 1rem">配置ルール</h3>
        <p class="muted small">
          レプリカを別 Node に置くか同じ Node の別ディスクに置くか、この Bucket が書き込めるディスク、他の Bucket に使わせない専用ディスクを指定します。
          ルールは次の書き込みから適用され、保存済みのオブジェクトは移動しません。
        </p>
        <div v-if="ruleError" class="alert alert--error" role="alert">
          <Icon name="alert" /><div class="alert__body">{{ ruleError }}</div>
        </div>
        <div v-if="ruleLoading" class="skeleton" style="height: 100px" aria-busy="true"></div>
        <template v-else>
          <fieldset class="field" style="border: 0; padding: 0; margin: 0; min-width: 0">
            <legend class="field__label">配置モード</legend>
            <div class="choices">
              <label v-for="m in modeOptions" :key="m.value" class="choice" :class="{ 'choice--on': ruleMode === m.value }">
                <input
                  v-model="ruleMode"
                  type="radio"
                  name="bucket-placement-mode"
                  :value="m.value"
                  :disabled="savedPlacement === 'ALL' && m.value !== 'spread'"
                />
                <span class="choice__title">{{ m.title }}</span>
                <span class="choice__desc">{{ m.desc }}</span>
              </label>
            </div>
          </fieldset>
          <div v-if="savedPlacement === 'ALL'" class="alert alert--warning">
            <Icon name="alert" />
            <div class="alert__body">Policy が ALL のときは spread のみ選べます。</div>
          </div>

          <div class="field">
            <span class="field__label">ディスク</span>
            <span class="field__hint">
              「使用」を 1 つも選ばないと全ディスクが対象です。選ぶと、この Bucket はそのディスクにだけ書き込みます。
              「専用」にしたディスクには、この Bucket 以外は書き込めません。
            </span>
            <p v-if="ruleDisks.length === 0" class="muted small">登録されたディスクがありません。</p>
            <div v-else class="table-wrap">
              <table class="table">
                <thead>
                  <tr>
                    <th>Node</th>
                    <th>Path</th>
                    <th class="num">使用量</th>
                    <th>使用</th>
                    <th>専用</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="d in ruleDisks" :key="d.key">
                    <td class="mono">{{ d.node_id }}</td>
                    <td class="mono" style="overflow-wrap: anywhere">
                      {{ d.path }}
                      <span v-if="d.other" class="badge badge--warning">{{ d.other }} 専用</span>
                      <span v-else-if="d.missing" class="badge">未登録</span>
                    </td>
                    <td class="num">{{ d.capacity ? `${formatBytes(d.used)} / ${formatBytes(d.capacity)}` : '—' }}</td>
                    <td>
                      <input
                        type="checkbox"
                        :checked="ruleAllow.includes(d.key)"
                        :disabled="!!d.other || (d.missing && !ruleAllow.includes(d.key))"
                        :aria-label="`${d.node_id}:${d.path} を使用する`"
                        @change="toggleAllow(d.key, $event.target.checked)"
                      />
                    </td>
                    <td>
                      <input
                        type="checkbox"
                        :checked="ruleReserved.includes(d.key)"
                        :disabled="!!d.other || (d.missing && !ruleReserved.includes(d.key))"
                        :aria-label="`${d.node_id}:${d.path} をこの Bucket 専用にする`"
                        @change="toggleReserved(d.key, $event.target.checked)"
                      />
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>

          <div class="row">
            <button type="submit" class="btn btn--primary" :disabled="ruleSaving || !ruleDirty">
              <span v-if="ruleSaving" class="spinner" aria-hidden="true"></span>保存
            </button>
            <button type="button" class="btn" :disabled="ruleSaving" @click="loadRule">
              <Icon name="refresh" :size="16" />ディスクを再読み込み
            </button>
          </div>
        </template>
      </form>
    </section>

    <!-- Quota -->
    <section v-else-if="tab === 'quota'" id="panel-quota" role="tabpanel" aria-labelledby="tab-quota" class="card">
      <div class="card__head"><h2 class="card__title">Quota</h2></div>
      <div v-if="quotaLoading" class="card__body" aria-busy="true"><div class="skeleton" style="height: 100px"></div></div>
      <form v-else class="card__body stack" @submit.prevent="saveQuota">
        <p class="muted small">この Bucket に格納できる合計サイズの上限を指定します。上限を超える書き込みは 507 InsufficientStorage で拒否されます。空欄または 0 で無制限です。</p>
        <div v-if="quotaError" class="alert alert--error" role="alert">
          <Icon name="alert" /><div class="alert__body">{{ quotaError }}</div>
        </div>
        <div class="field">
          <label class="field__label" for="quota-value">容量の上限</label>
          <div class="row">
            <input
              id="quota-value"
              v-model="quotaValue"
              class="input"
              type="number"
              min="0"
              step="any"
              inputmode="decimal"
              placeholder="無制限"
              style="max-width: 200px"
            />
            <select v-model.number="quotaUnit" class="input" aria-label="単位" style="max-width: 110px">
              <option v-for="u in quotaUnits" :key="u.label" :value="u.factor">{{ u.label }}</option>
            </select>
          </div>
        </div>
        <div v-if="quotaUsed !== null" class="stack">
          <span class="muted small">
            使用量: {{ formatBytes(quotaUsed) }}<template v-if="savedQuota"> / {{ formatBytes(savedQuota) }}（{{ quotaPercent }}%）</template>
          </span>
          <progress v-if="savedQuota" :value="quotaUsed" :max="savedQuota" aria-label="容量の使用率" style="width: 100%"></progress>
        </div>
        <div class="row">
          <button type="submit" class="btn btn--primary" :disabled="quotaSaving || quotaBytes === savedQuota">
            <span v-if="quotaSaving" class="spinner" aria-hidden="true"></span>保存
          </button>
        </div>
      </form>
    </section>

    <!-- Advanced -->
    <section v-else id="panel-advanced" role="tabpanel" aria-labelledby="tab-advanced" class="card">
      <div class="card__head"><h2 class="card__title">Advanced</h2></div>
      <div class="table-wrap">
        <table class="table">
          <thead>
            <tr><th>機能</th><th>状態</th><th>備考</th></tr>
          </thead>
          <tbody>
            <tr v-for="a in advancedItems" :key="a.label">
              <td>{{ a.label }}</td>
              <td><span class="badge">{{ a.badge }}</span></td>
              <td class="muted">{{ a.note }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <!-- Upload panel -->
    <aside v-if="uploads.length" class="uploads" aria-label="アップロード">
      <div class="card__head">
        <h2 class="card__title">アップロード {{ doneCount }} / {{ uploads.length }}</h2>
        <div class="row">
          <button v-if="doneCount > 0" type="button" class="btn btn--ghost btn--sm" @click="clearFinished">完了を消去</button>
          <button type="button" class="icon-btn" :aria-label="uploadsOpen ? '折りたたむ' : '展開'" :aria-expanded="uploadsOpen" @click="uploadsOpen = !uploadsOpen">
            <Icon :name="uploadsOpen ? 'chevron-down' : 'chevron-up'" :size="16" />
          </button>
          <button type="button" class="icon-btn" aria-label="閉じる（進行中はキャンセル）" @click="closeUploads"><Icon name="x" :size="16" /></button>
        </div>
      </div>
      <div v-show="uploadsOpen" class="uploads__list">
        <div v-for="u in uploads" :key="u.id" class="upload-item">
          <div class="upload-item__row">
            <span class="upload-item__name" :title="u.key">{{ u.key }}</span>
            <span class="row" style="gap: 6px">
              <span class="small" :class="{ muted: u.status !== 'error' }" :style="u.status === 'error' ? { color: 'var(--color-error)' } : null">{{ statusText(u) }}</span>
              <button v-if="u.status === 'error' || u.status === 'canceled'" type="button" class="icon-btn" :aria-label="`${u.key} を再試行`" @click="retryUpload(u)">
                <Icon name="refresh" :size="14" />
              </button>
              <button v-if="u.status === 'queued' || u.status === 'uploading'" type="button" class="icon-btn" :aria-label="`${u.key} をキャンセル`" @click="cancelUpload(u)">
                <Icon name="x" :size="14" />
              </button>
            </span>
          </div>
          <div class="progress progress--thin" role="progressbar" aria-valuemin="0" aria-valuemax="100" :aria-valuenow="pct(u)" :aria-label="u.key">
            <div class="progress__bar" :style="{ width: pct(u) + '%' }"></div>
          </div>
        </div>
      </div>
    </aside>
  </div>
</template>