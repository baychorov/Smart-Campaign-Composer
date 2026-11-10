import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import NumberField from './NumberField.vue'

const box = (w: ReturnType<typeof mount>) => w.get('input').element as HTMLInputElement

describe('NumberField', () => {
  it('shows the stored value again when the backend refused the change', async () => {
    const w = mount(NumberField, { props: { value: 25, min: 13, max: 100, label: 'Yaş', disabled: false } })
    await w.get('input').setValue('200')
    expect(w.emitted('commit')?.at(-1)).toEqual([200])
    // Saving disables the field; the refused change leaves the value at 25.
    await w.setProps({ disabled: true })
    await w.setProps({ disabled: false })
    expect(box(w).value).toBe('25')
  })

  it('takes the new value when the change was stored', async () => {
    const w = mount(NumberField, { props: { value: 25, min: 13, max: 100, label: 'Yaş', disabled: false } })
    await w.get('input').setValue('30')
    await w.setProps({ disabled: true })
    await w.setProps({ value: 30, disabled: false })
    expect(box(w).value).toBe('30')
  })

  it('does not send 0 for an emptied box', async () => {
    const w = mount(NumberField, { props: { value: 25, min: 13, max: 100, label: 'Yaş', disabled: false } })
    await w.get('input').setValue('')
    expect(w.emitted('commit')).toBeUndefined()
    expect(box(w).value).toBe('25')
  })
})
