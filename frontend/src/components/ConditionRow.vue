<script setup lang="ts">
import type { Condition } from '../api/types'
import { EVENT_LABELS, formatDay } from '../lib/labels'
import type { NumberField as Field } from '../lib/rule'
import NumberField from './NumberField.vue'

// One condition as an editable chip: cities can be removed one by one,
// ages, day windows and counts can be changed, the whole condition can be
// removed. Calendar dates and the order of events are shown, not edited.

defineProps<{
  condition: Condition
  /** Stable id prefix for the number inputs. */
  inputId: string
  editable: boolean
  /** False when this is the rule's last condition (an empty rule is invalid). */
  canRemove: boolean
}>()

const emit = defineEmits<{
  remove: []
  removeValue: [index: number]
  setNumber: [field: Field, n: number]
}>()
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
      <NumberField
        :id="inputId"
        :value="condition.value ?? 0"
        :min="13"
        :max="100"
        :disabled="!editable"
        label="Yaş sınırı"
        @commit="(n) => emit('setNumber', 'value', n)"
      />
      <span class="op">{{ condition.op === 'gte' ? 've üzeri' : 've altı' }}</span>
    </template>

    <template v-else-if="condition.event">
      <span class="key">{{ EVENT_LABELS[condition.event][condition.occurred ? 0 : 1] }}</span>
      <template v-if="condition.min_count">
        <span class="op">en az</span>
        <NumberField
          :id="inputId + '-count'"
          :value="condition.min_count"
          :min="2"
          :max="100"
          :disabled="!editable"
          label="En az kaç kez"
          @commit="(n) => emit('setNumber', 'min_count', n)"
        />
        <span class="op">kez,</span>
      </template>
      <template v-if="condition.within_days !== undefined">
        <span class="op">son</span>
        <NumberField
          :id="inputId"
          :value="condition.within_days"
          :min="1"
          :max="365"
          :disabled="!editable"
          label="Gün sayısı"
          @commit="(n) => emit('setNumber', 'within_days', n)"
        />
        <span class="op">gün</span>
      </template>
      <span v-else-if="condition.from && condition.to" class="token dates" data-test="dates">
        {{ condition.from === condition.to ? formatDay(condition.from) : `${formatDay(condition.from)} – ${formatDay(condition.to)}` }}
      </span>
      <template v-if="condition.then">
        <span class="op">→ sonra</span>
        <span class="key then" data-test="then">{{ EVENT_LABELS[condition.then.event][condition.then.occurred ? 0 : 1] }}</span>
      </template>
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
  gap: 0.375rem;
  padding: 0.375rem 0.375rem 0.375rem 0.75rem;
  border-radius: var(--radius-sm);
  background: var(--accent-soft);
  font-size: var(--text-md);
  min-height: 2.375rem;
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
  gap: 0.125rem;
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 0.3125rem;
  padding: 0 0.25rem 0 0.5rem;
  font-size: var(--text-sm);
}
.token-x {
  border: 0;
  background: none;
  color: var(--muted);
  padding: 0 0.1875rem;
  font-size: 0.875rem;
  line-height: 1;
}
.token-x:hover {
  color: var(--bad);
}
.dates {
  padding: 0 0.5rem;
}
.then {
  font-weight: 400;
}
.remove {
  margin-left: auto;
  border: 0;
  background: none;
  color: var(--muted);
  width: 1.75rem;
  height: 1.75rem;
  border-radius: 0.375rem;
  font-size: 1.0625rem;
  line-height: 1;
}
.remove:hover {
  background: var(--surface);
  color: var(--bad);
}
</style>
