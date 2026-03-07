import { useState, useCallback, useRef, useEffect } from 'react'
import { api } from '@/api/client'
import { useFetch, useMutation, useSSE, useAPIAuth } from '@/hooks/useAPI'
import { cn, timeAgo, EVENT_TYPES } from '@/lib/utils'
import {
  Button, Card, Badge, EmptyState, CopyButton, Modal, Input, Select,
  Table, TableRow, TableCell, Skeleton, useToast, CodeBlock, StatusDot, Callout,
} from '@/components/ui'
import {
  Brain, GitBranch, Package, XCircle, Lightbulb, Plus,
  Activity, Radio, Network,
  Terminal, Clock, RefreshCw, Save,
} from 'lucide-react'

/* ─── Event Card ─── */
function EventCard({ event }: { event: any }) {
  const typeInfo = EVENT_TYPES[event.event_type] || EVENT_TYPES.custom

  return (
    <div className="flex gap-3 py-3 border-b border-border last:border-0 animate-fade-in">
      <span className="text-sm mt-0.5">{typeInfo.icon}</span>
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2 mb-0.5">
          <span className={cn('text-xs font-medium', typeInfo.color)}>{typeInfo.label}</span>
          <span className="text-[10px] text-muted-foreground font-mono">seq:{event.sequence || '?'}</span>
        </div>
        <p className="text-xs text-muted-foreground break-all">
          {event.data ? (typeof event.data === 'string' ? event.data : JSON.stringify(event.data)) : '—'}
        </p>
        <div className="flex items-center gap-3 mt-1 text-[10px] text-muted-foreground">
          <span className="flex items-center gap-1"><GitBranch className="w-2.5 h-2.5" />{event.branch}</span>
          {event.created_at && <span className="flex items-center gap-1"><Clock className="w-2.5 h-2.5" />{timeAgo(event.created_at)}</span>}
        </div>
      </div>
    </div>
  )
}

/* ─── Live Stream Panel ─── */
function LiveStream({ branch }: { branch: string }) {
  const [events, setEvents] = useState<any[]>([])
  const { getAuthToken, orgId } = useAPIAuth()
  const containerRef = useRef<HTMLDivElement>(null)

  const streamUrl = branch ? `${api.events.streamURL(branch)}&org_id=${orgId}` : null
  const { connected } = useSSE(streamUrl, (data) => {
    setEvents(prev => [data, ...prev].slice(0, 100))
  })

  useEffect(() => {
    if (containerRef.current) containerRef.current.scrollTop = 0
  }, [events.length])

  return (
    <Card className="overflow-hidden">
      <div className="flex items-center justify-between px-4 py-3 border-b border-border">
        <div className="flex items-center gap-2">
          {connected ? (
            <>
              <StatusDot status="connected" className="animate-pulse-dot" />
              <span className="text-xs text-primary font-medium">Live</span>
            </>
          ) : (
            <>
              <StatusDot status="disconnected" />
              <span className="text-xs text-muted-foreground">Disconnected</span>
            </>
          )}
          <span className="text-[10px] text-muted-foreground">on {branch}</span>
        </div>
        <span className="text-[10px] text-muted-foreground">{events.length} events</span>
      </div>
      <div ref={containerRef} className="max-h-80 overflow-y-auto px-4">
        {events.length === 0 ? (
          <div className="py-10 text-center text-xs text-muted-foreground">
            <Radio className="w-5 h-5 mx-auto mb-2 text-muted-foreground" />
            <p>Waiting for events on <span className="font-mono">{branch}</span>…</p>
          </div>
        ) : (
          events.map((e, i) => <EventCard key={i} event={e} />)
        )}
      </div>
    </Card>
  )
}

/* ─── Branch Card ─── */
function BranchCard({ ctx, isActive, onClick }: { ctx: any; isActive: boolean; onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      className={cn(
        'w-full text-left p-3 rounded-md border transition-colors',
        isActive ? 'border-primary bg-primary/10' : 'border-border hover:border-muted-foreground/30',
      )}
    >
      <div className="flex items-center gap-2 mb-1">
        <GitBranch className="w-3.5 h-3.5 text-primary shrink-0" />
        <span className="text-sm font-medium text-foreground truncate">{ctx.branch}</span>
      </div>
      <div className="flex items-center gap-3 text-[10px] text-muted-foreground">
        {ctx.base_os && <Badge>{ctx.base_os}</Badge>}
        <span className="flex items-center gap-1"><Package className="w-2.5 h-2.5" />{ctx.installed_packages?.length || 0}</span>
        <span className="flex items-center gap-1"><XCircle className="w-2.5 h-2.5" />{ctx.previous_failures?.length || 0}</span>
      </div>
    </button>
  )
}

/* ─── Save Context Form ─── */
function SaveContextForm({ onSaved }: { onSaved: () => void }) {
  const [open, setOpen] = useState(false)
  const [branch, setBranch] = useState('')
  const [baseOs, setBaseOs] = useState('ubuntu-24.04')
  const [packages, setPackages] = useState('')
  const { toast } = useToast()
  const { mutate, loading, error } = useMutation(
    (token: string, orgId: string, body: any) => api.context.save(token, orgId, body)
  )

  const handleSave = async () => {
    const body: any = { branch, base_os: baseOs }
    if (packages) {
      body.installed_packages = packages.split(',').map(p => {
        const [name, version] = p.trim().split('=')
        return { name, version: version || '' }
      })
    }
    const result = await mutate(body)
    if (result) {
      toast('success', `Context saved for ${branch}`)
      setBranch(''); setPackages('')
      setOpen(false)
      onSaved()
    }
  }

  const cliCmd = `gc context save --branch ${branch || 'main'} --os ${baseOs}${packages ? ` --packages ${packages}` : ''}`

  return (
    <>
      <Button size="sm" onClick={() => setOpen(true)}><Plus className="w-3.5 h-3.5" /> Save Context</Button>
      <Modal open={open} onClose={() => setOpen(false)} title="Save Context" description="Create or update branch context" footer={
        <Button onClick={handleSave} loading={loading} disabled={!branch}><Save className="w-3.5 h-3.5" /> Save</Button>
      }>
        <div className="space-y-4">
          <Input label="Branch" placeholder="e.g. main, feature/auth" value={branch} onChange={e => setBranch(e.target.value)} autoFocus />
          <Select label="Base OS" options={[
            { value: 'ubuntu-24.04', label: 'Ubuntu 24.04' },
            { value: 'debian-12', label: 'Debian 12' },
            { value: 'alpine-3.19', label: 'Alpine 3.19' },
            { value: 'fedora-40', label: 'Fedora 40' },
          ]} value={baseOs} onChange={e => setBaseOs(e.target.value)} />
          <Input label="Packages (optional)" placeholder="python3=3.12,numpy=1.26.0" value={packages} onChange={e => setPackages(e.target.value)} />
          {error && <p className="text-xs text-destructive">{error}</p>}
          <div className="pt-2 border-t border-border">
            <p className="text-[10px] text-muted-foreground flex items-center gap-1.5 mb-1"><Terminal className="w-3 h-3" /> CLI equivalent</p>
            <CodeBlock code={cliCmd} />
          </div>
        </div>
      </Modal>
    </>
  )
}

/* ─── Publish Event Form ─── */
function PublishForm({ selectedBranch }: { selectedBranch: string }) {
  const [open, setOpen] = useState(false)
  const [eventType, setEventType] = useState('package_installed')
  const [key, setKey] = useState('')
  const [value, setValue] = useState('')
  const { toast } = useToast()
  const { mutate, loading, error } = useMutation(
    (token: string, orgId: string, body: any) => api.events.publish(token, orgId, body)
  )

  const handlePublish = async () => {
    let data: any = {}
    if (eventType === 'package_installed') data = { manager: key, name: value }
    else if (eventType === 'test_failed') data = { test: key, error: value }
    else if (eventType === 'pattern_learned') data = { key, value }
    else if (eventType === 'config_changed') data = { key, value }
    else data = { key, value }

    const result = await mutate({ branch: selectedBranch, event_type: eventType, data, source_env: 'dashboard' })
    if (result) {
      toast('success', `Event published to ${selectedBranch}`)
      setKey(''); setValue('')
      setOpen(false)
    }
  }

  return (
    <>
      <Button variant="secondary" size="sm" onClick={() => setOpen(true)} disabled={!selectedBranch}>
        <Activity className="w-3.5 h-3.5" /> Publish Event
      </Button>
      <Modal open={open} onClose={() => setOpen(false)} title="Publish Event" description={`to branch: ${selectedBranch}`} footer={
        <Button onClick={handlePublish} loading={loading} disabled={!key}>Publish</Button>
      }>
        <div className="space-y-4">
          <Select label="Event Type" options={Object.entries(EVENT_TYPES).map(([k, v]) => ({ value: k, label: v.label }))} value={eventType} onChange={e => setEventType(e.target.value)} />
          <Input label={eventType === 'package_installed' ? 'Package Manager / Name' : eventType === 'test_failed' ? 'Test Name' : 'Key'} value={key} onChange={e => setKey(e.target.value)} placeholder="e.g. torch" />
          <Input label={eventType === 'test_failed' ? 'Error' : 'Value'} value={value} onChange={e => setValue(e.target.value)} placeholder="e.g. 2.1.0" />
          {error && <p className="text-xs text-destructive">{error}</p>}
        </div>
      </Modal>
    </>
  )
}

/* ─── Main Component ─── */
export default function ContextTab() {
  const [selectedBranch, setSelectedBranch] = useState('')
  const [showLive, setShowLive] = useState(false)
  const [eventsFilter, setEventsFilter] = useState('')

  const { data: contexts, loading, refetch } = useFetch(
    useCallback((token: string, orgId: string) => api.context.list(token, orgId), [])
  )

  const { data: contextDetail } = useFetch(
    useCallback((token: string, orgId: string) => selectedBranch ? api.context.get(token, orgId, selectedBranch) : Promise.resolve(null), [selectedBranch]),
    [selectedBranch]
  )

  const { data: events, refetch: refetchEvents } = useFetch(
    useCallback((token: string, orgId: string) => {
      const params: Record<string, string> = {}
      if (selectedBranch) params.branch = selectedBranch
      if (eventsFilter) params.types = eventsFilter
      return api.events.list(token, orgId, params)
    }, [selectedBranch, eventsFilter]),
    [selectedBranch, eventsFilter]
  )

  const { data: meshHealth } = useFetch(
    useCallback((token: string, orgId: string) => api.events.meshHealth(token, orgId), [])
  )

  return (
    <div className="space-y-6">
      <div className="flex flex-col sm:flex-row items-start sm:items-center gap-3 justify-between">
        <div className="flex items-center gap-2">
          <SaveContextForm onSaved={refetch} />
          <PublishForm selectedBranch={selectedBranch} />
        </div>
        <div className="flex items-center gap-2">
          {meshHealth && (
            <Badge variant={meshHealth.status === 'ok' ? 'success' : 'destructive'}>
              <Network className="w-3 h-3 mr-1" />
              Mesh {meshHealth.status}
            </Badge>
          )}
          <Button variant="ghost" size="sm" onClick={() => { refetch(); refetchEvents() }}>
            <RefreshCw className="w-3.5 h-3.5" />
          </Button>
        </div>
      </div>

      <div className="grid lg:grid-cols-12 gap-6">
        <div className="lg:col-span-3 space-y-2">
          <p className="text-xs font-medium text-muted-foreground mb-2">Branches</p>
          {loading ? (
            [1,2,3].map(i => <Skeleton key={i} className="h-16 w-full" />)
          ) : !contexts || contexts.length === 0 ? (
            <div className="text-center py-6 text-xs text-muted-foreground">
              <Brain className="w-5 h-5 mx-auto mb-2" />
              No contexts yet
            </div>
          ) : (
            contexts.map((ctx: any) => (
              <BranchCard
                key={ctx.branch}
                ctx={ctx}
                isActive={ctx.branch === selectedBranch}
                onClick={() => { setSelectedBranch(ctx.branch); setShowLive(false) }}
              />
            ))
          )}
        </div>

        <div className="lg:col-span-9 space-y-6">
          {!selectedBranch ? (
            <EmptyState
              icon={Brain}
              title="Select a branch"
              description="Choose a branch from the left to view its context and events."
            />
          ) : (
            <>
              {contextDetail && (
                <Card className="p-5">
                  <div className="flex items-center justify-between mb-4">
                    <div className="flex items-center gap-2">
                      <GitBranch className="w-4 h-4 text-primary" />
                      <h3 className="text-sm font-medium text-foreground">{selectedBranch}</h3>
                      {contextDetail.base_os && <Badge>{contextDetail.base_os}</Badge>}
                    </div>
                    <div className="flex items-center gap-2">
                      <Button
                        variant={showLive ? 'default' : 'outline'}
                        size="sm"
                        onClick={() => setShowLive(!showLive)}
                      >
                        <Radio className="w-3.5 h-3.5" />
                        {showLive ? 'Watching' : 'Go Live'}
                      </Button>
                      <CopyButton text={JSON.stringify(contextDetail, null, 2)} label="JSON" />
                    </div>
                  </div>

                  {contextDetail.installed_packages?.length > 0 && (
                    <div className="mb-4">
                      <p className="text-xs font-medium text-muted-foreground mb-2 flex items-center gap-1"><Package className="w-3 h-3" /> Installed Packages</p>
                      <div className="flex flex-wrap gap-1.5">
                        {contextDetail.installed_packages.map((p: any, i: number) => (
                          <Badge key={i}>{p.name}{p.version ? `@${p.version}` : ''}</Badge>
                        ))}
                      </div>
                    </div>
                  )}

                  {contextDetail.previous_failures?.length > 0 && (
                    <div className="mb-4">
                      <p className="text-xs font-medium text-muted-foreground mb-2 flex items-center gap-1"><XCircle className="w-3 h-3" /> Previous Failures</p>
                      {contextDetail.previous_failures.map((f: any, i: number) => (
                        <div key={i} className="flex items-start gap-2 py-1.5 text-xs">
                          <Badge variant="destructive">{f.test || 'unknown'}</Badge>
                          <span className="text-muted-foreground">{f.error || '—'}</span>
                        </div>
                      ))}
                    </div>
                  )}

                  {contextDetail.patterns && Object.keys(contextDetail.patterns).length > 0 && (
                    <div>
                      <p className="text-xs font-medium text-muted-foreground mb-2 flex items-center gap-1"><Lightbulb className="w-3 h-3" /> Learned Patterns</p>
                      <div className="grid sm:grid-cols-2 gap-2">
                        {Object.entries(contextDetail.patterns).map(([k, v]) => (
                          <div key={k} className="bg-background p-3 rounded-md border border-border">
                            <p className="text-xs font-mono text-primary">{k}</p>
                            <p className="text-xs text-muted-foreground mt-0.5">{String(v)}</p>
                          </div>
                        ))}
                      </div>
                    </div>
                  )}
                </Card>
              )}

              {showLive && <LiveStream branch={selectedBranch} />}

              <Card>
                <div className="flex items-center justify-between px-4 py-3 border-b border-border">
                  <p className="text-xs font-medium text-foreground flex items-center gap-1.5"><Activity className="w-3.5 h-3.5" /> Event History</p>
                  <Select
                    value={eventsFilter}
                    onChange={e => setEventsFilter(e.target.value)}
                    options={[
                      { value: '', label: 'All types' },
                      ...Object.entries(EVENT_TYPES).map(([k, v]) => ({ value: k, label: v.label })),
                    ]}
                    aria-label="Filter events by type"
                    className="h-7 text-[10px] w-32"
                  />
                </div>
                <div className="px-4 max-h-96 overflow-y-auto">
                  {!events || events.length === 0 ? (
                    <div className="py-10 text-center text-xs text-muted-foreground">No events yet</div>
                  ) : (
                    events.map((e: any, i: number) => <EventCard key={e.id || i} event={e} />)
                  )}
                </div>
                <div className="px-4 py-2 border-t border-border">
                  <p className="text-[10px] text-muted-foreground flex items-center gap-1.5">
                    <Terminal className="w-3 h-3" /> gc context events --branch {selectedBranch}
                  </p>
                </div>
              </Card>
            </>
          )}
        </div>
      </div>
    </div>
  )
}
