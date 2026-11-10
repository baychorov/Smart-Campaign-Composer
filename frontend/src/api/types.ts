// JSON shapes of the Go API (backend/internal/httpapi). Kept by hand and
// small; the backend is the source of truth and validates everything again.

export type RuleStatus = 'ok' | 'partial' | 'unsupported' | 'rejected'
export type AudienceScope = 'filtered' | 'all'
export type EventType = 'signup' | 'product_view' | 'add_to_cart' | 'purchase'
export type Reason =
  | 'sequence'
  | 'aggregate'
  | 'ranking'
  | 'unknown_field'
  | 'absolute_date'
  | 'scheduling'
  | 'contradiction'
  | 'too_complex'
  | 'off_topic'

/** One flat condition (D-SEG): attribute fields or event fields are set. */
export interface Condition {
  type: 'attribute' | 'event'
  field?: 'city' | 'age'
  op?: 'in' | 'not_in' | 'gte' | 'lte'
  values?: string[]
  value?: number
  event?: EventType
  occurred?: boolean
  /** The window is within_days, or the calendar days from..to (YYYY-MM-DD, both included). */
  within_days?: number
  from?: string
  to?: string
  /** "At least n times" in the window; only on a "did" condition. */
  min_count?: number
  /** "Did event, and after that did (not) do then.event". */
  then?: { event: EventType; occurred: boolean }
}

export interface Group {
  conditions: Condition[]
}

export interface Unsupported {
  text: string
  reason: Reason
  group: number | null
}

export interface Rule {
  status: RuleStatus
  audience_scope: AudienceScope
  common: Condition[]
  groups: Group[]
  assumptions: string[]
  unsupported: Unsupported[]
  notes: string[]
}

export interface Summary {
  all: boolean
  common: string[] | null
  groups: string[][] | null
}

export interface Issue {
  path: string
  code: string
  message: string
}

export interface Message {
  sms: string
  email_subject: string
  email_body: string
}

export type MessageField = keyof Message

export interface FieldStats {
  runes: number
  worst_case_runes: number
  limit: number
}

export interface ContentValidation {
  valid: boolean
  errors: Issue[]
  stats: Partial<Record<MessageField, FieldStats>>
}

export interface Audience {
  snapshot: boolean
  reachable: number
  sms: number
  email: number
  groups?: number[]
}

export type CampaignStatus = 'draft' | 'approved' | 'scheduled' | 'sending' | 'completed' | 'cancelled'

export interface Campaign {
  id: number
  brief: string
  status: CampaignStatus
  rule: Rule
  summary: Summary
  approvable: boolean
  message: Message
  channels: { sms: boolean; email: boolean }
  partial_ack_at: string | null
  approved_at: string | null
  /** Draft: the send time suggested from the brief. Approved: the scheduled time. Null: now. */
  send_at: string | null
  created_at: string
  updated_at: string
  audience: Audience | null
  content_validation: ContentValidation | null
}

/** Body of POST /api/campaigns and PUT /api/campaigns/{id}/rule. */
export interface CampaignWithWarnings {
  campaign: Campaign
  warnings: Issue[]
}

export interface ChannelProgress {
  total: number
  pending: number
  sending: number
  sent: number
  failed: number
  skipped: number
}

/** The "progress" event of GET /api/campaigns/{id}/events. */
export interface Progress {
  campaign_id: number
  status: CampaignStatus
  channels: Partial<Record<'sms' | 'email', ChannelProgress>>
  rate_per_second: number
  top_errors: { error: string; count: number }[]
  done: boolean
  at: string
}

export type ListFilter = 'all' | 'draft' | 'sent'

/** A collapsed row of the campaign list; GET /api/campaigns/{id} has the rest. */
export interface CampaignListItem {
  id: number
  status: CampaignStatus
  created_at: string
  approved_at: string | null
  send_at: string | null
  /** False for a draft whose brief gave no approvable audience ("Geçersiz"). */
  approvable: boolean
  /** Delivery counts of the channels chosen at approval; empty for a draft. */
  progress: Partial<Record<'sms' | 'email', ChannelProgress>>
}

/** Body of GET /api/campaigns. */
export interface CampaignPage {
  campaigns: CampaignListItem[]
  /** Pass as `before` for the next page; null on the last page. */
  next_before: number | null
}
