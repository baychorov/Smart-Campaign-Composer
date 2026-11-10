import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { fakeFetch } from '../test/fakeFetch'
import { makeCampaign } from '../test/fixtures'
import NewCampaignView from './NewCampaignView.vue'

// The brief form: short copy, no visible "Brief" label, a neutral loading text.

const router = createRouter({
  history: createMemoryHistory(),
  routes: [
    { path: '/', name: 'new', component: {} },
    { path: '/campaigns/:id', name: 'campaign', component: {} },
  ],
})

afterEach(() => {
  vi.unstubAllGlobals()
  window.history.replaceState(null, '')
})

describe('NewCampaignView', () => {
  it('shows the short intro and an unlabelled but accessible text box', () => {
    const w = mount(NewCampaignView, { global: { plugins: [router] } })
    expect(w.text()).toContain('Oluşturmak istediğiniz kampanyayı açıklayın. Göndermeden önce düzenleyebilirsiniz.')
    expect(w.find('label[for="brief"]').exists()).toBe(false)
    expect(w.get('textarea').attributes('aria-label')).toBe('Kampanya açıklaması')
  })

  it('says "Hazırlanıyor" while the rule is being extracted', async () => {
    vi.stubGlobal('fetch', () => new Promise(() => {})) // the API never answers
    const w = mount(NewCampaignView, { global: { plugins: [router] } })
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(w.get('[data-test="preparing"]').text()).toBe('Hazırlanıyor…')
    expect(w.text()).not.toContain('Claude')
  })

  it('deletes the invalid draft it replaces once the new brief is stored', async () => {
    window.history.replaceState({ brief: 'kadın müşteriler', replaces: 12 }, '')
    const calls = fakeFetch({
      'POST /api/campaigns': () => ({ status: 201, json: { campaign: makeCampaign({ id: 13 }), warnings: [] } }),
      'DELETE /api/campaigns/12': () => ({ json: null }),
    })
    const push = vi.spyOn(router, 'push')
    const w = mount(NewCampaignView, { global: { plugins: [router] } })
    expect((w.get('textarea').element as HTMLTextAreaElement).value).toBe('kadın müşteriler')
    await w.get('form').trigger('submit')
    await flushPromises()
    // Created first, then the old draft is deleted: a failed create keeps it.
    expect(calls.map((c) => `${c.method} ${c.path}`)).toEqual(['POST /api/campaigns', 'DELETE /api/campaigns/12'])
    expect(push).toHaveBeenCalledWith(expect.objectContaining({ name: 'campaign', params: { id: 13 } }))
    push.mockRestore()
  })

  it('opens the new campaign even if the old draft is already gone', async () => {
    window.history.replaceState({ brief: 'kadın müşteriler', replaces: 12 }, '')
    fakeFetch({
      'POST /api/campaigns': () => ({ status: 201, json: { campaign: makeCampaign({ id: 13 }), warnings: [] } }),
      'DELETE /api/campaigns/12': () => ({ status: 404, json: { error: { code: 'not_found', message: 'Kampanya bulunamadı.' } } }),
    })
    const push = vi.spyOn(router, 'push')
    const w = mount(NewCampaignView, { global: { plugins: [router] } })
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(push).toHaveBeenCalledWith(expect.objectContaining({ name: 'campaign', params: { id: 13 } }))
    expect(w.find('[role="alert"]').exists()).toBe(false)
    push.mockRestore()
  })

  it('deletes nothing for a plain new campaign', async () => {
    const calls = fakeFetch({
      'POST /api/campaigns': () => ({ status: 201, json: { campaign: makeCampaign({ id: 13 }), warnings: [] } }),
    })
    const w = mount(NewCampaignView, { global: { plugins: [router] } })
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(calls.map((c) => c.method)).toEqual(['POST'])
  })
})
