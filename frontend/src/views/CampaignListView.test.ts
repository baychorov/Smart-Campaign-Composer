import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, KeepAlive, ref } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import type { CampaignListItem, CampaignPage } from '../api/types'
import { fakeFetch } from '../test/fakeFetch'
import { goodMessage, makeCampaign } from '../test/fixtures'
import CampaignListView from './CampaignListView.vue'

// The list page against a scripted API (fake fetch).

const router = createRouter({
  history: createMemoryHistory(),
  routes: [
    { path: '/', name: 'new', component: {} },
    { path: '/campaigns', name: 'campaigns', component: {} },
    { path: '/campaigns/:id', name: 'campaign', component: {} },
  ],
})

afterEach(() => vi.unstubAllGlobals())
// Unmount after each test: the router is shared, and a list left mounted
// would follow the next test's URL changes.
enableAutoUnmount(afterEach)

// The list's filter lives in the URL, so every test starts on /campaigns.
async function mountView(path = '/campaigns') {
  await router.replace(path)
  return mount(CampaignListView, { global: { plugins: [router] } })
}

const sentItem: CampaignListItem = {
  id: 8,
  status: 'completed',
  created_at: '2026-11-08T09:00:00Z',
  approved_at: '2026-11-08T09:05:00Z',
  send_at: null,
  approvable: true,
  progress: { sms: { total: 110, pending: 0, sending: 0, sent: 108, failed: 2, skipped: 0 } },
}
const draftItem: CampaignListItem = {
  id: 7,
  status: 'draft',
  created_at: '2026-11-07T10:00:00Z',
  approved_at: null,
  send_at: null,
  approvable: true,
  progress: {},
}
const page = (campaigns: CampaignListItem[], next: number | null = null): { json: CampaignPage } => ({
  json: { campaigns, next_before: next },
})

const calls = (c: { method: string; path: string }[]) => c.map((x) => `${x.method} ${x.path}`)

describe('CampaignListView', () => {
  it('shows collapsed rows without the brief', async () => {
    fakeFetch({ 'GET /api/campaigns': () => page([sentItem, draftItem]) })
    const w = await mountView()
    await flushPromises()

    const sent = w.get('[data-test="row-8"]')
    expect(sent.get('.id').text()).toBe('8') // no "#"
    expect(w.get('.cols').text()).toContain('Kanallar')
    expect(sent.get('[data-test="status"]').text()).toBe('Tamamlandı')
    expect(sent.get('[data-test="counts"]').text()).toContain('108')
    expect(sent.get('[data-test="counts"]').text()).toContain('2 başarısız')
    expect(w.get('[data-test="row-7"] [data-test="status"]').text()).toBe('Taslak')
    expect(w.find('[data-test="detail"]').exists()).toBe(false)
    expect(w.text()).not.toContain('İstanbul')
  })

  it('marks a draft whose brief gave no approvable audience as "Geçersiz"', async () => {
    const invalid: CampaignListItem = { ...draftItem, id: 6, approvable: false }
    fakeFetch({ 'GET /api/campaigns': () => page([draftItem, invalid]) })
    const w = await mountView()
    await flushPromises()
    const status = w.get('[data-test="row-6"] [data-test="status"]')
    expect(status.text()).toBe('Geçersiz')
    expect(status.classes()).toContain('st-invalid')
    expect(w.get('[data-test="row-7"] [data-test="status"]').text()).toBe('Taslak')
  })

  it('shows the send time of a scheduled campaign in its row', async () => {
    const item: CampaignListItem = { ...sentItem, id: 9, status: 'scheduled', send_at: '2026-11-10T07:00:00Z', progress: {} }
    fakeFetch({ 'GET /api/campaigns': () => page([item]) })
    const w = await mountView()
    await flushPromises()
    expect(w.get('[data-test="row-9"] [data-test="status"]').text()).toBe('Zamanlandı')
    expect(w.get('[data-test="row-send-at"]').text()).toContain('10:00')
    expect(w.get('.cols').text()).toContain('Gönderim zamanı')
  })

  it('says when each campaign was or will be sent', async () => {
    const at = '2026-11-12T07:00:00Z' // 10:00 in Turkey
    const rows: CampaignListItem[] = [
      { ...sentItem, id: 1, status: 'scheduled', send_at: at, progress: {} },
      { ...sentItem, id: 2, status: 'completed', send_at: at }, // scheduled, then sent
      { ...sentItem, id: 3, status: 'sending', send_at: null }, // sent at approval
      { ...sentItem, id: 4, status: 'completed', send_at: null },
      { ...sentItem, id: 5, status: 'cancelled', send_at: at, progress: {} },
      { ...draftItem, id: 6, send_at: at }, // the brief's suggestion is not shown here
      { ...draftItem, id: 7, approvable: false },
    ]
    fakeFetch({ 'GET /api/campaigns': () => page(rows) })
    const w = await mountView()
    await flushPromises()
    const cell = (id: number) => w.get(`[data-test="row-${id}"] [data-test="row-send-at"]`)

    expect(cell(1).text()).toContain('10:00')
    expect(cell(1).find('.scheduled').exists()).toBe(true)
    expect(cell(2).text()).toContain('10:00')
    expect(cell(2).find('.sent').exists()).toBe(true)
    expect(cell(3).text()).toBe('Anında')
    expect(cell(4).text()).toBe('Anında')
    for (const id of [5, 6, 7]) expect(cell(id).text()).toBe('—')
  })

  it('gives a cancelled campaign its own badge style, apart from a draft', async () => {
    const item: CampaignListItem = { ...sentItem, id: 5, status: 'cancelled', send_at: '2026-11-10T07:00:00Z', progress: {} }
    fakeFetch({ 'GET /api/campaigns': () => page([item, draftItem]) })
    const w = await mountView()
    await flushPromises()
    const status = w.get('[data-test="row-5"] [data-test="status"]')
    expect(status.text()).toBe('İptal edildi')
    expect(status.classes()).toContain('st-cancelled')
    expect(w.get('[data-test="row-7"] [data-test="status"]').classes()).not.toContain('st-cancelled')
  })

  it('opens a row with the campaign details, loaded once', async () => {
    const campaign = makeCampaign({ id: 8, status: 'completed', approved_at: sentItem.approved_at, message: goodMessage, channels: { sms: true, email: false } })
    const c = fakeFetch({
      'GET /api/campaigns': () => page([sentItem]),
      'GET /api/campaigns/8': () => ({ json: { campaign } }),
    })
    const w = await mountView()
    await flushPromises()

    const toggle = w.get('[data-test="row-8"] [data-test="toggle"]')
    await toggle.trigger('click')
    await flushPromises()
    expect(toggle.attributes('aria-expanded')).toBe('true')
    const detail = w.get('[data-test="detail"]')
    expect(detail.get('[data-test="brief"]').text()).toBe(campaign.brief)
    expect(detail.text()).toContain('şehri İstanbul olan')
    // One block per channel; the e-mail shows its subject over its body.
    expect(detail.get('[data-test="detail-sms"] .msg-box').text()).toBe(goodMessage.sms)
    expect(detail.get('[data-test="detail-email"] .mail-subject').text()).toBe(goodMessage.email_subject)
    expect(detail.get('[data-test="detail-email"] .mail-body').text()).toBe(goodMessage.email_body.trim())
    // Audience: only the description; the counts are in the delivery table.
    expect(detail.text()).not.toContain('Onay anında')
    // Delivery: no "Kanallar" line, one table row per chosen channel.
    expect(detail.text()).not.toContain('Kanallar:')
    const rows = detail.findAll('[data-test="delivery"] tbody tr')
    expect(rows.map((r) => r.get('th').text())).toEqual(['SMS'])
    expect(rows[0]!.findAll('td').map((td) => td.text())).toEqual(['110', '108', '2', '0', '0'])
    // Only drafts can be deleted.
    expect(detail.find('[data-test="delete"]').exists()).toBe(false)

    await toggle.trigger('click')
    expect(w.find('[data-test="detail"]').exists()).toBe(false)
    await toggle.trigger('click')
    await flushPromises()
    expect(calls(c).filter((x) => x === 'GET /api/campaigns/8')).toHaveLength(1)
  })

  it('deletes a draft only after confirmation', async () => {
    const c = fakeFetch({
      'GET /api/campaigns': () => page([sentItem, draftItem]),
      'GET /api/campaigns/7': () => ({ json: { campaign: makeCampaign() } }),
      'DELETE /api/campaigns/7': () => ({ json: null }),
    })
    const w = await mountView()
    await flushPromises()
    await w.get('[data-test="row-7"] [data-test="toggle"]').trigger('click')
    await flushPromises()

    await w.get('[data-test="delete"]').trigger('click')
    expect(calls(c)).not.toContain('DELETE /api/campaigns/7')
    await w.get('[data-test="confirm-delete"]').trigger('click')
    await flushPromises()

    expect(calls(c)).toContain('DELETE /api/campaigns/7')
    expect(w.find('[data-test="row-7"]').exists()).toBe(false)
    expect(w.find('[data-test="row-8"]').exists()).toBe(true)
  })

  it('keeps a draft that was approved meanwhile and reloads the list', async () => {
    let approved = false
    fakeFetch({
      'GET /api/campaigns': () => page([approved ? { ...draftItem, status: 'approved', approved_at: '2026-11-07T11:00:00Z' } : draftItem]),
      'GET /api/campaigns/7': () => ({ json: { campaign: makeCampaign() } }),
      'DELETE /api/campaigns/7': () => {
        approved = true
        return { status: 409, json: { error: 'not_draft', message: 'Kampanya artık taslak değil.' } }
      },
    })
    const w = await mountView()
    await flushPromises()
    await w.get('[data-test="row-7"] [data-test="toggle"]').trigger('click')
    await flushPromises()
    await w.get('[data-test="delete"]').trigger('click')
    await w.get('[data-test="confirm-delete"]').trigger('click')
    await flushPromises()

    expect(w.text()).toContain('artık taslak değil')
    expect(w.get('[data-test="row-7"] [data-test="status"]').text()).toBe('Onaylandı')
  })

  it('filters and pages', async () => {
    const c = fakeFetch({
      'GET /api/campaigns': () => page([sentItem], 8),
      'GET /api/campaigns?before=8': () => page([draftItem]),
      'GET /api/campaigns?status=draft': () => page([]),
    })
    const w = await mountView()
    await flushPromises()

    await w.get('[data-test="more"]').trigger('click')
    await flushPromises()
    expect(w.findAll('li.row').map((r) => r.attributes('data-test'))).toEqual(['row-8', 'row-7'])
    expect(w.find('[data-test="more"]').exists()).toBe(false)

    await w.get('[data-test="filter-draft"]').trigger('click')
    await flushPromises()
    expect(calls(c).at(-1)).toBe('GET /api/campaigns?status=draft')
    expect(router.currentRoute.value.query).toEqual({ durum: 'taslak' })
    expect(w.get('[data-test="empty"]').text()).toContain('Bu filtrede kampanya yok')
  })

  it('says the filter is invalid without naming the API parameter', async () => {
    fakeFetch({
      'GET /api/campaigns?from=2026-13-45': () => ({
        status: 400,
        json: { error: 'invalid_query', message: 'from bir gün olmalı (YYYY-AA-GG).' },
      }),
    })
    const w = await mountView('/campaigns?baslangic=2026-13-45')
    await flushPromises()
    const alert = w.get('[role="alert"]').text()
    expect(alert).toBe('Filtre geçersiz. Kampanya numarasını ve tarihleri kontrol edin.')
    expect(alert).not.toContain('from')
  })

  it('reads the filter from the URL and follows it when the URL changes', async () => {
    const c = fakeFetch({
      'GET /api/campaigns?status=approved%2Csending%2Ccompleted': () => page([sentItem]),
      'GET /api/campaigns': () => page([sentItem, draftItem]),
    })
    const w = await mountView('/campaigns?durum=gonderiliyor,tamamlandi')
    await flushPromises()
    expect(calls(c)).toEqual(['GET /api/campaigns?status=approved%2Csending%2Ccompleted'])
    expect(w.get('[data-test="filter-sent"]').attributes('aria-pressed')).toBe('true')

    await router.push('/campaigns') // the menu's "Kampanyalar" link
    await flushPromises()
    expect(calls(c).at(-1)).toBe('GET /api/campaigns')
    expect(w.findAll('li.row')).toHaveLength(2)
  })

  // App.vue keeps the list alive while a campaign is open. Coming back, every
  // row on screen must show its current status, also after "Daha fazla
  // göster": here a scheduled campaign on the second page was cancelled.
  it('refreshes every loaded page when the marketer comes back to the list', async () => {
    const scheduled: CampaignListItem = { ...sentItem, id: 5, status: 'scheduled', send_at: '2026-11-12T07:00:00Z', progress: {} }
    let second = scheduled
    const c = fakeFetch({
      'GET /api/campaigns': () => page([sentItem], 8),
      'GET /api/campaigns?before=8': () => page([second]),
    })
    const shown = ref(true)
    const Host = defineComponent(() => () => h(KeepAlive, null, [shown.value ? h(CampaignListView) : h('div')]))
    await router.replace('/campaigns')
    const w = mount(Host, { global: { plugins: [router] } })
    await flushPromises()
    await w.get('[data-test="more"]').trigger('click')
    await flushPromises()
    expect(w.get('[data-test="row-5"] [data-test="status"]').text()).toBe('Zamanlandı')

    shown.value = false // the campaign page is open; it is cancelled there
    await flushPromises()
    second = { ...scheduled, status: 'cancelled' }
    const before = c.length
    shown.value = true // back to the list
    await flushPromises()

    expect(calls(c.slice(before))).toEqual(['GET /api/campaigns', 'GET /api/campaigns?before=8'])
    expect(w.get('[data-test="row-5"] [data-test="status"]').text()).toBe('İptal edildi')
    expect(w.findAll('li.row').map((r) => r.attributes('data-test'))).toEqual(['row-8', 'row-5']) // the same rows, so the scroll position holds
    expect(w.find('[data-test="more"]').exists()).toBe(false)
  })

  it('shows no send time or delivery table for a cancelled campaign', async () => {
    const item: CampaignListItem = { ...sentItem, id: 5, status: 'cancelled', send_at: '2026-11-12T07:00:00Z', progress: {} }
    const campaign = makeCampaign({ id: 5, status: 'cancelled', approved_at: sentItem.approved_at, send_at: item.send_at, message: goodMessage })
    fakeFetch({
      'GET /api/campaigns': () => page([item]),
      'GET /api/campaigns/5': () => ({ json: { campaign } }),
    })
    const w = await mountView()
    await flushPromises()
    await w.get('[data-test="row-5"] [data-test="toggle"]').trigger('click')
    await flushPromises()
    const detail = w.get('[data-test="detail"]')
    expect(detail.find('[data-test="delivery"]').exists()).toBe(false)
    expect(detail.text()).not.toContain('Planlanan gönderim')
    expect(detail.text()).toContain('Onaylandı')
  })

  it('filters with the panel: number, dates and statuses, applied together', async () => {
    const c = fakeFetch({
      'GET /api/campaigns': () => page([sentItem, draftItem]),
      'GET /api/campaigns?status=invalid%2Ccancelled&id=7&from=2026-11-01&to=2026-11-10': () => page([]),
    })
    const w = await mountView()
    await flushPromises()
    expect(w.find('[data-test="filter-panel"]').exists()).toBe(false)
    expect(w.find('[data-test="filter-count"]').exists()).toBe(false)

    await w.get('[data-test="open-filters"]').trigger('click')
    expect(w.find('[data-test="filter-panel"]').exists()).toBe(true)
    await w.get('[data-test="filter-id"]').setValue('7a') // digits only
    expect((w.get('[data-test="filter-id"]').element as HTMLInputElement).value).toBe('7')
    await w.get('[data-test="filter-from"]').setValue('2026-11-01')
    await w.get('[data-test="filter-to"]').setValue('2026-11-10')
    await w.get('[data-test="filter-status-gecersiz"]').setValue(true)
    await w.get('[data-test="filter-status-iptal"]').setValue(true)
    expect(calls(c)).toHaveLength(1) // nothing applied yet
    await w.get('[data-test="filter-apply"]').trigger('submit')
    await flushPromises()

    expect(calls(c).at(-1)).toBe('GET /api/campaigns?status=invalid%2Ccancelled&id=7&from=2026-11-01&to=2026-11-10')
    expect(router.currentRoute.value.query).toEqual({ durum: 'gecersiz,iptal', no: '7', baslangic: '2026-11-01', bitis: '2026-11-10' })
    expect(w.find('[data-test="filter-panel"]').exists()).toBe(false)
    expect(w.get('[data-test="filter-count"]').text()).toBe('3')
    expect(w.findAll('.filter[aria-pressed="true"]')).toHaveLength(0) // no quick filter matches

    await w.get('[data-test="clear-filters"]').trigger('click')
    await flushPromises()
    expect(calls(c).at(-1)).toBe('GET /api/campaigns')
    expect(router.currentRoute.value.query).toEqual({})
  })

  it('leaves the list as it was when the panel is closed without applying', async () => {
    const c = fakeFetch({ 'GET /api/campaigns': () => page([sentItem]) })
    const w = await mountView()
    await flushPromises()
    await w.get('[data-test="open-filters"]').trigger('click')
    await w.get('[data-test="filter-status-iptal"]').setValue(true)
    await w.get('[data-test="filter-backdrop"]').trigger('click')
    expect(w.find('[data-test="filter-panel"]').exists()).toBe(false)

    await w.get('[data-test="open-filters"]').trigger('click') // a new opening starts from the filter in use
    expect((w.get('[data-test="filter-status-iptal"]').element as HTMLInputElement).checked).toBe(false)
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await flushPromises()
    expect(w.find('[data-test="filter-panel"]').exists()).toBe(false)
    expect(calls(c)).toHaveLength(1)
  })

  it('does not apply an end date before the start date', async () => {
    fakeFetch({ 'GET /api/campaigns': () => page([sentItem]) })
    const w = await mountView()
    await flushPromises()
    await w.get('[data-test="open-filters"]').trigger('click')
    await w.get('[data-test="filter-from"]').setValue('2026-11-10')
    await w.get('[data-test="filter-to"]').setValue('2026-11-01')
    expect(w.get('[data-test="filter-apply"]').attributes('disabled')).toBeDefined()
    expect(w.get('[data-test="filter-panel"]').text()).toContain('Bitiş tarihi başlangıçtan önce olamaz')
  })

  it('shows a quick filter as selected when the panel picks exactly its statuses', async () => {
    fakeFetch({ 'GET /api/campaigns?status=approved%2Csending%2Ccompleted': () => page([sentItem]) })
    const w = await mountView('/campaigns?durum=tamamlandi,gonderiliyor')
    await flushPromises()
    expect(w.get('[data-test="filter-sent"]').attributes('aria-pressed')).toBe('true')
    expect(w.find('[data-test="filter-count"]').exists()).toBe(false)
  })
})
