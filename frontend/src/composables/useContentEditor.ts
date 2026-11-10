import { computed, ref, watch, type Ref } from 'vue'
import { api } from '../api/client'
import type { Campaign, Message, MessageField } from '../api/types'
import { applyEvent, emptyMessage, GENERATION_EVENTS, initialGen, isRunning, TERMINAL_EVENTS } from '../lib/generation'
import { useEventStream, type EventSourceFactory } from './useEventStream'
import { toApiError } from './useCampaign'

// Message generation and editing for a draft.
//
// Generation is not saved by the backend; when the stream ends with
// "validated" the message is stored with PUT /content (the same check runs
// again there and on approval). The generation id is kept in sessionStorage
// so a page reload follows the running generation instead of starting a new,
// paid one.

const storageKey = (campaignId: number) => `scc.generation.${campaignId}`

function readStored(campaignId: number): string | null {
  try {
    return sessionStorage.getItem(storageKey(campaignId))
  } catch {
    return null
  }
}

function writeStored(campaignId: number, genId: string | null) {
  try {
    if (genId) sessionStorage.setItem(storageKey(campaignId), genId)
    else sessionStorage.removeItem(storageKey(campaignId))
  } catch {
    // Storage may be blocked; resuming after reload is a convenience.
  }
}

export function useContentEditor(
  campaign: Ref<Campaign | null>,
  setCampaign: (c: Campaign) => void,
  createSource?: EventSourceFactory,
) {
  const gen = ref(initialGen())
  /** The editor's text; equals the stored message unless dirty. */
  const draft = ref<Message>(emptyMessage())
  const dirty = ref(false)
  const saving = ref(false)
  const error = ref<string | null>(null)
  let campaignId = 0

  const stream = useEventStream({
    events: GENERATION_EVENTS,
    terminal: TERMINAL_EVENTS,
    onEvent: (name, data) => {
      gen.value = applyEvent(gen.value, name, data)
      draft.value = { ...gen.value.text }
      if (TERMINAL_EVENTS.includes(name)) writeStored(campaignId, null)
      if (name === 'validated') void save()
      // An invalid candidate stays in the editor for the marketer to fix.
      if (name === 'invalid') dirty.value = true
      // A failed generation leaves nothing new: the editor shows the stored
      // message again, the one an approval would send.
      if (name === 'failed') showStored()
    },
    createSource,
  })

  const running = computed(() => isRunning(gen.value.phase))

  // The stream was refused (e.g. the API restarted and no longer knows the
  // generation, or it expired): it ends as a failure instead of waiting
  // forever, and the stored id is dropped so a reload does not retry it.
  watch(stream.state, (s) => {
    if (s !== 'error' || !running.value) return
    writeStored(campaignId, null)
    gen.value = {
      ...gen.value,
      phase: 'failed',
      current: null,
      failure: { code: 'stream', message: 'Üretimin bağlantısı kesildi.' },
    }
    showStored()
  })

  function showStored() {
    draft.value = { ...(campaign.value?.message ?? emptyMessage()) }
    dirty.value = false
  }

  /** Loads the stored message, or resumes a generation started before a reload. */
  function init(c: Campaign) {
    campaignId = c.id
    draft.value = { ...c.message }
    dirty.value = false
    const stored = readStored(c.id)
    if (stored) follow(stored)
  }

  function follow(genId: string) {
    writeStored(campaignId, genId)
    gen.value = { ...initialGen(), phase: 'preparing' }
    stream.connect(api.generationEventsUrl(genId))
  }

  async function generate() {
    if (!campaign.value || running.value) return
    error.value = null
    gen.value = { ...initialGen(), phase: 'preparing' }
    draft.value = emptyMessage()
    dirty.value = false
    try {
      const out = await api.startGeneration(campaign.value.id)
      follow(out.id)
    } catch (e) {
      gen.value = { ...gen.value, phase: 'failed', failure: { code: 'start', message: toApiError(e).message } }
    }
  }

  function edit(field: MessageField, value: string) {
    draft.value = { ...draft.value, [field]: value }
    dirty.value = true
  }

  async function save() {
    if (!campaign.value) return
    saving.value = true
    error.value = null
    try {
      const out = await api.saveContent(campaign.value.id, draft.value)
      setCampaign(out.campaign)
      // The backend trims fields; show what it stored.
      draft.value = { ...out.campaign.message }
      dirty.value = false
    } catch (e) {
      error.value = toApiError(e).message
    } finally {
      saving.value = false
    }
  }

  return { gen, draft, dirty, saving, error, running, init, generate, edit, save, stream }
}

export type ContentEditor = ReturnType<typeof useContentEditor>
