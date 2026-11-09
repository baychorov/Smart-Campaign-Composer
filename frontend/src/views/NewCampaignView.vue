<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../api/client'
import { toApiError } from '../composables/useCampaign'

const router = useRouter()

const EXAMPLES = [
  {
    label: 'Sepeti terk edenler',
    brief: "Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul'daki kullanıcılara %30 indirim hatırlatması gönder. Samimi ama kısa olsun.",
  },
  { label: 'İki şehir, iki yaş grubu', brief: "İstanbul'daki 25 yaş altı ve Ankara'daki 40 yaş üstü kullanıcılara gönder." },
  { label: 'Geri kazanım', brief: 'Uzun süredir alışveriş yapmayan sadık müşterilerimizi geri kazanalım.' },
  { label: 'Yeni yıl', brief: 'Tüm kullanıcılarımıza yeni yıl kutlaması gönder.' },
]

// "Rewrite the brief" from a campaign page passes the old brief along.
const passed = (history.state as { brief?: unknown } | null)?.brief
const brief = ref(typeof passed === 'string' ? passed : EXAMPLES[0]!.brief)
const submitting = ref(false)
const error = ref<string | null>(null)

async function submit() {
  if (!brief.value.trim() || submitting.value) return
  submitting.value = true
  error.value = null
  try {
    const out = await api.createCampaign(brief.value)
    // Warnings come only with this response; pass them to the next page.
    await router.push({
      name: 'campaign',
      params: { id: out.campaign.id },
      state: { warnings: JSON.parse(JSON.stringify(out.warnings)) },
    })
  } catch (e) {
    error.value = toApiError(e).message
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <main class="container">
    <form class="brief-form card" @submit.prevent="submit">
      <div class="intro">
        <h1>Yeni kampanya</h1>
        <p class="muted">Oluşturmak istediğiniz kampanyayı açıklayın. Göndermeden önce düzenleyebilirsiniz.</p>
      </div>

      <!-- No visible label: the heading says what goes here; screen readers get aria-label. -->
      <textarea id="brief" v-model="brief" rows="5" aria-label="Kampanya açıklaması" :disabled="submitting" maxlength="2000"></textarea>

      <div class="examples" aria-label="Örnek brief'ler">
        <span class="muted">Örnekler:</span>
        <button v-for="ex in EXAMPLES" :key="ex.label" type="button" class="chip" :disabled="submitting" @click="brief = ex.brief">
          {{ ex.label }}
        </button>
      </div>

      <p v-if="error" class="note note-bad" role="alert">{{ error }}</p>

      <div class="actions">
        <button type="submit" class="btn btn-primary btn-lg" :disabled="submitting || !brief.trim()">Kitleyi çıkar</button>
        <span v-if="submitting" class="muted" data-test="preparing"><span class="spinner"></span> Hazırlanıyor…</span>
      </div>
    </form>
  </main>
</template>

<style scoped>
.brief-form {
  max-width: 47.5rem;
  margin: 3rem auto;
  padding: 1.75rem 2rem;
  display: flex;
  flex-direction: column;
  gap: 0.875rem;
}
.intro h1 {
  margin: 0 0 0.375rem;
  font-size: 1.5rem;
  font-weight: 600;
  letter-spacing: -0.015em;
}
.intro p {
  margin: 0;
}
textarea {
  width: 100%;
  border: 1.5px solid var(--line-input);
  border-radius: var(--radius-md);
  padding: 0.75rem 0.875rem;
  background: var(--surface);
  font-size: 0.9375rem;
  resize: vertical;
}
textarea:focus {
  border-color: var(--accent);
  outline: none;
  box-shadow: 0 0 0 0.1875rem var(--accent-soft);
}
.examples {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.375rem;
  font-size: var(--text-sm);
}
.chip {
  border: 1px solid var(--line);
  background: var(--surface-muted);
  border-radius: 62.4375rem;
  padding: 0.1875rem 0.75rem;
  font-size: var(--text-sm);
  color: var(--ink-soft);
}
.chip:hover:not(:disabled) {
  border-color: var(--line-strong);
  background: var(--accent-soft);
}
.actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 0.75rem;
}
@media (max-width: 560px) {
  .brief-form {
    margin: 1.25rem auto;
    padding: 1.25rem;
  }
}
</style>
