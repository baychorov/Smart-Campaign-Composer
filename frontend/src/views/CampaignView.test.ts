import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import type { Campaign, Progress } from '../api/types'
import { FakeEventSource } from '../test/fakeEventSource'
import { fakeFetch } from '../test/fakeFetch'
import { goodMessage, groupRule, makeCampaign, validValidation } from '../test/fixtures'
import CampaignView from './CampaignView.vue'

// The page end to end against a scripted API: fetch and EventSource are fakes.

const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', name: 'new', component: {} }] })

beforeEach(() => {
  sessionStorage.clear()
  vi.stubGlobal('EventSource', FakeEventSource)
  vi.stubGlobal('scrollTo', () => {})
})
afterEach(() => {
  FakeEventSource.reset()
  vi.unstubAllGlobals()
})

const mountView = () => mount(CampaignView, { props: { id: 7 }, global: { plugins: [router] } })

const progress = (over: Partial<Progress> = {}): Progress => ({
  campaign_id: 7,
  status: 'sending',
  channels: { sms: { total: 111, pending: 60, sending: 1, sent: 48, failed: 1, skipped: 1 } },
  rate_per_second: 42.5,
  top_errors: [{ error: 'consent withdrawn', count: 1 }],
  done: false,
  at: '2026-11-07T10:00:01Z',
  ...over,
})

describe('CampaignView', () => {
  it('approves a partial draft only after the acknowledgement, then shows the dashboard', async () => {
    const draft = makeCampaign({
      rule: groupRule(),
      summary: { all: false, common: ['x'], groups: [['a'], ['b'], ['c']] },
      message: goodMessage,
      content_validation: validValidation,
    })
    const approved: Campaign = { ...draft, status: 'approved', approved_at: '2026-11-07T10:00:00Z', content_validation: null }
    const calls = fakeFetch({
      'GET /api/campaigns/7': () => ({ json: { campaign: draft } }),
      'POST /api/campaigns/7/approve': () => ({ json: { campaign: approved, snapshot: { sms: 111, email: 0 } } }),
    })
    const w = mountView()
    await flushPromises()

    // A partial rule does not start generation by itself.
    expect(calls.map((c) => `${c.method} ${c.path}`)).toEqual(['GET /api/campaigns/7'])

    const button = w.get('[data-test="approve"]')
    expect(button.attributes('disabled')).toBeDefined()
    await w.get('[data-test="ch-email"]').setValue(false)
    await w.get('[data-test="partial-ack"]').setValue(true)
    expect(button.attributes('disabled')).toBeUndefined()
    await button.trigger('click')
    await flushPromises()

    expect(calls.at(-1)).toEqual({
      method: 'POST',
      path: '/api/campaigns/7/approve',
      body: { channels: { sms: true, email: false }, partial_ack: true },
    })
    expect(w.get('[data-test="status"]').text()).toBe('Onaylandı')

    const es = FakeEventSource.last()
    expect(es.url).toBe('/api/campaigns/7/events')
    es.open()
    es.emit('progress', progress())
    await flushPromises()
    expect(w.get('[data-test="finished"]').text()).toContain('50')
    expect(w.find('[data-test="channel-sms"]').exists()).toBe(true)
    expect(w.find('[data-test="channel-email"]').exists()).toBe(false)
    expect(w.text()).toContain('izin geri çekildi')

    es.emit('progress', progress({ status: 'completed', done: true }))
    await flushPromises()
    expect(w.get('[data-test="dash-status"]').text()).toContain('Tamamlandı')
    expect(es.closed).toBe(true)
  })

  it('starts generation by itself for an ok rule without messages', async () => {
    const calls = fakeFetch({
      'GET /api/campaigns/7': () => ({ json: { campaign: makeCampaign() } }),
      'POST /api/campaigns/7/content/generations': () => ({ status: 202, json: { id: 'G1', events_url: '' } }),
    })
    const w = mountView()
    await flushPromises()
    expect(calls.map((c) => c.method)).toEqual(['GET', 'POST'])
    const es = FakeEventSource.last()
    es.emit('started', {})
    es.emit('sms.delta', { text: 'Merhaba' })
    await flushPromises()
    expect(w.text()).toContain('Merhaba')
    expect(w.get('[data-test="approve"]').attributes('disabled')).toBeDefined()
    expect(w.text()).toContain('Mesajlar üretiliyor')
  })

  it('shows the reasons of a refused approval', async () => {
    const draft = makeCampaign({ message: goodMessage, content_validation: validValidation })
    fakeFetch({
      'GET /api/campaigns/7': () => ({ json: { campaign: draft } }),
      'POST /api/campaigns/7/approve': () => ({
        status: 422,
        json: {
          error: 'approval_blocked',
          message: 'Kampanya onaylanamadı.',
          issues: [{ path: 'message.sms', code: 'too_long', message: 'SMS en kötü durumda 161 karakter' }],
        },
      }),
    })
    const w = mountView()
    await flushPromises()
    await w.get('[data-test="approve"]').trigger('click')
    await flushPromises()
    expect(w.text()).toContain('Kampanya onaylanamadı.')
    expect(w.text()).toContain('SMS en kötü durumda 161 karakter')
    expect(w.get('[data-test="status"]').text()).toBe('Taslak')
  })
})
