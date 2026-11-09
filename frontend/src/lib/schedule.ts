// Send time helpers (PLAN step 15a). The marketer picks a time in Turkey
// (UTC+3 all year since 2016), whatever zone the browser is in; the API
// gets an RFC 3339 time with the +03:00 offset.

const TR_OFFSET_MS = 3 * 60 * 60 * 1000

/** Limits, the same as the backend's (campaign.MinScheduleLead/MaxScheduleLead). */
export const MIN_LEAD_MS = 5 * 60 * 1000
export const MAX_LEAD_MS = 30 * 24 * 60 * 60 * 1000

/** An API time as the value of a datetime-local input, in Turkey time ("2026-11-02T10:00"). */
export function toTurkeyInput(iso: string): string {
  return new Date(new Date(iso).getTime() + TR_OFFSET_MS).toISOString().slice(0, 16)
}

/** A datetime-local value read as Turkey time, for the API ("2026-11-02T10:00:00+03:00"). */
export function fromTurkeyInput(value: string): string {
  return `${value}:00+03:00`
}

/** Why the chosen time cannot be used, or null. An empty value means "send now". */
export function sendAtIssue(value: string, now: Date): string | null {
  if (!value) return null
  const t = new Date(fromTurkeyInput(value)).getTime()
  if (Number.isNaN(t)) return 'Gönderim zamanı geçersiz'
  if (t < now.getTime() + MIN_LEAD_MS) return 'Gönderim zamanı en erken 5 dakika sonrası olabilir'
  if (t > now.getTime() + MAX_LEAD_MS) return 'Gönderim zamanı en fazla 30 gün sonrası olabilir'
  return null
}

/** An API time shown in Turkey time, e.g. "2 Kas 2026 10:00". */
export function formatTurkey(iso: string): string {
  return new Date(iso).toLocaleString('tr-TR', { timeZone: 'Europe/Istanbul', dateStyle: 'medium', timeStyle: 'short' })
}
