<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from '../api/client'
import type { Campaign, ChannelProgress, Progress } from '../api/types'
import { useEventStream } from '../composables/useEventStream'
import { formatCount, STATUS_LABELS } from '../lib/labels'
import Sparkline from './Sparkline.vue'

// Live sending state (R-OBS-1). The server sends the full summary about once
// a second, so a reconnect is correct with the next event; the stream closes
// after the event with done: true.

const props = defineProps<{ campaign: Campaign }>()

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

const approvedAt = computed(() =>
  props.campaign.approved_at ? new Date(props.campaign.approved_at).toLocaleString('tr-TR') : '—',
)
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
            <div class="kpi-label">mesaj/sn · son 10 sn</div>
          </div>
          <div class="kpi">
            <div class="kpi-value num">{{ formatCount(sum('failed')) }}</div>
            <div class="kpi-label">başarısız</div>
          </div>
          <div class="kpi">
            <div class="kpi-value num">{{ formatCount(sum('skipped')) }}</div>
            <div class="kpi-label">atlandı (izin)</div>
          </div>
        </div>
        <p class="meta muted">
          Onay: {{ approvedAt }} ·
          <template v-if="stream.state.value === 'open'">canlı, saniyede bir güncelleniyor</template>
          <template v-else-if="stream.state.value === 'connecting'">bağlanıyor…</template>
          <template v-else-if="progress?.done">gönderim tamamlandı, akış kapandı</template>
          <template v-else-if="stream.state.value === 'error'">
            canlı bağlantı kurulamadı <button type="button" class="btn btn-sm" @click="connect">Tekrar bağlan</button>
          </template>
        </p>
        <p v-if="status === 'approved' && progress && finished === 0" class="note note-info">
          Kampanya onaylandı ve kuyruğa yazıldı. Gönderim, worker mesajları almaya başlayınca burada görünür.
        </p>
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
        <p class="muted small">Kanal başına sınır 100 mesaj/sn; worker 95 ile gönderir.</p>
      </div>
    </section>

    <section class="card">
      <div class="card-head">
        <h2 class="card-title">En sık hatalar</h2>
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
        <p v-else class="muted">Hata yok.</p>
        <p class="muted small">5xx ve zaman aşımı üstel bekleme ile en fazla 5 kez denenir; 429'da Retry-After beklenir.</p>
      </div>
    </section>
  </div>
</template>

<style scoped>
.dashboard {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 18px;
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
  gap: 14px 36px;
}
.kpi-value {
  font-size: 26px;
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
  gap: 7px;
  font-weight: 500;
  border-radius: 999px;
  padding: 4px 12px;
  background: var(--info-soft);
  color: var(--info);
}
.status-pill.completed {
  background: var(--ok-soft);
  color: var(--ok);
}
.dot {
  width: 8px;
  height: 8px;
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
  gap: 6px;
}
.btn-sm {
  padding: 2px 10px;
  font-size: var(--text-xs);
}
.stack {
  display: flex;
  height: 12px;
  border-radius: 6px;
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
  gap: 8px;
}
@media (max-width: 520px) {
  .cells {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
}
.cell {
  background: var(--surface-muted);
  border-radius: var(--radius-sm);
  padding: 8px 10px;
}
.cell-value {
  font-weight: 600;
  font-size: var(--text-lg);
}
.cell-label {
  font-size: 11px;
  color: var(--muted);
  display: flex;
  align-items: center;
  gap: 5px;
}
.legend {
  width: 7px;
  height: 7px;
  border-radius: 2px;
  display: inline-block;
}
.errors {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--text-sm);
}
.errors th,
.errors td {
  text-align: left;
  padding: 7px 4px;
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
