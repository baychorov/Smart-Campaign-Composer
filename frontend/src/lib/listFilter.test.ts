import { describe, expect, it } from 'vitest'
import { activeCount, apiQuery, EMPTY_FILTER, fromQuery, presetOf, sameFilter, toQuery, type ListFilter } from './listFilter'

const f = (over: Partial<ListFilter>): ListFilter => ({ ...EMPTY_FILTER, ...over })

describe('list filter', () => {
  it('round-trips through the URL in a fixed order', () => {
    const filter = f({ statuses: ['iptal', 'gecersiz'], id: 42, from: '2026-11-01', to: '2026-11-10' })
    const q = toQuery(filter)
    expect(q).toEqual({ durum: 'gecersiz,iptal', no: '42', baslangic: '2026-11-01', bitis: '2026-11-10' })
    expect(fromQuery(q)).toEqual({ ...filter, statuses: ['gecersiz', 'iptal'] })
    expect(toQuery(EMPTY_FILTER)).toEqual({})
  })

  it('ignores unknown or malformed URL values', () => {
    expect(fromQuery({ durum: 'taslak,arsiv', no: 'abc', baslangic: '10.11.2026', bitis: '2026-11-10' })).toEqual(
      f({ statuses: ['taslak'], to: '2026-11-10' }),
    )
    expect(fromQuery({ no: '0' }).id).toBeNull()
    expect(fromQuery({ no: '-3' }).id).toBeNull()
    expect(fromQuery({ filtre: 'taslak' })).toEqual(EMPTY_FILTER) // the old query is ignored
  })

  it('builds the API query: "Gönderiliyor" covers approved and sending', () => {
    expect(apiQuery(EMPTY_FILTER)).toBe('')
    const q = new URLSearchParams(apiQuery(f({ statuses: ['gonderiliyor', 'taslak'], id: 7, from: '2026-11-01' })))
    expect(q.get('status')).toBe('draft,approved,sending')
    expect(q.get('id')).toBe('7')
    expect(q.get('from')).toBe('2026-11-01')
    expect(q.has('to')).toBe(false)
  })

  it('knows the quick filters and counts what else is in use', () => {
    expect(presetOf(EMPTY_FILTER)).toBe('all')
    expect(presetOf(f({ statuses: ['taslak'] }))).toBe('draft')
    expect(presetOf(f({ statuses: ['tamamlandi', 'gonderiliyor'] }))).toBe('sent')
    expect(presetOf(f({ statuses: ['taslak', 'gecersiz'] }))).toBeNull()
    expect(presetOf(f({ statuses: ['taslak'], id: 3 }))).toBeNull()

    expect(activeCount(EMPTY_FILTER)).toBe(0)
    expect(activeCount(f({ statuses: ['iptal', 'zamanlandi'], from: '2026-11-01', to: '2026-11-02' }))).toBe(2)
    expect(activeCount(f({ statuses: ['iptal'], id: 5, to: '2026-11-02' }))).toBe(3)
    expect(sameFilter(f({ statuses: ['iptal', 'taslak'] }), f({ statuses: ['taslak', 'iptal'] }))).toBe(true)
  })
})
