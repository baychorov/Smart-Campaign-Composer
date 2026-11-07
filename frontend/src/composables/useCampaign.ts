import { ref } from 'vue'
import { api, ApiError } from '../api/client'
import type { Campaign, Issue, Rule } from '../api/types'

// The campaign being edited. Every change goes to the API and the answer
// replaces the local copy: the server's normalised rule, live counts and
// content validation are what the page shows.

export function useCampaign(id: number) {
  const campaign = ref<Campaign | null>(null)
  const warnings = ref<Issue[]>([])
  /** Error of the last load or rule edit, with its issues. */
  const error = ref<ApiError | null>(null)
  const loading = ref(false)
  const savingRule = ref(false)

  async function load() {
    loading.value = true
    error.value = null
    try {
      campaign.value = (await api.getCampaign(id)).campaign
    } catch (e) {
      error.value = toApiError(e)
    } finally {
      loading.value = false
    }
  }

  async function updateRule(rule: Rule) {
    savingRule.value = true
    error.value = null
    try {
      const out = await api.updateRule(id, rule)
      campaign.value = out.campaign
      warnings.value = out.warnings
    } catch (e) {
      // The stored rule is unchanged; the page keeps showing it.
      error.value = toApiError(e)
    } finally {
      savingRule.value = false
    }
  }

  return { campaign, warnings, error, loading, savingRule, load, updateRule }
}

export function toApiError(e: unknown): ApiError {
  return e instanceof ApiError ? e : new ApiError(0, 'unknown', 'Beklenmeyen bir hata oluştu.')
}
