import { useState, useCallback } from 'react'
import { useOrganization, useUser } from '@clerk/clerk-react'
import { api } from '@/api/client'
import { useFetch, useMutation } from '@/hooks/useAPI'
import { cn, formatCurrency, formatDate, SIZE_LABELS } from '@/lib/utils'
import {
  Button, Card, Badge, EmptyState, Modal, ProgressBar,
  Table, TableRow, TableCell, Skeleton, useToast, CodeBlock, Callout,
} from '@/components/ui'
import {
  CreditCard, DollarSign, Clock, Lock, Unlock, Zap,
  Shield, FileText, Download, ArrowRight, Terminal, Check, AlertTriangle,
  BarChart3, Settings,
} from 'lucide-react'

/* ─── Free Hours Ring ─── */
function FreeHoursRing({ used, total }: { used: number; total: number }) {
  const pct = Math.min(100, (used / total) * 100)
  const remaining = Math.max(0, total - used)
  const circumference = 2 * Math.PI * 54

  return (
    <div className="flex items-center gap-6">
      <div className="relative w-32 h-32 shrink-0">
        <svg viewBox="0 0 120 120" className="w-full h-full -rotate-90" aria-label={`${used.toFixed(1)} of ${total} free hours used`}>
          <circle cx="60" cy="60" r="54" fill="none" stroke="hsl(var(--secondary))" strokeWidth="8" />
          <circle
            cx="60" cy="60" r="54" fill="none"
            stroke={pct >= 90 ? 'hsl(var(--destructive))' : pct >= 70 ? 'hsl(var(--warning))' : 'hsl(var(--primary))'}
            strokeWidth="8" strokeLinecap="round"
            strokeDasharray={circumference}
            strokeDashoffset={circumference - (pct / 100) * circumference}
            className="transition-all duration-1000"
          />
        </svg>
        <div className="absolute inset-0 flex flex-col items-center justify-center">
          <span className="text-xl font-bold text-foreground">{used.toFixed(1)}</span>
          <span className="text-[10px] text-muted-foreground">/ {total} hrs</span>
        </div>
      </div>
      <div>
        <p className="text-sm font-medium text-foreground">{remaining.toFixed(1)} hours remaining</p>
        <p className="text-xs text-muted-foreground mt-1">
          {pct >= 90 ? 'Almost at limit — add payment to continue' :
           pct >= 70 ? 'Getting close to your free tier limit' :
           'Free tier resets monthly'}
        </p>
        <ProgressBar
          value={used}
          max={total}
          color={pct >= 90 ? 'bg-destructive' : pct >= 70 ? 'bg-yellow-500' : 'bg-primary'}
          className="mt-3 w-40"
        />
      </div>
    </div>
  )
}

/* ─── Size Card ─── */
function SizeCard({ sizeKey, hours, allowed }: { sizeKey: string; hours: number; allowed: boolean }) {
  const info = SIZE_LABELS[sizeKey] || { label: sizeKey, rate: '$0', specs: '' }
  const rates: Record<string, number> = { small: 0.15, medium: 0.35, large: 0.70, gpu: 3.50 }
  const cost = hours * (rates[sizeKey] || 0)

  return (
    <Card className={cn('p-4', !allowed && 'opacity-50')}>
      <div className="flex items-center justify-between mb-2">
        <p className="text-xs font-medium text-foreground">{info.label}</p>
        {allowed ? <Unlock className="w-3 h-3 text-primary" /> : <Lock className="w-3 h-3 text-muted-foreground" />}
      </div>
      <p className="text-xl font-bold text-foreground">{hours.toFixed(2)}<span className="text-xs text-muted-foreground font-normal ml-1">hrs</span></p>
      <p className="text-xs text-muted-foreground mt-1">{formatCurrency(cost)}</p>
      <p className="text-[10px] text-muted-foreground">{info.specs} · {info.rate}</p>
    </Card>
  )
}

/* ─── Billing Setup Modal ─── */
function BillingSetupModal({ open, onClose, onSetup }: { open: boolean; onClose: () => void; onSetup: () => void }) {
  const { organization } = useOrganization()
  const { user } = useUser()
  const { toast } = useToast()
  const [emailOverride, setEmailOverride] = useState('')
  const { mutate, loading, error } = useMutation(
    (token: string, orgId: string, body: any) => api.billing.setup(token, orgId, body)
  )

  const orgName = organization?.name || 'My Organization'
  const clerkEmail = user?.primaryEmailAddress?.emailAddress || user?.emailAddresses?.[0]?.emailAddress || ''
  const billingEmail = emailOverride || clerkEmail
  const isValidEmail = /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(billingEmail)

  const { mutate: openPortal } = useMutation(
    (token: string, orgId: string) => api.billing.portal(token, orgId, window.location.href, 'payment_method_update')
  )

  const handleSetup = async () => {
    if (!isValidEmail) {
      toast('error', 'Please enter a valid billing email')
      return
    }
    // Step 1: Create Stripe customer + subscription
    const result = await mutate({ org_name: orgName, email: billingEmail })
    if (!result) return

    // Step 2: Redirect to Stripe portal to add payment method
    try {
      const portal = await openPortal({})
      if (portal?.url) {
        window.location.href = portal.url
        return
      }
    } catch {
      // Portal failed — still show success, they can add payment later
    }

    toast('success', 'Billing configured — add a payment method to unlock all sizes')
    onSetup()
    onClose()
  }

  return (
    <Modal open={open} onClose={onClose} title="Set Up Billing" description="Connect Stripe for metered billing" size="sm" footer={
      <Button onClick={handleSetup} loading={loading} disabled={!isValidEmail}>
        <CreditCard className="w-3.5 h-3.5" /> Connect Stripe
      </Button>
    }>
      <div className="space-y-4">
        <Callout variant="tip">
          Setting up billing unlocks all environment sizes and removes the 20-hour free tier limit.
        </Callout>
        <div className="rounded-md border border-border bg-secondary/50 p-4 space-y-3">
          <div className="flex items-center justify-between">
            <span className="text-xs text-muted-foreground">Organization</span>
            <span className="text-xs font-medium text-foreground">{orgName}</span>
          </div>
          <div>
            <div className="flex items-center justify-between mb-1.5">
              <span className="text-xs text-muted-foreground">Billing email</span>
              {clerkEmail && !emailOverride && (
                <span className="text-xs font-medium text-foreground">{clerkEmail}</span>
              )}
            </div>
            {(!clerkEmail || emailOverride !== '') && (
              <input
                type="email"
                placeholder="billing@company.com"
                value={emailOverride}
                onChange={e => setEmailOverride(e.target.value)}
                className={cn(
                  'w-full rounded-md border bg-transparent px-3 py-1.5 text-xs text-foreground',
                  'placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring',
                  !isValidEmail && emailOverride ? 'border-destructive' : 'border-input',
                )}
                autoFocus
              />
            )}
            {clerkEmail && !emailOverride && (
              <button
                onClick={() => setEmailOverride(clerkEmail)}
                className="text-[10px] text-muted-foreground hover:text-primary transition-colors mt-1"
              >
                Use a different email
              </button>
            )}
          </div>
        </div>
        {error && <p className="text-xs text-destructive">{error}</p>}
      </div>
    </Modal>
  )
}

/* ─── Main Component ─── */
export default function BillingTab() {
  const [showSetup, setShowSetup] = useState(false)
  const [portalLoading, setPortalLoading] = useState(false)
  const { toast } = useToast()

  const { data: status, loading: statusLoading, refetch: refetchStatus } = useFetch(
    useCallback((token: string, orgId: string) => api.billing.status(token, orgId), [])
  )

  const { data: usage, loading: usageLoading, refetch: refetchUsage } = useFetch(
    useCallback((token: string, orgId: string) => api.billing.usage(token, orgId), [])
  )

  const { data: invoices } = useFetch(
    useCallback((token: string, orgId: string) => api.billing.invoices(token, orgId), [])
  )

  const { data: paymentMethod } = useFetch(
    useCallback((token: string, orgId: string) => api.billing.paymentMethod(token, orgId), [])
  )

  const { mutate: openPortal } = useMutation(
    (token: string, orgId: string) => api.billing.portal(token, orgId, window.location.href)
  )

  const { mutate: openPaymentMethodPortal } = useMutation(
    (token: string, orgId: string) => api.billing.portal(token, orgId, window.location.href, 'payment_method_update')
  )

  const handleManageBilling = async () => {
    setPortalLoading(true)
    try {
      const result = await openPortal({})
      if (result?.url) {
        window.location.href = result.url
      }
    } catch {
      toast('error', 'Failed to open billing portal')
    } finally {
      setPortalLoading(false)
    }
  }

  const handleChangePaymentMethod = async () => {
    setPortalLoading(true)
    try {
      const result = await openPaymentMethodPortal({})
      if (result?.url) {
        window.location.href = result.url
      }
    } catch {
      toast('error', 'Failed to open payment method page')
    } finally {
      setPortalLoading(false)
    }
  }

  const isPaid = status?.tier === 'paid' || status?.billing_tier === 'paid'
  const hasPayment = status?.has_payment_method === true
  const freeUsed = status?.free_hours_used || 0
  const freeTotal = 20
  const allowedSizes = status?.allowed_sizes || ['small']

  const chartData = usage ? [
    { name: 'Small', hours: usage.small_hours || 0, cost: (usage.small_hours || 0) * 0.15 },
    { name: 'Medium', hours: usage.medium_hours || 0, cost: (usage.medium_hours || 0) * 0.35 },
    { name: 'Large', hours: usage.large_hours || 0, cost: (usage.large_hours || 0) * 0.70 },
    { name: 'GPU', hours: usage.gpu_hours || 0, cost: (usage.gpu_hours || 0) * 3.50 },
  ] : []

  const totalHours = chartData.reduce((acc, d) => acc + d.hours, 0)
  const totalCost = chartData.reduce((acc, d) => acc + d.cost, 0)

  if (statusLoading) {
    return (
      <div className="space-y-6">
        <Skeleton className="h-40 w-full" />
        <div className="grid sm:grid-cols-4 gap-4">
          {[1,2,3,4].map(i => <Skeleton key={i} className="h-28 w-full" />)}
        </div>
      </div>
    )
  }

  return (
    <div className="space-y-6">
      <div className="flex flex-col sm:flex-row items-start sm:items-center gap-4 justify-between">
        <div className="flex items-center gap-3">
          <Badge variant={isPaid ? 'default' : 'secondary'} className="text-xs">
            <Shield className="w-3 h-3 mr-1" />
            {isPaid ? 'Paid' : 'Free'} Tier
          </Badge>
          <Badge variant={hasPayment ? 'success' : 'warning'}>
            {hasPayment ? <><Check className="w-3 h-3 mr-1" /> Payment active</> : <><AlertTriangle className="w-3 h-3 mr-1" /> No payment</>}
          </Badge>
          {!status?.stripe_configured && (
            <Badge variant="destructive"><AlertTriangle className="w-3 h-3 mr-1" /> Stripe not configured</Badge>
          )}
        </div>
        <div className="flex items-center gap-2">
          {!hasPayment && (
            <Button size="sm" onClick={() => setShowSetup(true)}>
              <Zap className="w-3.5 h-3.5" /> Upgrade — Unlock all sizes
            </Button>
          )}
          {hasPayment && (
            <Button size="sm" variant="outline" onClick={handleManageBilling} loading={portalLoading}>
              <Settings className="w-3.5 h-3.5" /> Manage Billing
            </Button>
          )}
        </div>
      </div>

      {/* Payment Method on File */}
      {hasPayment && paymentMethod?.has_method && (
        <Card className="p-5">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-4">
              <div className="w-10 h-7 rounded bg-secondary flex items-center justify-center">
                <CreditCard className="w-5 h-5 text-muted-foreground" />
              </div>
              <div>
                <p className="text-sm font-medium text-foreground capitalize">
                  {paymentMethod.brand} •••• {paymentMethod.last4}
                </p>
                <p className="text-xs text-muted-foreground">
                  Expires {String(paymentMethod.exp_month).padStart(2, '0')}/{paymentMethod.exp_year}
                </p>
              </div>
            </div>
            <Button size="sm" variant="ghost" onClick={handleChangePaymentMethod} loading={portalLoading}>
              Change
            </Button>
          </div>
        </Card>
      )}

      {!isPaid && (
        <Card className="p-6 border-primary/30 bg-primary/5">
          <div className="flex flex-col sm:flex-row items-start sm:items-center gap-6 justify-between">
            <div>
              <h3 className="text-sm font-semibold text-foreground mb-1 flex items-center gap-2"><Zap className="w-4 h-4 text-primary" /> Unlock the full platform</h3>
              <p className="text-xs text-muted-foreground max-w-md">
                Free tier: 20 hrs/month, Starter instances only. Add a payment method to unlock Standard, Pro, and GPU environments with per-second billing.
              </p>
            </div>
            <Button size="sm" onClick={() => setShowSetup(true)}>
              Add Payment <ArrowRight className="w-3.5 h-3.5" />
            </Button>
          </div>
        </Card>
      )}

      {!isPaid && (
        <Card className="p-6">
          <FreeHoursRing used={freeUsed} total={freeTotal} />
        </Card>
      )}

      <div>
        <div className="flex items-center justify-between mb-3">
          <h3 className="text-sm font-medium text-foreground flex items-center gap-2">
            <BarChart3 className="w-4 h-4 text-muted-foreground" />
            Usage — {new Date().toLocaleDateString('en-US', { month: 'long', year: 'numeric' })}
          </h3>
          <Card className="px-3 py-1.5 flex items-center gap-3">
            <span className="text-xs text-muted-foreground flex items-center gap-1"><Clock className="w-3 h-3" />{totalHours.toFixed(2)} hrs</span>
            <span className="text-xs text-primary font-medium flex items-center gap-1"><DollarSign className="w-3 h-3" />{formatCurrency(totalCost)}</span>
          </Card>
        </div>
        <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
          {['small', 'medium', 'large', 'gpu'].map(size => (
            <SizeCard
              key={size}
              sizeKey={size}
              hours={usage?.[`${size}_hours`] || 0}
              allowed={allowedSizes.includes(size)}
            />
          ))}
        </div>
      </div>

      <Card className="p-5">
        <h3 className="text-xs font-medium text-muted-foreground mb-3">Available Environment Sizes</h3>
        <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
          {Object.entries(SIZE_LABELS).map(([key, info]) => {
            const allowed = allowedSizes.includes(key)
            return (
              <div key={key} className={cn(
                'p-3 rounded-md border text-center transition-colors',
                allowed ? 'border-primary/30 bg-primary/10' : 'border-border opacity-40',
              )}>
                {allowed ? <Unlock className="w-4 h-4 mx-auto mb-1.5 text-primary" /> : <Lock className="w-4 h-4 mx-auto mb-1.5 text-muted-foreground" />}
                <p className="text-xs font-medium text-foreground">{info.label}</p>
                <p className="text-[10px] text-muted-foreground">{info.rate}</p>
              </div>
            )
          })}
        </div>
      </Card>

      <Card>
        <div className="px-4 py-3 border-b border-border">
          <h3 className="text-xs font-medium text-foreground flex items-center gap-1.5"><FileText className="w-3.5 h-3.5" /> Invoices</h3>
        </div>
        {!invoices || invoices.length === 0 ? (
          <div className="py-10 text-center text-xs text-muted-foreground">
            <FileText className="w-5 h-5 mx-auto mb-2" />
            <p>No invoices yet</p>
          </div>
        ) : (
          <Table headers={['Date', 'Amount', 'Status', '']}>
            {invoices.map((inv: any) => (
              <TableRow key={inv.id}>
                <TableCell>{formatDate(inv.created ? new Date(inv.created * 1000) : inv.created_at)}</TableCell>
                <TableCell>{formatCurrency(inv.amount)}</TableCell>
                <TableCell>
                  <Badge variant={inv.status === 'paid' ? 'success' : inv.status === 'open' ? 'warning' : 'destructive'}>
                    {inv.status}
                  </Badge>
                </TableCell>
                <TableCell>
                  {inv.invoice_pdf && (
                    <a href={inv.invoice_pdf} target="_blank" rel="noopener noreferrer" className="text-muted-foreground hover:text-primary">
                      <Download className="w-3.5 h-3.5" />
                    </a>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </Table>
        )}
      </Card>

      <div className="border-t border-border pt-4">
        <p className="text-[10px] text-muted-foreground flex items-center gap-1.5 mb-2"><Terminal className="w-3 h-3" /> CLI commands</p>
        <div className="grid sm:grid-cols-3 gap-2">
          <CodeBlock code="gc billing status" />
          <CodeBlock code="gc billing usage" />
          <CodeBlock code="gc billing invoices" />
        </div>
      </div>

      <BillingSetupModal open={showSetup} onClose={() => setShowSetup(false)} onSetup={() => { refetchStatus(); refetchUsage() }} />
    </div>
  )
}
