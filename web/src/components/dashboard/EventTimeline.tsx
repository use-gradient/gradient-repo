import { useState, useCallback, useRef, useEffect, useMemo } from 'react'
import { api } from '@/api/client'
import { useFetch, useSSE, useAPIAuth } from '@/hooks/useAPI'
import { cn, timeAgo, EVENT_TYPES } from '@/lib/utils'
import {
  Button, Card, Badge, EmptyState, Skeleton, StatusDot, Select, Input,
} from '@/components/ui'
import {
  Activity, GitBranch, Clock, RefreshCw, Radio, Filter,
  Search, ChevronDown, ChevronRight, Zap, Layers, X,
  ArrowDown, Pause, Play,
} from 'lucide-react'

/* ─── Types ─── */

interface ContextEvent {
  id: string
  event_type: string
  branch: string
  data: any
  source_env?: string
  agent_id?: string
  agent_role?: string
  task_id?: string
  sequence?: number
  created_at: string
}

/* ─── Extended event types for Phase 7 ─── */
const PHASE7_EVENT_TYPES: Record<string, { icon: string; label: string; color: string }> = {
  ...EVENT_TYPES,
  task_started:      { icon: '🚀', label: 'Task Started',      color: 'text-primary' },
  task_completed:    { icon: '✅', label: 'Task Completed',    color: 'text-emerald-400' },
  task_failed:       { icon: '💥', label: 'Task Failed',       color: 'text-destructive' },
  agent_spawned:     { icon: '🤖', label: 'Agent Spawned',     color: 'text-violet-400' },
  agent_completed:   { icon: '🏁', label: 'Agent Completed',   color: 'text-emerald-400' },
  contract_proposed: { icon: '📋', label: 'Contract Proposed', color: 'text-blue-400' },
  contract_fulfilled:{ icon: '🤝', label: 'Contract Fulfilled',color: 'text-emerald-400' },
  merge_conflict:    { icon: '⚠️',  label: 'Merge Conflict',    color: 'text-yellow-400' },
  bundle_applied:    { icon: '📦', label: 'Bundle Applied',    color: 'text-primary' },
  code_review:       { icon: '🔍', label: 'Code Review',       color: 'text-violet-400' },
}

/* ─── Single Event Row ─── */
function EventRow({ event, isExpanded, onToggle }: { event: ContextEvent; isExpanded: boolean; onToggle: () => void }) {
  const typeInfo = PHASE7_EVENT_TYPES[event.event_type] || PHASE7_EVENT_TYPES.custom || { icon: '📝', label: event.event_type, color: 'text-muted-foreground' }

  const dataStr = event.data
    ? (typeof event.data === 'string' ? event.data : JSON.stringify(event.data, null, 2))
    : null

  return (
    <div className={cn(
      'border-b border-border last:border-0 transition-colors',
      isExpanded && 'bg-secondary/20',
    )}>
      <button
        className="w-full flex items-center gap-3 px-4 py-3 text-left hover:bg-secondary/30 transition-colors"
        onClick={onToggle}
      >
        {/* Timeline dot */}
        <div className="flex flex-col items-center shrink-0 self-stretch">
          <span className={cn(
            'w-2 h-2 rounded-full shrink-0 mt-1.5',
            event.event_type.includes('failed') || event.event_type.includes('conflict') ? 'bg-destructive'
              : event.event_type.includes('completed') || event.event_type.includes('fulfilled') || event.event_type.includes('fixed') ? 'bg-emerald-400'
              : event.event_type.includes('started') || event.event_type.includes('spawned') ? 'bg-primary'
              : 'bg-muted-foreground',
          )} />
        </div>

        {/* Icon */}
        <span className="text-sm shrink-0">{typeInfo.icon}</span>

        {/* Content */}
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2 flex-wrap">
            <span className={cn('text-xs font-medium', typeInfo.color)}>{typeInfo.label}</span>
            {event.agent_role && (
              <Badge className="text-[10px]">{event.agent_role}</Badge>
            )}
            {event.task_id && (
              <span className="text-[10px] text-muted-foreground font-mono">task:{event.task_id.slice(0, 8)}</span>
            )}
          </div>
          {dataStr && !isExpanded && (
            <p className="text-[10px] text-muted-foreground truncate mt-0.5">
              {dataStr.length > 120 ? dataStr.slice(0, 120) + '…' : dataStr}
            </p>
          )}
        </div>

        {/* Meta */}
        <div className="flex items-center gap-2 shrink-0">
          <span className="text-[10px] text-muted-foreground flex items-center gap-1 font-mono">
            <GitBranch className="w-2.5 h-2.5" />{event.branch}
          </span>
          {event.sequence != null && (
            <span className="text-[10px] text-muted-foreground font-mono">#{event.sequence}</span>
          )}
          {event.created_at && (
            <span className="text-[10px] text-muted-foreground whitespace-nowrap">
              {timeAgo(event.created_at)}
            </span>
          )}
          {dataStr && (
            isExpanded
              ? <ChevronDown className="w-3.5 h-3.5 text-muted-foreground" />
              : <ChevronRight className="w-3.5 h-3.5 text-muted-foreground" />
          )}
        </div>
      </button>

      {/* Expanded data view */}
      {isExpanded && dataStr && (
        <div className="px-4 pb-3 ml-10">
          <pre className="p-3 rounded-md bg-background border border-border text-xs font-mono text-muted-foreground overflow-x-auto max-h-48 overflow-y-auto">
            {dataStr}
          </pre>
          <div className="flex items-center gap-3 mt-2 text-[10px] text-muted-foreground">
            {event.source_env && <span>source: {event.source_env}</span>}
            {event.agent_id && <span className="font-mono">agent: {event.agent_id.slice(0, 12)}</span>}
            {event.created_at && <span>{new Date(event.created_at).toLocaleString()}</span>}
          </div>
        </div>
      )}
    </div>
  )
}

/* ─── Live Events Indicator ─── */
function LiveIndicator({ count, connected, paused, onToggle }: {
  count: number; connected: boolean; paused: boolean; onToggle: () => void
}) {
  return (
    <div className="flex items-center gap-2">
      {connected ? (
        <>
          <StatusDot status="connected" className="animate-pulse-dot" />
          <span className="text-xs text-primary font-medium">Live</span>
        </>
      ) : (
        <>
          <StatusDot status="disconnected" />
          <span className="text-xs text-muted-foreground">Offline</span>
        </>
      )}
      <span className="text-[10px] text-muted-foreground">{count} new</span>
      <button
        onClick={onToggle}
        className="p-1 rounded hover:bg-secondary transition-colors"
        title={paused ? 'Resume live updates' : 'Pause live updates'}
      >
        {paused
          ? <Play className="w-3 h-3 text-muted-foreground" />
          : <Pause className="w-3 h-3 text-muted-foreground" />
        }
      </button>
    </div>
  )
}

/* ─── Main Component ─── */
export default function EventTimeline() {
  const [selectedBranch, setSelectedBranch] = useState('')
  const [typeFilter, setTypeFilter] = useState('')
  const [searchQuery, setSearchQuery] = useState('')
  const [expandedEventId, setExpandedEventId] = useState<string | null>(null)
  const [liveEvents, setLiveEvents] = useState<ContextEvent[]>([])
  const [livePaused, setLivePaused] = useState(false)
  const { orgId } = useAPIAuth()

  const { data: contexts } = useFetch(
    useCallback((token: string, orgId: string) => api.context.list(token, orgId), [])
  )

  const { data: historicalEvents, loading, refetch } = useFetch<ContextEvent[]>(
    useCallback((token: string, orgId: string) => {
      const params: Record<string, string> = { limit: '200' }
      if (selectedBranch) params.branch = selectedBranch
      if (typeFilter) params.types = typeFilter
      return api.events.list(token, orgId, params)
    }, [selectedBranch, typeFilter]),
    [selectedBranch, typeFilter]
  )

  const streamUrl = selectedBranch
    ? `${api.events.streamURL(selectedBranch)}&org_id=${orgId}`
    : null

  const { connected } = useSSE(streamUrl, (data) => {
    if (!livePaused) {
      setLiveEvents(prev => [data, ...prev].slice(0, 500))
    }
  })

  const allEvents = useMemo(() => {
    const combined = [...liveEvents, ...(historicalEvents || [])]
    const seen = new Set<string>()
    const deduped = combined.filter(e => {
      const key = e.id || `${e.event_type}-${e.created_at}-${e.branch}`
      if (seen.has(key)) return false
      seen.add(key)
      return true
    })

    if (!searchQuery) return deduped

    const q = searchQuery.toLowerCase()
    return deduped.filter(e =>
      e.event_type.toLowerCase().includes(q) ||
      e.branch.toLowerCase().includes(q) ||
      (e.agent_role && e.agent_role.toLowerCase().includes(q)) ||
      (e.data && JSON.stringify(e.data).toLowerCase().includes(q))
    )
  }, [liveEvents, historicalEvents, searchQuery])

  const branches = contexts?.map((c: any) => c.branch) || []
  const eventTypeOptions = Object.entries(PHASE7_EVENT_TYPES).map(([k, v]) => ({
    value: k,
    label: `${v.icon} ${v.label}`,
  }))

  const typeCounts = useMemo(() => {
    const counts: Record<string, number> = {}
    allEvents.forEach(e => {
      counts[e.event_type] = (counts[e.event_type] || 0) + 1
    })
    return counts
  }, [allEvents])

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-lg font-semibold text-foreground flex items-center gap-2">
            <Activity className="w-5 h-5 text-primary" /> Event Timeline
          </h2>
          <p className="text-xs text-muted-foreground mt-0.5">Unified view of all Live Context Mesh events across agents</p>
        </div>
        <div className="flex items-center gap-3">
          {selectedBranch && (
            <LiveIndicator
              count={liveEvents.length}
              connected={connected}
              paused={livePaused}
              onToggle={() => setLivePaused(!livePaused)}
            />
          )}
          <Button variant="ghost" size="sm" onClick={() => { refetch(); setLiveEvents([]) }}>
            <RefreshCw className="w-3.5 h-3.5" />
          </Button>
        </div>
      </div>

      {/* Filters */}
      <Card className="p-4">
        <div className="flex items-center gap-1.5 mb-3">
          <Filter className="w-3.5 h-3.5 text-muted-foreground" />
          <span className="text-xs font-medium text-foreground">Filters</span>
        </div>
        <div className="flex flex-col sm:flex-row gap-3">
          <div className="flex-1">
            <Select
              label="Branch"
              value={selectedBranch}
              onChange={e => { setSelectedBranch(e.target.value); setLiveEvents([]) }}
              options={[
                { value: '', label: 'All branches' },
                ...branches.map((b: string) => ({ value: b, label: b })),
              ]}
            />
          </div>
          <div className="flex-1">
            <Select
              label="Event Type"
              value={typeFilter}
              onChange={e => setTypeFilter(e.target.value)}
              options={[
                { value: '', label: 'All types' },
                ...eventTypeOptions,
              ]}
            />
          </div>
          <div className="flex-1">
            <div className="space-y-2">
              <label className="text-sm font-medium text-foreground">Search</label>
              <div className="relative">
                <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-muted-foreground" />
                <input
                  type="text"
                  value={searchQuery}
                  onChange={e => setSearchQuery(e.target.value)}
                  placeholder="Search event data…"
                  className="flex h-9 w-full rounded-md border border-input bg-transparent pl-9 pr-8 py-1 text-sm shadow-sm placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                />
                {searchQuery && (
                  <button
                    onClick={() => setSearchQuery('')}
                    className="absolute right-2 top-1/2 -translate-y-1/2 p-0.5 rounded hover:bg-secondary"
                  >
                    <X className="w-3.5 h-3.5 text-muted-foreground" />
                  </button>
                )}
              </div>
            </div>
          </div>
        </div>

        {/* Active filter summary */}
        {(selectedBranch || typeFilter || searchQuery) && (
          <div className="flex items-center gap-2 mt-3 pt-3 border-t border-border">
            <span className="text-[10px] text-muted-foreground">Active:</span>
            {selectedBranch && (
              <Badge className="text-[10px] gap-1">
                <GitBranch className="w-2.5 h-2.5" />{selectedBranch}
                <button onClick={() => { setSelectedBranch(''); setLiveEvents([]) }}><X className="w-2.5 h-2.5" /></button>
              </Badge>
            )}
            {typeFilter && (
              <Badge className="text-[10px] gap-1">
                {PHASE7_EVENT_TYPES[typeFilter]?.icon} {PHASE7_EVENT_TYPES[typeFilter]?.label || typeFilter}
                <button onClick={() => setTypeFilter('')}><X className="w-2.5 h-2.5" /></button>
              </Badge>
            )}
            {searchQuery && (
              <Badge className="text-[10px] gap-1">
                <Search className="w-2.5 h-2.5" />"{searchQuery}"
                <button onClick={() => setSearchQuery('')}><X className="w-2.5 h-2.5" /></button>
              </Badge>
            )}
            <span className="text-[10px] text-muted-foreground ml-auto">{allEvents.length} events</span>
          </div>
        )}
      </Card>

      {/* Type distribution */}
      {allEvents.length > 0 && (
        <div className="flex gap-1.5 flex-wrap">
          {Object.entries(typeCounts)
            .sort(([, a], [, b]) => b - a)
            .slice(0, 10)
            .map(([type, count]) => {
              const info = PHASE7_EVENT_TYPES[type] || { icon: '📝', label: type, color: 'text-muted-foreground' }
              return (
                <button
                  key={type}
                  onClick={() => setTypeFilter(typeFilter === type ? '' : type)}
                  className={cn(
                    'px-2 py-1 text-[10px] rounded-md border transition-colors flex items-center gap-1',
                    typeFilter === type
                      ? 'bg-primary/10 text-primary border-primary/30'
                      : 'text-muted-foreground border-border hover:border-muted-foreground/30',
                  )}
                >
                  <span>{info.icon}</span>
                  <span>{count}</span>
                </button>
              )
            })}
        </div>
      )}

      {/* Event list */}
      <Card className="overflow-hidden">
        <div className="px-4 py-3 border-b border-border flex items-center justify-between">
          <p className="text-xs font-medium text-foreground flex items-center gap-1.5">
            <Zap className="w-3.5 h-3.5 text-primary" />
            {allEvents.length} Events
          </p>
          {liveEvents.length > 0 && (
            <Badge variant="success" className="text-[10px] animate-pulse">
              {liveEvents.length} live
            </Badge>
          )}
        </div>

        {loading ? (
          <div className="p-4 space-y-3">
            {[1, 2, 3, 4, 5].map(i => <Skeleton key={i} className="h-12 w-full" />)}
          </div>
        ) : allEvents.length === 0 ? (
          <div className="py-12 text-center">
            <Activity className="w-8 h-8 text-muted-foreground mx-auto mb-3" />
            <p className="text-sm font-medium text-foreground mb-1">No events found</p>
            <p className="text-xs text-muted-foreground max-w-sm mx-auto">
              {selectedBranch
                ? `No events on branch "${selectedBranch}". Events will appear as agents work.`
                : 'Select a branch or wait for agents to start generating events.'}
            </p>
          </div>
        ) : (
          <div className="max-h-[600px] overflow-y-auto">
            {allEvents.map((event, i) => (
              <EventRow
                key={event.id || i}
                event={event}
                isExpanded={expandedEventId === (event.id || String(i))}
                onToggle={() => setExpandedEventId(
                  expandedEventId === (event.id || String(i)) ? null : (event.id || String(i))
                )}
              />
            ))}
          </div>
        )}
      </Card>
    </div>
  )
}
