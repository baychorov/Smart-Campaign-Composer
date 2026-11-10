import { describe, expect, it } from 'vitest'
import { formatTurkey, fromTurkeyInput, HOURS, joinSendAt, parseMinute, PAST_SEND_AT, sendAtIssue, splitSendAt, toTurkeyInput } from './schedule'

describe('send time helpers', () => {
  it('converts between API times and Turkey input values in any browser zone', () => {
    expect(toTurkeyInput('2026-11-02T07:00:00Z')).toBe('2026-11-02T10:00')
    expect(toTurkeyInput('2026-11-02T10:00:00+03:00')).toBe('2026-11-02T10:00')
    expect(toTurkeyInput('2026-10-31T21:30:00Z')).toBe('2026-11-01T00:30') // next day in Turkey
    expect(fromTurkeyInput('2026-11-02T10:00')).toBe('2026-11-02T10:00:00+03:00')
    expect(formatTurkey('2026-11-02T07:00:00Z')).toContain('10:00')
  })

  it('checks the same limits as the backend', () => {
    const now = new Date('2026-11-01T09:00:00Z') // 12:00 in Turkey
    expect(sendAtIssue('', now)).toBeNull()
    expect(sendAtIssue('2026-11-01T11:00', now)).toBe(PAST_SEND_AT)
    expect(sendAtIssue('2026-10-31T12:30', now)).toBe(PAST_SEND_AT) // an earlier day
    expect(sendAtIssue('2026-11-01T12:00', now)).toBe(PAST_SEND_AT) // the current minute has begun
    expect(sendAtIssue('2026-11-01T12:01', now)).toBeNull() // the next minute is fine
    expect(sendAtIssue('2026-12-01T12:00', now)).toBeNull()
    expect(sendAtIssue('2026-12-01T12:01', now)).toContain('30 gün')
  })

  it('splits and joins the 24-hour date, hour and minute parts', () => {
    expect(HOURS).toHaveLength(24)
    expect(HOURS[0]).toBe('00')
    expect(HOURS[23]).toBe('23')
    expect(splitSendAt('2026-11-02T18:05')).toEqual({ date: '2026-11-02', hour: '18', minute: '05' })
    expect(splitSendAt('')).toEqual({ date: '', hour: '', minute: '' })
    expect(joinSendAt('2026-11-02', '18', '05')).toBe('2026-11-02T18:05')
    expect(joinSendAt('2026-11-02', '', '')).toBe('2026-11-02T10:00') // a newly picked date
    expect(joinSendAt('', '18', '05')).toBe('') // no date: send now
  })

  it('reads a typed minute from 00 to 59', () => {
    expect(parseMinute('7')).toBe('07')
    expect(parseMinute('07')).toBe('07')
    expect(parseMinute('00')).toBe('00')
    expect(parseMinute('59')).toBe('59')
    expect(parseMinute(' 33 ')).toBe('33')
    for (const bad of ['', '60', '99', '-1', 'ab', '1a', '123', '4.5']) expect(parseMinute(bad)).toBeNull()
  })
})
