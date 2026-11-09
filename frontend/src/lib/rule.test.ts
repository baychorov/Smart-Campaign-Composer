import { describe, expect, it } from 'vitest'
import { caseRule, groupRule } from '../test/fixtures'
import { isLastCondition, removeCondition, removeValue, setNumber } from './rule'

describe('rule edits', () => {
  it('removes a common condition without touching the input', () => {
    const r = caseRule()
    const next = removeCondition(r, { group: null, index: 1 })
    expect(next.common.map((c) => c.event ?? c.field)).toEqual(['city', 'purchase'])
    expect(r.common).toHaveLength(3)
  })

  it('drops an emptied group and renumbers the unsupported criteria', () => {
    const next = removeCondition(groupRule(), { group: 2, index: 0 })
    expect(next.groups).toHaveLength(2)
    // "sadık" belonged to the removed group: now the whole brief.
    expect(next.unsupported.map((u) => [u.text, u.group])).toEqual([
      ['sadık', null],
      ['en az 3 kez', 1],
      ['yarın sabah', null],
    ])
  })

  it('renumbers unsupported criteria of later groups', () => {
    const r = groupRule()
    r.groups[0]!.conditions = [r.groups[0]!.conditions[0]!]
    const next = removeCondition(r, { group: 0, index: 0 })
    expect(next.groups).toHaveLength(2)
    expect(next.unsupported.map((u) => u.group)).toEqual([1, 0, null])
  })

  it('removes one city; the last city removes the condition', () => {
    const one = removeValue(groupRule(), { group: 2, index: 0 }, 0)
    expect(one.groups[2]!.conditions[0]!.values).toEqual(['Bursa'])
    const none = removeValue(one, { group: 2, index: 0 }, 0)
    expect(none.groups).toHaveLength(2)
  })

  it('sets an age or an event window', () => {
    let r = setNumber(groupRule(), { group: 0, index: 1 }, 29)
    expect(r.groups[0]!.conditions[1]!.value).toBe(29)
    r = setNumber(r, { group: null, index: 0 }, 14)
    expect(r.common[0]!.within_days).toBe(14)
    expect(r.common[0]!.value).toBeUndefined()
  })

  it('sets a count without touching the window', () => {
    const r = caseRule()
    r.common[1] = { type: 'event', event: 'purchase', occurred: true, within_days: 90, min_count: 3 }
    const next = setNumber(r, { group: null, index: 1 }, 4, 'min_count')
    expect(next.common[1]).toMatchObject({ min_count: 4, within_days: 90 })
    expect(setNumber(r, { group: null, index: 1 }, 60, 'within_days').common[1]).toMatchObject({ min_count: 3, within_days: 60 })
  })

  it('knows the last condition of a filtered rule', () => {
    expect(isLastCondition(caseRule())).toBe(false)
    const r = caseRule()
    r.common = [r.common[0]!]
    expect(isLastCondition(r)).toBe(true)
    expect(isLastCondition({ ...r, audience_scope: 'all', common: [] })).toBe(false)
  })
})

describe('rule edits on reactive props', () => {
  it('copy a Vue reactive rule (structuredClone cannot)', async () => {
    const { reactive } = await import('vue')
    const r = reactive(caseRule())
    expect(removeCondition(r, { group: null, index: 0 }).common).toHaveLength(2)
    expect(setNumber(r, { group: null, index: 1 }, 7).common[1]!.within_days).toBe(7)
  })
})
