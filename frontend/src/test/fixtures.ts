import type { Campaign, Rule } from '../api/types'

export const caseRule = (): Rule => ({
  status: 'ok',
  audience_scope: 'filtered',
  common: [
    { type: 'attribute', field: 'city', op: 'in', values: ['İstanbul'] },
    { type: 'event', event: 'add_to_cart', occurred: true, within_days: 30 },
    { type: 'event', event: 'purchase', occurred: false, within_days: 30 },
  ],
  groups: [],
  assumptions: [],
  unsupported: [],
  notes: [],
})

export const groupRule = (): Rule => ({
  status: 'partial',
  audience_scope: 'filtered',
  common: [{ type: 'event', event: 'add_to_cart', occurred: true, within_days: 30 }],
  groups: [
    { conditions: [{ type: 'attribute', field: 'city', op: 'in', values: ['İstanbul'] }, { type: 'attribute', field: 'age', op: 'lte', value: 24 }] },
    { conditions: [{ type: 'attribute', field: 'city', op: 'in', values: ['Ankara'] }, { type: 'attribute', field: 'age', op: 'gte', value: 40 }] },
    { conditions: [{ type: 'attribute', field: 'city', op: 'in', values: ['İzmir', 'Bursa'] }] },
  ],
  assumptions: [],
  unsupported: [
    { text: 'sadık', reason: 'unknown_field', group: 2 },
    { text: 'en az 3 kez', reason: 'aggregate', group: 1 },
    { text: 'yarın sabah', reason: 'scheduling', group: null },
  ],
  notes: [],
})

export function makeCampaign(over: Partial<Campaign> = {}): Campaign {
  return {
    id: 7,
    brief: "Son 30 günde sepete ekleyip alışveriş yapmayan İstanbul'daki kullanıcılar",
    status: 'draft',
    rule: caseRule(),
    summary: { all: false, common: ['şehri İstanbul olan', 'son 30 günde sepete ürün eklemiş', 'son 30 günde alışveriş yapmamış'], groups: null },
    approvable: true,
    message: { sms: '', email_subject: '', email_body: '' },
    channels: { sms: true, email: true },
    partial_ack_at: null,
    approved_at: null,
    send_at: null,
    created_at: '2026-11-07T10:00:00Z',
    updated_at: '2026-11-07T10:00:00Z',
    audience: { snapshot: false, reachable: 156, sms: 111, email: 118 },
    content_validation: null,
    ...over,
  }
}

export const goodMessage = {
  sms: 'Merhaba {{first_name}}, sepetin seni bekliyor!',
  email_subject: '{{first_name}}, sepetin hazır',
  email_body: 'Merhaba {{first_name}},\n\nSepetin seni bekliyor.',
}

export const validValidation = {
  valid: true,
  errors: [],
  stats: {
    sms: { runes: 45, worst_case_runes: 62, limit: 160 },
    email_subject: { runes: 28, worst_case_runes: 45, limit: 80 },
    email_body: { runes: 47, worst_case_runes: 64, limit: 2000 },
  },
}
