<script setup lang="ts">
import { onActivated, onMounted, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api } from '../api/client'
import type { Campaign, CampaignListItem, ChannelProgress, ListFilter } from '../api/types'
import { toApiError } from '../composables/useCampaign'
import { audienceSentence, formatCount, formatDate, REASON_LABELS, STATUS_LABELS } from '../lib/labels'
import { formatTurkey } from '../lib/schedule'

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

// The filter is in the URL (?filtre=taslak), so a reload or "back" from a
// campaign shows the same list. "Tümü" has no query.
const FILTER_QUERY: Record<ListFilter, string | undefined> = { all: undefined, draft: 'taslak', sent: 'gonderilen' }
const route = useRoute()
const router = useRouter()
const filterFromQuery = (): ListFilter =>
  (Object.keys(FILTER_QUERY) as ListFilter[]).find((f) => FILTER_QUERY[f] === route.query.filtre) ?? 'all'

const filter = ref<ListFilter>(filterFromQuery())
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

// Pages loaded so far; a refresh on return only reloads a single page.
let pages = 0

async function load(more = false) {
  const token = ++listToken
  loading.value = true
  error.value = null
  try {
    const page = await api.listCampaigns(filter.value, more ? nextBefore.value : null)
    if (token !== listToken) return
    items.value = more ? [...items.value, ...page.campaigns] : page.campaigns
    nextBefore.value = page.next_before
    pages = more ? pages + 1 : 1
  } catch (e) {
    if (token === listToken) error.value = toApiError(e).message
  } finally {
    if (token === listToken) loading.value = false
  }
}

function setFilter(f: ListFilter) {
  if (f === filter.value) return
  const q = FILTER_QUERY[f]
  void router.replace({ query: q ? { filtre: q } : {} })
}

// The URL drives the filter: a button above, the menu's "Kampanyalar" link
// (no query) or the browser's back/forward. While a campaign is open the
// list is kept alive but not on screen; its route is not the list's then.
watch(
  () => route.query.filtre,
  () => {
    if (route.name !== 'campaigns') return
    const f = filterFromQuery()
    if (f === filter.value) return
    filter.value = f
    openId.value = null
    void load()
  },
)

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

// Back from a campaign (App.vue keeps this page alive): the same rows, open
// row and scroll position, with fresh statuses and counts. A list that was
// paged further is left as it is so the scroll position stays valid.
let activated = false
onActivated(() => {
  if (!activated) {
    activated = true // the first activation is the mount itself
    return
  }
  if (pages <= 1) void load()
  // Other cached details may be stale; the open row's stays on screen
  // until its fresh copy arrives.
  const id = openId.value
  const shown = id !== null ? details.value[id] : undefined
  details.value = id !== null && shown ? { [id]: shown } : {}
  if (id !== null) void loadDetail(id)
})
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

    <div v-if="items.length" class="cols" aria-hidden="true">
      <span>No</span>
      <span>Durum</span>
      <span>Tarih</span>
      <span>Gönderim</span>
      <span class="right">Kanallar</span>
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
          <span class="id num">{{ it.id }}</span>
          <span class="status" :class="`st-${it.status}`" data-test="status">{{ STATUS_LABELS[it.status] }}</span>
          <span class="date muted">{{ formatDate(it.approved_at ?? it.created_at) }}</span>
          <span class="send-cell">
            <span v-if="it.status === 'scheduled' && it.send_at" class="send-at" data-test="row-send-at">{{ formatTurkey(it.send_at) }}</span>
          </span>
          <span class="counts" data-test="counts">
            <span v-for="ch in channelsOf(it)" :key="ch.key" class="count">
              <span class="ch">{{ ch.label }}</span> <b class="num">{{ formatCount(ch.c.sent) }}</b><span class="muted num">/{{ formatCount(ch.c.total) }}</span>
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
              <div v-if="details[it.id]!.message.sms || details[it.id]!.message.email_subject" class="msgs">
                <div v-if="details[it.id]!.message.sms" class="msg-block" data-test="detail-sms">
                  <div class="msg-label"><i class="swatch" style="background: var(--sms)"></i>SMS</div>
                  <p class="msg-box">{{ details[it.id]!.message.sms }}</p>
                </div>
                <div v-if="details[it.id]!.message.email_subject" class="msg-block" data-test="detail-email">
                  <div class="msg-label"><i class="swatch" style="background: var(--email)"></i>Email</div>
                  <div class="msg-box">
                    <p class="mail-subject">{{ details[it.id]!.message.email_subject }}</p>
                    <p v-if="details[it.id]!.message.email_body" class="mail-body">{{ details[it.id]!.message.email_body }}</p>
                  </div>
                </div>
              </div>
              <p v-else class="muted small">Henüz mesaj yok.</p>
            </section>

            <p class="muted small dates">
              Oluşturuldu: {{ formatDate(details[it.id]!.created_at) }}
              <template v-if="details[it.id]!.approved_at"> · Onaylandı: {{ formatDate(details[it.id]!.approved_at!) }}</template>
              <template v-if="it.status !== 'draft' && details[it.id]!.send_at">
                · Planlanan gönderim: {{ formatTurkey(details[it.id]!.send_at!) }} (Türkiye saati)
              </template>
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
  max-width: 64rem;
  padding-top: 1.75rem;
  padding-bottom: 3rem;
}
.head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  margin-bottom: 1rem;
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
  padding: 0.125rem;
}
.filter {
  border: 0;
  background: transparent;
  color: var(--ink-soft);
  font-size: var(--text-sm);
  padding: 0.3125rem 0.75rem;
  border-radius: 0.25rem;
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
  gap: 0.5rem;
}
.empty {
  padding: 1.75rem;
  text-align: center;
}
/* Rows are a grid with fixed columns, so ids, statuses, dates and counts
   line up from row to row under the grey column headings. */
.cols,
.row-head {
  display: grid;
  grid-template-columns: 3.5rem 8rem 9.5rem 10rem minmax(0, 1fr) 1rem;
  align-items: center;
  column-gap: 1.25rem;
}
.cols {
  padding: 0 1.25rem 0.5rem;
  font-size: var(--text-xs);
  font-weight: 500;
  letter-spacing: 0.04em;
  color: var(--muted);
}
.cols .right {
  text-align: right;
}
.row-head {
  width: 100%;
  padding: 0.875rem 1.25rem;
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
  font-weight: 700;
}
.status {
  justify-self: start;
  font-size: var(--text-xs);
  font-weight: 500;
  border-radius: 62.4375rem;
  padding: 0.125rem 0.5625rem;
  background: var(--surface-muted);
  color: var(--ink-soft);
}
.st-approved,
.st-sending {
  background: var(--info-soft);
  color: var(--info);
}
.st-scheduled {
  background: var(--variable-soft);
  color: var(--variable);
}
.send-at {
  font-size: var(--text-sm);
  color: var(--variable);
}
.st-completed {
  background: var(--ok-soft);
  color: var(--ok);
}
.date {
  font-size: var(--text-sm);
}
.counts {
  display: inline-flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 0.25rem 1.25rem;
}
.count {
  white-space: nowrap;
}
.ch {
  font-size: var(--text-xs);
  font-weight: 600;
  letter-spacing: 0.04em;
  color: var(--muted);
}
.failed {
  color: var(--bad);
}
.chevron {
  justify-self: end;
  color: var(--muted);
  transition: transform 0.15s;
}
.open .chevron {
  transform: rotate(180deg);
}
.detail {
  border-top: 1px solid var(--line);
  padding: 1.25rem 1.25rem 1.25rem;
  display: flex;
  flex-direction: column;
  gap: 1.125rem;
}
/* Thin lines between the sections of an open row. */
.detail > section + section,
.detail > .dates {
  border-top: 1px solid var(--line);
  padding-top: 1.125rem;
}
.detail h3 {
  margin: 0 0 0.375rem;
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
  padding: 0.625rem 0.875rem;
  border-left: 0.1875rem solid var(--accent);
  background: var(--accent-soft);
  border-radius: 0 var(--radius-sm) var(--radius-sm) 0;
  white-space: pre-wrap;
}
.small {
  font-size: var(--text-sm);
}
.unsupported {
  margin: 0.375rem 0 0;
  padding-left: 1.125rem;
  color: var(--warn);
}
table {
  border-collapse: collapse;
  margin-top: 0.375rem;
}
th,
td {
  padding: 0.25rem 0.75rem 0.25rem 0;
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
/* Messages: one block per channel, the e-mail as a small mail (subject
   over body), so the parts read apart at a glance. */
.msgs {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}
.msg-label {
  display: flex;
  align-items: center;
  margin-bottom: 0.375rem;
  font-size: var(--text-xs);
  font-weight: 600;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--ink-soft);
}
.msg-box {
  margin: 0;
  padding: 0.75rem 1rem;
  border: 1px solid var(--line);
  border-radius: var(--radius-md);
  background: var(--surface-muted);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.detail p.mail-subject {
  margin: 0 0 0.625rem;
  padding-bottom: 0.625rem;
  border-bottom: 1px solid var(--line);
  font-weight: 600;
}
.mail-body {
  color: var(--ink-soft);
}
.actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.625rem;
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
  gap: 0.5rem;
}
.more {
  margin-top: 0.75rem;
}
@media (max-width: 720px) {
  .cols {
    display: none;
  }
  .row-head {
    grid-template-columns: auto auto minmax(0, 1fr) 1rem;
    row-gap: 0.375rem;
  }
  .send-cell:not(:has(.send-at)) {
    display: none;
  }
  .send-at::before {
    content: 'gönderim: ';
  }
  .counts {
    grid-column: 1 / -2;
    justify-content: flex-start;
  }
}
@media (max-width: 560px) {
  table {
    display: block;
    overflow-x: auto;
  }
}
</style>
