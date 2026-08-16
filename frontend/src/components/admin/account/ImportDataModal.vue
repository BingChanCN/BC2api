<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.dataImportTitle')"
    width="normal"
    close-on-click-outside
    @close="handleClose"
  >
    <form id="import-data-form" class="space-y-4" @submit.prevent="handleImport">
      <div class="text-sm text-gray-600 dark:text-dark-300">
        {{ t('admin.accounts.dataImportHint') }}
      </div>
      <div
        class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-xs text-amber-600 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-400"
      >
        {{ t('admin.accounts.dataImportWarning') }}
      </div>

      <div>
        <label class="input-label">{{ t('admin.accounts.dataImportFile') }}</label>
        <div
          class="flex items-center justify-between gap-3 rounded-lg border border-dashed px-4 py-3 transition-colors"
          :class="dragActive
            ? 'border-primary-400 bg-primary-50/70 dark:border-primary-500 dark:bg-primary-900/20'
            : 'border-gray-300 bg-gray-50 dark:border-dark-600 dark:bg-dark-800'"
          @dragenter.prevent="handleDragEnter"
          @dragover.prevent
          @dragleave.prevent="handleDragLeave"
          @drop.prevent="handleDrop"
        >
          <div class="min-w-0">
            <div class="truncate text-sm text-gray-700 dark:text-dark-200" :title="fileListTitle">
              {{ selectedFilesLabel || t('admin.accounts.dataImportSelectFile') }}
            </div>
            <div class="text-xs text-gray-500 dark:text-dark-400">
              JSON (.json)
              <span v-if="files.length > 1"> · {{ fileListTitle }}</span>
            </div>
          </div>
          <button type="button" class="btn btn-secondary shrink-0" @click="openFilePicker">
            {{ t('common.chooseFile') }}
          </button>
        </div>
        <input
          ref="fileInput"
          type="file"
          class="hidden"
          accept="application/json,.json"
          multiple
          @change="handleFileChange"
        />
      </div>

      <div v-if="filesValidated" class="space-y-3 border-t border-gray-200 pt-4 dark:border-dark-700">
        <div class="text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.accounts.managedProxyImport.title') }}</div>
        <div class="grid grid-cols-3 gap-1 rounded-md bg-gray-100 p-1 dark:bg-dark-800">
          <button v-for="option in proxyModeOptions" :key="option.value" type="button" class="rounded px-2 py-2 text-xs font-medium disabled:cursor-not-allowed disabled:opacity-50" :class="proxyMode === option.value ? 'bg-white text-primary-600 shadow-sm dark:bg-dark-700' : 'text-gray-500 dark:text-dark-300'" :disabled="option.disabled" @click="proxyMode = option.value">
            {{ option.label }}
          </button>
        </div>

        <div v-if="proxyMode === 'managed'" class="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div class="sm:col-span-2">
            <label class="input-label">{{ t('admin.accounts.managedProxyImport.provider') }}</label>
            <select v-model.number="managedProviderId" class="input" required>
              <option v-for="config in activeManagedConfigs" :key="config.id" :value="config.id">{{ config.name }}{{ config.is_default ? ` · ${t('admin.accounts.managedProxyImport.default')}` : '' }}</option>
            </select>
          </div>
          <div>
            <label class="input-label">{{ t('admin.accounts.managedProxyImport.country') }}</label>
            <select v-model="managedTarget.country" class="input" @change="handleManagedCountryChange"><option value="">{{ t('admin.accounts.managedProxyImport.providerDefault') }}</option><option v-for="country in targeting.countries" :key="country.code" :value="country.code">{{ country.name }}</option></select>
          </div>
          <div v-if="managedTarget.country === 'us'">
            <label class="input-label">{{ t('admin.accounts.managedProxyImport.state') }}</label>
            <select v-model="managedTarget.state" class="input" @change="loadManagedCities"><option value="">{{ t('admin.accounts.managedProxyImport.any') }}</option><option v-for="state in targeting.us_states" :key="state" :value="state">{{ state }}</option></select>
          </div>
          <div>
            <label class="input-label">{{ t('admin.accounts.managedProxyImport.city') }}</label>
            <select v-model="managedTarget.city" class="input" :disabled="!managedTarget.country"><option value="">{{ t('admin.accounts.managedProxyImport.any') }}</option><option v-for="city in targeting.cities" :key="city" :value="city">{{ city }}</option></select>
          </div>
          <label v-if="managedTarget.country" class="flex items-end gap-2 pb-2 text-sm text-gray-700 dark:text-dark-200"><input v-model="managedTarget.strict" type="checkbox" class="h-4 w-4 rounded border-gray-300 text-primary-600" />{{ t('admin.accounts.managedProxyImport.strict') }}</label>
        </div>
        <p v-if="proxyMode === 'managed'" class="text-xs text-gray-500 dark:text-dark-400">{{ t('admin.accounts.managedProxyImport.probeHint', { count: parsedAccountCount }) }}</p>
      </div>

      <div
        v-if="result"
        class="space-y-2 rounded-xl border border-gray-200 p-4 dark:border-dark-700"
      >
        <div class="text-sm font-medium text-gray-900 dark:text-white">
          {{ t('admin.accounts.dataImportResult') }}
        </div>
        <div class="text-sm text-gray-700 dark:text-dark-300">
          {{ t('admin.accounts.dataImportResultSummary', result) }}
        </div>

        <div v-if="result.account_results?.length" class="max-h-48 divide-y divide-gray-100 overflow-auto rounded-md border border-gray-100 dark:divide-dark-700 dark:border-dark-700">
          <div v-for="(item, idx) in result.account_results" :key="`${item.name}-${idx}`" class="flex items-start justify-between gap-3 px-3 py-2 text-xs">
            <div class="min-w-0">
              <div class="truncate font-medium text-gray-800 dark:text-dark-100" :title="item.name">{{ item.name || '-' }}</div>
              <div v-if="item.message" class="mt-0.5 break-words text-gray-500 dark:text-dark-400">{{ item.message }}</div>
            </div>
            <span class="shrink-0 rounded px-2 py-0.5 font-medium" :class="item.status === 'created' ? 'bg-green-50 text-green-700 dark:bg-green-900/20 dark:text-green-300' : 'bg-red-50 text-red-700 dark:bg-red-900/20 dark:text-red-300'">
              {{ t(`admin.accounts.managedProxyImport.resultStatus.${item.status}`) }}
            </span>
          </div>
        </div>

        <div v-if="errorItems.length" class="mt-2">
          <div class="text-sm font-medium text-red-600 dark:text-red-400">
            {{ t('admin.accounts.dataImportErrors') }}
          </div>
          <div
            class="mt-2 max-h-48 overflow-auto rounded-lg bg-gray-50 p-3 font-mono text-xs dark:bg-dark-800"
          >
            <div v-for="(item, idx) in errorItems" :key="idx" class="whitespace-pre-wrap">
              {{ item.kind }} {{ item.name || item.proxy_key || '-' }} — {{ item.message }}
            </div>
          </div>
        </div>
      </div>
    </form>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button class="btn btn-secondary" type="button" :disabled="importing" @click="handleClose">
          {{ t('common.cancel') }}
        </button>
        <button
          class="btn btn-primary"
          type="submit"
          form="import-data-form"
          :disabled="importing || validatingFiles || !filesValidated"
        >
          {{ importing ? t('admin.accounts.dataImporting') : t('admin.accounts.dataImportButton') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import type { AdminDataImportResult, AdminDataPayload } from '@/types'
import type { CatProxyProviderConfig, CatProxyTargeting } from '@/api/admin'

interface Props {
  show: boolean
}

interface Emits {
  (e: 'close'): void
  (e: 'imported'): void
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()

const { t } = useI18n()
const appStore = useAppStore()

const importing = ref(false)
const validatingFiles = ref(false)
let dialogGeneration = 0
let fileValidationGeneration = 0
let targetingGeneration = 0
const files = ref<File[]>([])
const dragDepth = ref(0)
const dragActive = computed(() => dragDepth.value > 0)
const hasCreatedData = ref(false)
const result = ref<AdminDataImportResult | null>(null)
const filesValidated = ref(false)
const hasFileProxies = ref(false)
const parsedAccountCount = ref(0)
const managedConfigs = ref<CatProxyProviderConfig[]>([])
const managedProviderId = ref<number | null>(null)
const proxyMode = ref<'none' | 'file' | 'managed'>('none')
const targeting = ref<CatProxyTargeting>({ countries: [], us_states: [], cities: [] })
const managedTarget = reactive({ country: '', state: '', city: '', strict: true })
const activeManagedConfigs = computed(() => managedConfigs.value.filter((item) => item.status === 'active'))
const proxyModeOptions = computed(() => [
  { value: 'none' as const, label: t('admin.accounts.managedProxyImport.none'), disabled: hasFileProxies.value },
  { value: 'file' as const, label: t('admin.accounts.managedProxyImport.file'), disabled: !hasFileProxies.value },
  { value: 'managed' as const, label: t('admin.accounts.managedProxyImport.managed'), disabled: hasFileProxies.value || activeManagedConfigs.value.length === 0 }
])

const fileInput = ref<HTMLInputElement | null>(null)
const selectedFilesLabel = computed(() => {
  if (files.value.length === 0) return ''
  if (files.value.length === 1) return files.value[0]?.name || ''
  return t('admin.accounts.selectedCount', { count: files.value.length })
})
const fileListTitle = computed(() => files.value.map((item) => item.name).join(', '))

const errorItems = computed(() => result.value?.errors || [])

watch(
  () => props.show,
  (open) => {
    if (open) {
      const generation = ++dialogGeneration
      fileValidationGeneration += 1
      targetingGeneration += 1
      validatingFiles.value = false
      files.value = []
      dragDepth.value = 0
      hasCreatedData.value = false
      result.value = null
      filesValidated.value = false
      hasFileProxies.value = false
      parsedAccountCount.value = 0
      managedTarget.country = ''
      managedTarget.state = ''
      managedTarget.city = ''
      managedTarget.strict = true
      void loadManagedOptions(generation)
      if (fileInput.value) {
        fileInput.value.value = ''
      }
    }
  }
)

const openFilePicker = () => {
  fileInput.value?.click()
}

const handleFileChange = (event: Event) => {
  const target = event.target as HTMLInputElement
  setSelectedFiles(target.files)
  target.value = ''
}

const handleClose = () => {
  if (importing.value) return
  dialogGeneration += 1
  fileValidationGeneration += 1
  targetingGeneration += 1
  validatingFiles.value = false
  if (hasCreatedData.value) {
    hasCreatedData.value = false
    emit('imported')
  }
  emit('close')
}

const isJsonFile = (sourceFile: File) => {
  const name = sourceFile.name.toLowerCase()
  return name.endsWith('.json') || sourceFile.type === 'application/json'
}

const setSelectedFiles = (sourceFiles: FileList | File[] | null | undefined) => {
  if (importing.value) return
  const incoming = Array.from(sourceFiles || [])
  const picked = incoming.filter(isJsonFile)
  if (!picked.length) {
    appStore.showError(t('admin.accounts.dataImportSelectFile'))
    return
  }
  if (picked.length < incoming.length) {
    appStore.showWarning(
      t('admin.accounts.dataImportIgnoredFiles', { count: incoming.length - picked.length })
    )
  }
  const generation = ++fileValidationGeneration
  files.value = picked
  result.value = null
  filesValidated.value = false
  validatingFiles.value = true
  void validateSelectedFiles(picked, generation).finally(() => {
    if (generation === fileValidationGeneration) validatingFiles.value = false
  })
}

const handleDragEnter = () => {
  if (importing.value) return
  dragDepth.value += 1
}

const handleDragLeave = () => {
  dragDepth.value = Math.max(0, dragDepth.value - 1)
}

const handleDrop = (event: DragEvent) => {
  dragDepth.value = 0
  if (importing.value) return
  setSelectedFiles(event.dataTransfer?.files)
}

const readFileAsText = async (sourceFile: File): Promise<string> => {
  if (typeof sourceFile.text === 'function') {
    return sourceFile.text()
  }

  if (typeof sourceFile.arrayBuffer === 'function') {
    const buffer = await sourceFile.arrayBuffer()
    return new TextDecoder().decode(buffer)
  }

  return await new Promise<string>((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result ?? ''))
    reader.onerror = () => reject(reader.error || new Error('Failed to read file'))
    reader.readAsText(sourceFile)
  })
}

const SUPPORTED_DATA_TYPES = ['sub2api-data', 'sub2api-bundle', 'sub2api-account-data']

const loadManagedOptions = async (generation: number) => {
  try {
    const [configs, options] = await Promise.all([
      adminAPI.catproxies.listConfigs(),
      adminAPI.catproxies.getTargeting()
    ])
    if (generation !== dialogGeneration) return
    managedConfigs.value = configs
    targeting.value = options
    const defaultConfig = configs.find((item) => item.status === 'active' && item.is_default)
      || configs.find((item) => item.status === 'active')
    managedProviderId.value = defaultConfig?.id || null
    if (filesValidated.value && !hasFileProxies.value && defaultConfig) proxyMode.value = 'managed'
  } catch {
    if (generation !== dialogGeneration) return
    managedConfigs.value = []
    managedProviderId.value = null
  }
}

const handleManagedCountryChange = async () => {
  managedTarget.state = ''
  managedTarget.city = ''
  await loadManagedCities()
}

const loadManagedCities = async () => {
  const country = managedTarget.country
  const state = managedTarget.state
  const generation = ++targetingGeneration
  managedTarget.city = ''
  try {
    const options = await adminAPI.catproxies.getTargeting(country, state)
    if (generation !== targetingGeneration || country !== managedTarget.country || state !== managedTarget.state) return
    targeting.value = options
  } catch {
    if (generation !== targetingGeneration) return
    targeting.value = { ...targeting.value, cities: [] }
  }
}

// 与后端 validateDataHeader 对齐:合并前逐文件校验,避免坏文件混入合并 payload 后
// 报错无法定位来源,或绕过后端本会对单文件做的 type/version 检查。
const isValidDataPayload = (payload: unknown): payload is AdminDataPayload => {
  if (!payload || typeof payload !== 'object' || Array.isArray(payload)) return false
  const candidate = payload as Record<string, unknown>
  if (
    candidate.type !== undefined &&
    candidate.type !== '' &&
    !SUPPORTED_DATA_TYPES.includes(candidate.type as string)
  ) {
    return false
  }
  const isManagedV2 = candidate.type === 'sub2api-account-data'
  const expectedVersion = isManagedV2 ? 2 : 1
  if (candidate.version !== undefined && candidate.version !== 0 && candidate.version !== expectedVersion) {
    return false
  }
  return Array.isArray(candidate.proxies) && Array.isArray(candidate.accounts)
}

const hasEmbeddedProxyBindings = (payload: AdminDataPayload) => payload.proxies.length > 0
  || (payload.managed_proxy_providers?.length || 0) > 0
  || payload.accounts.some((item) => !!item.proxy_key || !!item.managed_proxy)

const validateSelectedFiles = async (selected: File[], generation: number) => {
  const payloads: AdminDataPayload[] = []
  for (const sourceFile of selected) {
    try {
      const parsed: unknown = JSON.parse(await readFileAsText(sourceFile))
      if (generation !== fileValidationGeneration) return
      if (!isValidDataPayload(parsed)) throw new Error('invalid')
      payloads.push(parsed)
    } catch {
      if (generation !== fileValidationGeneration) return
      filesValidated.value = false
      appStore.showError(t('admin.accounts.dataImportInvalidFile', { name: sourceFile.name }))
      return
    }
  }
  if (generation !== fileValidationGeneration) return
  const merged = mergeDataPayloads(payloads)
  filesValidated.value = true
  parsedAccountCount.value = merged.accounts.length
  hasFileProxies.value = hasEmbeddedProxyBindings(merged)
  if (hasFileProxies.value) {
    proxyMode.value = 'file'
  } else if (activeManagedConfigs.value.length > 0) {
    proxyMode.value = 'managed'
  } else {
    proxyMode.value = 'none'
  }
}

const mergeDataPayloads = (payloads: AdminDataPayload[]): AdminDataPayload => {
  const [firstPayload] = payloads
  if (payloads.length === 1 && firstPayload) return firstPayload

  // Keep every provider declaration. The backend compares duplicate keys
  // structurally and rejects conflicting definitions before restoring any.
  const managedProviders = payloads.flatMap((item) => item.managed_proxy_providers || [])
  const hasManagedV2 = payloads.some((item) => item.type === 'sub2api-account-data')
  return {
    type: hasManagedV2 ? 'sub2api-account-data' : payloads.find((item) => typeof item.type === 'string')?.type,
    version: hasManagedV2 ? 2 : payloads.find((item) => typeof item.version === 'number')?.version,
    exported_at: new Date().toISOString(),
    proxies: payloads.flatMap((item) => item.proxies),
    accounts: payloads.flatMap((item) => item.accounts),
    managed_proxy_providers: managedProviders.length ? managedProviders : undefined,
    skipped_shadows: payloads.reduce((sum, item) => {
      const count = Number(item.skipped_shadows || 0)
      return Number.isFinite(count) ? sum + count : sum
    }, 0)
  }
}

const handleImport = async () => {
  if (files.value.length === 0) {
    appStore.showError(t('admin.accounts.dataImportSelectFile'))
    return
  }
  if (validatingFiles.value || !filesValidated.value) return

  importing.value = true
  try {
    const dataPayloads: AdminDataPayload[] = []
    for (const sourceFile of files.value) {
      let parsed: unknown
      try {
        parsed = JSON.parse(await readFileAsText(sourceFile))
      } catch {
        appStore.showError(
          t('admin.accounts.dataImportParseFailedFile', { name: sourceFile.name })
        )
        return
      }
      if (!isValidDataPayload(parsed)) {
        appStore.showError(t('admin.accounts.dataImportInvalidFile', { name: sourceFile.name }))
        return
      }
      dataPayloads.push(parsed)
    }
    let dataPayload = mergeDataPayloads(dataPayloads)
    if (hasEmbeddedProxyBindings(dataPayload)) {
      proxyMode.value = 'file'
    }
    if (proxyMode.value === 'none') {
      dataPayload = {
        ...dataPayload,
        proxies: [],
        managed_proxy_providers: undefined,
        accounts: dataPayload.accounts.map((item) => ({ ...item, proxy_key: null, managed_proxy: undefined }))
      }
    }
    if (proxyMode.value === 'managed' && !managedProviderId.value && !dataPayload.accounts.some((item) => item.managed_proxy)) {
      appStore.showError(t('admin.accounts.managedProxyImport.providerRequired'))
      return
    }

    const res = await adminAPI.accounts.importData({
      data: dataPayload,
      skip_default_group_bind: true,
      managed_proxy_assignment: proxyMode.value === 'managed' && managedProviderId.value
        ? {
            provider_config_id: managedProviderId.value,
            country: managedTarget.country || null,
            state: managedTarget.state || null,
            city: managedTarget.city || null,
            strict: managedTarget.country ? managedTarget.strict : undefined
          }
        : undefined
    })

    result.value = res

    const msgParams: Record<string, unknown> = {
      account_created: res.account_created,
      account_failed: res.account_failed,
      proxy_created: res.proxy_created,
      proxy_reused: res.proxy_reused,
      proxy_failed: res.proxy_failed,
    }
    if (res.account_failed > 0 || res.proxy_failed > 0) {
      // 部分成功也创建了数据;弹窗关闭时通过 imported 通知父组件刷新列表
      if (res.account_created > 0 || res.proxy_created > 0) {
        hasCreatedData.value = true
      }
      appStore.showError(t('admin.accounts.dataImportCompletedWithErrors', msgParams))
    } else {
      appStore.showSuccess(t('admin.accounts.dataImportSuccess', msgParams))
      emit('imported')
    }
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.accounts.dataImportFailed'))
  } finally {
    importing.value = false
  }
}
</script>
