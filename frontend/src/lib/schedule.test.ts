import { describe, expect, it } from 'vitest'
import { formatTurkey, fromTurkeyInput, sendAtIssue, toTurkeyInput } from './schedule'

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
    expect(sendAtIssue('2026-11-01T12:04', now)).toContain('5 dakika')
    expect(sendAtIssue('2026-11-01T12:05', now)).toBeNull()
    expect(sendAtIssue('2026-12-01T12:00', now)).toBeNull()
    expect(sendAtIssue('2026-12-01T12:01', now)).toContain('30 gün')
  })
})
