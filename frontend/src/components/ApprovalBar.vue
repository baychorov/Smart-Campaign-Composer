<script setup lang="ts">
import { formatCount } from '../lib/labels'

// The fixed bar at the bottom of the draft: channel choice (D-CHANNEL), the
// explicit acknowledgement of a partial rule (D-PARTIAL) and the approve
// button with the reasons it is disabled.

const props = defineProps<{
  smsCount: number
  emailCount: number
  partial: boolean
  reasons: string[]
  busy: boolean
}>()

// defineModel: two-way bound values (v-model on the parent).
const channels = defineModel<{ sms: boolean; email: boolean }>('channels', { required: true })
const partialAck = defineModel<boolean>('partialAck', { required: true })

const emit = defineEmits<{ approve: [] }>()

const total = () => (channels.value.sms ? props.smsCount : 0) + (channels.value.email ? props.emailCount : 0)
const setChannel = (k: 'sms' | 'email', v: boolean) => (channels.value = { ...channels.value, [k]: v })
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

      <label v-if="partial" class="ack">
        <input v-model="partialAck" type="checkbox" data-test="partial-ack" />
        Uygulanmayan kriterler olmadan devam et
      </label>

      <ul class="reasons" aria-live="polite">
        <li v-for="r in reasons" :key="r">{{ r }}</li>
      </ul>

      <button type="button" class="btn btn-primary btn-lg" data-test="approve" :disabled="reasons.length > 0 || busy" @click="emit('approve')">
        <span v-if="busy" class="spinner"></span>
        Onayla ve gönder · {{ formatCount(total()) }} mesaj
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
  backdrop-filter: blur(6px);
  border-top: 1px solid var(--line);
  box-shadow: var(--shadow-bar);
  padding-bottom: env(safe-area-inset-bottom, 0px);
}
.bar-inner {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px 18px;
  padding-top: 12px;
  padding-bottom: 12px;
}
.channels {
  display: flex;
  gap: 18px;
  border: 0;
  margin: 0;
  padding: 0;
}
label {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  font-size: var(--text-md);
}
input[type='checkbox'] {
  width: 16px;
  height: 16px;
  accent-color: var(--accent);
}
.ack {
  background: var(--warn-soft);
  border: 1px solid color-mix(in srgb, var(--warn) 25%, transparent);
  padding: 5px 12px;
  border-radius: var(--radius-sm);
  font-size: var(--text-sm);
}
.reasons {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 6px;
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
  border-radius: 999px;
  padding: 2px 10px;
}
</style>
