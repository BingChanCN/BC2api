<template>
  <AppLayout>
    <section class="flex min-h-[calc(100vh-8rem)] flex-col">
      <header v-if="plugin" class="mb-4 flex-none">
        <h1 class="text-xl font-semibold text-gray-900 dark:text-white">{{ plugin.name }}</h1>
        <p v-if="plugin.description" class="mt-1 text-sm text-gray-500 dark:text-dark-400">
          {{ plugin.description }}
        </p>
      </header>

      <div v-if="loading" class="flex flex-1 items-center justify-center">
        <LoadingSpinner size="lg" />
      </div>

      <div v-else-if="!plugin" class="flex flex-1 items-center justify-center text-center">
        <div class="max-w-md">
          <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
            {{ t('pluginPage.notFoundTitle') }}
          </h2>
          <p class="mt-2 text-sm text-gray-500 dark:text-dark-400">
            {{ t('pluginPage.notFoundDesc') }}
          </p>
        </div>
      </div>

      <iframe
        v-else
        ref="pluginFrame"
        :key="capability"
        :src="iframeSource"
        :title="plugin.name"
        class="min-h-0 flex-1 rounded-md border border-gray-200 bg-white dark:border-dark-700"
        sandbox="allow-scripts"
        allow="fullscreen"
      ></iframe>
    </section>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import { buildGatewayUrl } from '@/api/client'
import { pluginsAPI } from '@/api/plugins'
import { usePluginStore } from '@/stores/plugins'
import {
  PLUGIN_BRIDGE_PROTOCOL,
  createPluginCapability,
  parsePluginInvokeMessage,
  pluginBridgeError,
  type PluginResultMessage,
} from '@/features/plugins/bridge'

const MAX_CONCURRENT_REQUESTS = 8

const route = useRoute()
const { t } = useI18n()
const pluginStore = usePluginStore()
const pluginFrame = ref<HTMLIFrameElement | null>(null)
const capability = ref(createPluginCapability())
const inFlight = new Set<string>()

const pluginId = computed(() => String(route.params.id || ''))
const plugin = computed(() => pluginStore.findById(pluginId.value))
const loading = computed(() => pluginStore.loading && !pluginStore.loaded)
const iframeSource = computed(() => {
  if (!plugin.value) return ''
  const source = buildGatewayUrl(plugin.value.ui_path)
  const fragment = new URLSearchParams({
    protocol: PLUGIN_BRIDGE_PROTOCOL,
    capability: capability.value,
  })
  return `${source}#${fragment.toString()}`
})

function postResult(target: Window, message: PluginResultMessage): void {
  if (pluginFrame.value?.contentWindow !== target || message.capability !== capability.value) {
    return
  }
  target.postMessage(message, '*')
}

async function onPluginMessage(event: MessageEvent): Promise<void> {
  const frameWindow = pluginFrame.value?.contentWindow
  if (!frameWindow || event.source !== frameWindow || !plugin.value) return

  const message = parsePluginInvokeMessage(event.data, capability.value)
  if (!message) return

  if (!plugin.value.has_api) {
    postResult(frameWindow, {
      protocol: PLUGIN_BRIDGE_PROTOCOL,
      type: 'result',
      capability: capability.value,
      request_id: message.request_id,
      ok: false,
      error: { status: 404, message: 'Plugin API is not configured' },
    })
    return
  }
  const requestKey = `${capability.value}:${message.request_id}`
  const activeRequestCount = Array.from(inFlight).filter((key) => key.startsWith(`${capability.value}:`)).length
  if (inFlight.has(requestKey) || activeRequestCount >= MAX_CONCURRENT_REQUESTS) {
    postResult(frameWindow, {
      protocol: PLUGIN_BRIDGE_PROTOCOL,
      type: 'result',
      capability: capability.value,
      request_id: message.request_id,
      ok: false,
      error: { status: 429, message: 'Too many plugin requests' },
    })
    return
  }

  const requestPluginId = plugin.value.id
  const requestCapability = capability.value
  inFlight.add(requestKey)
  try {
    const result = await pluginsAPI.invoke(requestPluginId, message.request)
    postResult(frameWindow, {
      protocol: PLUGIN_BRIDGE_PROTOCOL,
      type: 'result',
      capability: requestCapability,
      request_id: message.request_id,
      ok: true,
      response: result,
    })
  } catch (error) {
    postResult(frameWindow, {
      protocol: PLUGIN_BRIDGE_PROTOCOL,
      type: 'result',
      capability: requestCapability,
      request_id: message.request_id,
      ok: false,
      error: pluginBridgeError(error),
    })
  } finally {
    inFlight.delete(requestKey)
  }
}

async function refreshPlugin(): Promise<void> {
  try {
    await pluginStore.fetchPlugins(true)
  } catch (error) {
    console.warn('Failed to load plugin registry:', error)
  }
}

watch(pluginId, () => {
  capability.value = createPluginCapability()
  void refreshPlugin()
})

onMounted(() => {
  window.addEventListener('message', onPluginMessage)
  void refreshPlugin()
})

onBeforeUnmount(() => {
  window.removeEventListener('message', onPluginMessage)
  inFlight.clear()
})
</script>
