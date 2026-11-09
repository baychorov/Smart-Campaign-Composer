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
// The counter shows the typed length only (user decision, PLAN step 15c).
// The backend's worst case (variables filled with the audience's longest
// values) still turns it red and blocks approval, with its own message.
const worst = computed(() => (props.stats && !props.statsStale ? props.stats.worst_case_runes : null))
const over = computed(() => count.value > limit.value || (worst.value !== null && worst.value > limit.value))
const inputId = computed(() => `msg-${props.field}`)
</script>

<template>
  <div class="field">
    <div class="field-head">
      <label :for="inputId">{{ label }}</label>
      <span class="counter num" :class="{ over }" :data-test="`counter-${field}`">
        {{ count }} / {{ limit }}
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

    <ul v-if="errors.length" class="errors">
      <li v-for="(e, i) in errors" :key="i">{{ e.message }}</li>
    </ul>
  </div>
</template>

<style scoped>
.field {
  display: flex;
  flex-direction: column;
  gap: 0.375rem;
  padding: 0.75rem 0.875rem;
}
.field + .field {
  border-top: 1px solid var(--line);
}
.field-head {
  display: flex;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 0.375rem;
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
  padding: 0.5rem 0.625rem;
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
  box-shadow: 0 0 0 0.1875rem var(--accent-soft);
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
  width: 0.4375rem;
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
.errors {
  margin: 0;
  padding-left: 1.125rem;
  font-size: var(--text-sm);
  color: var(--bad);
}
</style>
