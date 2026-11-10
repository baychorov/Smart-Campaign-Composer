import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
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
    fakeFetch({ 'GET /api/campaigns?filter=all': () => page([sentItem, draftItem]) })
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
    fakeFetch({ 'GET /api/campaigns?filter=all': () => page([draftItem, invalid]) })
    const w = await mountView()
    await flushPromises()
    const status = w.get('[data-test="row-6"] [data-test="status"]')
    expect(status.text()).toBe('Geçersiz')
    expect(status.classes()).toContain('st-invalid')
    expect(w.get('[data-test="row-7"] [data-test="status"]').text()).toBe('Taslak')
  })

  it('shows the send time of a scheduled campaign in its row', async () => {
    const item: CampaignListItem = { ...sentItem, id: 9, status: 'scheduled', send_at: '2026-11-10T07:00:00Z', progress: {} }
    fakeFetch({ 'GET /api/campaigns?filter=all': () => page([item]) })
    const w = await mountView()
    await flushPromises()
    expect(w.get('[data-test="row-9"] [data-test="status"]').text()).toBe('Zamanlandı')
    expect(w.get('[data-test="row-send-at"]').text()).toContain('10:00')
  })

  it('opens a row with the campaign details, loaded once', async () => {
    const campaign = makeCampaign({ id: 8, status: 'completed', approved_at: sentItem.approved_at, message: goodMessage, channels: { sms: true, email: false } })
    const c = fakeFetch({
      'GET /api/campaigns?filter=all': () => page([sentItem]),
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
      'GET /api/campaigns?filter=all': () => page([sentItem, draftItem]),
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
      'GET /api/campaigns?filter=all': () => page([approved ? { ...draftItem, status: 'approved', approved_at: '2026-11-07T11:00:00Z' } : draftItem]),
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
      'GET /api/campaigns?filter=all': () => page([sentItem], 8),
      'GET /api/campaigns?filter=all&before=8': () => page([draftItem]),
      'GET /api/campaigns?filter=draft': () => page([]),
    })
    const w = await mountView()
    await flushPromises()

    await w.get('[data-test="more"]').trigger('click')
    await flushPromises()
    expect(w.findAll('li.row').map((r) => r.attributes('data-test'))).toEqual(['row-8', 'row-7'])
    expect(w.find('[data-test="more"]').exists()).toBe(false)

    await w.get('[data-test="filter-draft"]').trigger('click')
    await flushPromises()
    expect(calls(c).at(-1)).toBe('GET /api/campaigns?filter=draft')
    expect(router.currentRoute.value.query).toEqual({ filtre: 'taslak' })
    expect(w.get('[data-test="empty"]').text()).toContain('Bu filtrede kampanya yok')
  })

  it('reads the filter from the URL and follows it when the URL changes', async () => {
    const c = fakeFetch({
      'GET /api/campaigns?filter=sent': () => page([sentItem]),
      'GET /api/campaigns?filter=all': () => page([sentItem, draftItem]),
    })
    const w = await mountView('/campaigns?filtre=gonderilen')
    await flushPromises()
    expect(calls(c)).toEqual(['GET /api/campaigns?filter=sent'])
    expect(w.get('[data-test="filter-sent"]').attributes('aria-pressed')).toBe('true')

    await router.push('/campaigns') // the menu's "Kampanyalar" link
    await flushPromises()
    expect(calls(c).at(-1)).toBe('GET /api/campaigns?filter=all')
    expect(w.findAll('li.row')).toHaveLength(2)
  })
})
