import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { api } from '@/api/client'
import { useFetch, useMutation } from '@/hooks/useAPI'
import { cn } from '@/lib/utils'
import {
  Badge, Button, Callout, Card, Input, Select, Skeleton, useToast,
} from '@/components/ui'
import {
  ArrowLeft, ArrowRight, CheckCircle2, ExternalLink, FolderGit2, Github, KeyRound,
  Link2, Plug, Rocket, Sparkles,
} from 'lucide-react'

const STEPS = [
  {
    id: 'repo',
    label: 'Connect repo',
    description: 'Choose the GitHub repository Gradient should remember and work in.',
    icon: FolderGit2,
    required: true,
  },
  {
    id: 'linear',
    label: 'Optional Linear',
    description: 'Connect Linear if you want issue-driven automation.',
    icon: Link2,
    required: false,
  },
  {
    id: 'claude',
    label: 'Add AI key',
    description: 'Add your Anthropic key so tasks can execute against your own account.',
    icon: KeyRound,
    required: true,
  },
  {
    id: 'launch',
    label: 'Start building',
    description: 'Open memory, create tasks, or let Linear drive the workflow.',
    icon: Rocket,
    required: false,
  },
]

export default function OnboardingWizard() {
  const navigate = useNavigate()
  const { toast } = useToast()

  const [step, setStep] = useState(0)
  const [repoName, setRepoName] = useState('')
  const [manualRepoInput, setManualRepoInput] = useState(false)
  const [apiKey, setAPIKey] = useState('')
  const [model, setModel] = useState('claude-sonnet-4-20250514')

  const { data: status, refetch: refetchStatus } = useFetch(
    useCallback((token: string, orgId: string) => api.integrations.status(token, orgId), [])
  )

  const githubConnected = !!status?.github?.connected
  const linearConnected = !!status?.linear?.connected
  const claudeConfigured = !!status?.claude?.configured
  const repoConnected = !!status?.repos?.connected

  const { data: availableData, loading: loadingRepos, refetch: refetchRepos } = useFetch(
    useCallback((token: string, orgId: string) => {
      if (!githubConnected) return Promise.resolve({ repos: [] })
      return api.repos.available(token, orgId)
    }, [githubConnected])
  )
  const availableRepos = availableData?.repos || []

  const completed = useMemo(() => {
    const done = new Set<string>()
    if (repoConnected) done.add('repo')
    if (linearConnected) done.add('linear')
    if (claudeConfigured) done.add('claude')
    return done
  }, [claudeConfigured, linearConnected, repoConnected])

  useEffect(() => {
    const nextStep = !repoConnected
      ? 0
      : !linearConnected && !claudeConfigured
        ? 1
        : !claudeConfigured
          ? 2
          : 3

    setStep((current) => (current === nextStep ? current : nextStep))
  }, [repoConnected, linearConnected, claudeConfigured])

  const { mutate: getLinearURL, loading: linearLoading, error: linearError } = useMutation(
    (token: string, orgId: string, _: null) => api.integrations.linear.authUrl(token, orgId)
  )
  const { mutate: exchangeLinearCode } = useMutation(
    (token: string, orgId: string, body: any) => api.integrations.linear.callback(token, orgId, body)
  )
  const { mutate: getGitHubURL, loading: githubLoading, error: githubError } = useMutation(
    (token: string, orgId: string, _: null) => api.integrations.github.authUrl(token, orgId)
  )
  const { mutate: exchangeGitHubCode } = useMutation(
    (token: string, orgId: string, body: any) => api.integrations.github.callback(token, orgId, body)
  )
  const { mutate: connectRepo, loading: repoLoading, error: repoError } = useMutation(
    (token: string, orgId: string, body: any) => api.repos.connect(token, orgId, body)
  )
  const { mutate: saveClaude, loading: claudeLoading, error: claudeError } = useMutation(
    (token: string, orgId: string, body: any) => api.integrations.claude.save(token, orgId, body)
  )

  useEffect(() => {
    const params = new URLSearchParams(window.location.search)
    const code = params.get('code')
    const state = params.get('state')
    if (!code) return

    window.history.replaceState({}, '', window.location.pathname)
    const provider = localStorage.getItem('oauth_provider')
    localStorage.removeItem('oauth_provider')

    if (provider === 'linear') {
      exchangeLinearCode({ code, state: state || '' }).then((result) => {
        if (result?.connected) {
          toast('success', `Linear connected to ${result.workspace_name || 'workspace'}`)
          refetchStatus()
        }
      })
      return
    }

    if (provider === 'github') {
      exchangeGitHubCode({ code, state: state || '' }).then((result) => {
        if (result?.connected) {
          toast('success', `GitHub connected as ${result.github_user}`)
          refetchStatus()
          refetchRepos()
        }
      })
    }
  }, [exchangeGitHubCode, exchangeLinearCode, refetchRepos, refetchStatus, toast])

  const handleConnectGitHub = async () => {
    const result = await getGitHubURL(null)
    if (!result?.url) {
      toast('error', 'Failed to start GitHub authorization')
      return
    }
    localStorage.setItem('oauth_provider', 'github')
    window.location.href = result.url
  }

  const handleConnectRepo = async () => {
    if (!repoName) return
    const result = await connectRepo({ repo: repoName })
    if (!result) return
    toast('success', 'Repository connected')
    refetchStatus()
    setStep(1)
  }

  const handleConnectLinear = async () => {
    const result = await getLinearURL(null)
    if (!result?.url) {
      toast('error', 'Failed to start Linear authorization')
      return
    }
    localStorage.setItem('oauth_provider', 'linear')
    window.location.href = result.url
  }

  const handleSaveClaude = async () => {
    const result = await saveClaude({
      api_key: apiKey,
      model,
      max_turns: 50,
    })
    if (!result) return
    toast('success', 'Anthropic key saved')
    setAPIKey('')
    refetchStatus()
    setStep(3)
  }

  const allRequiredDone = repoConnected && claudeConfigured
  const currentStep = STEPS[step]

  return (
    <div className="mx-auto max-w-4xl space-y-6">
      <Card className="overflow-hidden">
        <div className="border-b border-border bg-gradient-to-r from-primary/10 via-transparent to-transparent px-6 py-6">
          <div className="max-w-2xl">
            <Badge variant="secondary">Fast onboarding</Badge>
            <h2 className="mt-3 text-2xl font-semibold text-foreground">Connect a repo, optionally plug in Linear, then add your AI key</h2>
            <p className="mt-2 text-sm leading-relaxed text-muted-foreground">
              Gradient is easiest when the repo is the center of everything. Once that is connected, we can start building repo memory, trajectory analyses, and retrieval guidance immediately.
            </p>
          </div>
        </div>

        <div className="px-6 py-5">
          <div className="grid gap-3 md:grid-cols-4">
            {STEPS.map((item, index) => (
              <button
                key={item.id}
                type="button"
                onClick={() => setStep(index)}
                className={cn(
                  'rounded-xl border px-4 py-4 text-left transition-colors',
                  step === index ? 'border-primary bg-primary/5' : 'border-border hover:border-primary/20',
                )}
              >
                <div className="flex items-center justify-between gap-2">
                  <div className="flex h-8 w-8 items-center justify-center rounded-full bg-muted text-xs font-semibold text-foreground">
                    {completed.has(item.id) ? <CheckCircle2 className="h-4 w-4 text-emerald-400" /> : index + 1}
                  </div>
                  {!item.required && <Badge variant="outline">Optional</Badge>}
                </div>
                <div className="mt-3">
                  <p className="text-sm font-medium text-foreground">{item.label}</p>
                  <p className="mt-1 text-xs text-muted-foreground">{item.description}</p>
                </div>
              </button>
            ))}
          </div>
        </div>
      </Card>

      <Card className="p-6">
        <div className="mb-5 flex items-start gap-3">
          <div className="rounded-lg border border-primary/20 bg-primary/5 p-2 text-primary">
            <currentStep.icon className="h-4 w-4" />
          </div>
          <div>
            <p className="text-sm font-semibold text-foreground">{currentStep.label}</p>
            <p className="mt-1 text-sm text-muted-foreground">{currentStep.description}</p>
          </div>
        </div>

        {step === 0 && (
          <div className="space-y-5">
            {repoConnected ? (
              <div className="space-y-4">
                <div className="rounded-lg border border-emerald-500/20 bg-emerald-500/5 p-4">
                  <div className="flex items-center gap-2 text-sm font-medium text-foreground">
                    <CheckCircle2 className="h-4 w-4 text-emerald-400" />
                    {status?.repos?.count || 1} repo connection{status?.repos?.count === 1 ? '' : 's'} ready
                  </div>
                  <p className="mt-1 text-xs text-muted-foreground">
                    Repo memory, trajectory analysis, and MCP guidance are all organized per repository now.
                  </p>
                </div>
                <Button onClick={() => setStep(1)}>
                  Continue <ArrowRight className="h-3.5 w-3.5" />
                </Button>
              </div>
            ) : !githubConnected ? (
              <div className="space-y-4">
                <Callout variant="tip" title="GitHub comes first">
                  Connect GitHub, pick the repository, and Gradient will scope durable memory, branch context, sessions, and retrieval history to that repo from the start.
                </Callout>
                <Button onClick={handleConnectGitHub} loading={githubLoading}>
                  <Github className="h-3.5 w-3.5" />
                  Connect GitHub
                </Button>
                {githubError && <p className="text-xs text-destructive">{githubError}</p>}
              </div>
            ) : (
              <div className="space-y-4">
                <div className="rounded-lg border border-primary/20 bg-primary/5 p-4">
                  <p className="text-sm text-foreground">GitHub connected as <span className="font-medium">{status?.github?.github_user}</span></p>
                </div>

                {loadingRepos ? (
                  <Skeleton className="h-10 w-full" />
                ) : availableRepos.length > 0 && !manualRepoInput ? (
                  <>
                    <Select
                      label="Repository"
                      value={repoName}
                      onChange={(event) => setRepoName(event.target.value)}
                      options={availableRepos.map((repo: string) => ({ value: repo, label: repo }))}
                      placeholder="Select a repository..."
                    />
                    <button
                      type="button"
                      onClick={() => setManualRepoInput(true)}
                      className="text-xs text-muted-foreground underline transition-colors hover:text-foreground"
                    >
                      Enter a repository manually instead
                    </button>
                  </>
                ) : (
                  <Input
                    label="Repository"
                    placeholder="owner/repo-name"
                    value={repoName}
                    onChange={(event) => setRepoName(event.target.value)}
                    mono
                  />
                )}

                <Button onClick={handleConnectRepo} loading={repoLoading} disabled={!repoName}>
                  <FolderGit2 className="h-3.5 w-3.5" />
                  Connect repository
                </Button>
                {repoError && <p className="text-xs text-destructive">{repoError}</p>}
              </div>
            )}
          </div>
        )}

        {step === 1 && (
          <div className="space-y-5">
            <Callout variant="info" title="Linear is optional">
              Skip this if you want to create tasks manually. Connect it if you want Gradient to pick up labeled issues and turn them into PRs automatically.
            </Callout>

            {linearConnected ? (
              <div className="space-y-4">
                <div className="rounded-lg border border-emerald-500/20 bg-emerald-500/5 p-4">
                  <div className="flex items-center gap-2 text-sm font-medium text-foreground">
                    <CheckCircle2 className="h-4 w-4 text-emerald-400" />
                    Connected to {status?.linear?.workspace_name || 'Linear'}
                  </div>
                </div>
                <Button onClick={() => setStep(2)}>
                  Continue <ArrowRight className="h-3.5 w-3.5" />
                </Button>
              </div>
            ) : (
              <div className="space-y-4">
                <Button onClick={handleConnectLinear} loading={linearLoading}>
                  <Plug className="h-3.5 w-3.5" />
                  Connect Linear
                </Button>
                {linearError && <p className="text-xs text-destructive">{linearError}</p>}
                <Button variant="ghost" onClick={() => setStep(2)}>
                  Skip for now <ArrowRight className="h-3.5 w-3.5" />
                </Button>
              </div>
            )}
          </div>
        )}

        {step === 2 && (
          <div className="space-y-5">
            {claudeConfigured ? (
              <div className="space-y-4">
                <div className="rounded-lg border border-emerald-500/20 bg-emerald-500/5 p-4">
                  <div className="flex items-center gap-2 text-sm font-medium text-foreground">
                    <CheckCircle2 className="h-4 w-4 text-emerald-400" />
                    Anthropic key configured for {status?.claude?.model || 'Claude'}
                  </div>
                </div>
                <Button onClick={() => setStep(3)}>
                  Continue <ArrowRight className="h-3.5 w-3.5" />
                </Button>
              </div>
            ) : (
              <div className="space-y-4">
                <Callout variant="tip" title="Transparent AI billing">
                  Your provider tokens stay on your Anthropic account. Gradient's free trial covers platform credits for persistent memory, trajectory analysis, retrieval, and runtime usage, but task execution still uses your own API key.
                </Callout>

                <Input
                  label="Anthropic API key"
                  placeholder="sk-ant-..."
                  value={apiKey}
                  onChange={(event) => setAPIKey(event.target.value)}
                  mono
                  type="password"
                />
                <Input
                  label="Model"
                  placeholder="claude-sonnet-4-20250514"
                  value={model}
                  onChange={(event) => setModel(event.target.value)}
                  mono
                />
                <p className="text-xs text-muted-foreground">
                  Recommended starting model: <code className="text-foreground">claude-sonnet-4-20250514</code>. Get a key at{' '}
                  <a href="https://console.anthropic.com/" target="_blank" rel="noopener noreferrer" className="text-primary hover:underline">
                    console.anthropic.com
                  </a>.
                </p>

                <Button onClick={handleSaveClaude} loading={claudeLoading} disabled={!apiKey}>
                  <KeyRound className="h-3.5 w-3.5" />
                  Save Anthropic key
                </Button>
                {claudeError && <p className="text-xs text-destructive">{claudeError}</p>}
              </div>
            )}
          </div>
        )}

        {step === 3 && (
          <div className="space-y-6">
            {allRequiredDone ? (
              <>
                <div className="rounded-2xl border border-primary/20 bg-gradient-to-br from-primary/10 to-transparent p-5">
                  <div className="flex items-start gap-3">
                    <div className="rounded-full bg-primary/15 p-2 text-primary">
                      <Sparkles className="h-4 w-4" />
                    </div>
                    <div>
                      <h3 className="text-base font-semibold text-foreground">You are ready to build repo memory</h3>
                      <p className="mt-1 text-sm text-muted-foreground">
                        Gradient can now execute tasks, attribute trajectories, store durable tips, and surface repo-level memory on the new Memory page.
                      </p>
                    </div>
                  </div>
                </div>

                <div className="grid gap-4 md:grid-cols-3">
                  <Card className="p-4">
                    <p className="text-sm font-semibold text-foreground">1. Open Memory</p>
                    <p className="mt-2 text-xs leading-relaxed text-muted-foreground">
                      Inspect the tips, trajectories, retrieval runs, MCP tools, and live mesh activity for your connected repo.
                    </p>
                  </Card>
                  <Card className="p-4">
                    <p className="text-sm font-semibold text-foreground">2. Start a task</p>
                    <p className="mt-2 text-xs leading-relaxed text-muted-foreground">
                      Create a manual task now, or let Linear drive it if you connected a workspace.
                    </p>
                  </Card>
                  <Card className="p-4">
                    <p className="text-sm font-semibold text-foreground">3. Watch the memory improve</p>
                    <p className="mt-2 text-xs leading-relaxed text-muted-foreground">
                      Each clean success, recovery, and inefficient success becomes reusable guidance for the next run.
                    </p>
                  </Card>
                </div>

                <div className="flex flex-col gap-3 sm:flex-row">
                  <Link to="/dashboard/context" className="flex-1">
                    <Button className="w-full">
                      Open Memory workspace <ArrowRight className="h-3.5 w-3.5" />
                    </Button>
                  </Link>
                  <Button variant="outline" className="flex-1" onClick={() => navigate('/dashboard/tasks')}>
                    Go to Tasks <ArrowRight className="h-3.5 w-3.5" />
                  </Button>
                  {linearConnected && (
                    <a href="https://linear.app" target="_blank" rel="noopener noreferrer" className="flex-1">
                      <Button variant="ghost" className="w-full">
                        Open Linear <ExternalLink className="h-3.5 w-3.5" />
                      </Button>
                    </a>
                  )}
                </div>
              </>
            ) : (
              <div className="space-y-4">
                <p className="text-sm text-muted-foreground">
                  Finish the required repo and AI key steps first. Linear can wait until you want issue-driven automation.
                </p>
                <Button variant="outline" onClick={() => setStep(!repoConnected ? 0 : 2)}>
                  Go to next required step <ArrowRight className="h-3.5 w-3.5" />
                </Button>
              </div>
            )}
          </div>
        )}
      </Card>

      <div className="flex items-center justify-between">
        <Button variant="ghost" onClick={() => setStep(Math.max(0, step - 1))} disabled={step === 0}>
          <ArrowLeft className="h-3.5 w-3.5" />
          Back
        </Button>
        {step < STEPS.length - 1 && (
          <Button variant="ghost" onClick={() => setStep(step + 1)}>
            Next
            <ArrowRight className="h-3.5 w-3.5" />
          </Button>
        )}
      </div>
    </div>
  )
}
