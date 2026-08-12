/**
 * User Groups API endpoints (non-admin)
 * Handles group-related operations for regular users
 */

import { apiClient } from './client'
import type { Group } from '@/types'

/**
 * Get available groups that the current user can bind to API keys
 * This returns groups based on user's permissions:
 * - Standard groups: public (non-exclusive) or explicitly allowed
 * - Subscription groups: user has active subscription
 * @returns List of available groups
 */
export async function getAvailable(): Promise<Group[]> {
  const { data } = await apiClient.get<Group[]>('/groups/available')
  return data
}

/**
 * Get current user's custom group rate multipliers
 * @returns Map of group_id to custom rate_multiplier
 */
export async function getUserGroupRates(): Promise<Record<number, number>> {
  const { data } = await apiClient.get<Record<number, number> | null>('/groups/rates')
  return data || {}
}

/**
 * Get current user's self-set rate ceilings per group/channel.
 */
export async function getUserGroupRateCeilings(): Promise<Record<number, number>> {
  const { data } = await apiClient.get<Record<number, number> | null>('/groups/rate-ceilings')
  return data || {}
}

/**
 * Set or clear the rate ceiling for one available group/channel.
 * Pass null to clear (no limit).
 */
export async function setUserGroupRateCeiling(
  groupId: number,
  rateCeiling: number | null
): Promise<void> {
  await apiClient.put(`/groups/${groupId}/rate-ceiling`, {
    rate_ceiling: rateCeiling,
  })
}

export const userGroupsAPI = {
  getAvailable,
  getUserGroupRates,
  getUserGroupRateCeilings,
  setUserGroupRateCeiling,
}

export default userGroupsAPI
