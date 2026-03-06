const API_BASE = '/api/v1'

export class APIError extends Error {
  constructor(public status: number, message: string) {
    super(message)
    this.name = 'APIError'
  }
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
  token?: string | null,
  orgId?: string | null,
): Promise<T> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  if (token) headers['Authorization'] = `Bearer ${token}`
  if (orgId) headers['X-Org-ID'] = orgId

  const res = await fetch(`${API_BASE}${path}`, {
    method,
    headers,
    body: body ? JSON.stringify(body) : undefined,
  })

  if (!res.ok) {
    let msg = `API error (${res.status})`
    try {
      const data = await res.json()
      msg = data.error || data.message || msg
    } catch { /* ignore */ }
    throw new APIError(res.status, msg)
  }

  if (res.status === 204) return null as T
  return res.json()
}

export const api = {
  // ── Health ──
  health: () => request<{ status: string; version: string; time: string }>('GET', '/health'),

  // ── Environments ──
  envs: {
    list:    (token: string, orgId: string) => request<any[]>('GET', '/environments', undefined, token, orgId),
    get:     (token: string, orgId: string, id: string) => request<any>('GET', `/environments/${id}`, undefined, token, orgId),
    create:  (token: string, orgId: string, body: { name: string; size: string; region: string; context_branch?: string }) =>
      request<any>('POST', '/environments', body, token, orgId),
    destroy: (token: string, orgId: string, id: string) => request<any>('DELETE', `/environments/${id}`, undefined, token, orgId),
    health:  (token: string, orgId: string, id: string) => request<any>('GET', `/environments/${id}/health`, undefined, token, orgId),
  },

  // ── Autoscale ──
  autoscale: {
    enable:  (token: string, orgId: string, envId: string, body: any) =>
      request<any>('POST', `/environments/${envId}/autoscale`, body, token, orgId),
    status:  (token: string, orgId: string, envId: string) =>
      request<any>('GET', `/environments/${envId}/autoscale/status`, undefined, token, orgId),
    history: (token: string, orgId: string, envId: string) =>
      request<any[]>('GET', `/environments/${envId}/autoscale/history`, undefined, token, orgId),
    disable: (token: string, orgId: string, envId: string) =>
      request<any>('DELETE', `/environments/${envId}/autoscale`, undefined, token, orgId),
  },

  // ── Context ──
  context: {
    list:   (token: string, orgId: string) => request<any[]>('GET', '/contexts', undefined, token, orgId),
    get:    (token: string, orgId: string, branch: string) => request<any>('GET', `/contexts/${encodeURIComponent(branch)}`, undefined, token, orgId),
    save:   (token: string, orgId: string, body: any) => request<any>('POST', '/contexts', body, token, orgId),
    delete: (token: string, orgId: string, branch: string) => request<any>('DELETE', `/contexts/${encodeURIComponent(branch)}`, undefined, token, orgId),
  },

  // ── Events ──
  events: {
    list:    (token: string, orgId: string, params?: Record<string, string>) => {
      const qs = params ? '?' + new URLSearchParams(params).toString() : ''
      return request<any[]>('GET', `/events${qs}`, undefined, token, orgId)
    },
    publish: (token: string, orgId: string, body: any) => request<any>('POST', '/events', body, token, orgId),
    stats:   (token: string, orgId: string) => request<any>('GET', '/events/stats', undefined, token, orgId),
    meshHealth: (token: string, orgId: string) => request<any>('GET', '/mesh/health', undefined, token, orgId),
    streamURL: (branch: string) => `${API_BASE}/events/stream?branch=${encodeURIComponent(branch)}`,
  },

  // ── Billing ──
  billing: {
    usage:         (token: string, orgId: string) => request<any>('GET', '/billing/usage', undefined, token, orgId),
    status:        (token: string, orgId: string) => request<any>('GET', '/billing/status', undefined, token, orgId),
    setup:         (token: string, orgId: string, body: { org_name: string; email: string }) =>
      request<any>('POST', '/billing/setup', body, token, orgId),
    invoices:      (token: string, orgId: string) => request<any[]>('GET', '/billing/invoices', undefined, token, orgId),
    portal:        (token: string, orgId: string, returnUrl: string, flow?: string) =>
      request<{ url: string }>('POST', '/billing/portal', { return_url: returnUrl, flow }, token, orgId),
    paymentMethod: (token: string, orgId: string) => request<any>('GET', '/billing/payment-method', undefined, token, orgId),
  },

  // ── Snapshots ──
  snapshots: {
    list:   (token: string, orgId: string, params?: Record<string, string>) => {
      const qs = params ? '?' + new URLSearchParams(params).toString() : ''
      return request<any[]>('GET', `/snapshots${qs}`, undefined, token, orgId)
    },
    create: (token: string, orgId: string, body: { environment_id: string }) =>
      request<any>('POST', '/snapshots', body, token, orgId),
  },

  // ── Repos ──
  repos: {
    list:       (token: string, orgId: string) => request<any[]>('GET', '/repos', undefined, token, orgId),
    connect:    (token: string, orgId: string, body: { repo_full_name: string }) =>
      request<any>('POST', '/repos', body, token, orgId),
    disconnect: (token: string, orgId: string, id: string) => request<any>('DELETE', `/repos/${id}`, undefined, token, orgId),
  },

  // ── Secrets ──
  secrets: {
    sync: (token: string, orgId: string, envId: string, body: any) =>
      request<any>('POST', `/environments/${envId}/secrets/sync`, body, token, orgId),
  },
}
