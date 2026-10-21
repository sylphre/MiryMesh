// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
import { defineStore } from 'pinia'
import { ref } from 'vue'

export const useClusterStore = defineStore('cluster', () => {
  const cluster = ref(null)
  const metrics = ref(null)
  const loading = ref(false)
  const error = ref('')

  async function fetchCluster() {
    loading.value = true
    error.value = ''
    try {
      const token = localStorage.getItem('mirymesh_admin_token') || ''
      const headers = token ? { 'X-Admin-Token': token } : {}
      const [cRes, mRes] = await Promise.all([
        fetch('/api/admin/cluster', { headers }),
        fetch('/api/admin/metrics', { headers }),
      ])
      if (!cRes.ok) throw new Error(await cRes.text())
      cluster.value = await cRes.json()
      if (mRes.ok) metrics.value = await mRes.json()
    } catch (e) {
      error.value = e.message
      if (e.message.includes('unauthorized') || e.message.includes('401')) {
        localStorage.removeItem('mirymesh_admin_token')
        location.href = '/login'
      }
    } finally {
      loading.value = false
    }
  }

  return { cluster, metrics, loading, error, fetchCluster }
})
