import { Link } from 'react-router-dom'
import { SignedIn, SignedOut } from '@clerk/clerk-react'
import {
  Server, Brain, CreditCard, GitBranch, Zap, Shield, Terminal, ArrowRight,
  Check, Clock, Network, Camera, BookOpen,
} from 'lucide-react'
import { Button, CodeBlock, Badge, Separator } from '@/components/ui'

const features = [
  {
    icon: Brain,
    title: 'Context Memory',
    desc: 'Every branch remembers installed packages, test failures, and learned patterns. Fork a branch and the new one inherits everything.',
  },
  {
    icon: Network,
    title: 'Live Context Mesh',
    desc: 'Multiple environments on the same branch share discoveries in real-time via NATS JetStream. Install numpy in one — known everywhere instantly.',
  },
  {
    icon: Camera,
    title: 'Auto Snapshots',
    desc: 'Every 15 minutes, on git push, and on stop. Restore any environment to any point in time. Never lose work.',
  },
  {
    icon: GitBranch,
    title: 'GitHub Auto-Fork',
    desc: "Create a branch in GitHub, Gradient automatically copies the parent context + snapshot pointers. Your new env boots with the parent's state.",
  },
  {
    icon: Shield,
    title: 'Secrets Management',
    desc: 'Sync secrets from HashiCorp Vault directly into your environments. Rotate without restarting.',
  },
  {
    icon: Zap,
    title: 'Smart Billing',
    desc: '20 free hours/month. Per-second billing after that. Only pay for what you use. GPU, CPU, any size.',
  },
]

const steps = [
  { num: '01', title: 'Install & Login', code: 'gc auth login', desc: 'One command to authenticate via your browser.' },
  { num: '02', title: 'Create Environment', code: 'gc env create --name my-env --size small --region nbg1', desc: 'Spin up a cloud dev environment in under 90 seconds.' },
  { num: '03', title: 'Save Context', code: 'gc context save --branch main --packages python3=3.12', desc: 'Start building memory for your branch.' },
  { num: '04', title: 'Share & Collaborate', code: 'gc context live --branch main', desc: 'Watch discoveries from all environments on your branch in real-time.' },
]

const pricing = [
  { size: 'Starter', specs: '2 vCPU · 4 GB', rate: '$0.15', per: 'hour', highlight: false },
  { size: 'Standard', specs: '4 vCPU · 8 GB', rate: '$0.35', per: 'hour', highlight: false },
  { size: 'Pro', specs: '8 vCPU · 16 GB', rate: '$0.70', per: 'hour', highlight: true },
  { size: 'GPU', specs: 'GPU · 16 GB VRAM', rate: '$3.50', per: 'hour', highlight: false },
]

export default function Landing() {
  return (
    <div className="min-h-screen bg-background">
      <a href="#main-content" className="skip-link">Skip to main content</a>

      {/* ── Navbar ── */}
      <nav className="sticky top-0 z-50 w-full border-b border-border/50 bg-background/95 backdrop-blur supports-[backdrop-filter]:bg-background/60" aria-label="Site navigation">
        <div className="mx-auto flex h-14 max-w-screen-xl items-center justify-between px-4 sm:px-6 lg:px-8">
          <Link to="/" className="flex items-center gap-2.5">
            <span className="text-primary text-xl font-bold">◇</span>
            <span className="text-sm font-bold text-foreground">Gradient</span>
          </Link>
          <div className="hidden md:flex items-center gap-6 text-sm text-muted-foreground">
            <a href="#features" className="transition-colors hover:text-foreground">Features</a>
            <a href="#how-it-works" className="transition-colors hover:text-foreground">How it works</a>
            <a href="#pricing" className="transition-colors hover:text-foreground">Pricing</a>
            <Link to="/docs" className="transition-colors hover:text-foreground">Docs</Link>
          </div>
          <div className="flex items-center gap-3">
            <SignedOut>
              <Link to="/login"><Button variant="ghost" size="sm">Sign in</Button></Link>
              <Link to="/signup"><Button size="sm">Get Started</Button></Link>
            </SignedOut>
            <SignedIn>
              <Link to="/dashboard"><Button size="sm">Dashboard <ArrowRight className="ml-1 h-3.5 w-3.5" /></Button></Link>
            </SignedIn>
          </div>
        </div>
      </nav>

      <main id="main-content">
        {/* ── Hero ── */}
        <section className="relative overflow-hidden">
          {/* Background gradient */}
          <div className="absolute inset-0 -z-10">
            <div className="absolute inset-0 bg-[radial-gradient(ellipse_80%_50%_at_50%_-20%,hsl(172_26%_48%/0.15),transparent)]" />
          </div>

          <div className="mx-auto max-w-screen-xl px-4 sm:px-6 lg:px-8 pb-20 pt-24 sm:pt-32 md:pt-40">
            <div className="mx-auto max-w-3xl text-center">
              <Badge variant="default" className="mb-6 gap-1.5">
                <Zap className="h-3 w-3" /> Now in public beta
              </Badge>

              <h1 className="text-4xl font-bold tracking-tight text-foreground sm:text-5xl md:text-6xl lg:text-7xl">
                Cloud environments that{' '}
                <span className="bg-gradient-to-r from-primary to-emerald-400 bg-clip-text text-transparent">
                  remember everything
                </span>
              </h1>

              <p className="mx-auto mt-6 max-w-2xl text-lg text-muted-foreground leading-relaxed sm:text-xl">
                Every install, every fix, every pattern — persisted per branch and shared
                across your team in real-time. Stop re-doing setup. Start where you left off.
              </p>

              <div className="mt-10 flex flex-col items-center justify-center gap-4 sm:flex-row">
                <SignedOut>
                  <Link to="/signup">
                    <Button size="lg" className="gap-2">
                      Start free — 20 hrs/month <ArrowRight className="h-4 w-4" />
                    </Button>
                  </Link>
                </SignedOut>
                <SignedIn>
                  <Link to="/dashboard">
                    <Button size="lg" className="gap-2">
                      Go to Dashboard <ArrowRight className="h-4 w-4" />
                    </Button>
                  </Link>
                </SignedIn>
                <Link to="/docs">
                  <Button variant="outline" size="lg" className="gap-2">
                    <BookOpen className="h-4 w-4" /> Read the docs
                  </Button>
                </Link>
              </div>
            </div>
          </div>
        </section>

        {/* ── Terminal demo ── */}
        <section className="mx-auto max-w-3xl px-4 sm:px-6 lg:px-8 -mt-4 mb-24">
          <div className="rounded-xl border border-border bg-card shadow-2xl shadow-primary/5 overflow-hidden">
            <div className="flex items-center gap-2 px-4 py-3 border-b border-border bg-muted/30">
              <span className="h-3 w-3 rounded-full bg-red-500/60" aria-hidden="true" />
              <span className="h-3 w-3 rounded-full bg-yellow-500/60" aria-hidden="true" />
              <span className="h-3 w-3 rounded-full bg-green-500/60" aria-hidden="true" />
              <span className="ml-3 text-xs text-muted-foreground font-mono">gradient ~</span>
            </div>
            <pre className="p-6 text-[13px] font-mono leading-relaxed text-muted-foreground overflow-x-auto">
              <code>
{`$ gc env create --name ml-training --size gpu --region nbg1
`}<span className="text-emerald-400">✓ Environment created</span>{`
  ID:       env-f8a3b2c1-...
  Status:   creating
  Region:   Nuremberg, DE
  Size:     GPU (16 GB VRAM)

$ gc context save --branch main --packages torch=2.1.0,numpy=1.26.0
`}<span className="text-emerald-400">✓ Context saved for branch: main</span>{`

$ gc context live --branch main
🔴 Listening for events on branch 'main'...
  [14:23:01] `}<span className="text-primary">package_installed</span>{`  torch=2.1.0
  [14:23:05] `}<span className="text-violet-400">pattern_learned</span>{`   oom_fix → "Reduce batch to 32"
  [14:24:11] `}<span className="text-red-400">test_failed</span>{`       test_model → OOM at batch=64`}
              </code>
            </pre>
          </div>
        </section>

        {/* ── Features ── */}
        <section id="features" className="mx-auto max-w-screen-xl px-4 sm:px-6 lg:px-8 py-24">
          <div className="mx-auto max-w-2xl text-center mb-16">
            <h2 className="text-3xl font-bold tracking-tight text-foreground sm:text-4xl">
              Everything your environment forgets,<br />Gradient remembers
            </h2>
            <p className="mt-4 text-lg text-muted-foreground">
              A complete platform for persistent, context-aware cloud development.
            </p>
          </div>
          <div className="grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
            {features.map(f => (
              <div key={f.title} className="group relative rounded-xl border border-border bg-card p-6 transition-all hover:border-primary/30 hover:shadow-lg hover:shadow-primary/5">
                <div className="mb-4 inline-flex h-10 w-10 items-center justify-center rounded-lg bg-primary/10 text-primary group-hover:bg-primary/15 transition-colors">
                  <f.icon className="h-5 w-5" />
                </div>
                <h3 className="text-base font-semibold text-foreground mb-2">{f.title}</h3>
                <p className="text-sm text-muted-foreground leading-relaxed">{f.desc}</p>
              </div>
            ))}
          </div>
        </section>

        {/* ── How it works ── */}
        <section id="how-it-works" className="border-y border-border bg-muted/30 py-24">
          <div className="mx-auto max-w-3xl px-4 sm:px-6 lg:px-8">
            <div className="text-center mb-16">
              <h2 className="text-3xl font-bold tracking-tight text-foreground sm:text-4xl">
                Up and running in 4 commands
              </h2>
              <p className="mt-4 text-lg text-muted-foreground">
                From zero to a fully context-aware cloud environment.
              </p>
            </div>
            <div className="space-y-12">
              {steps.map(step => (
                <div key={step.num} className="flex gap-6">
                  <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full border border-border bg-card text-sm font-mono text-muted-foreground">
                    {step.num}
                  </div>
                  <div className="flex-1 min-w-0 space-y-3">
                    <div>
                      <h3 className="text-base font-semibold text-foreground">{step.title}</h3>
                      <p className="text-sm text-muted-foreground mt-1">{step.desc}</p>
                    </div>
                    <CodeBlock code={step.code} />
                  </div>
                </div>
              ))}
            </div>
          </div>
        </section>

        {/* ── Pricing ── */}
        <section id="pricing" className="mx-auto max-w-screen-xl px-4 sm:px-6 lg:px-8 py-24">
          <div className="mx-auto max-w-2xl text-center mb-16">
            <h2 className="text-3xl font-bold tracking-tight text-foreground sm:text-4xl">
              Simple, transparent pricing
            </h2>
            <p className="mt-4 text-lg text-muted-foreground">
              20 free hours every month. Per-second billing after that. No surprises.
            </p>
          </div>

          <div className="mx-auto max-w-3xl">
            {/* Free tier highlight */}
            <div className="rounded-xl border-2 border-primary/30 bg-gradient-to-b from-primary/5 to-transparent p-8 mb-8">
              <div className="flex flex-col sm:flex-row items-start sm:items-center gap-4">
                <div className="flex h-14 w-14 items-center justify-center rounded-xl bg-primary/10">
                  <Zap className="h-7 w-7 text-primary" />
                </div>
                <div>
                  <div className="flex items-baseline gap-2">
                    <span className="text-4xl font-bold text-foreground">$0</span>
                    <span className="text-muted-foreground">to start</span>
                  </div>
                  <p className="text-sm text-muted-foreground mt-1">20 hours/month · Starter instances · No credit card required</p>
                </div>
              </div>
            </div>

            {/* Pricing grid */}
            <div className="grid sm:grid-cols-2 lg:grid-cols-4 gap-4">
              {pricing.map(p => (
                <div
                  key={p.size}
                  className={`rounded-xl border p-6 text-center transition-all ${
                    p.highlight ? 'border-primary/40 bg-primary/5 shadow-lg shadow-primary/5' : 'border-border bg-card hover:border-border/80'
                  }`}
                >
                  {p.highlight && <Badge className="mb-3">Popular</Badge>}
                  <p className="text-sm font-medium text-muted-foreground">{p.size}</p>
                  <p className="mt-2 text-3xl font-bold text-foreground">
                    {p.rate}<span className="text-sm font-normal text-muted-foreground">/{p.per}</span>
                  </p>
                  <p className="mt-2 text-xs text-muted-foreground">{p.specs}</p>
                </div>
              ))}
            </div>

            {/* Features list */}
            <div className="mt-8 flex flex-wrap items-center justify-center gap-x-6 gap-y-2 text-sm text-muted-foreground">
              {['Per-second billing', '1 minute minimum', 'No hidden fees', 'Cancel anytime'].map(item => (
                <span key={item} className="flex items-center gap-2">
                  <Check className="h-4 w-4 text-primary" /> {item}
                </span>
              ))}
            </div>
          </div>
        </section>

        {/* ── CTA ── */}
        <section className="border-t border-border bg-muted/20 py-24">
          <div className="mx-auto max-w-2xl px-4 sm:px-6 lg:px-8 text-center">
            <h2 className="text-3xl font-bold tracking-tight text-foreground sm:text-4xl">
              Start building with persistent context
            </h2>
            <p className="mt-4 text-lg text-muted-foreground">
              Free tier. No credit card. Full API access. CLI + Dashboard + MCP server for AI agents.
            </p>
            <div className="mt-10 flex flex-col items-center justify-center gap-4 sm:flex-row">
              <SignedOut>
                <Link to="/signup">
                  <Button size="lg" className="gap-2">Get Started Free <ArrowRight className="h-4 w-4" /></Button>
                </Link>
              </SignedOut>
              <SignedIn>
                <Link to="/dashboard">
                  <Button size="lg" className="gap-2">Open Dashboard <ArrowRight className="h-4 w-4" /></Button>
                </Link>
              </SignedIn>
            </div>
          </div>
        </section>
      </main>

      {/* ── Footer ── */}
      <footer className="border-t border-border" role="contentinfo">
        <div className="mx-auto max-w-screen-xl px-4 sm:px-6 lg:px-8 py-12">
          <div className="grid gap-8 sm:grid-cols-2 lg:grid-cols-4">
            <div>
              <div className="flex items-center gap-2 mb-4">
                <span className="text-primary text-lg font-bold">◇</span>
                <span className="font-bold text-foreground">Gradient</span>
              </div>
              <p className="text-sm text-muted-foreground leading-relaxed">
                Cloud dev environments that remember everything.
              </p>
            </div>
            <div>
              <h4 className="text-sm font-semibold text-foreground mb-4">Product</h4>
              <ul className="space-y-3 text-sm text-muted-foreground">
                <li><a href="#features" className="transition-colors hover:text-foreground">Features</a></li>
                <li><a href="#pricing" className="transition-colors hover:text-foreground">Pricing</a></li>
                <li><Link to="/docs" className="transition-colors hover:text-foreground">Documentation</Link></li>
                <li><Link to="/docs/cli/installation" className="transition-colors hover:text-foreground">CLI</Link></li>
              </ul>
            </div>
            <div>
              <h4 className="text-sm font-semibold text-foreground mb-4">Developers</h4>
              <ul className="space-y-3 text-sm text-muted-foreground">
                <li><Link to="/docs/getting-started/quickstart" className="transition-colors hover:text-foreground">Quickstart</Link></li>
                <li><Link to="/docs/api/authentication" className="transition-colors hover:text-foreground">API Reference</Link></li>
                <li><Link to="/docs/guides/mcp-agent" className="transition-colors hover:text-foreground">MCP / AI Agents</Link></li>
                <li><Link to="/docs/llm" className="transition-colors hover:text-foreground">LLM-friendly docs</Link></li>
              </ul>
            </div>
            <div>
              <h4 className="text-sm font-semibold text-foreground mb-4">Company</h4>
              <ul className="space-y-3 text-sm text-muted-foreground">
                <li><a href="https://github.com/gradient-platform" className="transition-colors hover:text-foreground">GitHub</a></li>
                <li><a href="mailto:hello@gradient.dev" className="transition-colors hover:text-foreground">Contact</a></li>
              </ul>
            </div>
          </div>
          <Separator className="my-8" />
          <div className="flex flex-col items-center justify-between gap-4 sm:flex-row text-xs text-muted-foreground">
            <p>&copy; {new Date().getFullYear()} Gradient. All rights reserved.</p>
            <p>Per-second billing · 20 free hours/month · No credit card required</p>
          </div>
        </div>
      </footer>
    </div>
  )
}
