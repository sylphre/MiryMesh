<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
<script setup>
</script>

<template>
  <header class="topbar">
    <h1 class="topbar__title">MiryMesh</h1>
    <nav class="topbar__nav">
      <router-link to="/" class="topbar__link">Dashboard</router-link>
      <router-link to="/nodes" class="topbar__link">Nodes</router-link>
      <router-link to="/disks" class="topbar__link">Disks</router-link>
      <router-link to="/buckets" class="topbar__link">Buckets</router-link>
      <router-link to="/jobs" class="topbar__link">Jobs</router-link>
      <router-link to="/settings" class="topbar__link">Settings</router-link>
    </nav>
  </header>
  <router-view />
</template>
