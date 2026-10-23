<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
<script setup>
import { ref, computed, watch, onMounted, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import Icon from './components/Icon.vue'
import BrandMark from './components/BrandMark.vue'
import ToastHost from './components/ToastHost.vue'
import ConfirmHost from './components/ConfirmHost.vue'
import { theme, toggleTheme } from './lib/theme.js'
import { clearToken } from './lib/api.js'

const route = useRoute()
const router = useRouter()

const ready = ref(false)
router.isReady().then(() => { ready.value = true })

const navOpen = ref(false)
const bare = computed(() => route.path === '/login' || route.path === '/setup')

const items = [
  { to: '/', label: 'Dashboard', icon: 'dashboard', exact: true },
  { to: '/buckets', label: 'Buckets', icon: 'bucket' },
  { to: '/nodes', label: 'Nodes', icon: 'server' },
  { to: '/disks', label: 'Disks', icon: 'disk' },
  { to: '/jobs', label: 'Jobs', icon: 'activity' },
  { to: '/settings', label: 'Settings', icon: 'settings' },
]

const section = computed(() =>
  items.find((i) => (i.exact ? route.path === i.to : route.path === i.to || route.path.startsWith(`${i.to}/`))),
)

watch(
  () => section.value && section.value.label,
  (label) => { document.title = label ? `${label} · MiryMesh` : 'MiryMesh' },
  { immediate: true },
)

watch(() => route.fullPath, () => { navOpen.value = false })

function onKey(e) {
  if (e.key === 'Escape' && navOpen.value) navOpen.value = false
}
onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))

function focusMain() {
  const el = document.getElementById('main')
  if (el) el.focus()
}

function logout() {
  clearToken()
  router.push('/login')
}
</script>

<template>
  <template v-if="ready">
    <router-view v-if="bare" />

    <div v-else class="app" :class="{ 'app--nav-open': navOpen }">
      <a class="skip-link" href="#main" @click.prevent="focusMain">メインコンテンツへ移動</a>

      <aside id="sidebar" class="sidebar" aria-label="サイドバー">
        <div class="sidebar__brand">
          <BrandMark />
          <div>
            <div class="brand__name">MiryMesh</div>
            <div class="brand__sub">Object Storage Console</div>
          </div>
        </div>

        <nav class="nav" aria-label="メインナビゲーション">
          <router-link v-for="it in items" :key="it.to" :to="it.to" custom v-slot="{ href, navigate, isActive, isExactActive }">
            <a
              :href="href"
              class="nav__link"
              :class="{ 'nav__link--active': it.exact ? isExactActive : isActive }"
              :aria-current="(it.exact ? isExactActive : isActive) ? 'page' : null"
              @click="navigate"
            >
              <Icon :name="it.icon" />
              <span>{{ it.label }}</span>
            </a>
          </router-link>
        </nav>

        <div class="sidebar__foot">
          <button type="button" class="sidebar__btn" @click="logout">
            <Icon name="logout" :size="16" />
            <span>Sign out</span>
          </button>
        </div>
      </aside>

      <div v-if="navOpen" class="scrim" aria-hidden="true" @click="navOpen = false"></div>

      <div class="main">
        <header class="topbar">
          <button type="button" class="icon-btn menu-btn" aria-label="メニューを開く" aria-controls="sidebar" :aria-expanded="navOpen" @click="navOpen = !navOpen">
            <Icon name="menu" />
          </button>
          <div class="crumbs">
            <span>MiryMesh</span>
            <Icon name="chevron-right" :size="14" />
            <strong>{{ section ? section.label : '' }}</strong>
          </div>
          <div class="topbar__spacer"></div>
          <button
            type="button"
            class="icon-btn"
            :aria-label="theme === 'dark' ? 'ライトテーマに切り替え' : 'ダークテーマに切り替え'"
            @click="toggleTheme"
          >
            <Icon :name="theme === 'dark' ? 'sun' : 'moon'" />
          </button>
        </header>

        <main id="main" class="content" tabindex="-1">
          <router-view :key="route.path" />
        </main>
      </div>
    </div>
  </template>

  <ToastHost />
  <ConfirmHost />
</template>