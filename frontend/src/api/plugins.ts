import { apiClient } from './client'

export type PluginVisibility = 'user' | 'admin'

export interface PublicPlugin {
  id: string
  name: string
  version: string
  description?: string
  visibility: PluginVisibility
  sort_order: number
  has_api: boolean
  ui_path: string
}

export interface PluginInvokeRequest {
  method: string
  path: string
  query?: string
  content_type?: string
  accept?: string
  idempotency_key?: string
  body?: string
}

export interface PluginInvokeResponse {
  status: number
  content_type?: string
  encoding: 'utf8' | 'base64'
  body: string
}

export interface PluginStatus extends PublicPlugin {
  runtime: 'static' | 'proxy'
}

export interface PluginRegistryStatus {
  enabled: boolean
  last_refresh: string
  plugins: PluginStatus[]
  errors: Record<string, string>
}

export async function listPlugins(): Promise<PublicPlugin[]> {
  const { data } = await apiClient.get<PublicPlugin[]>('/plugins')
  return data
}

export async function invokePlugin(
  pluginId: string,
  request: PluginInvokeRequest
): Promise<PluginInvokeResponse> {
  const { data } = await apiClient.post<PluginInvokeResponse>(
    `/plugins/${encodeURIComponent(pluginId)}/invoke`,
    request
  )
  return data
}

export async function getPluginRegistryStatus(): Promise<PluginRegistryStatus> {
  const { data } = await apiClient.get<PluginRegistryStatus>('/admin/plugins')
  return data
}

export async function refreshPluginRegistry(): Promise<PluginRegistryStatus> {
  const { data } = await apiClient.post<PluginRegistryStatus>('/admin/plugins/refresh')
  return data
}

export const pluginsAPI = {
  list: listPlugins,
  invoke: invokePlugin,
  getRegistryStatus: getPluginRegistryStatus,
  refreshRegistry: refreshPluginRegistry,
}
