import type { EventType, Reason, Summary } from '../api/types'

/** Short labels for event condition chips: [did, did not]. */
export const EVENT_LABELS: Record<EventType, [string, string]> = {
  signup: ['Kayıt oldu', 'Kayıt olmadı'],
  product_view: ['Ürün inceledi', 'Ürün incelemedi'],
  add_to_cart: ['Sepete ekledi', 'Sepete eklemedi'],
  purchase: ['Alışveriş yaptı', 'Alışveriş yapmadı'],
}

/** Marketer-facing names of the reasons a criterion was not applied. */
export const REASON_LABELS: Record<Reason, string> = {
  sequence: 'Sıralı olay',
  aggregate: 'Sayı veya toplam koşulu',
  ranking: 'Sıralama',
  unknown_field: 'Veride karşılığı yok',
  absolute_date: 'Kesin tarih',
  scheduling: 'Zamanlama',
  contradiction: 'Çelişki',
  too_complex: 'Çok karmaşık',
  off_topic: 'Kampanya isteği değil',
}

export const FIELD_LABELS = {
  sms: 'SMS',
  email_subject: 'Email konusu',
  email_body: 'Email gövdesi',
} as const

export const STATUS_LABELS = {
  draft: 'Taslak',
  approved: 'Onaylandı',
  sending: 'Gönderiliyor',
  completed: 'Tamamlandı',
} as const

export function formatCount(n: number): string {
  return n.toLocaleString('tr-TR')
}

export function formatDate(iso: string): string {
  return new Date(iso).toLocaleString('tr-TR', { dateStyle: 'medium', timeStyle: 'short' })
}

/** The rule summary as one sentence, e.g. on the audience card. */
export function audienceSentence(s: Summary): string {
  if (s.all) return 'Kanal izni olan tüm kullanıcılar.'
  const parts: string[] = []
  if (s.common?.length) parts.push(s.common.join(', '))
  if (s.groups?.length) parts.push(s.groups.map((g) => `(${g.join(', ')})`).join(' veya '))
  return `Kanal izni olan, ${parts.join('; ayrıca ')} kullanıcılar.`
}

/** A calendar day "2026-10-01" as "1 Ekim 2026". The day is not shifted by the viewer's time zone. */
export function formatDay(day: string): string {
  return new Date(day + 'T00:00:00Z').toLocaleDateString('tr-TR', { day: 'numeric', month: 'long', year: 'numeric', timeZone: 'UTC' })
}
