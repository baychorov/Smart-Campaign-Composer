<script setup lang="ts">
import { ref, watch } from 'vue'

// A small number input inside a condition chip. It keeps a local copy, so
// typing does not send a request per keystroke; the change is sent on blur
// or Enter, and an invalid or unchanged entry snaps back.

const props = defineProps<{
  value: number
  min: number
  max: number
  label: string
  disabled: boolean
  id?: string
}>()

const emit = defineEmits<{ commit: [n: number] }>()

const local = ref(props.value)
watch(
  () => props.value,
  (n) => (local.value = n),
)
// The field is disabled while the change is saved. If the backend refused
// it, the value did not change; the box shows the stored value again.
watch(
  () => props.disabled,
  (now, before) => {
    if (before && !now) local.value = props.value
  },
)

function commit() {
  // An emptied box is not 0: it snaps back like any invalid entry.
  const n = String(local.value).trim() === '' ? NaN : Math.round(Number(local.value))
  if (!Number.isFinite(n) || n === props.value) {
    local.value = props.value
    return
  }
  emit('commit', n)
}
</script>

<template>
  <input
    :id="id"
    v-model.number="local"
    type="number"
    :min="min"
    :max="max"
    :disabled="disabled"
    :aria-label="label"
    @change="commit"
    @keydown.enter.prevent="commit"
  />
</template>

<style scoped>
input {
  width: 4rem;
  padding: 0.125rem 0.375rem;
  border: 1px solid var(--line-strong);
  border-radius: 0.3125rem;
  background: var(--surface);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
}
input:focus {
  outline: none;
  border-color: var(--accent);
}
</style>
