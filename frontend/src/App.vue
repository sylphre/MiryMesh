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
import { api, clearToken, getToken } from './lib/api.js'

const route = useRoute()
const router = useRouter()

const ready = ref(false)
router.isReady().then(() => { ready.value = true })

const navOpen = ref(false)
const bare = computed(() => route.path === '/login' || route.path === '/setup')

const items = [
  { to: '/', label: 'Dashboard', icon: 'dashboard', exact: true },
  { to: '/buckets', label: 'Buckets', icon: 'bucket' },
  { to: '/users', label: 'Users', icon: 'users' },
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

// Release notice: the Master polls GitHub Releases; the console only shows the cached result.
const DISMISS_KEY = 'mirymesh_update_dismissed'
const update = ref(null)
const dismissed = ref('')
try { dismissed.value = localStorage.getItem(DISMISS_KEY) || '' } catch { /* storage unavailable */ }

const showUpdate = computed(
  () => !!update.value && !!update.value.update_available && update.value.latest !== dismissed.value,
)
const releaseUrl = computed(() => {
  const u = update.value && update.value.release_url
  return typeof u === 'string' && u.startsWith('https://') ? u : ''
})

async function loadUpdate() {
  if (bare.value || !getToken()) return
  try {
    update.value = await api('/api/admin/update', { redirectOn401: false })
  } catch {
    /* the notice is optional, so errors stay silent */
  }
}

function dismissUpdate() {
  if (!update.value) return
  dismissed.value = update.value.latest || ''
  try { localStorage.setItem(DISMISS_KEY, dismissed.value) } catch { /* storage unavailable */ }
}

let updateTimer = null
watch(bare, (isBare) => { if (!isBare) loadUpdate() }, { immediate: true })
onMounted(() => { updateTimer = setInterval(loadUpdate, 30 * 60 * 1000) })
onBeforeUnmount(() => { if (updateTimer) clearInterval(updateTimer) })
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
          <div v-if="showUpdate" class="alert alert--info" role="status" style="margin-bottom: 16px">
            <Icon name="refresh" />
            <div class="alert__body">
              新しいバージョン <strong>{{ update.latest }}</strong> が公開されています（現在 {{ update.current }}）。
              Master は <code>install.sh --update</code> で更新できます。DataNode は Nodes 画面からワンクリックで更新できます。
              <a v-if="releaseUrl" :href="releaseUrl" target="_blank" rel="noopener noreferrer">リリースノート</a>
            </div>
            <button type="button" class="btn btn--sm btn--ghost" @click="dismissUpdate">閉じる</button>
          </div>
          <router-view :key="route.path" />
        </main>
      </div>
    </div>
  </template>

  <ToastHost />
  <ConfirmHost />
</template>