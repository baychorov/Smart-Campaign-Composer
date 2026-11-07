import type { EventType, Reason } from '../api/types'

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
