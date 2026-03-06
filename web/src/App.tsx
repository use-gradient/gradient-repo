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
import ReposTab from '@/components/dashboard/ReposTab'
import SettingsTab from '@/components/dashboard/SettingsTab'

function ProtectedRoute({ children }: { children: React.ReactNode }) {
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
        <span className="text-primary text-3xl font-bold animate-pulse">◇</span>
        <span className="text-xs text-muted-foreground">Loading…</span>
      </div>
    </div>
  )
}

export default function App() {
  const { isLoaded } = useAuth()

  if (!isLoaded) return <LoadingScreen />

  return (
    <ToastProvider>
      <Routes>
        {/* Public pages — redirect to dashboard if signed in */}
        <Route path="/" element={
          <>
            <SignedIn><Navigate to="/dashboard" replace /></SignedIn>
            <SignedOut><Landing /></SignedOut>
          </>
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
          <Route path="repos" element={<ReposTab />} />
          <Route path="settings" element={<SettingsTab />} />
        </Route>

        {/* Catch-all */}
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </ToastProvider>
  )
}
