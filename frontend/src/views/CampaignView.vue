<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { api } from '../api/client'
import type { Issue } from '../api/types'
import ApprovalBar from '../components/ApprovalBar.vue'
import AudiencePanel from '../components/AudiencePanel.vue'
import ContentPanel from '../components/ContentPanel.vue'
import DashboardPanel from '../components/DashboardPanel.vue'
import { toApiError, useCampaign } from '../composables/useCampaign'
import { useContentEditor } from '../composables/useContentEditor'
import { blockingReasons } from '../lib/approval'
import { STATUS_LABELS } from '../lib/labels'

// One campaign. A draft shows the workspace (audience | messages + approval
// bar); after approval the same URL shows the live dashboard.

const props = defineProps<{ id: number }>()

const { campaign, warnings, error, loading, savingRule, load, updateRule } = useCampaign(props.id)
const editor = useContentEditor(campaign, (c) => (campaign.value = c))

const channels = ref({ sms: true, email: true })
const partialAck = ref(false)
const approving = ref(false)
const approveIssues = ref<Issue[]>([])
const approveError = ref<string | null>(null)

const isDraft = computed(() => campaign.value?.status === 'draft')

onMounted(async () => {
  const passed = (history.state as { warnings?: Issue[] } | null)?.warnings
  if (Array.isArray(passed)) warnings.value = passed
  await load()
  const c = campaign.value
  if (!c || c.status !== 'draft') return
  editor.init(c)
  // Decision (PLAN step 11): an "ok" rule starts generation by itself; a
  // partial rule waits until the marketer has read what was left out.
  const empty = !Object.values(c.message).some((v) => v.trim() !== '')
  if (c.approvable && c.rule.status === 'ok' && empty && editor.gen.value.phase === 'idle') void editor.generate()
})

// A changed rule may change the partial status; the acknowledgement is for
// the rule on screen only.
watch(() => campaign.value?.rule.status, () => (partialAck.value = false))

const reasons = computed(() =>
  campaign.value
    ? blockingReasons({
        campaign: campaign.value,
        channels: channels.value,
        partialAck: partialAck.value,
        genPhase: editor.gen.value.phase,
        dirty: editor.dirty.value,
        busy: savingRule.value || editor.saving.value,
      })
    : [],
)

async function approve() {
  if (!campaign.value) return
  approving.value = true
  approveIssues.value = []
  approveError.value = null
  try {
    const out = await api.approve(campaign.value.id, channels.value, partialAck.value)
    campaign.value = out.campaign
    window.scrollTo({ top: 0 })
  } catch (e) {
    const err = toApiError(e)
    approveError.value = err.message
    approveIssues.value = err.issues
    if (err.code === 'not_draft') await load() // approved elsewhere: show its dashboard
  } finally {
    approving.value = false
  }
}
</script>

<template>
  <main class="container page" :class="{ 'has-bar': isDraft }">
    <p v-if="loading && !campaign" class="muted loading"><span class="spinner"></span> Kampanya yükleniyor…</p>

    <div v-else-if="!campaign" class="note note-bad" role="alert">
      <span class="note-title">{{ error?.message ?? 'Kampanya yüklenemedi.' }}</span>
      <RouterLink to="/">Yeni kampanya oluştur</RouterLink>
    </div>

    <template v-else>
      <header class="title-row">
        <div class="title-main">
          <div class="eyebrow">
            <span>Kampanya #{{ campaign.id }}</span>
            <span class="status" :class="campaign.status" data-test="status">{{ STATUS_LABELS[campaign.status] }}</span>
          </div>
          <p class="brief">“{{ campaign.brief }}”</p>
        </div>
      </header>

      <template v-if="isDraft">
        <div v-if="approveError" class="note note-bad approve-error" role="alert">
          <span class="note-title">{{ approveError }}</span>
          <ul v-if="approveIssues.length">
            <li v-for="(iss, i) in approveIssues" :key="i">{{ iss.message }}</li>
          </ul>
        </div>

        <div class="workspace">
          <AudiencePanel
            :campaign="campaign"
            :warnings="warnings"
            :error="error"
            :saving="savingRule"
            :locked="editor.running.value"
            @update-rule="updateRule"
          />
          <ContentPanel :campaign="campaign" :editor="editor" />
        </div>

        <ApprovalBar
          v-model:channels="channels"
          v-model:partial-ack="partialAck"
          :sms-count="campaign.audience?.sms ?? 0"
          :email-count="campaign.audience?.email ?? 0"
          :partial="campaign.rule.status === 'partial'"
          :reasons="reasons"
          :busy="approving"
          @approve="approve"
        />
      </template>

      <DashboardPanel v-else :campaign="campaign" />
    </template>
  </main>
</template>

<style scoped>
.page {
  padding-top: 24px;
  padding-bottom: 40px;
}
.page.has-bar {
  padding-bottom: 140px;
}
.loading {
  padding: 40px 0;
}
.title-row {
  margin-bottom: 18px;
}
.eyebrow {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: var(--text-sm);
  color: var(--muted);
}
.status {
  font-size: var(--text-xs);
  font-weight: 500;
  border-radius: 999px;
  padding: 2px 10px;
  background: var(--surface-muted);
  border: 1px solid var(--line);
  color: var(--ink-soft);
}
.status.sending,
.status.approved {
  background: var(--info-soft);
  border-color: transparent;
  color: var(--info);
}
.status.completed {
  background: var(--ok-soft);
  border-color: transparent;
  color: var(--ok);
}
.brief {
  margin: 6px 0 0;
  font-size: var(--text-lg);
  color: var(--ink);
  max-width: 90ch;
}
.workspace {
  display: grid;
  grid-template-columns: minmax(0, 5fr) minmax(0, 6fr);
  gap: 18px;
  align-items: start;
}
@media (max-width: 960px) {
  .workspace {
    grid-template-columns: minmax(0, 1fr);
  }
}
.approve-error {
  margin-bottom: 16px;
}
</style>
