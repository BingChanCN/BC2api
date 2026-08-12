import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { usePluginStore } from '@/stores/plugins'

const mockListPlugins = vi.fn()

vi.mock('@/api/plugins', () => ({
  pluginsAPI: {
    list: (...args: unknown[]) => mockListPlugins(...args),
  },
}))

const plugin = {
  id: 'hello',
  name: 'Hello',
  version: '1.0.0',
  visibility: 'user' as const,
  sort_order: 10,
  has_api: false,
  ui_path: '/plugin-runtime/hello/ui/',
}

describe('usePluginStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.useFakeTimers()
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('loads and caches plugin manifests', async () => {
    mockListPlugins.mockResolvedValue([plugin])
    const store = usePluginStore()

    await store.fetchPlugins()
    await store.fetchPlugins()

    expect(mockListPlugins).toHaveBeenCalledTimes(1)
    expect(store.plugins).toEqual([plugin])
    expect(store.findById('hello')).toEqual(plugin)
  })

  it('deduplicates concurrent requests', async () => {
    let resolveRequest: (value: typeof plugin[]) => void = () => undefined
    mockListPlugins.mockImplementation(() => new Promise((resolve) => {
      resolveRequest = resolve
    }))
    const store = usePluginStore()

    const first = store.fetchPlugins(true)
    const second = store.fetchPlugins(true)
    expect(mockListPlugins).toHaveBeenCalledTimes(1)

    resolveRequest([plugin])
    await expect(Promise.all([first, second])).resolves.toEqual([[plugin], [plugin]])
  })

  it('polls for manifest changes without duplicate intervals', async () => {
    mockListPlugins.mockResolvedValue([plugin])
    const store = usePluginStore()

    store.startPolling()
    store.startPolling()
    await vi.advanceTimersByTimeAsync(5_000)

    expect(mockListPlugins).toHaveBeenCalledTimes(1)
    store.stopPolling()
    await vi.advanceTimersByTimeAsync(10_000)
    expect(mockListPlugins).toHaveBeenCalledTimes(1)
  })

  it('clears stale results when the user logs out during a request', async () => {
    let resolveRequest: (value: typeof plugin[]) => void = () => undefined
    mockListPlugins.mockImplementation(() => new Promise((resolve) => {
      resolveRequest = resolve
    }))
    const store = usePluginStore()

    const request = store.fetchPlugins(true)
    store.clear()
    resolveRequest([plugin])
    await request

    expect(store.plugins).toEqual([])
    expect(store.loaded).toBe(false)
    expect(store.loading).toBe(false)
  })
})
