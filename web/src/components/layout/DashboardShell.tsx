import { useState } from 'react'
import { Outlet, useLocation } from 'react-router-dom'
import { useOrganization, CreateOrganization } from '@clerk/clerk-react'
import { Sidebar } from './Sidebar'
import { HelpCircle, Building2 } from 'lucide-react'
import { cn } from '@/lib/utils'
import { CopyButton } from '@/components/ui'

const IS_DEV = import.meta.env.DEV

const tabTitles: Record<string, string> = {
  environments:  'Environments',
  tasks:         'Agent Tasks',
  context:       'Repo Memory',
  billing:       'Billing & Usage',
  integrations:  'Integrations',
  'get-started': 'Get Started',
  settings:      'Settings',
}

export function DashboardShell() {
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false)
  const location = useLocation()
  const { organization } = useOrganization()
  const segment = location.pathname.split('/').pop() || 'environments'
  const title = tabTitles[segment] || 'Dashboard'

  return (
    <div className="min-h-screen bg-background">
      <a href="#main-content" className="skip-link">Skip to main content</a>
      <Sidebar collapsed={sidebarCollapsed} setCollapsed={setSidebarCollapsed} />

      <main
        id="main-content"
        className={cn('transition-all duration-200', sidebarCollapsed ? 'md:ml-16' : 'md:ml-56')}
      >
        <header className="sticky top-0 z-30 bg-background/95 backdrop-blur-sm border-b border-border">
          <div className="flex items-center justify-between h-14 px-6 md:px-8">
            <div className="flex items-center gap-3 ml-10 md:ml-0">
              <h1 className="text-base font-semibold text-foreground">{title}</h1>
              {organization && (
                <div className="hidden sm:flex items-center gap-1.5 px-2 py-1 bg-secondary rounded-md border border-border">
                  <Building2 className="w-3 h-3 text-muted-foreground" />
                  <span className="text-[10px] text-muted-foreground font-medium truncate max-w-[120px]">{organization.name}</span>
                  <CopyButton text={organization.slug || organization.id} label="" className="opacity-50 hover:opacity-100" />
                </div>
              )}
            </div>
            <div className="flex items-center gap-3">
              <a
                href={`/docs/dashboard/${segment}`}
                target="_blank"
                rel="noopener noreferrer"
                className="text-muted-foreground hover:text-foreground p-1.5 transition-colors"
                aria-label="Help for this page"
              >
                <HelpCircle className="w-4 h-4" />
              </a>
            </div>
          </div>
        </header>

        <div className="px-6 md:px-8 py-6">
          {!IS_DEV && !organization ? (
            <div className="flex flex-col items-center justify-center py-20 gap-6">
              <div className="text-center space-y-2">
                <Building2 className="w-10 h-10 text-muted-foreground mx-auto mb-3" />
                <h2 className="text-lg font-semibold text-foreground">Create an Organization</h2>
                <p className="text-sm text-muted-foreground max-w-md">
                  Create an organization to get started. Organizations hold your environments, context, integrations, and billing.
                </p>
              </div>
              <CreateOrganization
                afterCreateOrganizationUrl="/dashboard/get-started"
                appearance={{
                  elements: {
                    rootBox: { width: '100%', maxWidth: '420px' },
                    card: { backgroundColor: 'hsl(220 14% 5%)', borderColor: 'hsl(220 10% 12%)', boxShadow: 'none' },
                    headerTitle: { color: 'hsl(220 20% 76%)' },
                    headerSubtitle: { color: 'hsl(220 12% 38%)' },
                    formFieldInput: { backgroundColor: 'hsl(220 14% 4%)', borderColor: 'hsl(220 10% 12%)', color: 'hsl(220 20% 76%)' },
                    formButtonPrimary: { backgroundColor: 'hsl(172 26% 48%)' },
                  },
                }}
              />
            </div>
          ) : (
            <Outlet />
          )}
        </div>
      </main>
    </div>
  )
}
