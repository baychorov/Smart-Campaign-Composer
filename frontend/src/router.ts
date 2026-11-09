import { createRouter, createWebHistory } from 'vue-router'
import NewCampaignView from './views/NewCampaignView.vue'
import CampaignView from './views/CampaignView.vue'
import CampaignListView from './views/CampaignListView.vue'

// Three pages: the brief form, the campaign list, and one campaign. The
// campaign id is in the URL so a reload finds the same campaign.
export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'new', component: NewCampaignView },
    { path: '/campaigns', name: 'campaigns', component: CampaignListView },
    { path: '/campaigns/:id(\\d+)', name: 'campaign', component: CampaignView, props: (r) => ({ id: Number(r.params.id) }) },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})
