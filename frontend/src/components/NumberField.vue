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

function commit() {
  const n = Math.round(Number(local.value))
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
  width: 64px;
  padding: 2px 6px;
  border: 1px solid var(--line-strong);
  border-radius: 5px;
  background: var(--surface);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
}
input:focus {
  outline: none;
  border-color: var(--accent);
}
</style>
