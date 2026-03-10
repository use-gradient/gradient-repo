import { useState, useCallback, useMemo, useRef, useEffect } from 'react'
import { api } from '@/api/client'
import { useFetch } from '@/hooks/useAPI'
import { cn, timeAgo, formatDuration, formatDate } from '@/lib/utils'
import {
  Button, Card, Badge, EmptyState, Skeleton, Select, Tooltip,
} from '@/components/ui'
import {
  History, GitBranch, Clock, RefreshCw, ChevronLeft, ChevronRight,
  FileCode, Play, Pause, SkipBack, SkipForward, Rewind,
  Package, Terminal, Diff, Eye, Code, Layers,
  ArrowRight, Maximize2, Minimize2,
} from 'lucide-react'

/* ─── Types ─── */

interface TaskSummary {
  id: string
  title: string
  branch?: string
  status: string
  created_at: string
}

interface LogEntry {
  id: string
  step: string
  message?: string
  status: string
  created_at: string
  agent_id?: string
  sub_task_id?: string
  snapshot?: SnapshotState
}

interface ChangeBundle {
  id: string
  session_id: string
  created_at: string
  description?: string
  files_changed: number
  insertions: number
  deletions: number
  status: string
  commit_sha?: string
  agent_id?: string
  agent_role?: string
  files?: FileChange[]
}

interface FileChange {
  path: string
  action: 'added' | 'modified' | 'deleted' | 'renamed'
  insertions: number
  deletions: number
  diff_preview?: string
  old_path?: string
}

interface SnapshotState {
  timestamp: string
  context: ContextSnapshot
  files_modified: string[]
  agent_states: AgentSnapshot[]
}

interface ContextSnapshot {
  branch: string
  base_os?: string
  installed_packages?: { name: string; version: string }[]
  patterns?: Record<string, string>
  active_failures?: string[]
}

interface AgentSnapshot {
  agent_id: string
  role: string
  status: string
  current_step?: string
  tokens_used?: number
}

interface TimelinePoint {
  id: string
  timestamp: string
  type: 'log' | 'bundle' | 'event'
  label: string
  detail?: string
  status?: string
  agentRole?: string
  snapshot?: SnapshotState
  bundle?: ChangeBundle
}

/* ─── Timeline Scrubber ─── */
function TimelineScrubber({ points, currentIndex, onChange, isPlaying, onPlayToggle }: {
  points: TimelinePoint[]
  currentIndex: number
  onChange: (index: number) => void
  isPlaying: boolean
  onPlayToggle: () => void
}) {
  const trackRef = useRef<HTMLDivElement>(null)

  const handleTrackClick = (e: React.MouseEvent<HTMLDivElement>) => {
    if (!trackRef.current || points.length === 0) return
    const rect = trackRef.current.getBoundingClientRect()
    const pct = (e.clientX - rect.left) / rect.width
    const idx = Math.round(pct * (points.length - 1))
    onChange(Math.max(0, Math.min(points.length - 1, idx)))
  }

  const progress = points.length > 1 ? (currentIndex / (points.length - 1)) * 100 : 0

  return (
    <Card className="p-4">
      {/* Transport controls */}
      <div className="flex items-center gap-2 mb-3">
        <Tooltip text="Jump to start">
          <button
            onClick={() => onChange(0)}
            disabled={points.length === 0}
            className="p-1.5 rounded-md hover:bg-secondary transition-colors disabled:opacity-50"
          >
            <SkipBack className="w-3.5 h-3.5 text-muted-foreground" />
          </button>
        </Tooltip>
        <Tooltip text="Step back">
          <button
            onClick={() => onChange(Math.max(0, currentIndex - 1))}
            disabled={currentIndex <= 0}
            className="p-1.5 rounded-md hover:bg-secondary transition-colors disabled:opacity-50"
          >
            <ChevronLeft className="w-3.5 h-3.5 text-muted-foreground" />
          </button>
        </Tooltip>
        <Tooltip text={isPlaying ? 'Pause' : 'Play'}>
          <button
            onClick={onPlayToggle}
            disabled={points.length === 0}
            className={cn(
              'p-2 rounded-md transition-colors',
              isPlaying ? 'bg-primary/10 text-primary' : 'hover:bg-secondary text-muted-foreground',
            )}
          >
            {isPlaying
              ? <Pause className="w-4 h-4" />
              : <Play className="w-4 h-4" />
            }
          </button>
        </Tooltip>
        <Tooltip text="Step forward">
          <button
            onClick={() => onChange(Math.min(points.length - 1, currentIndex + 1))}
            disabled={currentIndex >= points.length - 1}
            className="p-1.5 rounded-md hover:bg-secondary transition-colors disabled:opacity-50"
          >
            <ChevronRight className="w-3.5 h-3.5 text-muted-foreground" />
          </button>
        </Tooltip>
        <Tooltip text="Jump to end">
          <button
            onClick={() => onChange(points.length - 1)}
            disabled={points.length === 0}
            className="p-1.5 rounded-md hover:bg-secondary transition-colors disabled:opacity-50"
          >
            <SkipForward className="w-3.5 h-3.5 text-muted-foreground" />
          </button>
        </Tooltip>

        <div className="flex-1" />

        <span className="text-[10px] text-muted-foreground font-mono">
          {points.length > 0 ? `${currentIndex + 1} / ${points.length}` : '0 / 0'}
        </span>
      </div>

      {/* Timeline track */}
      <div
        ref={trackRef}
        className="relative h-8 cursor-pointer group"
        onClick={handleTrackClick}
      >
        {/* Background track */}
        <div className="absolute top-1/2 -translate-y-1/2 left-0 right-0 h-1.5 rounded-full bg-secondary" />

        {/* Progress fill */}
        <div
          className="absolute top-1/2 -translate-y-1/2 left-0 h-1.5 rounded-full bg-primary transition-all duration-150"
          style={{ width: `${progress}%` }}
        />

        {/* Event markers */}
        {points.map((point, i) => {
          const pct = points.length > 1 ? (i / (points.length - 1)) * 100 : 50
          return (
            <button
              key={point.id}
              className={cn(
                'absolute top-1/2 -translate-y-1/2 -translate-x-1/2 w-2 h-2 rounded-full transition-all z-10',
                i === currentIndex ? 'w-3.5 h-3.5 ring-2 ring-primary ring-offset-1 ring-offset-background' : '',
                point.type === 'bundle' ? 'bg-violet-400'
                  : point.status === 'failed' ? 'bg-destructive'
                  : point.status === 'completed' ? 'bg-emerald-400'
                  : 'bg-muted-foreground',
              )}
              style={{ left: `${pct}%` }}
              onClick={(e) => { e.stopPropagation(); onChange(i) }}
              title={point.label}
            />
          )
        })}

        {/* Playhead */}
        {points.length > 0 && (
          <div
            className="absolute top-0 bottom-0 -translate-x-1/2 w-0.5 bg-primary z-20 transition-all duration-150"
            style={{ left: `${progress}%` }}
          />
        )}
      </div>

      {/* Time range labels */}
      {points.length > 0 && (
        <div className="flex items-center justify-between mt-1 text-[10px] text-muted-foreground">
          <span>{timeAgo(points[0].timestamp)}</span>
          {points[currentIndex] && (
            <span className="text-primary font-medium">
              {new Date(points[currentIndex].timestamp).toLocaleTimeString()}
            </span>
          )}
          <span>{timeAgo(points[points.length - 1].timestamp)}</span>
        </div>
      )}
    </Card>
  )
}

/* ─── Snapshot Viewer ─── */
function SnapshotViewer({ point }: { point: TimelinePoint | null }) {
  const [activeTab, setActiveTab] = useState<'overview' | 'context' | 'diff' | 'agents'>('overview')

  if (!point) {
    return (
      <Card className="p-8 text-center">
        <History className="w-8 h-8 text-muted-foreground mx-auto mb-3" />
        <p className="text-sm font-medium text-foreground">Select a point in time</p>
        <p className="text-xs text-muted-foreground mt-1">Use the timeline scrubber above to navigate through the task's history.</p>
      </Card>
    )
  }

  const snapshot = point.snapshot
  const bundle = point.bundle

  return (
    <div className="space-y-4">
      {/* Current point summary */}
      <Card className="p-4">
        <div className="flex items-center gap-3">
          <div className={cn(
            'w-10 h-10 rounded-lg flex items-center justify-center shrink-0',
            point.type === 'bundle' ? 'bg-violet-500/10'
              : point.status === 'failed' ? 'bg-destructive/10'
              : point.status === 'completed' ? 'bg-emerald-500/10'
              : 'bg-secondary',
          )}>
            {point.type === 'bundle'
              ? <Package className="w-5 h-5 text-violet-400" />
              : point.type === 'log'
              ? <Terminal className="w-5 h-5 text-primary" />
              : <Layers className="w-5 h-5 text-muted-foreground" />
            }
          </div>
          <div className="flex-1 min-w-0">
            <p className="text-sm font-medium text-foreground">{point.label}</p>
            <div className="flex items-center gap-3 mt-0.5">
              {point.agentRole && <Badge className="text-[10px]">{point.agentRole}</Badge>}
              {point.status && (
                <Badge className={cn(
                  'text-[10px]',
                  point.status === 'completed' ? 'bg-emerald-500/10 text-emerald-400'
                    : point.status === 'failed' ? 'bg-destructive/10 text-destructive'
                    : 'bg-yellow-500/10 text-yellow-400',
                )}>
                  {point.status}
                </Badge>
              )}
              <span className="text-[10px] text-muted-foreground">
                {new Date(point.timestamp).toLocaleString()}
              </span>
            </div>
          </div>
        </div>
        {point.detail && (
          <p className="text-xs text-muted-foreground mt-3 whitespace-pre-wrap">{point.detail}</p>
        )}
      </Card>

      {/* Tab bar */}
      <div className="inline-flex h-9 items-center justify-center rounded-lg bg-muted p-1 text-muted-foreground" role="tablist">
        {([
          { id: 'overview', label: 'Overview', icon: <Eye className="w-3.5 h-3.5" /> },
          { id: 'context', label: 'Context', icon: <Code className="w-3.5 h-3.5" /> },
          { id: 'diff', label: 'Changes', icon: <Diff className="w-3.5 h-3.5" /> },
          { id: 'agents', label: 'Agents', icon: <Layers className="w-3.5 h-3.5" /> },
        ] as const).map(tab => (
          <button
            key={tab.id}
            role="tab"
            aria-selected={activeTab === tab.id}
            onClick={() => setActiveTab(tab.id)}
            className={cn(
              'inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-md px-3 py-1 text-sm font-medium ring-offset-background transition-all',
              'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2',
              activeTab === tab.id ? 'bg-background text-foreground shadow' : 'hover:text-foreground',
            )}
          >
            {tab.icon}
            {tab.label}
          </button>
        ))}
      </div>

      {/* Tab content */}
      {activeTab === 'overview' && (
        <Card className="p-4 space-y-3">
          <div className="grid grid-cols-2 gap-3">
            <div className="p-3 rounded-md bg-secondary/50">
              <p className="text-[10px] text-muted-foreground mb-0.5">Timestamp</p>
              <p className="text-xs font-mono text-foreground">{new Date(point.timestamp).toLocaleString()}</p>
            </div>
            <div className="p-3 rounded-md bg-secondary/50">
              <p className="text-[10px] text-muted-foreground mb-0.5">Type</p>
              <p className="text-xs font-medium text-foreground capitalize">{point.type}</p>
            </div>
          </div>
          {snapshot && snapshot.files_modified.length > 0 && (
            <div>
              <p className="text-xs font-medium text-muted-foreground mb-2 flex items-center gap-1.5">
                <FileCode className="w-3.5 h-3.5" /> Modified Files at This Point
              </p>
              <div className="space-y-1">
                {snapshot.files_modified.map(file => (
                  <div key={file} className="flex items-center gap-2 py-1 px-2 rounded-md bg-background border border-border">
                    <FileCode className="w-3 h-3 text-primary shrink-0" />
                    <span className="text-xs font-mono text-foreground truncate">{file}</span>
                  </div>
                ))}
              </div>
            </div>
          )}
        </Card>
      )}

      {activeTab === 'context' && (
        <Card className="p-4 space-y-4">
          {snapshot?.context ? (
            <>
              <div className="flex items-center gap-2 mb-2">
                <GitBranch className="w-3.5 h-3.5 text-primary" />
                <span className="text-sm font-medium text-foreground">{snapshot.context.branch}</span>
                {snapshot.context.base_os && <Badge className="text-[10px]">{snapshot.context.base_os}</Badge>}
              </div>

              {snapshot.context.installed_packages && snapshot.context.installed_packages.length > 0 && (
                <div>
                  <p className="text-xs font-medium text-muted-foreground mb-2">Installed Packages</p>
                  <div className="flex flex-wrap gap-1.5">
                    {snapshot.context.installed_packages.map((p, i) => (
                      <Badge key={i} className="text-[10px]">{p.name}{p.version ? `@${p.version}` : ''}</Badge>
                    ))}
                  </div>
                </div>
              )}

              {snapshot.context.patterns && Object.keys(snapshot.context.patterns).length > 0 && (
                <div>
                  <p className="text-xs font-medium text-muted-foreground mb-2">Learned Patterns</p>
                  <div className="grid sm:grid-cols-2 gap-2">
                    {Object.entries(snapshot.context.patterns).map(([k, v]) => (
                      <div key={k} className="p-2 rounded-md bg-background border border-border">
                        <p className="text-[10px] font-mono text-primary">{k}</p>
                        <p className="text-[10px] text-muted-foreground">{v}</p>
                      </div>
                    ))}
                  </div>
                </div>
              )}

              {snapshot.context.active_failures && snapshot.context.active_failures.length > 0 && (
                <div>
                  <p className="text-xs font-medium text-destructive mb-2">Active Failures</p>
                  {snapshot.context.active_failures.map((f, i) => (
                    <div key={i} className="flex items-center gap-2 py-1">
                      <span className="w-1.5 h-1.5 rounded-full bg-destructive shrink-0" />
                      <span className="text-xs text-muted-foreground font-mono">{f}</span>
                    </div>
                  ))}
                </div>
              )}
            </>
          ) : (
            <div className="py-6 text-center text-xs text-muted-foreground">
              <Code className="w-5 h-5 mx-auto mb-2" />
              <p>No context snapshot available at this point</p>
            </div>
          )}
        </Card>
      )}

      {activeTab === 'diff' && (
        <Card className="overflow-hidden">
          <div className="px-4 py-3 border-b border-border">
            <p className="text-xs font-medium text-foreground flex items-center gap-1.5">
              <Diff className="w-3.5 h-3.5 text-primary" /> Changes
            </p>
          </div>
          {bundle && bundle.files ? (
            <div className="divide-y divide-border">
              {bundle.files.map((file, i) => (
                <div key={i} className="px-4 py-3">
                  <div className="flex items-center gap-2 mb-2">
                    <Badge className={cn(
                      'text-[10px]',
                      file.action === 'added' ? 'bg-emerald-500/10 text-emerald-400'
                        : file.action === 'deleted' ? 'bg-destructive/10 text-destructive'
                        : file.action === 'renamed' ? 'bg-violet-500/10 text-violet-400'
                        : 'bg-yellow-500/10 text-yellow-400',
                    )}>
                      {file.action}
                    </Badge>
                    <span className="text-xs font-mono text-foreground truncate">{file.path}</span>
                    {file.old_path && (
                      <span className="text-[10px] text-muted-foreground flex items-center gap-1">
                        <ArrowRight className="w-2.5 h-2.5" /> from {file.old_path}
                      </span>
                    )}
                    <span className="text-[10px] text-muted-foreground ml-auto shrink-0">
                      <span className="text-emerald-400">+{file.insertions}</span>
                      {' '}
                      <span className="text-destructive">-{file.deletions}</span>
                    </span>
                  </div>
                  {file.diff_preview && (
                    <pre className="p-2 rounded-md bg-background border border-border text-[10px] font-mono text-muted-foreground overflow-x-auto max-h-32 overflow-y-auto">
                      {file.diff_preview}
                    </pre>
                  )}
                </div>
              ))}
            </div>
          ) : snapshot && snapshot.files_modified.length > 0 ? (
            <div className="px-4 py-3 space-y-1">
              {snapshot.files_modified.map(file => (
                <div key={file} className="flex items-center gap-2 py-1">
                  <FileCode className="w-3 h-3 text-muted-foreground" />
                  <span className="text-xs font-mono text-foreground">{file}</span>
                </div>
              ))}
            </div>
          ) : (
            <div className="py-8 text-center text-xs text-muted-foreground">
              <Diff className="w-5 h-5 mx-auto mb-2" />
              <p>No diff data available at this point</p>
            </div>
          )}
        </Card>
      )}

      {activeTab === 'agents' && (
        <Card className="overflow-hidden">
          <div className="px-4 py-3 border-b border-border">
            <p className="text-xs font-medium text-foreground flex items-center gap-1.5">
              <Layers className="w-3.5 h-3.5 text-primary" /> Agent States
            </p>
          </div>
          {snapshot?.agent_states && snapshot.agent_states.length > 0 ? (
            <div className="divide-y divide-border">
              {snapshot.agent_states.map(agent => (
                <div key={agent.agent_id} className="px-4 py-3 flex items-center gap-3">
                  <div className={cn(
                    'w-8 h-8 rounded-md flex items-center justify-center shrink-0',
                    agent.status === 'working' ? 'bg-primary/10' : agent.status === 'error' ? 'bg-destructive/10' : 'bg-secondary',
                  )}>
                    <Layers className={cn(
                      'w-4 h-4',
                      agent.status === 'working' ? 'text-primary' : agent.status === 'error' ? 'text-destructive' : 'text-muted-foreground',
                    )} />
                  </div>
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2">
                      <span className="text-xs font-medium text-foreground capitalize">{agent.role}</span>
                      <Badge className={cn(
                        'text-[10px]',
                        agent.status === 'working' ? 'bg-primary/10 text-primary'
                          : agent.status === 'done' ? 'bg-emerald-500/10 text-emerald-400'
                          : agent.status === 'error' ? 'bg-destructive/10 text-destructive'
                          : 'bg-secondary text-muted-foreground',
                      )}>
                        {agent.status}
                      </Badge>
                    </div>
                    <div className="flex items-center gap-3 mt-0.5 text-[10px] text-muted-foreground">
                      <span className="font-mono">{agent.agent_id.slice(0, 12)}</span>
                      {agent.current_step && <span>{agent.current_step}</span>}
                      {agent.tokens_used != null && <span>{agent.tokens_used.toLocaleString()} tokens</span>}
                    </div>
                  </div>
                </div>
              ))}
            </div>
          ) : (
            <div className="py-8 text-center text-xs text-muted-foreground">
              <Layers className="w-5 h-5 mx-auto mb-2" />
              <p>No agent state data at this point</p>
            </div>
          )}
        </Card>
      )}
    </div>
  )
}

/* ─── Main Component ─── */
export default function TimeMachine() {
  const [selectedTaskId, setSelectedTaskId] = useState<string | null>(null)
  const [currentIndex, setCurrentIndex] = useState(0)
  const [isPlaying, setIsPlaying] = useState(false)
  const [playSpeed, setPlaySpeed] = useState(1000)
  const playTimerRef = useRef<ReturnType<typeof setInterval> | null>(null)

  const { data: tasks, loading: tasksLoading } = useFetch<TaskSummary[]>(
    useCallback((token: string, orgId: string) =>
      api.tasks.list(token, orgId, { limit: '50' }),
    [])
  )

  const { data: logs, loading: logsLoading } = useFetch<LogEntry[]>(
    useCallback((token: string, orgId: string) =>
      selectedTaskId ? api.tasks.logs(token, orgId, selectedTaskId) : Promise.resolve([]),
    [selectedTaskId]),
    [selectedTaskId]
  )

  const { data: sessions } = useFetch(
    useCallback((token: string, orgId: string) =>
      selectedTaskId ? api.sessions.list(token, orgId, { task_id: selectedTaskId }) : Promise.resolve([]),
    [selectedTaskId]),
    [selectedTaskId]
  )

  const firstSessionId = sessions?.[0]?.id
  const { data: bundles } = useFetch<ChangeBundle[]>(
    useCallback((token: string, orgId: string) =>
      firstSessionId ? api.sessions.bundles(token, orgId, firstSessionId) : Promise.resolve([]),
    [firstSessionId]),
    [firstSessionId]
  )

  const timelinePoints = useMemo<TimelinePoint[]>(() => {
    const points: TimelinePoint[] = []

    if (logs) {
      logs.forEach(log => {
        points.push({
          id: `log-${log.id}`,
          timestamp: log.created_at,
          type: 'log',
          label: log.step,
          detail: log.message,
          status: log.status,
          agentRole: log.agent_id ? 'agent' : undefined,
          snapshot: log.snapshot,
        })
      })
    }

    if (bundles) {
      bundles.forEach(bundle => {
        points.push({
          id: `bundle-${bundle.id}`,
          timestamp: bundle.created_at,
          type: 'bundle',
          label: bundle.description || `Change bundle (${bundle.files_changed} files)`,
          detail: `+${bundle.insertions} -${bundle.deletions} across ${bundle.files_changed} files`,
          status: bundle.status,
          agentRole: bundle.agent_role,
          bundle,
        })
      })
    }

    points.sort((a, b) => new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime())
    return points
  }, [logs, bundles])

  useEffect(() => {
    setCurrentIndex(0)
    setIsPlaying(false)
  }, [selectedTaskId])

  useEffect(() => {
    if (playTimerRef.current) {
      clearInterval(playTimerRef.current)
      playTimerRef.current = null
    }

    if (isPlaying && timelinePoints.length > 0) {
      playTimerRef.current = setInterval(() => {
        setCurrentIndex(prev => {
          if (prev >= timelinePoints.length - 1) {
            setIsPlaying(false)
            return prev
          }
          return prev + 1
        })
      }, playSpeed)
    }

    return () => {
      if (playTimerRef.current) clearInterval(playTimerRef.current)
    }
  }, [isPlaying, playSpeed, timelinePoints.length])

  const currentPoint = timelinePoints[currentIndex] || null
  const completedTasks = tasks?.filter((t: any) => ['complete', 'failed', 'cancelled'].includes(t.status)) || []

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-lg font-semibold text-foreground flex items-center gap-2">
            <History className="w-5 h-5 text-primary" /> Time Machine
          </h2>
          <p className="text-xs text-muted-foreground mt-0.5">Scrub through code and context states at any point in a task's history</p>
        </div>
        <div className="flex items-center gap-2">
          <Select
            value={String(playSpeed)}
            onChange={e => setPlaySpeed(Number(e.target.value))}
            options={[
              { value: '500', label: '2x Speed' },
              { value: '1000', label: '1x Speed' },
              { value: '2000', label: '0.5x Speed' },
              { value: '3000', label: '0.3x Speed' },
            ]}
            className="h-7 text-[10px] w-28"
            aria-label="Playback speed"
          />
        </div>
      </div>

      {/* Task selector */}
      <Card className="p-4">
        <p className="text-xs font-medium text-muted-foreground mb-3">Select a task to explore its history</p>
        {tasksLoading ? (
          <div className="flex gap-2">
            <Skeleton className="h-10 w-32" />
            <Skeleton className="h-10 w-32" />
            <Skeleton className="h-10 w-32" />
          </div>
        ) : !tasks || tasks.length === 0 ? (
          <p className="text-xs text-muted-foreground">No tasks available</p>
        ) : (
          <div className="flex gap-2 flex-wrap">
            {tasks.map((task: any) => (
              <button
                key={task.id}
                onClick={() => setSelectedTaskId(task.id === selectedTaskId ? null : task.id)}
                className={cn(
                  'px-3 py-2 text-xs rounded-md border transition-colors text-left',
                  selectedTaskId === task.id
                    ? 'bg-primary/10 text-primary border-primary/30'
                    : 'text-muted-foreground border-border hover:border-muted-foreground/30',
                )}
              >
                <span className="block font-medium truncate max-w-[160px]">{task.title}</span>
                <span className="flex items-center gap-1.5 mt-0.5">
                  {task.branch && (
                    <>
                      <GitBranch className="w-2.5 h-2.5" />
                      <span className="font-mono">{task.branch}</span>
                    </>
                  )}
                  <span className="text-[10px]">{timeAgo(task.created_at)}</span>
                </span>
              </button>
            ))}
          </div>
        )}
      </Card>

      {!selectedTaskId ? (
        <EmptyState
          icon={History}
          title="Choose a task"
          description="Select a task above to load its execution history. You can then scrub through each step to see the state of code, context, and agents at that moment."
        />
      ) : logsLoading ? (
        <div className="space-y-4">
          <Skeleton className="h-24 w-full" />
          <Skeleton className="h-64 w-full" />
        </div>
      ) : timelinePoints.length === 0 ? (
        <EmptyState
          icon={History}
          title="No history data"
          description="This task doesn't have any execution log entries or change bundles yet."
        />
      ) : (
        <>
          <TimelineScrubber
            points={timelinePoints}
            currentIndex={currentIndex}
            onChange={setCurrentIndex}
            isPlaying={isPlaying}
            onPlayToggle={() => setIsPlaying(!isPlaying)}
          />

          {/* Point list (mini sidebar) */}
          <div className="grid lg:grid-cols-12 gap-6">
            <div className="lg:col-span-3">
              <Card className="overflow-hidden">
                <div className="px-3 py-2 border-b border-border">
                  <p className="text-[10px] font-medium text-muted-foreground">{timelinePoints.length} points</p>
                </div>
                <div className="max-h-[500px] overflow-y-auto">
                  {timelinePoints.map((point, i) => (
                    <button
                      key={point.id}
                      onClick={() => { setCurrentIndex(i); setIsPlaying(false) }}
                      className={cn(
                        'w-full text-left px-3 py-2 border-b border-border last:border-0 transition-colors',
                        i === currentIndex ? 'bg-primary/10' : 'hover:bg-secondary/50',
                      )}
                    >
                      <div className="flex items-center gap-2">
                        <span className={cn(
                          'w-1.5 h-1.5 rounded-full shrink-0',
                          point.type === 'bundle' ? 'bg-violet-400'
                            : point.status === 'failed' ? 'bg-destructive'
                            : point.status === 'completed' ? 'bg-emerald-400'
                            : 'bg-muted-foreground',
                        )} />
                        <span className="text-[10px] font-medium text-foreground truncate">{point.label}</span>
                      </div>
                      <div className="flex items-center gap-2 mt-0.5 ml-3.5">
                        <span className="text-[9px] text-muted-foreground">
                          {new Date(point.timestamp).toLocaleTimeString()}
                        </span>
                        {point.agentRole && (
                          <span className="text-[9px] text-muted-foreground">{point.agentRole}</span>
                        )}
                      </div>
                    </button>
                  ))}
                </div>
              </Card>
            </div>

            <div className="lg:col-span-9">
              <SnapshotViewer point={currentPoint} />
            </div>
          </div>
        </>
      )}
    </div>
  )
}
