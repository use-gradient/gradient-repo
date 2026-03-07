import { useState, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import { api } from '@/api/client'
import { useFetch, useMutation } from '@/hooks/useAPI'
import { cn, timeAgo } from '@/lib/utils'
import {
  Button, Card, Badge, EmptyState, Modal, Input, Skeleton, useToast, Tabs,
} from '@/components/ui'
import {
  Bot, Plus, Play, RefreshCw, XCircle, RotateCcw, Clock,
  CheckCircle2, AlertTriangle, Loader2, GitBranch, ExternalLink,
  ChevronRight, BarChart3, FileText, Plug, Key,
} from 'lucide-react'

const STATUS_MAP: Record<string, { label: string; color: string; icon: any }> = {
  pending:   { label: 'Pending',   color: 'bg-yellow-500/10 text-yellow-400 border-yellow-500/20', icon: Clock },
  queued:    { label: 'Queued',    color: 'bg-blue-500/10 text-blue-400 border-blue-500/20', icon: Clock },
  running:   { label: 'Running',   color: 'bg-primary/10 text-primary border-primary/20', icon: Loader2 },
  complete:  { label: 'Complete',  color: 'bg-emerald-500/10 text-emerald-400 border-emerald-500/20', icon: CheckCircle2 },
  failed:    { label: 'Failed',    color: 'bg-destructive/10 text-destructive border-destructive/20', icon: AlertTriangle },
  cancelled: { label: 'Cancelled', color: 'bg-muted text-muted-foreground border-border', icon: XCircle },
}

/* ─── Setup Required Banner ─── */
function SetupRequired({ readiness }: { readiness: { claude_configured: boolean; linear_connected: boolean; message?: string } }) {
  const navigate = useNavigate()
  return (
    <Card className="p-8 text-center space-y-4">
      <div className="w-12 h-12 mx-auto rounded-xl bg-primary/10 flex items-center justify-center">
        <Bot className="w-6 h-6 text-primary" />
      </div>
      <div>
        <h3 className="text-lg font-semibold text-foreground">Set up integrations to enable agent tasks</h3>
        <p className="text-sm text-muted-foreground mt-1 max-w-md mx-auto">
          Agent tasks need Claude Code to execute. Connect your Anthropic API key first, then optionally connect Linear for issue-driven workflows.
        </p>
      </div>

      <div className="flex items-center justify-center gap-6 text-sm">
        <div className="flex items-center gap-2">
          {readiness.claude_configured
            ? <CheckCircle2 className="w-4 h-4 text-emerald-400" />
            : <XCircle className="w-4 h-4 text-destructive" />}
          <span className={readiness.claude_configured ? 'text-foreground' : 'text-muted-foreground'}>
            Claude Code
          </span>
        </div>
        <div className="flex items-center gap-2">
          {readiness.linear_connected
            ? <CheckCircle2 className="w-4 h-4 text-emerald-400" />
            : <span className="w-4 h-4 rounded-full border border-border" />}
          <span className="text-muted-foreground">Linear <span className="text-[10px]">(optional)</span></span>
        </div>
      </div>

      <div className="flex items-center justify-center gap-3 pt-2">
        <Button onClick={() => navigate('/dashboard/integrations')}>
          <Key className="w-3.5 h-3.5" /> Configure Claude Code
        </Button>
        {!readiness.linear_connected && (
          <Button variant="outline" onClick={() => navigate('/dashboard/integrations')}>
            <Plug className="w-3.5 h-3.5" /> Connect Linear
          </Button>
        )}
      </div>
    </Card>
  )
}

/* ─── Create Task Modal ─── */
function CreateTaskModal({ open, onClose, onCreated }: { open: boolean; onClose: () => void; onCreated: () => void }) {
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [branch, setBranch] = useState('')
  const { toast } = useToast()

  const { mutate, loading, error } = useMutation(
    (token: string, orgId: string, body: any) => api.tasks.create(token, orgId, body)
  )

  const handleCreate = async () => {
    const result = await mutate({ title, description, branch: branch || undefined })
    if (result) {
      toast('success', `Task "${title}" created`)
      setTitle(''); setDescription(''); setBranch('')
      onCreated()
      onClose()
    }
  }

  return (
    <Modal open={open} onClose={onClose} title="Create Task" description="Describe what you need the AI agent to do" size="md" footer={
      <div className="flex gap-2">
        <Button variant="outline" onClick={onClose}>Cancel</Button>
        <Button onClick={handleCreate} loading={loading} disabled={!title}>
          <Bot className="w-3.5 h-3.5" /> Create Task
        </Button>
      </div>
    }>
      <div className="space-y-4">
        <Input
          label="Title"
          placeholder="e.g. Add dark mode toggle to settings page"
          value={title}
          onChange={e => setTitle(e.target.value)}
          autoFocus
        />
        <div>
          <label className="text-sm font-medium text-foreground mb-1.5 block">Description</label>
          <textarea
            value={description}
            onChange={e => setDescription(e.target.value)}
            placeholder="Detailed instructions, acceptance criteria, links to designs..."
            className="w-full bg-card border border-input rounded-md px-3 py-2 text-sm text-foreground placeholder:text-muted-foreground outline-none focus:ring-1 focus:ring-ring min-h-[100px] resize-y"
          />
        </div>
        <Input
          label="Branch (optional)"
          placeholder="feature/dark-mode"
          value={branch}
          onChange={e => setBranch(e.target.value)}
          mono
        />
        {error && <p className="text-xs text-destructive">{error}</p>}
      </div>
    </Modal>
  )
}

/* ─── Task Detail Modal ─── */
function TaskDetailModal({ task, open, onClose, onAction }: { task: any; open: boolean; onClose: () => void; onAction: () => void }) {
  const { toast } = useToast()
  const { data: logs, loading: logsLoading } = useFetch(
    useCallback((token: string, orgId: string) =>
      task?.id ? api.tasks.logs(token, orgId, task.id) : Promise.resolve([]),
    [task?.id]),
    [task?.id]
  )

  const { mutate: startTask, loading: starting } = useMutation(
    (token: string, orgId: string, id: string) => api.tasks.start(token, orgId, id)
  )
  const { mutate: cancelTask, loading: cancelling } = useMutation(
    (token: string, orgId: string, id: string) => api.tasks.cancel(token, orgId, id)
  )
  const { mutate: retryTask, loading: retrying } = useMutation(
    (token: string, orgId: string, id: string) => api.tasks.retry(token, orgId, id)
  )

  if (!task) return null

  const status = STATUS_MAP[task.status] || STATUS_MAP.pending
  const StatusIcon = status.icon

  const handleStart = async () => {
    await startTask(task.id)
    toast('success', 'Task started')
    onAction()
  }
  const handleCancel = async () => {
    await cancelTask(task.id)
    toast('success', 'Task cancelled')
    onAction()
  }
  const handleRetry = async () => {
    await retryTask(task.id)
    toast('success', 'Task retried')
    onAction()
  }

  return (
    <Modal open={open} onClose={onClose} title={task.title} size="lg" footer={
      <div className="flex gap-2">
        {(task.status === 'pending') && (
          <Button onClick={handleStart} loading={starting}><Play className="w-3.5 h-3.5" /> Start</Button>
        )}
        {(task.status === 'pending' || task.status === 'running' || task.status === 'queued') && (
          <Button variant="destructive" onClick={handleCancel} loading={cancelling}><XCircle className="w-3.5 h-3.5" /> Cancel</Button>
        )}
        {(task.status === 'failed' || task.status === 'cancelled') && (
          <Button onClick={handleRetry} loading={retrying}><RotateCcw className="w-3.5 h-3.5" /> Retry</Button>
        )}
      </div>
    }>
      <div className="space-y-4">
        {/* Status + meta */}
        <div className="flex items-center gap-3 flex-wrap">
          <Badge className={status.color}>
            <StatusIcon className={cn('w-3 h-3 mr-1', task.status === 'running' && 'animate-spin')} />
            {status.label}
          </Badge>
          {task.branch && <Badge><GitBranch className="w-3 h-3 mr-1" />{task.branch}</Badge>}
          {task.linear_identifier && (
            <a href={task.linear_url} target="_blank" rel="noopener noreferrer" className="text-xs text-primary hover:underline flex items-center gap-1">
              {task.linear_identifier} <ExternalLink className="w-3 h-3" />
            </a>
          )}
          {task.created_at && <span className="text-[10px] text-muted-foreground">{timeAgo(task.created_at)}</span>}
        </div>

        {/* Description */}
        {task.description && (
          <div className="text-sm text-muted-foreground whitespace-pre-wrap bg-secondary/50 rounded-md p-3">
            {task.description}
          </div>
        )}

        {/* Results */}
        {task.output_summary && (
          <Card className="p-3">
            <p className="text-xs font-medium text-foreground mb-1">Output Summary</p>
            <p className="text-sm text-muted-foreground whitespace-pre-wrap">{task.output_summary}</p>
          </Card>
        )}

        {task.pr_url && (
          <a href={task.pr_url} target="_blank" rel="noopener noreferrer"
            className="flex items-center gap-2 text-sm text-primary hover:underline">
            <GitBranch className="w-4 h-4" /> View Pull Request <ExternalLink className="w-3 h-3" />
          </a>
        )}

        {task.error_message && (
          <Card className="p-3 border-destructive/30">
            <p className="text-xs font-medium text-destructive mb-1">Error</p>
            <p className="text-sm text-muted-foreground font-mono">{task.error_message}</p>
          </Card>
        )}

        {/* Execution metrics */}
        {(task.duration_seconds > 0 || task.tokens_used > 0) && (
          <div className="flex gap-4 text-xs text-muted-foreground">
            {task.duration_seconds > 0 && <span>⏱ {Math.round(task.duration_seconds)}s</span>}
            {task.tokens_used > 0 && <span>🔤 {task.tokens_used.toLocaleString()} tokens</span>}
            {task.estimated_cost > 0 && <span>💰 ${task.estimated_cost.toFixed(4)}</span>}
          </div>
        )}

        {/* Execution log */}
        <div>
          <p className="text-xs font-medium text-foreground mb-2 flex items-center gap-1.5">
            <FileText className="w-3.5 h-3.5 text-primary" /> Execution Log
          </p>
          {logsLoading ? (
            <Skeleton className="h-20 w-full" />
          ) : !logs || logs.length === 0 ? (
            <p className="text-xs text-muted-foreground">No log entries yet</p>
          ) : (
            <div className="space-y-1 max-h-48 overflow-y-auto">
              {logs.map((log: any) => (
                <div key={log.id} className="flex items-start gap-2 text-xs py-1">
                  <span className={cn(
                    'w-1.5 h-1.5 rounded-full mt-1.5 shrink-0',
                    log.status === 'completed' ? 'bg-emerald-400' : log.status === 'failed' ? 'bg-destructive' : 'bg-yellow-400',
                  )} />
                  <div className="flex-1 min-w-0">
                    <span className="font-mono text-muted-foreground">{log.step}</span>
                    {log.message && <span className="ml-2 text-muted-foreground/70">{log.message}</span>}
                  </div>
                  {log.created_at && <span className="text-[10px] text-muted-foreground shrink-0">{timeAgo(log.created_at)}</span>}
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </Modal>
  )
}

/* ─── Main Tasks Tab ─── */
export default function TasksTab() {
  const [activeTab, setActiveTab] = useState('tasks')
  const [showCreate, setShowCreate] = useState(false)
  const [selectedTask, setSelectedTask] = useState<any>(null)
  const [statusFilter, setStatusFilter] = useState('')
  const { toast } = useToast()

  // ── Readiness check — gates the entire tab ──
  const { data: readiness, loading: readinessLoading } = useFetch(
    useCallback((token: string, orgId: string) => api.tasks.readiness(token, orgId), [])
  )

  const { data: tasks, loading: tasksLoading, refetch } = useFetch(
    useCallback((token: string, orgId: string) => {
      const params: Record<string, string> = { limit: '50' }
      if (statusFilter) params.status = statusFilter
      return api.tasks.list(token, orgId, params)
    }, [statusFilter]),
    [statusFilter]
  )

  const { data: stats } = useFetch(
    useCallback((token: string, orgId: string) => api.tasks.stats(token, orgId), [])
  )

  const isReady = readiness?.ready === true

  // While loading readiness, show skeleton
  if (readinessLoading) {
    return <div className="space-y-4"><Skeleton className="h-10 w-full" /><Skeleton className="h-40 w-full" /></div>
  }

  // If not ready, show the setup CTA — don't show tasks UI at all
  if (!isReady && readiness) {
    return <SetupRequired readiness={readiness} />
  }

  return (
    <div className="space-y-6">
      <Tabs
        tabs={[
          { id: 'tasks', label: 'Tasks', icon: <Bot className="w-3.5 h-3.5" /> },
          { id: 'overview', label: 'Overview', icon: <BarChart3 className="w-3.5 h-3.5" /> },
        ]}
        active={activeTab}
        onChange={setActiveTab}
      />

      {activeTab === 'tasks' && (
        <div className="space-y-4">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              <p className="text-xs text-muted-foreground">AI agent tasks powered by Claude Code</p>
              {/* Status filter chips */}
              <div className="flex gap-1 ml-2">
                {['', 'running', 'pending', 'complete', 'failed'].map(s => (
                  <button
                    key={s}
                    onClick={() => setStatusFilter(s)}
                    className={cn(
                      'px-2 py-0.5 text-[10px] rounded-full border transition-colors',
                      statusFilter === s ? 'bg-primary/10 text-primary border-primary/30' : 'text-muted-foreground border-transparent hover:border-border',
                    )}
                  >
                    {s || 'All'}
                  </button>
                ))}
              </div>
            </div>
            <div className="flex items-center gap-2">
              <Button variant="ghost" size="sm" onClick={refetch}><RefreshCw className="w-3.5 h-3.5" /></Button>
              <Button size="sm" onClick={() => setShowCreate(true)}>
                <Plus className="w-3.5 h-3.5" /> New Task
              </Button>
            </div>
          </div>

          {tasksLoading ? (
            [1, 2, 3].map(i => <Skeleton key={i} className="h-20 w-full" />)
          ) : !tasks || tasks.length === 0 ? (
            <EmptyState
              icon={Bot}
              title={statusFilter ? `No ${statusFilter} tasks` : 'No tasks yet'}
              description="Create a task to have the AI agent work on it. Tasks can come from Linear issues or be created manually."
              action={!statusFilter && <Button size="sm" onClick={() => setShowCreate(true)}><Plus className="w-3.5 h-3.5" /> Create Task</Button>}
            />
          ) : (
            <div className="space-y-2">
              {tasks.map((task: any) => {
                const s = STATUS_MAP[task.status] || STATUS_MAP.pending
                const Icon = s.icon
                return (
                  <Card key={task.id} className="p-4 hover:border-muted-foreground/20 cursor-pointer transition-colors" onClick={() => setSelectedTask(task)}>
                    <div className="flex items-center gap-3">
                      <div className={cn('w-8 h-8 rounded-lg flex items-center justify-center shrink-0', s.color)}>
                        <Icon className={cn('w-4 h-4', task.status === 'running' && 'animate-spin')} />
                      </div>
                      <div className="flex-1 min-w-0">
                        <p className="text-sm font-medium text-foreground truncate">{task.title}</p>
                        <div className="flex items-center gap-3 mt-0.5">
                          <Badge className={cn('text-[10px]', s.color)}>{s.label}</Badge>
                          {task.branch && <span className="text-[10px] text-muted-foreground flex items-center gap-1"><GitBranch className="w-2.5 h-2.5" />{task.branch}</span>}
                          {task.linear_identifier && <span className="text-[10px] text-primary">{task.linear_identifier}</span>}
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
        </div>
      )}

      {activeTab === 'overview' && (
        <div className="space-y-4">
          {stats && (
            <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
              {[
                { label: 'Total Tasks', value: stats.total, icon: Bot },
                { label: 'Running', value: stats.running, icon: Loader2, color: 'text-primary' },
                { label: 'Completed', value: stats.complete, icon: CheckCircle2, color: 'text-emerald-400' },
                { label: 'Failed', value: stats.failed, icon: AlertTriangle, color: 'text-destructive' },
              ].map(s => (
                <Card key={s.label} className="p-4">
                  <div className="flex items-center gap-2 mb-1">
                    <s.icon className={cn('w-4 h-4', s.color || 'text-muted-foreground')} />
                    <span className="text-xs text-muted-foreground">{s.label}</span>
                  </div>
                  <p className="text-2xl font-bold text-foreground">{s.value || 0}</p>
                </Card>
              ))}
            </div>
          )}
          {stats && (
            <div className="grid grid-cols-2 gap-3">
              <Card className="p-4">
                <p className="text-xs text-muted-foreground mb-1">Avg Duration</p>
                <p className="text-lg font-semibold text-foreground">
                  {stats.avg_duration_seconds > 0 ? `${Math.round(stats.avg_duration_seconds)}s` : '—'}
                </p>
              </Card>
              <Card className="p-4">
                <p className="text-xs text-muted-foreground mb-1">Total Cost</p>
                <p className="text-lg font-semibold text-foreground">
                  {stats.total_cost > 0 ? `$${stats.total_cost.toFixed(2)}` : '$0.00'}
                </p>
              </Card>
            </div>
          )}
        </div>
      )}

      <CreateTaskModal open={showCreate} onClose={() => setShowCreate(false)} onCreated={refetch} />
      <TaskDetailModal task={selectedTask} open={!!selectedTask} onClose={() => setSelectedTask(null)} onAction={() => { refetch(); setSelectedTask(null) }} />
    </div>
  )
}
