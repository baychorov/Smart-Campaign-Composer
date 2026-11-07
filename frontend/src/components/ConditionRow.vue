<script setup lang="ts">
import { ref, watch } from 'vue'
import type { Condition } from '../api/types'
import { EVENT_LABELS } from '../lib/labels'

// One condition as an editable chip: cities can be removed one by one,
// ages and day windows can be changed, the whole condition can be removed.

const props = defineProps<{
  condition: Condition
  /** Stable id prefix for the number input. */
  inputId: string
  editable: boolean
  /** False when this is the rule's last condition (an empty rule is invalid). */
  canRemove: boolean
}>()

const emit = defineEmits<{
  remove: []
  removeValue: [index: number]
  setNumber: [n: number]
}>()

const numberValue = () => (props.condition.type === 'event' ? props.condition.within_days : props.condition.value) ?? 0
// A local copy, so typing does not send a request per keystroke; the change
// is sent on blur or Enter.
const local = ref(numberValue())
watch(() => numberValue(), (n) => (local.value = n))

function commit() {
  const n = Math.round(Number(local.value))
  if (!Number.isFinite(n) || n === numberValue()) {
    local.value = numberValue()
    return
  }
  emit('setNumber', n)
}
</script>

<template>
  <div class="cond">
    <template v-if="condition.field === 'city'">
      <span class="key">Şehir</span>
      <span class="op">{{ condition.op === 'not_in' ? 'dışında:' : '' }}</span>
      <span v-for="(v, i) in condition.values" :key="v" class="token">
        {{ v }}
        <button
          v-if="editable && (canRemove || (condition.values?.length ?? 0) > 1)"
          type="button"
          class="token-x"
          :aria-label="`${v} değerini kaldır`"
          @click="emit('removeValue', i)"
        >×</button>
      </span>
    </template>

    <template v-else-if="condition.field === 'age'">
      <span class="key">Yaş</span>
      <input
        :id="inputId"
        v-model.number="local"
        type="number"
        min="13"
        max="100"
        :disabled="!editable"
        aria-label="Yaş sınırı"
        @change="commit"
        @keydown.enter.prevent="commit"
      />
      <span class="op">{{ condition.op === 'gte' ? 've üzeri' : 've altı' }}</span>
    </template>

    <template v-else-if="condition.event">
      <span class="key">{{ EVENT_LABELS[condition.event][condition.occurred ? 0 : 1] }}</span>
      <span class="op">son</span>
      <input
        :id="inputId"
        v-model.number="local"
        type="number"
        min="1"
        max="365"
        :disabled="!editable"
        aria-label="Gün sayısı"
        @change="commit"
        @keydown.enter.prevent="commit"
      />
      <span class="op">gün</span>
    </template>

    <button
      v-if="editable && canRemove"
      type="button"
      class="remove"
      title="Koşulu kaldır"
      aria-label="Koşulu kaldır"
      @click="emit('remove')"
    >×</button>
  </div>
</template>

<style scoped>
.cond {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  padding: 6px 6px 6px 12px;
  border-radius: var(--radius-sm);
  background: var(--accent-soft);
  font-size: var(--text-md);
  min-height: 38px;
}
.key {
  font-weight: 500;
  color: var(--ink);
}
.op {
  color: var(--muted);
  font-size: var(--text-sm);
}
.token {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 5px;
  padding: 0 4px 0 8px;
  font-size: var(--text-sm);
}
.token-x {
  border: 0;
  background: none;
  color: var(--muted);
  padding: 0 3px;
  font-size: 14px;
  line-height: 1;
}
.token-x:hover {
  color: var(--bad);
}
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
.remove {
  margin-left: auto;
  border: 0;
  background: none;
  color: var(--muted);
  width: 28px;
  height: 28px;
  border-radius: 6px;
  font-size: 17px;
  line-height: 1;
}
.remove:hover {
  background: var(--surface);
  color: var(--bad);
}
</style>
