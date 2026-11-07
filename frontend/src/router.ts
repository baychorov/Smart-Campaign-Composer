import { createRouter, createWebHistory } from 'vue-router'
import NewCampaignView from './views/NewCampaignView.vue'
import CampaignView from './views/CampaignView.vue'

// Two pages: the brief form, and one campaign. The campaign id is in the URL
// so a reload finds the same campaign (there is no campaign list endpoint).
export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'new', component: NewCampaignView },
    { path: '/campaigns/:id(\\d+)', name: 'campaign', component: CampaignView, props: (r) => ({ id: Number(r.params.id) }) },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})
