// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

export function formatBytes(n, digits = 1) {
  const v = Number(n)
  if (!Number.isFinite(v) || v < 0) return '—'
  if (v < 1024) return `${v} B`
  const units = ['KiB', 'MiB', 'GiB', 'TiB', 'PiB']
  let x = v / 1024
  let i = 0
  while (x >= 1024 && i < units.length - 1) { x /= 1024; i++ }
  return `${x.toFixed(x >= 100 ? 0 : digits)} ${units[i]}`
}

function toDate(v) {
  if (!v) return null
  const d = new Date(v)
  // Go's zero time (0001-01-01) means "never"
  if (Number.isNaN(d.getTime()) || d.getFullYear() <= 1) return null
  return d
}

export function formatDate(v) {
  const d = toDate(v)
  if (!d) return '—'
  return d.toLocaleString('ja-JP', { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })
}

export function timeAgo(v) {
  const d = toDate(v)
  if (!d) return '—'
  const s = Math.max(0, Math.round((Date.now() - d.getTime()) / 1000))
  if (s < 5) return 'たった今'
  if (s < 60) return `${s}秒前`
  if (s < 3600) return `${Math.floor(s / 60)}分前`
  if (s < 86400) return `${Math.floor(s / 3600)}時間前`
  return `${Math.floor(s / 86400)}日前`
}

/** "1h2m3.456789s" -> "1h2m3s" */
export function shortDuration(s) {
  if (!s || typeof s !== 'string') return '—'
  return s.replace(/(\d+)\.\d+s/, '$1s').replace(/^0s$/, '0s')
}

export function percent(used, total) {
  const u = Number(used)
  const t = Number(total)
  if (!Number.isFinite(u) || !Number.isFinite(t) || t <= 0) return 0
  return Math.max(0, Math.min(100, Math.round((u / t) * 100)))
}