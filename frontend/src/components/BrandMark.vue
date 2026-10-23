<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
<script setup>
defineProps({ size: { type: Number, default: 28 } })
</script>

<template>
  <svg class="brand__mark" :width="size" :height="size" viewBox="0 0 32 32" aria-hidden="true" focusable="false">
    <rect width="32" height="32" rx="8" fill="#0ea5e9" />
    <path d="M9 21l7-11 7 11M9 21h14" fill="none" stroke="#04121f" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
    <circle cx="9" cy="21" r="2.6" fill="#04121f" />
    <circle cx="16" cy="10" r="2.6" fill="#04121f" />
    <circle cx="23" cy="21" r="2.6" fill="#04121f" />
  </svg>
</template>