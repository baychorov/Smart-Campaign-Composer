<script setup lang="ts">
import { computed } from 'vue'
import type { FieldStats, Issue, MessageField } from '../api/types'
import { LIMITS, runes } from '../lib/text'

const props = defineProps<{
  field: MessageField
  label: string
  value: string
  /** Text is arriving from the model; shown read-only. */
  streaming: boolean
  /** Show the typing caret after the text. */
  caret: boolean
  editable: boolean
  /** Backend stats for the stored text; stale while the editor is dirty. */
  stats?: FieldStats
  statsStale: boolean
  errors: Issue[]
}>()

const emit = defineEmits<{ update: [value: string] }>()

const limit = computed(() => LIMITS[props.field])
const count = computed(() => runes(props.value.trim()))
/** Worst case from the backend: variables filled with the audience's longest values. */
const worst = computed(() => (props.stats && !props.statsStale ? props.stats.worst_case_runes : null))
const over = computed(() => count.value > limit.value || (worst.value !== null && worst.value > limit.value))
const fill = computed(() => Math.min(100, ((worst.value ?? count.value) / limit.value) * 100))
const inputId = computed(() => `msg-${props.field}`)
</script>

<template>
  <div class="field">
    <div class="field-head">
      <label :for="inputId">{{ label }}</label>
      <span class="counter num" :class="{ over }" :data-test="`counter-${field}`">
        {{ count }}<template v-if="worst !== null && worst !== stats?.runes"> · en kötü durum {{ worst }}</template> / {{ limit }}
      </span>
    </div>

    <div v-if="streaming" :id="inputId" class="stream" :class="{ waiting: !value }" aria-live="polite">
      {{ value || 'hazırlanıyor…' }}<span v-if="caret" class="caret" aria-hidden="true"></span>
    </div>
    <input
      v-else-if="field === 'email_subject'"
      :id="inputId"
      type="text"
      :value="value"
      :readonly="!editable"
      @input="emit('update', ($event.target as HTMLInputElement).value)"
    />
    <textarea
      v-else
      :id="inputId"
      :value="value"
      :readonly="!editable"
      :rows="field === 'sms' ? 3 : 8"
      @input="emit('update', ($event.target as HTMLTextAreaElement).value)"
    ></textarea>

    <div class="meter" aria-hidden="true"><i :class="{ over }" :style="{ width: fill + '%' }"></i></div>
    <p v-if="statsStale && !streaming" class="hint">En kötü durum uzunluğu kaydedince hesaplanır.</p>
    <ul v-if="errors.length" class="errors">
      <li v-for="(e, i) in errors" :key="i">{{ e.message }}</li>
    </ul>
  </div>
</template>

<style scoped>
.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 12px 14px;
}
.field + .field {
  border-top: 1px solid var(--line);
}
.field-head {
  display: flex;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 6px;
  font-size: var(--text-xs);
  color: var(--muted);
}
.field-head label {
  font-weight: 500;
  color: var(--ink-soft);
}
.counter {
  font-family: var(--font-mono);
  color: var(--ok);
}
.counter.over {
  color: var(--bad);
  font-weight: 500;
}
textarea,
input,
.stream {
  width: 100%;
  border: 1px solid var(--line);
  border-radius: var(--radius-sm);
  padding: 8px 10px;
  background: var(--surface-muted);
  font-size: var(--text-md);
  line-height: 1.55;
  resize: vertical;
}
textarea:focus,
input:focus {
  outline: none;
  border-color: var(--accent);
  background: var(--surface);
  box-shadow: 0 0 0 3px var(--accent-soft);
}
.stream {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  min-height: 2.8em;
}
.stream.waiting {
  color: var(--muted);
  font-style: italic;
}
.caret {
  display: inline-block;
  width: 7px;
  height: 1.05em;
  margin-left: 1px;
  background: var(--accent);
  vertical-align: -2px;
  animation: blink 0.9s infinite;
}
@keyframes blink {
  50% {
    opacity: 0.2;
  }
}
.meter {
  height: 3px;
  background: var(--line);
  border-radius: 2px;
  overflow: hidden;
}
.meter i {
  display: block;
  height: 100%;
  background: var(--ok);
  transition: width 0.2s;
}
.meter i.over {
  background: var(--bad);
}
.hint {
  margin: 0;
  font-size: var(--text-xs);
  color: var(--muted);
}
.errors {
  margin: 0;
  padding-left: 18px;
  font-size: var(--text-sm);
  color: var(--bad);
}
</style>
