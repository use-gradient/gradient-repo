import { SignUp } from '@clerk/clerk-react'
import { Link } from 'react-router-dom'

export default function Signup() {
  return (
    <div className="min-h-screen bg-background flex flex-col">
      <nav className="h-14 border-b border-border flex items-center px-6" aria-label="Site navigation">
        <Link to="/" className="flex items-center gap-2.5 text-foreground hover:text-primary transition-colors">
          <span className="text-primary text-xl font-bold">◇</span>
          <span className="text-sm font-semibold">Gradient</span>
        </Link>
      </nav>
      <main className="flex-1 flex items-center justify-center p-4">
        <div className="w-full max-w-sm">
          <SignUp
            routing="path"
            path="/signup"
            signInUrl="/login"
            afterSignUpUrl="/dashboard"
          />
        </div>
      </main>
    </div>
  )
}
