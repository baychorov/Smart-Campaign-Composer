import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import NewCampaignView from './NewCampaignView.vue'

// The brief form: short copy, no visible "Brief" label, a neutral loading text.

const router = createRouter({
  history: createMemoryHistory(),
  routes: [
    { path: '/', name: 'new', component: {} },
    { path: '/campaigns/:id', name: 'campaign', component: {} },
  ],
})

afterEach(() => vi.unstubAllGlobals())

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
})
