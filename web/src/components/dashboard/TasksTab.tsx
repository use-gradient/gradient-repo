import { useState, useCallback, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import Markdown from 'react-markdown'
import { api } from '@/api/client'
import { useFetch, useMutation, usePolling } from '@/hooks/useAPI'
import { cn, copyToClipboard, timeAgo } from '@/lib/utils'
import {
  Button, Card, Badge, EmptyState, Modal, Skeleton, Tabs, useToast,
} from '@/components/ui'
import {
  Bot, RefreshCw, XCircle, Clock,
  CheckCircle2, AlertTriangle, Loader2, GitBranch, ExternalLink,
  ChevronRight, BarChart3, FileText, Plug, Key,
  RotateCcw, Copy, Download,
} from 'lucide-react'

const STATUS_MAP: Record<string, { label: string; color: string; icon: any }> = {
  pending:   { label: 'Pending',   color: 'bg-yellow-500/10 text-yellow-400 border-yellow-500/20', icon: Clock },
  queued:    { label: 'Queued',    color: 'bg-blue-500/10 text-blue-400 border-blue-500/20', icon: Clock },
  running:   { label: 'Running',   color: 'bg-primary/10 text-primary border-primary/20', icon: Loader2 },
  complete:  { label: 'Complete',  color: 'bg-emerald-500/10 text-emerald-400 border-emerald-500/20', icon: CheckCircle2 },
  failed:    { label: 'Failed',    color: 'bg-destructive/10 text-destructive border-destructive/20', icon: AlertTriangle },
  cancelled: { label: 'Cancelled', color: 'bg-muted text-muted-foreground border-border', icon: XCircle },
}

function canRetryTask(task: any) {
  return task?.status === 'failed' || task?.status === 'cancelled'
}

function latestAttemptLogs(logs: any[]) {
  if (!Array.isArray(logs) || logs.length === 0) return []
  let startIndex = 0
  logs.forEach((log: any, index: number) => {
    if (log?.step === 'execution_started') {
      startIndex = index
    }
  })
  return logs.slice(startIndex)
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
          Agent tasks run against your own Anthropic account. Add an Anthropic API key first, then optionally connect Linear for issue-driven workflows.
        </p>
      </div>

      <div className="flex items-center justify-center gap-6 text-sm">
        <div className="flex items-center gap-2">
          {readiness.claude_configured
            ? <CheckCircle2 className="w-4 h-4 text-emerald-400" />
            : <XCircle className="w-4 h-4 text-destructive" />}
          <span className={readiness.claude_configured ? 'text-foreground' : 'text-muted-foreground'}>
            Anthropic key
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
          <Key className="w-3.5 h-3.5" /> Add Anthropic key
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

/* ─── Task Detail Modal ─── */
function TaskDetailModal({
  task,
  open,
  onClose,
  onRetry,
  retrying,
}: {
  task: any
  open: boolean
  onClose: () => void
  onRetry: (task: any) => void
  retrying: boolean
}) {
  const { toast } = useToast()
  const { data: taskDetails, loading: taskLoading } = useFetch(
    useCallback((token: string, orgId: string) =>
      task?.id ? api.tasks.get(token, orgId, task.id) : Promise.resolve(null),
    [task?.id]),
    [task?.id, task?.updated_at, task?.status]
  )
  const { data: logs, loading: logsLoading } = useFetch(
    useCallback((token: string, orgId: string) =>
      task?.id ? api.tasks.logs(token, orgId, task.id) : Promise.resolve([]),
    [task?.id]),
    [task?.id, task?.updated_at, task?.status]
  )

  if (!task) return null
  const detailedTask = taskDetails || task
  const currentAttemptLogs = latestAttemptLogs(Array.isArray(logs) ? logs : [])

  const status = STATUS_MAP[detailedTask.status] || STATUS_MAP.pending
  const StatusIcon = status.icon
  const claudeOutputFromTask = typeof detailedTask?.output_json?.claude_output_markdown === 'string'
    ? detailedTask.output_json.claude_output_markdown
    : ''
  const claudeOutputFromLogs = currentAttemptLogs.length > 0
    ? [...currentAttemptLogs].reverse().find((log: any) => log?.step === 'claude_output')?.message || ''
    : ''
  const claudeOutputMarkdown = claudeOutputFromTask || claudeOutputFromLogs

  const handleCopyClaudeOutput = async () => {
    if (!claudeOutputMarkdown) return
    const copied = await copyToClipboard(claudeOutputMarkdown)
    toast(copied ? 'success' : 'error', copied ? 'Claude output copied' : 'Failed to copy Claude output')
  }

  const handleDownloadClaudeOutput = () => {
    if (!claudeOutputMarkdown) return
    const blob = new Blob([claudeOutputMarkdown], { type: 'text/markdown;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = `${detailedTask.id || 'task'}-claude-output.md`
    document.body.appendChild(link)
    link.click()
    document.body.removeChild(link)
    URL.revokeObjectURL(url)
  }

  return (
    <Modal open={open} onClose={onClose} title={task.title} size="lg">
      <div className="space-y-4 pb-1">
        {/* Status + meta */}
        <div className="flex items-center gap-3 flex-wrap">
            <Badge className={status.color}>
            <StatusIcon className={cn('w-3 h-3 mr-1', detailedTask.status === 'running' && 'animate-spin')} />
            {status.label}
          </Badge>
          {canRetryTask(task) && (
            <Button
              variant="outline"
              size="sm"
              loading={retrying}
              onClick={() => onRetry(task)}
            >
              <RotateCcw className="w-3.5 h-3.5" />
              Rerun task
            </Button>
          )}
          {detailedTask.branch && <Badge><GitBranch className="w-3 h-3 mr-1" />{detailedTask.branch}</Badge>}
          {detailedTask.linear_identifier && (
            <a href={detailedTask.linear_url} target="_blank" rel="noopener noreferrer" className="text-xs text-primary hover:underline flex items-center gap-1">
              {detailedTask.linear_identifier} <ExternalLink className="w-3 h-3" />
            </a>
          )}
          {detailedTask.created_at && <span className="text-[10px] text-muted-foreground">{timeAgo(detailedTask.created_at)}</span>}
        </div>

        {/* Description */}
        {detailedTask.description && (
          <div className="text-sm text-muted-foreground bg-secondary/50 rounded-md p-3 max-h-64 overflow-y-auto">
            <Markdown
              components={{
                h1: ({ children }) => <h1 className="text-base font-bold text-foreground mt-2 mb-1">{children}</h1>,
                h2: ({ children }) => <h2 className="text-sm font-semibold text-foreground mt-2 mb-1">{children}</h2>,
                h3: ({ children }) => <h3 className="text-sm font-medium text-foreground mt-1.5 mb-0.5">{children}</h3>,
                p: ({ children }) => <p className="mb-1.5 leading-relaxed">{children}</p>,
                ul: ({ children }) => <ul className="list-disc pl-4 mb-1.5 space-y-0.5">{children}</ul>,
                ol: ({ children }) => <ol className="list-decimal pl-4 mb-1.5 space-y-0.5">{children}</ol>,
                li: ({ children }) => <li className="leading-relaxed">{children}</li>,
                code: ({ children, className }) => className
                  ? <code className="block bg-background/80 rounded p-2 text-xs font-mono overflow-x-auto my-1">{children}</code>
                  : <code className="bg-background/80 rounded px-1 py-0.5 text-xs font-mono">{children}</code>,
                strong: ({ children }) => <strong className="font-semibold text-foreground">{children}</strong>,
                a: ({ href, children }) => <a href={href} target="_blank" rel="noopener noreferrer" className="text-primary hover:underline">{children}</a>,
              }}
            >
              {detailedTask.description}
            </Markdown>
          </div>
        )}

        {/* Results */}
        {(taskLoading || logsLoading) && (
          <Skeleton className="h-32 w-full" />
        )}

        {detailedTask.output_summary && (
          <Card className="p-3">
            <p className="text-xs font-medium text-foreground mb-1">Output Summary</p>
            <div className="text-sm text-muted-foreground max-h-48 overflow-y-auto">
              <Markdown>{detailedTask.output_summary}</Markdown>
            </div>
          </Card>
        )}

        {claudeOutputMarkdown && (
          <Card className="p-3">
            <div className="flex items-center justify-between gap-3 mb-2">
              <p className="text-xs font-medium text-foreground">Claude Output.md</p>
              <div className="flex items-center gap-2">
                <Button variant="outline" size="sm" onClick={handleCopyClaudeOutput}>
                  <Copy className="w-3.5 h-3.5" />
                  Copy
                </Button>
                <Button variant="outline" size="sm" onClick={handleDownloadClaudeOutput}>
                  <Download className="w-3.5 h-3.5" />
                  Download
                </Button>
              </div>
            </div>
            <div className="text-sm text-muted-foreground bg-secondary/40 rounded-md p-3 max-h-[32rem] overflow-auto">
              <Markdown>{claudeOutputMarkdown}</Markdown>
            </div>
          </Card>
        )}

        {detailedTask.pr_url && (
          <a href={detailedTask.pr_url} target="_blank" rel="noopener noreferrer"
            className="flex items-center gap-2 text-sm text-primary hover:underline">
            <GitBranch className="w-4 h-4" /> View Pull Request <ExternalLink className="w-3 h-3" />
          </a>
        )}

        {detailedTask.error_message && (
          <Card className="p-3 border-destructive/30">
            <p className="text-xs font-medium text-destructive mb-1">Error</p>
            <p className="text-sm text-muted-foreground font-mono">{detailedTask.error_message}</p>
          </Card>
        )}

        {/* Execution metrics */}
        {(detailedTask.duration_seconds > 0 || detailedTask.tokens_used > 0) && (
          <div className="flex gap-4 text-xs text-muted-foreground">
            {detailedTask.duration_seconds > 0 && <span>⏱ {Math.round(detailedTask.duration_seconds)}s</span>}
            {detailedTask.tokens_used > 0 && <span>🔤 {detailedTask.tokens_used.toLocaleString()} tokens</span>}
            {detailedTask.estimated_cost > 0 && <span>💰 ${detailedTask.estimated_cost.toFixed(4)}</span>}
          </div>
        )}

        {/* Execution log */}
        <div>
          <p className="text-xs font-medium text-foreground mb-2 flex items-center gap-1.5">
            <FileText className="w-3.5 h-3.5 text-primary" /> Execution Log
          </p>
          {logsLoading ? (
            <Skeleton className="h-20 w-full" />
          ) : currentAttemptLogs.length === 0 ? (
            <p className="text-xs text-muted-foreground">No log entries yet</p>
          ) : (
            <div className="space-y-1 max-h-48 overflow-y-auto">
              {currentAttemptLogs.map((log: any) => (
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
  const [selectedTask, setSelectedTask] = useState<any>(null)
  const [statusFilter, setStatusFilter] = useState('')
  const [retryingTaskID, setRetryingTaskID] = useState<string | null>(null)
  const { toast } = useToast()

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

  const { data: stats, refetch: refetchStats } = useFetch(
    useCallback((token: string, orgId: string) => api.tasks.stats(token, orgId), [])
  )
  const { mutate: retryTask, loading: retryLoading } = useMutation(
    (token: string, orgId: string, taskID: string) => api.tasks.retry(token, orgId, taskID)
  )

  const hasActiveTasks = (stats?.running ?? 0) > 0 || (tasks || []).some(
    (t: any) => t.status === 'running' || t.status === 'pending' || t.status === 'queued'
  )
  usePolling(() => { refetch(); refetchStats() }, 5000, hasActiveTasks)
  usePolling(() => { refetch(); refetchStats() }, 30000, !hasActiveTasks)

  useEffect(() => {
    if (!selectedTask || !Array.isArray(tasks)) return
    const freshTask = tasks.find((task: any) => task.id === selectedTask.id)
    if (freshTask) {
      setSelectedTask((current: any) => ({ ...current, ...freshTask }))
    }
  }, [tasks, selectedTask?.id])

  const isReady = readiness?.ready === true

  const handleRetryTask = useCallback(async (task: any) => {
    if (!task?.id) return
    setRetryingTaskID(task.id)
    const result = await retryTask(task.id)
    if (result) {
      toast('success', `Rerunning "${task.title}"`)
      setSelectedTask((current: any) => current?.id === task.id ? { ...current, ...result } : current)
      refetch()
      refetchStats()
    } else {
      toast('error', 'Failed to rerun task')
    }
    setRetryingTaskID(null)
  }, [retryTask, toast, refetch, refetchStats])

  if (readinessLoading) {
    return <div className="space-y-4"><Skeleton className="h-10 w-full" /><Skeleton className="h-40 w-full" /></div>
  }

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
              <p className="text-xs text-muted-foreground">AI agent tasks using your Anthropic account plus Gradient memory</p>
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
            <button onClick={refetch} className="p-2 text-muted-foreground hover:text-foreground transition-colors">
              <RefreshCw className="w-3.5 h-3.5" />
            </button>
          </div>

          {tasksLoading ? (
            [1, 2, 3].map(i => <Skeleton key={i} className="h-20 w-full" />)
          ) : !tasks || tasks.length === 0 ? (
            <EmptyState
              icon={Bot}
              title={statusFilter ? `No ${statusFilter} tasks` : 'No tasks yet'}
              description="Tasks are created automatically from Linear issues. Connect Linear in Integrations to get started."
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
                      {canRetryTask(task) && (
                        <Button
                          variant="outline"
                          size="sm"
                          loading={retryLoading && retryingTaskID === task.id}
                          onClick={(event) => {
                            event.stopPropagation()
                            void handleRetryTask(task)
                          }}
                        >
                          <RotateCcw className="w-3.5 h-3.5" />
                          Rerun
                        </Button>
                      )}
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

      <TaskDetailModal
        task={selectedTask}
        open={!!selectedTask}
        onClose={() => setSelectedTask(null)}
        onRetry={(task) => void handleRetryTask(task)}
        retrying={retryLoading && retryingTaskID === selectedTask?.id}
      />
    </div>
  )
}
