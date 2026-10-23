// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

const TOKEN_KEY = 'mirymesh_admin_token'

export class ApiError extends Error {
  constructor(message, status = 0) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

export function getToken() {
  try { return localStorage.getItem(TOKEN_KEY) || '' } catch { return '' }
}

export function setToken(token) {
  try { localStorage.setItem(TOKEN_KEY, token) } catch { /* storage unavailable */ }
}

export function clearToken() {
  try { localStorage.removeItem(TOKEN_KEY) } catch { /* storage unavailable */ }
}

function authHeaders() {
  const t = getToken()
  return t ? { 'X-Admin-Token': t } : {}
}

function handleUnauthorized() {
  clearToken()
  const p = window.location.pathname
  if (p !== '/login' && p !== '/setup') window.location.assign('/login')
}

/**
 * fetch wrapper for /api/admin/*.
 * - objects/arrays are sent as JSON, Blob/File/FormData/string are sent as-is
 * - 401 clears the token and redirects to /login (disable with redirectOn401: false)
 * - non-2xx throws ApiError carrying the response text
 */
export async function api(path, { method = 'GET', body, headers = {}, signal, auth = true, redirectOn401 = true, raw = false } = {}) {
  const h = { ...(auth ? authHeaders() : {}), ...headers }
  let payload = body
  const isBinary = body instanceof Blob || body instanceof FormData || body instanceof ArrayBuffer
  if (body !== undefined && body !== null && typeof body === 'object' && !isBinary) {
    payload = JSON.stringify(body)
    h['Content-Type'] = 'application/json'
  }

  let res
  try {
    res = await fetch(path, { method, headers: h, body: payload, signal })
  } catch (e) {
    if (e && e.name === 'AbortError') throw e
    throw new ApiError('サーバーに接続できません', 0)
  }

  if (res.status === 401 && auth && redirectOn401) {
    handleUnauthorized()
    throw new ApiError('unauthorized', 401)
  }
  if (!res.ok) {
    const text = (await res.text().catch(() => '')).trim()
    throw new ApiError(text || res.statusText || `HTTP ${res.status}`, res.status)
  }
  if (raw) return res
  if (res.status === 204) return null
  const ct = res.headers.get('content-type') || ''
  return ct.includes('json') ? res.json() : res.text()
}

export function objectUrl(bucket, key) {
  return `/api/admin/buckets/${encodeURIComponent(bucket)}/object?key=${encodeURIComponent(key)}`
}

/** Upload one file with progress. Rejects with AbortError when the signal aborts. */
export function uploadObject(bucket, key, file, { onProgress, signal } = {}) {
  return new Promise((resolve, reject) => {
    if (signal && signal.aborted) {
      reject(new DOMException('Aborted', 'AbortError'))
      return
    }
    const xhr = new XMLHttpRequest()
    xhr.open('PUT', objectUrl(bucket, key))
    const t = getToken()
    if (t) xhr.setRequestHeader('X-Admin-Token', t)
    if (file.type) xhr.setRequestHeader('Content-Type', file.type)
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable && onProgress) onProgress(e.loaded, e.total)
    }
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve()
      } else if (xhr.status === 401) {
        handleUnauthorized()
        reject(new ApiError('unauthorized', 401))
      } else {
        reject(new ApiError((xhr.responseText || '').trim() || `HTTP ${xhr.status}`, xhr.status))
      }
    }
    xhr.onerror = () => reject(new ApiError('ネットワークエラー', 0))
    xhr.onabort = () => reject(new DOMException('Aborted', 'AbortError'))
    if (signal) signal.addEventListener('abort', () => xhr.abort(), { once: true })
    xhr.send(file)
  })
}

/** Download an object through the authenticated admin API and hand it to the browser. */
export async function downloadObject(bucket, key) {
  const res = await api(objectUrl(bucket, key), { raw: true })
  const blob = await res.blob()
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = key.split('/').pop() || 'download'
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 10000)
}