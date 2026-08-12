import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { pluginsAPI, type PublicPlugin } from '@/api/plugins'

const CACHE_TTL_MS = 2_000
const POLL_INTERVAL_MS = 5_000

let requestGeneration = 0

export const usePluginStore = defineStore('plugins', () => {
  const plugins = ref<PublicPlugin[]>([])
  const loading = ref(false)
  const loaded = ref(false)
  const lastFetchedAt = ref<number | null>(null)

  let activePromise: Promise<PublicPlugin[]> | null = null
  let pollerInterval: ReturnType<typeof setInterval> | null = null

  const hasPlugins = computed(() => plugins.value.length > 0)

  async function fetchPlugins(force = false): Promise<PublicPlugin[]> {
    const now = Date.now()
    if (
      !force &&
      loaded.value &&
      lastFetchedAt.value !== null &&
      now - lastFetchedAt.value < CACHE_TTL_MS
    ) {
      return plugins.value
    }
    if (activePromise) {
      return activePromise
    }

    const currentGeneration = ++requestGeneration
    loading.value = true
    const requestPromise = pluginsAPI
      .list()
      .then((items) => {
        if (currentGeneration === requestGeneration) {
          plugins.value = items
          loaded.value = true
          lastFetchedAt.value = Date.now()
        }
        return items
      })
      .finally(() => {
        if (activePromise === requestPromise) {
          activePromise = null
          loading.value = false
        }
      })

    activePromise = requestPromise
    return requestPromise
  }

  function startPolling(): void {
    if (pollerInterval) return
    pollerInterval = setInterval(() => {
      fetchPlugins(true).catch((error) => {
        console.warn('Plugin registry polling failed:', error)
      })
    }, POLL_INTERVAL_MS)
  }

  function stopPolling(): void {
    if (!pollerInterval) return
    clearInterval(pollerInterval)
    pollerInterval = null
  }

  function clear(): void {
    requestGeneration++
    activePromise = null
    plugins.value = []
    loading.value = false
    loaded.value = false
    lastFetchedAt.value = null
    stopPolling()
  }

  function findById(id: string): PublicPlugin | undefined {
    return plugins.value.find((plugin) => plugin.id === id)
  }

  return {
    plugins,
    loading,
    loaded,
    hasPlugins,
    fetchPlugins,
    startPolling,
    stopPolling,
    clear,
    findById,
  }
})
