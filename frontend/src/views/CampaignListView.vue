<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '../api/client'
import type { Campaign, CampaignListItem, ChannelProgress, ListFilter } from '../api/types'
import { toApiError } from '../composables/useCampaign'
import { audienceSentence, formatCount, formatDate, REASON_LABELS, STATUS_LABELS } from '../lib/labels'

// Past campaigns, newest first. A row is collapsed (id, status, date,
// delivery counts); opening it loads the campaign with GET {id} and shows
// the brief, audience, channels and message. Drafts can be deleted here.

const FILTERS: { value: ListFilter; label: string }[] = [
  { value: 'all', label: 'Tümü' },
  { value: 'draft', label: 'Taslaklar' },
  { value: 'sent', label: 'Gönderilenler' },
]
const CHANNELS = [
  ['sms', 'SMS'],
  ['email', 'Email'],
] as const

const filter = ref<ListFilter>('all')
const items = ref<CampaignListItem[]>([])
const nextBefore = ref<number | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)

const openId = ref<number | null>(null)
const details = ref<Record<number, Campaign>>({})
const detailError = ref<string | null>(null)
const confirmingDelete = ref(false)
const deleting = ref(false)
const deleteError = ref<string | null>(null)

// A request token: a slow page of an old filter must not land in a new one.
let listToken = 0

async function load(more = false) {
  const token = ++listToken
  loading.value = true
  error.value = null
  try {
    const page = await api.listCampaigns(filter.value, more ? nextBefore.value : null)
    if (token !== listToken) return
    items.value = more ? [...items.value, ...page.campaigns] : page.campaigns
    nextBefore.value = page.next_before
  } catch (e) {
    if (token === listToken) error.value = toApiError(e).message
  } finally {
    if (token === listToken) loading.value = false
  }
}

function setFilter(f: ListFilter) {
  if (f === filter.value) return
  filter.value = f
  openId.value = null
  void load()
}

async function toggle(id: number) {
  confirmingDelete.value = false
  deleteError.value = null
  if (openId.value === id) {
    openId.value = null
    return
  }
  openId.value = id
  if (!details.value[id]) await loadDetail(id)
}

async function loadDetail(id: number) {
  detailError.value = null
  try {
    const { campaign } = await api.getCampaign(id)
    details.value = { ...details.value, [id]: campaign }
  } catch (e) {
    if (openId.value === id) detailError.value = toApiError(e).message
  }
}

async function deleteDraft(id: number) {
  deleting.value = true
  deleteError.value = null
  try {
    await api.deleteCampaign(id)
    items.value = items.value.filter((it) => it.id !== id)
    openId.value = null
    confirmingDelete.value = false
  } catch (e) {
    const err = toApiError(e)
    if (err.code === 'not_draft') {
      // Approved in another tab meanwhile: show the current state.
      deleteError.value = 'Bu kampanya artık taslak değil, silinemez.'
      confirmingDelete.value = false
      void load()
      void loadDetail(id)
    } else if (err.code === 'not_found') {
      items.value = items.value.filter((it) => it.id !== id)
      openId.value = null
    } else {
      deleteError.value = err.message
    }
  } finally {
    deleting.value = false
  }
}

const channelsOf = (it: CampaignListItem) =>
  CHANNELS.flatMap(([key, label]) => (it.progress[key] ? [{ key, label, c: it.progress[key] as ChannelProgress }] : []))

const chosenChannels = (c: Campaign) =>
  [c.channels.sms && 'SMS', c.channels.email && 'Email'].filter(Boolean).join(' + ') || '—'

onMounted(() => void load())
</script>

<template>
  <main class="container page">
    <div class="head">
      <h1>Kampanyalar</h1>
      <div class="filters" role="group" aria-label="Filtre">
        <button
          v-for="f in FILTERS"
          :key="f.value"
          type="button"
          class="filter"
          :class="{ active: filter === f.value }"
          :aria-pressed="filter === f.value"
          :data-test="`filter-${f.value}`"
          @click="setFilter(f.value)"
        >
          {{ f.label }}
        </button>
      </div>
    </div>

    <p v-if="error" class="note note-bad" role="alert">{{ error }}</p>

    <div v-if="!loading && !error && items.length === 0" class="card empty" data-test="empty">
      <p class="muted">{{ filter === 'all' ? 'Henüz kampanya yok.' : 'Bu filtrede kampanya yok.' }}</p>
      <RouterLink v-if="filter === 'all'" :to="{ name: 'new' }" class="btn btn-primary">Yeni kampanya</RouterLink>
    </div>

    <ul class="list">
      <li v-for="it in items" :key="it.id" class="card row" :class="{ open: openId === it.id }" :data-test="`row-${it.id}`">
        <button
          type="button"
          class="row-head"
          :aria-expanded="openId === it.id"
          :aria-controls="`detail-${it.id}`"
          data-test="toggle"
          @click="toggle(it.id)"
        >
          <span class="id num">#{{ it.id }}</span>
          <span class="status" :class="`st-${it.status}`" data-test="status">{{ STATUS_LABELS[it.status] }}</span>
          <span class="date muted">{{ formatDate(it.approved_at ?? it.created_at) }}</span>
          <span class="counts" data-test="counts">
            <span v-for="ch in channelsOf(it)" :key="ch.key" class="count">
              {{ ch.label }} <b class="num">{{ formatCount(ch.c.sent) }}</b><span class="muted num">/{{ formatCount(ch.c.total) }}</span>
              <span v-if="ch.c.failed" class="failed num">· {{ formatCount(ch.c.failed) }} başarısız</span>
            </span>
          </span>
          <span class="chevron" aria-hidden="true">▾</span>
        </button>

        <div v-if="openId === it.id" :id="`detail-${it.id}`" class="detail" data-test="detail">
          <p v-if="detailError" class="note note-bad" role="alert">{{ detailError }}</p>
          <p v-else-if="!details[it.id]" class="muted"><span class="spinner"></span> Yükleniyor…</p>
          <template v-else>
            <section>
              <h3>Brief</h3>
              <blockquote class="brief" data-test="brief">{{ details[it.id]!.brief }}</blockquote>
            </section>

            <section>
              <h3>Kitle</h3>
              <p>{{ audienceSentence(details[it.id]!.summary) }}</p>
              <p v-if="details[it.id]!.audience" class="muted small">
                {{ details[it.id]!.audience!.snapshot ? 'Onay anında' : 'Şu an' }}:
                <b class="num">{{ formatCount(details[it.id]!.audience!.reachable) }}</b> kişi · SMS
                {{ formatCount(details[it.id]!.audience!.sms) }} · Email {{ formatCount(details[it.id]!.audience!.email) }}
              </p>
              <ul v-if="details[it.id]!.rule.unsupported.length" class="unsupported small">
                <li v-for="(u, i) in details[it.id]!.rule.unsupported" :key="i">
                  Uygulanmadı: “{{ u.text }}” <span class="muted">({{ REASON_LABELS[u.reason] }})</span>
                </li>
              </ul>
            </section>

            <section v-if="it.status !== 'draft'">
              <h3>Gönderim</h3>
              <p class="small">Kanallar: {{ chosenChannels(details[it.id]!) }}</p>
              <table class="small num">
                <thead>
                  <tr>
                    <th></th>
                    <th>Toplam</th>
                    <th>Gönderildi</th>
                    <th>Başarısız</th>
                    <th>Atlandı</th>
                    <th>Bekliyor</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="ch in channelsOf(it)" :key="ch.key">
                    <th>{{ ch.label }}</th>
                    <td>{{ formatCount(ch.c.total) }}</td>
                    <td>{{ formatCount(ch.c.sent) }}</td>
                    <td>{{ formatCount(ch.c.failed) }}</td>
                    <td>{{ formatCount(ch.c.skipped) }}</td>
                    <td>{{ formatCount(ch.c.pending + ch.c.sending) }}</td>
                  </tr>
                </tbody>
              </table>
            </section>

            <section>
              <h3>Mesajlar</h3>
              <template v-if="details[it.id]!.message.sms || details[it.id]!.message.email_subject">
                <p v-if="details[it.id]!.message.sms" class="msg"><span class="label">SMS</span>{{ details[it.id]!.message.sms }}</p>
                <p v-if="details[it.id]!.message.email_subject" class="msg">
                  <span class="label">Email konusu</span>{{ details[it.id]!.message.email_subject }}
                </p>
                <p v-if="details[it.id]!.message.email_body" class="msg body">
                  <span class="label">Email gövdesi</span>{{ details[it.id]!.message.email_body }}
                </p>
              </template>
              <p v-else class="muted small">Henüz mesaj yok.</p>
            </section>

            <p class="muted small">
              Oluşturuldu: {{ formatDate(details[it.id]!.created_at) }}
              <template v-if="details[it.id]!.approved_at"> · Onaylandı: {{ formatDate(details[it.id]!.approved_at!) }}</template>
            </p>

            <p v-if="deleteError" class="note note-bad" role="alert">{{ deleteError }}</p>

            <div class="actions">
              <RouterLink :to="{ name: 'campaign', params: { id: it.id } }" class="btn btn-primary" data-test="open">
                {{ it.status === 'draft' ? 'Taslağa devam et' : 'Dashboard’u aç' }}
              </RouterLink>
              <template v-if="it.status === 'draft'">
                <button v-if="!confirmingDelete" type="button" class="btn danger" data-test="delete" @click="confirmingDelete = true">
                  Taslağı sil
                </button>
                <span v-else class="confirm" role="group" aria-label="Silmeyi onayla">
                  <span class="small">Bu taslak kalıcı olarak silinecek.</span>
                  <button type="button" class="btn btn-danger" :disabled="deleting" data-test="confirm-delete" @click="deleteDraft(it.id)">
                    Sil
                  </button>
                  <button type="button" class="btn" :disabled="deleting" @click="confirmingDelete = false">Vazgeç</button>
                </span>
              </template>
            </div>
          </template>
        </div>
      </li>
    </ul>

    <p v-if="loading" class="muted"><span class="spinner"></span> Yükleniyor…</p>
    <button v-else-if="nextBefore" type="button" class="btn more" data-test="more" @click="load(true)">Daha fazla göster</button>
  </main>
</template>

<style scoped>
.page {
  max-width: 920px;
  padding-top: 28px;
  padding-bottom: 48px;
}
.head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 16px;
}
h1 {
  margin: 0;
  font-size: var(--text-xl);
}
.filters {
  display: inline-flex;
  background: var(--surface-muted);
  border: 1px solid var(--line);
  border-radius: var(--radius-sm);
  padding: 2px;
}
.filter {
  border: 0;
  background: transparent;
  color: var(--ink-soft);
  font-size: var(--text-sm);
  padding: 5px 12px;
  border-radius: 4px;
}
.filter.active {
  background: var(--surface);
  color: var(--ink);
  box-shadow: var(--shadow-card);
  font-weight: 500;
}
.list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.empty {
  padding: 28px;
  text-align: center;
}
.row-head {
  position: relative;
  width: 100%;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px 14px;
  padding: 12px 40px 12px 16px;
  border: 0;
  background: transparent;
  text-align: left;
  color: var(--ink);
  font-size: var(--text-sm);
}
.row-head:hover {
  background: var(--surface-muted);
  border-radius: var(--radius-lg);
}
.id {
  font-weight: 600;
  min-width: 3ch;
}
.status {
  font-size: var(--text-xs);
  font-weight: 500;
  border-radius: 999px;
  padding: 2px 9px;
  background: var(--surface-muted);
  color: var(--ink-soft);
}
.st-approved,
.st-sending {
  background: var(--info-soft);
  color: var(--info);
}
.st-completed {
  background: var(--ok-soft);
  color: var(--ok);
}
.counts {
  display: inline-flex;
  flex-wrap: wrap;
  gap: 4px 14px;
  margin-left: auto;
}
.counts:empty {
  display: none;
}
.failed {
  color: var(--bad);
}
.chevron {
  position: absolute;
  right: 16px;
  top: 12px;
  color: var(--muted);
  transition: transform 0.15s;
}
.open .chevron {
  transform: rotate(180deg);
}
.detail {
  border-top: 1px solid var(--line);
  padding: 16px 20px 18px;
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.detail h3 {
  margin: 0 0 6px;
  font-size: var(--text-xs);
  font-weight: 600;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--muted);
}
.detail p {
  margin: 0;
}
.brief {
  margin: 0;
  padding: 10px 14px;
  border-left: 3px solid var(--accent);
  background: var(--accent-soft);
  border-radius: 0 var(--radius-sm) var(--radius-sm) 0;
  white-space: pre-wrap;
}
.small {
  font-size: var(--text-sm);
}
.unsupported {
  margin: 6px 0 0;
  padding-left: 18px;
  color: var(--warn);
}
table {
  border-collapse: collapse;
  margin-top: 6px;
}
th,
td {
  padding: 4px 12px 4px 0;
  text-align: right;
  font-weight: 400;
}
thead th {
  color: var(--muted);
}
tbody th {
  text-align: left;
  font-weight: 500;
}
.msg {
  white-space: pre-wrap;
  margin-bottom: 6px !important;
}
.msg .label {
  display: block;
  font-size: var(--text-xs);
  color: var(--muted);
}
.actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
}
a.btn {
  text-decoration: none;
}
.btn.danger {
  color: var(--bad);
}
.btn-danger {
  background: var(--bad);
  border-color: var(--bad);
  color: var(--accent-ink);
}
.btn-danger:hover:not(:disabled) {
  color: var(--accent-ink);
}
.confirm {
  display: inline-flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}
.more {
  margin-top: 12px;
}
@media (max-width: 560px) {
  .counts {
    margin-left: 0;
    width: 100%;
  }
  table {
    display: block;
    overflow-x: auto;
  }
}
</style>
