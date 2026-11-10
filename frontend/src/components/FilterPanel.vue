<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from 'vue'
import { EMPTY_FILTER, STATUS_CHOICES, type ListFilter, type StatusKey } from '../lib/listFilter'

// The campaign list's filter panel, opened from the right. The choices are
// a draft until "Uygula": closing the panel leaves the list as it was.

const props = defineProps<{ open: boolean; filter: ListFilter }>()
const emit = defineEmits<{ close: []; apply: [filter: ListFilter] }>()

const draft = ref<ListFilter>({ ...EMPTY_FILTER })
const idText = ref('')
const firstField = ref<HTMLInputElement | null>(null)

// Each opening starts from the filter in use.
watch(
  () => props.open,
  async (open) => {
    if (!open) return
    draft.value = { ...props.filter, statuses: [...props.filter.statuses] }
    idText.value = props.filter.id ? String(props.filter.id) : ''
    await nextTick()
    firstField.value?.focus()
  },
  { immediate: true },
)

// Esc closes the panel (a listener on window: focus may be anywhere in it).
const onKey = (e: KeyboardEvent) => e.key === 'Escape' && emit('close')
watch(
  () => props.open,
  (open) => (open ? window.addEventListener('keydown', onKey) : window.removeEventListener('keydown', onKey)),
  { immediate: true },
)
onUnmounted(() => window.removeEventListener('keydown', onKey))

function typeId(el: HTMLInputElement) {
  idText.value = el.value.replace(/\D/g, '')
  el.value = idText.value
}

function toggleStatus(key: StatusKey, on: boolean) {
  const s = draft.value.statuses.filter((k) => k !== key)
  draft.value.statuses = on ? [...s, key] : s
}

const datesWrong = computed(() => !!draft.value.from && !!draft.value.to && draft.value.from > draft.value.to)

function apply() {
  if (datesWrong.value) return
  const id = Number(idText.value)
  emit('apply', { ...draft.value, id: id > 0 ? id : null })
}

function clear() {
  draft.value = { ...EMPTY_FILTER, statuses: [] }
  idText.value = ''
}
</script>

<template>
  <Transition name="fade">
    <div v-if="open" class="backdrop" data-test="filter-backdrop" @click="emit('close')"></div>
  </Transition>
  <Transition name="slide">
    <aside v-if="open" class="panel" role="dialog" aria-modal="true" aria-labelledby="filter-title" data-test="filter-panel">
      <header class="panel-head">
        <h2 id="filter-title">Filtrele</h2>
        <button type="button" class="close" aria-label="Kapat" data-test="filter-close" @click="emit('close')">
          <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M4 4l8 8M12 4l-8 8" /></svg>
        </button>
      </header>

      <form class="panel-body" @submit.prevent="apply">
        <label class="field">
          <span class="label">Kampanya no</span>
          <input
            ref="firstField"
            type="text"
            inputmode="numeric"
            autocomplete="off"
            data-test="filter-id"
            :value="idText"
            @input="typeId($event.target as HTMLInputElement)"
          />
        </label>

        <fieldset class="field">
          <legend class="label">Tarih</legend>
          <div class="dates">
            <input v-model="draft.from" type="date" aria-label="Başlangıç tarihi" data-test="filter-from" :class="{ wrong: datesWrong }" />
            <span class="muted" aria-hidden="true">–</span>
            <input v-model="draft.to" type="date" aria-label="Bitiş tarihi" data-test="filter-to" :class="{ wrong: datesWrong }" />
          </div>
          <p v-if="datesWrong" class="error" role="alert">Bitiş tarihi başlangıçtan önce olamaz</p>
        </fieldset>

        <fieldset class="field">
          <legend class="label">Durum</legend>
          <label v-for="c in STATUS_CHOICES" :key="c.key" class="check">
            <input
              type="checkbox"
              :checked="draft.statuses.includes(c.key)"
              :data-test="`filter-status-${c.key}`"
              @change="toggleStatus(c.key, ($event.target as HTMLInputElement).checked)"
            />
            {{ c.label }}
          </label>
        </fieldset>

        <!-- The submit button is in the form, so Enter in a field applies too. -->
        <footer class="panel-foot">
          <button type="button" class="btn" data-test="filter-clear" @click="clear">Temizle</button>
          <button type="submit" class="btn btn-primary" data-test="filter-apply" :disabled="datesWrong">Uygula</button>
        </footer>
      </form>
    </aside>
  </Transition>
</template>

<style scoped>
.backdrop {
  position: fixed;
  inset: 0;
  z-index: 30;
  background: rgba(39, 49, 66, 0.18);
}
.panel {
  position: fixed;
  top: 0;
  right: 0;
  bottom: 0;
  z-index: 31;
  width: min(22rem, 100vw);
  display: flex;
  flex-direction: column;
  background: var(--surface);
  border-left: 1px solid var(--line);
  box-shadow: -0.5rem 0 2rem rgba(39, 49, 66, 0.12);
}
.panel-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 1.125rem 1.25rem;
  border-bottom: 1px solid var(--line);
}
h2 {
  margin: 0;
  font-size: var(--text-lg);
  font-weight: 600;
}
.close {
  display: grid;
  place-items: center;
  width: 2rem;
  height: 2rem;
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--ink-soft);
}
.close:hover {
  background: var(--surface-muted);
}
.close svg {
  width: 1rem;
  height: 1rem;
  stroke: currentColor;
  stroke-width: 1.6;
  stroke-linecap: round;
  fill: none;
}
.panel-body {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 1.375rem;
  padding: 1.25rem;
  overflow-y: auto;
}
.field {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  border: 0;
  margin: 0;
  padding: 0;
  min-width: 0;
}
.label {
  padding: 0;
  font-size: var(--text-xs);
  font-weight: 500;
  letter-spacing: 0.04em;
  color: var(--muted);
}
input[type='text'],
input[type='date'] {
  width: 100%;
  min-width: 0;
  padding: 0.4375rem 0.625rem;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-sm);
  background: var(--surface);
  font-size: var(--text-sm);
}
input[type='text']:focus,
input[type='date']:focus {
  outline: none;
  border-color: var(--accent);
}
input.wrong {
  border-color: var(--bad);
}
.dates {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.error {
  margin: 0;
  font-size: var(--text-xs);
  color: var(--bad);
}
.check {
  display: flex;
  align-items: center;
  gap: 0.625rem;
  font-size: var(--text-md);
  padding: 0.125rem 0;
  cursor: pointer;
}
.check input {
  width: 1rem;
  height: 1rem;
  accent-color: var(--accent);
}
.panel-foot {
  margin-top: auto;
  display: flex;
  justify-content: flex-end;
  gap: 0.5rem;
  padding-top: 1rem;
  border-top: 1px solid var(--line);
}
.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.18s ease;
}
.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}
.slide-enter-active,
.slide-leave-active {
  transition: transform 0.22s ease;
}
.slide-enter-from,
.slide-leave-to {
  transform: translateX(100%);
}
@media (prefers-reduced-motion: reduce) {
  .fade-enter-active,
  .fade-leave-active,
  .slide-enter-active,
  .slide-leave-active {
    transition: none;
  }
}
</style>
