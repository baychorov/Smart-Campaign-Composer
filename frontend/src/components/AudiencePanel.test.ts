import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import type { Rule } from '../api/types'
import { caseRule, groupRule, makeCampaign } from '../test/fixtures'
import AudiencePanel from './AudiencePanel.vue'

const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', name: 'new', component: {} }] })

function mountPanel(campaign = makeCampaign(), extra: Record<string, unknown> = {}) {
  return mount(AudiencePanel, {
    props: { campaign, warnings: [], error: null, saving: false, locked: false, ...extra },
    global: { plugins: [router] },
  })
}

describe('AudiencePanel', () => {
  it('shows common conditions, the count and the consent lock', () => {
    const w = mountPanel()
    expect(w.get('[data-test="reachable"]').text()).toBe('156')
    expect(w.findAll('[data-test="common"] .cond')).toHaveLength(3)
    expect(w.text()).toContain('Kanal izni filtresi her zaman uygulanır')
  })

  it('shows groups joined by OR with their own counts', () => {
    const c = makeCampaign({
      rule: groupRule(),
      summary: { all: false, common: ['x'], groups: [['a'], ['b'], ['c']] },
      audience: { snapshot: false, reachable: 120, sms: 80, email: 90, groups: [64, 52, 9] },
    })
    const w = mountPanel(c)
    const groups = w.findAll('[data-test="group"]')
    expect(groups).toHaveLength(3)
    expect(groups[1]!.text()).toContain('52 kişi')
    expect(w.findAll('.or')).toHaveLength(2)
    expect(w.get('[data-test="unsupported"]').text()).toContain('Veride karşılığı yok')
  })

  it('emits the rule without the removed condition', async () => {
    const w = mountPanel()
    await w.findAll('[data-test="common"] .remove')[1]!.trigger('click')
    const rule = w.emitted('updateRule')![0]![0] as Rule
    expect(rule.common.map((c) => c.event ?? c.field)).toEqual(['city', 'purchase'])
  })

  it('sends a changed day window on blur, not per keystroke', async () => {
    const w = mountPanel()
    const input = w.get('#cond-c-1')
    await input.setValue(14)
    await input.trigger('change')
    expect((w.emitted('updateRule')![0]![0] as Rule).common[1]!.within_days).toBe(14)
  })

  it('does not offer to remove the last condition', () => {
    const r = caseRule()
    r.common = [r.common[0]!]
    const w = mountPanel(makeCampaign({ rule: r }))
    expect(w.find('.remove').exists()).toBe(false)
    expect(w.find('.token-x').exists()).toBe(false)
  })

  it('is read-only while locked', () => {
    const w = mountPanel(makeCampaign(), { locked: true })
    expect(w.find('.remove').exists()).toBe(false)
    expect(w.get('#cond-c-1').attributes('disabled')).toBeDefined()
  })

  it('explains a rejected brief instead of showing rule cards', () => {
    const rejected: Rule = { ...caseRule(), status: 'rejected', common: [], unsupported: [{ text: 'Python', reason: 'off_topic', group: null }] }
    const w = mountPanel(makeCampaign({ rule: rejected, approvable: false, audience: null }))
    expect(w.text()).toContain('gönderilebilir bir kitle çıkmadı')
    expect(w.text()).toContain('Kampanya isteği değil')
    expect(w.find('.cond').exists()).toBe(false)
  })

  it('lists the issues of a refused rule edit', () => {
    const error = { message: 'Kural geçerli değil.', issues: [{ path: 'common[0].value', code: 'out_of_range', message: 'yaş 13–100 olmalı' }] }
    const w = mountPanel(makeCampaign(), { error })
    expect(w.text()).toContain('Kural değiştirilmedi')
    expect(w.text()).toContain('yaş 13–100 olmalı')
  })
})
