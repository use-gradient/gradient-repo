const API_BASE = import.meta.env.VITE_API_URL
  ? `${import.meta.env.VITE_API_URL}/api/v1`
  : '/api/v1'

export class APIError extends Error {
  constructor(public status: number, message: string) {
    super(message)
    this.name = 'APIError'
  }
}

const IS_DEV = import.meta.env.DEV

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
  token?: string | null,
  orgId?: string | null,
): Promise<T> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  if (token && token !== 'dev-token') {
    headers['Authorization'] = `Bearer ${token}`
  }
  if (IS_DEV) {
    headers['X-User-ID'] = import.meta.env.VITE_DEV_USER_ID || 'dev-user'
    headers['X-Org-ID'] = orgId || import.meta.env.VITE_DEV_ORG_ID || 'dev-org'
  } else if (orgId) {
    headers['X-Org-ID'] = orgId
  }

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
    available:  (token: string, orgId: string) => request<{ repos: string[] }>('GET', '/repos/available', undefined, token, orgId),
    connect:    (token: string, orgId: string, body: { repo: string }) =>
      request<any>('POST', '/repos', body, token, orgId),
    disconnect: (token: string, orgId: string, id: string) => request<any>('DELETE', `/repos/${id}`, undefined, token, orgId),
  },

  // ── Secrets ──
  secrets: {
    sync: (token: string, orgId: string, envId: string, body: any) =>
      request<any>('POST', `/environments/${envId}/secrets/sync`, body, token, orgId),
  },

  // ── Agent Tasks ──
  tasks: {
    readiness:(token: string, orgId: string) => request<{ ready: boolean; claude_configured: boolean; linear_connected: boolean; message?: string }>('GET', '/tasks/readiness', undefined, token, orgId),
    list:     (token: string, orgId: string, params?: Record<string, string>) => {
      const qs = params ? '?' + new URLSearchParams(params).toString() : ''
      return request<any[]>('GET', `/tasks${qs}`, undefined, token, orgId)
    },
    get:      (token: string, orgId: string, id: string) => request<any>('GET', `/tasks/${id}`, undefined, token, orgId),
    create:   (token: string, orgId: string, body: any) => request<any>('POST', '/tasks', body, token, orgId),
    start:    (token: string, orgId: string, id: string) => request<any>('POST', `/tasks/${id}/start`, {}, token, orgId),
    cancel:   (token: string, orgId: string, id: string) => request<any>('POST', `/tasks/${id}/cancel`, {}, token, orgId),
    retry:    (token: string, orgId: string, id: string) => request<any>('POST', `/tasks/${id}/retry`, {}, token, orgId),
    logs:     (token: string, orgId: string, id: string) => request<any[]>('GET', `/tasks/${id}/logs`, undefined, token, orgId),
    stats:    (token: string, orgId: string) => request<any>('GET', '/tasks/stats', undefined, token, orgId),
  },

  // ── Integrations ──
  integrations: {
    status:          (token: string, orgId: string) => request<any>('GET', '/integrations/status', undefined, token, orgId),
    linear: {
      get:           (token: string, orgId: string) => request<any>('GET', '/integrations/linear', undefined, token, orgId),
      authUrl:       (token: string, orgId: string) => request<{ url: string; state: string }>('GET', '/integrations/linear/auth-url', undefined, token, orgId),
      callback:      (token: string, orgId: string, body: { code: string; state: string }) => request<any>('POST', '/integrations/linear/callback', body, token, orgId),
      disconnect:    (token: string, orgId: string) => request<any>('DELETE', '/integrations/linear', undefined, token, orgId),
    },
    claude: {
      get:           (token: string, orgId: string) => request<any>('GET', '/integrations/claude', undefined, token, orgId),
      save:          (token: string, orgId: string, body: any) => request<any>('PUT', '/integrations/claude', body, token, orgId),
      disconnect:    (token: string, orgId: string) => request<any>('DELETE', '/integrations/claude', undefined, token, orgId),
    },
    github: {
      get:           (token: string, orgId: string) => request<any>('GET', '/integrations/github', undefined, token, orgId),
      authUrl:       (token: string, orgId: string) => request<{ url: string; state: string }>('GET', '/integrations/github/auth-url', undefined, token, orgId),
      callback:      (token: string, orgId: string, body: { code: string; state: string }) => request<any>('POST', '/integrations/github/callback', body, token, orgId),
      disconnect:    (token: string, orgId: string) => request<any>('DELETE', '/integrations/github', undefined, token, orgId),
    },
  },
}
