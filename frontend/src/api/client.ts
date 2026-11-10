import type { Campaign, CampaignPage, CampaignWithWarnings, ContentValidation, Issue, ListFilter, Message, Rule } from './types'

/** An error response of the API: {error, message, issues?}. */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly issues: Issue[]

  constructor(status: number, code: string, message: string, issues: Issue[] = []) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.issues = issues
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  let resp: Response
  try {
    resp = await fetch(path, {
      method,
      headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
  } catch {
    throw new ApiError(0, 'network', 'Sunucuya ulaşılamadı. Bağlantını kontrol edip tekrar dene.')
  }
  const data: unknown = await resp.json().catch(() => null)
  if (!resp.ok) {
    const e = (data ?? {}) as { error?: string; message?: string; issues?: Issue[] }
    throw new ApiError(resp.status, e.error ?? 'http_' + resp.status, e.message ?? `İstek başarısız oldu (HTTP ${resp.status}).`, e.issues ?? [])
  }
  return data as T
}

const campaignPath = (id: number) => `/api/campaigns/${id}`

export const api = {
  info: () => request<{ llm_provider: string }>('GET', '/api/info'),

  createCampaign: (brief: string) => request<CampaignWithWarnings>('POST', '/api/campaigns', { brief }),

  getCampaign: (id: number) => request<{ campaign: Campaign }>('GET', campaignPath(id)),

  listCampaigns: (filter: ListFilter, before: number | null) =>
    request<CampaignPage>('GET', `/api/campaigns?filter=${filter}` + (before ? `&before=${before}` : '')),

  /** Drafts only; an approved campaign answers 409 not_draft. */
  deleteCampaign: (id: number) => request<null>('DELETE', campaignPath(id)),

  updateRule: (id: number, rule: Rule) =>
    request<CampaignWithWarnings>('PUT', campaignPath(id) + '/rule', { rule }),

  saveContent: (id: number, message: Message) =>
    request<{ campaign: Campaign; validation: ContentValidation }>('PUT', campaignPath(id) + '/content', message),

  startGeneration: (id: number) =>
    request<{ id: string; events_url: string }>('POST', campaignPath(id) + '/content/generations'),

  /** sendAt: RFC 3339 time to schedule, or null to send now. */
  approve: (id: number, channels: { sms: boolean; email: boolean }, partialAck: boolean, sendAt: string | null = null) =>
    request<{ campaign: Campaign; snapshot: { sms: number; email: number } }>('POST', campaignPath(id) + '/approve', {
      channels,
      partial_ack: partialAck,
      send_at: sendAt,
    }),

  /** Scheduled campaigns only, before sending starts (409 otherwise). */
  cancel: (id: number) => request<{ campaign: Campaign }>('POST', campaignPath(id) + '/cancel'),

  /** Completed campaigns with failed messages only (409 otherwise). */
  retryFailed: (id: number) =>
    request<{ campaign: Campaign; requeued: number }>('POST', campaignPath(id) + '/retry-failed'),

  generationEventsUrl: (genId: string) => `/api/content/generations/${encodeURIComponent(genId)}/events`,
  campaignEventsUrl: (id: number) => campaignPath(id) + '/events',
}
