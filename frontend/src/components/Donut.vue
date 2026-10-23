<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
<script setup>
import { computed } from 'vue'

const props = defineProps({
  pct: { type: Number, default: 0 },
  caption: { type: String, default: '' },
})

const p = computed(() => Math.max(0, Math.min(100, Number(props.pct) || 0)))
</script>

<template>
  <svg class="donut" viewBox="0 0 100 100" role="img" :aria-label="`${caption} ${p}%`">
    <circle class="donut__track" cx="50" cy="50" r="42" fill="none" stroke-width="10" />
    <circle
      v-if="p > 0"
      class="donut__value"
      cx="50"
      cy="50"
      r="42"
      fill="none"
      stroke="var(--chart-a)"
      stroke-width="10"
      stroke-linecap="round"
      pathLength="100"
      :stroke-dasharray="`${p} ${100 - p}`"
      transform="rotate(-90 50 50)"
    />
    <text class="donut__pct" x="50" y="53" text-anchor="middle">{{ p }}%</text>
    <text class="donut__cap" x="50" y="67" text-anchor="middle">{{ caption }}</text>
  </svg>
</template>