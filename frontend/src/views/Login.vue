<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
<script setup>
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'

const router = useRouter()
const username = ref('')
const password = ref('')
const error = ref('')
const loading = ref(false)

onMounted(async () => {
  const t = localStorage.getItem('mirymesh_admin_token')
  if (t) {
    try {
      const r = await fetch('/api/admin/config', { headers: { 'X-Admin-Token': t } })
      if (r.ok) router.push('/')
      else localStorage.removeItem('mirymesh_admin_token')
    } catch {
      localStorage.removeItem('mirymesh_admin_token')
    }
  }
})

async function submit() {
  loading.value = true
  error.value = ''
  try {
    const res = await fetch('/api/admin/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: username.value, password: password.value }),
    })
    if (!res.ok) throw new Error('invalid credentials')
    const j = await res.json()
    localStorage.setItem('mirymesh_admin_token', j.admin_token)
    router.push('/')
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <main class="container container--narrow">
    <div class="card">
      <h2>Login</h2>
      <p class="caption" style="margin-top: 4px;">Admin アカウントでログインしてください。</p>
      <div v-if="error" class="state--error" style="margin-top: 16px;">{{ error }}</div>
      <form @submit.prevent="submit" class="form form--stack" style="margin-top: 24px;">
        <label>Username <input v-model="username" class="input" required minlength="3" placeholder="admin" /></label>
        <label>Password <input v-model="password" type="password" class="input" required minlength="8" placeholder="••••••••" /></label>
        <button type="submit" class="btn btn--primary" :disabled="loading">{{ loading ? '...' : 'ログイン' }}</button>
      </form>
      <p class="caption" style="margin-top: 16px;">未初期化の場合は <router-link to="/setup">Setup</router-link> へ。</p>
    </div>
  </main>
</template>
