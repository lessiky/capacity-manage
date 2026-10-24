import { defineStore } from 'pinia'
import { http } from '@/api/http'

// 与 /api/v1/auth/login 响应一致（docs/10-design/03-API规范.md）
export interface LoginUser {
  id: number
  username: string
  realName: string
  dept: string
  roles: string[]
  mustChangePwd: boolean
}

interface LoginResult {
  accessToken: string
  expiresIn: number
  userInfo: LoginUser
}

let restorePromise: Promise<void> | null = null

export const useAuthStore = defineStore('auth', {
  state: () => ({
    token: localStorage.getItem('cap_access_token') || '',
    user: null as LoginUser | null,
  }),
  getters: {
    isLoggedIn: (s) => !!s.token,
    isAdmin: (s) => (s.user?.roles ?? []).includes('platform_admin'),
  },
  actions: {
    async login(username: string, password: string) {
      const data = (await http.post('/api/v1/auth/login', { username, password })) as unknown as LoginResult
      this.token = data.accessToken
      this.user = data.userInfo ?? null
      localStorage.setItem('cap_access_token', data.accessToken)
    },
    async restore() {
      if (!this.token || this.user) return
      if (!restorePromise) {
        restorePromise = http.get('/api/v1/auth/me', { silent: true })
          .then((data) => {
            this.user = data as unknown as LoginUser
          })
          .catch((err) => {
            this.token = ''
            this.user = null
            localStorage.removeItem('cap_access_token')
            throw err
          })
          .finally(() => {
            restorePromise = null
          })
      }
      return restorePromise
    },
    async logout() {
      try {
        await http.post('/api/v1/auth/logout')
      } finally {
        this.token = ''
        this.user = null
        localStorage.removeItem('cap_access_token')
      }
    },
  },
})
