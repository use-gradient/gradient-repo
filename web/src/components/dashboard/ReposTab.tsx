import { useState, useCallback } from 'react'
import { api } from '@/api/client'
import { useFetch, useMutation } from '@/hooks/useAPI'
import { cn, timeAgo, formatDate } from '@/lib/utils'
import {
  Button, Card, Badge, EmptyState, Modal, Input, ConfirmDialog,
  Table, TableRow, TableCell, Skeleton, useToast, CodeBlock, Tabs,
} from '@/components/ui'
import {
  GitBranch, Plus, Github, Link, Unlink, Camera, Archive,
  Trash2, Terminal, Clock, RefreshCw, Search, ExternalLink, RotateCcw,
} from 'lucide-react'

/* ─── Connect Repo Modal ─── */
function ConnectRepoModal({ open, onClose, onConnected }: { open: boolean; onClose: () => void; onConnected: () => void }) {
  const [repoName, setRepoName] = useState('')
  const { toast } = useToast()
  const { mutate, loading, error } = useMutation(
    (token: string, orgId: string, body: any) => api.repos.connect(token, orgId, body)
  )

  const handleConnect = async () => {
    const result = await mutate({ repo_full_name: repoName })
    if (result) {
      toast('success', `Repository "${repoName}" connected`)
      setRepoName('')
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
        <Input
          label="Repository"
          placeholder="owner/repo-name"
          value={repoName}
          onChange={e => setRepoName(e.target.value)}
          autoFocus
          mono
        />
        <p className="text-xs text-muted-foreground">
          The Gradient GitHub App must be installed on this repository. When a new branch is created,
          Gradient automatically copies the parent branch&#39;s context + snapshot pointers.
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

/* ─── Main Component ─── */
export default function ReposTab() {
  const [activeTab, setActiveTab] = useState('repos')
  const [showConnect, setShowConnect] = useState(false)
  const [disconnectId, setDisconnectId] = useState<string | null>(null)
  const [snapshotBranch, setSnapshotBranch] = useState('')
  const { toast } = useToast()

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

  const { mutate: disconnectRepo, loading: disconnecting } = useMutation(
    (token: string, orgId: string, id: string) => api.repos.disconnect(token, orgId, id)
  )

  const handleDisconnect = async () => {
    if (!disconnectId) return
    const result = await disconnectRepo(disconnectId)
    if (result !== null) {
      toast('success', 'Repository disconnected')
      setDisconnectId(null)
      refetchRepos()
    }
  }

  return (
    <div className="space-y-6">
      <Tabs
        tabs={[
          { id: 'repos', label: 'Repositories', icon: <Github className="w-3.5 h-3.5" /> },
          { id: 'snapshots', label: 'Snapshots', icon: <Camera className="w-3.5 h-3.5" /> },
        ]}
        active={activeTab}
        onChange={setActiveTab}
      />

      {activeTab === 'repos' && (
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
              description="Connect a GitHub repository to enable auto-fork context. When a branch is created, its parent&#39;s context is automatically copied."
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
                      onClick={() => setDisconnectId(repo.id)}
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
              <div className="flex gap-3">
                <span className="text-primary font-mono text-[10px] mt-0.5">01</span>
                <p>Install the Gradient GitHub App on your repository</p>
              </div>
              <div className="flex gap-3">
                <span className="text-primary font-mono text-[10px] mt-0.5">02</span>
                <p>Connect it here with <code className="text-foreground font-mono bg-secondary px-1 rounded">gc repo connect</code></p>
              </div>
              <div className="flex gap-3">
                <span className="text-primary font-mono text-[10px] mt-0.5">03</span>
                <p>Create a branch: <code className="text-foreground font-mono bg-secondary px-1 rounded">git checkout -b feature/new</code></p>
              </div>
              <div className="flex gap-3">
                <span className="text-primary font-mono text-[10px] mt-0.5">04</span>
                <p>Gradient copies main&#39;s context + snapshots to the new branch automatically</p>
              </div>
            </div>
          </Card>
        </div>
      )}

      {activeTab === 'snapshots' && (
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

      <ConnectRepoModal open={showConnect} onClose={() => setShowConnect(false)} onConnected={refetchRepos} />
      <ConfirmDialog
        open={!!disconnectId}
        onClose={() => setDisconnectId(null)}
        onConfirm={handleDisconnect}
        title="Disconnect Repository"
        message="This will stop auto-fork for this repository. Existing contexts and snapshots are preserved."
        confirmLabel="Disconnect"
        destructive
        loading={disconnecting}
      />
    </div>
  )
}
