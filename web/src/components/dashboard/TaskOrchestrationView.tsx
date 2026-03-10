import { useState, useCallback, useEffect, useRef } from 'react'
import { api } from '@/api/client'
import { useFetch, useSSE, useAPIAuth, usePolling } from '@/hooks/useAPI'
import { cn, timeAgo, formatDuration } from '@/lib/utils'
import {
  Button, Card, Badge, EmptyState, Skeleton, useToast, StatusDot, ProgressBar, Tooltip,
} from '@/components/ui'
import {
  Bot, ChevronRight, ChevronDown, GitBranch, RefreshCw, Radio,
  Clock, CheckCircle2, AlertTriangle, Loader2, XCircle, Play,
  Layers, Users, FileCode, Zap, Terminal, ArrowRight,
} from 'lucide-react'

/* ─── Types ─── */

interface SubTask {
  id: string
  title: string
  status: 'pending' | 'running' | 'complete' | 'failed' | 'cancelled'
  agent_id?: string
  agent_role?: string
  started_at?: string
  completed_at?: string
  duration_seconds?: number
  output_summary?: string
  error_message?: string
  contract?: ContractInfo
  children?: SubTask[]
}

interface ContractInfo {
  id: string
  status: 'proposed' | 'accepted' | 'in_progress' | 'fulfilled' | 'breached'
  input_schema?: string
  output_schema?: string
  deadline?: string
}

interface TaskDetail {
  id: string
  title: string
  description?: string
  status: string
  branch?: string
  created_at?: string
  started_at?: string
  completed_at?: string
  duration_seconds?: number
  tokens_used?: number
  estimated_cost?: number
  pr_url?: string
  output_summary?: string
  error_message?: string
  sub_tasks?: SubTask[]
  agents?: AgentInfo[]
}

interface AgentInfo {
  id: string
  role: string
  status: 'idle' | 'working' | 'done' | 'error'
  current_task_id?: string
  tokens_used?: number
  started_at?: string
}

interface LogEntry {
  id: string
  step: string
  message?: string
  status: string
  created_at: string
  agent_id?: string
  sub_task_id?: string
}

const STATUS_CONFIG: Record<string, { label: string; color: string; icon: any; dot: string }> = {
  pending:   { label: 'Pending',   color: 'bg-yellow-500/10 text-yellow-400 border-yellow-500/20', icon: Clock,          dot: 'bg-yellow-400' },
  queued:    { label: 'Queued',    color: 'bg-blue-500/10 text-blue-400 border-blue-500/20',       icon: Clock,          dot: 'bg-blue-400' },
  running:   { label: 'Running',   color: 'bg-primary/10 text-primary border-primary/20',          icon: Loader2,        dot: 'bg-primary' },
  complete:  { label: 'Complete',  color: 'bg-emerald-500/10 text-emerald-400 border-emerald-500/20', icon: CheckCircle2, dot: 'bg-emerald-400' },
  failed:    { label: 'Failed',    color: 'bg-destructive/10 text-destructive border-destructive/20', icon: AlertTriangle, dot: 'bg-destructive' },
  cancelled: { label: 'Cancelled', color: 'bg-muted text-muted-foreground border-border',          icon: XCircle,        dot: 'bg-muted-foreground' },
}

const CONTRACT_STATUS: Record<string, { label: string; color: string }> = {
  proposed:    { label: 'Proposed',    color: 'bg-blue-500/10 text-blue-400' },
  accepted:    { label: 'Accepted',    color: 'bg-violet-500/10 text-violet-400' },
  in_progress: { label: 'In Progress', color: 'bg-primary/10 text-primary' },
  fulfilled:   { label: 'Fulfilled',   color: 'bg-emerald-500/10 text-emerald-400' },
  breached:    { label: 'Breached',    color: 'bg-destructive/10 text-destructive' },
}

const AGENT_ROLES: Record<string, { icon: any; color: string }> = {
  manager:    { icon: Users,    color: 'text-violet-400' },
  coder:      { icon: FileCode, color: 'text-primary' },
  reviewer:   { icon: Zap,      color: 'text-yellow-400' },
  tester:     { icon: Terminal,  color: 'text-emerald-400' },
}

/* ─── Sub-task Tree Node ─── */
function SubTaskNode({ task, depth = 0 }: { task: SubTask; depth?: number }) {
  const [expanded, setExpanded] = useState(task.status === 'running')
  const hasChildren = task.children && task.children.length > 0
  const cfg = STATUS_CONFIG[task.status] || STATUS_CONFIG.pending
  const StatusIcon = cfg.icon
  const roleInfo = AGENT_ROLES[task.agent_role || ''] || AGENT_ROLES.coder

  return (
    <div className={cn(depth > 0 && 'ml-6 border-l border-border pl-4')}>
      <div
        className={cn(
          'flex items-center gap-3 py-2 px-3 rounded-md transition-colors group',
          hasChildren && 'cursor-pointer hover:bg-secondary/50',
          task.status === 'running' && 'bg-primary/5',
        )}
        onClick={() => hasChildren && setExpanded(!expanded)}
      >
        {hasChildren ? (
          expanded
            ? <ChevronDown className="w-3.5 h-3.5 text-muted-foreground shrink-0" />
            : <ChevronRight className="w-3.5 h-3.5 text-muted-foreground shrink-0" />
        ) : (
          <span className="w-3.5 shrink-0" />
        )}

        <div className={cn('w-6 h-6 rounded-md flex items-center justify-center shrink-0', cfg.color)}>
          <StatusIcon className={cn('w-3.5 h-3.5', task.status === 'running' && 'animate-spin')} />
        </div>

        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2">
            <span className="text-sm font-medium text-foreground truncate">{task.title}</span>
            {task.agent_role && (
              <Badge className="text-[10px] gap-1">
                <roleInfo.icon className={cn('w-2.5 h-2.5', roleInfo.color)} />
                {task.agent_role}
              </Badge>
            )}
          </div>
          <div className="flex items-center gap-3 mt-0.5">
            <span className={cn('text-[10px]', cfg.color.split(' ')[1])}>{cfg.label}</span>
            {task.agent_id && (
              <span className="text-[10px] text-muted-foreground font-mono">agent:{task.agent_id.slice(0, 8)}</span>
            )}
            {task.duration_seconds != null && task.duration_seconds > 0 && (
              <span className="text-[10px] text-muted-foreground">{formatDuration(task.duration_seconds)}</span>
            )}
          </div>
        </div>

        {task.contract && (
          <Tooltip text={`Contract: ${task.contract.status}`}>
            <Badge className={cn('text-[10px] shrink-0', CONTRACT_STATUS[task.contract.status]?.color)}>
              {CONTRACT_STATUS[task.contract.status]?.label || task.contract.status}
            </Badge>
          </Tooltip>
        )}
      </div>

      {expanded && task.error_message && (
        <div className="ml-10 mt-1 mb-2 p-2 rounded-md bg-destructive/5 border border-destructive/20">
          <p className="text-xs text-destructive font-mono">{task.error_message}</p>
        </div>
      )}

      {expanded && task.output_summary && (
        <div className="ml-10 mt-1 mb-2 p-2 rounded-md bg-secondary/50">
          <p className="text-xs text-muted-foreground">{task.output_summary}</p>
        </div>
      )}

      {expanded && hasChildren && (
        <div className="mt-1">
          {task.children!.map(child => (
            <SubTaskNode key={child.id} task={child} depth={depth + 1} />
          ))}
        </div>
      )}
    </div>
  )
}

/* ─── Agent Status Bar ─── */
function AgentStatusBar({ agents }: { agents: AgentInfo[] }) {
  if (!agents || agents.length === 0) return null

  return (
    <Card className="p-4">
      <p className="text-xs font-medium text-foreground mb-3 flex items-center gap-1.5">
        <Users className="w-3.5 h-3.5 text-primary" /> Active Agents
      </p>
      <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
        {agents.map(agent => {
          const roleInfo = AGENT_ROLES[agent.role] || AGENT_ROLES.coder
          const RoleIcon = roleInfo.icon
          return (
            <div key={agent.id} className="flex items-center gap-2 p-2 rounded-md border border-border">
              <div className="w-8 h-8 rounded-md bg-secondary flex items-center justify-center shrink-0">
                <RoleIcon className={cn('w-4 h-4', roleInfo.color)} />
              </div>
              <div className="min-w-0">
                <div className="flex items-center gap-1.5">
                  <StatusDot status={agent.status === 'working' ? 'running' : agent.status === 'error' ? 'error' : 'stopped'} />
                  <span className="text-xs font-medium text-foreground capitalize">{agent.role}</span>
                </div>
                <span className="text-[10px] text-muted-foreground font-mono block truncate">{agent.id.slice(0, 12)}</span>
              </div>
            </div>
          )
        })}
      </div>
    </Card>
  )
}

/* ─── Live Log Stream ─── */
function LiveLogStream({ taskId, branch }: { taskId: string; branch?: string }) {
  const [logEntries, setLogEntries] = useState<any[]>([])
  const { orgId } = useAPIAuth()
  const containerRef = useRef<HTMLDivElement>(null)

  const streamUrl = branch ? `${api.events.streamURL(branch)}&org_id=${orgId}` : null
  const { connected } = useSSE(streamUrl, (data) => {
    setLogEntries(prev => [data, ...prev].slice(0, 200))
  })

  useEffect(() => {
    if (containerRef.current) containerRef.current.scrollTop = 0
  }, [logEntries.length])

  return (
    <Card className="overflow-hidden">
      <div className="flex items-center justify-between px-4 py-3 border-b border-border">
        <div className="flex items-center gap-2">
          {connected ? (
            <>
              <StatusDot status="connected" className="animate-pulse-dot" />
              <span className="text-xs text-primary font-medium">Live Stream</span>
            </>
          ) : (
            <>
              <StatusDot status="disconnected" />
              <span className="text-xs text-muted-foreground">Disconnected</span>
            </>
          )}
          <span className="text-[10px] text-muted-foreground font-mono">task:{taskId.slice(0, 8)}</span>
        </div>
        <span className="text-[10px] text-muted-foreground">{logEntries.length} entries</span>
      </div>
      <div ref={containerRef} className="max-h-64 overflow-y-auto px-4">
        {logEntries.length === 0 ? (
          <div className="py-8 text-center text-xs text-muted-foreground">
            <Radio className="w-5 h-5 mx-auto mb-2 text-muted-foreground" />
            <p>Waiting for live events…</p>
          </div>
        ) : (
          logEntries.map((entry, i) => (
            <div key={i} className="flex items-start gap-2 py-2 border-b border-border last:border-0 animate-fade-in">
              <span className={cn(
                'w-1.5 h-1.5 rounded-full mt-1.5 shrink-0',
                entry.status === 'completed' ? 'bg-emerald-400' : entry.status === 'failed' ? 'bg-destructive' : 'bg-yellow-400',
              )} />
              <div className="flex-1 min-w-0">
                <div className="flex items-center gap-2">
                  <span className="text-xs font-mono text-foreground">{entry.step || entry.event_type || '—'}</span>
                  {entry.agent_id && (
                    <span className="text-[10px] text-muted-foreground font-mono">@{entry.agent_id.slice(0, 6)}</span>
                  )}
                </div>
                {(entry.message || entry.data) && (
                  <p className="text-[10px] text-muted-foreground mt-0.5 break-all">
                    {entry.message || (typeof entry.data === 'string' ? entry.data : JSON.stringify(entry.data))}
                  </p>
                )}
              </div>
              {entry.created_at && <span className="text-[10px] text-muted-foreground shrink-0">{timeAgo(entry.created_at)}</span>}
            </div>
          ))
        )}
      </div>
    </Card>
  )
}

/* ─── Task Detail Panel ─── */
function TaskDetailPanel({ taskId, onBack }: { taskId: string; onBack: () => void }) {
  const { data: task, loading, refetch } = useFetch<TaskDetail>(
    useCallback((token: string, orgId: string) => api.tasks.get(token, orgId, taskId), [taskId]),
    [taskId]
  )

  const { data: logs } = useFetch<LogEntry[]>(
    useCallback((token: string, orgId: string) => api.tasks.logs(token, orgId, taskId), [taskId]),
    [taskId]
  )

  usePolling(refetch, 5000, task?.status === 'running')

  if (loading) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-10 w-full" />
        <Skeleton className="h-40 w-full" />
        <Skeleton className="h-32 w-full" />
      </div>
    )
  }

  if (!task) {
    return (
      <EmptyState icon={Bot} title="Task not found" description="This task may have been deleted." />
    )
  }

  const cfg = STATUS_CONFIG[task.status] || STATUS_CONFIG.pending
  const StatusIcon = cfg.icon
  const completedSubTasks = task.sub_tasks?.filter(st => st.status === 'complete').length || 0
  const totalSubTasks = task.sub_tasks?.length || 0

  return (
    <div className="space-y-4">
      {/* Header */}
      <div className="flex items-center gap-3">
        <Button variant="ghost" size="sm" onClick={onBack}>
          <ArrowRight className="w-3.5 h-3.5 rotate-180" /> Back
        </Button>
        <div className="flex-1 min-w-0">
          <h2 className="text-lg font-semibold text-foreground truncate">{task.title}</h2>
          <div className="flex items-center gap-3 mt-1">
            <Badge className={cfg.color}>
              <StatusIcon className={cn('w-3 h-3 mr-1', task.status === 'running' && 'animate-spin')} />
              {cfg.label}
            </Badge>
            {task.branch && <Badge><GitBranch className="w-3 h-3 mr-1" />{task.branch}</Badge>}
            {task.created_at && <span className="text-[10px] text-muted-foreground">{timeAgo(task.created_at)}</span>}
          </div>
        </div>
        <Button variant="ghost" size="sm" onClick={refetch}><RefreshCw className="w-3.5 h-3.5" /></Button>
      </div>

      {task.description && (
        <Card className="p-4">
          <p className="text-sm text-muted-foreground whitespace-pre-wrap">{task.description}</p>
        </Card>
      )}

      {/* Progress */}
      {totalSubTasks > 0 && (
        <Card className="p-4">
          <div className="flex items-center justify-between mb-2">
            <p className="text-xs font-medium text-foreground flex items-center gap-1.5">
              <Layers className="w-3.5 h-3.5 text-primary" /> Task Decomposition
            </p>
            <span className="text-xs text-muted-foreground">{completedSubTasks}/{totalSubTasks} complete</span>
          </div>
          <ProgressBar value={completedSubTasks} max={totalSubTasks} className="mb-4" />
          <div className="space-y-1">
            {task.sub_tasks!.map(st => (
              <SubTaskNode key={st.id} task={st} />
            ))}
          </div>
        </Card>
      )}

      {/* Agents */}
      {task.agents && task.agents.length > 0 && <AgentStatusBar agents={task.agents} />}

      {/* Live stream for running tasks */}
      {task.status === 'running' && task.branch && (
        <LiveLogStream taskId={task.id} branch={task.branch} />
      )}

      {/* Execution metrics */}
      {(task.duration_seconds != null || task.tokens_used != null) && (
        <div className="grid grid-cols-3 gap-3">
          <Card className="p-3">
            <p className="text-[10px] text-muted-foreground mb-0.5">Duration</p>
            <p className="text-sm font-semibold text-foreground">
              {task.duration_seconds ? formatDuration(task.duration_seconds) : '—'}
            </p>
          </Card>
          <Card className="p-3">
            <p className="text-[10px] text-muted-foreground mb-0.5">Tokens</p>
            <p className="text-sm font-semibold text-foreground">
              {task.tokens_used ? task.tokens_used.toLocaleString() : '—'}
            </p>
          </Card>
          <Card className="p-3">
            <p className="text-[10px] text-muted-foreground mb-0.5">Cost</p>
            <p className="text-sm font-semibold text-foreground">
              {task.estimated_cost ? `$${task.estimated_cost.toFixed(4)}` : '—'}
            </p>
          </Card>
        </div>
      )}

      {/* Static log history */}
      {logs && logs.length > 0 && (
        <Card className="overflow-hidden">
          <div className="px-4 py-3 border-b border-border">
            <p className="text-xs font-medium text-foreground flex items-center gap-1.5">
              <Terminal className="w-3.5 h-3.5 text-primary" /> Execution Log
            </p>
          </div>
          <div className="px-4 max-h-60 overflow-y-auto">
            {logs.map((log) => (
              <div key={log.id} className="flex items-start gap-2 py-2 border-b border-border last:border-0">
                <span className={cn(
                  'w-1.5 h-1.5 rounded-full mt-1.5 shrink-0',
                  log.status === 'completed' ? 'bg-emerald-400' : log.status === 'failed' ? 'bg-destructive' : 'bg-yellow-400',
                )} />
                <div className="flex-1 min-w-0">
                  <span className="text-xs font-mono text-foreground">{log.step}</span>
                  {log.agent_id && <span className="text-[10px] text-muted-foreground ml-2">@{log.agent_id.slice(0, 6)}</span>}
                  {log.message && <p className="text-[10px] text-muted-foreground mt-0.5">{log.message}</p>}
                </div>
                {log.created_at && <span className="text-[10px] text-muted-foreground shrink-0">{timeAgo(log.created_at)}</span>}
              </div>
            ))}
          </div>
        </Card>
      )}

      {/* Error / Output */}
      {task.error_message && (
        <Card className="p-4 border-destructive/30">
          <p className="text-xs font-medium text-destructive mb-1">Error</p>
          <p className="text-sm text-muted-foreground font-mono whitespace-pre-wrap">{task.error_message}</p>
        </Card>
      )}
      {task.output_summary && (
        <Card className="p-4">
          <p className="text-xs font-medium text-foreground mb-1">Output Summary</p>
          <p className="text-sm text-muted-foreground whitespace-pre-wrap">{task.output_summary}</p>
        </Card>
      )}
    </div>
  )
}

/* ─── Main Component ─── */
export default function TaskOrchestrationView() {
  const [selectedTaskId, setSelectedTaskId] = useState<string | null>(null)
  const [statusFilter, setStatusFilter] = useState('')

  const { data: tasks, loading, refetch } = useFetch(
    useCallback((token: string, orgId: string) => {
      const params: Record<string, string> = { limit: '50' }
      if (statusFilter) params.status = statusFilter
      return api.tasks.list(token, orgId, params)
    }, [statusFilter]),
    [statusFilter]
  )

  usePolling(refetch, 10000, !selectedTaskId)

  if (selectedTaskId) {
    return <TaskDetailPanel taskId={selectedTaskId} onBack={() => setSelectedTaskId(null)} />
  }

  const runningTasks = tasks?.filter((t: any) => t.status === 'running') || []
  const otherTasks = tasks?.filter((t: any) => t.status !== 'running') || []

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-lg font-semibold text-foreground flex items-center gap-2">
            <Layers className="w-5 h-5 text-primary" /> Task Orchestration
          </h2>
          <p className="text-xs text-muted-foreground mt-0.5">Multi-agent task decomposition and execution</p>
        </div>
        <Button variant="ghost" size="sm" onClick={refetch}><RefreshCw className="w-3.5 h-3.5" /></Button>
      </div>

      {/* Status filter chips */}
      <div className="flex gap-1.5">
        {['', 'running', 'pending', 'complete', 'failed'].map(s => (
          <button
            key={s}
            onClick={() => setStatusFilter(s)}
            className={cn(
              'px-2.5 py-1 text-xs rounded-full border transition-colors',
              statusFilter === s
                ? 'bg-primary/10 text-primary border-primary/30'
                : 'text-muted-foreground border-transparent hover:border-border',
            )}
          >
            {s || 'All'}
          </button>
        ))}
      </div>

      {loading ? (
        [1, 2, 3].map(i => <Skeleton key={i} className="h-24 w-full" />)
      ) : !tasks || tasks.length === 0 ? (
        <EmptyState
          icon={Layers}
          title={statusFilter ? `No ${statusFilter} tasks` : 'No orchestrated tasks'}
          description="Tasks with multi-agent decomposition will appear here as they are created and executed."
        />
      ) : (
        <>
          {/* Running tasks first */}
          {runningTasks.length > 0 && (
            <div className="space-y-2">
              <p className="text-xs font-medium text-muted-foreground flex items-center gap-1.5">
                <Loader2 className="w-3 h-3 animate-spin text-primary" /> Running ({runningTasks.length})
              </p>
              {runningTasks.map((task: any) => {
                const cfg = STATUS_CONFIG[task.status] || STATUS_CONFIG.running
                const Icon = cfg.icon
                return (
                  <Card
                    key={task.id}
                    className="p-4 border-primary/20 hover:border-primary/40 cursor-pointer transition-colors"
                    onClick={() => setSelectedTaskId(task.id)}
                  >
                    <div className="flex items-center gap-3">
                      <div className="w-10 h-10 rounded-lg bg-primary/10 flex items-center justify-center shrink-0">
                        <Icon className="w-5 h-5 text-primary animate-spin" />
                      </div>
                      <div className="flex-1 min-w-0">
                        <p className="text-sm font-medium text-foreground truncate">{task.title}</p>
                        <div className="flex items-center gap-3 mt-1">
                          <Badge className={cfg.color}>{cfg.label}</Badge>
                          {task.branch && (
                            <span className="text-[10px] text-muted-foreground flex items-center gap-1">
                              <GitBranch className="w-2.5 h-2.5" />{task.branch}
                            </span>
                          )}
                          {task.started_at && <span className="text-[10px] text-muted-foreground">started {timeAgo(task.started_at)}</span>}
                        </div>
                      </div>
                      <div className="flex items-center gap-2 shrink-0">
                        <StatusDot status="running" className="animate-pulse-dot" />
                        <ChevronRight className="w-4 h-4 text-muted-foreground" />
                      </div>
                    </div>
                  </Card>
                )
              })}
            </div>
          )}

          {/* Other tasks */}
          {otherTasks.length > 0 && (
            <div className="space-y-2">
              {runningTasks.length > 0 && (
                <p className="text-xs font-medium text-muted-foreground mt-4">Other Tasks ({otherTasks.length})</p>
              )}
              {otherTasks.map((task: any) => {
                const cfg = STATUS_CONFIG[task.status] || STATUS_CONFIG.pending
                const Icon = cfg.icon
                return (
                  <Card
                    key={task.id}
                    className="p-4 hover:border-muted-foreground/20 cursor-pointer transition-colors"
                    onClick={() => setSelectedTaskId(task.id)}
                  >
                    <div className="flex items-center gap-3">
                      <div className={cn('w-8 h-8 rounded-lg flex items-center justify-center shrink-0', cfg.color)}>
                        <Icon className="w-4 h-4" />
                      </div>
                      <div className="flex-1 min-w-0">
                        <p className="text-sm font-medium text-foreground truncate">{task.title}</p>
                        <div className="flex items-center gap-3 mt-0.5">
                          <Badge className={cn('text-[10px]', cfg.color)}>{cfg.label}</Badge>
                          {task.branch && (
                            <span className="text-[10px] text-muted-foreground flex items-center gap-1">
                              <GitBranch className="w-2.5 h-2.5" />{task.branch}
                            </span>
                          )}
                          {task.created_at && <span className="text-[10px] text-muted-foreground">{timeAgo(task.created_at)}</span>}
                        </div>
                      </div>
                      <ChevronRight className="w-4 h-4 text-muted-foreground shrink-0" />
                    </div>
                  </Card>
                )
              })}
            </div>
          )}
        </>
      )}
    </div>
  )
}
