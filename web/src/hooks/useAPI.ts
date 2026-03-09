import { useState, useEffect, useCallback, useRef } from 'react'
import { useAuth, useOrganization, useOrganizationList } from '@clerk/clerk-react'

const IS_DEV = import.meta.env.DEV

/** Hook to get auth token + org ID for API calls */
export function useAPIAuth() {
  const { getToken, isSignedIn, orgId: sessionOrgId } = useAuth()
  const { organization } = useOrganization()
  const { setActive } = useOrganizationList()
  const [ready, setReady] = useState(IS_DEV)

  const orgId = IS_DEV
    ? (import.meta.env.VITE_DEV_ORG_ID || 'dev-org')
    : sessionOrgId || ''

  // Scope the session to the active org. Clerk JWTs only include org_id
  // after setActive({ organization }) completes. Block API calls until done.
  useEffect(() => {
    if (IS_DEV) return
    if (!organization?.id || !setActive) {
      setReady(false)
      return
    }
    if (sessionOrgId === organization.id) {
      setReady(true)
      return
    }
    setReady(false)
    setActive({ organization: organization.id }).then(() => setReady(true))
  }, [organization?.id, sessionOrgId, setActive])

  const getAuthToken = useCallback(async () => {
    try {
      const token = await getToken()
      return token || (IS_DEV ? 'dev-token' : null)
    } catch {
      return IS_DEV ? 'dev-token' : null
    }
  }, [getToken])

  return { getAuthToken, orgId, isSignedIn: (isSignedIn && ready) || IS_DEV }
}

/** Generic data fetching hook */
export function useFetch<T>(
  fetcher: (token: string, orgId: string) => Promise<T>,
  deps: any[] = [],
) {
  const [data, setData] = useState<T | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const { getAuthToken, orgId, isSignedIn } = useAPIAuth()
  const mountedRef = useRef(true)
  const fetchingRef = useRef(false)

  const refetch = useCallback(async () => {
    if (!isSignedIn || !orgId) return
    // Prevent concurrent calls
    if (fetchingRef.current) return
    fetchingRef.current = true
    setLoading(true)
    setError(null)
    try {
      const token = await getAuthToken()
      if (!token) throw new Error('Not authenticated')
      const result = await fetcher(token, orgId)
      if (mountedRef.current) setData(result)
    } catch (e: any) {
      if (mountedRef.current) setError(e.message || 'Failed to fetch')
    } finally {
      if (mountedRef.current) setLoading(false)
      fetchingRef.current = false
    }
  }, [isSignedIn, orgId, getAuthToken, fetcher, ...deps])

  useEffect(() => {
    mountedRef.current = true
    refetch()
    return () => { mountedRef.current = false }
  }, [refetch])

  return { data, loading, error, refetch, setData }
}

/** Mutation hook (POST, PUT, DELETE) */
export function useMutation<TInput, TOutput>(
  mutator: (token: string, orgId: string, input: TInput) => Promise<TOutput>,
) {
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const { getAuthToken, orgId } = useAPIAuth()

  const mutate = useCallback(async (input: TInput): Promise<TOutput | null> => {
    setLoading(true)
    setError(null)
    try {
      const token = await getAuthToken()
      if (!token) throw new Error('Not authenticated')
      const result = await mutator(token, orgId, input)
      return result
    } catch (e: any) {
      setError(e.message || 'Operation failed')
      return null
    } finally {
      setLoading(false)
    }
  }, [getAuthToken, orgId, mutator])

  return { mutate, loading, error, setError }
}

/** SSE hook for live context streaming */
export function useSSE(url: string | null, onEvent: (data: any) => void) {
  const [connected, setConnected] = useState(false)
  const sourceRef = useRef<EventSource | null>(null)

  useEffect(() => {
    if (!url) return

    const source = new EventSource(url)
    sourceRef.current = source

    source.onopen = () => setConnected(true)
    source.onmessage = (e) => {
      try {
        const data = JSON.parse(e.data)
        onEvent(data)
      } catch { /* skip non-JSON */ }
    }
    source.onerror = () => setConnected(false)

    return () => {
      source.close()
      setConnected(false)
    }
  }, [url])

  return { connected }
}

/** Polling hook */
export function usePolling(callback: () => void, intervalMs: number, enabled = true) {
  const callbackRef = useRef(callback)
  
  // Keep callback ref up to date
  useEffect(() => {
    callbackRef.current = callback
  }, [callback])

  useEffect(() => {
    if (!enabled) return
    // Don't call immediately - wait for first interval to avoid duplicate calls
    // The initial fetch is already handled by useFetch's useEffect
    const id = setInterval(() => {
      callbackRef.current()
    }, intervalMs)
    return () => clearInterval(id)
  }, [intervalMs, enabled])
}
