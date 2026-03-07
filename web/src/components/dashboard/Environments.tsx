import { useState, useCallback } from 'react'
import { api } from '@/api/client'
import { useFetch, useMutation, usePolling, useAPIAuth } from '@/hooks/useAPI'
import { cn, timeAgo, SIZE_LABELS, REGION_LABELS } from '@/lib/utils'
import {
  Button, Card, Badge, StatusDot, Modal, Input, EmptyState,
  CopyButton, ConfirmDialog, Skeleton, useToast, CodeBlock, ProgressBar, Tooltip,
} from '@/components/ui'
import {
  Plus, Server, Clock, MapPin, Terminal, HeartPulse,
  Trash2, Activity, ChevronDown, ChevronRight, Cpu, HardDrive,
  ServerOff, Search, RefreshCw,
} from 'lucide-react'

/* ─── Create Wizard ─── */
function CreateWizard({ open, onClose, onCreated }: { open: boolean; onClose: () => void; onCreated: () => void }) {
  const [step, setStep] = useState(0)
  const [name, setName] = useState('')
  const [size, setSize] = useState('small')
  const [region, setRegion] = useState('nbg1')
  const [branch, setBranch] = useState('')
  const { toast } = useToast()
  const { mutate, loading, error } = useMutation(
    (token: string, orgId: string, body: any) => api.envs.create(token, orgId, body)
  )

  const handleCreate = async () => {
    const body: any = { name, size, region }
    if (branch) body.context_branch = branch
    const result = await mutate(body)
    if (result) {
      toast('success', `Environment "${name}" created`)
      setStep(0); setName(''); setSize('small'); setRegion('nbg1'); setBranch('')
      onCreated()
      onClose()
    }
  }

  const cliCmd = `gc env create --name ${name || 'my-env'} --size ${size} --region ${region}${branch ? ` --branch ${branch}` : ''}`

  return (
    <Modal open={open} onClose={onClose} title="Create Environment" description="Provision a new cloud dev environment" footer={
      <>
        {step > 0 && <Button variant="secondary" onClick={() => setStep(step - 1)}>Back</Button>}
        {step < 3 ? (
          <Button onClick={() => setStep(step + 1)} disabled={step === 0 && !name}>Next <ChevronRight className="w-3.5 h-3.5" /></Button>
        ) : (
          <Button onClick={handleCreate} loading={loading}>Create Environment</Button>
        )}
      </>
    }>
      {/* Progress */}
      <div className="flex gap-1.5 mb-6">
        {['Name', 'Size', 'Region', 'Review'].map((s, i) => (
          <div key={s} className="flex-1">
            <div className={cn('h-1 rounded-full', i <= step ? 'bg-primary' : 'bg-secondary')} />
            <p className={cn('text-[10px] mt-1', i <= step ? 'text-primary' : 'text-muted-foreground')}>{s}</p>
          </div>
        ))}
      </div>

      {step === 0 && (
        <div className="space-y-4">
          <Input label="Environment Name" placeholder="e.g. ml-training, api-dev" value={name} onChange={e => setName(e.target.value)} autoFocus />
          <Input label="Context Branch (optional)" placeholder="e.g. main, feature/auth" value={branch} onChange={e => setBranch(e.target.value)} />
        </div>
      )}

      {step === 1 && (
        <div className="grid grid-cols-2 gap-3">
          {Object.entries(SIZE_LABELS).map(([key, info]) => (
            <button
              key={key}
              onClick={() => setSize(key)}
              className={cn(
                'p-4 rounded-md border text-left transition-colors',
                size === key ? 'border-primary bg-primary/10' : 'border-border hover:border-muted-foreground/30',
              )}
            >
              <p className="text-sm font-medium text-foreground">{info.label}</p>
              <p className="text-[10px] text-muted-foreground mt-0.5">{info.specs}</p>
              <p className="text-xs text-primary mt-2">{info.rate}</p>
            </button>
          ))}
        </div>
      )}

      {step === 2 && (
        <div className="space-y-3">
          {Object.entries(REGION_LABELS).map(([key, info]) => (
            <button
              key={key}
              onClick={() => setRegion(key)}
              className={cn(
                'w-full flex items-center gap-3 p-3 rounded-md border text-left transition-colors',
                region === key ? 'border-primary bg-primary/10' : 'border-border hover:border-muted-foreground/30',
              )}
            >
              <span className="text-lg">{info.flag}</span>
              <div>
                <p className="text-sm font-medium text-foreground">{info.label}</p>
                <p className="text-[10px] text-muted-foreground font-mono">{key}</p>
              </div>
            </button>
          ))}
        </div>
      )}

      {step === 3 && (
        <div className="space-y-4">
          <div className="bg-background rounded-md border border-border p-4 space-y-2">
            <div className="flex justify-between text-sm"><span className="text-muted-foreground">Name</span><span className="text-foreground">{name}</span></div>
            <div className="flex justify-between text-sm"><span className="text-muted-foreground">Size</span><span className="text-foreground">{SIZE_LABELS[size]?.label} ({size})</span></div>
            <div className="flex justify-between text-sm"><span className="text-muted-foreground">Region</span><span className="text-foreground">{REGION_LABELS[region]?.label}</span></div>
            <div className="flex justify-between text-sm"><span className="text-muted-foreground">Rate</span><span className="text-primary">{SIZE_LABELS[size]?.rate}</span></div>
            {branch && <div className="flex justify-between text-sm"><span className="text-muted-foreground">Branch</span><span className="text-foreground font-mono text-xs">{branch}</span></div>}
          </div>
          {error && <p className="text-xs text-destructive">{error}</p>}
          <div className="pt-2 border-t border-border">
            <p className="text-[10px] text-muted-foreground flex items-center gap-1.5 mb-1"><Terminal className="w-3 h-3" /> CLI equivalent</p>
            <CodeBlock code={cliCmd} />
          </div>
        </div>
      )}
    </Modal>
  )
}

/* ─── Health Panel ─── */
function HealthPanel({ envId }: { envId: string }) {
  const { data, loading } = useFetch(
    useCallback((token: string, orgId: string) => api.envs.health(token, orgId, envId), [envId])
  )

  if (loading) return <div className="p-4"><Skeleton className="h-20 w-full" /></div>
  if (!data) return <p className="p-4 text-xs text-muted-foreground">Health data unavailable</p>

  return (
    <div className="grid grid-cols-3 gap-4 p-4 bg-background rounded-md">
      <div>
        <p className="text-[10px] text-muted-foreground flex items-center gap-1"><Cpu className="w-3 h-3" /> CPU</p>
        <p className="text-sm text-foreground font-mono">{data.cpu_percent?.toFixed(1) || '—'}%</p>
        <ProgressBar value={data.cpu_percent || 0} className="mt-1" />
      </div>
      <div>
        <p className="text-[10px] text-muted-foreground flex items-center gap-1"><Activity className="w-3 h-3" /> Memory</p>
        <p className="text-sm text-foreground font-mono">{data.memory_percent?.toFixed(1) || '—'}%</p>
        <ProgressBar value={data.memory_percent || 0} className="mt-1" />
      </div>
      <div>
        <p className="text-[10px] text-muted-foreground flex items-center gap-1"><HardDrive className="w-3 h-3" /> Disk</p>
        <p className="text-sm text-foreground font-mono">{data.disk_percent?.toFixed(1) || '—'}%</p>
        <ProgressBar value={data.disk_percent || 0} className="mt-1" />
      </div>
    </div>
  )
}

/* ─── Environment Card ─── */
function EnvCard({ env, onRefresh }: { env: any; onRefresh: () => void }) {
  const [expanded, setExpanded] = useState(false)
  const [showDestroy, setShowDestroy] = useState(false)
  const { mutate: destroyEnv, loading: destroying } = useMutation(
    (token: string, orgId: string, id: string) => api.envs.destroy(token, orgId, id)
  )
  const { toast } = useToast()

  const handleDestroy = async () => {
    const result = await destroyEnv(env.id)
    if (result !== null) {
      toast('success', `Environment "${env.name}" destroyed`)
      setShowDestroy(false)
      onRefresh()
    }
  }

  const sshCmd = env.provider_ref ? `ssh -i ~/.gradient/keys/gradient_ed25519 root@${env.provider_ref}` : `gc env ssh ${env.id}`
  const sizeInfo = SIZE_LABELS[env.size] || { label: env.size, specs: '', rate: '' }
  const regionInfo = REGION_LABELS[env.region] || { label: env.region, flag: '' }

  const statusLabel: Record<string, string> = {
    running: 'Running', creating: 'Creating…', migrating: 'Migrating…',
    stopped: 'Stopped', destroyed: 'Destroyed', error: 'Error',
  }

  return (
    <>
      <Card className="overflow-hidden">
        <div className="p-4">
          <div className="flex items-start justify-between mb-3">
            <div className="flex items-center gap-2.5">
              <StatusDot status={env.status} />
              <div>
                <h3 className="text-sm font-medium text-foreground">{env.name}</h3>
                <p className="text-[10px] text-muted-foreground font-mono">{env.id}</p>
              </div>
            </div>
            <Badge variant={env.status === 'running' ? 'success' : env.status === 'error' ? 'destructive' : 'secondary'}>
              {statusLabel[env.status] || env.status}
            </Badge>
          </div>

          <div className="flex flex-wrap gap-x-4 gap-y-1.5 text-xs text-muted-foreground mb-3">
            <span className="flex items-center gap-1"><Server className="w-3 h-3" />{sizeInfo.label}</span>
            <span className="flex items-center gap-1"><MapPin className="w-3 h-3" />{regionInfo.flag} {regionInfo.label}</span>
            {env.created_at && <span className="flex items-center gap-1"><Clock className="w-3 h-3" />{timeAgo(env.created_at)}</span>}
          </div>

          <div className="flex items-center gap-2">
            {env.status === 'running' && (
              <>
                <Tooltip text="Copy SSH command">
                  <CopyButton text={sshCmd} label="SSH" className="bg-secondary px-2 py-1 rounded-md" />
                </Tooltip>
                <button
                  onClick={() => setExpanded(!expanded)}
                  className="flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground bg-secondary px-2 py-1 rounded-md transition-colors"
                >
                  <HeartPulse className="w-3 h-3" />
                  Health
                  <ChevronDown className={cn('w-3 h-3 transition-transform', expanded && 'rotate-180')} />
                </button>
              </>
            )}
            <div className="flex-1" />
            {env.status !== 'destroyed' && (
              <button
                onClick={() => setShowDestroy(true)}
                className="flex items-center gap-1.5 text-xs text-muted-foreground hover:text-destructive transition-colors"
              >
                <Trash2 className="w-3 h-3" />
                Destroy
              </button>
            )}
          </div>
        </div>

        {expanded && env.status === 'running' && (
          <div className="border-t border-border">
            <HealthPanel envId={env.id} />
          </div>
        )}

        <div className="border-t border-border px-4 py-2 flex items-center gap-1.5 text-[10px] text-muted-foreground">
          <Terminal className="w-3 h-3" />
          <span>gc env status {env.id}</span>
          <CopyButton text={`gc env status ${env.id}`} label="" className="ml-auto" />
        </div>
      </Card>

      <ConfirmDialog
        open={showDestroy}
        onClose={() => setShowDestroy(false)}
        onConfirm={handleDestroy}
        title="Destroy Environment"
        message={`This will permanently destroy "${env.name}". A final snapshot will be taken. Billing will stop. This cannot be undone.`}
        confirmLabel="Destroy"
        destructive
        loading={destroying}
      />
    </>
  )
}

/* ─── Main Component ─── */
export default function Environments() {
  const [showCreate, setShowCreate] = useState(false)
  const [search, setSearch] = useState('')
  const { data: envs, loading, refetch } = useFetch(
    useCallback((token: string, orgId: string) => api.envs.list(token, orgId), [])
  )

  usePolling(refetch, 10000, !loading)

  const filtered = (envs || []).filter((e: any) =>
    e.name?.toLowerCase().includes(search.toLowerCase()) ||
    e.id?.toLowerCase().includes(search.toLowerCase())
  )

  return (
    <div className="space-y-6">
      <div className="flex flex-col sm:flex-row items-start sm:items-center gap-3 justify-between">
        <div className="relative flex-1 max-w-xs">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
          <input
            type="search"
            placeholder="Search environments…"
            value={search}
            onChange={e => setSearch(e.target.value)}
            className="w-full bg-card border border-input rounded-md pl-9 pr-3 py-2 text-sm text-foreground placeholder:text-muted-foreground outline-none focus:ring-1 focus:ring-ring"
            aria-label="Search environments"
          />
        </div>
        <div className="flex items-center gap-2">
          <Button variant="ghost" size="sm" onClick={refetch}><RefreshCw className="w-3.5 h-3.5" /></Button>
          <Button size="sm" onClick={() => setShowCreate(true)}><Plus className="w-3.5 h-3.5" /> Create Environment</Button>
        </div>
      </div>

      {loading && !envs ? (
        <div className="grid gap-4 sm:grid-cols-2">
          {[1,2,3].map(i => <Skeleton key={i} className="h-40 w-full" />)}
        </div>
      ) : filtered.length === 0 ? (
        <EmptyState
          icon={ServerOff}
          title={search ? 'No matching environments' : 'No environments yet'}
          description={search ? 'Try a different search term.' : 'Create your first cloud dev environment to get started.'}
          action={!search && <Button size="sm" onClick={() => setShowCreate(true)}><Plus className="w-3.5 h-3.5" /> Create Environment</Button>}
        />
      ) : (
        <div className="grid gap-4 sm:grid-cols-2">
          {filtered.map((env: any) => (
            <EnvCard key={env.id} env={env} onRefresh={refetch} />
          ))}
        </div>
      )}

      <CreateWizard open={showCreate} onClose={() => setShowCreate(false)} onCreated={refetch} />
    </div>
  )
}
