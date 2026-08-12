import type { PluginInvokeRequest, PluginInvokeResponse } from '@/api/plugins'

export const PLUGIN_BRIDGE_PROTOCOL = 'sub2api-plugin-v1'

const REQUEST_ID_PATTERN = /^[A-Za-z0-9._-]{1,80}$/

export interface PluginInvokeMessage {
  protocol: typeof PLUGIN_BRIDGE_PROTOCOL
  type: 'invoke'
  capability: string
  request_id: string
  request: PluginInvokeRequest
}

export interface PluginResultMessage {
  protocol: typeof PLUGIN_BRIDGE_PROTOCOL
  type: 'result'
  capability: string
  request_id: string
  ok: boolean
  response?: PluginInvokeResponse
  error?: {
    status: number
    message: string
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

export function parsePluginInvokeMessage(
  value: unknown,
  capability: string
): PluginInvokeMessage | null {
  if (!isRecord(value) || value.protocol !== PLUGIN_BRIDGE_PROTOCOL || value.type !== 'invoke') {
    return null
  }
  if (value.capability !== capability || typeof value.request_id !== 'string') {
    return null
  }
  if (!REQUEST_ID_PATTERN.test(value.request_id) || !isRecord(value.request)) {
    return null
  }

  const request = value.request
  if (
    typeof request.method !== 'string' ||
    typeof request.path !== 'string' ||
    (request.query !== undefined && typeof request.query !== 'string') ||
    (request.content_type !== undefined && typeof request.content_type !== 'string') ||
    (request.accept !== undefined && typeof request.accept !== 'string') ||
    (request.idempotency_key !== undefined && typeof request.idempotency_key !== 'string') ||
    (request.body !== undefined && typeof request.body !== 'string')
  ) {
    return null
  }

  return value as unknown as PluginInvokeMessage
}

export function createPluginCapability(): string {
  const bytes = new Uint8Array(24)
  crypto.getRandomValues(bytes)
  return Array.from(bytes, (value) => value.toString(16).padStart(2, '0')).join('')
}

function isPlainRecord(value: unknown): value is Record<string, unknown> {
  if (!isRecord(value)) return false
  const prototype = Object.getPrototypeOf(value)
  return prototype === Object.prototype || prototype === null
}

export function pluginBridgeError(error: unknown): { status: number; message: string } {
  const value = isPlainRecord(error) ? error : {}
  const status = typeof value.status === 'number' ? value.status : 0
  const message = typeof value.message === 'string' && value.message.trim()
    ? value.message.trim().slice(0, 300)
    : 'Plugin request failed'
  return { status, message }
}
