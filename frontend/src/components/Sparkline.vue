<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
<script setup>
import { computed } from 'vue'

const props = defineProps({
  a: { type: Array, default: () => [] },
  b: { type: Array, default: () => [] },
  size: { type: Number, default: 30 }, // number of slots in the window
  label: { type: String, default: 'スループットの推移' },
})

const W = 300
const H = 100
const PAD = 6

const max = computed(() => Math.max(0.001, ...props.a, ...props.b))

function pts(arr) {
  const n = arr.length
  const offset = Math.max(0, props.size - n)
  return arr.map((v, i) => {
    const x = ((i + offset) / Math.max(1, props.size - 1)) * W
    const y = H - PAD - (Math.max(0, v) / max.value) * (H - PAD * 2)
    return [x, y]
  })
}

function line(arr) {
  return pts(arr).map(([x, y], i) => `${i === 0 ? 'M' : 'L'}${x.toFixed(1)} ${y.toFixed(1)}`).join(' ')
}

function area(arr) {
  const p = pts(arr)
  if (p.length < 2) return ''
  return `${line(arr)} L${p[p.length - 1][0].toFixed(1)} ${H} L${p[0][0].toFixed(1)} ${H} Z`
}

const lineA = computed(() => (props.a.length > 1 ? line(props.a) : ''))
const lineB = computed(() => (props.b.length > 1 ? line(props.b) : ''))
const areaA = computed(() => area(props.a))
const areaB = computed(() => area(props.b))
</script>

<template>
  <svg class="spark" :viewBox="`0 0 ${W} ${H}`" preserveAspectRatio="none" role="img" :aria-label="label">
    <line class="spark__grid" x1="0" :y1="H * 0.25" :x2="W" :y2="H * 0.25" />
    <line class="spark__grid" x1="0" :y1="H * 0.5" :x2="W" :y2="H * 0.5" />
    <line class="spark__grid" x1="0" :y1="H * 0.75" :x2="W" :y2="H * 0.75" />
    <path v-if="areaB" class="spark__area--b" :d="areaB" />
    <path v-if="areaA" class="spark__area--a" :d="areaA" />
    <path v-if="lineB" class="spark__line spark__line--b" :d="lineB" />
    <path v-if="lineA" class="spark__line spark__line--a" :d="lineA" />
  </svg>
</template>