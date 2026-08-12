import { describe, expect, it, vi } from 'vitest'
import {
  PLUGIN_BRIDGE_PROTOCOL,
  createPluginCapability,
  parsePluginInvokeMessage,
  pluginBridgeError,
} from '@/features/plugins/bridge'

describe('plugin bridge', () => {
  it('accepts a valid invoke request for the current capability', () => {
    const capability = 'current-capability'
    const message = {
      protocol: PLUGIN_BRIDGE_PROTOCOL,
      type: 'invoke',
      capability,
      request_id: 'score-1',
      request: {
        method: 'POST',
        path: 'scores',
        content_type: 'application/json',
        body: '{"score":7}',
      },
    }

    expect(parsePluginInvokeMessage(message, capability)).toEqual(message)
  })

  it('rejects stale capabilities and malformed request fields', () => {
    const base = {
      protocol: PLUGIN_BRIDGE_PROTOCOL,
      type: 'invoke',
      capability: 'old-capability',
      request_id: 'score-1',
      request: { method: 'POST', path: 'scores' },
    }

    expect(parsePluginInvokeMessage(base, 'current-capability')).toBeNull()
    expect(parsePluginInvokeMessage({ ...base, capability: 'current-capability', request_id: '../bad' }, 'current-capability')).toBeNull()
    expect(parsePluginInvokeMessage({ ...base, capability: 'current-capability', request: { method: 1, path: 'scores' } }, 'current-capability')).toBeNull()
  })

  it('creates an unpredictable capability with the Web Crypto API', () => {
    const spy = vi.spyOn(crypto, 'getRandomValues')
    const capability = createPluginCapability()

    expect(spy).toHaveBeenCalledOnce()
    expect(capability).toMatch(/^[a-f0-9]{48}$/)
  })

  it('returns only bounded, display-safe error details', () => {
    expect(pluginBridgeError({ status: 502, message: 'downstream failed' })).toEqual({
      status: 502,
      message: 'downstream failed',
    })
    expect(pluginBridgeError(new Error('internal details'))).toEqual({
      status: 0,
      message: 'Plugin request failed',
    })
    expect(pluginBridgeError({ status: 400, message: 'x'.repeat(500) }).message).toHaveLength(300)
  })
})
