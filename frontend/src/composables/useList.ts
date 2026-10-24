import { ref } from 'vue'
import { http } from '@/api/http'

export interface PageResult<T> {
  items: T[]
  total: number
}

// 通用列表拉取：GET /api/v1/<path>?page=&pageSize=...
export function useList<T = Record<string, unknown>>(path: string, pageSize = 20) {
  const items = ref<T[]>([])
  const total = ref(0)
  const loading = ref(false)
  const error = ref('')
  const page = ref(1)
  const params = ref<Record<string, unknown>>({})

  async function load() {
    loading.value = true
    error.value = ''
    try {
      const data = (await http.get(path, {
        params: { page: page.value, pageSize, ...params.value },
      })) as unknown as PageResult<T>
      items.value = data.items ?? []
      total.value = data.total ?? 0
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
    } finally {
      loading.value = false
    }
  }

  load()
  return { items, total, loading, error, page, params, pageSize, reload: load }
}