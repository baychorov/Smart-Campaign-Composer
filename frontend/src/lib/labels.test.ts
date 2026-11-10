import { describe, expect, it } from 'vitest'
import { formatDate } from './labels'
import { formatTurkey } from './schedule'

describe('formatDate', () => {
  it('shows Turkey time whatever the browser zone, like the send times', () => {
    // 19:50 UTC is 22:50 in Turkey. The tests also run with TZ=UTC (CI).
    expect(formatDate('2026-11-10T19:50:00Z')).toContain('22:50')
    expect(formatDate('2026-11-10T19:50:00Z')).toBe(formatTurkey('2026-11-10T19:50:00Z'))
  })
})
