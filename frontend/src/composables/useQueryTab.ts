import { ref, watch, type Ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'

export function useQueryTab<T extends string>(tabs: readonly T[], fallback: T) {
  const route = useRoute()
  const router = useRouter()

  const normalize = (value: unknown): T => {
    return tabs.includes(value as T) ? (value as T) : fallback
  }

  const activeTab = ref(normalize(route.query.tab)) as Ref<T>

  watch(
    () => route.query.tab,
    (value) => {
      activeTab.value = normalize(value)
    },
  )

  function onTabChange(name: string | number) {
    const next = normalize(String(name))
    activeTab.value = next

    const query = { ...route.query }
    if (next === fallback) {
      delete query.tab
    } else {
      query.tab = next
    }
    if (route.query.tab !== query.tab) {
      router.replace({ query })
    }
  }

  return { activeTab, onTabChange }
}
