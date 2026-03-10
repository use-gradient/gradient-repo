import { useState, useCallback, useEffect } from 'react'
import { api } from '@/api/client'
import { useFetch, useMutation } from '@/hooks/useAPI'
import { cn, timeAgo } from '@/lib/utils'
import {
  Button, Card, Badge, Input, Modal, Skeleton, useToast, CodeBlock,
  EmptyState, ConfirmDialog, Table, TableRow, TableCell, Tabs, Select,
} from '@/components/ui'
import {
  Plug, CheckCircle2, ExternalLink, Trash2,
  Bot, CreditCard, GitBranch, Key, ArrowRight,
  Zap, Settings, Plus, Github, Link, Unlink, Camera,
  Terminal, Clock, RefreshCw, Search, RotateCcw
} from 'lucide-react'

/* ─── Claude Config Modal ─── */
function ClaudeConfigModal({ open, onClose, onSaved, existing }: {
  open: boolean; onClose: () => void; onSaved: () => void; existing?: any
}) {
  const [apiKey, setApiKey] = useState('')
  const [model, setModel] = useState(existing?.model || 'claude-sonnet-4-20250514')
  const [maxTurns, setMaxTurns] = useState(existing?.max_turns?.toString() || '50')
  const { toast } = useToast()

  const { mutate, loading, error } = useMutation(
    (token: string, orgId: string, body: any) => api.integrations.claude.save(token, orgId, body)
  )

  const handleSave = async () => {
    const result = await mutate({
      api_key: apiKey,
      model,
      max_turns: parseInt(maxTurns) || 50,
    })
    if (result) {
      toast('success', 'Claude Code configured')
      setApiKey('')
      onSaved()
      onClose()
    }
  }

  return (
    <Modal open={open} onClose={onClose} title="Configure Claude Code" description="Add your Anthropic API key to enable AI agent tasks" size="sm" footer={
      <div className="flex gap-2">
        <Button variant="outline" onClick={onClose}>Cancel</Button>
        <Button onClick={handleSave} loading={loading} disabled={!apiKey}>
          <Key className="w-3.5 h-3.5" /> Save
        </Button>
      </div>
    }>
      <div className="space-y-4">
        <Input
          label="Anthropic API Key"
          placeholder="sk-ant-..."
          value={apiKey}
          onChange={e => setApiKey(e.target.value)}
          autoFocus
          mono
          type="password"
        />
        {existing?.configured && existing?.api_key_masked && (
          <p className="text-xs text-muted-foreground">
            Current key: <code className="text-foreground">{existing.api_key_masked}</code>
          </p>
        )}
        <Input
          label="Model"
          placeholder="claude-sonnet-4-20250514"
          value={model}
          onChange={e => setModel(e.target.value)}
          mono
        />
        <Input
          label="Max Turns"
          placeholder="50"
          value={maxTurns}
          onChange={e => setMaxTurns(e.target.value)}
          type="number"
        />
        <p className="text-xs text-muted-foreground">
          Get an API key from{' '}
          <a href="https://console.anthropic.com/" target="_blank" rel="noopener noreferrer" className="text-primary hover:underline">
            console.anthropic.com <ExternalLink className="w-3 h-3 inline" />
          </a>
        </p>
        {error && <p className="text-xs text-destructive">{error}</p>}
      </div>
    </Modal>
  )
}

/* ─── Connect Repo Modal ─── */
function ConnectRepoModal({ open, onClose, onConnected, githubConnected }: {
  open: boolean; onClose: () => void; onConnected: () => void; githubConnected: boolean
}) {
  const [repoName, setRepoName] = useState('')
  const [manualInput, setManualInput] = useState(false)
  const { toast } = useToast()
  const { mutate, loading, error } = useMutation(
    (token: string, orgId: string, body: any) => api.repos.connect(token, orgId, body)
  )
  
  const { data: availableData, loading: loadingAvailable, refetch } = useFetch(
    useCallback((token: string, orgId: string) => {
      if (!open || !githubConnected) return Promise.resolve({ repos: [] })
      return api.repos.available(token, orgId)
    }, [open, githubConnected])
  )
  const availableRepos = availableData?.repos || []

  useEffect(() => {
    if (open && githubConnected) refetch()
  }, [open, githubConnected, refetch])

  const handleConnect = async () => {
    if (!repoName) return
    const result = await mutate({ repo: repoName })
    if (result) {
      toast('success', `Repository "${repoName}" connected`)
      setRepoName('')
      setManualInput(false)
      onConnected()
      onClose()
    }
  }

  return (
    <Modal open={open} onClose={onClose} title="Connect Repository" description="Link a GitHub repo for auto-fork context" size="sm" footer={
      <Button onClick={handleConnect} loading={loading} disabled={!repoName}>
        <Github className="w-3.5 h-3.5" /> Connect
      </Button>
    }>
      <div className="space-y-4">
        {!githubConnected ? (
          <div className="p-4 rounded-md bg-muted/50 border border-border text-center">
            <p className="text-sm text-muted-foreground">
              Connect your GitHub account first from the Connections tab.
            </p>
          </div>
        ) : loadingAvailable ? (
          <Skeleton className="h-10 w-full" />
        ) : availableRepos.length > 0 ? (
          <>
            <Select
              label="Repository"
              placeholder="Select a repository..."
              value={repoName}
              onChange={e => setRepoName(e.target.value)}
              options={availableRepos.map((repo: string) => ({ value: repo, label: repo }))}
            />
            {!manualInput && (
              <button type="button" onClick={() => setManualInput(true)}
                className="text-xs text-muted-foreground hover:text-foreground underline">
                Or enter repository manually
              </button>
            )}
          </>
        ) : (
          <div className="space-y-3">
            <p className="text-sm text-muted-foreground text-center">
              No additional repositories found. Enter a repo name manually.
            </p>
            {!manualInput && (
              <button type="button" onClick={() => setManualInput(true)}
                className="text-xs text-muted-foreground hover:text-foreground underline">
                Enter repository manually
              </button>
            )}
          </div>
        )}
        
        {manualInput && (
          <Input label="Repository" placeholder="owner/repo-name" value={repoName}
            onChange={e => setRepoName(e.target.value)} mono />
        )}
        
        <p className="text-xs text-muted-foreground">
          When a new branch is created, Gradient automatically copies the parent branch&#39;s context + snapshot pointers.
        </p>
        {error && <p className="text-xs text-destructive">{error}</p>}
        <div className="pt-2 border-t border-border">
          <p className="text-[10px] text-muted-foreground flex items-center gap-1.5 mb-1"><Terminal className="w-3 h-3" /> CLI equivalent</p>
          <CodeBlock code={`gc repo connect --repo ${repoName || 'owner/repo'}`} />
        </div>
      </div>
    </Modal>
  )
}

/* ─── Main Integrations Tab ─── */
export default function IntegrationsTab() {
  const [showClaude, setShowClaude] = useState(false)
  const [showConnect, setShowConnect] = useState(false)
  const [activeSection, setActiveSection] = useState('integrations')
  const [disconnectRepoId, setDisconnectRepoId] = useState<string | null>(null)
  const [snapshotBranch, setSnapshotBranch] = useState('')
  const { toast } = useToast()

  const { data: status, loading, refetch } = useFetch(
    useCallback((token: string, orgId: string) => api.integrations.status(token, orgId), [])
  )

  const { data: repos, loading: reposLoading, refetch: refetchRepos } = useFetch(
    useCallback((token: string, orgId: string) => api.repos.list(token, orgId), [])
  )

  const { data: snapshots, loading: snapsLoading, refetch: refetchSnaps } = useFetch(
    useCallback((token: string, orgId: string) => {
      const params: Record<string, string> = {}
      if (snapshotBranch) params.branch = snapshotBranch
      return api.snapshots.list(token, orgId, params)
    }, [snapshotBranch]),
    [snapshotBranch]
  )

  const { mutate: getLinearURL, loading: linearLoading } = useMutation(
    (token: string, orgId: string, _: any) => api.integrations.linear.authUrl(token, orgId)
  )

  const { mutate: disconnectLinear, loading: disconnectingLinear } = useMutation(
    (token: string, orgId: string, _: any) => api.integrations.linear.disconnect(token, orgId)
  )

  const { mutate: disconnectClaude, loading: disconnectingClaude } = useMutation(
    (token: string, orgId: string, _: any) => api.integrations.claude.disconnect(token, orgId)
  )

  const { mutate: disconnectRepo, loading: disconnectingRepo } = useMutation(
    (token: string, orgId: string, id: string) => api.repos.disconnect(token, orgId, id)
  )

  const { mutate: getGitHubURL, loading: githubLoading } = useMutation(
    (token: string, orgId: string, _: any) => api.integrations.github.authUrl(token, orgId)
  )

  const { mutate: exchangeGitHubCode, loading: githubExchanging } = useMutation(
    (token: string, orgId: string, body: any) => api.integrations.github.callback(token, orgId, body)
  )

  const { mutate: disconnectGitHub, loading: disconnectingGitHub } = useMutation(
    (token: string, orgId: string, _: any) => api.integrations.github.disconnect(token, orgId)
  )

  const { mutate: exchangeLinearCode, loading: linearExchanging } = useMutation(
    (token: string, orgId: string, body: any) => api.integrations.linear.callback(token, orgId, body)
  )

  // Handle OAuth callback — read ?code= from URL, try Linear first then GitHub
  useEffect(() => {
    const params = new URLSearchParams(window.location.search)
    const code = params.get('code')
    const state = params.get('state')
    if (!code) return

    window.history.replaceState({}, '', window.location.pathname)
    const provider = localStorage.getItem('oauth_provider')
    localStorage.removeItem('oauth_provider')

    if (provider === 'linear') {
      exchangeLinearCode({ code, state: state || '' }).then(result => {
        if (result?.connected) {
          toast('success', `Linear connected to ${result.workspace_name || 'workspace'}`)
          refetch()
        }
      })
    } else if (provider === 'github') {
      exchangeGitHubCode({ code, state: state || '' }).then(result => {
        if (result?.connected) {
          toast('success', `GitHub connected as ${result.github_user}`)
          refetch()
        }
      })
    } else {
      // No provider flag (e.g. cross-domain localStorage lost) — try Linear first, fall back to GitHub
      exchangeLinearCode({ code, state: state || '' }).then(result => {
        if (result?.connected) {
          toast('success', `Linear connected to ${result.workspace_name || 'workspace'}`)
          refetch()
        } else {
          exchangeGitHubCode({ code, state: state || '' }).then(ghResult => {
            if (ghResult?.connected) {
              toast('success', `GitHub connected as ${ghResult.github_user}`)
              refetch()
            }
          })
        }
      })
    }
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  const handleConnectGitHub = async () => {
    const result = await getGitHubURL(null)
    if (result?.url) {
      localStorage.setItem('oauth_provider', 'github')
      window.location.href = result.url
    }
  }

  const handleDisconnectGitHub = async () => {
    await disconnectGitHub(null)
    toast('success', 'GitHub disconnected')
    refetch()
  }

  const handleConnectLinear = async () => {
    const result = await getLinearURL(null)
    if (result?.url) {
      localStorage.setItem('oauth_provider', 'linear')
      window.location.href = result.url
    }
  }

  const handleDisconnectLinear = async () => {
    await disconnectLinear(null)
    toast('success', 'Linear disconnected')
    refetch()
  }

  const handleDisconnectClaude = async () => {
    await disconnectClaude(null)
    toast('success', 'Claude Code disconnected')
    refetch()
  }

  const handleDisconnectRepo = async () => {
    if (!disconnectRepoId) return
    const result = await disconnectRepo(disconnectRepoId)
    if (result !== null) {
      toast('success', 'Repository disconnected')
      setDisconnectRepoId(null)
      refetchRepos()
      refetch()
    }
  }

  const handleRepoConnected = () => {
    refetchRepos()
    refetch()
  }

  const isReady = status?.ready
  const githubConnected = !!status?.github?.connected

  if (loading) {
    return <div className="space-y-4">{[1,2,3,4].map(i => <Skeleton key={i} className="h-24 w-full" />)}</div>
  }

  return (
    <div className="space-y-6">
      {/* Readiness banner */}
      <Card className={cn('p-5', isReady ? 'border-emerald-500/30 bg-emerald-500/5' : 'border-yellow-500/30 bg-yellow-500/5')}>
        <div className="flex items-center gap-3">
          {isReady ? (
            <CheckCircle2 className="w-5 h-5 text-emerald-400 shrink-0" />
          ) : (
            <Zap className="w-5 h-5 text-yellow-400 shrink-0" />
          )}
          <div className="flex-1">
            <p className="text-sm font-medium text-foreground">
              {isReady ? 'Agent tasks are ready!' : 'Complete setup to enable agent tasks'}
            </p>
            <p className="text-xs text-muted-foreground mt-0.5">
              {isReady
                ? 'Linear issues labeled "gradient-agent" will be automatically picked up.'
                : 'Connect Linear and configure Claude Code to start running AI agent tasks.'}
            </p>
          </div>
        </div>
      </Card>

      {/* Section tabs */}
      <Tabs
        tabs={[
          { id: 'integrations', label: 'Connections', icon: <Plug className="w-3.5 h-3.5" /> },
          { id: 'repos', label: 'Repositories', icon: <Github className="w-3.5 h-3.5" /> },
          { id: 'snapshots', label: 'Snapshots', icon: <Camera className="w-3.5 h-3.5" /> },
        ]}
        active={activeSection}
        onChange={setActiveSection}
      />

      {/* ─── Connections Section ─── */}
      {activeSection === 'integrations' && (
        <div className="grid gap-4">
          {/* Linear */}
          <Card className="p-5">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-3">
                <div className={cn(
                  'w-10 h-10 rounded-lg flex items-center justify-center',
                  status?.linear?.connected ? 'bg-primary/10' : 'bg-muted',
                )}>
                  <svg className="w-5 h-5" viewBox="0 0 24 24" fill="currentColor">
                    <path d="M2.893 2.893a3.22 3.22 0 0 1 4.556 0l13.658 13.658a3.22 3.22 0 0 1-4.556 4.556L2.893 7.449a3.22 3.22 0 0 1 0-4.556Z" />
                    <path d="M2.893 16.551a3.22 3.22 0 0 0 4.556 4.556l6.858-6.858a3.22 3.22 0 0 0-4.556-4.556l-6.858 6.858Z" />
                  </svg>
                </div>
                <div>
                  <p className="text-sm font-medium text-foreground">Linear</p>
                  <p className="text-xs text-muted-foreground">
                    {status?.linear?.connected
                      ? `Connected to ${status.linear.workspace_name || 'workspace'}`
                      : 'Connect your Linear workspace for task management'}
                  </p>
                </div>
              </div>
              <div className="flex items-center gap-2">
                {status?.linear?.connected ? (
                  <>
                    <Badge className="bg-emerald-500/10 text-emerald-400 border-emerald-500/20">
                      <CheckCircle2 className="w-3 h-3 mr-1" /> Connected
                    </Badge>
                    <Button variant="ghost" size="sm" onClick={handleDisconnectLinear} loading={disconnectingLinear}>
                      <Trash2 className="w-3.5 h-3.5" />
                    </Button>
                  </>
                ) : (
                  <Button size="sm" onClick={handleConnectLinear} loading={linearLoading}>
                    <Plug className="w-3.5 h-3.5" /> Connect Linear
                  </Button>
                )}
              </div>
            </div>
          </Card>

          {/* Claude Code */}
          <Card className="p-5">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-3">
                <div className={cn(
                  'w-10 h-10 rounded-lg flex items-center justify-center',
                  status?.claude?.configured ? 'bg-primary/10' : 'bg-muted',
                )}>
                  <Bot className="w-5 h-5" />
                </div>
                <div>
                  <p className="text-sm font-medium text-foreground">Claude Code</p>
                  <p className="text-xs text-muted-foreground">
                    {status?.claude?.configured
                      ? `Model: ${status.claude.model || 'claude-sonnet-4-20250514'}`
                      : 'Add your Anthropic API key for AI agent execution'}
                  </p>
                </div>
              </div>
              <div className="flex items-center gap-2">
                {status?.claude?.configured ? (
                  <>
                    <Badge className="bg-emerald-500/10 text-emerald-400 border-emerald-500/20">
                      <CheckCircle2 className="w-3 h-3 mr-1" /> Configured
                    </Badge>
                    <Button variant="ghost" size="sm" onClick={() => setShowClaude(true)}>
                      <Settings className="w-3.5 h-3.5" />
                    </Button>
                    <Button variant="ghost" size="sm" onClick={handleDisconnectClaude} loading={disconnectingClaude}>
                      <Trash2 className="w-3.5 h-3.5" />
                    </Button>
                  </>
                ) : (
                  <Button size="sm" onClick={() => setShowClaude(true)}>
                    <Key className="w-3.5 h-3.5" /> Add API Key
                  </Button>
                )}
              </div>
            </div>
          </Card>

          {/* GitHub OAuth */}
          <Card className="p-5">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-3">
                <div className={cn(
                  'w-10 h-10 rounded-lg flex items-center justify-center',
                  githubConnected ? 'bg-primary/10' : 'bg-muted',
                )}>
                  <Github className="w-5 h-5" />
                </div>
                <div>
                  <p className="text-sm font-medium text-foreground">GitHub</p>
                  <p className="text-xs text-muted-foreground">
                    {githubConnected
                      ? <>Connected as <span className="text-foreground font-medium">{status.github.github_user}</span>{status.repos?.count > 0 ? ` · ${status.repos.count} repo${status.repos.count !== 1 ? 's' : ''}` : ''}</>
                      : 'Authenticate with GitHub to connect repositories'}
                  </p>
                </div>
              </div>
              <div className="flex items-center gap-2">
                {githubConnected ? (
                  <>
                    <Badge className="bg-emerald-500/10 text-emerald-400 border-emerald-500/20">
                      <CheckCircle2 className="w-3 h-3 mr-1" /> Connected
                    </Badge>
                    <Button variant="outline" size="sm" onClick={() => setActiveSection('repos')}>
                      Repos <ArrowRight className="w-3 h-3" />
                    </Button>
                    <Button variant="ghost" size="sm" onClick={handleDisconnectGitHub} loading={disconnectingGitHub}>
                      <Trash2 className="w-3.5 h-3.5" />
                    </Button>
                  </>
                ) : (
                  <Button size="sm" onClick={handleConnectGitHub} loading={githubLoading || githubExchanging}>
                    <Github className="w-3.5 h-3.5" /> Connect GitHub
                  </Button>
                )}
              </div>
            </div>
          </Card>

          {/* Billing status */}
          <Card className="p-5">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-3">
                <div className={cn(
                  'w-10 h-10 rounded-lg flex items-center justify-center',
                  status?.billing?.active ? 'bg-primary/10' : 'bg-muted',
                )}>
                  <CreditCard className="w-5 h-5" />
                </div>
                <div>
                  <p className="text-sm font-medium text-foreground">Billing</p>
                  <p className="text-xs text-muted-foreground">
                    {status?.billing?.active
                      ? `${status.billing.tier || 'Paid'} tier active`
                      : 'Set up billing for production use'}
                  </p>
                </div>
              </div>
              {status?.billing?.active ? (
                <Badge className="bg-emerald-500/10 text-emerald-400 border-emerald-500/20">
                  <CheckCircle2 className="w-3 h-3 mr-1" /> Active
                </Badge>
              ) : (
                <Button variant="outline" size="sm" asChild>
                  <a href="/dashboard/billing">
                    <CreditCard className="w-3.5 h-3.5" /> Set Up <ArrowRight className="w-3 h-3" />
                  </a>
                </Button>
              )}
            </div>
          </Card>

          {/* How it works */}
          <Card className="p-5">
            <h3 className="text-xs font-medium text-foreground mb-3 flex items-center gap-1.5">
              <Zap className="w-3.5 h-3.5 text-primary" /> How Agent Tasks Work
            </h3>
            <div className="space-y-3 text-xs text-muted-foreground">
              {[
                'Label a Linear issue with "gradient-agent" and move it to Todo',
                'Gradient spins up a cloud environment with your repo context',
                'Claude Code works on the task autonomously (edit, test, commit)',
                'A pull request is created and the Linear issue is updated',
              ].map((s, i) => (
                <div key={i} className="flex gap-3">
                  <span className="text-primary font-mono text-[10px] mt-0.5">{String(i + 1).padStart(2, '0')}</span>
                  <p>{s}</p>
                </div>
              ))}
            </div>
          </Card>
        </div>
      )}

      {/* ─── Repositories Section ─── */}
      {activeSection === 'repos' && (
        <div className="space-y-4">
          <div className="flex items-center justify-between">
            <p className="text-xs text-muted-foreground">Connected repositories get auto-fork on new branches.</p>
            <div className="flex items-center gap-2">
              <Button variant="ghost" size="sm" onClick={refetchRepos}><RefreshCw className="w-3.5 h-3.5" /></Button>
              <Button size="sm" onClick={() => setShowConnect(true)}><Plus className="w-3.5 h-3.5" /> Connect Repo</Button>
            </div>
          </div>

          {reposLoading ? (
            [1,2].map(i => <Skeleton key={i} className="h-20 w-full" />)
          ) : !repos || repos.length === 0 ? (
            <EmptyState
              icon={Github}
              title="No repositories connected"
              description="Connect a GitHub repository to enable auto-fork context. When a branch is created, its parent's context is automatically copied."
              action={<Button size="sm" onClick={() => setShowConnect(true)}><Plus className="w-3.5 h-3.5" /> Connect Repository</Button>}
            />
          ) : (
            <div className="space-y-3">
              {repos.map((repo: any) => (
                <Card key={repo.id} className="p-4">
                  <div className="flex items-center justify-between">
                    <div className="flex items-center gap-3">
                      <Github className="w-5 h-5 text-muted-foreground" />
                      <div>
                        <p className="text-sm font-medium text-foreground font-mono">{repo.repo_full_name}</p>
                        <div className="flex items-center gap-3 mt-1 text-[10px] text-muted-foreground">
                          <Badge variant="success"><Link className="w-2.5 h-2.5 mr-1" /> Connected</Badge>
                          {repo.created_at && <span className="flex items-center gap-1"><Clock className="w-2.5 h-2.5" />{timeAgo(repo.created_at)}</span>}
                        </div>
                      </div>
                    </div>
                    <button
                      onClick={() => setDisconnectRepoId(repo.id)}
                      className="text-muted-foreground hover:text-destructive transition-colors p-2"
                      aria-label={`Disconnect ${repo.repo_full_name}`}
                    >
                      <Unlink className="w-4 h-4" />
                    </button>
                  </div>
                </Card>
              ))}
            </div>
          )}

          <Card className="p-5">
            <h3 className="text-xs font-medium text-foreground mb-3 flex items-center gap-1.5"><GitBranch className="w-3.5 h-3.5 text-primary" /> How Auto-Fork Works</h3>
            <div className="space-y-3 text-xs text-muted-foreground">
              {[
                <>Authenticate with GitHub via <code className="text-foreground font-mono bg-secondary px-1 rounded">gc repo auth</code> or the Connections tab</>,
                <>Connect a repo with <code className="text-foreground font-mono bg-secondary px-1 rounded">gc repo connect --repo owner/repo</code></>,
                <>Create a branch: <code className="text-foreground font-mono bg-secondary px-1 rounded">git checkout -b feature/new</code></>,
                "Gradient copies main's context + snapshots to the new branch automatically",
              ].map((text, i) => (
                <div key={i} className="flex gap-3">
                  <span className="text-primary font-mono text-[10px] mt-0.5">{String(i + 1).padStart(2, '0')}</span>
                  <p>{text}</p>
                </div>
              ))}
            </div>
          </Card>
        </div>
      )}

      {/* ─── Snapshots Section ─── */}
      {activeSection === 'snapshots' && (
        <div className="space-y-4">
          <div className="flex items-center justify-between">
            <div className="relative max-w-xs">
              <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
              <input
                type="search"
                placeholder="Filter by branch…"
                value={snapshotBranch}
                onChange={e => setSnapshotBranch(e.target.value)}
                className="w-full bg-card border border-input rounded-md pl-9 pr-3 py-2 text-sm text-foreground placeholder:text-muted-foreground outline-none focus:ring-1 focus:ring-ring"
                aria-label="Filter snapshots by branch"
              />
            </div>
            <Button variant="ghost" size="sm" onClick={refetchSnaps}><RefreshCw className="w-3.5 h-3.5" /></Button>
          </div>

          {snapsLoading ? (
            <Skeleton className="h-40 w-full" />
          ) : !snapshots || snapshots.length === 0 ? (
            <EmptyState
              icon={Camera}
              title="No snapshots yet"
              description="Snapshots are created automatically every 15 minutes, on git push, and when environments are stopped."
            />
          ) : (
            <Card>
              <Table headers={['Snapshot ID', 'Environment', 'Branch', 'Type', 'Created', '']}>
                {snapshots.map((snap: any) => (
                  <TableRow key={snap.id}>
                    <TableCell mono>{snap.id?.slice(0, 12)}…</TableCell>
                    <TableCell>{snap.environment_name || snap.environment_id?.slice(0, 8)}</TableCell>
                    <TableCell>
                      <Badge><GitBranch className="w-2.5 h-2.5 mr-1" />{snap.branch || '—'}</Badge>
                    </TableCell>
                    <TableCell>
                      <Badge variant={snap.trigger_type === 'manual' ? 'default' : 'secondary'}>{snap.trigger_type || 'auto'}</Badge>
                    </TableCell>
                    <TableCell>{snap.created_at ? timeAgo(snap.created_at) : '—'}</TableCell>
                    <TableCell>
                      <button className="text-muted-foreground hover:text-primary" aria-label="Restore snapshot">
                        <RotateCcw className="w-3.5 h-3.5" />
                      </button>
                    </TableCell>
                  </TableRow>
                ))}
              </Table>
            </Card>
          )}

          <div className="border-t border-border pt-4">
            <p className="text-[10px] text-muted-foreground flex items-center gap-1.5 mb-2"><Terminal className="w-3 h-3" /> CLI commands</p>
            <div className="grid sm:grid-cols-2 gap-2">
              <CodeBlock code="gc snapshot list --branch main" />
              <CodeBlock code="gc snapshot create --env <env-id>" />
            </div>
          </div>
        </div>
      )}

      {/* Modals */}
      <ClaudeConfigModal
        open={showClaude}
        onClose={() => setShowClaude(false)}
        onSaved={refetch}
        existing={status?.claude}
      />

      <ConnectRepoModal
        open={showConnect}
        onClose={() => setShowConnect(false)}
        onConnected={handleRepoConnected}
        githubConnected={githubConnected}
      />

      <ConfirmDialog
        open={!!disconnectRepoId}
        onClose={() => setDisconnectRepoId(null)}
        onConfirm={handleDisconnectRepo}
        title="Disconnect Repository"
        message="This will stop auto-fork for this repository. Existing contexts and snapshots are preserved."
        confirmLabel="Disconnect"
        destructive
        loading={disconnectingRepo}
      />
    </div>
  )
}
