import { Routes, Route, Navigate } from 'react-router-dom'
import { SignedIn, SignedOut, RedirectToSignIn, useAuth } from '@clerk/clerk-react'
import { ToastProvider } from '@/components/ui'
import { DashboardShell } from '@/components/layout/DashboardShell'
import Landing from '@/pages/Landing'
import Login from '@/pages/Login'
import Signup from '@/pages/Signup'
import DocsPage from '@/pages/DocsPage'
import Environments from '@/components/dashboard/Environments'
import ContextTab from '@/components/dashboard/ContextTab'
import BillingTab from '@/components/dashboard/BillingTab'
import SettingsTab from '@/components/dashboard/SettingsTab'
import TasksTab from '@/components/dashboard/TasksTab'
import IntegrationsTab from '@/components/dashboard/IntegrationsTab'
import OnboardingWizard from '@/components/dashboard/OnboardingWizard'

const IS_DEV = import.meta.env.DEV

function ProtectedRoute({ children }: { children: React.ReactNode }) {
  if (IS_DEV) return <>{children}</>
  return (
    <>
      <SignedIn>{children}</SignedIn>
      <SignedOut><RedirectToSignIn /></SignedOut>
    </>
  )
}

function LoadingScreen() {
  return (
    <div className="min-h-screen bg-background flex items-center justify-center">
      <div className="flex flex-col items-center gap-3">
        <img src="/logo.svg" alt="Gradient" className="h-10 w-auto animate-pulse" />
        <span className="text-xs text-muted-foreground">Loading…</span>
      </div>
    </div>
  )
}

export default function App() {
  const { isLoaded } = useAuth()

  if (!isLoaded && !IS_DEV) return <LoadingScreen />

  return (
    <ToastProvider>
      <Routes>
        {/* Public pages — redirect to dashboard if signed in */}
        <Route path="/" element={
          IS_DEV ? <Navigate to="/dashboard" replace /> : (
            <>
              <SignedIn><Navigate to="/dashboard" replace /></SignedIn>
              <SignedOut><Landing /></SignedOut>
            </>
          )
        } />
        <Route path="/login/*" element={<Login />} />
        <Route path="/signup/*" element={<Signup />} />
        <Route path="/docs" element={<DocsPage />} />
        <Route path="/docs/*" element={<DocsPage />} />

        {/* Protected dashboard */}
        <Route
          path="/dashboard"
          element={
            <ProtectedRoute>
              <DashboardShell />
            </ProtectedRoute>
          }
        >
          <Route index element={<Navigate to="environments" replace />} />
          <Route path="environments" element={<Environments />} />
          <Route path="context" element={<ContextTab />} />
          <Route path="billing" element={<BillingTab />} />
          <Route path="tasks" element={<TasksTab />} />
          <Route path="integrations" element={<IntegrationsTab />} />
          <Route path="get-started" element={<OnboardingWizard />} />
          <Route path="settings" element={<SettingsTab />} />
        </Route>

        {/* Catch-all */}
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </ToastProvider>
  )
}
