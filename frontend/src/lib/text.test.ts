import { describe, expect, it } from 'vitest'
import { FIRST_NAME_FALLBACK, renderPreview, runes, splitVariables } from './text'

describe('runes', () => {
  it.each([
    ['ascii', 'abc', 3],
    ['Turkish letters are one rune each', 'İğüşöçı', 7],
    ['an emoji is one rune, two UTF-16 units', '👋', 1],
    ['160 Turkish characters', 'ğ'.repeat(160), 160],
  ])('%s', (_name, s, want) => {
    expect(runes(s)).toBe(want)
  })
})

describe('renderPreview', () => {
  it('fills variables, with spaces inside the braces', () => {
    expect(renderPreview('Merhaba {{ first_name }}, {{city}}', { first_name: 'Ayşe', city: 'İzmir' })).toBe('Merhaba Ayşe, İzmir')
  })
  it('uses the fallback for an empty first name', () => {
    expect(renderPreview('{{first_name}}!', { first_name: '' })).toBe(FIRST_NAME_FALLBACK + '!')
  })
  it('leaves unknown variables as written', () => {
    expect(renderPreview('Kod: {{kupon}}', { first_name: 'A' })).toBe('Kod: {{kupon}}')
  })
})

describe('splitVariables', () => {
  it('splits literal text and variables', () => {
    expect(splitVariables('Hi {{first_name}}!')).toEqual([{ text: 'Hi ' }, { text: '{{first_name}}', variable: 'first_name' }, { text: '!' }])
  })
  it('treats single braces as text', () => {
    expect(splitVariables('{SEPET10}')).toEqual([{ text: '{SEPET10}' }])
  })
})
