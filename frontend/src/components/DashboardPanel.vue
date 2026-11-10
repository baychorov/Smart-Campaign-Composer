<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from '../api/client'
import type { Campaign, ChannelProgress, Progress } from '../api/types'
import { toApiError } from '../composables/useCampaign'
import { useEventStream } from '../composables/useEventStream'
import { formatCount, STATUS_LABELS } from '../lib/labels'
import { formatTurkey } from '../lib/schedule'
import Sparkline from './Sparkline.vue'

// Live sending state (R-OBS-1). The server sends the full summary about once
// a second, so a reconnect is correct with the next event; the stream closes
// after the event with done: true.

const props = defineProps<{ campaign: Campaign }>()
const emit = defineEmits<{ update: [campaign: Campaign]; reload: [] }>()

const RATE_HISTORY = 60
const progress = ref<Progress | null>(null)
const rates = ref<number[]>([])

const stream = useEventStream({
  events: ['progress'],
  terminal: [],
  onEvent: (_name, data) => {
    const p = data as Progress
    progress.value = p
    rates.value = [...rates.value, p.rate_per_second].slice(-RATE_HISTORY)
    if (p.done) stream.close()
  },
})

const connect = () => stream.connect(api.campaignEventsUrl(props.campaign.id))
onMounted(connect)

const status = computed(() => progress.value?.status ?? props.campaign.status)
const channels = computed(() =>
  (['sms', 'email'] as const).flatMap((k) => {
    const c = progress.value?.channels[k]
    return c && c.total > 0 ? [{ key: k, label: k === 'sms' ? 'SMS' : 'Email', c }] : []
  }),
)
const sum = (f: keyof ChannelProgress) => channels.value.reduce((n, ch) => n + ch.c[f], 0)
const total = computed(() => sum('total'))
const finished = computed(() => sum('sent') + sum('failed') + sum('skipped'))
const pct = (c: ChannelProgress, n: number) => (c.total ? (n / c.total) * 100 : 0)

const ERROR_LABELS: Record<string, string> = { 'consent withdrawn': 'izin geri çekildi (atlandı)' }
const errorLabel = (e: string) => ERROR_LABELS[e] ?? e

const CELLS: [keyof ChannelProgress, string][] = [
  ['pending', 'bekliyor'],
  ['sending', 'gönderiliyor'],
  ['sent', 'gönderildi'],
  ['failed', 'başarısız'],
  ['skipped', 'atlandı'],
]

// Cancelling a scheduled campaign (PLAN step 15a), in two steps. If the
// worker took the first messages in the meantime, sending wins and the
// page reloads to show it.
const confirmingCancel = ref(false)
const cancelling = ref(false)
const cancelError = ref<string | null>(null)

async function cancelSchedule() {
  cancelling.value = true
  cancelError.value = null
  try {
    const out = await api.cancel(props.campaign.id)
    confirmingCancel.value = false
    emit('update', out.campaign)
  } catch (e) {
    const err = toApiError(e)
    cancelError.value = err.message
    confirmingCancel.value = false
    if (err.code === 'sending_started' || err.code === 'not_scheduled') emit('reload')
  } finally {
    cancelling.value = false
  }
}

// Date and time are shown as separate parts so they do not run together.
const approvedAt = computed(() => {
  if (!props.campaign.approved_at) return null
  const d = new Date(props.campaign.approved_at)
  return { date: d.toLocaleDateString('tr-TR'), time: d.toLocaleTimeString('tr-TR', { hour: '2-digit', minute: '2-digit' }) }
})
</script>

<template>
  <div class="dashboard">
    <section class="card full">
      <div class="card-body">
        <div class="kpis">
          <div class="kpi">
            <span class="status-pill" :class="status" data-test="dash-status">
              <span class="dot" :class="{ live: status === 'sending' || status === 'approved' }"></span>
              {{ STATUS_LABELS[status] }}
            </span>
          </div>
          <div class="kpi">
            <div class="kpi-value num" data-test="finished">{{ formatCount(finished) }}<span class="of"> / {{ formatCount(total) }}</span></div>
            <div class="kpi-label">işlenen mesaj</div>
          </div>
          <div class="kpi">
            <div class="kpi-value num">{{ (progress?.rate_per_second ?? 0).toFixed(1) }}</div>
            <div class="kpi-label">mesaj/sn</div>
          </div>
          <div class="kpi">
            <div class="kpi-value num">{{ formatCount(sum('failed')) }}</div>
            <div class="kpi-label">başarısız</div>
          </div>
          <div class="kpi">
            <div class="kpi-value num">{{ formatCount(sum('skipped')) }}</div>
            <div class="kpi-label">atlandı</div>
          </div>
        </div>
        <p class="meta muted" data-test="meta">
          <span class="meta-approved">
            <span class="meta-label">Onay</span>
            <template v-if="approvedAt">
              <span class="num">{{ approvedAt.date }}</span>
              <span class="num">{{ approvedAt.time }}</span>
            </template>
            <template v-else>—</template>
          </span>
          <span class="meta-sep" aria-hidden="true"></span>
          <template v-if="stream.state.value === 'open'">canlı, saniyede bir güncelleniyor</template>
          <template v-else-if="stream.state.value === 'connecting'">bağlanıyor…</template>
          <template v-else-if="progress?.done">{{ status === 'cancelled' ? 'kampanya iptal edildi' : 'gönderim tamamlandı' }}</template>
          <template v-else-if="stream.state.value === 'error'">
            canlı bağlantı kurulamadı <button type="button" class="btn btn-sm" @click="connect">Tekrar bağlan</button>
          </template>
        </p>
        <p v-if="status === 'approved' && progress && finished === 0" class="note note-info">
          Kampanya onaylandı ve kuyruğa yazıldı. Gönderim, worker mesajları almaya başlayınca burada görünür.
        </p>
        <div v-if="status === 'scheduled'" class="note note-info schedule" data-test="scheduled">
          <span>
            Gönderim <b>{{ campaign.send_at ? formatTurkey(campaign.send_at) : '—' }}</b> (Türkiye saati) tarihinde başlayacak.
            Kitle onay anında hesaplandı; mesajlar o zamana kadar kuyrukta bekliyor.
          </span>
          <button v-if="!confirmingCancel" type="button" class="btn btn-sm" data-test="cancel" @click="confirmingCancel = true">
            Zamanlamayı iptal et
          </button>
          <span v-else class="confirm">
            Kampanya iptal edilecek ve hiç mesaj gönderilmeyecek.
            <button type="button" class="btn btn-sm btn-danger" :disabled="cancelling" data-test="confirm-cancel" @click="cancelSchedule">İptal et</button>
            <button type="button" class="btn btn-sm" :disabled="cancelling" @click="confirmingCancel = false">Vazgeç</button>
          </span>
        </div>
        <p v-if="status === 'cancelled'" class="note note-plain" data-test="cancelled">
          Kampanya gönderimden önce iptal edildi; hiç mesaj gönderilmedi.
        </p>
        <p v-if="cancelError" class="note note-bad" role="alert">{{ cancelError }}</p>
      </div>
    </section>

    <section v-for="ch in channels" :key="ch.key" class="card" :data-test="`channel-${ch.key}`">
      <div class="card-head">
        <h2 class="card-title"><i class="swatch" :style="{ background: `var(--${ch.key})` }"></i>{{ ch.label }}</h2>
        <span class="spacer"></span>
        <span class="num"><b>{{ formatCount(ch.c.sent + ch.c.failed + ch.c.skipped) }}</b> / {{ formatCount(ch.c.total) }}</span>
      </div>
      <div class="card-body">
        <div class="stack" role="img" :aria-label="`${ch.label}: ${ch.c.sent} gönderildi, ${ch.c.failed} başarısız, ${ch.c.skipped} atlandı`">
          <i class="s-sent" :style="{ width: pct(ch.c, ch.c.sent) + '%' }"></i>
          <i class="s-failed" :style="{ width: pct(ch.c, ch.c.failed) + '%' }"></i>
          <i class="s-skipped" :style="{ width: pct(ch.c, ch.c.skipped) + '%' }"></i>
          <i class="s-sending" :style="{ width: pct(ch.c, ch.c.sending) + '%' }"></i>
        </div>
        <div class="cells">
          <div v-for="[f, label] in CELLS" :key="f" class="cell">
            <div class="cell-value num">{{ formatCount(ch.c[f]) }}</div>
            <div class="cell-label"><i :class="`legend s-${f}`"></i>{{ label }}</div>
          </div>
        </div>
      </div>
    </section>

    <section class="card">
      <div class="card-head">
        <h2 class="card-title">Gönderim hızı</h2>
        <span class="muted">mesaj/sn, tüm kanallar</span>
      </div>
      <div class="card-body">
        <Sparkline :values="rates" :capacity="RATE_HISTORY" />
        <p class="muted small">Kanal başına sınır 100 mesaj/sn</p>
      </div>
    </section>

    <section class="card errors-card" data-test="errors-card">
      <div class="card-head">
        <h2 class="card-title">Hatalar</h2>
        <span class="muted">gönderilmemiş satırlar</span>
      </div>
      <div class="card-body">
        <table v-if="progress?.top_errors.length" class="errors">
          <thead>
            <tr><th>Hata</th><th class="n">Satır</th></tr>
          </thead>
          <tbody>
            <tr v-for="e in progress.top_errors" :key="e.error">
              <td class="mono">{{ errorLabel(e.error) }}</td>
              <td class="n num">{{ formatCount(e.count) }}</td>
            </tr>
          </tbody>
        </table>
        <p v-else class="muted no-errors">Hata yok.</p>
      </div>
    </section>
  </div>
</template>

<style scoped>
.dashboard {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 1.125rem;
}
.full {
  grid-column: 1 / -1;
}
@media (max-width: 900px) {
  .dashboard {
    grid-template-columns: minmax(0, 1fr);
  }
}
.spacer {
  flex: 1;
}
.kpis {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.875rem 2.25rem;
}
.kpi-value {
  font-size: 1.625rem;
  font-weight: 600;
  line-height: 1.2;
}
.of {
  font-size: var(--text-lg);
  color: var(--muted);
  font-weight: 400;
}
.kpi-label {
  font-size: var(--text-xs);
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--muted);
}
.status-pill {
  display: inline-flex;
  align-items: center;
  gap: 0.4375rem;
  font-weight: 500;
  border-radius: 62.4375rem;
  padding: 0.25rem 0.75rem;
  background: var(--info-soft);
  color: var(--info);
}
.schedule {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem 0.875rem;
}
.confirm {
  display: inline-flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
}
.btn-danger {
  background: var(--bad);
  border-color: var(--bad);
  color: var(--accent-ink);
}
.btn-danger:hover:not(:disabled) {
  color: var(--accent-ink);
}
.status-pill.completed {
  background: var(--ok-soft);
  color: var(--ok);
}
.dot {
  width: 0.5rem;
  height: 0.5rem;
  border-radius: 50%;
  background: currentColor;
}
.dot.live {
  animation: pulse 1.4s infinite;
}
@keyframes pulse {
  50% {
    opacity: 0.25;
  }
}
.meta {
  margin: 0;
  font-size: var(--text-sm);
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 0.375rem 0.875rem;
}
.btn-sm {
  padding: 0.125rem 0.625rem;
  font-size: var(--text-xs);
}
.stack {
  display: flex;
  height: 0.75rem;
  border-radius: 0.375rem;
  overflow: hidden;
  background: var(--surface-muted);
  border: 1px solid var(--line);
}
.stack i {
  display: block;
  height: 100%;
  transition: width 0.4s ease;
}
.s-sent {
  background: var(--ok);
}
.s-failed {
  background: var(--bad);
}
.s-skipped {
  background: var(--warn);
}
.s-sending {
  background: var(--info);
}
.s-pending {
  background: var(--line-strong);
}
.cells {
  display: grid;
  grid-template-columns: repeat(5, minmax(0, 1fr));
  gap: 0.5rem;
}
@media (max-width: 520px) {
  .cells {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
}
.cell {
  background: var(--surface-muted);
  border-radius: var(--radius-sm);
  padding: 0.5rem 0.625rem;
}
.cell-value {
  font-weight: 600;
  font-size: var(--text-lg);
}
.cell-label {
  font-size: 0.6875rem;
  color: var(--muted);
  display: flex;
  align-items: center;
  gap: 0.3125rem;
}
.legend {
  width: 0.4375rem;
  height: 0.4375rem;
  border-radius: 2px;
  display: inline-block;
}
/* The approval time: a label, then date and time with air between them. */
.meta-approved {
  display: inline-flex;
  align-items: baseline;
  gap: 0.625rem;
}
.meta-label {
  font-weight: 600;
  color: var(--ink-soft);
}
.meta-sep {
  width: 1px;
  height: 1em;
  background: var(--line-strong);
}
/* The errors card fills its grid cell; "Hata yok." sits in its middle. */
.errors-card {
  display: flex;
  flex-direction: column;
}
.errors-card .card-body {
  flex: 1;
}
.no-errors {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  margin: 0;
  min-height: 6rem;
}
.errors {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--text-sm);
}
.errors th,
.errors td {
  text-align: left;
  padding: 0.4375rem 0.25rem;
  border-bottom: 1px solid var(--line);
}
.errors th {
  font-size: var(--text-xs);
  font-weight: 500;
  color: var(--muted);
  text-transform: uppercase;
  letter-spacing: 0.06em;
}
.errors .n {
  text-align: right;
}
.small {
  margin: 0;
  font-size: var(--text-xs);
}
</style>
