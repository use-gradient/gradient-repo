import { useState, useCallback } from 'react'
import { api } from '@/api/client'
import { useFetch, usePolling } from '@/hooks/useAPI'
import { cn, timeAgo, formatDate } from '@/lib/utils'
import {
  Button, Card, Badge, EmptyState, Skeleton, useToast, StatusDot, ProgressBar,
  Table, TableRow, TableCell, Tooltip, Select, Modal,
} from '@/components/ui'
import {
  GitBranch, GitMerge, RefreshCw, AlertTriangle, CheckCircle2, XCircle,
  Clock, FileCode, ChevronRight, ChevronDown, Shield, Layers,
  ArrowRight, Package, Diff, TriangleAlert, CircleDot,
} from 'lucide-react'

/* ─── Types ─── */

interface MergeStatus {
  task_id: string
  task_title: string
  branch: string
  base_branch: string
  health: 'green' | 'yellow' | 'red'
  health_reason?: string
  conflicts: ConflictEntry[]
  files_changed: number
  insertions: number
  deletions: number
  mergeable: boolean
  last_checked_at?: string
  ci_status?: 'passing' | 'failing' | 'pending' | 'unknown'
  ahead_by?: number
  behind_by?: number
}

interface ConflictEntry {
  file_path: string
  conflict_type: 'content' | 'rename' | 'delete' | 'mode'
  severity: 'low' | 'medium' | 'high'
  detected_at: string
  resolved: boolean
  resolved_at?: string
  ours_snippet?: string
  theirs_snippet?: string
}

interface ChangeBundle {
  id: string
  session_id: string
  created_at: string
  description?: string
  files_changed: number
  insertions: number
  deletions: number
  status: 'pending' | 'applied' | 'rejected' | 'merged'
  commit_sha?: string
  agent_id?: string
  agent_role?: string
}

interface Session {
  id: string
  task_id: string
  branch: string
  status: string
  created_at: string
  agent_id?: string
}

const HEALTH_CONFIG: Record<string, { label: string; color: string; bgColor: string; icon: any }> = {
  green:  { label: 'Healthy',  color: 'text-emerald-400', bgColor: 'bg-emerald-500/10 border-emerald-500/20', icon: CheckCircle2 },
  yellow: { label: 'Warning',  color: 'text-yellow-400',  bgColor: 'bg-yellow-500/10 border-yellow-500/20',   icon: AlertTriangle },
  red:    { label: 'Critical', color: 'text-destructive',  bgColor: 'bg-destructive/10 border-destructive/20', icon: XCircle },
}

const SEVERITY_CONFIG: Record<string, { label: string; color: string }> = {
  low:    { label: 'Low',    color: 'bg-blue-500/10 text-blue-400' },
  medium: { label: 'Medium', color: 'bg-yellow-500/10 text-yellow-400' },
  high:   { label: 'High',   color: 'bg-destructive/10 text-destructive' },
}

const BUNDLE_STATUS: Record<string, { label: string; color: string }> = {
  pending:  { label: 'Pending',  color: 'bg-yellow-500/10 text-yellow-400 border-yellow-500/20' },
  applied:  { label: 'Applied',  color: 'bg-primary/10 text-primary border-primary/20' },
  rejected: { label: 'Rejected', color: 'bg-destructive/10 text-destructive border-destructive/20' },
  merged:   { label: 'Merged',   color: 'bg-emerald-500/10 text-emerald-400 border-emerald-500/20' },
}

/* ─── Health Indicator Card ─── */
function HealthIndicator({ mergeStatus }: { mergeStatus: MergeStatus }) {
  const health = HEALTH_CONFIG[mergeStatus.health] || HEALTH_CONFIG.green
  const HealthIcon = health.icon

  return (
    <Card className={cn('p-5 border', health.bgColor)}>
      <div className="flex items-center gap-4">
        <div className={cn('w-12 h-12 rounded-xl flex items-center justify-center', health.bgColor)}>
          <HealthIcon className={cn('w-6 h-6', health.color)} />
        </div>
        <div className="flex-1">
          <div className="flex items-center gap-2 mb-1">
            <h3 className={cn('text-lg font-bold', health.color)}>{health.label}</h3>
            {mergeStatus.ci_status && (
              <Badge className={cn(
                'text-[10px]',
                mergeStatus.ci_status === 'passing' ? 'bg-emerald-500/10 text-emerald-400'
                  : mergeStatus.ci_status === 'failing' ? 'bg-destructive/10 text-destructive'
                  : 'bg-yellow-500/10 text-yellow-400',
              )}>
                CI: {mergeStatus.ci_status}
              </Badge>
            )}
          </div>
          <p className="text-sm text-muted-foreground">
            {mergeStatus.health_reason || `Integration branch is ${mergeStatus.mergeable ? 'mergeable' : 'not mergeable'}`}
          </p>
        </div>
        <div className="text-right shrink-0">
          <div className="flex items-center gap-1 text-xs text-muted-foreground">
            <GitBranch className="w-3 h-3" />
            <span className="font-mono">{mergeStatus.branch}</span>
          </div>
          <div className="flex items-center gap-1 text-[10px] text-muted-foreground mt-1">
            <ArrowRight className="w-2.5 h-2.5" />
            <span className="font-mono">{mergeStatus.base_branch}</span>
          </div>
        </div>
      </div>

      <div className="grid grid-cols-4 gap-3 mt-4 pt-4 border-t border-border">
        <div>
          <p className="text-[10px] text-muted-foreground">Files Changed</p>
          <p className="text-sm font-semibold text-foreground">{mergeStatus.files_changed}</p>
        </div>
        <div>
          <p className="text-[10px] text-muted-foreground">Insertions</p>
          <p className="text-sm font-semibold text-emerald-400">+{mergeStatus.insertions}</p>
        </div>
        <div>
          <p className="text-[10px] text-muted-foreground">Deletions</p>
          <p className="text-sm font-semibold text-destructive">-{mergeStatus.deletions}</p>
        </div>
        <div>
          <p className="text-[10px] text-muted-foreground">Conflicts</p>
          <p className={cn(
            'text-sm font-semibold',
            mergeStatus.conflicts.length > 0 ? 'text-destructive' : 'text-emerald-400',
          )}>
            {mergeStatus.conflicts.length}
          </p>
        </div>
      </div>

      {(mergeStatus.ahead_by != null || mergeStatus.behind_by != null) && (
        <div className="flex items-center gap-4 mt-3 text-[10px] text-muted-foreground">
          {mergeStatus.ahead_by != null && <span>{mergeStatus.ahead_by} commits ahead</span>}
          {mergeStatus.behind_by != null && (
            <span className={mergeStatus.behind_by > 5 ? 'text-yellow-400' : ''}>
              {mergeStatus.behind_by} commits behind
            </span>
          )}
          {mergeStatus.last_checked_at && <span>checked {timeAgo(mergeStatus.last_checked_at)}</span>}
        </div>
      )}
    </Card>
  )
}

/* ─── Conflict Timeline ─── */
function ConflictTimeline({ conflicts }: { conflicts: ConflictEntry[] }) {
  const [expandedFile, setExpandedFile] = useState<string | null>(null)

  if (conflicts.length === 0) {
    return (
      <Card className="p-6 text-center">
        <CheckCircle2 className="w-8 h-8 text-emerald-400 mx-auto mb-2" />
        <p className="text-sm font-medium text-foreground">No Conflicts</p>
        <p className="text-xs text-muted-foreground mt-1">All changes merge cleanly with the base branch.</p>
      </Card>
    )
  }

  const sorted = [...conflicts].sort((a, b) => {
    const severityOrder = { high: 0, medium: 1, low: 2 }
    return (severityOrder[a.severity] || 2) - (severityOrder[b.severity] || 2)
  })

  return (
    <Card className="overflow-hidden">
      <div className="px-4 py-3 border-b border-border flex items-center justify-between">
        <p className="text-xs font-medium text-foreground flex items-center gap-1.5">
          <TriangleAlert className="w-3.5 h-3.5 text-yellow-400" /> Conflict Timeline
        </p>
        <div className="flex items-center gap-2 text-[10px] text-muted-foreground">
          <span>{conflicts.filter(c => c.resolved).length}/{conflicts.length} resolved</span>
        </div>
      </div>

      <div className="divide-y divide-border">
        {sorted.map((conflict, i) => {
          const isExpanded = expandedFile === conflict.file_path
          const sevCfg = SEVERITY_CONFIG[conflict.severity] || SEVERITY_CONFIG.low
          return (
            <div key={i}>
              <button
                className="w-full flex items-center gap-3 px-4 py-3 text-left hover:bg-secondary/30 transition-colors"
                onClick={() => setExpandedFile(isExpanded ? null : conflict.file_path)}
              >
                {isExpanded
                  ? <ChevronDown className="w-3.5 h-3.5 text-muted-foreground shrink-0" />
                  : <ChevronRight className="w-3.5 h-3.5 text-muted-foreground shrink-0" />
                }
                <div className="flex items-center gap-2 flex-1 min-w-0">
                  <FileCode className="w-3.5 h-3.5 text-muted-foreground shrink-0" />
                  <span className="text-sm font-mono text-foreground truncate">{conflict.file_path}</span>
                </div>
                <div className="flex items-center gap-2 shrink-0">
                  <Badge className={cn('text-[10px]', sevCfg.color)}>{sevCfg.label}</Badge>
                  <Badge className="text-[10px]">{conflict.conflict_type}</Badge>
                  {conflict.resolved ? (
                    <CheckCircle2 className="w-3.5 h-3.5 text-emerald-400" />
                  ) : (
                    <CircleDot className="w-3.5 h-3.5 text-yellow-400" />
                  )}
                </div>
              </button>

              {isExpanded && (
                <div className="px-4 pb-3 ml-8 space-y-2">
                  <div className="flex items-center gap-4 text-[10px] text-muted-foreground">
                    <span>Detected {timeAgo(conflict.detected_at)}</span>
                    {conflict.resolved && conflict.resolved_at && (
                      <span className="text-emerald-400">Resolved {timeAgo(conflict.resolved_at)}</span>
                    )}
                  </div>
                  {conflict.ours_snippet && (
                    <div className="rounded-md overflow-hidden border border-border">
                      <div className="px-3 py-1.5 bg-emerald-500/5 border-b border-border">
                        <span className="text-[10px] text-emerald-400 font-medium">Ours (integration branch)</span>
                      </div>
                      <pre className="px-3 py-2 text-xs font-mono text-muted-foreground overflow-x-auto bg-background">
                        {conflict.ours_snippet}
                      </pre>
                    </div>
                  )}
                  {conflict.theirs_snippet && (
                    <div className="rounded-md overflow-hidden border border-border">
                      <div className="px-3 py-1.5 bg-destructive/5 border-b border-border">
                        <span className="text-[10px] text-destructive font-medium">Theirs (base branch)</span>
                      </div>
                      <pre className="px-3 py-2 text-xs font-mono text-muted-foreground overflow-x-auto bg-background">
                        {conflict.theirs_snippet}
                      </pre>
                    </div>
                  )}
                </div>
              )}
            </div>
          )
        })}
      </div>
    </Card>
  )
}

/* ─── Change Bundle History ─── */
function BundleHistory({ taskId }: { taskId: string }) {
  const { data: sessions, loading: sessionsLoading } = useFetch<Session[]>(
    useCallback((token: string, orgId: string) =>
      api.sessions.list(token, orgId, { task_id: taskId }),
    [taskId]),
    [taskId]
  )

  const [selectedSessionId, setSelectedSessionId] = useState<string | null>(null)

  const { data: bundles, loading: bundlesLoading } = useFetch<ChangeBundle[]>(
    useCallback((token: string, orgId: string) =>
      selectedSessionId ? api.sessions.bundles(token, orgId, selectedSessionId) : Promise.resolve([]),
    [selectedSessionId]),
    [selectedSessionId]
  )

  return (
    <Card className="overflow-hidden">
      <div className="px-4 py-3 border-b border-border flex items-center justify-between">
        <p className="text-xs font-medium text-foreground flex items-center gap-1.5">
          <Package className="w-3.5 h-3.5 text-primary" /> Change Bundles
        </p>
        {sessions && sessions.length > 0 && (
          <Select
            value={selectedSessionId || ''}
            onChange={e => setSelectedSessionId(e.target.value || null)}
            options={[
              { value: '', label: 'Select session…' },
              ...sessions.map(s => ({
                value: s.id,
                label: `${s.branch} — ${timeAgo(s.created_at)}`,
              })),
            ]}
            className="h-7 text-[10px] w-48"
            aria-label="Select session"
          />
        )}
      </div>

      {sessionsLoading ? (
        <div className="p-4 space-y-2">
          <Skeleton className="h-8 w-full" />
          <Skeleton className="h-8 w-full" />
        </div>
      ) : !selectedSessionId ? (
        <div className="py-8 text-center text-xs text-muted-foreground">
          <Layers className="w-5 h-5 mx-auto mb-2" />
          <p>Select a session to view change bundles</p>
        </div>
      ) : bundlesLoading ? (
        <div className="p-4 space-y-2">
          <Skeleton className="h-12 w-full" />
          <Skeleton className="h-12 w-full" />
        </div>
      ) : !bundles || bundles.length === 0 ? (
        <div className="py-8 text-center text-xs text-muted-foreground">
          <Package className="w-5 h-5 mx-auto mb-2" />
          <p>No change bundles in this session</p>
        </div>
      ) : (
        <div className="divide-y divide-border">
          {bundles.map(bundle => {
            const statusCfg = BUNDLE_STATUS[bundle.status] || BUNDLE_STATUS.pending
            return (
              <div key={bundle.id} className="px-4 py-3 hover:bg-secondary/20 transition-colors">
                <div className="flex items-center gap-3">
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2">
                      <Diff className="w-3.5 h-3.5 text-muted-foreground shrink-0" />
                      <span className="text-sm font-medium text-foreground truncate">
                        {bundle.description || `Bundle ${bundle.id.slice(0, 8)}`}
                      </span>
                    </div>
                    <div className="flex items-center gap-3 mt-1 text-[10px] text-muted-foreground">
                      <span>{bundle.files_changed} files</span>
                      <span className="text-emerald-400">+{bundle.insertions}</span>
                      <span className="text-destructive">-{bundle.deletions}</span>
                      {bundle.commit_sha && (
                        <span className="font-mono">{bundle.commit_sha.slice(0, 7)}</span>
                      )}
                      {bundle.agent_role && <span>by {bundle.agent_role}</span>}
                      <span>{timeAgo(bundle.created_at)}</span>
                    </div>
                  </div>
                  <Badge className={cn('text-[10px] shrink-0', statusCfg.color)}>{statusCfg.label}</Badge>
                </div>
              </div>
            )
          })}
        </div>
      )}
    </Card>
  )
}

/* ─── Main Component ─── */
export default function MergeSimulationView() {
  const [selectedTaskId, setSelectedTaskId] = useState<string | null>(null)

  const { data: tasks, loading: tasksLoading, refetch: refetchTasks } = useFetch(
    useCallback((token: string, orgId: string) =>
      api.tasks.list(token, orgId, { limit: '50' }),
    [])
  )

  const { data: mergeStatus, loading: mergeLoading, refetch: refetchMerge } = useFetch<MergeStatus>(
    useCallback((token: string, orgId: string) =>
      selectedTaskId ? api.mergeStatus.get(token, orgId, selectedTaskId) : Promise.resolve(null),
    [selectedTaskId]),
    [selectedTaskId]
  )

  usePolling(refetchMerge, 15000, !!selectedTaskId && mergeStatus?.health !== 'green')

  const activeTasks = tasks?.filter((t: any) => t.branch) || []

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-lg font-semibold text-foreground flex items-center gap-2">
            <GitMerge className="w-5 h-5 text-primary" /> Merge Simulation
          </h2>
          <p className="text-xs text-muted-foreground mt-0.5">Integration branch health, conflicts, and change bundle history</p>
        </div>
        <Button variant="ghost" size="sm" onClick={() => { refetchTasks(); refetchMerge() }}>
          <RefreshCw className="w-3.5 h-3.5" />
        </Button>
      </div>

      {/* Task selector */}
      <div className="flex items-center gap-3">
        <p className="text-xs text-muted-foreground shrink-0">Select task:</p>
        {tasksLoading ? (
          <Skeleton className="h-9 w-64" />
        ) : activeTasks.length === 0 ? (
          <p className="text-xs text-muted-foreground">No tasks with branches available</p>
        ) : (
          <div className="flex gap-1.5 flex-wrap">
            {activeTasks.map((task: any) => (
              <button
                key={task.id}
                onClick={() => setSelectedTaskId(task.id === selectedTaskId ? null : task.id)}
                className={cn(
                  'px-3 py-1.5 text-xs rounded-md border transition-colors flex items-center gap-1.5',
                  selectedTaskId === task.id
                    ? 'bg-primary/10 text-primary border-primary/30'
                    : 'text-muted-foreground border-border hover:border-muted-foreground/30',
                )}
              >
                <GitBranch className="w-3 h-3" />
                <span className="font-mono truncate max-w-[120px]">{task.branch}</span>
              </button>
            ))}
          </div>
        )}
      </div>

      {!selectedTaskId ? (
        <EmptyState
          icon={GitMerge}
          title="Select a task branch"
          description="Choose a task with an active branch to view merge simulation results, conflict analysis, and change bundle history."
        />
      ) : mergeLoading ? (
        <div className="space-y-4">
          <Skeleton className="h-40 w-full" />
          <Skeleton className="h-32 w-full" />
          <Skeleton className="h-48 w-full" />
        </div>
      ) : !mergeStatus ? (
        <EmptyState
          icon={GitMerge}
          title="No merge data"
          description="Merge simulation data is not yet available for this task. It will appear once the agent begins making changes."
        />
      ) : (
        <>
          <HealthIndicator mergeStatus={mergeStatus} />
          <ConflictTimeline conflicts={mergeStatus.conflicts} />
          <BundleHistory taskId={selectedTaskId} />
        </>
      )}
    </div>
  )
}
