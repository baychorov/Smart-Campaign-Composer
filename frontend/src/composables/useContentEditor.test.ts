import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, ref } from 'vue'
import type { Campaign } from '../api/types'
import { FakeEventSource } from '../test/fakeEventSource'
import { fakeFetch } from '../test/fakeFetch'
import { goodMessage, makeCampaign, validValidation } from '../test/fixtures'
import { useContentEditor } from './useContentEditor'

const flush = () => new Promise((r) => setTimeout(r, 0))

beforeEach(() => sessionStorage.clear())
afterEach(() => {
  FakeEventSource.reset()
  vi.unstubAllGlobals()
})

function setup(c = makeCampaign()) {
  const campaign = ref<Campaign | null>(c)
  const scope = effectScope()
  const editor = scope.run(() => useContentEditor(campaign, (x) => (campaign.value = x), FakeEventSource.create))!
  editor.init(c)
  return { editor, campaign }
}

describe('useContentEditor', () => {
  it('streams, then stores the validated message with PUT /content', async () => {
    const saved = makeCampaign({ message: goodMessage, content_validation: validValidation })
    const calls = fakeFetch({
      'POST /api/campaigns/7/content/generations': () => ({ status: 202, json: { id: 'G1', events_url: '' } }),
      'PUT /api/campaigns/7/content': () => ({ json: { campaign: saved, validation: validValidation } }),
    })
    const { editor, campaign } = setup()

    await editor.generate()
    const es = FakeEventSource.last()
    expect(es.url).toBe('/api/content/generations/G1/events')
    expect(sessionStorage.getItem('scc.generation.7')).toBe('G1')

    es.emit('started', {})
    es.emit('sms.delta', { text: 'Merhaba' })
    expect(editor.running.value).toBe(true)
    expect(editor.gen.value.text.sms).toBe('Merhaba')

    es.emit('validated', { message: goodMessage, stats: {} })
    await flush()
    expect(calls.at(-1)).toEqual({ method: 'PUT', path: '/api/campaigns/7/content', body: goodMessage })
    expect(campaign.value?.content_validation?.valid).toBe(true)
    expect(editor.dirty.value).toBe(false)
    expect(es.closed).toBe(true)
    expect(sessionStorage.getItem('scc.generation.7')).toBeNull()
  })

  it('keeps an invalid candidate as unsaved work and does not store it', async () => {
    const calls = fakeFetch({
      'POST /api/campaigns/7/content/generations': () => ({ status: 202, json: { id: 'G2', events_url: '' } }),
    })
    const { editor } = setup()
    await editor.generate()
    const es = FakeEventSource.last()
    es.emit('sms.delta', { text: 'aday' })
    es.emit('invalid', { issues: [{ path: 'sms', code: 'too_long', message: 'uzun' }] })
    await flush()
    expect(editor.draft.value.sms).toBe('aday')
    expect(editor.dirty.value).toBe(true)
    expect(calls.map((c) => c.method)).toEqual(['POST'])
  })

  it('shows the stored message again after a failed regeneration, so the screen matches what approval sends', async () => {
    fakeFetch({ 'POST /api/campaigns/7/content/generations': () => ({ status: 202, json: { id: 'G4', events_url: '' } }) })
    const { editor } = setup(makeCampaign({ message: goodMessage, content_validation: validValidation }))
    await editor.generate()
    const es = FakeEventSource.last()
    es.emit('sms.delta', { text: 'yarım kalan' })
    es.emit('failed', { code: 'llm_unavailable', message: 'Claude\'a şu anda ulaşılamıyor.' })
    expect(editor.gen.value.phase).toBe('failed')
    expect(editor.draft.value).toEqual(goodMessage)
    expect(editor.dirty.value).toBe(false)
  })

  it('ends a refused stream as a failure and forgets it, instead of waiting forever', async () => {
    sessionStorage.setItem('scc.generation.7', 'GONE') // e.g. the API restarted since
    fakeFetch({})
    const { editor } = setup(makeCampaign({ message: goodMessage, content_validation: validValidation }))
    expect(editor.running.value).toBe(true)
    FakeEventSource.last().fail(2)
    await flush()
    expect(editor.running.value).toBe(false)
    expect(editor.gen.value.phase).toBe('failed')
    expect(editor.gen.value.failure?.code).toBe('stream')
    expect(editor.draft.value).toEqual(goodMessage)
    expect(sessionStorage.getItem('scc.generation.7')).toBeNull()
  })

  it('keeps waiting while the browser retries a dropped stream', async () => {
    sessionStorage.setItem('scc.generation.7', 'G5')
    fakeFetch({})
    const { editor } = setup()
    FakeEventSource.last().fail(0)
    await flush()
    expect(editor.running.value).toBe(true)
    expect(sessionStorage.getItem('scc.generation.7')).toBe('G5')
  })

  it('follows a generation started before a reload instead of starting a new one', () => {
    sessionStorage.setItem('scc.generation.7', 'G3')
    const calls = fakeFetch({})
    setup()
    expect(FakeEventSource.last().url).toBe('/api/content/generations/G3/events')
    expect(calls).toHaveLength(0)
  })

  it('shows a refused start as a failure', async () => {
    fakeFetch({
      'POST /api/campaigns/7/content/generations': () => ({
        status: 422,
        json: { error: 'rule_not_approvable', message: 'Kural bir kitle tanımlamıyor.' },
      }),
    })
    const { editor } = setup()
    await editor.generate()
    expect(editor.gen.value.phase).toBe('failed')
    expect(editor.gen.value.failure?.message).toBe('Kural bir kitle tanımlamıyor.')
  })
})
