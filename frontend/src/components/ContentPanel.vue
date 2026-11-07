<script setup lang="ts">
import { computed, ref } from 'vue'
import type { Campaign, Issue, MessageField } from '../api/types'
import type { ContentEditor } from '../composables/useContentEditor'
import { renderPreview, splitVariables } from '../lib/text'
import MessageFieldView from './MessageField.vue'

const props = defineProps<{ campaign: Campaign; editor: ContentEditor }>()

const gen = computed(() => props.editor.gen.value)
const running = computed(() => props.editor.running.value)
const dirty = computed(() => props.editor.dirty.value)
const text = computed(() => (running.value ? gen.value.text : props.editor.draft.value))
const validation = computed(() => props.campaign.content_validation)
const hasMessage = computed(() => Object.values(text.value).some((v) => v.trim() !== ''))
const editable = computed(() => props.campaign.status === 'draft' && !running.value && !props.editor.saving.value)

const PHASE_LABELS = {
  idle: '',
  preparing: 'hazırlanıyor',
  streaming: 'Claude yazıyor',
  validated: 'doğrulandı',
  invalid: 'doğrulanamadı',
  failed: 'üretilemedi',
} as const

const status = computed(() => {
  if (running.value) return PHASE_LABELS[gen.value.phase]
  if (dirty.value) return 'kaydedilmemiş değişiklik'
  if (validation.value) return validation.value.valid ? 'doğrulandı' : 'hata var'
  return PHASE_LABELS[gen.value.phase]
})

function errorsFor(field: MessageField): Issue[] {
  if (dirty.value) return gen.value.phase === 'invalid' ? gen.value.issues.filter((i) => i.path === field) : []
  return validation.value?.errors.filter((e) => e.path === field) ?? []
}

const SAMPLES = {
  named: { label: 'Ayşe · İstanbul', values: { first_name: 'Ayşe', city: 'İstanbul' } },
  empty: { label: 'İsim boş', values: { first_name: '', city: 'İzmir' } },
}
const sample = ref<keyof typeof SAMPLES>('named')
const preview = (t: string) => splitVariables(renderPreview(t, SAMPLES[sample.value].values))
</script>

<template>
  <section class="card" aria-labelledby="content-title">
    <div class="card-head">
      <h2 id="content-title" class="card-title">Mesajlar</h2>
      <span class="muted" data-test="content-status">{{ status }}</span>
      <span class="spacer"></span>
      <button
        v-if="campaign.approvable"
        type="button"
        class="btn"
        :class="{ 'btn-primary': !hasMessage }"
        :disabled="running || editor.saving.value"
        @click="editor.generate()"
      >
        {{ hasMessage ? 'Yeniden üret' : 'Mesajları üret' }}
      </button>
    </div>

    <div class="card-body">
      <p v-if="!campaign.approvable" class="empty">Kural onaylanabilir olmadığı için mesaj üretilmez.</p>

      <template v-else>
        <div v-if="gen.phase === 'failed' && gen.failure" class="note note-bad" role="alert">
          <span class="note-title">Mesajlar üretilemedi</span>
          {{ gen.failure.message }} Tekrar denemek için "Yeniden üret"e bas.
        </div>
        <div v-if="gen.repairs > 0 && running" class="note note-info">
          İlk aday doğrulamadan geçmedi; Claude düzeltilmiş bir aday yazıyor ({{ gen.repairs }}. onarım).
        </div>

        <p v-if="!hasMessage && !running && gen.phase !== 'failed'" class="empty">
          <template v-if="campaign.rule.status === 'partial'">
            Kural kısmi. Soldaki uygulanmayan kriterleri ve varsayımları gözden geçir, sonra mesajları üret.
          </template>
          <template v-else>Henüz mesaj yok. "Mesajları üret" ile Claude'dan SMS ve email taslağı iste.</template>
        </p>

        <template v-if="hasMessage || running">
          <div v-if="gen.log.length" class="event-log mono" aria-label="Akış olayları">{{ gen.log.join(' → ') }}</div>

          <div class="msg">
            <div class="msg-head"><i class="swatch" style="background: var(--sms)"></i>SMS</div>
            <MessageFieldView
              field="sms"
              label="Metin"
              :value="text.sms"
              :streaming="running"
              :caret="gen.current === 'sms'"
              :editable="editable"
              :stats="validation?.stats.sms"
              :stats-stale="dirty"
              :errors="errorsFor('sms')"
              @update="(v) => editor.edit('sms', v)"
            />
            <div v-if="!running" class="preview">
              <div class="bubble">
                <template v-for="(p, i) in preview(text.sms)" :key="i"><span :class="{ 'unknown-var': p.variable }">{{ p.text }}</span></template>
              </div>
            </div>
          </div>

          <div class="msg">
            <div class="msg-head"><i class="swatch" style="background: var(--email)"></i>Email</div>
            <MessageFieldView
              v-for="f in (['email_subject', 'email_body'] as const)"
              :key="f"
              :field="f"
              :label="f === 'email_subject' ? 'Konu' : 'Gövde (düz metin)'"
              :value="text[f]"
              :streaming="running"
              :caret="gen.current === f"
              :editable="editable"
              :stats="validation?.stats[f]"
              :stats-stale="dirty"
              :errors="errorsFor(f)"
              @update="(v) => editor.edit(f, v)"
            />
            <div v-if="!running" class="preview">
              <div class="mail">
                <div class="mail-subject">{{ renderPreview(text.email_subject, SAMPLES[sample].values) }}</div>
                <div class="mail-body">{{ renderPreview(text.email_body, SAMPLES[sample].values) }}</div>
              </div>
            </div>
          </div>

          <div v-if="!running" class="footer">
            <div class="samples" role="group" aria-label="Önizleme örneği">
              <span class="muted">Önizleme:</span>
              <button
                v-for="(s, k) in SAMPLES"
                :key="k"
                type="button"
                class="seg"
                :aria-pressed="sample === k"
                @click="sample = k"
              >{{ s.label }}</button>
            </div>
            <div class="save-row">
              <template v-if="dirty">
                <span class="warn-text">Kaydedilmemiş değişiklik var</span>
                <button type="button" class="btn btn-primary" :disabled="editor.saving.value" @click="editor.save()">
                  <span v-if="editor.saving.value" class="spinner"></span>Kaydet ve doğrula
                </button>
              </template>
              <span v-else-if="validation?.valid" class="ok-text">
                Backend doğruladı: değişkenler kitledeki en uzun değerlerle doldurulduğunda da sınırların içinde.
              </span>
              <span v-else-if="validation" class="bad-text">Mesajda düzeltilmesi gereken hata var; ilgili alanın altındaki açıklamaya bak.</span>
            </div>
            <p v-if="editor.error.value" class="bad-text" role="alert">{{ editor.error.value }}</p>
            <p class="muted vars">
              Değişkenler: <span class="code">{{ '{' + '{first_name}' + '}' }}</span>
              <span class="code">{{ '{' + '{city}' + '}' }}</span>. İsim boşsa "Değerli Müşterimiz" yazılır.
            </p>
          </div>
        </template>
      </template>
    </div>
  </section>
</template>

<style scoped>
.spacer {
  flex: 1;
}
.empty {
  margin: 0;
  color: var(--muted);
  text-align: center;
  padding: 24px 8px;
}
.event-log {
  font-size: 11.5px;
  color: var(--muted);
  background: var(--surface-muted);
  border-radius: var(--radius-sm);
  padding: 5px 10px;
  overflow-x: auto;
  white-space: nowrap;
}
.msg {
  border: 1px solid var(--line);
  border-radius: var(--radius-md);
  overflow: hidden;
}
.msg-head {
  display: flex;
  align-items: center;
  padding: 8px 14px;
  background: var(--surface-muted);
  border-bottom: 1px solid var(--line);
  font-size: var(--text-xs);
  font-weight: 600;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--ink-soft);
}
.preview {
  padding: 12px 14px;
  border-top: 1px dashed var(--line);
  background: color-mix(in srgb, var(--surface-muted) 50%, var(--surface));
}
.bubble {
  max-width: 88%;
  background: var(--sms);
  color: var(--accent-ink);
  border-radius: 16px 16px 16px 4px;
  padding: 9px 13px;
  font-size: var(--text-sm);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.unknown-var {
  background: var(--bad-soft);
  color: var(--bad);
  border-radius: 3px;
  padding: 0 2px;
}
.mail {
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: var(--radius-sm);
  padding: 12px 14px;
  font-size: var(--text-sm);
}
.mail-subject {
  font-weight: 600;
  padding-bottom: 8px;
  margin-bottom: 8px;
  border-bottom: 1px solid var(--line);
  overflow-wrap: anywhere;
}
.mail-body {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  color: var(--ink-soft);
}
.footer {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.samples {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  font-size: var(--text-xs);
}
.seg {
  border: 1px solid var(--line);
  background: var(--surface);
  border-radius: 999px;
  padding: 2px 10px;
  font-size: var(--text-xs);
  color: var(--ink-soft);
}
.seg[aria-pressed='true'] {
  background: var(--accent-soft);
  border-color: color-mix(in srgb, var(--accent) 40%, transparent);
  color: var(--accent);
}
.save-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
  font-size: var(--text-sm);
}
.warn-text {
  color: var(--warn);
}
.ok-text {
  color: var(--ok);
}
.bad-text {
  color: var(--bad);
  margin: 0;
  font-size: var(--text-sm);
}
.vars {
  margin: 0;
  font-size: var(--text-xs);
}
</style>
