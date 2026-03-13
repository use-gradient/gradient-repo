import { useState, useCallback, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { api } from '@/api/client'
import { useFetch, useMutation } from '@/hooks/useAPI'
import { cn } from '@/lib/utils'
import {
  Button, Card, Input, Badge, useToast, Select, Skeleton,
} from '@/components/ui'
import {
  CheckCircle2, ArrowRight, ArrowLeft, Bot, Key, Plug,
  GitBranch, Rocket, Tag, ExternalLink, PartyPopper,
  Github,
} from 'lucide-react'

const STEPS = [
  { id: 'linear', label: 'Connect Linear', icon: Plug, description: 'Link your Linear workspace' },
  { id: 'claude', label: 'AI Model', icon: Bot, description: 'Choose your AI model' },
  { id: 'repo', label: 'Connect Repo', icon: GitBranch, description: 'Link a GitHub repository' },
  { id: 'launch', label: 'Start Building', icon: Tag, description: 'Label issues and watch the magic' },
]

export default function OnboardingWizard() {
  const [step, setStep] = useState(0)
  const [completed, setCompleted] = useState<Set<string>>(new Set())
  const navigate = useNavigate()
  const { toast } = useToast()

  const [apiKey, setApiKey] = useState('')
  const [model, setModel] = useState('claude-sonnet-4-20250514')
  const [useFreeTier, setUseFreeTier] = useState(false)

  const [repoName, setRepoName] = useState('')
  const [manualRepoInput, setManualRepoInput] = useState(false)

  const { data: status, refetch: refetchStatus } = useFetch(
    useCallback((token: string, orgId: string) => api.integrations.status(token, orgId), [])
  )

  const githubConnected = !!status?.github?.connected

  const { data: availableData, loading: loadingRepos, refetch: refetchRepos } = useFetch(
    useCallback((token: string, orgId: string) => {
      if (!githubConnected) return Promise.resolve({ repos: [] })
      return api.repos.available(token, orgId)
    }, [githubConnected])
  )
  const availableRepos = availableData?.repos || []

  useEffect(() => {
    if (!status) return
    const done = new Set<string>()
    if (status.linear?.connected) done.add('linear')
    if (status.claude?.configured) done.add('claude')
    if (status.repos?.connected) done.add('repo')
    setCompleted(done)

    const firstIncomplete = STEPS.findIndex(s => !done.has(s.id))
    if (firstIncomplete >= 0 && firstIncomplete !== step) setStep(firstIncomplete)
  }, [status])

  // OAuth mutations
  const { mutate: getLinearURL, loading: linearLoading, error: linearError } = useMutation(
    (token: string, orgId: string, _: any) => api.integrations.linear.authUrl(token, orgId)
  )
  const { mutate: exchangeLinearCode } = useMutation(
    (token: string, orgId: string, body: any) => api.integrations.linear.callback(token, orgId, body)
  )
  const { mutate: getGitHubURL, loading: githubLoading, error: githubError } = useMutation(
    (token: string, orgId: string, _: any) => api.integrations.github.authUrl(token, orgId)
  )
  const { mutate: exchangeGitHubCode } = useMutation(
    (token: string, orgId: string, body: any) => api.integrations.github.callback(token, orgId, body)
  )

  const { mutate: saveClaude, loading: claudeLoading, error: claudeError } = useMutation(
    (token: string, orgId: string, body: any) => api.integrations.claude.save(token, orgId, body)
  )
  const { mutate: connectRepo, loading: repoLoading, error: repoError } = useMutation(
    (token: string, orgId: string, body: any) => api.repos.connect(token, orgId, body)
  )

  // Handle OAuth callback when returning from Linear/GitHub
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
          refetchStatus()
        }
      })
    } else if (provider === 'github') {
      exchangeGitHubCode({ code, state: state || '' }).then(result => {
        if (result?.connected) {
          toast('success', `GitHub connected as ${result.github_user}`)
          refetchStatus()
          refetchRepos()
        }
      })
    } else {
      exchangeLinearCode({ code, state: state || '' }).then(result => {
        if (result?.connected) {
          toast('success', `Linear connected to ${result.workspace_name || 'workspace'}`)
          refetchStatus()
        } else {
          exchangeGitHubCode({ code, state: state || '' }).then(ghResult => {
            if (ghResult?.connected) {
              toast('success', `GitHub connected as ${ghResult.github_user}`)
              refetchStatus()
              refetchRepos()
            }
          })
        }
      })
    }
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  const handleConnectLinear = async () => {
    const result = await getLinearURL(null)
    if (result?.url) {
      localStorage.setItem('oauth_provider', 'linear')
      window.location.href = result.url
    } else {
      toast('error', 'Failed to get Linear authorization URL. Please try again.')
    }
  }

  const handleConnectGitHub = async () => {
    const result = await getGitHubURL(null)
    if (result?.url) {
      localStorage.setItem('oauth_provider', 'github')
      window.location.href = result.url
    } else {
      toast('error', 'Failed to get GitHub authorization URL. Please try again.')
    }
  }

  const handleSaveClaude = async () => {
    const body = useFreeTier
      ? { use_free_tier: true, model: 'qwen3.5-122b' }
      : { api_key: apiKey, model, max_turns: 50 }
    const result = await saveClaude(body)
    if (result) {
      setCompleted(prev => new Set([...prev, 'claude']))
      toast('success', useFreeTier ? 'Free tier model activated!' : 'Claude Code configured!')
      setStep(2)
      refetchStatus()
    }
  }

  const handleConnectRepo = async () => {
    const result = await connectRepo({ repo: repoName })
    if (result) {
      setCompleted(prev => new Set([...prev, 'repo']))
      toast('success', 'Repository connected!')
      setStep(3)
      refetchStatus()
    }
  }

  const allDone = completed.has('linear') && completed.has('claude') && completed.has('repo')
  const currentStep = STEPS[step]

  return (
    <div className="max-w-2xl mx-auto space-y-6">
      {/* Header */}
      <div className="text-center">
        <div className="flex justify-center mb-3">
          <Rocket className="w-8 h-8 text-primary" />
        </div>
        <h2 className="text-lg font-semibold text-foreground">Get Started with Gradient</h2>
        <p className="text-sm text-muted-foreground mt-1">Connect your tools, then start labeling issues — the agent handles the rest</p>
      </div>

      {/* Progress */}
      <div className="flex items-center justify-center gap-2">
        {STEPS.map((s, i) => (
          <div key={s.id} className="flex items-center gap-2">
            <button
              onClick={() => setStep(i)}
              className={cn(
                'w-8 h-8 rounded-full flex items-center justify-center text-xs font-bold transition-all',
                completed.has(s.id)
                  ? 'bg-emerald-500 text-white'
                  : i === step
                    ? 'bg-primary text-primary-foreground ring-2 ring-primary/30'
                    : 'bg-muted text-muted-foreground',
              )}
            >
              {completed.has(s.id) ? <CheckCircle2 className="w-4 h-4" /> : i + 1}
            </button>
            {i < STEPS.length - 1 && (
              <div className={cn('w-12 h-0.5', completed.has(s.id) ? 'bg-emerald-500' : 'bg-border')} />
            )}
          </div>
        ))}
      </div>

      {/* Step content */}
      <Card className="p-6">
        <div className="flex items-center gap-3 mb-4">
          <currentStep.icon className="w-5 h-5 text-primary" />
          <div>
            <p className="text-sm font-medium text-foreground">{currentStep.label}</p>
            <p className="text-xs text-muted-foreground">{currentStep.description}</p>
          </div>
          {completed.has(currentStep.id) && (
            <Badge className="ml-auto bg-emerald-500/10 text-emerald-400">
              <CheckCircle2 className="w-3 h-3 mr-1" /> Done
            </Badge>
          )}
        </div>

        {/* Step 1: Linear */}
        {step === 0 && (
          <div className="space-y-4">
            {status?.linear?.connected ? (
              <>
                <div className="flex items-center gap-2 p-3 rounded-md bg-emerald-500/5 border border-emerald-500/20">
                  <CheckCircle2 className="w-4 h-4 text-emerald-400" />
                  <span className="text-sm text-foreground">Connected to {status.linear.workspace_name}</span>
                </div>
                <Button variant="outline" size="sm" onClick={() => setStep(1)}>
                  Continue <ArrowRight className="w-3.5 h-3.5" />
                </Button>
              </>
            ) : (
              <>
                <p className="text-sm text-muted-foreground">
                  Connect your Linear workspace so Gradient can watch for issues labeled <code className="text-primary font-semibold">gradient-agent</code>.
                  You'll be redirected to Linear to authorize access.
                </p>
                <Button onClick={handleConnectLinear} loading={linearLoading}>
                  <Plug className="w-3.5 h-3.5" /> Connect Linear
                </Button>
                {linearError && <p className="text-xs text-destructive">{linearError}</p>}
              </>
            )}
          </div>
        )}

        {/* Step 2: AI Model */}
        {step === 1 && (
          <div className="space-y-4">
            {status?.claude?.configured ? (
              <>
                <div className="flex items-center gap-2 p-3 rounded-md bg-emerald-500/5 border border-emerald-500/20">
                  <CheckCircle2 className="w-4 h-4 text-emerald-400" />
                  <span className="text-sm text-foreground">AI model configured ({status.claude.model})</span>
                </div>
                <Button variant="outline" size="sm" onClick={() => setStep(2)}>
                  Continue <ArrowRight className="w-3.5 h-3.5" />
                </Button>
              </>
            ) : (
              <>
                <p className="text-sm text-muted-foreground">
                  Choose the AI model that powers the agent. Use our free tier to get started instantly, or bring your own Claude API key for premium performance.
                </p>

                {/* Free tier option */}
                <button
                  onClick={() => setUseFreeTier(true)}
                  className={cn(
                    'w-full text-left rounded-lg border p-4 transition-all',
                    useFreeTier
                      ? 'border-primary bg-primary/5 ring-1 ring-primary/30'
                      : 'border-border hover:border-primary/30',
                  )}
                >
                  <div className="flex items-start gap-3">
                    <div className={cn(
                      'mt-0.5 h-4 w-4 rounded-full border-2 flex items-center justify-center shrink-0',
                      useFreeTier ? 'border-primary' : 'border-muted-foreground/40',
                    )}>
                      {useFreeTier && <div className="h-2 w-2 rounded-full bg-primary" />}
                    </div>
                    <div>
                      <div className="flex items-center gap-2">
                        <p className="text-sm font-medium text-foreground">Free tier</p>
                        <Badge variant="success" className="text-[10px]">No API key needed</Badge>
                      </div>
                      <p className="text-xs text-muted-foreground mt-1">
                        Powered by Qwen 3.5 122B — a capable open-source model. Great for getting started and smaller tasks.
                      </p>
                    </div>
                  </div>
                </button>

                {/* BYOK option */}
                <button
                  onClick={() => setUseFreeTier(false)}
                  className={cn(
                    'w-full text-left rounded-lg border p-4 transition-all',
                    !useFreeTier
                      ? 'border-primary bg-primary/5 ring-1 ring-primary/30'
                      : 'border-border hover:border-primary/30',
                  )}
                >
                  <div className="flex items-start gap-3">
                    <div className={cn(
                      'mt-0.5 h-4 w-4 rounded-full border-2 flex items-center justify-center shrink-0',
                      !useFreeTier ? 'border-primary' : 'border-muted-foreground/40',
                    )}>
                      {!useFreeTier && <div className="h-2 w-2 rounded-full bg-primary" />}
                    </div>
                    <div>
                      <p className="text-sm font-medium text-foreground">Bring your own key</p>
                      <p className="text-xs text-muted-foreground mt-1">
                        Use your Anthropic API key with Claude Sonnet for the best results on complex tasks.
                      </p>
                    </div>
                  </div>
                </button>

                {!useFreeTier && (
                  <div className="space-y-3 pl-7">
                    <Input
                      label="Anthropic API Key"
                      placeholder="sk-ant-..."
                      value={apiKey}
                      onChange={e => setApiKey(e.target.value)}
                      mono
                      type="password"
                    />
                    <Input
                      label="Model (optional)"
                      placeholder="claude-sonnet-4-20250514"
                      value={model}
                      onChange={e => setModel(e.target.value)}
                      mono
                    />
                    <p className="text-[10px] text-muted-foreground">
                      Get a key at{' '}
                      <a href="https://console.anthropic.com/" target="_blank" rel="noopener noreferrer" className="text-primary hover:underline">
                        console.anthropic.com
                      </a>
                    </p>
                  </div>
                )}

                <Button
                  onClick={handleSaveClaude}
                  loading={claudeLoading}
                  disabled={!useFreeTier && !apiKey}
                >
                  <Key className="w-3.5 h-3.5" /> {useFreeTier ? 'Activate Free Tier' : 'Save Configuration'}
                </Button>
                {claudeError && <p className="text-xs text-destructive">{claudeError}</p>}
              </>
            )}
          </div>
        )}

        {/* Step 3: Connect repo */}
        {step === 2 && (
          <div className="space-y-4">
            {status?.repos?.connected ? (
              <>
                <div className="flex items-center gap-2 p-3 rounded-md bg-emerald-500/5 border border-emerald-500/20">
                  <CheckCircle2 className="w-4 h-4 text-emerald-400" />
                  <span className="text-sm text-foreground">{status.repos.count} repo(s) connected</span>
                </div>
                <Button variant="outline" size="sm" onClick={() => setStep(3)}>
                  Continue <ArrowRight className="w-3.5 h-3.5" />
                </Button>
              </>
            ) : !githubConnected ? (
              <>
                <p className="text-sm text-muted-foreground">
                  First, connect your GitHub account. You'll be redirected to GitHub to authorize access.
                  Then you can select which repository the agent should work in.
                </p>
                <Button onClick={handleConnectGitHub} loading={githubLoading}>
                  <Github className="w-3.5 h-3.5" /> Connect GitHub
                </Button>
                {githubError && <p className="text-xs text-destructive">{githubError}</p>}
              </>
            ) : (
              <>
                <div className="flex items-center gap-2 p-3 rounded-md bg-primary/5 border border-primary/20 mb-2">
                  <Github className="w-4 h-4 text-primary" />
                  <span className="text-sm text-foreground">GitHub connected as {status?.github?.github_user}</span>
                </div>
                <p className="text-sm text-muted-foreground">
                  Select a repository for the agent to work in. It will create branches and open PRs here.
                </p>

                {loadingRepos ? (
                  <Skeleton className="h-10 w-full" />
                ) : availableRepos.length > 0 && !manualRepoInput ? (
                  <>
                    <Select
                      label="Repository"
                      placeholder="Select a repository..."
                      value={repoName}
                      onChange={e => setRepoName(e.target.value)}
                      options={availableRepos.map((repo: string) => ({ value: repo, label: repo }))}
                    />
                    <button
                      type="button"
                      onClick={() => setManualRepoInput(true)}
                      className="text-xs text-muted-foreground hover:text-foreground underline"
                    >
                      Or enter repository manually
                    </button>
                  </>
                ) : (
                  <Input
                    label="Repository"
                    placeholder="owner/repo-name"
                    value={repoName}
                    onChange={e => setRepoName(e.target.value)}
                    mono
                  />
                )}

                <Button onClick={handleConnectRepo} loading={repoLoading} disabled={!repoName}>
                  <GitBranch className="w-3.5 h-3.5" /> Connect Repo
                </Button>
                {repoError && <p className="text-xs text-destructive">{repoError}</p>}
              </>
            )}
            <Button variant="ghost" size="sm" onClick={() => { setCompleted(prev => new Set([...prev, 'repo'])); setStep(3) }}>
              Skip for now <ArrowRight className="w-3 h-3" />
            </Button>
          </div>
        )}

        {/* Step 4: Start Building */}
        {step === 3 && (
          <div className="space-y-5">
            {allDone ? (
              <>
                <div className="text-center py-4">
                  <PartyPopper className="w-10 h-10 text-primary mx-auto mb-3" />
                  <h3 className="text-base font-semibold text-foreground mb-1">You're all set!</h3>
                  <p className="text-sm text-muted-foreground">
                    Everything is connected. Here's what to do next:
                  </p>
                </div>

                <div className="rounded-lg border border-border bg-muted/30 p-5 space-y-4">
                  <div className="flex gap-3">
                    <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-primary/10 text-primary text-xs font-bold">1</div>
                    <div>
                      <p className="text-sm font-medium text-foreground">Go to Linear</p>
                      <p className="text-xs text-muted-foreground mt-0.5">
                        Open your Linear workspace and create an issue (or pick an existing one).
                      </p>
                    </div>
                  </div>
                  <div className="flex gap-3">
                    <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-primary/10 text-primary text-xs font-bold">2</div>
                    <div>
                      <p className="text-sm font-medium text-foreground">
                        Add the <code className="px-1.5 py-0.5 rounded bg-primary/10 text-primary text-xs font-semibold">gradient-agent</code> label
                      </p>
                      <p className="text-xs text-muted-foreground mt-0.5">
                        Create a label called "gradient-agent" in Linear if you haven't already, then apply it to any issue you want the AI to work on.
                      </p>
                    </div>
                  </div>
                  <div className="flex gap-3">
                    <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-primary/10 text-primary text-xs font-bold">3</div>
                    <div>
                      <p className="text-sm font-medium text-foreground">Watch the agent work</p>
                      <p className="text-xs text-muted-foreground mt-0.5">
                        Come back to the <strong>Tasks</strong> tab to monitor progress. The agent will create a branch, write code, and open a PR automatically.
                      </p>
                    </div>
                  </div>
                </div>

                <div className="flex flex-col sm:flex-row gap-3">
                  <a href="https://linear.app" target="_blank" rel="noopener noreferrer" className="flex-1">
                    <Button className="w-full gap-2">
                      Open Linear <ExternalLink className="w-3.5 h-3.5" />
                    </Button>
                  </a>
                  <Button variant="outline" className="flex-1 gap-2" onClick={() => navigate('/dashboard/tasks')}>
                    Go to Tasks <ArrowRight className="w-3.5 h-3.5" />
                  </Button>
                </div>
              </>
            ) : (
              <div className="text-center py-6">
                <p className="text-sm text-muted-foreground mb-4">
                  Complete the previous steps first to unlock this page.
                </p>
                <Button variant="outline" size="sm" onClick={() => {
                  const firstIncomplete = STEPS.findIndex(s => !completed.has(s.id))
                  if (firstIncomplete >= 0) setStep(firstIncomplete)
                }}>
                  Go to first incomplete step <ArrowRight className="w-3 h-3" />
                </Button>
              </div>
            )}
          </div>
        )}
      </Card>

      {/* Navigation */}
      <div className="flex items-center justify-between">
        <Button
          variant="ghost"
          size="sm"
          onClick={() => setStep(Math.max(0, step - 1))}
          disabled={step === 0}
        >
          <ArrowLeft className="w-3.5 h-3.5" /> Back
        </Button>
        {step < STEPS.length - 1 && (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => setStep(step + 1)}
          >
            Next <ArrowRight className="w-3.5 h-3.5" />
          </Button>
        )}
      </div>
    </div>
  )
}
