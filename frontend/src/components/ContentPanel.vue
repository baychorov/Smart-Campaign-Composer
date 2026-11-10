<script setup lang="ts">
import { computed } from 'vue'
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

// The preview fills {{first_name}} with one sample customer; the editor
// above shows it raw. {{city}} is no longer a variable (D-CONTENT): in an
// older draft it stays as written, marked like any other variable.
const SAMPLE = { first_name: 'Ayşe' }
const preview = (t: string) => splitVariables(renderPreview(t, SAMPLE))
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
            <div v-if="!running" class="preview sms-preview" data-test="sms-preview">
              <div class="bubble">
                <template v-for="(p, i) in preview(text.sms)" :key="i"><span :class="{ 'unknown-var': p.variable }">{{ p.text }}</span></template>
              </div>
              <p class="sms-time">Şimdi</p>
            </div>
          </div>

          <div class="msg">
            <div class="msg-head"><i class="swatch" style="background: var(--email)"></i>Email</div>
            <MessageFieldView
              v-for="f in (['email_subject', 'email_body'] as const)"
              :key="f"
              :field="f"
              :label="f === 'email_subject' ? 'Konu' : 'Gövde'"
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
                <div class="mail-subject">{{ renderPreview(text.email_subject, SAMPLE) }}</div>
                <div class="mail-body">{{ renderPreview(text.email_body, SAMPLE) }}</div>
              </div>
            </div>
          </div>

          <div v-if="!running && (dirty || validation?.valid === false || editor.error.value)" class="footer">
            <div v-if="dirty || validation?.valid === false" class="save-row">
              <template v-if="dirty">
                <span class="warn-text">Kaydedilmemiş değişiklik var</span>
                <button type="button" class="btn btn-primary" :disabled="editor.saving.value" @click="editor.save()">
                  <span v-if="editor.saving.value" class="spinner"></span>Kaydet ve doğrula
                </button>
              </template>
              <span v-else class="bad-text">Mesajda düzeltilmesi gereken hata var; ilgili alanın altındaki açıklamaya bak.</span>
            </div>
            <p v-if="editor.error.value" class="bad-text" role="alert">{{ editor.error.value }}</p>
          </div>
        </template>
      </template>
    </div>
  </section>
</template>

<style scoped>
/* The SMS and email boxes need clear air between them. */
.card-body {
  gap: 1.75rem;
}
.spacer {
  flex: 1;
}
.empty {
  margin: 0;
  color: var(--muted);
  text-align: center;
  padding: 1.5rem 0.5rem;
}
.msg {
  border: 1px solid var(--line);
  border-radius: var(--radius-md);
  overflow: hidden;
}
.msg-head {
  display: flex;
  align-items: center;
  padding: 0.625rem 1rem;
  background: var(--surface-muted);
  border-bottom: 1px solid var(--line);
  font-size: var(--text-md);
  font-weight: 700;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--ink);
}
.msg-head .swatch {
  width: 0.625rem;
  height: 0.625rem;
  margin-right: 0.5rem;
}
.preview {
  padding: 0.75rem 0.875rem;
  border-top: 1px dashed var(--line);
  background: color-mix(in srgb, var(--surface-muted) 50%, var(--surface));
}
/* The SMS preview looks like a received message on a phone: a grey
   bubble with a tail at the bottom left. The tail is two pseudo-elements;
   the second one is painted in the preview's background colour. */
.sms-preview {
  background: var(--surface);
  padding: 1rem 1.25rem 1rem 1.5rem;
}
.bubble {
  position: relative;
  width: fit-content;
  max-width: 80%;
  background: var(--bubble);
  color: var(--ink);
  border-radius: 1.125rem;
  padding: 0.625rem 0.875rem;
  font-size: var(--text-md);
  line-height: 1.45;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.bubble::before,
.bubble::after {
  content: '';
  position: absolute;
  bottom: 0;
  height: 1.25rem;
}
.bubble::before {
  left: -0.4375rem;
  width: 1.25rem;
  background: var(--bubble);
  border-bottom-right-radius: 1rem 0.875rem;
}
.bubble::after {
  left: -0.625rem;
  width: 0.625rem;
  background: var(--surface);
  border-bottom-right-radius: 0.625rem;
}
.sms-time {
  margin: 0.375rem 0 0 0.25rem;
  font-size: var(--text-xs);
  color: var(--muted);
}
.unknown-var {
  background: var(--bad-soft);
  color: var(--bad);
  border-radius: 0.1875rem;
  padding: 0 0.125rem;
}
.mail {
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: var(--radius-sm);
  padding: 0.75rem 0.875rem;
  font-size: var(--text-sm);
}
.mail-subject {
  font-weight: 600;
  padding-bottom: 0.5rem;
  margin-bottom: 0.5rem;
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
  gap: 0.625rem;
}
.save-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 0.625rem;
  font-size: var(--text-sm);
}
.warn-text {
  color: var(--warn);
}
.bad-text {
  color: var(--bad);
  margin: 0;
  font-size: var(--text-sm);
}
</style>
