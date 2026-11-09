import { describe, expect, it } from 'vitest'
import { applyEvent, initialGen, type GenState } from './generation'

const run = (events: [string, unknown][], from: GenState = initialGen()) =>
  events.reduce((s, [name, data]) => applyEvent(s, name, data), from)

describe('generation events', () => {
  it('writes deltas to their field whatever the order', () => {
    const s = run([
      ['started', {}],
      ['email.body.delta', { text: 'Gövde ' }],
      ['sms.delta', { text: 'Mer' }],
      ['email.subject.delta', { text: 'Konu' }],
      ['sms.delta', { text: 'haba' }],
      ['email.body.delta', { text: 'metni' }],
    ])
    expect(s.text).toEqual({ sms: 'Merhaba', email_subject: 'Konu', email_body: 'Gövde metni' })
    expect(s.phase).toBe('streaming')
    expect(s.current).toBe('email_body')
  })

  it('shows "preparing" until the first delta', () => {
    expect(run([['started', {}]]).phase).toBe('preparing')
  })

  it('clears the candidate on reset and counts the repair', () => {
    const s = run([
      ['started', {}],
      ['sms.delta', { text: 'çok uzun aday' }],
      ['reset', {}],
    ])
    expect(s.text.sms).toBe('')
    expect(s.phase).toBe('preparing')
    expect(s.repairs).toBe(1)
  })

  it('takes the validated message from the server, not the streamed text', () => {
    const message = { sms: 'Merhaba', email_subject: 'Konu', email_body: 'Gövde' }
    const s = run([
      ['sms.delta', { text: ' Merhaba ' }],
      ['validated', { message, stats: { sms: { runes: 7, worst_case_runes: 7, limit: 160 } } }],
    ])
    expect(s.phase).toBe('validated')
    expect(s.text).toEqual(message)
    expect(s.stats.sms?.limit).toBe(160)
  })

  it('keeps the candidate and the issues when invalid', () => {
    const s = run([
      ['sms.delta', { text: 'x' }],
      ['invalid', { issues: [{ path: 'sms', code: 'too_long', message: 'uzun' }] }],
    ])
    expect(s.phase).toBe('invalid')
    expect(s.text.sms).toBe('x')
    expect(s.issues[0]?.code).toBe('too_long')
  })

  it('records a failure', () => {
    const s = run([['failed', { code: 'refusal', message: 'Reddetti.' }]])
    expect(s.phase).toBe('failed')
    expect(s.failure).toEqual({ code: 'refusal', message: 'Reddetti.' })
  })
})
