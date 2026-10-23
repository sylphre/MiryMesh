<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
<script setup>
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import BrandMark from '../components/BrandMark.vue'
import Icon from '../components/Icon.vue'
import { api, getToken, setToken, clearToken } from '../lib/api.js'

const router = useRouter()
const username = ref('')
const password = ref('')
const error = ref('')
const loading = ref(false)

onMounted(async () => {
  if (!getToken()) return
  try {
    await api('/api/admin/config', { redirectOn401: false })
    router.push('/')
  } catch {
    clearToken()
  }
})

async function submit() {
  if (loading.value) return
  loading.value = true
  error.value = ''
  try {
    const j = await api('/api/admin/login', {
      method: 'POST',
      body: { username: username.value, password: password.value },
      auth: false,
    })
    setToken(j.admin_token)
    router.push('/')
  } catch (e) {
    error.value = e.status === 401 || e.status === 403 ? 'ユーザー名またはパスワードが正しくありません' : e.message
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="auth">
    <div class="auth__card">
      <div class="auth__brand">
        <BrandMark :size="36" />
        <div>
          <div class="brand__name">MiryMesh</div>
          <div class="brand__sub">Object Storage Console</div>
        </div>
      </div>

      <h1 class="auth__title">Sign in</h1>
      <p class="muted small mt-1">Admin アカウントでログインしてください。</p>

      <div v-if="error" class="alert alert--error mt-3" role="alert">
        <Icon name="alert" />
        <div class="alert__body">{{ error }}</div>
      </div>

      <form class="stack mt-3" @submit.prevent="submit">
        <label class="field">
          <span class="field__label">Username</span>
          <input v-model="username" class="input" required minlength="3" autocomplete="username" autofocus placeholder="admin" />
        </label>
        <label class="field">
          <span class="field__label">Password</span>
          <input v-model="password" type="password" class="input" required minlength="8" autocomplete="current-password" placeholder="••••••••" />
        </label>
        <button type="submit" class="btn btn--primary btn--block" :disabled="loading">
          <span v-if="loading" class="spinner" aria-hidden="true"></span>
          Sign in
        </button>
      </form>

      <p class="muted small mt-3">未初期化の場合は <router-link class="link" to="/setup">Setup</router-link> へ。</p>
    </div>
  </div>
</template>