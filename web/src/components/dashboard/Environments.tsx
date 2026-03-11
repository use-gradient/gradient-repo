import { useState, useCallback } from 'react'
import { api } from '@/api/client'
import { useFetch, usePolling } from '@/hooks/useAPI'
import { cn, timeAgo, SIZE_LABELS, REGION_LABELS, PROVIDER_LABELS } from '@/lib/utils'
import {
  Card, Badge, StatusDot, EmptyState,
  CopyButton, Skeleton, ProgressBar, Tooltip,
} from '@/components/ui'
import {
  Server, Clock, MapPin, Terminal, HeartPulse,
  Activity, ChevronDown, Cpu, HardDrive,
  ServerOff, Search, RefreshCw,
} from 'lucide-react'

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
function EnvCard({ env }: { env: any }) {
  const [expanded, setExpanded] = useState(false)

  const isAWS = env.provider === 'aws'
  const connectCmd = isAWS && env.cluster_name
    ? `aws ssm start-session --target ${env.cluster_name} --region ${env.region}`
    : env.ip_address
      ? `ssh -i ~/.gradient/keys/gradient_ed25519 root@${env.ip_address}`
      : `gc env ssh ${env.id}`
  const sizeInfo = SIZE_LABELS[env.size] || { label: env.size, specs: '', rate: '' }
  const regionInfo = REGION_LABELS[env.region] || { label: env.region, flag: '' }
  const providerInfo = PROVIDER_LABELS[env.provider] || { label: env.provider || 'Unknown', icon: '☁️' }

  const statusLabel: Record<string, string> = {
    running: 'Running', creating: 'Creating…', migrating: 'Migrating…',
    stopped: 'Stopped', destroyed: 'Destroyed', error: 'Error',
  }

  return (
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
          <span className="flex items-center gap-1">{providerInfo.icon} {providerInfo.label}</span>
          <span className="flex items-center gap-1"><Server className="w-3 h-3" />{sizeInfo.label}</span>
          <span className="flex items-center gap-1"><MapPin className="w-3 h-3" />{regionInfo.flag} {regionInfo.label}</span>
          {env.created_at && <span className="flex items-center gap-1"><Clock className="w-3 h-3" />{timeAgo(env.created_at)}</span>}
        </div>

        <div className="flex items-center gap-2">
          {env.status === 'running' && (
            <>
              <Tooltip text={isAWS ? 'Copy SSM connect command' : 'Copy SSH command'}>
                <CopyButton text={connectCmd} label={isAWS ? 'SSM' : 'SSH'} className="bg-secondary px-2 py-1 rounded-md" />
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
  )
}

/* ─── Main Component ─── */
export default function Environments() {
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
        <button onClick={refetch} className="p-2 text-muted-foreground hover:text-foreground transition-colors">
          <RefreshCw className="w-3.5 h-3.5" />
        </button>
      </div>

      {loading && !envs ? (
        <div className="grid gap-4 sm:grid-cols-2">
          {[1,2,3].map(i => <Skeleton key={i} className="h-40 w-full" />)}
        </div>
      ) : filtered.length === 0 ? (
        <EmptyState
          icon={ServerOff}
          title={search ? 'No matching environments' : 'No environments yet'}
          description={search ? 'Try a different search term.' : 'Environments are provisioned automatically when agent tasks run. Create a Linear issue to get started.'}
        />
      ) : (
        <div className="grid gap-4 sm:grid-cols-2">
          {filtered.map((env: any) => (
            <EnvCard key={env.id} env={env} />
          ))}
        </div>
      )}
    </div>
  )
}
