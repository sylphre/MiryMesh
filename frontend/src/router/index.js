// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
import { createRouter, createWebHistory } from 'vue-router'
import { useClusterStore } from '../stores/cluster'

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
  const store = useClusterStore()
  const token = localStorage.getItem('mirymesh_admin_token') || ''
  // Use cached config if available, else fetch
  let cfg
  if (store.initialized !== null) {
    // Check if we need to re-fetch due to 401
    cfg = { initialized: store.initialized, dbConfigured: store.dbConfigured, mode: store.mode, status: 200 }
    // If initialized and no token, we know we need login without fetching
    if (cfg.initialized && !token && to.path !== '/login' && to.path !== '/setup') {
      return '/login'
    }
  } else {
    cfg = await store.fetchConfig()
  }
  if (cfg.status === 401) {
    if (to.path === '/setup') return true
    if (to.path !== '/login') return '/login'
    return true
  }
  if (!cfg.initialized) {
    if (to.path !== '/setup') return '/setup'
    return true
  }
  if (to.path === '/setup') return '/'
  if (to.meta.requiresAuth && !token) return '/login'
  return true
})

export default router
