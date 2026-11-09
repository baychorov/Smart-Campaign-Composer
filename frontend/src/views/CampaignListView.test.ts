import { flushPromises, mount } from '@vue/test-utils'
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
    { path: '/campaigns/:id', name: 'campaign', component: {} },
  ],
})

afterEach(() => vi.unstubAllGlobals())

const mountView = () => mount(CampaignListView, { global: { plugins: [router] } })

const sentItem: CampaignListItem = {
  id: 8,
  status: 'completed',
  created_at: '2026-11-08T09:00:00Z',
  approved_at: '2026-11-08T09:05:00Z',
  progress: { sms: { total: 110, pending: 0, sending: 0, sent: 108, failed: 2, skipped: 0 } },
}
const draftItem: CampaignListItem = { id: 7, status: 'draft', created_at: '2026-11-07T10:00:00Z', approved_at: null, progress: {} }
const page = (campaigns: CampaignListItem[], next: number | null = null): { json: CampaignPage } => ({
  json: { campaigns, next_before: next },
})

const calls = (c: { method: string; path: string }[]) => c.map((x) => `${x.method} ${x.path}`)

describe('CampaignListView', () => {
  it('shows collapsed rows without the brief', async () => {
    fakeFetch({ 'GET /api/campaigns?filter=all': () => page([sentItem, draftItem]) })
    const w = mountView()
    await flushPromises()

    const sent = w.get('[data-test="row-8"]')
    expect(sent.get('[data-test="status"]').text()).toBe('Tamamlandı')
    expect(sent.get('[data-test="counts"]').text()).toContain('108')
    expect(sent.get('[data-test="counts"]').text()).toContain('2 başarısız')
    expect(w.get('[data-test="row-7"] [data-test="status"]').text()).toBe('Taslak')
    expect(w.find('[data-test="detail"]').exists()).toBe(false)
    expect(w.text()).not.toContain('İstanbul')
  })

  it('opens a row with the campaign details, loaded once', async () => {
    const campaign = makeCampaign({ id: 8, status: 'completed', approved_at: sentItem.approved_at, message: goodMessage, channels: { sms: true, email: false } })
    const c = fakeFetch({
      'GET /api/campaigns?filter=all': () => page([sentItem]),
      'GET /api/campaigns/8': () => ({ json: { campaign } }),
    })
    const w = mountView()
    await flushPromises()

    const toggle = w.get('[data-test="row-8"] [data-test="toggle"]')
    await toggle.trigger('click')
    await flushPromises()
    expect(toggle.attributes('aria-expanded')).toBe('true')
    const detail = w.get('[data-test="detail"]')
    expect(detail.get('[data-test="brief"]').text()).toBe(campaign.brief)
    expect(detail.text()).toContain('şehri İstanbul olan')
    expect(detail.text()).toContain(goodMessage.sms)
    expect(detail.text()).toContain('Kanallar: SMS')
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
    const w = mountView()
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
    const w = mountView()
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
    const w = mountView()
    await flushPromises()

    await w.get('[data-test="more"]').trigger('click')
    await flushPromises()
    expect(w.findAll('li.row').map((r) => r.attributes('data-test'))).toEqual(['row-8', 'row-7'])
    expect(w.find('[data-test="more"]').exists()).toBe(false)

    await w.get('[data-test="filter-draft"]').trigger('click')
    await flushPromises()
    expect(calls(c).at(-1)).toBe('GET /api/campaigns?filter=draft')
    expect(w.get('[data-test="empty"]').text()).toContain('Bu filtrede kampanya yok')
  })
})
