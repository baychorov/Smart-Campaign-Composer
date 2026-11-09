<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
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
import { fromTurkeyInput, toTurkeyInput } from '../lib/schedule'

// One campaign. A draft shows the workspace (audience | messages + approval
// bar); after approval the same URL shows the live dashboard.

const props = defineProps<{ id: number }>()
const router = useRouter()

// "← Kampanyalar": coming from the list, go back in history so the list
// shows the same filter, open row and scroll position (it is kept alive in
// App.vue). Coming from the brief form or a direct link, open the list.
function backToList() {
  const back = router.options.history.state.back // the previous page's path, kept by vue-router
  if (typeof back === 'string' && /^\/campaigns(\?|$)/.test(back)) router.back()
  else void router.push({ name: 'campaigns' })
}

const { campaign, warnings, error, loading, savingRule, load, updateRule } = useCampaign(props.id)
const editor = useContentEditor(campaign, (c) => (campaign.value = c))

const channels = ref({ sms: true, email: true })
const partialAck = ref(false)
/** datetime-local value in Turkey time; filled from the brief's suggestion. */
const sendAt = ref('')
// The time limits move with the clock; a minute is precise enough.
const now = ref(new Date())
const clock = setInterval(() => (now.value = new Date()), 60_000)
onUnmounted(() => clearInterval(clock))
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
  if (c.send_at) sendAt.value = toTurkeyInput(c.send_at)
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
        sendAt: sendAt.value,
        now: now.value,
      })
    : [],
)

async function approve() {
  if (!campaign.value) return
  approving.value = true
  approveIssues.value = []
  approveError.value = null
  try {
    const out = await api.approve(campaign.value.id, channels.value, partialAck.value, sendAt.value ? fromTurkeyInput(sendAt.value) : null)
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
      <a href="/campaigns" class="back" data-test="back" @click.prevent="backToList">
        <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M10 3.5 5.5 8l4.5 4.5" /></svg>
        Kampanyalar
      </a>

      <header class="title-row">
        <div class="title-main">
          <div class="eyebrow">
            <span class="campaign-no">Kampanya {{ campaign.id }}</span>
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
          v-model:send-at="sendAt"
          :sms-count="campaign.audience?.sms ?? 0"
          :email-count="campaign.audience?.email ?? 0"
          :partial="campaign.rule.status === 'partial'"
          :reasons="reasons"
          :busy="approving"
          @approve="approve"
        />
      </template>

      <DashboardPanel v-else :campaign="campaign" @update="(c) => (campaign = c)" @reload="load" />
    </template>
  </main>
</template>

<style scoped>
.page {
  padding-top: 1.5rem;
  padding-bottom: 2.5rem;
}
.page.has-bar {
  padding-bottom: 8.75rem;
}
.loading {
  padding: 2.5rem 0;
}
.back {
  display: inline-flex;
  align-items: center;
  gap: 0.25rem;
  margin: 0 0 0.75rem -0.5rem;
  padding: 0.25rem 0.625rem 0.25rem 0.375rem;
  border-radius: var(--radius-sm);
  font-size: var(--text-sm);
  font-weight: 500;
  color: var(--muted);
  text-decoration: none;
  transition: background 0.15s, color 0.15s;
}
.back:hover {
  background: var(--surface-muted);
  color: var(--accent);
}
.back svg {
  width: 1rem;
  height: 1rem;
  fill: none;
  stroke: currentColor;
  stroke-width: 1.75;
  stroke-linecap: round;
  stroke-linejoin: round;
}
.title-row {
  margin-bottom: 1.75rem;
}
.eyebrow {
  display: flex;
  align-items: center;
  gap: 1rem;
  font-size: var(--text-sm);
  color: var(--muted);
}
.status {
  font-size: var(--text-xs);
  font-weight: 500;
  border-radius: 62.4375rem;
  padding: 0.125rem 0.625rem;
  background: var(--surface-muted);
  border: 1px solid var(--line);
  color: var(--ink-soft);
}
.status.sending,
.status.approved,
.status.scheduled {
  background: var(--info-soft);
  border-color: transparent;
  color: var(--info);
}
.status.completed {
  background: var(--ok-soft);
  border-color: transparent;
  color: var(--ok);
}
.campaign-no {
  font-weight: 600;
  color: var(--ink-soft);
}
.brief {
  margin: 0.625rem 0 0;
  font-size: var(--text-lg);
  color: var(--ink);
  max-width: 90ch;
}
.workspace {
  display: grid;
  grid-template-columns: minmax(0, 5fr) minmax(0, 6fr);
  gap: 1.5rem;
  align-items: start;
}
@media (max-width: 960px) {
  .workspace {
    grid-template-columns: minmax(0, 1fr);
  }
}
.approve-error {
  margin-bottom: 1rem;
}
</style>
