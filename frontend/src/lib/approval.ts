import type { Campaign, Issue } from '../api/types'
import type { GenPhase } from './generation'

// Why the approve button is disabled, in the marketer's words. This mirrors
// the backend's checks (campaign.precheck, contentIssues) so the button is
// never enabled for a request the backend would refuse; the backend still
// checks everything again on approval (D-PARTIAL, D-CHANNEL).

export interface ApprovalInput {
  campaign: Campaign
  channels: { sms: boolean; email: boolean }
  partialAck: boolean
  genPhase: GenPhase
  /** The editor has changes not yet saved with PUT /content. */
  dirty: boolean
  busy: boolean
}

/** Content errors that matter for the chosen channels. */
export function channelIssues(errors: Issue[], channels: { sms: boolean; email: boolean }): Issue[] {
  return errors.filter((e) => (e.path === 'sms' ? channels.sms : channels.email))
}

export function blockingReasons(i: ApprovalInput): string[] {
  const out: string[] = []
  const c = i.campaign
  if (!c.approvable) out.push('Kural onaylanabilir değil')
  else if (!c.audience || c.audience.reachable === 0) out.push('Kitle boş')

  if (i.genPhase === 'preparing' || i.genPhase === 'streaming') out.push('Mesajlar üretiliyor')
  else if (i.dirty) out.push('Değişiklikler kaydedilmedi')
  else if (!c.content_validation) out.push('Mesajlar henüz yok')
  else if (channelIssues(c.content_validation.errors, i.channels).length > 0) out.push('Mesajda düzeltilecek hata var')

  if (!i.channels.sms && !i.channels.email) out.push('En az bir kanal seç')
  if (c.rule.status === 'partial' && !i.partialAck) out.push('Uygulanmayan kriterleri onayla')
  if (i.busy) out.push('İşlem sürüyor')
  return out
}
