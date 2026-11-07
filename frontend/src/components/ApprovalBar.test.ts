import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import ApprovalBar from './ApprovalBar.vue'

function mountBar(props: Record<string, unknown> = {}) {
  return mount(ApprovalBar, {
    props: {
      smsCount: 111,
      emailCount: 118,
      partial: false,
      reasons: [],
      busy: false,
      channels: { sms: true, email: true },
      partialAck: false,
      ...props,
    },
  })
}

describe('ApprovalBar', () => {
  it('shows the message total of the selected channels', () => {
    expect(mountBar().get('[data-test="approve"]').text()).toContain('229 mesaj')
    expect(mountBar({ channels: { sms: false, email: true } }).get('[data-test="approve"]').text()).toContain('118 mesaj')
  })

  it('is disabled while there is a reason, and lists it', () => {
    const w = mountBar({ reasons: ['Uygulanmayan kriterleri onayla'] })
    expect(w.get('[data-test="approve"]').attributes('disabled')).toBeDefined()
    expect(w.text()).toContain('Uygulanmayan kriterleri onayla')
  })

  it('shows the acknowledgement only for a partial rule', async () => {
    expect(mountBar().find('[data-test="partial-ack"]').exists()).toBe(false)
    const w = mountBar({ partial: true })
    await w.get('[data-test="partial-ack"]').setValue(true)
    expect(w.emitted('update:partialAck')![0]).toEqual([true])
  })

  it('reports channel changes and the click', async () => {
    const w = mountBar()
    await w.get('[data-test="ch-sms"]').setValue(false)
    expect(w.emitted('update:channels')![0]).toEqual([{ sms: false, email: true }])
    await w.get('[data-test="approve"]').trigger('click')
    expect(w.emitted('approve')).toHaveLength(1)
  })
})
