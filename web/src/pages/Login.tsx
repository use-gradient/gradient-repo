import { SignIn } from '@clerk/clerk-react'
import { Link } from 'react-router-dom'

export default function Login() {
  return (
    <div className="min-h-screen bg-background flex flex-col">
      <nav className="h-14 border-b border-border flex items-center px-6" aria-label="Site navigation">
        <Link to="/" className="flex items-center gap-2.5 text-foreground hover:text-primary transition-colors">
          <img src="/logo.svg" alt="Gradient" className="h-7 w-auto" />
          <span className="text-sm font-semibold">Gradient</span>
        </Link>
      </nav>
      <main className="flex-1 flex items-center justify-center p-4">
        <div className="w-full max-w-sm">
          <SignIn
            routing="path"
            path="/login"
            signUpUrl="/signup"
            afterSignInUrl="/dashboard"
          />
        </div>
      </main>
    </div>
  )
}
