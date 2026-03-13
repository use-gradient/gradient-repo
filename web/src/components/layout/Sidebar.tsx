import { NavLink, useLocation } from 'react-router-dom'
import { useOrganizationList, useOrganization, useUser, CreateOrganization, UserButton } from '@clerk/clerk-react'
import { useState, useRef, useEffect, useCallback } from 'react'
import { createPortal } from 'react-dom'
import { cn } from '@/lib/utils'
import {
  Server, Brain, CreditCard, Settings, BookOpen, Terminal,
  ChevronDown, Building2, PanelLeftClose, Menu, ExternalLink,
  Plus, Check, Crown, Users, Bot, Plug, Rocket,
} from 'lucide-react'
import { Badge, Modal } from '@/components/ui'

function OrgAvatar({ name, imageUrl, size = 'md', active }: { name: string; imageUrl?: string; size?: 'sm' | 'md'; active?: boolean }) {
  const dims = size === 'sm' ? 'w-6 h-6' : 'w-7 h-7'
  if (imageUrl) {
    return <img src={imageUrl} alt={name} className={cn(dims, 'rounded-md object-cover shrink-0')} />
  }
  return (
    <div className={cn(
      dims, 'rounded-md flex items-center justify-center text-[10px] font-bold shrink-0',
      active ? 'bg-primary text-primary-foreground' : 'bg-primary/10 text-primary',
    )}>
      {name?.[0]?.toUpperCase() || 'P'}
    </div>
  )
}

const navItems = [
  { to: '/dashboard/get-started',  label: 'Get Started',  icon: Rocket },
  { to: '/dashboard/environments', label: 'Environments', icon: Server },
  { to: '/dashboard/tasks',        label: 'Tasks',        icon: Bot },
  { to: '/dashboard/context',      label: 'Memory',       icon: Brain },
  { to: '/dashboard/billing',      label: 'Billing',      icon: CreditCard },
  { to: '/dashboard/integrations', label: 'Integrations', icon: Plug },
  { to: '/dashboard/settings',     label: 'Settings',     icon: Settings },
]

export function Sidebar({ collapsed, setCollapsed }: { collapsed: boolean; setCollapsed: (v: boolean) => void }) {
  const [orgOpen, setOrgOpen] = useState(false)
  const [mobileOpen, setMobileOpen] = useState(false)
  const [showCreateOrg, setShowCreateOrg] = useState(false)
  const { user } = useUser()
  const { organization, membership } = useOrganization()
  const { userMemberships, setActive } = useOrganizationList({ userMemberships: { infinite: true } })
  const location = useLocation()
  const triggerRef = useRef<HTMLButtonElement>(null)
  const popoverRef = useRef<HTMLDivElement>(null)
  const [popoverPos, setPopoverPos] = useState<{ top: number; left: number }>({ top: 0, left: 0 })

  const updatePopoverPosition = useCallback(() => {
    if (!triggerRef.current) return
    const rect = triggerRef.current.getBoundingClientRect()
    setPopoverPos({
      top: rect.top,
      left: rect.right + 8,
    })
  }, [])

  useEffect(() => {
    if (!orgOpen) return
    updatePopoverPosition()
    const handler = (e: MouseEvent) => {
      const target = e.target as Node
      if (
        triggerRef.current && !triggerRef.current.contains(target) &&
        popoverRef.current && !popoverRef.current.contains(target)
      ) {
        setOrgOpen(false)
      }
    }
    document.addEventListener('mousedown', handler)
    window.addEventListener('resize', updatePopoverPosition)
    window.addEventListener('scroll', updatePopoverPosition, true)
    return () => {
      document.removeEventListener('mousedown', handler)
      window.removeEventListener('resize', updatePopoverPosition)
      window.removeEventListener('scroll', updatePopoverPosition, true)
    }
  }, [orgOpen, updatePopoverPosition])

  const handleOrgSwitch = async (orgId: string) => {
    await setActive?.({ organization: orgId })
    setOrgOpen(false)
  }

  const currentRole = membership?.role
  const orgCount = userMemberships?.data?.length || 0

  return (
    <>
      {/* Mobile menu button */}
      {!mobileOpen && (
        <button
          className="fixed top-3 left-3 z-50 p-2 bg-card border border-border rounded-md md:hidden"
          onClick={() => setMobileOpen(true)}
          aria-label="Open navigation"
        >
          <Menu className="w-5 h-5 text-foreground" />
        </button>
      )}

      {/* Mobile overlay */}
      {mobileOpen && (
        <div className="fixed inset-0 z-40 bg-background/80 backdrop-blur-sm md:hidden" onClick={() => setMobileOpen(false)} />
      )}

      {/* Sidebar */}
      <aside
        className={cn(
          'fixed top-0 left-0 h-full bg-card border-r border-border z-40 flex flex-col transition-all duration-200',
          collapsed ? 'w-16' : 'w-56',
          mobileOpen ? 'translate-x-0' : '-translate-x-full md:translate-x-0',
        )}
        role="navigation"
        aria-label="Main navigation"
      >
        {/* Logo / Collapse toggle */}
        <div className={cn('flex items-center border-b border-border h-14', collapsed ? 'justify-center px-0' : 'px-4')}>
          {collapsed ? (
            <button
              onClick={() => setCollapsed(false)}
              className="hidden md:flex text-muted-foreground hover:text-foreground p-1"
              aria-label="Expand sidebar"
            >
              <PanelLeftClose className="w-4 h-4 rotate-180" />
            </button>
          ) : (
            <>
              <NavLink to="/" className="flex items-center gap-2.5 text-foreground hover:text-primary transition-colors" onClick={() => setMobileOpen(false)}>
                <img src="/logo.svg" alt="Gradient" className="h-7 w-auto" />
                <span className="text-sm font-semibold">Gradient</span>
              </NavLink>
              <button
                onClick={() => setCollapsed(true)}
                className="hidden md:flex ml-auto text-muted-foreground hover:text-foreground p-1"
                aria-label="Collapse sidebar"
              >
                <PanelLeftClose className="w-4 h-4" />
              </button>
            </>
          )}
        </div>

        {/* Org Switcher */}
        <div className={cn('border-b border-border', collapsed ? 'px-1 py-2' : 'px-3 py-3')}>
          {collapsed ? (
            <button
              onClick={() => { setCollapsed(false); setTimeout(() => setOrgOpen(true), 200) }}
              className="w-full flex items-center justify-center py-1.5 text-muted-foreground hover:text-foreground rounded-md hover:bg-secondary transition-colors"
              aria-label="Expand to switch organization"
              title={organization?.name || 'Personal'}
            >
              <OrgAvatar name={organization?.name || 'Personal'} imageUrl={organization?.imageUrl} size="sm" />
            </button>
          ) : (
            <button
              ref={triggerRef}
              onClick={() => setOrgOpen(!orgOpen)}
              className={cn(
                'w-full flex items-center gap-2 px-2.5 py-2 text-xs rounded-md hover:bg-secondary transition-colors group',
                orgOpen && 'bg-secondary',
              )}
              aria-expanded={orgOpen}
              aria-haspopup="listbox"
            >
              <OrgAvatar name={organization?.name || 'Personal'} imageUrl={organization?.imageUrl} />
              <div className="flex-1 min-w-0 text-left">
                <p className="text-xs font-medium text-foreground truncate">{organization?.name || 'Personal'}</p>
                <p className="text-[10px] text-muted-foreground truncate">
                  {currentRole === 'org:admin' ? 'Admin' : currentRole === 'org:member' ? 'Member' : 'Owner'}
                  {orgCount > 1 && ` · ${orgCount} orgs`}
                </p>
              </div>
              <ChevronDown className={cn('w-3.5 h-3.5 text-muted-foreground transition-transform shrink-0', orgOpen && 'rotate-180')} />
            </button>
          )}
        </div>

        {/* Nav links */}
        <nav className="flex-1 py-3 px-2 space-y-0.5 overflow-y-auto">
          {navItems.map(item => (
            <NavLink
              key={item.to}
              to={item.to}
              onClick={() => setMobileOpen(false)}
              className={({ isActive }) => cn(
                'flex items-center gap-3 px-3 py-2 rounded-md text-sm font-medium transition-colors',
                isActive ? 'bg-primary/10 text-primary' : 'text-muted-foreground hover:text-foreground hover:bg-secondary',
                collapsed && 'justify-center px-0',
              )}
              aria-current={location.pathname.startsWith(item.to) ? 'page' : undefined}
              title={collapsed ? item.label : undefined}
            >
              <item.icon className="w-4 h-4 shrink-0" />
              {!collapsed && item.label}
            </NavLink>
          ))}
        </nav>

        {/* External links */}
        <div className="px-2 py-2 border-t border-border space-y-0.5">
          <a
            href="/docs"
            target="_blank"
            rel="noopener noreferrer"
            className={cn(
              'flex items-center gap-3 px-3 py-2 rounded-md text-xs text-muted-foreground hover:text-foreground hover:bg-secondary transition-colors',
              collapsed && 'justify-center px-0',
            )}
            title={collapsed ? 'Docs' : undefined}
          >
            <BookOpen className="w-4 h-4 shrink-0" />
            {!collapsed && <>Docs <ExternalLink className="w-3 h-3 ml-auto" /></>}
          </a>
          <a
            href="/docs/cli/installation"
            target="_blank"
            rel="noopener noreferrer"
            className={cn(
              'flex items-center gap-3 px-3 py-2 rounded-md text-xs text-muted-foreground hover:text-foreground hover:bg-secondary transition-colors',
              collapsed && 'justify-center px-0',
            )}
            title={collapsed ? 'CLI Guide' : undefined}
          >
            <Terminal className="w-4 h-4 shrink-0" />
            {!collapsed && <>CLI Guide <ExternalLink className="w-3 h-3 ml-auto" /></>}
          </a>
        </div>

        {/* User */}
        <div className={cn('border-t border-border', collapsed ? 'py-2' : 'px-3 py-3')}>
          <div className={cn('flex items-center', collapsed ? 'w-full flex justify-center' : 'gap-2.5')}>
            <UserButton
              afterSignOutUrl="/"
              appearance={{
                elements: {
                  avatarBox: 'w-7 h-7',
                  rootBox: collapsed ? 'w-full flex justify-center' : '',
                  userButtonTrigger: collapsed ? 'w-full flex justify-center' : '',
                },
              }}
            />
            {!collapsed && (
              <div className="flex-1 min-w-0">
                <p className="text-xs font-medium text-foreground truncate">{user?.fullName || 'User'}</p>
                <p className="text-[10px] text-muted-foreground truncate">{user?.emailAddresses?.[0]?.emailAddress}</p>
              </div>
            )}
          </div>
        </div>
      </aside>


      {/* Org Switcher Popover (portal) */}
      {orgOpen && createPortal(
        <div
          ref={popoverRef}
          className="fixed z-[9999] w-64 bg-popover border border-border rounded-lg shadow-xl animate-fade-in"
          style={{ top: popoverPos.top, left: popoverPos.left }}
          role="listbox"
          aria-label="Organizations"
        >
          <div className="px-3 py-2.5 border-b border-border">
            <p className="text-[10px] font-medium text-muted-foreground uppercase tracking-wider">Organizations</p>
          </div>
          <div className="max-h-64 overflow-y-auto py-1">
            {userMemberships?.data?.map(mem => {
              const isActive = mem.organization.id === organization?.id
              const role = mem.role
              return (
                <button
                  key={mem.organization.id}
                  onClick={() => handleOrgSwitch(mem.organization.id)}
                  className={cn(
                    'w-full flex items-center gap-2.5 px-3 py-2.5 text-left hover:bg-secondary transition-colors',
                    isActive && 'bg-primary/5',
                  )}
                  role="option"
                  aria-selected={isActive}
                >
                  <OrgAvatar
                    name={mem.organization.name}
                    imageUrl={mem.organization.imageUrl}
                    active={isActive}
                  />
                  <div className="flex-1 min-w-0">
                    <p className={cn('text-xs truncate', isActive ? 'text-primary font-medium' : 'text-foreground')}>
                      {mem.organization.name}
                    </p>
                    <div className="flex items-center gap-1.5 mt-0.5">
                      {role === 'org:admin' && (
                        <span className="text-[9px] text-violet-400 flex items-center gap-0.5">
                          <Crown className="w-2.5 h-2.5" /> Admin
                        </span>
                      )}
                      {mem.organization.membersCount && (
                        <span className="text-[9px] text-muted-foreground flex items-center gap-0.5">
                          <Users className="w-2.5 h-2.5" /> {mem.organization.membersCount}
                        </span>
                      )}
                    </div>
                  </div>
                  {isActive && <Check className="w-3.5 h-3.5 text-primary shrink-0" />}
                </button>
              )
            })}
          </div>
          <div className="border-t border-border">
            <button
              onClick={() => { setShowCreateOrg(true); setOrgOpen(false) }}
              className="w-full flex items-center gap-2 px-3 py-2.5 text-xs text-muted-foreground hover:text-primary hover:bg-secondary transition-colors rounded-b-lg"
            >
              <Plus className="w-3.5 h-3.5" />
              Create organization
            </button>
          </div>
        </div>,
        document.body,
      )}

      {/* Create Org Modal */}
      <Modal
        open={showCreateOrg}
        onClose={() => setShowCreateOrg(false)}
        title="Create Organization"
        description="Set up a new team to collaborate on environments"
        size="md"
      >
        <CreateOrganization
          afterCreateOrganizationUrl="/dashboard/environments"
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
    </>
  )
}
