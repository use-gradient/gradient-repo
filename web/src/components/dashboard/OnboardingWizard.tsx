import { useState, useCallback, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { api } from '@/api/client'
import { useFetch, useMutation, useAPIAuth } from '@/hooks/useAPI'
import { cn } from '@/lib/utils'
import {
  Button, Card, Input, Modal, Badge, useToast, CodeBlock,
} from '@/components/ui'
import {
  CheckCircle2, ArrowRight, ArrowLeft, Bot, Key, Plug,
  GitBranch, Zap, Play, Rocket,
  Terminal,
} from 'lucide-react'

const STEPS = [
  { id: 'linear', label: 'Connect Linear', icon: Plug, description: 'Link your Linear workspace' },
  { id: 'claude', label: 'Claude Code', icon: Bot, description: 'Add your Anthropic API key' },
  { id: 'repo', label: 'Connect Repo', icon: GitBranch, description: 'Link a GitHub repository' },
  { id: 'task', label: 'Create Task', icon: Zap, description: 'Run your first AI task' },
]

export default function OnboardingWizard() {
  const [step, setStep] = useState(0)
  const [completed, setCompleted] = useState<Set<string>>(new Set())
  const navigate = useNavigate()
  const { toast } = useToast()

  // Claude config
  const [apiKey, setApiKey] = useState('')
  const [model, setModel] = useState('claude-sonnet-4-20250514')

  // Repo
  const [repoName, setRepoName] = useState('')

  // Task
  const [taskTitle, setTaskTitle] = useState('')
  const [taskDesc, setTaskDesc] = useState('')
  const [taskBranch, setTaskBranch] = useState('')

  // Integration status
  const { data: status, refetch: refetchStatus } = useFetch(
    useCallback((token: string, orgId: string) => api.integrations.status(token, orgId), [])
  )

  // Auto-advance based on existing status
  useEffect(() => {
    if (!status) return
    const done = new Set<string>()
    if (status.linear?.connected) done.add('linear')
    if (status.claude?.configured) done.add('claude')
    if (status.repos?.connected) done.add('repo')
    setCompleted(done)

    // Skip to first incomplete step
    const firstIncomplete = STEPS.findIndex(s => !done.has(s.id))
    if (firstIncomplete >= 0) setStep(firstIncomplete)
  }, [status])

  // Mutations
  const { mutate: getLinearURL, loading: linearLoading } = useMutation(
    (token: string, orgId: string, _: any) => api.integrations.linear.authUrl(token, orgId)
  )
  const { mutate: saveClaude, loading: claudeLoading, error: claudeError } = useMutation(
    (token: string, orgId: string, body: any) => api.integrations.claude.save(token, orgId, body)
  )
  const { mutate: connectRepo, loading: repoLoading, error: repoError } = useMutation(
    (token: string, orgId: string, body: any) => api.repos.connect(token, orgId, body)
  )
  const { mutate: createTask, loading: taskLoading, error: taskError } = useMutation(
    (token: string, orgId: string, body: any) => api.tasks.create(token, orgId, body)
  )

  const handleConnectLinear = async () => {
    const result = await getLinearURL(null)
    if (result?.url) {
      window.open(result.url, '_blank')
      toast('info', 'Complete OAuth in the new tab, then click "I\'ve connected" below.')
    }
  }

  const handleLinearDone = () => {
    setCompleted(prev => new Set([...prev, 'linear']))
    refetchStatus()
    setStep(1)
  }

  const handleSaveClaude = async () => {
    const result = await saveClaude({ api_key: apiKey, model, max_turns: 50 })
    if (result) {
      setCompleted(prev => new Set([...prev, 'claude']))
      toast('success', 'Claude Code configured!')
      setStep(2)
      refetchStatus()
    }
  }

  const handleConnectRepo = async () => {
    const result = await connectRepo({ repo_full_name: repoName })
    if (result) {
      setCompleted(prev => new Set([...prev, 'repo']))
      toast('success', 'Repository connected!')
      setStep(3)
      refetchStatus()
    }
  }

  const handleCreateTask = async () => {
    const result = await createTask({
      title: taskTitle,
      description: taskDesc,
      branch: taskBranch || undefined,
    })
    if (result) {
      toast('success', 'Task created! Redirecting to tasks...')
      setCompleted(prev => new Set([...prev, 'task']))
      setTimeout(() => navigate('/dashboard/tasks'), 1000)
    }
  }

  const allDone = completed.size === 4
  const currentStep = STEPS[step]

  return (
    <div className="max-w-2xl mx-auto space-y-6">
      {/* Header */}
      <div className="text-center">
        <div className="flex justify-center mb-3">
          <Rocket className="w-8 h-8 text-primary" />
        </div>
        <h2 className="text-lg font-semibold text-foreground">Get Started with Agent Tasks</h2>
        <p className="text-sm text-muted-foreground mt-1">Connect your tools, then create your first AI-powered task</p>
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
              <div className="flex items-center gap-2 p-3 rounded-md bg-emerald-500/5 border border-emerald-500/20">
                <CheckCircle2 className="w-4 h-4 text-emerald-400" />
                <span className="text-sm text-foreground">Connected to {status.linear.workspace_name}</span>
              </div>
            ) : (
              <>
                <p className="text-sm text-muted-foreground">
                  Connect your Linear workspace to automatically pick up issues labeled <code className="text-primary">gradient-agent</code>.
                </p>
                <Button onClick={handleConnectLinear} loading={linearLoading}>
                  <Plug className="w-3.5 h-3.5" /> Connect Linear
                </Button>
                <Button variant="outline" onClick={handleLinearDone} className="ml-2">
                  I've connected <ArrowRight className="w-3.5 h-3.5" />
                </Button>
                <p className="text-[10px] text-muted-foreground">
                  Or skip — you can create tasks manually without Linear.
                </p>
              </>
            )}
          </div>
        )}

        {/* Step 2: Claude Code */}
        {step === 1 && (
          <div className="space-y-4">
            {status?.claude?.configured ? (
              <div className="flex items-center gap-2 p-3 rounded-md bg-emerald-500/5 border border-emerald-500/20">
                <CheckCircle2 className="w-4 h-4 text-emerald-400" />
                <span className="text-sm text-foreground">Claude Code configured ({status.claude.model})</span>
              </div>
            ) : (
              <>
                <p className="text-sm text-muted-foreground">
                  Add your Anthropic API key. This powers the AI agent that works on your tasks.
                </p>
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
                <Button onClick={handleSaveClaude} loading={claudeLoading} disabled={!apiKey}>
                  <Key className="w-3.5 h-3.5" /> Save Configuration
                </Button>
                {claudeError && <p className="text-xs text-destructive">{claudeError}</p>}
                <p className="text-[10px] text-muted-foreground">
                  Get a key at{' '}
                  <a href="https://console.anthropic.com/" target="_blank" rel="noopener noreferrer" className="text-primary hover:underline">
                    console.anthropic.com
                  </a>
                </p>
              </>
            )}
          </div>
        )}

        {/* Step 3: Connect repo */}
        {step === 2 && (
          <div className="space-y-4">
            {status?.repos?.connected ? (
              <div className="flex items-center gap-2 p-3 rounded-md bg-emerald-500/5 border border-emerald-500/20">
                <CheckCircle2 className="w-4 h-4 text-emerald-400" />
                <span className="text-sm text-foreground">{status.repos.count} repo(s) connected</span>
              </div>
            ) : (
              <>
                <p className="text-sm text-muted-foreground">
                  Connect a GitHub repository. The agent will clone, branch, and create PRs here.
                </p>
                <Input
                  label="Repository"
                  placeholder="owner/repo-name"
                  value={repoName}
                  onChange={e => setRepoName(e.target.value)}
                  mono
                />
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

        {/* Step 4: Create first task */}
        {step === 3 && (
          <div className="space-y-4">
            <p className="text-sm text-muted-foreground">
              Create your first task. Describe what you want the AI agent to build.
            </p>
            <Input
              label="Task Title"
              placeholder="e.g. Add dark mode toggle to settings page"
              value={taskTitle}
              onChange={e => setTaskTitle(e.target.value)}
              autoFocus
            />
            <div>
              <label className="text-sm font-medium text-foreground mb-1.5 block">Description (optional)</label>
              <textarea
                value={taskDesc}
                onChange={e => setTaskDesc(e.target.value)}
                placeholder="Detailed instructions, links..."
                className="w-full bg-card border border-input rounded-md px-3 py-2 text-sm text-foreground placeholder:text-muted-foreground outline-none focus:ring-1 focus:ring-ring min-h-[80px] resize-y"
              />
            </div>
            <Input
              label="Branch (optional)"
              placeholder="feature/dark-mode"
              value={taskBranch}
              onChange={e => setTaskBranch(e.target.value)}
              mono
            />
            <Button onClick={handleCreateTask} loading={taskLoading} disabled={!taskTitle}>
              <Play className="w-3.5 h-3.5" /> Create & Start Task
            </Button>
            {taskError && <p className="text-xs text-destructive">{taskError}</p>}
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

      {/* CLI alternative */}
      <Card className="p-4">
        <p className="text-[10px] text-muted-foreground flex items-center gap-1.5 mb-2">
          <Terminal className="w-3 h-3" /> Or use the CLI
        </p>
        <div className="space-y-2">
          <CodeBlock code={`# Install & auth
curl -fsSL https://raw.githubusercontent.com/use-gradient/gradient-repo/main/scripts/install.sh | sh
gc auth login

# Connect Linear (in Linear settings, install the Gradient app)
# Then configure Claude Code
gc integration claude --api-key sk-ant-...

# Create a task
gc task create --title "Add dark mode" --branch feature/dark-mode

# Monitor
gc task list
gc task logs <task-id>`} />
        </div>
      </Card>
    </div>
  )
}
