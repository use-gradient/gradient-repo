import { type ComponentType, useCallback, useEffect, useMemo, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { api } from '@/api/client'
import { useFetch } from '@/hooks/useAPI'
import { EVENT_TYPES, cn, formatCurrency, formatDate, timeAgo, truncate } from '@/lib/utils'
import {
  Badge, Button, Callout, Card, EmptyState, Select, Skeleton, StatusDot, Table, TableCell, TableRow, Tabs,
} from '@/components/ui'
import {
  Activity, ArrowRight, Brain, Clock, Database, FolderGit2, GitBranch, GitCommitHorizontal,
  KeyRound, Lightbulb, Network, RefreshCw, Route, ShieldCheck, Sparkles, SplitSquareVertical,
  Workflow, Wrench,
} from 'lucide-react'

function MemoryStatCard({ label, value, hint, icon: Icon }: {
  label: string
  value: string | number
  hint: string
  icon: ComponentType<{ className?: string }>
}) {
  return (
    <Card className="p-4">
      <div className="flex items-start justify-between gap-3">
        <div>
          <p className="text-xs font-medium text-muted-foreground">{label}</p>
          <p className="mt-2 text-2xl font-semibold text-foreground">{value}</p>
          <p className="mt-1 text-xs text-muted-foreground">{hint}</p>
        </div>
        <div className="rounded-lg border border-primary/20 bg-primary/5 p-2 text-primary">
          <Icon className="h-4 w-4" />
        </div>
      </div>
    </Card>
  )
}

function TipCard({ tip }: { tip: any }) {
  const variant = tip.tip_type === 'recovery'
    ? 'warning'
    : tip.tip_type === 'optimization'
      ? 'secondary'
      : 'success'

  return (
    <Card className="p-5">
      <div className="flex flex-wrap items-start gap-2 justify-between">
        <div className="space-y-2">
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant={variant as any}>{tip.tip_type}</Badge>
            <Badge variant="outline">{tip.priority}</Badge>
            {tip.outcome_class && <Badge variant="secondary">{tip.outcome_class.replace('_', ' ')}</Badge>}
          </div>
          <h3 className="text-sm font-semibold text-foreground">{tip.title}</h3>
        </div>
        <div className="text-right text-[11px] text-muted-foreground">
          <p>{tip.source_branch || 'repo-wide'}</p>
          <p>{tip.last_retrieved_at ? `Used ${timeAgo(tip.last_retrieved_at)}` : 'Not retrieved yet'}</p>
        </div>
      </div>

      <p className="mt-3 text-sm text-muted-foreground leading-relaxed">{tip.content}</p>

      {tip.trigger_condition && (
        <div className="mt-3 rounded-md border border-border bg-muted/30 px-3 py-2 text-xs text-muted-foreground">
          <span className="font-medium text-foreground">Trigger:</span> {tip.trigger_condition}
        </div>
      )}

      {Array.isArray(tip.action_steps) && tip.action_steps.length > 0 && (
        <div className="mt-4">
          <p className="text-xs font-medium text-foreground">What Gradient injects</p>
          <ol className="mt-2 space-y-1 text-sm text-muted-foreground">
            {tip.action_steps.map((step: string, index: number) => (
              <li key={`${tip.id}-${index}`} className="flex gap-2">
                <span className="text-primary">{index + 1}.</span>
                <span>{step}</span>
              </li>
            ))}
          </ol>
        </div>
      )}

      <div className="mt-4 flex flex-wrap gap-2 text-[11px] text-muted-foreground">
        <span>Confidence {(Number(tip.confidence || 0) * 100).toFixed(0)}%</span>
        <span>Evidence {tip.evidence_count || 0}</span>
        <span>Uses {tip.use_count || 0}</span>
        {tip.failure_signature && <span className="font-mono">{truncate(tip.failure_signature, 36)}</span>}
      </div>
    </Card>
  )
}

function TrajectoryCard({ analysis, task }: { analysis: any; task?: any }) {
  const outcomeVariant = analysis.outcome_class === 'recovered'
    ? 'warning'
    : analysis.outcome_class === 'failure'
      ? 'destructive'
      : analysis.outcome_class === 'inefficient_success'
        ? 'secondary'
        : 'success'

  return (
    <Card className="p-5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant={outcomeVariant as any}>{analysis.outcome_class.replace('_', ' ')}</Badge>
            <Badge variant="outline">{analysis.model_name || 'heuristic'}</Badge>
            {analysis.source_branch && <Badge variant="secondary">{analysis.source_branch}</Badge>}
          </div>
          <h3 className="mt-3 text-sm font-semibold text-foreground">
            {task?.title || analysis.trajectory_summary || 'Trajectory analysis'}
          </h3>
          <p className="mt-1 text-xs text-muted-foreground">
            {analysis.created_at ? `${timeAgo(analysis.created_at)} · ` : ''}
            Confidence {(Number(analysis.confidence || 0) * 100).toFixed(0)}%
          </p>
        </div>
        {task && (
          <div className="text-right text-xs text-muted-foreground">
            <p>{task.status}</p>
            {task.tokens_used > 0 && <p>{task.tokens_used.toLocaleString()} tokens</p>}
          </div>
        )}
      </div>

      {analysis.trajectory_summary && (
        <p className="mt-3 text-sm text-muted-foreground leading-relaxed">{analysis.trajectory_summary}</p>
      )}

      <div className="mt-4 grid gap-3 md:grid-cols-2">
        <div className="rounded-md border border-border bg-muted/30 p-3">
          <p className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">Root cause</p>
          <p className="mt-1 text-sm text-foreground">{analysis.root_cause || 'No root cause recorded.'}</p>
        </div>
        <div className="rounded-md border border-border bg-muted/30 p-3">
          <p className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">Immediate cause</p>
          <p className="mt-1 text-sm text-foreground">{analysis.immediate_cause || 'No immediate cause recorded.'}</p>
        </div>
      </div>

      {(analysis.recovery_action || analysis.recommended_alternative) && (
        <div className="mt-4 grid gap-3 md:grid-cols-2">
          {analysis.recovery_action && (
            <div className="rounded-md border border-amber-500/20 bg-amber-500/5 p-3">
              <p className="text-[11px] font-medium uppercase tracking-wide text-amber-300">Recovery path</p>
              <p className="mt-1 text-sm text-muted-foreground">{analysis.recovery_action}</p>
            </div>
          )}
          {analysis.recommended_alternative && (
            <div className="rounded-md border border-primary/20 bg-primary/5 p-3">
              <p className="text-[11px] font-medium uppercase tracking-wide text-primary">Recommended alternative</p>
              <p className="mt-1 text-sm text-muted-foreground">{analysis.recommended_alternative}</p>
            </div>
          )}
        </div>
      )}

      {Array.isArray(analysis.subtask_analyses) && analysis.subtask_analyses.length > 0 && (
        <div className="mt-4 space-y-2">
          <p className="text-xs font-medium text-foreground">Subtask trajectory</p>
          {analysis.subtask_analyses.map((subtask: any, index: number) => (
            <div key={`${analysis.id}-${subtask.name}-${index}`} className="rounded-md border border-border px-3 py-2">
              <div className="flex flex-wrap items-center gap-2">
                <span className="text-sm font-medium text-foreground">{subtask.name || 'Subtask'}</span>
                <Badge variant="outline">{subtask.outcome_class?.replace('_', ' ') || 'unknown'}</Badge>
                {subtask.failure_signature && (
                  <span className="text-[11px] font-mono text-muted-foreground">{truncate(subtask.failure_signature, 42)}</span>
                )}
              </div>
              {(subtask.summary || subtask.recovery_action || subtask.recommended_alternative) && (
                <p className="mt-1 text-xs text-muted-foreground leading-relaxed">
                  {subtask.summary || subtask.recovery_action || subtask.recommended_alternative}
                </p>
              )}
            </div>
          ))}
        </div>
      )}
    </Card>
  )
}

function RetrievalCard({ item, task }: { item: any; task?: any }) {
  const run = item.run

  return (
    <Card className="p-5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant={run.failure_signature ? 'warning' : 'default'}>
              {run.failure_signature ? 'Recovery-biased' : 'Strategy-biased'}
            </Badge>
            <Badge variant="outline">{run.vector_search_used ? 'Vector + metadata' : 'Metadata + rerank'}</Badge>
            {run.reranker_model && <Badge variant="secondary">{run.reranker_model}</Badge>}
          </div>
          <h3 className="mt-3 text-sm font-semibold text-foreground">
            {task?.title || run.subtask || 'Memory retrieval'}
          </h3>
          <p className="mt-1 text-xs text-muted-foreground">
            {run.created_at ? `${timeAgo(run.created_at)} · ` : ''}
            {run.latency_ms || 0} ms
          </p>
        </div>
        <div className="text-right text-xs text-muted-foreground">
          <p>{run.status}</p>
          <p>{(run.selected_tip_ids || []).length} tips selected</p>
        </div>
      </div>

      {(run.query_text || run.subtask || run.failure_signature) && (
        <div className="mt-3 rounded-md border border-border bg-muted/30 p-3 text-sm text-muted-foreground">
          {run.query_text && <p>{truncate(run.query_text, 220)}</p>}
          <div className="mt-2 flex flex-wrap gap-2 text-[11px]">
            {run.subtask && <span>Subtask: {run.subtask}</span>}
            {run.failure_signature && <span className="font-mono">Failure: {truncate(run.failure_signature, 42)}</span>}
          </div>
        </div>
      )}

      {Array.isArray(item.selected_tips) && item.selected_tips.length > 0 ? (
        <div className="mt-4 grid gap-3">
          {item.selected_tips.map((tip: any) => (
            <div key={tip.id} className="rounded-md border border-border px-3 py-3">
              <div className="flex flex-wrap items-center gap-2">
                <Badge variant={tip.tip_type === 'recovery' ? 'warning' : tip.tip_type === 'optimization' ? 'secondary' : 'success'}>
                  {tip.tip_type}
                </Badge>
                <span className="text-sm font-medium text-foreground">{tip.title}</span>
              </div>
              <p className="mt-2 text-sm text-muted-foreground">{tip.content}</p>
              {tip.trigger_condition && (
                <p className="mt-2 text-[11px] text-muted-foreground">
                  <span className="font-medium text-foreground">Trigger:</span> {tip.trigger_condition}
                </p>
              )}
            </div>
          ))}
        </div>
      ) : (
        <p className="mt-4 text-sm text-muted-foreground">No selected tips were captured for this retrieval.</p>
      )}
    </Card>
  )
}

function BranchContextCard({ context }: { context: any }) {
  return (
    <Card className="p-4">
      <div className="flex items-start justify-between gap-3">
        <div>
          <div className="flex items-center gap-2">
            <GitBranch className="h-4 w-4 text-primary" />
            <p className="text-sm font-semibold text-foreground">{context.branch}</p>
            {context.base_os && <Badge variant="outline">{context.base_os}</Badge>}
          </div>
          <p className="mt-2 text-xs text-muted-foreground">
            Updated {timeAgo(context.updated_at)} · {formatDate(context.updated_at)}
          </p>
        </div>
        <div className="text-right text-xs text-muted-foreground">
          <p>{context.installed_packages?.length || 0} packages</p>
          <p>{context.previous_failures?.length || 0} failures tracked</p>
        </div>
      </div>

      {!!context.patterns && Object.keys(context.patterns).length > 0 && (
        <div className="mt-3 flex flex-wrap gap-1.5">
          {Object.keys(context.patterns).slice(0, 6).map((pattern) => (
            <Badge key={pattern} variant="secondary">{pattern}</Badge>
          ))}
        </div>
      )}
    </Card>
  )
}

function EventCard({ event }: { event: any }) {
  const typeKey = event.type || event.event_type || 'custom'
  const typeInfo = EVENT_TYPES[typeKey] || EVENT_TYPES.custom

  return (
    <div className="rounded-md border border-border px-3 py-3">
      <div className="flex items-start justify-between gap-3">
        <div>
          <div className="flex items-center gap-2">
            <span className="text-sm">{typeInfo.icon}</span>
            <span className={cn('text-xs font-medium', typeInfo.color)}>{typeInfo.label}</span>
            <span className="text-[10px] text-muted-foreground">#{event.sequence || '—'}</span>
          </div>
          <p className="mt-2 text-sm text-muted-foreground break-words">
            {event.data ? JSON.stringify(event.data) : 'No event payload'}
          </p>
        </div>
        <div className="text-right text-[11px] text-muted-foreground">
          <p>{event.branch}</p>
          <p>{event.created_at ? timeAgo(event.created_at) : '—'}</p>
        </div>
      </div>
    </div>
  )
}

function SessionCard({ session, task }: { session: any; task?: any }) {
  return (
    <Card className="p-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant="outline">{session.agent_role || 'agent'}</Badge>
            <Badge variant={session.status === 'completed' ? 'success' : session.status === 'failed' ? 'destructive' : 'secondary'}>
              {session.status}
            </Badge>
            {session.branch_name && <Badge variant="secondary">{session.branch_name}</Badge>}
          </div>
          <h3 className="mt-3 text-sm font-semibold text-foreground">
            {task?.title || session.task_id}
          </h3>
          <p className="mt-1 text-xs text-muted-foreground">
            Started {timeAgo(session.created_at)} · Session {truncate(session.id, 16)}
          </p>
        </div>
        <div className="text-right text-[11px] text-muted-foreground">
          {session.completed_at ? <p>Completed {timeAgo(session.completed_at)}</p> : <p>In progress</p>}
          {task?.estimated_cost > 0 && <p>{formatCurrency(task.estimated_cost)}</p>}
        </div>
      </div>
    </Card>
  )
}

export default function ContextTab() {
  const [searchParams, setSearchParams] = useSearchParams()
  const [activeTab, setActiveTab] = useState('learned')
  const [selectedRepo, setSelectedRepo] = useState(searchParams.get('repo') || '')

  const { data: repos, loading: reposLoading } = useFetch(
    useCallback((token: string, orgId: string) => api.repos.list(token, orgId), [])
  )

  useEffect(() => {
    const repoFromUrl = searchParams.get('repo') || ''
    if (repoFromUrl && repoFromUrl !== selectedRepo) {
      setSelectedRepo(repoFromUrl)
    }
  }, [searchParams, selectedRepo])

  useEffect(() => {
    if (!selectedRepo && Array.isArray(repos) && repos.length > 0) {
      const firstRepo = repos[0]?.repo_full_name
      if (firstRepo) {
        setSelectedRepo(firstRepo)
        setSearchParams({ repo: firstRepo }, { replace: true })
      }
    }
  }, [repos, selectedRepo, setSearchParams])

  const { data: overview, loading, error, refetch } = useFetch(
    useCallback((token: string, orgId: string) => {
      if (!selectedRepo) return Promise.resolve(null)
      return api.memory.overview(token, orgId, selectedRepo, 12)
    }, [selectedRepo]),
    [selectedRepo]
  )

  const repoOptions = useMemo(
    () => (repos || []).map((repo: any) => ({
      value: repo.repo_full_name,
      label: repo.repo_full_name,
    })),
    [repos]
  )

  const taskMap = useMemo(() => {
    const map = new Map<string, any>()
    for (const task of overview?.tasks || []) {
      map.set(task.id, task)
    }
    return map
  }, [overview?.tasks])

  const selectedMeshHealthy = overview?.mesh?.status === 'ok'

  if (reposLoading) {
    return (
      <div className="space-y-6">
        <Skeleton className="h-20 w-full" />
        <div className="grid gap-4 md:grid-cols-4">
          {[1, 2, 3, 4].map((item) => <Skeleton key={item} className="h-28 w-full" />)}
        </div>
        <Skeleton className="h-80 w-full" />
      </div>
    )
  }

  if (!repos || repos.length === 0) {
    return (
      <EmptyState
        icon={FolderGit2}
        title="Connect a repository first"
        description="Gradient's durable memory is organized per repo. Connect GitHub, pick a repo, and the memory workspace will fill in as trajectories run."
        action={(
          <div className="flex flex-wrap items-center justify-center gap-3">
            <Link to="/dashboard/get-started">
              <Button>
                Open onboarding <ArrowRight className="h-3.5 w-3.5" />
              </Button>
            </Link>
            <Link to="/dashboard/integrations">
              <Button variant="outline">
                Configure integrations <KeyRound className="h-3.5 w-3.5" />
              </Button>
            </Link>
          </div>
        )}
      />
    )
  }

  return (
    <div className="space-y-6">
      <Card className="overflow-hidden">
        <div className="border-b border-border bg-gradient-to-r from-primary/10 via-transparent to-transparent px-6 py-5">
          <div className="flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between">
            <div className="max-w-2xl">
              <div className="flex items-center gap-2">
                <Badge variant="secondary">Repo memory</Badge>
                <Badge variant={selectedMeshHealthy ? 'success' : 'destructive'}>
                  <Network className="mr-1 h-3 w-3" />
                  {overview?.mesh?.bus === 'nats' ? 'Live mesh' : 'Local mesh'}
                </Badge>
              </div>
              <h2 className="mt-3 text-xl font-semibold text-foreground">
                Durable memory for {selectedRepo || 'your repo'}
              </h2>
              <p className="mt-2 text-sm text-muted-foreground leading-relaxed">
                This workspace shows the tips Gradient has distilled from past executions, the trajectories it attributed, what it retrieves at runtime, and what the MCP/live context layer is sharing right now.
              </p>
            </div>

            <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
              <div className="min-w-[260px]">
                <Select
                  label="Repository"
                  value={selectedRepo}
                  onChange={(event) => {
                    const nextRepo = event.target.value
                    setSelectedRepo(nextRepo)
                    setSearchParams({ repo: nextRepo }, { replace: true })
                  }}
                  options={repoOptions}
                />
              </div>
              <Button variant="outline" onClick={() => refetch()}>
                <RefreshCw className="h-3.5 w-3.5" />
                Refresh
              </Button>
            </div>
          </div>
        </div>

        <div className="grid gap-4 px-6 py-5 md:grid-cols-2 xl:grid-cols-5">
          <MemoryStatCard
            label="Learned tips"
            value={overview?.summary?.tips || 0}
            hint={`${overview?.summary?.strategy_tips || 0} strategy · ${overview?.summary?.recovery_tips || 0} recovery`}
            icon={Lightbulb}
          />
          <MemoryStatCard
            label="Trajectories"
            value={overview?.summary?.trajectory_analyses || 0}
            hint="Attributed runs used to generate durable memory"
            icon={Route}
          />
          <MemoryStatCard
            label="Guidance retrievals"
            value={overview?.summary?.retrieval_runs || 0}
            hint="Prompt injections recorded for this repo"
            icon={Sparkles}
          />
          <MemoryStatCard
            label="Tracked branches"
            value={overview?.summary?.branches || 0}
            hint={`${overview?.summary?.live_events || 0} recent live mesh events`}
            icon={GitBranch}
          />
          <MemoryStatCard
            label="Agent sessions"
            value={overview?.summary?.sessions || 0}
            hint={selectedMeshHealthy ? 'MCP and live mesh healthy' : 'Check live mesh connectivity'}
            icon={Workflow}
          />
        </div>
      </Card>

      <Callout variant="tip" title="How pricing and memory fit together">
        Anthropic or OpenAI token spend stays transparent on the provider side. Gradient bills separately for repo memory, trajectory analysis, retrieval, and runtime credits, and this page is the clearest view into that durable layer.
      </Callout>

      {loading ? (
        <div className="space-y-4">
          <Skeleton className="h-12 w-full" />
          <Skeleton className="h-96 w-full" />
        </div>
      ) : error ? (
        <Card className="p-6">
          <p className="text-sm text-destructive">{error}</p>
        </Card>
      ) : !overview ? (
        <EmptyState
          icon={Brain}
          title="Pick a repository"
          description="Select one of your connected repos to inspect its memory and trajectory history."
        />
      ) : (
        <>
          <Tabs
            tabs={[
              { id: 'learned', label: 'Learned Memory', icon: <Lightbulb className="h-3.5 w-3.5" /> },
              { id: 'trajectories', label: 'Trajectories', icon: <Route className="h-3.5 w-3.5" /> },
              { id: 'guidance', label: 'Selected Guidance', icon: <Sparkles className="h-3.5 w-3.5" /> },
              { id: 'live', label: 'Live Mesh + MCP', icon: <Network className="h-3.5 w-3.5" /> },
              { id: 'sessions', label: 'Sessions', icon: <SplitSquareVertical className="h-3.5 w-3.5" /> },
            ]}
            active={activeTab}
            onChange={setActiveTab}
          />

          {activeTab === 'learned' && (
            <div className="space-y-4">
              <div className="grid gap-4 lg:grid-cols-[1.1fr_0.9fr]">
                <Card className="p-5">
                  <h3 className="text-sm font-semibold text-foreground">What this repo has learned</h3>
                  <p className="mt-2 text-sm text-muted-foreground">
                    Gradient no longer stores raw branch chatter as long-term memory. It distills clean strategies, recoveries, and optimization tips with provenance and injects only the most relevant guidance into the next task prompt.
                  </p>
                </Card>
                <Card className="p-5">
                  <h3 className="text-sm font-semibold text-foreground">Current memory mix</h3>
                  <div className="mt-3 flex flex-wrap gap-2">
                    <Badge variant="success">{overview.summary?.strategy_tips || 0} strategy tips</Badge>
                    <Badge variant="warning">{overview.summary?.recovery_tips || 0} recovery tips</Badge>
                    <Badge variant="secondary">{overview.summary?.optimization_tips || 0} optimization tips</Badge>
                  </div>
                </Card>
              </div>

              {(overview.tips || []).length === 0 ? (
                <EmptyState
                  icon={Lightbulb}
                  title="No durable tips yet"
                  description="Once Gradient completes or recovers from a few tasks in this repo, strategy and recovery tips will show up here."
                />
              ) : (
                <div className="grid gap-4">
                  {overview.tips.map((tip: any) => <TipCard key={tip.id} tip={tip} />)}
                </div>
              )}
            </div>
          )}

          {activeTab === 'trajectories' && (
            <div className="space-y-4">
              {(overview.analyses || []).length === 0 ? (
                <EmptyState
                  icon={Route}
                  title="No trajectory analyses yet"
                  description="Trajectory analyses appear after tasks finish and Gradient attributes what caused the success, failure, recovery, or inefficiency."
                />
              ) : (
                overview.analyses.map((analysis: any) => (
                  <TrajectoryCard
                    key={analysis.id}
                    analysis={analysis}
                    task={taskMap.get(analysis.task_id)}
                  />
                ))
              )}
            </div>
          )}

          {activeTab === 'guidance' && (
            <div className="space-y-4">
              <Card className="p-5">
                <h3 className="text-sm font-semibold text-foreground">What the agent actually selected</h3>
                <p className="mt-2 text-sm text-muted-foreground">
                  Each retrieval run shows the repo-scoped query, whether vector search was used, and the exact tips chosen for the prompt. This is the clearest runtime view of the memory system.
                </p>
              </Card>

              {(overview.retrievals || []).length === 0 ? (
                <EmptyState
                  icon={Sparkles}
                  title="No guidance retrievals yet"
                  description="After the next task starts, Gradient will record which durable tips it selected and why."
                />
              ) : (
                overview.retrievals.map((item: any, index: number) => (
                  <RetrievalCard
                    key={item.run?.id || index}
                    item={item}
                    task={taskMap.get(item.run?.task_id)}
                  />
                ))
              )}
            </div>
          )}

          {activeTab === 'live' && (
            <div className="grid gap-4 xl:grid-cols-[0.95fr_1.05fr]">
              <div className="space-y-4">
                <Card className="p-5">
                  <div className="flex items-center justify-between gap-3">
                    <div>
                      <h3 className="text-sm font-semibold text-foreground">Memory persistence MCP</h3>
                      <p className="mt-1 text-sm text-muted-foreground">
                        The MCP is the runtime bridge between live repo state and durable memory retrieval.
                      </p>
                    </div>
                    <div className="flex items-center gap-2 text-xs text-muted-foreground">
                      <StatusDot status={selectedMeshHealthy ? 'connected' : 'disconnected'} />
                      {selectedMeshHealthy ? 'Healthy' : 'Attention needed'}
                    </div>
                  </div>

                  <div className="mt-4 grid gap-3">
                    {(overview.mcp?.tools || []).map((tool: any) => (
                      <div key={tool.name} className="rounded-md border border-border px-3 py-3">
                        <div className="flex items-center gap-2">
                          <Wrench className="h-3.5 w-3.5 text-primary" />
                          <span className="text-sm font-medium text-foreground">{tool.name}</span>
                        </div>
                        <p className="mt-1 text-xs text-muted-foreground">{tool.purpose}</p>
                      </div>
                    ))}
                  </div>

                  <div className="mt-4 flex flex-wrap gap-2">
                    {(overview.mcp?.artifacts || []).map((artifact: string) => (
                      <Badge key={artifact} variant="secondary">{artifact}</Badge>
                    ))}
                  </div>
                </Card>

                <Card className="p-5">
                  <h3 className="text-sm font-semibold text-foreground">Tracked branch contexts</h3>
                  <div className="mt-4 space-y-3">
                    {(overview.contexts || []).length === 0 ? (
                      <p className="text-sm text-muted-foreground">No saved branch contexts for this repo yet.</p>
                    ) : (
                      overview.contexts.map((context: any) => (
                        <BranchContextCard key={context.id || context.branch} context={context} />
                      ))
                    )}
                  </div>
                </Card>
              </div>

              <div className="space-y-4">
                <Card className="p-5">
                  <div className="flex items-center justify-between gap-3">
                    <div>
                      <h3 className="text-sm font-semibold text-foreground">Live context mesh</h3>
                      <p className="mt-1 text-sm text-muted-foreground">
                        Operational deltas still move through the mesh, but only the durable learnings graduate into repo memory.
                      </p>
                    </div>
                    <Badge variant={selectedMeshHealthy ? 'success' : 'destructive'}>
                      <Activity className="mr-1 h-3 w-3" />
                      {overview.mesh?.bus || 'mesh'}
                    </Badge>
                  </div>

                  <div className="mt-4 grid gap-3 sm:grid-cols-2">
                    <div className="rounded-md border border-border bg-muted/30 p-3">
                      <p className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">Bus</p>
                      <p className="mt-1 text-sm text-foreground">{overview.mesh?.bus || 'local'}</p>
                    </div>
                    <div className="rounded-md border border-border bg-muted/30 p-3">
                      <p className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">Connection</p>
                      <p className="mt-1 text-sm text-foreground">
                        {overview.mesh?.connected === false ? 'Disconnected' : 'Connected'}
                      </p>
                    </div>
                  </div>
                </Card>

                <Card className="p-5">
                  <h3 className="text-sm font-semibold text-foreground">Recent repo events</h3>
                  <div className="mt-4 space-y-3">
                    {(overview.events || []).length === 0 ? (
                      <p className="text-sm text-muted-foreground">No live events recorded for this repo yet.</p>
                    ) : (
                      overview.events.map((event: any) => (
                        <EventCard key={event.id} event={event} />
                      ))
                    )}
                  </div>
                </Card>
              </div>
            </div>
          )}

          {activeTab === 'sessions' && (
            <div className="grid gap-4 xl:grid-cols-[0.8fr_1.2fr]">
              <Card className="p-5">
                <h3 className="text-sm font-semibold text-foreground">Recent repo task activity</h3>
                <div className="mt-4 overflow-hidden rounded-lg border border-border">
                  <Table headers={['Task', 'Status', 'Branch', 'AI cost']}>
                    {(overview.tasks || []).length === 0 ? (
                      <TableRow>
                        <td colSpan={4} className="p-4 text-sm text-muted-foreground">
                          No task activity yet.
                        </td>
                      </TableRow>
                    ) : (
                      overview.tasks.map((task: any) => (
                        <TableRow key={task.id}>
                          <TableCell>
                            <div>
                              <p className="text-sm font-medium text-foreground">{truncate(task.title, 56)}</p>
                              <p className="text-xs text-muted-foreground">{timeAgo(task.created_at)}</p>
                            </div>
                          </TableCell>
                          <TableCell>
                            <Badge variant={task.status === 'complete' ? 'success' : task.status === 'failed' ? 'destructive' : 'secondary'}>
                              {task.status}
                            </Badge>
                          </TableCell>
                          <TableCell className="text-xs text-muted-foreground">{task.branch || '—'}</TableCell>
                          <TableCell className="text-xs text-muted-foreground">
                            {task.estimated_cost > 0 ? formatCurrency(task.estimated_cost) : '—'}
                          </TableCell>
                        </TableRow>
                      ))
                    )}
                  </Table>
                </div>
              </Card>

              <div className="space-y-4">
                {(overview.sessions || []).length === 0 ? (
                  <EmptyState
                    icon={SplitSquareVertical}
                    title="No agent sessions yet"
                    description="Agent sessions appear once Gradient starts executing tasks for this repository."
                  />
                ) : (
                  overview.sessions.map((session: any) => (
                    <SessionCard
                      key={session.id}
                      session={session}
                      task={taskMap.get(session.task_id)}
                    />
                  ))
                )}
              </div>
            </div>
          )}
        </>
      )}
    </div>
  )
}
