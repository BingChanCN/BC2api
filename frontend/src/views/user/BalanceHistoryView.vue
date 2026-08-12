<template>
  <AppLayout>
    <div class="mx-auto max-w-3xl space-y-6">
      <div class="flex flex-wrap items-center justify-between gap-4">
        <div>
          <h1 class="text-2xl font-bold text-gray-900 dark:text-white">
            {{ t('balanceHistory.title') }}
          </h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
            {{ t('balanceHistory.description') }}
          </p>
        </div>
        <select
          v-model="typeFilter"
          class="input w-auto min-w-[10rem]"
          data-testid="balance-history-type-filter"
          @change="loadHistory(1)"
        >
          <option v-for="opt in typeOptions" :key="opt.value" :value="opt.value">
            {{ opt.label }}
          </option>
        </select>
      </div>

      <div v-if="loading" class="card p-8 text-center text-sm text-gray-500">
        {{ t('common.loading') }}
      </div>

      <div v-else-if="items.length === 0" class="card p-8 text-center">
        <p class="text-sm text-gray-500 dark:text-gray-400">
          {{ t('balanceHistory.empty') }}
        </p>
      </div>

      <div v-else class="card overflow-hidden">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-gray-100 text-left text-xs uppercase tracking-wide text-gray-400 dark:border-dark-700 dark:text-gray-500">
              <th class="px-6 py-3 font-medium">{{ t('balanceHistory.columns.time') }}</th>
              <th class="px-6 py-3 font-medium">{{ t('balanceHistory.columns.type') }}</th>
              <th class="px-6 py-3 text-right font-medium">{{ t('balanceHistory.columns.amount') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="item in items"
              :key="item.id"
              class="border-b border-gray-50 last:border-0 dark:border-dark-800"
            >
              <td class="px-6 py-3 text-gray-600 dark:text-gray-300">
                {{ formatTime(item.used_at || item.created_at) }}
              </td>
              <td class="px-6 py-3 text-gray-700 dark:text-gray-200">
                {{ typeLabel(item) }}
              </td>
              <td
                class="px-6 py-3 text-right font-semibold"
                :class="item.value >= 0 ? 'text-emerald-600 dark:text-emerald-400' : 'text-red-600 dark:text-red-400'"
              >
                {{ formatValue(item) }}
              </td>
            </tr>
          </tbody>
        </table>

        <div v-if="pages > 1" class="flex items-center justify-between border-t border-gray-100 px-6 py-3 dark:border-dark-700">
          <button
            class="btn btn-sm"
            :disabled="page <= 1"
            data-testid="balance-history-prev"
            @click="loadHistory(page - 1)"
          >
            {{ t('balanceHistory.previous') }}
          </button>
          <span class="text-xs text-gray-500">{{ page }} / {{ pages }}</span>
          <button
            class="btn btn-sm"
            :disabled="page >= pages"
            data-testid="balance-history-next"
            @click="loadHistory(page + 1)"
          >
            {{ t('balanceHistory.next') }}
          </button>
        </div>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import { userAPI, type UserBalanceHistoryItem } from '@/api/user'
import { useAppStore } from '@/stores/app'

const { t } = useI18n()
const appStore = useAppStore()

const items = ref<UserBalanceHistoryItem[]>([])
const total = ref(0)
const page = ref(1)
const pages = ref(1)
const pageSize = 20
const typeFilter = ref('')
const loading = ref(false)

const typeOptions = computed(() => [
  { value: '', label: t('balanceHistory.filters.all') },
  { value: 'game', label: t('balanceHistory.filters.game') },
  { value: 'balance', label: t('balanceHistory.filters.redeem') },
  { value: 'affiliate_balance', label: t('balanceHistory.filters.affiliate') },
  { value: 'admin_balance', label: t('balanceHistory.filters.admin') },
  { value: 'concurrency', label: t('balanceHistory.filters.concurrency') },
  { value: 'subscription', label: t('balanceHistory.filters.subscription') }
])

const loadHistory = async (targetPage: number) => {
  loading.value = true
  try {
    const res = await userAPI.getMyBalanceHistory(targetPage, pageSize, typeFilter.value || undefined)
    items.value = res.items ?? []
    total.value = res.total ?? 0
    page.value = res.page ?? targetPage
    pages.value = res.pages ?? 1
  } catch (error: any) {
    appStore.showError(error.response?.data?.message || t('balanceHistory.loadFailed'))
  } finally {
    loading.value = false
  }
}

const formatTime = (raw: string) => {
  const date = new Date(raw)
  if (Number.isNaN(date.getTime())) return raw
  return date.toLocaleString()
}

const formatValue = (item: UserBalanceHistoryItem) => {
  const sign = item.value >= 0 ? '+' : ''
  return `${sign}$${item.value.toFixed(2)}`
}

const typeLabel = (item: UserBalanceHistoryItem) => {
  switch (item.type) {
    case 'balance':
      return t('redeem.balanceAddedRedeem')
    case 'affiliate_balance':
      return t('redeem.balanceAddedAffiliate')
    case 'admin_balance':
      return item.value >= 0 ? t('redeem.balanceAddedAdmin') : t('redeem.balanceDeductedAdmin')
    case 'game':
      return item.value >= 0 ? t('redeem.balanceAddedGame') : t('redeem.balanceDeductedGame')
    case 'concurrency':
      return t('redeem.concurrencyAddedRedeem')
    case 'admin_concurrency':
      return item.value >= 0 ? t('redeem.concurrencyAddedAdmin') : t('redeem.concurrencyReducedAdmin')
    case 'subscription':
      return t('redeem.subscriptionAssigned')
    default:
      return item.type
  }
}

onMounted(() => loadHistory(1))
</script>
