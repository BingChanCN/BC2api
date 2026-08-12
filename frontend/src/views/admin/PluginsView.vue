<template>
  <AppLayout>
    <div class="space-y-6">
      <div class="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 class="text-2xl font-bold text-gray-900 dark:text-white">
            {{ t('admin.plugins.title') }}
          </h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">
            {{ t('admin.plugins.description') }}
          </p>
        </div>
        <div class="flex flex-wrap items-center gap-3">
          <button type="button" class="btn btn-secondary" :disabled="loading" @click="loadStatus(false)">
            {{ t('common.refresh') }}
          </button>
          <button type="button" class="btn btn-primary" :disabled="loading || refreshing" @click="forceRefresh">
            {{ refreshing ? t('admin.plugins.refreshing') : t('admin.plugins.forceRefresh') }}
          </button>
        </div>
      </div>

      <div v-if="error" class="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/40 dark:bg-red-950/40 dark:text-red-300">
        {{ error }}
      </div>

      <div class="grid gap-4 sm:grid-cols-3">
        <div class="card p-4">
          <div class="text-sm text-gray-500 dark:text-dark-400">{{ t('admin.plugins.status') }}</div>
          <div class="mt-2 text-lg font-semibold text-gray-900 dark:text-white">
            {{ status?.enabled ? t('admin.plugins.enabled') : t('admin.plugins.disabled') }}
          </div>
        </div>
        <div class="card p-4">
          <div class="text-sm text-gray-500 dark:text-dark-400">{{ t('admin.plugins.loadedCount') }}</div>
          <div class="mt-2 text-lg font-semibold text-gray-900 dark:text-white">
            {{ status?.plugins.length ?? 0 }}
          </div>
        </div>
        <div class="card p-4">
          <div class="text-sm text-gray-500 dark:text-dark-400">{{ t('admin.plugins.lastRefresh') }}</div>
          <div class="mt-2 text-lg font-semibold text-gray-900 dark:text-white">
            {{ formatTime(status?.last_refresh) }}
          </div>
        </div>
      </div>

      <div class="card overflow-hidden">
        <div class="border-b border-gray-200 px-4 py-3 dark:border-dark-700">
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('admin.plugins.loadedPlugins') }}
          </h2>
        </div>
        <div v-if="loading && !status" class="flex justify-center p-10">
          <LoadingSpinner size="lg" />
        </div>
        <div v-else-if="!(status?.plugins.length)" class="p-8 text-center text-sm text-gray-500 dark:text-dark-400">
          {{ t('admin.plugins.empty') }}
        </div>
        <div v-else class="overflow-x-auto">
          <table class="min-w-full divide-y divide-gray-200 dark:divide-dark-700">
            <thead class="bg-gray-50 dark:bg-dark-800/60">
              <tr>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.plugins.columns.id') }}</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.plugins.columns.name') }}</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.plugins.columns.version') }}</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.plugins.columns.visibility') }}</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.plugins.columns.runtime') }}</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.plugins.columns.api') }}</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.plugins.columns.actions') }}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-200 bg-white dark:divide-dark-700 dark:bg-dark-900">
              <tr v-for="plugin in status?.plugins || []" :key="plugin.id">
                <td class="whitespace-nowrap px-4 py-3 text-sm font-mono text-gray-700 dark:text-dark-200">{{ plugin.id }}</td>
                <td class="px-4 py-3 text-sm text-gray-900 dark:text-white">
                  <div>{{ plugin.name }}</div>
                  <div v-if="plugin.description" class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{ plugin.description }}</div>
                </td>
                <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-600 dark:text-dark-300">{{ plugin.version || '—' }}</td>
                <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-600 dark:text-dark-300">{{ visibilityLabel(plugin.visibility) }}</td>
                <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-600 dark:text-dark-300">{{ plugin.runtime }}</td>
                <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-600 dark:text-dark-300">
                  {{ plugin.has_api ? t('admin.plugins.yes') : t('admin.plugins.no') }}
                </td>
                <td class="whitespace-nowrap px-4 py-3 text-sm">
                  <router-link :to="`/plugins/${plugin.id}`" class="text-primary-600 hover:underline dark:text-primary-400">
                    {{ t('admin.plugins.open') }}
                  </router-link>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>

      <div class="card overflow-hidden">
        <div class="border-b border-gray-200 px-4 py-3 dark:border-dark-700">
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('admin.plugins.errors') }}
          </h2>
        </div>
        <div v-if="!errorEntries.length" class="p-8 text-center text-sm text-gray-500 dark:text-dark-400">
          {{ t('admin.plugins.noErrors') }}
        </div>
        <ul v-else class="divide-y divide-gray-200 dark:divide-dark-700">
          <li v-for="item in errorEntries" :key="item.id" class="px-4 py-3">
            <div class="font-mono text-sm font-medium text-gray-900 dark:text-white">{{ item.id }}</div>
            <div class="mt-1 text-sm text-red-600 dark:text-red-400">{{ item.message }}</div>
          </li>
        </ul>
      </div>

      <div class="card p-4 text-sm text-gray-600 dark:text-dark-300">
        <p>{{ t('admin.plugins.hintDirectory') }}</p>
        <p class="mt-2">{{ t('admin.plugins.hintHotReload') }}</p>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import { pluginsAPI, type PluginRegistryStatus } from '@/api/plugins'
import { usePluginStore } from '@/stores/plugins'

const { t, locale } = useI18n()
const pluginStore = usePluginStore()

const status = ref<PluginRegistryStatus | null>(null)
const loading = ref(false)
const refreshing = ref(false)
const error = ref('')

const errorEntries = computed(() => {
  const errors = status.value?.errors || {}
  return Object.entries(errors)
    .map(([id, message]) => ({ id, message }))
    .sort((a, b) => a.id.localeCompare(b.id))
})

function formatTime(value?: string): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime()) || date.getTime() === 0) return '—'
  return date.toLocaleString(locale.value === 'zh' ? 'zh-CN' : 'en-US')
}

function visibilityLabel(value: string): string {
  if (value === 'admin') return t('admin.plugins.visibilityAdmin')
  return t('admin.plugins.visibilityUser')
}

async function loadStatus(silent = false): Promise<void> {
  if (!silent) loading.value = true
  error.value = ''
  try {
    status.value = await pluginsAPI.getRegistryStatus()
  } catch (err) {
    const message = err && typeof err === 'object' && 'message' in err
      ? String((err as { message?: string }).message || '')
      : ''
    error.value = message || t('admin.plugins.loadFailed')
  } finally {
    loading.value = false
  }
}

async function forceRefresh(): Promise<void> {
  refreshing.value = true
  error.value = ''
  try {
    status.value = await pluginsAPI.refreshRegistry()
    await pluginStore.fetchPlugins(true)
  } catch (err) {
    const message = err && typeof err === 'object' && 'message' in err
      ? String((err as { message?: string }).message || '')
      : ''
    error.value = message || t('admin.plugins.refreshFailed')
  } finally {
    refreshing.value = false
  }
}

onMounted(() => {
  void loadStatus()
})
</script>
