<script setup lang="ts">
import { computed, onMounted, ref, watchEffect } from 'vue'
import { RouterLink, RouterView, useRoute } from 'vue-router'
import { api } from './api/client'

// "mock" when the backend runs without a real model (D-LLM-MOCK).
const provider = ref<string | null>(null)
const route = useRoute()

// A campaign page belongs to the campaign list section of the menu.
const inCampaigns = computed(() => route.name === 'campaigns' || route.name === 'campaign')

// The page's UI scale is chosen in base.css from this attribute.
watchEffect(() => {
  document.documentElement.dataset.page = String(route.name ?? '')
})

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
      <span v-if="provider === 'mock'" class="mock-badge" title="Demo modu: LLM_PROVIDER=mock, kural ve mesajlar sabit demo yanıtlarıdır">
        Demo
      </span>
      <span class="spacer"></span>
      <nav class="nav">
        <RouterLink :to="{ name: 'campaigns' }" class="nav-link" :class="{ active: inCampaigns }">Kampanyalar</RouterLink>
        <RouterLink to="/" class="nav-btn">Yeni kampanya</RouterLink>
      </nav>
    </div>
  </header>
  <!-- The list keeps its filter, open row and scroll position while a
       campaign is open, so "back" returns to the same place. -->
  <RouterView v-slot="{ Component }">
    <KeepAlive include="CampaignListView">
      <component :is="Component" />
    </KeepAlive>
  </RouterView>
</template>

<style scoped>
/* px on purpose: the top bar has the same size on every page, whatever the
   page's rem scale (base.css). */
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
  gap: 12px;
  padding-top: 16px;
  padding-bottom: 16px;
}
.brand {
  display: inline-flex;
  align-items: center;
  gap: 14px;
  font-weight: 600;
  font-size: 23px;
  color: var(--ink);
  text-decoration: none;
  letter-spacing: -0.015em;
}
.brand-mark {
  width: 32px;
  height: 32px;
  border-radius: 10px;
  background: linear-gradient(135deg, var(--accent) 0%, var(--sms) 100%);
}
.mock-badge {
  align-self: flex-start;
  margin-top: 2px;
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--variable);
  background: var(--variable-soft);
  border-radius: 999px;
  padding: 2px 9px;
  cursor: help;
}
.spacer {
  flex: 1;
}
.nav {
  display: flex;
  align-items: center;
  gap: 24px;
}
.nav-link {
  position: relative;
  font-size: 19px;
  font-weight: 500;
  color: var(--ink-soft);
  text-decoration: none;
  padding: 8px 2px;
}
.nav-link:hover,
.nav-link.active {
  color: var(--accent);
}
.nav-link.active::after {
  content: '';
  position: absolute;
  left: 0;
  right: 0;
  bottom: -2px;
  height: 2px;
  border-radius: 2px;
  background: var(--accent);
}
.nav-btn {
  display: inline-flex;
  align-items: center;
  white-space: nowrap;
  text-decoration: none;
  font-size: 19px;
  font-weight: 500;
  color: var(--accent-ink);
  background: var(--accent);
  border: 1px solid var(--accent);
  border-radius: 9px;
  padding: 10px 20px;
  transition: background 0.15s;
}
.nav-btn:hover {
  background: var(--accent-hover);
}
@media (max-width: 720px) {
  .brand {
    font-size: 17px;
    gap: 10px;
  }
  .brand-mark {
    width: 24px;
    height: 24px;
  }
  .nav {
    gap: 14px;
  }
  .nav-link,
  .nav-btn {
    font-size: 15px;
  }
  .nav-btn {
    padding: 7px 12px;
  }
}
@media (max-width: 480px) {
  .mock-badge {
    display: none;
  }
}
</style>
