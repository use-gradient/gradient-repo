import { type ClassValue, clsx } from 'clsx'
import { twMerge } from 'tailwind-merge'

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

/** Format a date to relative time */
export function timeAgo(date: string | Date): string {
  const now = new Date()
  const d = typeof date === 'string' ? new Date(date) : date
  const seconds = Math.floor((now.getTime() - d.getTime()) / 1000)
  if (seconds < 60) return 'just now'
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`
  if (seconds < 604800) return `${Math.floor(seconds / 86400)}d ago`
  return d.toLocaleDateString()
}

export function formatDate(date: string | Date | undefined | null): string {
  if (!date) return '—'
  const d = typeof date === 'string' ? new Date(date) : date
  if (isNaN(d.getTime())) return '—'
  return d.toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' })
}

export function formatDuration(seconds: number): string {
  if (seconds < 60) return `${seconds}s`
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ${seconds % 60}s`
  const h = Math.floor(seconds / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  return `${h}h ${m}m`
}

export function formatCurrency(amount: number): string {
  return `$${amount.toFixed(2)}`
}

export function truncate(str: string, max: number): string {
  return str.length > max ? str.slice(0, max) + '…' : str
}

export async function copyToClipboard(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    const ok = document.execCommand('copy')
    document.body.removeChild(ta)
    return ok
  }
}

export const SIZE_LABELS: Record<string, { label: string; desc: string; specs: string; rate: string }> = {
  small:  { label: 'Starter',  desc: 'Light development',  specs: '2 vCPU · 4 GB RAM',  rate: '$0.15/hr' },
  medium: { label: 'Standard', desc: 'General workloads',  specs: '4 vCPU · 8 GB RAM',  rate: '$0.35/hr' },
  large:  { label: 'Pro',      desc: 'Heavy computation',  specs: '8 vCPU · 16 GB RAM', rate: '$0.70/hr' },
  gpu:    { label: 'GPU',      desc: 'ML & AI training',   specs: 'GPU · 16 GB VRAM',   rate: '$3.50/hr' },
}

export const REGION_LABELS: Record<string, { label: string; flag: string }> = {
  nbg1: { label: 'Nuremberg, DE', flag: '🇩🇪' },
  fsn1: { label: 'Falkenstein, DE', flag: '🇩🇪' },
  hel1: { label: 'Helsinki, FI', flag: '🇫🇮' },
  ash:  { label: 'Ashburn, US', flag: '🇺🇸' },
  hil:  { label: 'Hillsboro, US', flag: '🇺🇸' },
}

export const EVENT_TYPES: Record<string, { icon: string; label: string; color: string }> = {
  package_installed: { icon: '📦', label: 'Package Installed', color: 'text-primary' },
  test_failed:       { icon: '❌', label: 'Test Failed',       color: 'text-destructive' },
  test_fixed:        { icon: '✅', label: 'Test Fixed',        color: 'text-primary' },
  pattern_learned:   { icon: '💡', label: 'Pattern Learned',   color: 'text-violet-400' },
  config_changed:    { icon: '⚙️',  label: 'Config Changed',    color: 'text-yellow-400' },
  error_encountered: { icon: '🚨', label: 'Error Encountered', color: 'text-destructive' },
  custom:            { icon: '📝', label: 'Custom Event',      color: 'text-muted-foreground' },
}
