import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter, createWebHistory } from 'vue-router'
import type { Campaign, Progress } from '../api/types'
import { FakeEventSource } from '../test/fakeEventSource'
import { fakeFetch } from '../test/fakeFetch'
import { goodMessage, groupRule, makeCampaign, validValidation } from '../test/fixtures'
import CampaignView from './CampaignView.vue'

// The page end to end against a scripted API: fetch and EventSource are fakes.

const routes = [
  { path: '/', name: 'new', component: {} },
  { path: '/campaigns', name: 'campaigns', component: {} },
  { path: '/campaigns/:id', name: 'campaign', component: {} },
]
const router = createRouter({ history: createMemoryHistory(), routes })

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
      body: { channels: { sms: true, email: false }, partial_ack: true, send_at: null },
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

  it('fills the send time from the brief and schedules with it', async () => {
    vi.useFakeTimers({ now: new Date('2026-11-01T09:00:00Z'), toFake: ['Date'] })
    const draft = makeCampaign({ message: goodMessage, content_validation: validValidation, send_at: '2026-11-02T07:00:00Z' })
    const scheduled: Campaign = { ...draft, status: 'scheduled', approved_at: '2026-11-01T09:00:00Z', content_validation: null }
    const calls = fakeFetch({
      'GET /api/campaigns/7': () => ({ json: { campaign: draft } }),
      'POST /api/campaigns/7/approve': () => ({ json: { campaign: scheduled, snapshot: { sms: 111, email: 118 } } }),
    })
    const w = mountView()
    await flushPromises()

    // Turkey time, filled from the suggestion
    expect((w.get('[data-test="send-date"]').element as HTMLInputElement).value).toBe('2026-11-02')
    expect((w.get('[data-test="send-hour"]').element as HTMLSelectElement).value).toBe('10')
    // the marketer changes it on the approval screen
    await w.get('[data-test="send-date"]').setValue('2026-11-03')
    await w.get('[data-test="send-hour"]').setValue('09')
    await w.get('[data-test="send-minute"]').setValue('30')
    await w.get('[data-test="approve"]').trigger('click')
    await flushPromises()

    expect(calls.at(-1)!.body).toEqual({ channels: { sms: true, email: true }, partial_ack: false, send_at: '2026-11-03T09:30:00+03:00' })
    expect(w.get('[data-test="status"]').text()).toBe('Zamanlandı')
    expect(w.get('[data-test="scheduled"]').text()).toContain('10:00')
    vi.useRealTimers()
  })

  it('cancels a scheduled campaign after confirmation', async () => {
    const scheduled = makeCampaign({ status: 'scheduled', approved_at: '2026-11-01T09:00:00Z', send_at: '2026-11-02T07:00:00Z' })
    const calls = fakeFetch({
      'GET /api/campaigns/7': () => ({ json: { campaign: scheduled } }),
      'POST /api/campaigns/7/cancel': () => ({ json: { campaign: { ...scheduled, status: 'cancelled' } } }),
    })
    const w = mountView()
    await flushPromises()

    await w.get('[data-test="cancel"]').trigger('click')
    expect(calls.map((c) => c.path)).not.toContain('/api/campaigns/7/cancel')
    await w.get('[data-test="confirm-cancel"]').trigger('click')
    await flushPromises()

    expect(w.get('[data-test="status"]').text()).toBe('İptal edildi')
    expect(w.find('[data-test="cancelled"]').exists()).toBe(true)
  })

  it('shows that sending won when the cancel comes too late', async () => {
    const scheduled = makeCampaign({ status: 'scheduled', approved_at: '2026-11-01T09:00:00Z', send_at: '2026-11-02T07:00:00Z' })
    let current = scheduled
    fakeFetch({
      'GET /api/campaigns/7': () => ({ json: { campaign: current } }),
      'POST /api/campaigns/7/cancel': () => {
        current = { ...scheduled, status: 'sending' }
        return { status: 409, json: { error: 'sending_started', message: 'Gönderim bu sırada başladı; kampanya iptal edilemedi.' } }
      },
    })
    const w = mountView()
    await flushPromises()
    await w.get('[data-test="cancel"]').trigger('click')
    await w.get('[data-test="confirm-cancel"]').trigger('click')
    await flushPromises()

    expect(w.get('[data-test="status"]').text()).toBe('Gönderiliyor')
  })

  it('shows "Kampanya 7" and goes back to the list it came from, or opens the list', async () => {
    fakeFetch({ 'GET /api/campaigns/7': () => ({ json: { campaign: makeCampaign({ message: goodMessage }) } }) })

    // Came from the list with a filter: back in history, so the list keeps its state.
    const web = createRouter({ history: createWebHistory(), routes })
    await web.push('/campaigns?filtre=taslak')
    await web.push('/campaigns/7')
    const w = mount(CampaignView, { props: { id: 7 }, global: { plugins: [web] } })
    await flushPromises()
    expect(w.text()).toContain('Kampanya 7')
    expect(w.text()).not.toContain('#7')
    const back = vi.spyOn(web, 'back')
    await w.get('[data-test="back"]').trigger('click')
    expect(back).toHaveBeenCalledOnce()

    // Opened from the brief form (or a link): the list opens.
    const direct = createRouter({ history: createMemoryHistory(), routes })
    await direct.push('/campaigns/7')
    const push = vi.spyOn(direct, 'push')
    const d = mount(CampaignView, { props: { id: 7 }, global: { plugins: [direct] } })
    await flushPromises()
    await d.get('[data-test="back"]').trigger('click')
    expect(push).toHaveBeenCalledWith({ name: 'campaigns' })
  })

  it('shows the messages for the marketer: plain counters, an SMS bubble, no developer notes', async () => {
    const draft = makeCampaign({ message: goodMessage, content_validation: validValidation })
    fakeFetch({ 'GET /api/campaigns/7': () => ({ json: { campaign: draft } }) })
    const w = mountView()
    await flushPromises()

    expect(w.get('[data-test="counter-sms"]').text()).toBe('46 / 160') // typed length of the SMS, not the worst case
    expect(w.text()).not.toContain('en kötü durum')
    expect(w.find('.meter').exists()).toBe(false)
    expect(w.get('[data-test="sms-preview"] .bubble').text()).toBe('Merhaba Ayşe, sepetin seni bekliyor!')
    expect(w.get('label[for="msg-email_body"]').text()).toBe('Gövde')
    expect(w.text()).not.toContain('Backend doğruladı')
    expect(w.text()).not.toContain('Değişkenler:')
    expect(w.text()).not.toContain('İsim boş')
  })

  it('still turns the counter red when the worst case is over the limit', async () => {
    const stats = { ...validValidation.stats, sms: { runes: 150, worst_case_runes: 165, limit: 160 } }
    const draft = makeCampaign({ message: goodMessage, content_validation: { ...validValidation, valid: false, stats } })
    fakeFetch({ 'GET /api/campaigns/7': () => ({ json: { campaign: draft } }) })
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-test="counter-sms"]').classes()).toContain('over')
  })

  it('shows the dashboard without developer notes: short labels, spaced approval time, centred "Hata yok"', async () => {
    const done = makeCampaign({ status: 'completed', approved_at: '2026-11-07T10:00:00Z', message: goodMessage })
    fakeFetch({ 'GET /api/campaigns/7': () => ({ json: { campaign: done } }) })
    const w = mountView()
    await flushPromises()
    const es = FakeEventSource.last()
    es.open()
    es.emit('progress', progress({ status: 'completed', done: true, top_errors: [] }))
    await flushPromises()

    const text = w.text()
    expect(text).toContain('gönderim tamamlandı')
    for (const gone of ['akış kapandı', 'son 10 sn', '(izin)', 'worker 95', '5xx', 'En sık']) expect(text).not.toContain(gone)
    expect(text).toContain('Kanal başına sınır 100 mesaj/sn')
    const meta = w.get('[data-test="meta"] .meta-approved')
    // Date and time as separate parts (the browser's own zone, so only the shape is checked).
    const [date, time] = meta.findAll('.num').map((n) => n.text())
    expect(date).toMatch(/^\d{2}\.11\.2026$/)
    expect(time).toMatch(/^\d{2}:\d{2}$/)
    const errors = w.get('[data-test="errors-card"]')
    expect(errors.get('.card-title').text()).toBe('Hatalar')
    expect(errors.get('.no-errors').text()).toBe('Hata yok.')
  })

  it('says what the live dashboard is waiting for: sending now, or the scheduled time', async () => {
    const sending = makeCampaign({ status: 'sending', approved_at: '2026-11-07T10:00:00Z', message: goodMessage })
    fakeFetch({ 'GET /api/campaigns/7': () => ({ json: { campaign: sending } }) })
    const w = mountView()
    await flushPromises()
    FakeEventSource.last().open()
    FakeEventSource.last().emit('progress', progress())
    await flushPromises()
    expect(w.get('[data-test="meta"]').text()).toContain('gönderiliyor')
    expect(w.text()).not.toContain('saniyede bir')

    const scheduled = makeCampaign({ status: 'scheduled', approved_at: '2026-11-01T09:00:00Z', send_at: '2026-11-02T07:00:00Z' })
    fakeFetch({ 'GET /api/campaigns/7': () => ({ json: { campaign: scheduled } }) })
    const s = mountView()
    await flushPromises()
    FakeEventSource.last().open()
    FakeEventSource.last().emit('progress', progress({ status: 'scheduled', channels: {}, rate_per_second: 0, top_errors: [] }))
    await flushPromises()
    expect(s.get('[data-test="meta"]').text()).toContain('belirlenen tarihte gönderilecek')
    const note = s.get('[data-test="scheduled"]').text()
    expect(note).toContain('10:00')
    expect(note).not.toContain('Türkiye saati')
    expect(note).not.toContain('Kitle onay anında')
  })

  it('shows "Geçersiz" for a draft whose brief gave no approvable audience', async () => {
    const rule = { ...makeCampaign().rule, status: 'rejected' as const, common: [], unsupported: [{ text: 'cinsiyet', reason: 'unknown_field' as const, group: null }] }
    const invalid = makeCampaign({ rule, approvable: false, audience: null })
    fakeFetch({ 'GET /api/campaigns/7': () => ({ json: { campaign: invalid } }) })
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-test="status"]').text()).toBe('Geçersiz')
    expect(w.get('[data-test="status"]').classes()).toContain('invalid')
  })
})
