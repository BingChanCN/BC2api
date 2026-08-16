import { apiClient } from '../client'

export type CatProxyProviderStatus = 'active' | 'retiring' | 'disabled' | 'credential_error'
export type CatProxyProtocol = 'http' | 'socks5h'

export interface CatProxyProviderConfig {
  id: number
  name: string
  provider_type: 'catproxies'
  status: CatProxyProviderStatus
  is_default: boolean
  protocol: CatProxyProtocol
  host: string
  base_username: string
  password_configured: boolean
  default_country?: string | null
  default_state?: string | null
  default_city?: string | null
  lifetime_minutes: number
  strict: boolean
  last_probe_at?: string
  last_probe_latency_ms?: number
  last_error?: string | null
  last_error_at?: string | null
  created_at: string
  updated_at: string
}

export interface CatProxyConfigInput {
  name: string
  is_default: boolean
  protocol: CatProxyProtocol
  host: string
  base_username: string
  password?: string
  default_country?: string | null
  default_state?: string | null
  default_city?: string | null
  lifetime_minutes: number
  strict: boolean
}

export interface CatProxyTargeting {
  countries: Array<{ code: string; name: string }>
  us_states: string[]
  cities: string[]
}

export interface ManagedProxyLease {
  id: number
  account_id: number
  proxy_id: number
  provider_config_id: number
  target_country?: string | null
  target_state?: string | null
  target_city?: string | null
  strict: boolean
  lifetime_minutes: number
  state: string
  health_status: string
  health_checked_at?: string | null
  observed_exit_ip?: string | null
  observed_country?: string | null
  observed_state?: string | null
  observed_city?: string | null
  observed_latency_ms?: number | null
  activated_at?: string | null
  last_rotated_at?: string | null
  next_rotation_at?: string | null
  expires_at?: string | null
  failure_count: number
  consecutive_failure_count: number
  last_error?: string | null
  last_error_at?: string | null
  created_at: string
  updated_at: string
}

export interface ManagedProxyAccount {
  lease: ManagedProxyLease
  account_name: string
  platform: string
  schedulable: boolean
  managed_proxy_ready: boolean
  provider_name: string
}

export interface CatProxyProbeResult {
  exit_ip?: string | null
  country?: string | null
  state?: string | null
  city?: string | null
  latency_ms: number
  checked_at: string
  strict_match: boolean
}

export interface ManagedProxyTargetInput {
  provider_config_id: number
  country?: string | null
  state?: string | null
  city?: string | null
  strict?: boolean
}

const listConfigs = async (): Promise<CatProxyProviderConfig[]> =>
  (await apiClient.get<CatProxyProviderConfig[]>('/admin/catproxies/configs')).data

const createConfig = async (payload: CatProxyConfigInput): Promise<CatProxyProviderConfig> =>
  (await apiClient.post<CatProxyProviderConfig>('/admin/catproxies/configs', payload)).data

const updateConfig = async (id: number, payload: CatProxyConfigInput): Promise<CatProxyProviderConfig> =>
  (await apiClient.put<CatProxyProviderConfig>(`/admin/catproxies/configs/${id}`, payload)).data

const updateConfigStatus = async (id: number, status: CatProxyProviderStatus): Promise<CatProxyProviderConfig> =>
  (await apiClient.put<CatProxyProviderConfig>(`/admin/catproxies/configs/${id}/status`, { status })).data

const deleteConfig = async (id: number): Promise<void> => {
  await apiClient.delete(`/admin/catproxies/configs/${id}`)
}

const testConfig = async (id: number): Promise<CatProxyProbeResult> =>
  (await apiClient.post<CatProxyProbeResult>(`/admin/catproxies/configs/${id}/test`)).data

const getTargeting = async (country = '', state = ''): Promise<CatProxyTargeting> =>
  (await apiClient.get<CatProxyTargeting>('/admin/catproxies/targeting', { params: { country, state } })).data

const listManaged = async (): Promise<ManagedProxyAccount[]> =>
  (await apiClient.get<ManagedProxyAccount[]>('/admin/managed-proxies')).data

const manageAccount = async (accountId: number, payload: ManagedProxyTargetInput): Promise<ManagedProxyAccount> =>
  (await apiClient.post<ManagedProxyAccount>(`/admin/accounts/${accountId}/managed-proxy`, payload)).data

const rotateAccount = async (accountId: number): Promise<ManagedProxyAccount> =>
  (await apiClient.post<ManagedProxyAccount>(`/admin/accounts/${accountId}/managed-proxy/rotate`)).data

const migrateAccount = async (accountId: number, providerConfigId: number): Promise<ManagedProxyAccount> =>
  (await apiClient.post<ManagedProxyAccount>(`/admin/accounts/${accountId}/managed-proxy/migrate`, { provider_config_id: providerConfigId })).data

const releaseAccount = async (accountId: number): Promise<void> => {
  await apiClient.delete(`/admin/accounts/${accountId}/managed-proxy`)
}

export const catproxiesAPI = {
  listConfigs,
  createConfig,
  updateConfig,
  updateConfigStatus,
  deleteConfig,
  testConfig,
  getTargeting,
  listManaged,
  manageAccount,
  rotateAccount,
  migrateAccount,
  releaseAccount
}

export default catproxiesAPI
