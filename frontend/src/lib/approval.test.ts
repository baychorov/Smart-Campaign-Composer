import { describe, expect, it } from 'vitest'
import { groupRule, makeCampaign, validValidation } from '../test/fixtures'
import { blockingReasons, type ApprovalInput } from './approval'

const ready = (over: Partial<ApprovalInput> = {}): ApprovalInput => ({
  campaign: makeCampaign({ content_validation: validValidation }),
  channels: { sms: true, email: true },
  partialAck: false,
  genPhase: 'validated',
  dirty: false,
  busy: false,
  ...over,
})

const smsTooLong = {
  ...validValidation,
  valid: false,
  errors: [{ path: 'sms', code: 'too_long', message: 'uzun' }],
}

describe('blockingReasons', () => {
  it('allows a validated ok draft', () => {
    expect(blockingReasons(ready())).toEqual([])
  })

  it('requires the acknowledgement for a partial rule (D-PARTIAL)', () => {
    const partial = makeCampaign({ rule: groupRule(), content_validation: validValidation })
    expect(blockingReasons(ready({ campaign: partial }))).toEqual(['Uygulanmayan kriterleri onayla'])
    expect(blockingReasons(ready({ campaign: partial, partialAck: true }))).toEqual([])
  })

  it('needs a channel', () => {
    expect(blockingReasons(ready({ channels: { sms: false, email: false } }))).toContain('En az bir kanal seç')
  })

  it('ignores content errors of a channel that is not selected', () => {
    const c = makeCampaign({ content_validation: smsTooLong })
    expect(blockingReasons(ready({ campaign: c }))).toContain('Mesajda düzeltilecek hata var')
    expect(blockingReasons(ready({ campaign: c, channels: { sms: false, email: true } }))).toEqual([])
  })

  it('blocks while generating, with unsaved edits, and without messages', () => {
    expect(blockingReasons(ready({ genPhase: 'streaming' }))).toContain('Mesajlar üretiliyor')
    expect(blockingReasons(ready({ dirty: true }))).toContain('Değişiklikler kaydedilmedi')
    expect(blockingReasons(ready({ campaign: makeCampaign() }))).toContain('Mesajlar henüz yok')
  })

  it('blocks a rule without an audience', () => {
    const rejected = makeCampaign({ approvable: false, audience: null, content_validation: validValidation })
    expect(blockingReasons(ready({ campaign: rejected }))).toContain('Kural onaylanabilir değil')
    const empty = makeCampaign({ audience: { snapshot: false, reachable: 0, sms: 0, email: 0 }, content_validation: validValidation })
    expect(blockingReasons(ready({ campaign: empty }))).toContain('Kitle boş')
  })
})
