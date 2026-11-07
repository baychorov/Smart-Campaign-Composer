<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import type { ApiError } from '../api/client'
import type { Campaign, Issue, Rule } from '../api/types'
import { formatCount, REASON_LABELS } from '../lib/labels'
import { isLastCondition, removeCondition, removeValue, setNumber, type CondPath } from '../lib/rule'
import ConditionRow from './ConditionRow.vue'

const props = defineProps<{
  campaign: Campaign
  warnings: Issue[]
  error: ApiError | null
  saving: boolean
  /** Editing is paused, e.g. while messages are generated for this rule. */
  locked: boolean
}>()

const emit = defineEmits<{ updateRule: [rule: Rule] }>()
const router = useRouter()

const rule = computed(() => props.campaign.rule)
const editable = computed(() => props.campaign.status === 'draft' && !props.saving && !props.locked)
const canRemove = computed(() => !isLastCondition(rule.value))
const audience = computed(() => props.campaign.audience)
const blocked = computed(() => !props.campaign.approvable)

const summaryText = computed(() => {
  const s = props.campaign.summary
  if (s.all) return 'Kanal izni olan tüm kullanıcılar.'
  const parts: string[] = []
  if (s.common?.length) parts.push(s.common.join(', '))
  if (s.groups?.length) parts.push(s.groups.map((g) => `(${g.join(', ')})`).join(' veya '))
  return `Kanal izni olan, ${parts.join('; ayrıca ')} kullanıcılar.`
})

const onRemove = (p: CondPath) => emit('updateRule', removeCondition(rule.value, p))
const onRemoveValue = (p: CondPath, i: number) => emit('updateRule', removeValue(rule.value, p, i))
const onSetNumber = (p: CondPath, n: number) => emit('updateRule', setNumber(rule.value, p, n))

function rewriteBrief() {
  void router.push({ name: 'new', state: { brief: props.campaign.brief } })
}
</script>

<template>
  <section class="card" aria-labelledby="aud-title">
    <div class="card-head">
      <h2 id="aud-title" class="card-title">Hedef kitle</h2>
      <span v-if="saving" class="muted"><span class="spinner"></span> güncelleniyor</span>
      <span class="spacer"></span>
      <template v-if="audience">
        <span class="big num" data-test="reachable">{{ formatCount(audience.reachable) }}</span>
        <span class="muted">kişi</span>
      </template>
      <span v-else class="muted">—</span>
    </div>

    <div class="card-body">
      <template v-if="blocked">
        <div class="note note-bad" role="alert">
          <span class="note-title">Bu brief'ten gönderilebilir bir kitle çıkmadı</span>
          <ul v-if="rule.unsupported.length">
            <li v-for="(u, i) in rule.unsupported" :key="i">
              "{{ u.text }}" · {{ REASON_LABELS[u.reason] ?? u.reason }}
            </li>
          </ul>
          <p class="note-text">Brief'i bir kampanya isteği olarak yeniden yaz. Bu taslak için mesaj üretilmez ve onay kapalı kalır.</p>
        </div>
        <div><button type="button" class="btn" @click="rewriteBrief">Brief'i yeniden yaz</button></div>
      </template>

      <template v-else>
        <p class="summary">{{ summaryText }}</p>

        <div v-if="audience" class="channel-counts">
          <span><i class="swatch" style="background: var(--sms)"></i>SMS izni olan <b class="num">{{ formatCount(audience.sms) }}</b></span>
          <span><i class="swatch" style="background: var(--email)"></i>Email izni olan <b class="num">{{ formatCount(audience.email) }}</b></span>
        </div>

        <div class="consent">
          <svg width="14" height="14" viewBox="0 0 16 16" aria-hidden="true">
            <rect x="3" y="7" width="10" height="7" rx="1.5" fill="none" stroke="currentColor" stroke-width="1.5" />
            <path d="M5.5 7V5a2.5 2.5 0 0 1 5 0v2" fill="none" stroke="currentColor" stroke-width="1.5" />
          </svg>
          Kanal izni filtresi her zaman uygulanır ve kuraldan kaldırılamaz.
        </div>

        <div v-if="error" class="note note-bad" role="alert">
          <span class="note-title">{{ error.message }} Kural değiştirilmedi.</span>
          <ul v-if="error.issues.length">
            <li v-for="(iss, i) in error.issues" :key="i">{{ iss.message }}</li>
          </ul>
        </div>

        <div v-if="warnings.length" class="note note-info">
          <span class="note-title">Otomatik düzeltmeler</span>
          <ul>
            <li v-for="(w, i) in warnings" :key="i">{{ w.message }}</li>
          </ul>
        </div>

        <div v-if="campaign.summary.all" class="note note-plain">Kural koşul içermiyor: izni olan herkes hedeflenir.</div>

        <div v-if="rule.common.length" class="box" data-test="common">
          <div class="box-head">{{ rule.groups.length ? 'Herkeste ortak' : 'Koşullar' }} · hepsi sağlanmalı</div>
          <div class="conds">
            <ConditionRow
              v-for="(c, i) in rule.common"
              :key="`c${i}`"
              :condition="c"
              :input-id="`cond-c-${i}`"
              :editable="editable"
              :can-remove="canRemove"
              @remove="onRemove({ group: null, index: i })"
              @remove-value="(v) => onRemoveValue({ group: null, index: i }, v)"
              @set-number="(n) => onSetNumber({ group: null, index: i }, n)"
            />
          </div>
        </div>

        <template v-if="rule.groups.length">
          <div v-if="rule.common.length" class="joiner">VE şu gruplardan en az biri</div>
          <template v-for="(g, gi) in rule.groups" :key="`g${gi}`">
            <div v-if="gi > 0" class="or" aria-hidden="true">VEYA</div>
            <div class="box" data-test="group">
              <div class="box-head">
                <span>Grup {{ gi + 1 }}</span>
                <span v-if="audience?.groups"><b class="num">{{ formatCount(audience.groups[gi] ?? 0) }}</b> kişi</span>
              </div>
              <div class="conds">
                <ConditionRow
                  v-for="(c, i) in g.conditions"
                  :key="`g${gi}c${i}`"
                  :condition="c"
                  :input-id="`cond-g${gi}-${i}`"
                  :editable="editable"
                  :can-remove="canRemove"
                  @remove="onRemove({ group: gi, index: i })"
                  @remove-value="(v) => onRemoveValue({ group: gi, index: i }, v)"
                  @set-number="(n) => onSetNumber({ group: gi, index: i }, n)"
                />
              </div>
            </div>
          </template>
        </template>

        <div v-if="rule.unsupported.length" class="note note-warn" data-test="unsupported">
          <span class="note-title">Uygulanmayan kriterler</span>
          <ul>
            <li v-for="(u, i) in rule.unsupported" :key="i">
              "{{ u.text }}" · {{ REASON_LABELS[u.reason] ?? u.reason }}
              <span v-if="u.group !== null" class="muted">(grup {{ u.group + 1 }})</span>
            </li>
          </ul>
          <p class="note-text">
            Bu kriterler uygulanmadığı için kitle hedeflenenden geniş olabilir. Onaylamadan önce bunu alttaki kutuyla kabul
            etmen gerekir.
          </p>
        </div>

        <div v-if="rule.assumptions.length" class="note note-info">
          <span class="note-title">Varsayımlar</span>
          <ul>
            <li v-for="(a, i) in rule.assumptions" :key="i">{{ a }}</li>
          </ul>
        </div>

        <div v-if="rule.notes.length" class="note note-plain">
          <span class="note-title">Notlar</span>
          <ul>
            <li v-for="(n, i) in rule.notes" :key="i">{{ n }}</li>
          </ul>
        </div>
      </template>
    </div>
  </section>
</template>

<style scoped>
.spacer {
  flex: 1;
}
.big {
  font-size: var(--text-xl);
  font-weight: 600;
  color: var(--ink);
}
.summary {
  margin: 0;
  background: var(--surface-muted);
  border-radius: var(--radius-md);
  padding: 10px 14px;
  color: var(--ink-soft);
}
.channel-counts {
  display: flex;
  flex-wrap: wrap;
  gap: 18px;
  font-size: var(--text-sm);
  color: var(--muted);
}
.channel-counts b {
  color: var(--ink);
  font-weight: 600;
}
.consent {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: var(--text-sm);
  color: var(--muted);
  border: 1px dashed var(--line-strong);
  border-radius: var(--radius-md);
  padding: 7px 12px;
}
.box {
  border: 1px solid var(--line);
  border-radius: var(--radius-md);
  overflow: hidden;
}
.box-head {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  padding: 7px 12px;
  font-size: var(--text-xs);
  color: var(--muted);
  background: var(--surface-muted);
  border-bottom: 1px solid var(--line);
}
.box-head b {
  color: var(--ink);
}
.conds {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 8px;
}
.joiner {
  font-size: var(--text-xs);
  color: var(--muted);
  text-align: center;
  letter-spacing: 0.04em;
}
.or {
  display: flex;
  align-items: center;
  gap: 10px;
  font-family: var(--font-mono);
  font-size: 11.5px;
  letter-spacing: 0.1em;
  color: var(--muted);
}
.or::before,
.or::after {
  content: '';
  flex: 1;
  height: 1px;
  background: var(--line);
}
.note-text {
  margin: 6px 0 0;
}
</style>
