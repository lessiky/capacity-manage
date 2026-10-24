import { defineStore } from 'pinia'
import { http } from '@/api/http'

const DEFAULT_NAME = '容量管理平台'

interface BrandingData {
  name?: string
  icon?: string
}

function syncFavicon(icon: string) {
  let link = document.querySelector<HTMLLinkElement>('link[data-platform-branding="true"]')
  if (!icon) {
    link?.remove()
    return
  }
  if (!link) {
    link = document.createElement('link')
    link.rel = 'icon'
    link.dataset.platformBranding = 'true'
    document.head.appendChild(link)
  }
  link.href = icon
}

export const useBrandingStore = defineStore('branding', {
  state: () => ({
    name: DEFAULT_NAME,
    icon: '',
    pageTitle: '',
  }),
  getters: {
    initial: (s) => (s.name.trim() || DEFAULT_NAME).slice(0, 1),
  },
  actions: {
    apply(data: BrandingData) {
      this.name = data.name?.trim() || DEFAULT_NAME
      this.icon = data.icon ?? ''
      syncFavicon(this.icon)
      this.syncTitle()
    },
    async load() {
      try {
        const data = (await http.get('/api/v1/branding', { silent: true })) as unknown as BrandingData
        this.apply(data)
      } catch {
        this.apply({})
      }
    },
    setPageTitle(title: string) {
      this.pageTitle = title
      this.syncTitle()
    },
    syncTitle() {
      document.title = this.pageTitle ? `${this.pageTitle} - ${this.name}` : this.name
    },
  },
})
