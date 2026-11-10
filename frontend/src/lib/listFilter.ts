import type { ListStatus } from '../api/types'

// The campaign list filter: statuses, a campaign number and a date range.
// It lives in the URL (?durum=taslak,iptal&no=12&baslangic=2026-11-01), so a
// reload or "back" from a campaign shows the same list.

/** The status choices of the filter panel; "Gönderiliyor" covers approved and sending. */
export const STATUS_CHOICES = [
  { key: 'taslak', label: 'Taslak', statuses: ['draft'] },
  { key: 'gecersiz', label: 'Geçersiz', statuses: ['invalid'] },
  { key: 'zamanlandi', label: 'Zamanlandı', statuses: ['scheduled'] },
  { key: 'gonderiliyor', label: 'Gönderiliyor', statuses: ['approved', 'sending'] },
  { key: 'tamamlandi', label: 'Tamamlandı', statuses: ['completed'] },
  { key: 'iptal', label: 'İptal edildi', statuses: ['cancelled'] },
] as const satisfies readonly { key: string; label: string; statuses: readonly ListStatus[] }[]

export type StatusKey = (typeof STATUS_CHOICES)[number]['key']

export interface ListFilter {
  /** Chosen status choices; empty means every status. */
  statuses: StatusKey[]
  /** Campaign number, or null. */
  id: number | null
  /** Days ("2026-11-01"), both included; empty means open. */
  from: string
  to: string
}

export const EMPTY_FILTER: ListFilter = { statuses: [], id: null, from: '', to: '' }

/** The quick filters above the list. */
export const PRESETS: { value: 'all' | 'draft' | 'sent'; label: string; statuses: StatusKey[] }[] = [
  { value: 'all', label: 'Tümü', statuses: [] },
  { value: 'draft', label: 'Taslaklar', statuses: ['taslak'] },
  { value: 'sent', label: 'Gönderilenler', statuses: ['gonderiliyor', 'tamamlandi'] },
]

const KEYS = STATUS_CHOICES.map((c) => c.key) as StatusKey[]
const DAY = /^\d{4}-\d{2}-\d{2}$/

/** Keeps the choice order, so two filters with the same choices are equal. */
function ordered(keys: StatusKey[]): StatusKey[] {
  return KEYS.filter((k) => keys.includes(k))
}

const first = (v: unknown): string => (Array.isArray(v) ? String(v[0] ?? '') : typeof v === 'string' ? v : '')

/** The filter in a route query; unknown values are ignored. */
export function fromQuery(q: Record<string, unknown>): ListFilter {
  const statuses = ordered(first(q.durum).split(',') as StatusKey[])
  const n = Number(first(q.no))
  const day = (v: unknown) => (DAY.test(first(v)) ? first(v) : '')
  return { statuses, id: Number.isInteger(n) && n > 0 ? n : null, from: day(q.baslangic), to: day(q.bitis) }
}

/** The route query of a filter; an empty filter has none. */
export function toQuery(f: ListFilter): Record<string, string> {
  const q: Record<string, string> = {}
  const statuses = ordered(f.statuses)
  if (statuses.length) q.durum = statuses.join(',')
  if (f.id) q.no = String(f.id)
  if (f.from) q.baslangic = f.from
  if (f.to) q.bitis = f.to
  return q
}

/** The API query string of a filter, without paging. */
export function apiQuery(f: ListFilter): string {
  const p = new URLSearchParams()
  const statuses = STATUS_CHOICES.filter((c) => f.statuses.includes(c.key)).flatMap((c) => c.statuses)
  if (statuses.length) p.set('status', statuses.join(','))
  if (f.id) p.set('id', String(f.id))
  if (f.from) p.set('from', f.from)
  if (f.to) p.set('to', f.to)
  return p.toString()
}

/** The quick filter this filter is exactly, or null for a custom one. */
export function presetOf(f: ListFilter): (typeof PRESETS)[number]['value'] | null {
  if (f.id || f.from || f.to) return null
  const keys = ordered(f.statuses).join(',')
  return PRESETS.find((p) => ordered(p.statuses).join(',') === keys)?.value ?? null
}

/** How many parts of the panel are in use (statuses, number, dates), for the icon's badge. */
export function activeCount(f: ListFilter): number {
  return (f.statuses.length ? 1 : 0) + (f.id ? 1 : 0) + (f.from || f.to ? 1 : 0)
}

export const sameFilter = (a: ListFilter, b: ListFilter) => JSON.stringify(toQuery(a)) === JSON.stringify(toQuery(b))
