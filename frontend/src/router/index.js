// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
import { createRouter, createWebHistory } from 'vue-router'

const routes = [
  { path: '/static/setup.html', redirect: '/setup' },
  { path: '/static/login.html', redirect: '/login' },
  { path: '/static/index.html', redirect: '/' },
  { path: '/setup', component: () => import('../views/Setup.vue'), meta: { requiresSetup: true } },
  { path: '/login', component: () => import('../views/Login.vue') },
  { path: '/', component: () => import('../views/Dashboard.vue'), meta: { requiresAuth: true } },
  { path: '/nodes', component: () => import('../views/Nodes.vue'), meta: { requiresAuth: true } },
  { path: '/disks', component: () => import('../views/Disks.vue'), meta: { requiresAuth: true } },
  { path: '/buckets', component: () => import('../views/Buckets.vue'), meta: { requiresAuth: true } },
  { path: '/jobs', component: () => import('../views/Jobs.vue'), meta: { requiresAuth: true } },
  { path: '/settings', component: () => import('../views/Settings.vue'), meta: { requiresAuth: true } },
]

const router = createRouter({
  history: createWebHistory(),
  routes,
})

router.beforeEach(async (to) => {
  // isSetupNeeded = !IsInitialized() only
  try {
    const token = localStorage.getItem('mirymesh_admin_token') || ''
    const headers = token ? { 'X-Admin-Token': token } : {}
    const res = await fetch('/api/admin/config', { headers })
    if (!res.ok && res.status === 401) {
      // Initialized but no valid token
      if (to.path === '/setup') return true
      if (to.path !== '/login') return '/login'
      return true
    }
    const j = await res.json().catch(() => ({}))
    const initialized = !!j.initialized
    const dbConfigured = !!j.db_configured
    // Store for setup view
    sessionStorage.setItem('mirymesh_setup_hint', JSON.stringify({ dbConfigured, mode: j.mode || 'standalone' }))
    if (!initialized) {
      if (to.path !== '/setup') return '/setup'
      return true
    }
    // Initialized
    if (to.path === '/setup') return '/'
    if (to.meta.requiresAuth && !token) return '/login'
    return true
  } catch {
    return true
  }
})

export default router
