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
      sendAt: '',
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

  it('shows the send time and switches the button between send and schedule', async () => {
    const now = mountBar()
    expect(now.get('[data-test="approve"]').text()).toContain('Onayla ve gönder')
    expect(now.find('[data-test="send-now"]').exists()).toBe(false)

    const later = mountBar({ sendAt: '2026-11-02T18:30' })
    expect((later.get('[data-test="send-date"]').element as HTMLInputElement).value).toBe('2026-11-02')
    expect((later.get('[data-test="send-hour"]').element as HTMLSelectElement).value).toBe('18')
    expect((later.get('[data-test="send-minute"]').element as HTMLInputElement).value).toBe('30')
    expect(later.text()).not.toContain('Türkiye saati')
    expect(later.get('[data-test="approve"]').text()).toContain('Onayla ve zamanla')
    await later.get('[data-test="send-now"]').trigger('click')
    expect(later.emitted('update:sendAt')![0]).toEqual([''])
  })

  it('picks the time in 24-hour parts: a new date starts at 10:00, no date sends now', async () => {
    const now = mountBar()
    expect(now.get('[data-test="send-hour"]').attributes('disabled')).toBeDefined()
    await now.get('[data-test="send-date"]').setValue('2026-11-03')
    expect(now.emitted('update:sendAt')![0]).toEqual(['2026-11-03T10:00'])

    const later = mountBar({ sendAt: '2026-11-02T10:00' })
    const hours = later.get('[data-test="send-hour"]').findAll('option').map((o) => o.text())
    expect(hours).toHaveLength(24)
    expect(hours.at(-1)).toBe('23') // no AM/PM
    await later.get('[data-test="send-hour"]').setValue('21')
    expect(later.emitted('update:sendAt')![0]).toEqual(['2026-11-02T21:00'])
    await later.get('[data-test="send-minute"]').setValue('45')
    expect(later.emitted('update:sendAt')![1]).toEqual(['2026-11-02T21:45']) // keeps the new hour
    await later.get('[data-test="send-date"]').setValue('')
    expect(later.emitted('update:sendAt')![2]).toEqual([''])
  })

  it('takes any minute typed from 00 to 59', async () => {
    const bar = mountBar({ sendAt: '2026-11-02T10:00' })
    const minute = bar.get('[data-test="send-minute"]')
    const el = minute.element as HTMLInputElement

    await minute.setValue('37') // two digits apply while typing
    expect(bar.emitted('update:sendAt')!.at(-1)).toEqual(['2026-11-02T10:37'])

    el.value = '7' // one digit is padded on leaving the field
    await minute.trigger('change')
    expect(bar.emitted('update:sendAt')!.at(-1)).toEqual(['2026-11-02T10:07'])
    expect(el.value).toBe('07')

    const before = bar.emitted('update:sendAt')!.length
    el.value = '75' // out of range: not applied, put back
    await minute.trigger('input')
    await minute.trigger('change')
    expect(bar.emitted('update:sendAt')!).toHaveLength(before)
    expect(el.value).toBe('07') // back to the last valid minute
  })

  it('shows a send time issue in red', () => {
    const issue = 'Lütfen ileri bir tarih/saat seçiniz'
    const bar = mountBar({ sendAt: '2026-11-01T09:00', reasons: ['En az bir kanal seç', issue], timeIssue: issue })
    const pills = bar.findAll('.reasons li')
    expect(pills.find((p) => p.text() === issue)!.classes()).toContain('bad')
    expect(pills.find((p) => p.text() === 'En az bir kanal seç')!.classes()).not.toContain('bad')
    expect(bar.get('.send-at').classes()).toContain('invalid')
  })

  it('reports channel changes and the click', async () => {
    const w = mountBar()
    await w.get('[data-test="ch-sms"]').setValue(false)
    expect(w.emitted('update:channels')![0]).toEqual([{ sms: false, email: true }])
    await w.get('[data-test="approve"]').trigger('click')
    expect(w.emitted('approve')).toHaveLength(1)
  })
})
