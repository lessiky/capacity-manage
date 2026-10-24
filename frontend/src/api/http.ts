import axios from 'axios'
import { ElMessage } from 'element-plus'
import router from '@/router'

// 可选请求静默: 传 silent=true 时不弹出错误提示（用于"暂无预测结果"等可空数据）
declare module 'axios' {
  export interface AxiosRequestConfig {
    silent?: boolean
  }
}

// 统一响应包裹（docs/10-design/03-API规范.md §1.2）
export interface ApiResponse<T = unknown> {
  code: number
  message: string
  data: T
  traceId?: string
}

export const http = axios.create({
  baseURL: '/',
  timeout: 30000,
})

http.interceptors.request.use((config) => {
  const token = localStorage.getItem('cap_access_token')
  if (token) {
    config.headers = config.headers ?? {}
    ;(config.headers as Record<string, string>)['Authorization'] = `Bearer ${token}`
  }
  return config
})

http.interceptors.response.use(
  (response) => {
    const body = response.data as ApiResponse
    if (body && typeof body.code === 'number') {
      if (body.code === 0) {
        return body.data as never
      }
      if (body.code === 40100 || body.code === 40101) {
        localStorage.removeItem('cap_access_token')
        router.push({ name: 'login', query: { redirect: router.currentRoute.value.fullPath } })
      }
      if (!response.config.silent) {
        ElMessage.error(body.message || `请求失败(${body.code})`)
      }
      return Promise.reject(new Error(body.message || `code=${body.code}`))
    }
    return response.data
  },
  (error) => {
    const status = error.response?.status
    if (status === 401) {
      localStorage.removeItem('cap_access_token')
      router.push({ name: 'login' })
    }
    const msg = error.response?.data?.message || error.message || '网络错误'
    if (!error.config?.silent) {
      ElMessage.error(msg)
    }
    return Promise.reject(error)
  },
)

export function getPage(data: ApiResponse<{ items: unknown[]; total: number }>) {
  return { items: data.data.items ?? [], total: data.data.total ?? 0 }
}
