<script setup lang="ts">
import { computed } from 'vue'

// Send rate over the last updates, drawn to scale: the top line is the
// highest value shown, the bottom line is zero.

const props = defineProps<{ values: number[]; capacity: number }>()

const W = 320
const H = 72
const PAD = 6
const LABEL_W = 34

const max = computed(() => Math.max(10, ...props.values))
const points = computed(() => {
  const step = (W - PAD - LABEL_W) / Math.max(1, props.capacity - 1)
  const offset = props.capacity - props.values.length
  return props.values.map((v, i) => [PAD + (offset + i) * step, H - PAD - (v / max.value) * (H - PAD * 2)] as const)
})
const line = computed(() => points.value.map((p) => p.join(',')).join(' '))
const area = computed(() => {
  const p = points.value
  if (p.length < 2) return ''
  return `${p[0]![0]},${H - PAD} ${line.value} ${p[p.length - 1]![0]},${H - PAD}`
})
const last = computed(() => points.value[points.value.length - 1])
</script>

<template>
  <svg :viewBox="`0 0 ${W} ${H}`" class="spark" role="img" :aria-label="`Gönderim hızı, en yüksek ${max.toFixed(0)} mesaj/sn`">
    <line :x1="PAD" :x2="W - LABEL_W" :y1="PAD" :y2="PAD" class="grid" stroke-dasharray="2 4" />
    <line :x1="PAD" :x2="W - LABEL_W" :y1="H - PAD" :y2="H - PAD" class="grid" />
    <text :x="W - LABEL_W + 6" :y="PAD + 4" class="label">{{ max.toFixed(0) }}</text>
    <text :x="W - LABEL_W + 6" :y="H - PAD + 3" class="label">0</text>
    <template v-if="points.length >= 2">
      <polygon :points="area" class="area" />
      <polyline :points="line" class="line" />
    </template>
    <circle v-if="last" :cx="last[0]" :cy="last[1]" r="3.5" class="dot" />
  </svg>
</template>

<style scoped>
/* Scales with the card width; the viewBox keeps the proportions. */
.spark {
  display: block;
  width: 100%;
  height: auto;
}
.grid {
  stroke: var(--line);
}
.label {
  fill: var(--muted);
  font-family: var(--font-mono);
  font-size: 10px;
}
.area {
  fill: var(--accent-soft);
}
.line {
  fill: none;
  stroke: var(--accent);
  stroke-width: 1.8;
  stroke-linejoin: round;
}
.dot {
  fill: var(--accent);
}
</style>
