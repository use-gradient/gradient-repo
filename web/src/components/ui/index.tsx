import React, { useState, useEffect, useRef, createContext, useContext, forwardRef } from 'react'
import { cn, copyToClipboard } from '@/lib/utils'
import { cva, type VariantProps } from 'class-variance-authority'
import { Slot } from '@radix-ui/react-slot'
import * as DialogPrimitive from '@radix-ui/react-dialog'
import * as TooltipPrimitive from '@radix-ui/react-tooltip'
import { X, Check, Copy, Loader2, Info, AlertTriangle, Flame, Lightbulb, ChevronDown } from 'lucide-react'

/* ━━━ Button ━━━ */
const buttonVariants = cva(
  'inline-flex items-center justify-center gap-2 whitespace-nowrap font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:size-4 [&_svg]:shrink-0',
  {
    variants: {
      variant: {
        default: 'bg-primary text-primary-foreground shadow hover:bg-primary/90',
        destructive: 'bg-destructive text-destructive-foreground shadow-sm hover:bg-destructive/90',
        outline: 'border border-border bg-background shadow-sm hover:bg-secondary hover:text-foreground',
        secondary: 'bg-secondary text-secondary-foreground shadow-sm hover:bg-secondary/80',
        ghost: 'hover:bg-secondary hover:text-foreground',
        link: 'text-primary underline-offset-4 hover:underline',
      },
      size: {
        default: 'h-9 px-4 py-2 text-sm rounded-md',
        sm: 'h-8 rounded-md px-3 text-xs',
        lg: 'h-10 rounded-md px-8 text-sm',
        icon: 'h-9 w-9 rounded-md',
      },
    },
    defaultVariants: { variant: 'default', size: 'default' },
  }
)

interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement>, VariantProps<typeof buttonVariants> {
  loading?: boolean
  asChild?: boolean
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, loading, asChild, children, disabled, ...props }, ref) => {
    const Comp = asChild ? Slot : 'button'
    return (
      <Comp
        className={cn(buttonVariants({ variant, size, className }))}
        ref={ref}
        disabled={disabled || loading}
        {...props}
      >
        {asChild ? (
          children
        ) : (
          <>
            {loading && <Loader2 className="animate-spin" />}
            {children}
          </>
        )}
      </Comp>
    )
  }
)
Button.displayName = 'Button'

/* ━━━ Input ━━━ */
interface InputProps extends React.InputHTMLAttributes<HTMLInputElement> {
  label?: string
  error?: string
  mono?: boolean
}

export const Input = forwardRef<HTMLInputElement, InputProps>(
  ({ label, error, mono, className, id, ...props }, ref) => {
    const inputId = id || label?.toLowerCase().replace(/\s+/g, '-')
    return (
      <div className="space-y-2">
        {label && <label htmlFor={inputId} className="text-sm font-medium text-foreground">{label}</label>}
        <input
          id={inputId}
          ref={ref}
          className={cn(
            'flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm transition-colors',
            'file:border-0 file:bg-transparent file:text-sm file:font-medium',
            'placeholder:text-muted-foreground',
            'focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring',
            'disabled:cursor-not-allowed disabled:opacity-50',
            mono && 'font-mono',
            error && 'border-destructive focus-visible:ring-destructive',
            className,
          )}
          aria-invalid={!!error}
          aria-describedby={error ? `${inputId}-error` : undefined}
          {...props}
        />
        {error && <p id={`${inputId}-error`} className="text-xs text-destructive" role="alert">{error}</p>}
      </div>
    )
  }
)
Input.displayName = 'Input'

/* ━━━ Select (custom popover dropdown) ━━━ */
interface SelectProps {
  label?: string
  options: { value: string; label: string }[]
  value?: string
  onChange?: (e: { target: { value: string } }) => void
  className?: string
  disabled?: boolean
  placeholder?: string
  'aria-label'?: string
  id?: string
}

export function Select({ label, options, value, onChange, className, disabled, placeholder, id, ...props }: SelectProps) {
  const [open, setOpen] = useState(false)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const dropdownRef = useRef<HTMLDivElement>(null)
  const selectId = id || label?.toLowerCase().replace(/\s+/g, '-')

  const selected = options.find(o => o.value === value)

  useEffect(() => {
    if (!open) return
    const handleClick = (e: MouseEvent) => {
      if (
        triggerRef.current?.contains(e.target as Node) ||
        dropdownRef.current?.contains(e.target as Node)
      ) return
      setOpen(false)
    }
    const handleEsc = (e: KeyboardEvent) => { if (e.key === 'Escape') setOpen(false) }
    document.addEventListener('mousedown', handleClick)
    document.addEventListener('keydown', handleEsc)
    return () => { document.removeEventListener('mousedown', handleClick); document.removeEventListener('keydown', handleEsc) }
  }, [open])

  return (
    <div className="space-y-2">
      {label && <label htmlFor={selectId} className="text-sm font-medium text-foreground">{label}</label>}
      <div className="relative">
        <button
          ref={triggerRef}
          id={selectId}
          type="button"
          disabled={disabled}
          onClick={() => !disabled && setOpen(v => !v)}
          aria-label={props['aria-label']}
          className={cn(
            'flex h-9 w-full items-center justify-between rounded-md border border-input bg-transparent px-3 py-2 text-sm shadow-sm',
            'focus:outline-none focus:ring-1 focus:ring-ring',
            'disabled:cursor-not-allowed disabled:opacity-50',
            'cursor-pointer text-left',
            className,
          )}
        >
          <span className={cn(!selected && 'text-muted-foreground')}>
            {selected?.label || placeholder || 'Select…'}
          </span>
          <ChevronDown className={cn('h-4 w-4 text-muted-foreground transition-transform', open && 'rotate-180')} />
        </button>
        {open && (
          <div
            ref={dropdownRef}
            className="absolute z-50 mt-1 w-full rounded-md border border-border bg-popover shadow-lg py-1 max-h-60 overflow-y-auto animate-in fade-in-0 zoom-in-95"
          >
            {options.map(o => (
              <button
                key={o.value}
                type="button"
                className={cn(
                  'flex w-full items-center gap-2 px-3 py-1.5 text-sm transition-colors',
                  'hover:bg-secondary/80 text-foreground',
                  o.value === value && 'bg-secondary font-medium',
                )}
                onClick={() => {
                  onChange?.({ target: { value: o.value } })
                  setOpen(false)
                }}
              >
                {o.value === value && <Check className="h-3.5 w-3.5 text-primary shrink-0" />}
                {o.value !== value && <span className="w-3.5 shrink-0" />}
                {o.label}
              </button>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

/* ━━━ Card ━━━ */
export const Card = forwardRef<HTMLDivElement, React.HTMLAttributes<HTMLDivElement> & { hover?: boolean }>(
  ({ className, hover, children, onClick, ...props }, ref) => (
    <div
      ref={ref}
      className={cn(
        'rounded-lg border border-border bg-card text-card-foreground shadow-sm',
        hover && 'cursor-pointer transition-colors hover:border-muted-foreground/30',
        onClick && 'cursor-pointer',
        className,
      )}
      onClick={onClick}
      role={onClick ? 'button' : undefined}
      tabIndex={onClick ? 0 : undefined}
      onKeyDown={onClick ? (e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); (onClick as any)(e) } } : undefined}
      {...props}
    >
      {children}
    </div>
  )
)
Card.displayName = 'Card'

/* ━━━ Badge ━━━ */
const badgeVariants = cva(
  'inline-flex items-center rounded-md border px-2.5 py-0.5 text-xs font-semibold transition-colors focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2',
  {
    variants: {
      variant: {
        default: 'border-transparent bg-primary/10 text-primary',
        secondary: 'border-transparent bg-secondary text-secondary-foreground',
        destructive: 'border-transparent bg-destructive/10 text-destructive',
        outline: 'text-foreground border-border',
        success: 'border-transparent bg-emerald-500/10 text-emerald-400',
        warning: 'border-transparent bg-yellow-500/10 text-yellow-400',
        purple: 'border-transparent bg-violet-500/10 text-violet-400',
      },
    },
    defaultVariants: { variant: 'default' },
  }
)

interface BadgeProps extends React.HTMLAttributes<HTMLDivElement>, VariantProps<typeof badgeVariants> {}

export function Badge({ className, variant, ...props }: BadgeProps) {
  return <div className={cn(badgeVariants({ variant }), className)} {...props} />
}

/* ━━━ StatusDot ━━━ */
const dotColors: Record<string, string> = {
  running: 'bg-emerald-400 animate-pulse-dot',
  creating: 'bg-yellow-400 animate-pulse-dot',
  migrating: 'bg-yellow-400 animate-pulse-dot',
  stopped: 'bg-muted-foreground',
  destroyed: 'bg-muted-foreground/50',
  error: 'bg-destructive',
  healthy: 'bg-emerald-400',
  connected: 'bg-emerald-400',
  disconnected: 'bg-destructive',
}

export function StatusDot({ status, className }: { status: string; className?: string }) {
  return (
    <span
      className={cn('inline-block h-2 w-2 rounded-full shrink-0', dotColors[status] || 'bg-muted-foreground', className)}
      aria-label={status}
      role="img"
    />
  )
}

/* ━━━ ProgressBar ━━━ */
export function ProgressBar({ value, max = 100, color = 'bg-primary', className }: {
  value: number; max?: number; color?: string; className?: string
}) {
  const pct = Math.min(100, Math.max(0, (value / max) * 100))
  return (
    <div className={cn('h-2 w-full overflow-hidden rounded-full bg-secondary', className)} role="progressbar" aria-valuenow={value} aria-valuemin={0} aria-valuemax={max}>
      <div className={cn('h-full rounded-full transition-all duration-500 ease-out', color)} style={{ width: `${pct}%` }} />
    </div>
  )
}

/* ━━━ Modal (Radix Dialog) ━━━ */
interface ModalProps {
  open: boolean
  onClose: () => void
  title: string
  description?: string
  children: React.ReactNode
  footer?: React.ReactNode
  size?: 'sm' | 'md' | 'lg'
}

const modalSizes = { sm: 'max-w-sm', md: 'max-w-lg', lg: 'max-w-2xl' }

export function Modal({ open, onClose, title, description, children, footer, size = 'md' }: ModalProps) {
  return (
    <DialogPrimitive.Root open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className="fixed inset-0 z-50 bg-black/80 data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0" />
        <DialogPrimitive.Content className={cn(
          'fixed left-[50%] top-[50%] z-50 grid min-w-[50%] translate-x-[-50%] translate-y-[-50%] gap-4 border border-border bg-card p-6 shadow-lg duration-200',
          'data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0',
          'data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95',
          'data-[state=closed]:slide-out-to-left-1/2 data-[state=closed]:slide-out-to-top-[48%]',
          'data-[state=open]:slide-in-from-left-1/2 data-[state=open]:slide-in-from-top-[48%]',
          'rounded-lg max-h-[85vh] overflow-hidden', modalSizes[size],
        )}>
          <div className="flex flex-col space-y-1.5 shrink-0">
            <DialogPrimitive.Title className="text-lg font-semibold leading-none tracking-tight text-foreground">{title}</DialogPrimitive.Title>
            {description && <DialogPrimitive.Description className="text-sm text-muted-foreground">{description}</DialogPrimitive.Description>}
          </div>
          <div className="overflow-y-auto min-h-0">{children}</div>
          {footer && <div className="flex flex-col-reverse sm:flex-row sm:justify-end sm:space-x-2 shrink-0">{footer}</div>}
          <DialogPrimitive.Close className="absolute right-4 top-4 rounded-sm opacity-70 ring-offset-background transition-opacity hover:opacity-100 focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2 disabled:pointer-events-none">
            <X className="h-4 w-4" />
            <span className="sr-only">Close</span>
          </DialogPrimitive.Close>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  )
}

/* ━━━ ConfirmDialog ━━━ */
export function ConfirmDialog({ open, onClose, onConfirm, title, message, confirmLabel = 'Confirm', destructive = false, loading = false }: {
  open: boolean; onClose: () => void; onConfirm: () => void; title: string; message: string; confirmLabel?: string; destructive?: boolean; loading?: boolean
}) {
  return (
    <Modal open={open} onClose={onClose} title={title} size="sm" footer={
      <>
        <Button variant="outline" onClick={onClose}>Cancel</Button>
        <Button variant={destructive ? 'destructive' : 'default'} onClick={onConfirm} loading={loading}>{confirmLabel}</Button>
      </>
    }>
      <p className="text-sm text-muted-foreground">{message}</p>
    </Modal>
  )
}

/* ━━━ CopyButton ━━━ */
export function CopyButton({ text, label = 'Copy', className }: { text: string; label?: string; className?: string }) {
  const [copied, setCopied] = useState(false)
  const handleCopy = async () => {
    await copyToClipboard(text)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }
  return (
    <button
      onClick={handleCopy}
      className={cn('inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-primary transition-colors', className)}
      aria-label={copied ? 'Copied' : label}
    >
      {copied ? <Check className="h-3.5 w-3.5 text-emerald-400" /> : <Copy className="h-3.5 w-3.5" />}
      {copied ? 'Copied!' : label}
    </button>
  )
}

/* ━━━ CodeBlock ━━━ */
export function CodeBlock({ code, language = 'bash', title }: { code: string; language?: string; title?: string }) {
  return (
    <div className="rounded-lg border border-border overflow-hidden bg-background">
      {title && (
        <div className="flex items-center justify-between px-4 py-2.5 bg-secondary/50 border-b border-border">
          <span className="text-xs text-muted-foreground font-medium">{title}</span>
          <CopyButton text={code} />
        </div>
      )}
      <pre className="p-4 overflow-x-auto">
        <code className="text-sm font-mono text-foreground leading-relaxed">{code}</code>
      </pre>
      {!title && (
        <div className="flex justify-end px-3 py-2 border-t border-border">
          <CopyButton text={code} />
        </div>
      )}
    </div>
  )
}

/* ━━━ EmptyState ━━━ */
export function EmptyState({ icon: Icon, title, description, action }: {
  icon: React.ComponentType<{ className?: string }>; title: string; description: string; action?: React.ReactNode
}) {
  return (
    <div className="flex flex-col items-center justify-center py-16 text-center">
      <div className="flex h-14 w-14 items-center justify-center rounded-full bg-muted mb-4">
        <Icon className="h-6 w-6 text-muted-foreground" />
      </div>
      <h3 className="text-sm font-semibold text-foreground mb-1">{title}</h3>
      <p className="text-sm text-muted-foreground max-w-sm mb-4">{description}</p>
      {action}
    </div>
  )
}

/* ━━━ Skeleton ━━━ */
export function Skeleton({ className }: { className?: string }) {
  return <div className={cn('animate-pulse rounded-md bg-muted', className)} />
}

/* ━━━ Callout ━━━ */
type CalloutVariant = 'info' | 'warning' | 'danger' | 'tip'
const calloutConfig: Record<CalloutVariant, { border: string; icon: typeof Info }> = {
  info:    { border: 'border-l-blue-400', icon: Info },
  warning: { border: 'border-l-yellow-400', icon: AlertTriangle },
  danger:  { border: 'border-l-destructive', icon: Flame },
  tip:     { border: 'border-l-primary', icon: Lightbulb },
}

export function Callout({ variant = 'info', title, children }: { variant?: CalloutVariant; title?: string; children: React.ReactNode }) {
  const { border, icon: Icon } = calloutConfig[variant]
  return (
    <div className={cn('border-l-4 bg-muted/50 rounded-r-md p-4', border)}>
      <div className="flex items-start gap-3">
        <Icon className="h-4 w-4 mt-0.5 shrink-0 text-muted-foreground" />
        <div>
          {title && <p className="text-sm font-medium text-foreground mb-1">{title}</p>}
          <div className="text-sm text-muted-foreground leading-relaxed">{children}</div>
        </div>
      </div>
    </div>
  )
}

/* ━━━ Toast System ━━━ */
type ToastType = 'success' | 'error' | 'info'
interface Toast { id: number; type: ToastType; message: string }
const ToastCtx = createContext<{ toast: (type: ToastType, message: string) => void }>({ toast: () => {} })
export const useToast = () => useContext(ToastCtx)

export function ToastProvider({ children }: { children: React.ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([])
  const nextId = useRef(0)

  const toast = (type: ToastType, message: string) => {
    const id = nextId.current++
    setToasts(prev => [...prev, { id, type, message }])
    setTimeout(() => setToasts(prev => prev.filter(t => t.id !== id)), 4000)
  }

  const styles: Record<ToastType, string> = {
    success: 'border-emerald-500/30 bg-emerald-500/10 text-emerald-400',
    error: 'border-destructive/30 bg-destructive/10 text-destructive',
    info: 'border-primary/30 bg-primary/10 text-primary',
  }

  return (
    <ToastCtx.Provider value={{ toast }}>
      {children}
      <div className="fixed bottom-4 right-4 z-[100] flex flex-col gap-2" aria-live="polite">
        {toasts.map(t => (
          <div key={t.id} className={cn('rounded-lg border px-4 py-3 text-sm shadow-lg animate-slide-in backdrop-blur-sm', styles[t.type])}>
            {t.message}
          </div>
        ))}
      </div>
    </ToastCtx.Provider>
  )
}

/* ━━━ Tabs ━━━ */
export function Tabs({ tabs, active, onChange }: {
  tabs: { id: string; label: string; icon?: React.ReactNode }[]
  active: string
  onChange: (id: string) => void
}) {
  return (
    <div className="inline-flex h-9 items-center justify-center rounded-lg bg-muted p-1 text-muted-foreground" role="tablist">
      {tabs.map(tab => (
        <button
          key={tab.id}
          role="tab"
          aria-selected={active === tab.id}
          onClick={() => onChange(tab.id)}
          className={cn(
            'inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-md px-3 py-1 text-sm font-medium ring-offset-background transition-all',
            'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2',
            active === tab.id
              ? 'bg-background text-foreground shadow'
              : 'hover:text-foreground',
          )}
        >
          {tab.icon}
          {tab.label}
        </button>
      ))}
    </div>
  )
}

/* ━━━ Table ━━━ */
export function Table({ headers, children, className }: {
  headers: string[]; children: React.ReactNode; className?: string
}) {
  return (
    <div className={cn('relative w-full overflow-auto', className)}>
      <table className="w-full caption-bottom text-sm">
        <thead className="[&_tr]:border-b">
          <tr className="border-b border-border transition-colors">
            {headers.map(h => (
              <th key={h} className="h-10 px-4 text-left align-middle font-medium text-muted-foreground [&:has([role=checkbox])]:pr-0">{h}</th>
            ))}
          </tr>
        </thead>
        <tbody className="[&_tr:last-child]:border-0">{children}</tbody>
      </table>
    </div>
  )
}

export function TableRow({ children, className }: { children: React.ReactNode; className?: string }) {
  return <tr className={cn('border-b border-border transition-colors hover:bg-muted/50', className)}>{children}</tr>
}

export function TableCell({ children, mono, className }: { children: React.ReactNode; mono?: boolean; className?: string }) {
  return <td className={cn('p-4 align-middle [&:has([role=checkbox])]:pr-0', mono && 'font-mono text-xs', className)}>{children}</td>
}

/* ━━━ Tooltip (Radix) ━━━ */
export function Tooltip({ children, text }: { children: React.ReactNode; text: string }) {
  return (
    <TooltipPrimitive.Provider delayDuration={200}>
      <TooltipPrimitive.Root>
        <TooltipPrimitive.Trigger asChild>{children}</TooltipPrimitive.Trigger>
        <TooltipPrimitive.Portal>
          <TooltipPrimitive.Content
            className="z-50 overflow-hidden rounded-md bg-popover px-3 py-1.5 text-xs text-popover-foreground shadow-md animate-in fade-in-0 zoom-in-95"
            sideOffset={5}
          >
            {text}
          </TooltipPrimitive.Content>
        </TooltipPrimitive.Portal>
      </TooltipPrimitive.Root>
    </TooltipPrimitive.Provider>
  )
}

/* ━━━ Separator ━━━ */
export function Separator({ className }: { className?: string }) {
  return <div className={cn('shrink-0 bg-border h-[1px] w-full', className)} role="separator" />
}
