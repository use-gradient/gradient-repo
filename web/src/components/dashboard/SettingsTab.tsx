import { useState, useCallback } from 'react'
import { useOrganization, useOrganizationList, useUser, useClerk, CreateOrganization } from '@clerk/clerk-react'
import { cn } from '@/lib/utils'
import {
  Button, Card, Badge, Input, Modal, CopyButton, Tabs, EmptyState, Select,
  Table, TableRow, TableCell, useToast, CodeBlock, Callout, ConfirmDialog,
} from '@/components/ui'
import {
  Building2, Users, UserPlus, Crown, Shield, Mail, Send, Copy, Plus,
  Settings, Terminal, Check, Key, RefreshCw, AlertTriangle, Trash2,
  Container, Eye, EyeOff, Pencil, Save, ExternalLink, LogOut, ArrowRight,
} from 'lucide-react'

const settingsTabs = [
  { id: 'org', label: 'Organization', icon: <Building2 className="w-3.5 h-3.5" /> },
  { id: 'members', label: 'Members', icon: <Users className="w-3.5 h-3.5" /> },
  { id: 'cli', label: 'CLI & API', icon: <Terminal className="w-3.5 h-3.5" /> },
]

/* ─── Org Settings ─── */
function OrgSettings() {
  const { organization, membership } = useOrganization()
  const { userMemberships, setActive } = useOrganizationList({ userMemberships: { infinite: true } })
  const [showCreateOrg, setShowCreateOrg] = useState(false)
  const { toast } = useToast()

  const handleSwitch = async (orgId: string) => {
    await setActive?.({ organization: orgId })
    toast('success', 'Switched organization')
  }

  return (
    <div className="space-y-6">
      {organization ? (
        <Card className="p-5">
          <div className="flex items-center justify-between mb-4">
            <h3 className="text-xs font-medium text-muted-foreground flex items-center gap-1.5">
              <Building2 className="w-3.5 h-3.5" /> Current Organization
            </h3>
            <Badge variant={membership?.role === 'org:admin' ? 'purple' : 'secondary'}>
              {membership?.role === 'org:admin' ? <><Crown className="w-2.5 h-2.5 mr-1" /> Admin</> : 'Member'}
            </Badge>
          </div>
          <div className="space-y-3">
            <div className="flex items-center justify-between">
              <span className="text-xs text-muted-foreground">Name</span>
              <span className="text-sm text-foreground font-medium">{organization.name}</span>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-xs text-muted-foreground">Slug</span>
              <span className="text-sm text-foreground font-mono">{organization.slug}</span>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-xs text-muted-foreground">ID</span>
              <div className="flex items-center gap-2">
                <span className="text-xs text-foreground font-mono">{organization.id}</span>
                <CopyButton text={organization.id} label="" />
              </div>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-xs text-muted-foreground">Members</span>
              <span className="text-sm text-foreground">{organization.membersCount}</span>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-xs text-muted-foreground">Created</span>
              <span className="text-sm text-foreground">{new Date(organization.createdAt).toLocaleDateString()}</span>
            </div>
          </div>
        </Card>
      ) : (
        <Card className="p-6 border-primary/30 bg-primary/5">
          <div className="flex items-center gap-4 justify-between">
            <div>
              <h3 className="text-sm font-semibold text-foreground mb-1">No organization selected</h3>
              <p className="text-xs text-muted-foreground">Create or join an organization to start collaborating with your team.</p>
            </div>
            <Button size="sm" onClick={() => setShowCreateOrg(true)}>
              <Plus className="w-3.5 h-3.5" /> Create
            </Button>
          </div>
        </Card>
      )}

      <Card className="overflow-hidden">
        <div className="flex items-center justify-between px-5 py-3 border-b border-border">
          <h3 className="text-xs font-medium text-foreground flex items-center gap-1.5">
            <Building2 className="w-3.5 h-3.5" /> Your Organizations
            <Badge variant="secondary" className="ml-1">{userMemberships?.data?.length || 0}</Badge>
          </h3>
          <Button variant="ghost" size="sm" onClick={() => setShowCreateOrg(true)}>
            <Plus className="w-3.5 h-3.5" /> New
          </Button>
        </div>

        {!userMemberships?.data || userMemberships.data.length === 0 ? (
          <div className="p-8 text-center">
            <Building2 className="w-8 h-8 text-muted-foreground mx-auto mb-3" />
            <p className="text-sm text-foreground mb-1">No organizations yet</p>
            <p className="text-xs text-muted-foreground mb-4">Create one to start managing environments and billing.</p>
            <Button size="sm" onClick={() => setShowCreateOrg(true)}>
              <Plus className="w-3.5 h-3.5" /> Create Organization
            </Button>
          </div>
        ) : (
          <div className="divide-y divide-border">
            {userMemberships.data.map(mem => {
              const isActive = mem.organization.id === organization?.id
              return (
                <div
                  key={mem.organization.id}
                  className={cn(
                    'flex items-center gap-4 px-5 py-4 transition-colors',
                    isActive && 'bg-primary/5',
                  )}
                >
                  {mem.organization.imageUrl ? (
                    <img src={mem.organization.imageUrl} alt={mem.organization.name} className="w-9 h-9 rounded-md object-cover shrink-0" />
                  ) : (
                    <div className={cn(
                      'w-9 h-9 rounded-md flex items-center justify-center text-sm font-bold shrink-0',
                      isActive ? 'bg-primary text-primary-foreground' : 'bg-secondary text-muted-foreground',
                    )}>
                      {mem.organization.name[0]?.toUpperCase()}
                    </div>
                  )}

                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2">
                      <p className={cn('text-sm font-medium truncate', isActive ? 'text-primary' : 'text-foreground')}>
                        {mem.organization.name}
                      </p>
                      {isActive && <Badge className="text-[8px]">Active</Badge>}
                    </div>
                    <div className="flex items-center gap-3 mt-0.5">
                      <span className="text-[10px] text-muted-foreground font-mono">{mem.organization.id}</span>
                      <CopyButton text={mem.organization.id} label="" />
                      {mem.role === 'org:admin' && (
                        <span className="text-[10px] text-violet-400 flex items-center gap-0.5">
                          <Crown className="w-2.5 h-2.5" /> Admin
                        </span>
                      )}
                      {mem.organization.membersCount !== undefined && (
                        <span className="text-[10px] text-muted-foreground flex items-center gap-0.5">
                          <Users className="w-2.5 h-2.5" /> {mem.organization.membersCount}
                        </span>
                      )}
                    </div>
                  </div>

                  {isActive ? (
                    <div className="flex items-center gap-1.5 text-primary">
                      <Check className="w-4 h-4" />
                      <span className="text-xs font-medium hidden sm:inline">Current</span>
                    </div>
                  ) : (
                    <Button variant="secondary" size="sm" onClick={() => handleSwitch(mem.organization.id)}>
                      Switch
                    </Button>
                  )}
                </div>
              )
            })}
          </div>
        )}
      </Card>

      <div className="border-t border-border pt-4">
        <p className="text-[10px] text-muted-foreground flex items-center gap-1.5 mb-2"><Terminal className="w-3 h-3" /> CLI commands</p>
        <div className="grid sm:grid-cols-3 gap-2">
          <CodeBlock code="gc org list" />
          <CodeBlock code="gc org create &quot;Team Name&quot;" />
          <CodeBlock code={`gc org switch ${organization?.id || '<org-id>'}`} />
        </div>
      </div>

      <Modal
        open={showCreateOrg}
        onClose={() => setShowCreateOrg(false)}
        title="Create Organization"
        description="Set up a new team to manage environments, billing, and context together"
        size="md"
      >
        <CreateOrganization
          afterCreateOrganizationUrl="/dashboard/settings"
          appearance={{
            elements: {
              rootBox: { width: '100%' },
              card: { backgroundColor: 'transparent', border: 'none', boxShadow: 'none', padding: 0 },
              headerTitle: { color: 'hsl(220 20% 76%)' },
              headerSubtitle: { color: 'hsl(220 12% 38%)' },
              formFieldInput: { backgroundColor: 'hsl(220 14% 4%)', borderColor: 'hsl(220 10% 12%)', color: 'hsl(220 20% 76%)' },
              formButtonPrimary: { backgroundColor: 'hsl(172 26% 48%)' },
            },
          }}
        />
      </Modal>
    </div>
  )
}

/* ─── Members ─── */
function MembersSettings() {
  const { organization, memberships, invitations } = useOrganization({
    memberships: { infinite: true },
    invitations: { infinite: true },
  })
  const [inviteEmail, setInviteEmail] = useState('')
  const [inviteRole, setInviteRole] = useState('org:member')
  const [inviting, setInviting] = useState(false)
  const { toast } = useToast()

  const handleInvite = async () => {
    if (!organization || !inviteEmail) return
    setInviting(true)
    try {
      await organization.inviteMember({ emailAddress: inviteEmail, role: inviteRole as any })
      toast('success', `Invitation sent to ${inviteEmail}`)
      setInviteEmail('')
    } catch (e: any) {
      toast('error', e.message || 'Failed to invite')
    } finally {
      setInviting(false)
    }
  }

  return (
    <div className="space-y-6">
      <Card className="p-5">
        <h3 className="text-xs font-medium text-muted-foreground mb-4 flex items-center gap-1.5"><UserPlus className="w-3.5 h-3.5" /> Invite Member</h3>
        <div className="flex gap-2">
          <Input
            placeholder="teammate@company.com"
            type="email"
            value={inviteEmail}
            onChange={e => setInviteEmail(e.target.value)}
            className="flex-1"
          />
          <Select
            value={inviteRole}
            onChange={e => setInviteRole(e.target.value)}
            options={[
              { value: 'org:member', label: 'Member' },
              { value: 'org:admin', label: 'Admin' },
            ]}
            aria-label="Role"
            className="w-32"
          />
          <Button onClick={handleInvite} loading={inviting} disabled={!inviteEmail}>
            <Send className="w-3.5 h-3.5" /> Invite
          </Button>
        </div>
      </Card>

      <Card>
        <div className="px-4 py-3 border-b border-border">
          <h3 className="text-xs font-medium text-foreground flex items-center gap-1.5"><Users className="w-3.5 h-3.5" /> Members</h3>
        </div>
        {!memberships?.data || memberships.data.length === 0 ? (
          <div className="py-10 text-center text-xs text-muted-foreground">No members</div>
        ) : (
          <Table headers={['Member', 'Role', 'Joined']}>
            {memberships.data.map(mem => (
              <TableRow key={mem.id}>
                <TableCell>
                  <div className="flex items-center gap-2">
                    <div className="w-6 h-6 rounded-full bg-primary/10 flex items-center justify-center text-primary text-[10px] font-bold">
                      {mem.publicUserData?.firstName?.[0] || mem.publicUserData?.identifier?.[0]?.toUpperCase() || '?'}
                    </div>
                    <div>
                      <p className="text-xs text-foreground">{mem.publicUserData?.firstName} {mem.publicUserData?.lastName}</p>
                      <p className="text-[10px] text-muted-foreground">{mem.publicUserData?.identifier}</p>
                    </div>
                  </div>
                </TableCell>
                <TableCell>
                  <Badge variant={mem.role === 'org:admin' ? 'purple' : 'secondary'}>
                    {mem.role === 'org:admin' ? <><Crown className="w-2.5 h-2.5 mr-1" /> Admin</> : 'Member'}
                  </Badge>
                </TableCell>
                <TableCell>{new Date(mem.createdAt).toLocaleDateString()}</TableCell>
              </TableRow>
            ))}
          </Table>
        )}
      </Card>

      {invitations?.data && invitations.data.length > 0 && (
        <Card>
          <div className="px-4 py-3 border-b border-border">
            <h3 className="text-xs font-medium text-foreground flex items-center gap-1.5"><Mail className="w-3.5 h-3.5" /> Pending Invitations</h3>
          </div>
          <Table headers={['Email', 'Role', 'Sent', '']}>
            {invitations.data.map(inv => (
              <TableRow key={inv.id}>
                <TableCell mono>{inv.emailAddress}</TableCell>
                <TableCell><Badge variant="secondary">{inv.role}</Badge></TableCell>
                <TableCell>{new Date(inv.createdAt).toLocaleDateString()}</TableCell>
                <TableCell>
                  <button
                    onClick={() => inv.revoke()}
                    className="text-muted-foreground hover:text-destructive transition-colors"
                    aria-label="Revoke invitation"
                  >
                    <Trash2 className="w-3.5 h-3.5" />
                  </button>
                </TableCell>
              </TableRow>
            ))}
          </Table>
        </Card>
      )}

      <div className="border-t border-border pt-4">
        <p className="text-[10px] text-muted-foreground flex items-center gap-1.5 mb-2"><Terminal className="w-3 h-3" /> CLI commands</p>
        <div className="grid sm:grid-cols-3 gap-2">
          <CodeBlock code="gc org members" />
          <CodeBlock code="gc org invite user@email.com" />
          <CodeBlock code="gc org invitations" />
        </div>
      </div>
    </div>
  )
}

/* ─── CLI & API ─── */
function CLISettings() {
  const { user } = useUser()

  return (
    <div className="space-y-6">
      <Card className="p-5">
        <h3 className="text-xs font-medium text-muted-foreground mb-4 flex items-center gap-1.5"><Terminal className="w-3.5 h-3.5" /> CLI Installation</h3>
        <div className="space-y-4">
          <div>
            <p className="text-[10px] text-muted-foreground mb-1">Step 1 — Install</p>
            <CodeBlock code={`# macOS / Linux
curl -fsSL https://get.gradient.dev | sh

# or build from source
git clone https://github.com/gradient-platform/gradient
cd gradient && make install-cli`} title="Install the CLI" />
          </div>
          <div>
            <p className="text-[10px] text-muted-foreground mb-1">Step 2 — Authenticate</p>
            <CodeBlock code="gc auth login" title="Sign in" />
          </div>
          <div>
            <p className="text-[10px] text-muted-foreground mb-1">Step 3 — Verify</p>
            <CodeBlock code={`gc auth status
# Status:       ✓ logged in
# Name:         ${user?.fullName || 'Your Name'}
# Email:        ${user?.emailAddresses?.[0]?.emailAddress || 'you@email.com'}`} title="Check status" />
          </div>
        </div>
      </Card>

      <Card className="p-5">
        <h3 className="text-xs font-medium text-muted-foreground mb-4 flex items-center gap-1.5"><Key className="w-3.5 h-3.5" /> API Access</h3>
        <Callout variant="info">
          The Gradient API uses Clerk JWTs for authentication. Get a token from the CLI or use the Clerk SDK to generate one programmatically.
        </Callout>
        <div className="mt-4">
          <CodeBlock code={`# Get your token
TOKEN=$(cat ~/.gradient/config.json | python3 -c "import sys,json; print(json.load(sys.stdin)['token'])")

# Use it
curl -H "Authorization: Bearer $TOKEN" \\
     http://localhost:6767/api/v1/environments`} title="API usage example" />
        </div>
      </Card>

      <Card className="p-5">
        <h3 className="text-xs font-medium text-muted-foreground mb-4 flex items-center gap-1.5">🤖 MCP Server (AI Agents)</h3>
        <p className="text-xs text-muted-foreground mb-3">
          Gradient includes a Model Context Protocol (MCP) server for AI agents like Claude, Cursor, and other LLM-based tools.
        </p>
        <CodeBlock code={`# Run the MCP server (stdio JSON-RPC)
./bin/gradient-mcp

# Available tools:
# gradient_env_create, gradient_env_list, gradient_env_status
# gradient_context_save, gradient_context_get, gradient_context_events
# gradient_billing_usage, gradient_snapshot_list, ...`} title="MCP Server" />
      </Card>

      <Card className="p-5">
        <h3 className="text-xs font-medium text-muted-foreground mb-4">Quick Reference</h3>
        <div className="grid sm:grid-cols-2 gap-3">
          {[
            { label: 'Environments', cmds: ['gc env create', 'gc env list', 'gc env ssh <id>'] },
            { label: 'Context', cmds: ['gc context save', 'gc context show', 'gc context live'] },
            { label: 'Billing', cmds: ['gc billing status', 'gc billing usage', 'gc billing setup'] },
            { label: 'Repos', cmds: ['gc repo connect', 'gc repo list'] },
          ].map(group => (
            <div key={group.label} className="bg-background p-3 rounded-md border border-border">
              <p className="text-[10px] font-medium text-foreground mb-1.5">{group.label}</p>
              {group.cmds.map(cmd => (
                <div key={cmd} className="flex items-center justify-between py-0.5">
                  <code className="text-[10px] font-mono text-muted-foreground">{cmd}</code>
                  <CopyButton text={cmd} label="" />
                </div>
              ))}
            </div>
          ))}
        </div>
      </Card>
    </div>
  )
}

/* ─── Main ─── */
export default function SettingsTab() {
  const [activeTab, setActiveTab] = useState('org')

  return (
    <div className="space-y-6">
      <Tabs tabs={settingsTabs} active={activeTab} onChange={setActiveTab} />
      {activeTab === 'org' && <OrgSettings />}
      {activeTab === 'members' && <MembersSettings />}
      {activeTab === 'cli' && <CLISettings />}
    </div>
  )
}
