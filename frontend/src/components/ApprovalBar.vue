<script setup lang="ts">
import { computed } from 'vue'
import { formatCount } from '../lib/labels'
import { HOURS, joinSendAt, parseMinute, splitSendAt } from '../lib/schedule'

// The fixed bar at the bottom of the draft: channel choice (D-CHANNEL), the
// send time (PLAN step 15a: empty sends now; a time the model read from the
// brief arrives filled in), the explicit acknowledgement of a partial rule
// (D-PARTIAL) and the approve button with the reasons it is disabled.

const props = defineProps<{
  smsCount: number
  emailCount: number
  partial: boolean
  reasons: string[]
  busy: boolean
  /** Why the chosen send time cannot be used; shown in red. */
  timeIssue?: string | null
}>()

// defineModel: two-way bound values (v-model on the parent).
const channels = defineModel<{ sms: boolean; email: boolean }>('channels', { required: true })
const partialAck = defineModel<boolean>('partialAck', { required: true })
/** A datetime-local value in Turkey time; empty means send now. */
const sendAt = defineModel<string>('sendAt', { required: true })

const emit = defineEmits<{ approve: [] }>()

const total = () => (channels.value.sms ? props.smsCount : 0) + (channels.value.email ? props.emailCount : 0)
const setChannel = (k: 'sms' | 'email', v: boolean) => (channels.value = { ...channels.value, [k]: v })

// Date, hour and minute are separate fields so the time is always 24-hour
// (lib/schedule.ts). Clearing the date means "send now".
const parts = computed(() => splitSendAt(sendAt.value))
function setPart(part: 'date' | 'hour' | 'minute', v: string) {
  const p = { ...parts.value, [part]: v }
  sendAt.value = joinSendAt(p.date, p.hour, p.minute)
}

// The minute is typed (00–59). Two valid digits apply at once; on leaving
// the field a single digit is padded ("7" → "07") and anything else is put
// back to the current minute.
function typeMinute(el: HTMLInputElement) {
  const m = el.value.length === 2 ? parseMinute(el.value) : null
  if (m) setPart('minute', m)
}
function commitMinute(el: HTMLInputElement) {
  const m = parseMinute(el.value)
  if (m) setPart('minute', m)
  el.value = m ?? parts.value.minute
}
</script>

<template>
  <div class="bar" role="region" aria-label="Onay">
    <div class="container bar-inner">
      <fieldset class="channels">
        <legend class="visually-hidden">Kanallar</legend>
        <label>
          <input type="checkbox" :checked="channels.sms" data-test="ch-sms" @change="setChannel('sms', ($event.target as HTMLInputElement).checked)" />
          SMS <span class="muted num">{{ formatCount(smsCount) }}</span>
        </label>
        <label>
          <input type="checkbox" :checked="channels.email" data-test="ch-email" @change="setChannel('email', ($event.target as HTMLInputElement).checked)" />
          Email <span class="muted num">{{ formatCount(emailCount) }}</span>
        </label>
      </fieldset>

      <div class="send-at" :class="{ invalid: timeIssue }" role="group" aria-labelledby="send-at-label">
        <span id="send-at-label">Gönderim zamanı</span>
        <input
          type="date"
          aria-label="Gönderim tarihi"
          data-test="send-date"
          :value="parts.date"
          @input="setPart('date', ($event.target as HTMLInputElement).value)"
        />
        <span class="time" :class="{ off: !parts.date }">
          <select
            aria-label="Saat"
            data-test="send-hour"
            :value="parts.hour"
            :disabled="!parts.date"
            @change="setPart('hour', ($event.target as HTMLSelectElement).value)"
          >
            <option v-if="!parts.date" value="">--</option>
            <option v-for="h in HOURS" :key="h" :value="h">{{ h }}</option>
          </select>
          <span aria-hidden="true">:</span>
          <input
            type="text"
            inputmode="numeric"
            maxlength="2"
            class="minute"
            aria-label="Dakika"
            data-test="send-minute"
            placeholder="--"
            :value="parts.minute"
            :disabled="!parts.date"
            @focus="($event.target as HTMLInputElement).select()"
            @input="typeMinute($event.target as HTMLInputElement)"
            @change="commitMinute($event.target as HTMLInputElement)"
          />
        </span>
        <button v-if="sendAt" type="button" class="btn btn-sm" data-test="send-now" @click="sendAt = ''">Şimdi gönder</button>
        <span v-else class="muted now">boş: hemen gönderilir</span>
      </div>

      <label v-if="partial" class="ack">
        <input v-model="partialAck" type="checkbox" data-test="partial-ack" />
        Uygulanmayan kriterler olmadan devam et
      </label>

      <ul class="reasons" aria-live="polite">
        <li v-for="r in reasons" :key="r" :class="{ bad: r === timeIssue }">{{ r }}</li>
      </ul>

      <button type="button" class="btn btn-primary btn-lg" data-test="approve" :disabled="reasons.length > 0 || busy" @click="emit('approve')">
        <span v-if="busy" class="spinner"></span>
        {{ sendAt ? 'Onayla ve zamanla' : 'Onayla ve gönder' }} · {{ formatCount(total()) }} mesaj
      </button>
    </div>
  </div>
</template>

<style scoped>
.bar {
  position: fixed;
  left: 0;
  right: 0;
  bottom: 0;
  z-index: 15;
  background: color-mix(in srgb, var(--surface) 94%, transparent);
  backdrop-filter: blur(0.375rem);
  border-top: 1px solid var(--line);
  box-shadow: var(--shadow-bar);
  padding-bottom: env(safe-area-inset-bottom, 0px);
}
.bar-inner {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 0.625rem 1.125rem;
  padding-top: 0.75rem;
  padding-bottom: 0.75rem;
}
.channels {
  display: flex;
  gap: 1.125rem;
  border: 0;
  margin: 0;
  padding: 0;
}
label {
  display: inline-flex;
  align-items: center;
  gap: 0.4375rem;
  font-size: var(--text-md);
}
input[type='checkbox'] {
  width: 1rem;
  height: 1rem;
  accent-color: var(--accent);
}
.send-at {
  display: inline-flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 0.5rem;
  font-size: var(--text-sm);
}
.send-at input,
.send-at select {
  padding: 0.25rem 0.5rem;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-sm);
  background: var(--surface);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
}
.send-at input:focus,
.send-at select:focus {
  outline: none;
  border-color: var(--accent);
}
.time {
  display: inline-flex;
  align-items: center;
  gap: 0.25rem;
}
.time.off {
  opacity: 0.5;
}
/* The native date field keeps typing with the keyboard; only its calendar
   button gets a hover state, so it reads as clickable (Chromium, Safari;
   Firefox does not expose this button to CSS). */
.send-at input[type='date']::-webkit-calendar-picker-indicator {
  cursor: pointer;
  padding: 0.25rem;
  margin-left: 0.25rem;
  border-radius: var(--radius-sm);
  transition: background 0.15s, box-shadow 0.15s;
}
.send-at input[type='date']::-webkit-calendar-picker-indicator:hover {
  background: var(--accent-soft);
  box-shadow: 0 0 0 0.1875rem var(--accent-soft), 0 1px 4px rgba(39, 49, 66, 0.18);
}
.send-at input:hover,
.send-at select:hover:not(:disabled) {
  border-color: var(--muted);
}
.send-at select:disabled,
.send-at input:disabled {
  cursor: not-allowed;
}
.minute {
  width: 2.75rem;
  text-align: center;
}
.send-at.invalid input,
.send-at.invalid select {
  border-color: var(--bad);
}
.now {
  font-size: var(--text-xs);
}
.btn-sm {
  padding: 0.1875rem 0.625rem;
  font-size: var(--text-xs);
}
.ack {
  background: var(--warn-soft);
  border: 1px solid color-mix(in srgb, var(--warn) 25%, transparent);
  padding: 0.3125rem 0.75rem;
  border-radius: var(--radius-sm);
  font-size: var(--text-sm);
}
.reasons {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 0.375rem;
  flex: 1;
  min-width: 0;
  margin: 0;
  padding: 0;
  list-style: none;
}
.reasons li {
  font-size: var(--text-xs);
  color: var(--muted);
  background: var(--surface-muted);
  border: 1px solid var(--line);
  border-radius: 62.4375rem;
  padding: 0.125rem 0.625rem;
}
.reasons li.bad {
  font-weight: 500;
  color: var(--bad);
  background: var(--bad-soft);
  border-color: color-mix(in srgb, var(--bad) 45%, transparent);
}
</style>
