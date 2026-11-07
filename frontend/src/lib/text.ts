// Text helpers for the message editor. The backend is the authority on
// limits (worst-case render with the audience's longest values, D-CONTENT);
// these only drive the live counter and the preview.

/**
 * Counts Unicode code points, like Go's utf8.RuneCountInString. String
 * length in JS counts UTF-16 units, which is wrong for emoji; the spread
 * iterates code points.
 */
export function runes(s: string): number {
  return [...s].length
}

/** Fallback for an empty first name, same as the backend (D-CONTENT). */
export const FIRST_NAME_FALLBACK = 'Değerli Müşterimiz'

export const VARIABLES = ['first_name', 'city'] as const

const VAR_RE = /\{\{\s*([A-Za-z_]+)\s*\}\}/g

export interface TextPart {
  text: string
  /** Variable name when this part is a {{variable}}. */
  variable?: string
}

/** Splits a template into literal text and {{variable}} parts. */
export function splitVariables(template: string): TextPart[] {
  const parts: TextPart[] = []
  let last = 0
  for (const m of template.matchAll(VAR_RE)) {
    if (m.index > last) parts.push({ text: template.slice(last, m.index) })
    parts.push({ text: m[0], variable: m[1] })
    last = m.index + m[0].length
  }
  if (last < template.length) parts.push({ text: template.slice(last) })
  return parts
}

/** Renders a template for the preview. Unknown variables stay as written. */
export function renderPreview(template: string, values: Record<string, string>): string {
  return template.replace(VAR_RE, (raw, name: string) => {
    if (name === 'first_name') return values.first_name || FIRST_NAME_FALLBACK
    return values[name] ?? raw
  })
}

/** Rune limits per field (R-CNT-1, R-CNT-2; body cap from D-CONTENT). */
export const LIMITS = { sms: 160, email_subject: 80, email_body: 2000 } as const
