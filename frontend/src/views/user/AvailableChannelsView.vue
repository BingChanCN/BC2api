<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="flex flex-col justify-between gap-4 lg:flex-row lg:items-start">
          <div class="flex flex-1 flex-wrap items-center gap-3">
            <div class="relative w-full sm:w-80">
              <Icon
                name="search"
                size="md"
                class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400 dark:text-gray-500"
              />
              <input
                v-model="searchQuery"
                type="text"
                :placeholder="t('availableChannels.searchPlaceholder')"
                class="input pl-10"
              />
            </div>
          </div>

          <div class="flex w-full flex-shrink-0 flex-wrap items-center justify-end gap-3 lg:w-auto">
            <button
              @click="loadChannels"
              :disabled="loading"
              class="btn btn-secondary"
              :title="t('common.refresh', 'Refresh')"
            >
              <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
            </button>
          </div>
        </div>
      </template>

      <template #table>
        <div class="space-y-4">
          <AvailableChannelsTable
            :columns="columnLabels"
            :rows="filteredChannels"
            :loading="loading"
            :user-group-rates="userGroupRates"
            pricing-key-prefix="availableChannels.pricing"
            :no-pricing-label="t('availableChannels.noPricing')"
            :no-models-label="t('availableChannels.noModels')"
            :empty-label="t('availableChannels.empty')"
          />

          <section class="card overflow-hidden">
            <div class="border-b border-gray-200 px-4 py-3 dark:border-dark-700">
              <h2 class="text-base font-semibold text-gray-900 dark:text-white">
                {{ t('availableChannels.rateCeilingTitle') }}
              </h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">
                {{ t('availableChannels.rateCeilingDesc') }}
              </p>
            </div>

            <div v-if="loading && flatGroups.length === 0" class="flex justify-center p-8">
              <Icon name="refresh" size="lg" class="animate-spin text-gray-400" />
            </div>
            <div
              v-else-if="flatGroups.length === 0"
              class="p-8 text-center text-sm text-gray-500 dark:text-dark-400"
            >
              {{ t('availableChannels.empty') }}
            </div>
            <div v-else class="overflow-x-auto">
              <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
                <thead class="bg-gray-50 dark:bg-dark-800/60">
                  <tr>
                    <th class="px-4 py-3 text-left font-medium text-gray-500">
                      {{ t('availableChannels.columns.groups') }}
                    </th>
                    <th class="px-4 py-3 text-left font-medium text-gray-500">
                      {{ t('availableChannels.columns.platform') }}
                    </th>
                    <th class="px-4 py-3 text-left font-medium text-gray-500">
                      {{ t('availableChannels.currentRate') }}
                    </th>
                    <th class="px-4 py-3 text-left font-medium text-gray-500">
                      {{ t('availableChannels.rateCeiling') }}
                    </th>
                    <th class="px-4 py-3 text-left font-medium text-gray-500">
                      {{ t('common.actions') }}
                    </th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-gray-200 dark:divide-dark-700">
                  <tr v-for="group in flatGroups" :key="group.id">
                    <td class="px-4 py-3 font-medium text-gray-900 dark:text-white">
                      {{ group.name }}
                    </td>
                    <td class="px-4 py-3 uppercase text-gray-600 dark:text-dark-300">
                      {{ group.platform }}
                    </td>
                    <td class="px-4 py-3 text-gray-700 dark:text-dark-200">
                      ×{{ formatRate(effectiveRate(group)) }}
                    </td>
                    <td class="px-4 py-3">
                      <input
                        v-model="ceilingDrafts[group.id]"
                        type="number"
                        min="0"
                        step="0.01"
                        class="input h-9 w-28"
                        :placeholder="t('availableChannels.rateCeilingUnlimited')"
                        :disabled="savingGroupId === group.id"
                      />
                    </td>
                    <td class="px-4 py-3">
                      <div class="flex flex-wrap gap-2">
                        <button
                          type="button"
                          class="btn btn-primary btn-sm"
                          :disabled="savingGroupId === group.id"
                          @click="saveCeiling(group.id)"
                        >
                          {{ t('common.save') }}
                        </button>
                        <button
                          type="button"
                          class="btn btn-secondary btn-sm"
                          :disabled="savingGroupId === group.id || ceilingDrafts[group.id] === ''"
                          @click="clearCeiling(group.id)"
                        >
                          {{ t('availableChannels.clearCeiling') }}
                        </button>
                      </div>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </section>
        </div>
      </template>
    </TablePageLayout>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import AvailableChannelsTable from '@/components/channels/AvailableChannelsTable.vue'
import userChannelsAPI, { type UserAvailableChannel, type UserAvailableGroup } from '@/api/channels'
import userGroupsAPI from '@/api/groups'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const appStore = useAppStore()

const channels = ref<UserAvailableChannel[]>([])
const userGroupRates = ref<Record<number, number>>({})
const userGroupCeilings = ref<Record<number, number>>({})
const ceilingDrafts = reactive<Record<number, string>>({})
const loading = ref(false)
const savingGroupId = ref<number | null>(null)
const searchQuery = ref('')

const columnLabels = computed(() => ({
  name: t('availableChannels.columns.name'),
  description: t('availableChannels.columns.description'),
  platform: t('availableChannels.columns.platform'),
  groups: t('availableChannels.columns.groups'),
  supportedModels: t('availableChannels.columns.supportedModels'),
}))

const filteredChannels = computed(() => {
  const q = searchQuery.value.trim().toLowerCase()
  if (!q) return channels.value
  return channels.value
    .map((ch) => {
      const nameHit = ch.name.toLowerCase().includes(q)
      const descHit = (ch.description || '').toLowerCase().includes(q)
      if (nameHit || descHit) return ch
      const matchingSections = ch.platforms.filter(
        (p) =>
          p.platform.toLowerCase().includes(q) ||
          p.groups.some((g) => g.name.toLowerCase().includes(q)) ||
          p.supported_models.some((m) => m.name.toLowerCase().includes(q)),
      )
      if (matchingSections.length === 0) return null
      return { ...ch, platforms: matchingSections }
    })
    .filter((ch): ch is UserAvailableChannel => ch !== null)
})

const flatGroups = computed(() => {
  const map = new Map<number, UserAvailableGroup>()
  for (const channel of channels.value) {
    for (const platform of channel.platforms) {
      for (const group of platform.groups) {
        if (!map.has(group.id)) {
          map.set(group.id, group)
        }
      }
    }
  }
  return Array.from(map.values()).sort((a, b) => a.name.localeCompare(b.name))
})

function formatRate(value: number): string {
  return Number.isFinite(value) ? value.toFixed(2).replace(/\.?0+$/, '') : '1'
}

function effectiveRate(group: UserAvailableGroup): number {
  const override = userGroupRates.value[group.id]
  return typeof override === 'number' ? override : group.rate_multiplier
}

function syncCeilingDrafts(ceilings: Record<number, number>) {
  for (const key of Object.keys(ceilingDrafts)) {
    delete ceilingDrafts[Number(key)]
  }
  for (const group of flatGroups.value) {
    const value = ceilings[group.id]
    ceilingDrafts[group.id] = typeof value === 'number' ? String(value) : ''
  }
}

async function loadChannels() {
  loading.value = true
  try {
    const [list, rates, ceilings] = await Promise.all([
      userChannelsAPI.getAvailable(),
      userGroupsAPI.getUserGroupRates().catch((err: unknown) => {
        console.error('Failed to load user group rates:', err)
        return {} as Record<number, number>
      }),
      userGroupsAPI.getUserGroupRateCeilings().catch((err: unknown) => {
        console.error('Failed to load user group rate ceilings:', err)
        return {} as Record<number, number>
      }),
    ])
    channels.value = list
    userGroupRates.value = rates
    userGroupCeilings.value = ceilings
    syncCeilingDrafts(ceilings)
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    loading.value = false
  }
}

async function saveCeiling(groupId: number) {
  const raw = (ceilingDrafts[groupId] ?? '').trim()
  let value: number | null = null
  if (raw !== '') {
    value = Number(raw)
    if (!Number.isFinite(value) || value <= 0) {
      appStore.showError(t('availableChannels.rateCeilingInvalid'))
      return
    }
  }
  savingGroupId.value = groupId
  try {
    await userGroupsAPI.setUserGroupRateCeiling(groupId, value)
    if (value === null) {
      const next = { ...userGroupCeilings.value }
      delete next[groupId]
      userGroupCeilings.value = next
      ceilingDrafts[groupId] = ''
    } else {
      userGroupCeilings.value = { ...userGroupCeilings.value, [groupId]: value }
      ceilingDrafts[groupId] = String(value)
    }
    appStore.showSuccess(t('availableChannels.rateCeilingSaved'))
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    savingGroupId.value = null
  }
}

async function clearCeiling(groupId: number) {
  ceilingDrafts[groupId] = ''
  await saveCeiling(groupId)
}

onMounted(loadChannels)
</script>
