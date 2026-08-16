<template>
  <TablePageLayout>
    <template #filters>
      <div class="flex flex-wrap items-center gap-3">
        <div class="relative w-full sm:w-64">
          <Icon name="search" size="md" class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
          <input v-model="search" class="input pl-10" :placeholder="t('admin.proxies.managed.search')" />
        </div>
        <select v-model="statusFilter" class="input w-full sm:w-44">
          <option value="">{{ t('admin.proxies.managed.allStatuses') }}</option>
          <option v-for="status in statuses" :key="status" :value="status">{{ statusLabel(status) }}</option>
        </select>
        <div class="flex flex-1 justify-end gap-2">
          <button class="btn btn-secondary" :disabled="loading" :title="t('common.refresh')" @click="load">
            <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
          </button>
          <button class="btn btn-primary" @click="openCreate">
            <Icon name="plus" size="md" class="mr-2" />
            {{ t('admin.proxies.managed.create') }}
          </button>
        </div>
      </div>
    </template>

    <template #table>
      <div class="min-h-0 flex-1 overflow-auto border-y border-gray-200 dark:border-dark-700">
        <div class="divide-y divide-gray-200 bg-white dark:divide-dark-700 dark:bg-dark-900 md:hidden">
          <div v-if="loading" class="px-4 py-12 text-center text-sm text-gray-500">{{ t('common.loading') }}</div>
          <div v-else-if="filteredConfigs.length === 0" class="px-4 py-12 text-center text-sm text-gray-500">{{ t('admin.proxies.managed.empty') }}</div>
          <article v-for="config in filteredConfigs" v-else :key="`mobile-${config.id}`" class="space-y-4 px-4 py-5">
            <div class="flex items-start justify-between gap-3">
              <div class="min-w-0">
                <div class="break-words font-medium text-gray-900 dark:text-white">{{ config.name }}</div>
                <div v-if="config.is_default" class="mt-1 text-xs text-primary-600">{{ t('admin.proxies.managed.default') }}</div>
              </div>
              <select class="input w-32 shrink-0 py-1 text-xs" :value="config.status" :disabled="busyId === config.id" @change="changeStatus(config, $event)">
                <option v-for="status in statuses" :key="status" :value="status">{{ statusLabel(status) }}</option>
              </select>
            </div>
            <div v-if="config.last_error" class="rounded bg-red-50 px-3 py-2 text-xs text-red-700 dark:bg-red-900/20 dark:text-red-300">{{ config.last_error }}</div>
            <dl class="grid grid-cols-2 gap-x-4 gap-y-3 text-xs">
              <div class="min-w-0">
                <dt class="text-gray-500 dark:text-dark-400">{{ t('admin.proxies.managed.columns.endpoint') }}</dt>
                <dd class="mt-1 break-all font-mono text-gray-800 dark:text-dark-100">{{ config.host }}:{{ protocolPort(config.protocol) }}</dd>
                <span class="badge badge-gray mt-1">{{ config.protocol.toUpperCase() }}</span>
              </div>
              <div class="min-w-0">
                <dt class="text-gray-500 dark:text-dark-400">{{ t('admin.proxies.managed.columns.target') }}</dt>
                <dd class="mt-1 break-words text-gray-800 dark:text-dark-100">{{ targetLabel(config) }}</dd>
              </div>
              <div>
                <dt class="text-gray-500 dark:text-dark-400">{{ t('admin.proxies.managed.columns.lifetime') }}</dt>
                <dd class="mt-1 text-gray-800 dark:text-dark-100">{{ config.lifetime_minutes }} min</dd>
              </div>
              <div>
                <dt class="text-gray-500 dark:text-dark-400">{{ t('admin.proxies.managed.columns.lastProbe') }}</dt>
                <dd class="mt-1 text-gray-800 dark:text-dark-100">{{ formatTime(config.last_probe_at) }}</dd>
                <dd v-if="config.last_probe_latency_ms != null" class="mt-1 text-gray-600 dark:text-dark-300">{{ config.last_probe_latency_ms }} ms</dd>
              </div>
            </dl>
            <div class="flex items-center justify-between border-t border-gray-100 pt-3 dark:border-dark-700">
              <button class="text-xs font-medium text-primary-600 hover:underline" @click="openDetails(config)">
                {{ t('admin.proxies.managed.columns.accounts') }} {{ leasesFor(config.id).length }} ·
                <span class="text-green-600">{{ healthyCount(config.id) }}</span>/<span :class="unhealthyCount(config.id) ? 'text-red-600' : 'text-gray-500'">{{ unhealthyCount(config.id) }}</span>
              </button>
              <div class="flex gap-1">
                <button class="rounded p-2 text-gray-500 hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700" :title="t('admin.proxies.managed.details')" @click="openDetails(config)"><Icon name="eye" size="sm" /></button>
                <button class="rounded p-2 text-gray-500 hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700" :disabled="busyId === config.id" :title="t('admin.proxies.managed.test')" @click="testConfig(config)"><Icon name="play" size="sm" /></button>
                <button class="rounded p-2 text-gray-500 hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700" :title="t('common.edit')" @click="openEdit(config)"><Icon name="edit" size="sm" /></button>
                <button class="rounded p-2 text-gray-500 hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20" :title="t('common.delete')" @click="pendingDelete = config"><Icon name="trash" size="sm" /></button>
              </div>
            </div>
          </article>
        </div>
        <div class="hidden md:block">
          <table class="min-w-[1100px] divide-y divide-gray-200 text-sm dark:divide-dark-700">
          <thead class="sticky top-0 z-10 bg-gray-50 text-xs text-gray-500 dark:bg-dark-800 dark:text-dark-400">
            <tr>
              <th class="px-4 py-3 text-left">{{ t('admin.proxies.managed.columns.name') }}</th>
              <th class="px-4 py-3 text-left">{{ t('admin.proxies.managed.columns.status') }}</th>
              <th class="px-4 py-3 text-left">{{ t('admin.proxies.managed.columns.endpoint') }}</th>
              <th class="px-4 py-3 text-left">{{ t('admin.proxies.managed.columns.target') }}</th>
              <th class="px-4 py-3 text-left">{{ t('admin.proxies.managed.columns.lifetime') }}</th>
              <th class="px-4 py-3 text-left">{{ t('admin.proxies.managed.columns.lastProbe') }}</th>
              <th class="px-4 py-3 text-left">{{ t('admin.proxies.managed.columns.accounts') }}</th>
              <th class="px-4 py-3 text-left">{{ t('admin.proxies.managed.columns.health') }}</th>
              <th class="sticky right-0 bg-gray-50 px-4 py-3 text-right shadow-[-8px_0_12px_-12px_rgba(15,23,42,0.45)] dark:bg-dark-800">{{ t('common.actions') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-200 bg-white dark:divide-dark-700 dark:bg-dark-900">
            <tr v-if="loading">
              <td colspan="9" class="px-4 py-12 text-center text-gray-500">{{ t('common.loading') }}</td>
            </tr>
            <tr v-else-if="filteredConfigs.length === 0">
              <td colspan="9" class="px-4 py-12 text-center text-gray-500">{{ t('admin.proxies.managed.empty') }}</td>
            </tr>
            <tr v-for="config in filteredConfigs" v-else :key="config.id" class="hover:bg-gray-50 dark:hover:bg-dark-800/60">
              <td class="px-4 py-3">
                <div class="font-medium text-gray-900 dark:text-white">{{ config.name }}</div>
                <div v-if="config.is_default" class="mt-1 text-xs text-primary-600">{{ t('admin.proxies.managed.default') }}</div>
              </td>
              <td class="px-4 py-3">
                <select class="input min-w-32 py-1 text-xs" :value="config.status" :disabled="busyId === config.id" @change="changeStatus(config, $event)">
                  <option v-for="status in statuses" :key="status" :value="status">{{ statusLabel(status) }}</option>
                </select>
                <div v-if="config.last_error" class="mt-1 max-w-52 truncate text-xs text-red-500" :title="config.last_error">{{ config.last_error }}</div>
              </td>
              <td class="px-4 py-3">
                <div class="font-mono text-xs text-gray-700 dark:text-dark-200">{{ config.host }}:{{ protocolPort(config.protocol) }}</div>
                <span class="badge badge-gray mt-1">{{ config.protocol.toUpperCase() }}</span>
              </td>
              <td class="px-4 py-3 text-xs text-gray-600 dark:text-dark-300">{{ targetLabel(config) }}</td>
              <td class="px-4 py-3 text-gray-700 dark:text-dark-200">{{ config.lifetime_minutes }} min</td>
              <td class="px-4 py-3 text-xs text-gray-600 dark:text-dark-300">
                <div>{{ formatTime(config.last_probe_at) }}</div>
                <div v-if="config.last_probe_latency_ms != null" class="mt-1">{{ config.last_probe_latency_ms }} ms</div>
              </td>
              <td class="px-4 py-3">
                <button class="font-medium text-primary-600 hover:underline" @click="openDetails(config)">{{ leasesFor(config.id).length }}</button>
              </td>
              <td class="px-4 py-3 text-xs">
                <span class="text-green-600">{{ healthyCount(config.id) }}</span>
                <span class="mx-1 text-gray-300">/</span>
                <span :class="unhealthyCount(config.id) ? 'text-red-600' : 'text-gray-500'">{{ unhealthyCount(config.id) }}</span>
              </td>
              <td class="sticky right-0 bg-white px-4 py-3 shadow-[-8px_0_12px_-12px_rgba(15,23,42,0.45)] dark:bg-dark-900">
                <div class="flex justify-end gap-1">
                  <button class="rounded p-2 text-gray-500 hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700" :title="t('admin.proxies.managed.details')" @click="openDetails(config)"><Icon name="eye" size="sm" /></button>
                  <button class="rounded p-2 text-gray-500 hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700" :disabled="busyId === config.id" :title="t('admin.proxies.managed.test')" @click="testConfig(config)"><Icon name="play" size="sm" /></button>
                  <button class="rounded p-2 text-gray-500 hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700" :title="t('common.edit')" @click="openEdit(config)"><Icon name="edit" size="sm" /></button>
                  <button class="rounded p-2 text-gray-500 hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20" :title="t('common.delete')" @click="pendingDelete = config"><Icon name="trash" size="sm" /></button>
                </div>
              </td>
            </tr>
          </tbody>
          </table>
        </div>
      </div>
    </template>
  </TablePageLayout>

  <BaseDialog :show="showEditor" :title="editing ? t('admin.proxies.managed.edit') : t('admin.proxies.managed.create')" width="normal" @close="closeEditor">
    <form id="catproxy-config-form" class="grid grid-cols-1 gap-4 sm:grid-cols-2" @submit.prevent="saveConfig">
      <div class="sm:col-span-2">
        <label class="input-label">{{ t('admin.proxies.managed.fields.name') }}</label>
        <input v-model.trim="form.name" class="input" required maxlength="100" />
      </div>
      <div>
        <label class="input-label">{{ t('admin.proxies.managed.fields.protocol') }}</label>
        <select v-model="form.protocol" class="input"><option value="http">HTTP</option><option value="socks5h">SOCKS5H</option></select>
      </div>
      <div>
        <label class="input-label">{{ t('admin.proxies.managed.fields.host') }}</label>
        <input v-model.trim="form.host" class="input" required maxlength="255" />
      </div>
      <div>
        <label class="input-label">{{ t('admin.proxies.managed.fields.username') }}</label>
        <input v-model.trim="form.base_username" class="input" required maxlength="255" autocomplete="off" />
      </div>
      <div>
        <label class="input-label">{{ t('admin.proxies.managed.fields.password') }}</label>
        <input v-model="form.password" class="input" :required="!editing" type="password" autocomplete="new-password" :placeholder="editing ? t('admin.proxies.managed.keepPassword') : ''" />
      </div>
      <div>
        <label class="input-label">{{ t('admin.proxies.managed.fields.country') }}</label>
        <select v-model="form.default_country" class="input" @change="onCountryChange"><option value="">{{ t('admin.proxies.managed.noTarget') }}</option><option v-for="country in targeting.countries" :key="country.code" :value="country.code">{{ country.name }}</option></select>
      </div>
      <div v-if="form.default_country === 'us'">
        <label class="input-label">{{ t('admin.proxies.managed.fields.state') }}</label>
        <select v-model="form.default_state" class="input" @change="loadCities"><option value="">{{ t('admin.proxies.managed.any') }}</option><option v-for="state in targeting.us_states" :key="state" :value="state">{{ state }}</option></select>
      </div>
      <div>
        <label class="input-label">{{ t('admin.proxies.managed.fields.city') }}</label>
        <select v-model="form.default_city" class="input" :disabled="!form.default_country"><option value="">{{ t('admin.proxies.managed.any') }}</option><option v-for="city in targeting.cities" :key="city" :value="city">{{ city }}</option></select>
      </div>
      <div>
        <label class="input-label">{{ t('admin.proxies.managed.fields.lifetime') }}</label>
        <input v-model.number="form.lifetime_minutes" class="input" type="number" min="15" max="1440" required />
      </div>
      <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-dark-200"><input v-model="form.is_default" type="checkbox" class="h-4 w-4 rounded border-gray-300 text-primary-600" :disabled="Boolean(editing && editing.status !== 'active')" />{{ t('admin.proxies.managed.fields.default') }}</label>
      <label v-if="form.default_country" class="flex items-center gap-2 text-sm text-gray-700 dark:text-dark-200"><input v-model="form.strict" type="checkbox" class="h-4 w-4 rounded border-gray-300 text-primary-600" />{{ t('admin.proxies.managed.fields.strict') }}</label>
    </form>
    <template #footer><div class="flex justify-end gap-3"><button class="btn btn-secondary" :disabled="saving" @click="closeEditor">{{ t('common.cancel') }}</button><button class="btn btn-primary" form="catproxy-config-form" type="submit" :disabled="saving">{{ saving ? t('common.saving') : t('common.save') }}</button></div></template>
  </BaseDialog>

  <BaseDialog :show="!!detailConfig" :title="detailConfig?.name || ''" width="wide" @close="detailConfig = null">
    <div class="max-h-[60vh] overflow-auto">
      <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
        <thead><tr class="text-left text-xs text-gray-500"><th class="px-3 py-2">{{ t('admin.proxies.managed.detail.account') }}</th><th class="px-3 py-2">IP</th><th class="px-3 py-2">{{ t('admin.proxies.managed.detail.region') }}</th><th class="px-3 py-2">{{ t('admin.proxies.managed.detail.latency') }}</th><th class="px-3 py-2">{{ t('admin.proxies.managed.detail.nextRotation') }}</th><th class="px-3 py-2">{{ t('admin.proxies.managed.columns.health') }}</th></tr></thead>
        <tbody class="divide-y divide-gray-200 dark:divide-dark-700"><tr v-for="item in detailLeases" :key="item.lease.account_id"><td class="px-3 py-2 font-medium">{{ item.account_name }}</td><td class="px-3 py-2 font-mono text-xs">{{ item.lease.observed_exit_ip || '-' }}</td><td class="px-3 py-2 text-xs">{{ observedTarget(item) }}</td><td class="px-3 py-2">{{ item.lease.observed_latency_ms ?? '-' }} ms</td><td class="px-3 py-2 text-xs">{{ formatTime(item.lease.next_rotation_at) }}</td><td class="px-3 py-2"><span :class="item.managed_proxy_ready ? 'text-green-600' : 'text-red-600'">{{ item.managed_proxy_ready ? t('admin.proxies.managed.ready') : t('admin.proxies.managed.blocked') }}</span></td></tr><tr v-if="detailLeases.length === 0"><td colspan="6" class="px-3 py-8 text-center text-gray-500">{{ t('admin.proxies.managed.noAccounts') }}</td></tr></tbody>
      </table>
    </div>
  </BaseDialog>

  <ConfirmDialog :show="!!pendingDelete" :title="t('admin.proxies.managed.deleteTitle')" :message="t('admin.proxies.managed.deleteMessage', { name: pendingDelete?.name || '' })" danger @confirm="deletePending" @cancel="pendingDelete = null" />
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { CatProxyConfigInput, CatProxyProviderConfig, CatProxyTargeting, ManagedProxyAccount } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Icon from '@/components/icons/Icon.vue'

const { t } = useI18n()
const appStore = useAppStore()
const statuses = ['active', 'retiring', 'disabled', 'credential_error'] as const
const configs = ref<CatProxyProviderConfig[]>([])
const managed = ref<ManagedProxyAccount[]>([])
const targeting = ref<CatProxyTargeting>({ countries: [], us_states: [], cities: [] })
const loading = ref(false)
const saving = ref(false)
const busyId = ref<number | null>(null)
const search = ref('')
const statusFilter = ref('')
const showEditor = ref(false)
const editing = ref<CatProxyProviderConfig | null>(null)
const detailConfig = ref<CatProxyProviderConfig | null>(null)
const pendingDelete = ref<CatProxyProviderConfig | null>(null)

const emptyForm = (): CatProxyConfigInput => ({ name: '', is_default: false, protocol: 'http', host: '', base_username: '', password: '', default_country: '', default_state: '', default_city: '', lifetime_minutes: 60, strict: true })
const form = reactive<CatProxyConfigInput>(emptyForm())
const filteredConfigs = computed(() => configs.value.filter((item) => (!statusFilter.value || item.status === statusFilter.value) && (!search.value.trim() || `${item.name} ${item.host}`.toLowerCase().includes(search.value.trim().toLowerCase()))))
const detailLeases = computed(() => detailConfig.value ? leasesFor(detailConfig.value.id) : [])

const leasesFor = (id: number) => managed.value.filter((item) => item.lease.provider_config_id === id)
const healthyCount = (id: number) => leasesFor(id).filter((item) => item.managed_proxy_ready && item.lease.health_status === 'healthy').length
const unhealthyCount = (id: number) => leasesFor(id).filter((item) => !item.managed_proxy_ready || item.lease.health_status === 'unhealthy').length
const protocolPort = (protocol: string) => protocol === 'socks5h' ? 12000 : 10000
const statusLabel = (status: string) => t(`admin.proxies.managed.status.${status}`)
const targetLabel = (config: CatProxyProviderConfig) => [config.default_country, config.default_state, config.default_city].filter(Boolean).join(' / ') || t('admin.proxies.managed.noTarget')
const observedTarget = (item: ManagedProxyAccount) => [item.lease.observed_country, item.lease.observed_state, item.lease.observed_city].filter(Boolean).join(' / ') || '-'
const formatTime = (value?: string | null) => value ? new Date(value).toLocaleString() : '-'

const load = async () => {
  loading.value = true
  try {
    const [nextConfigs, nextManaged, nextTargeting] = await Promise.all([adminAPI.catproxies.listConfigs(), adminAPI.catproxies.listManaged(), adminAPI.catproxies.getTargeting()])
    configs.value = nextConfigs
    managed.value = nextManaged
    targeting.value = nextTargeting
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.proxies.managed.loadFailed'))
  } finally { loading.value = false }
}

const openCreate = () => { editing.value = null; Object.assign(form, emptyForm()); showEditor.value = true }
const openEdit = async (config: CatProxyProviderConfig) => {
  editing.value = config
  Object.assign(form, { name: config.name, is_default: config.is_default, protocol: config.protocol, host: config.host, base_username: config.base_username, password: '', default_country: config.default_country || '', default_state: config.default_state || '', default_city: config.default_city || '', lifetime_minutes: config.lifetime_minutes, strict: config.strict })
  showEditor.value = true
  await loadCities()
}
const closeEditor = () => { if (!saving.value) showEditor.value = false }
const onCountryChange = async () => { form.default_state = ''; form.default_city = ''; await loadCities() }
const loadCities = async () => {
  const selectedCity = form.default_city
  targeting.value = await adminAPI.catproxies.getTargeting(form.default_country || '', form.default_state || '')
  form.default_city = selectedCity && targeting.value.cities.includes(selectedCity) ? selectedCity : ''
}

const saveConfig = async () => {
  saving.value = true
  try {
    const payload = { ...form, default_country: form.default_country || null, default_state: form.default_state || null, default_city: form.default_city || null }
    if (editing.value) {
      if (!payload.password) delete payload.password
      await adminAPI.catproxies.updateConfig(editing.value.id, payload)
    } else {
      await adminAPI.catproxies.createConfig(payload)
    }
    appStore.showSuccess(t('admin.proxies.managed.saved'))
    showEditor.value = false
    await load()
  } catch (error: any) { appStore.showError(error?.message || t('admin.proxies.managed.saveFailed')) } finally { saving.value = false }
}
const changeStatus = async (config: CatProxyProviderConfig, event: Event) => {
  busyId.value = config.id
  try { await adminAPI.catproxies.updateConfigStatus(config.id, (event.target as HTMLSelectElement).value as any); await load() } catch (error: any) { appStore.showError(error?.message || t('admin.proxies.managed.saveFailed')); await load() } finally { busyId.value = null }
}
const testConfig = async (config: CatProxyProviderConfig) => {
  busyId.value = config.id
  try { const result = await adminAPI.catproxies.testConfig(config.id); appStore.showSuccess(t('admin.proxies.managed.testSuccess', { ip: result.exit_ip || '-', latency: result.latency_ms })) } catch (error: any) { appStore.showError(error?.message || t('admin.proxies.managed.testFailed')) } finally { busyId.value = null }
}
const openDetails = (config: CatProxyProviderConfig) => { detailConfig.value = config }
const deletePending = async () => {
  if (!pendingDelete.value) return
  const id = pendingDelete.value.id
  pendingDelete.value = null
  try { await adminAPI.catproxies.deleteConfig(id); appStore.showSuccess(t('admin.proxies.managed.deleted')); await load() } catch (error: any) { appStore.showError(error?.message || t('admin.proxies.managed.deleteFailed')) }
}

onMounted(load)
</script>
