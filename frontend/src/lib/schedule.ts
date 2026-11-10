// Send time helpers (PLAN step 15a). The marketer picks a time in Turkey
// (UTC+3 all year since 2016), whatever zone the browser is in; the API
// gets an RFC 3339 time with the +03:00 offset.

const TR_OFFSET_MS = 3 * 60 * 60 * 1000

/** The latest send time, the same as the backend's (campaign.MaxScheduleLead). */
export const MAX_LEAD_MS = 30 * 24 * 60 * 60 * 1000

/** An API time as the value of a datetime-local input, in Turkey time ("2026-11-02T10:00"). */
export function toTurkeyInput(iso: string): string {
  return new Date(new Date(iso).getTime() + TR_OFFSET_MS).toISOString().slice(0, 16)
}

/** A datetime-local value read as Turkey time, for the API ("2026-11-02T10:00:00+03:00"). */
export function fromTurkeyInput(value: string): string {
  return `${value}:00+03:00`
}

/** The message for a send time that is not in the future, the same as the backend's. */
export const PAST_SEND_AT = 'Lütfen ileri bir tarih/saat seçiniz'

/**
 * Why the chosen time cannot be used, or null. An empty value means "send
 * now". Any moment after now is accepted, as in the backend (checkSendAt).
 */
export function sendAtIssue(value: string, now: Date): string | null {
  if (!value) return null
  const t = new Date(fromTurkeyInput(value)).getTime()
  if (Number.isNaN(t)) return 'Gönderim zamanı geçersiz'
  if (t <= now.getTime()) return PAST_SEND_AT
  if (t > now.getTime() + MAX_LEAD_MS) return 'Gönderim zamanı en fazla 30 gün sonrası olabilir'
  return null
}

/** An API time shown in Turkey time, e.g. "2 Kas 2026 10:00". */
export function formatTurkey(iso: string): string {
  return new Date(iso).toLocaleString('tr-TR', { timeZone: 'Europe/Istanbul', dateStyle: 'medium', timeStyle: 'short' })
}

// The approval bar picks the send time as a native date field, an hour
// select and a typed minute, so the time is 24-hour whatever the browser's locale is
// (a datetime-local field follows the OS locale and may show AM/PM). The
// value stays a datetime-local string ("2026-11-02T10:00", Turkey time).

/** The time a newly picked date starts with, as the model's default (D-SCHEDULE). */
export const DEFAULT_TIME = { hour: '10', minute: '00' }

export const HOURS = Array.from({ length: 24 }, (_, h) => String(h).padStart(2, '0'))

/** A typed minute ("7", "07", "45") as two digits, or null unless it is 0–59. */
export function parseMinute(typed: string): string | null {
  const v = typed.trim()
  if (!/^\d{1,2}$/.test(v) || Number(v) > 59) return null
  return v.padStart(2, '0')
}

/** The parts of a send time value; all empty for "send now". */
export function splitSendAt(value: string): { date: string; hour: string; minute: string } {
  if (!value) return { date: '', hour: '', minute: '' }
  return { date: value.slice(0, 10), hour: value.slice(11, 13), minute: value.slice(14, 16) }
}

/** A send time value from its parts; no date means "send now" (''). */
export function joinSendAt(date: string, hour: string, minute: string): string {
  if (!date) return ''
  return `${date}T${hour || DEFAULT_TIME.hour}:${minute || DEFAULT_TIME.minute}`
}
