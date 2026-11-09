<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { RouterLink, RouterView } from 'vue-router'
import { api } from './api/client'

// "mock" when the backend runs without a real model (D-LLM-MOCK).
const provider = ref<string | null>(null)

onMounted(async () => {
  try {
    provider.value = (await api.info()).llm_provider
  } catch {
    provider.value = null // the badge is informational only
  }
})
</script>

<template>
  <header class="topbar">
    <div class="container topbar-inner">
      <RouterLink to="/" class="brand">
        <span class="brand-mark" aria-hidden="true"></span>
        Smart Campaign Composer
      </RouterLink>
      <span class="spacer"></span>
      <span v-if="provider === 'mock'" class="mock-badge" title="LLM_PROVIDER=mock: kural ve mesajlar sabit demo yanıtlarıdır">
        Demo modu · mock LLM
      </span>
      <RouterLink :to="{ name: 'campaigns' }" class="nav-link">Kampanyalar</RouterLink>
      <RouterLink to="/" class="btn">Yeni kampanya</RouterLink>
    </div>
  </header>
  <RouterView />
</template>

<style scoped>
.topbar {
  background: var(--surface);
  border-bottom: 1px solid var(--line);
  position: sticky;
  top: 0;
  z-index: 20;
}
.topbar-inner {
  display: flex;
  align-items: center;
  gap: 14px;
  padding-top: 12px;
  padding-bottom: 12px;
}
.brand {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  font-weight: 600;
  font-size: var(--text-lg);
  color: var(--ink);
  text-decoration: none;
  letter-spacing: -0.01em;
}
.brand-mark {
  width: 22px;
  height: 22px;
  border-radius: 7px;
  background: linear-gradient(135deg, var(--accent) 0%, var(--sms) 100%);
}
.spacer {
  flex: 1;
}
.mock-badge {
  font-size: var(--text-xs);
  font-weight: 500;
  color: var(--variable);
  background: var(--variable-soft);
  border-radius: 999px;
  padding: 3px 10px;
}
a.btn {
  text-decoration: none;
  white-space: nowrap;
}
.nav-link {
  font-size: var(--text-sm);
  font-weight: 500;
  color: var(--ink-soft);
  text-decoration: none;
  padding: 7px 4px;
}
.nav-link:hover,
.nav-link.router-link-exact-active {
  color: var(--accent);
}
@media (max-width: 560px) {
  .brand {
    font-size: var(--text-md);
  }
  .mock-badge {
    display: none;
  }
}
</style>
