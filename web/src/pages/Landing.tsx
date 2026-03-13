import { Link } from 'react-router-dom'
import { SignedIn, SignedOut } from '@clerk/clerk-react'
import {
  ArrowRight, Check, Plug, Bot, GitBranch, Tag, Eye,
  BookOpen, Zap, ChevronRight,
} from 'lucide-react'
import { Button, Badge, Separator } from '@/components/ui'

const setupSteps = [
  {
    num: '1',
    icon: Plug,
    title: 'Connect your tools',
    desc: 'Link your Linear workspace, add your Claude Code API key (or use our free tier), and connect a GitHub repo. Takes about 2 minutes.',
    visual: (
      <div className="flex items-center gap-3 mt-4">
        {['Linear', 'Claude Code', 'GitHub'].map(tool => (
          <div key={tool} className="flex items-center gap-1.5 px-3 py-1.5 rounded-full bg-primary/10 border border-primary/20 text-xs font-medium text-primary">
            <Check className="h-3 w-3" /> {tool}
          </div>
        ))}
      </div>
    ),
  },
  {
    num: '2',
    icon: Tag,
    title: 'Add issues in Linear with the gradient-agent label',
    desc: 'Create issues in Linear like you normally would. Add the "gradient-agent" label to any issue you want the AI to pick up and work on.',
    visual: (
      <div className="mt-4 rounded-lg border border-border bg-card/50 p-4 space-y-3">
        <div className="flex items-center gap-3">
          <div className="h-4 w-4 rounded border border-primary/40 bg-primary/10" />
          <span className="text-sm text-foreground">Add dark mode to settings page</span>
          <Badge className="ml-auto text-[10px]">gradient-agent</Badge>
        </div>
        <div className="flex items-center gap-3">
          <div className="h-4 w-4 rounded border border-primary/40 bg-primary/10" />
          <span className="text-sm text-foreground">Fix login redirect on mobile</span>
          <Badge className="ml-auto text-[10px]">gradient-agent</Badge>
        </div>
        <div className="flex items-center gap-3 opacity-40">
          <div className="h-4 w-4 rounded border border-border" />
          <span className="text-sm text-muted-foreground">Update brand guidelines</span>
        </div>
      </div>
    ),
  },
  {
    num: '3',
    icon: Eye,
    title: 'Watch it work',
    desc: 'Gradient picks up labeled issues, spins up a cloud environment, writes the code, runs tests, and opens a pull request — all automatically.',
    visual: (
      <div className="mt-4 rounded-lg border border-border bg-card/50 p-4">
        <div className="space-y-2.5 text-xs font-mono text-muted-foreground">
          <div className="flex items-center gap-2">
            <span className="text-emerald-400">●</span>
            <span>Issue picked up: <span className="text-foreground">Add dark mode to settings</span></span>
          </div>
          <div className="flex items-center gap-2">
            <span className="text-emerald-400">●</span>
            <span>Branch created: <span className="text-primary">feature/dark-mode</span></span>
          </div>
          <div className="flex items-center gap-2">
            <span className="text-emerald-400">●</span>
            <span>Agent coding... <span className="text-foreground">14 files changed</span></span>
          </div>
          <div className="flex items-center gap-2">
            <span className="text-emerald-400">●</span>
            <span>Tests passing: <span className="text-emerald-400">12/12</span></span>
          </div>
          <div className="flex items-center gap-2">
            <span className="text-violet-400">●</span>
            <span>PR opened: <span className="text-primary">#247</span></span>
          </div>
        </div>
      </div>
    ),
  },
]

export default function Landing() {
  return (
    <div className="min-h-screen bg-background">
      <a href="#main-content" className="skip-link">Skip to main content</a>

      {/* ── Navbar ── */}
      <nav className="sticky top-0 z-50 w-full border-b border-border/50 bg-background/95 backdrop-blur supports-[backdrop-filter]:bg-background/60" aria-label="Site navigation">
        <div className="mx-auto flex h-14 max-w-screen-xl items-center justify-between px-4 sm:px-6 lg:px-8">
          <Link to="/" className="flex items-center gap-2.5">
            <img src="/logo.svg" alt="Gradient" className="h-7 w-auto" />
            <span className="text-sm font-bold text-foreground">Gradient</span>
          </Link>
          <div className="hidden md:flex items-center gap-6 text-sm text-muted-foreground">
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
          <div className="absolute inset-0 -z-10">
            <div className="absolute inset-0 bg-[radial-gradient(ellipse_80%_50%_at_50%_-20%,hsl(172_26%_48%/0.15),transparent)]" />
          </div>

          <div className="mx-auto max-w-screen-xl px-4 sm:px-6 lg:px-8 pb-16 pt-24 sm:pt-32 md:pt-40">
            <div className="mx-auto max-w-3xl text-center">
              <Badge variant="default" className="mb-6 gap-1.5">
                <Zap className="h-3 w-3" /> Now in public beta
              </Badge>

              <h1 className="text-4xl font-bold tracking-tight text-foreground sm:text-5xl md:text-6xl">
                Add a label in Linear.{' '}
                <span className="bg-gradient-to-r from-primary to-emerald-400 bg-clip-text text-transparent">
                  AI writes the code.
                </span>
              </h1>

              <p className="mx-auto mt-6 max-w-2xl text-lg text-muted-foreground leading-relaxed sm:text-xl">
                Gradient watches your Linear issues for the <code className="px-1.5 py-0.5 rounded bg-primary/10 text-primary text-base font-semibold">gradient-agent</code> label,
                spins up a cloud environment, writes the code, and opens a PR. You review, merge, and move on.
              </p>

              <div className="mt-10 flex flex-col items-center justify-center gap-4 sm:flex-row">
                <SignedOut>
                  <Link to="/signup">
                    <Button size="lg" className="gap-2">
                      Get started free <ArrowRight className="h-4 w-4" />
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
                <a href="#how-it-works">
                  <Button variant="outline" size="lg" className="gap-2">
                    See how it works <ChevronRight className="h-4 w-4" />
                  </Button>
                </a>
              </div>
            </div>
          </div>
        </section>

        {/* ── How it works ── */}
        <section id="how-it-works" className="border-y border-border bg-muted/20 py-24">
          <div className="mx-auto max-w-3xl px-4 sm:px-6 lg:px-8">
            <div className="text-center mb-16">
              <h2 className="text-3xl font-bold tracking-tight text-foreground sm:text-4xl">
                Three steps. That's it.
              </h2>
              <p className="mt-4 text-lg text-muted-foreground">
                Set up once, then just label issues and let the agent handle the rest.
              </p>
            </div>

            <div className="space-y-16">
              {setupSteps.map(step => (
                <div key={step.num} className="flex gap-6">
                  <div className="flex flex-col items-center">
                    <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-primary text-primary-foreground text-sm font-bold">
                      {step.num}
                    </div>
                    {step.num !== '3' && <div className="w-px flex-1 bg-border mt-3" />}
                  </div>
                  <div className="flex-1 min-w-0 pb-2">
                    <div className="flex items-center gap-2 mb-2">
                      <step.icon className="h-5 w-5 text-primary" />
                      <h3 className="text-lg font-semibold text-foreground">{step.title}</h3>
                    </div>
                    <p className="text-sm text-muted-foreground leading-relaxed">{step.desc}</p>
                    {step.visual}
                  </div>
                </div>
              ))}
            </div>

            <div className="mt-16 text-center">
              <SignedOut>
                <Link to="/signup">
                  <Button size="lg" className="gap-2">
                    Start the setup <ArrowRight className="h-4 w-4" />
                  </Button>
                </Link>
              </SignedOut>
              <SignedIn>
                <Link to="/dashboard/get-started">
                  <Button size="lg" className="gap-2">
                    Open setup guide <ArrowRight className="h-4 w-4" />
                  </Button>
                </Link>
              </SignedIn>
            </div>
          </div>
        </section>

        {/* ── What the agent does ── */}
        <section className="mx-auto max-w-screen-xl px-4 sm:px-6 lg:px-8 py-24">
          <div className="mx-auto max-w-2xl text-center mb-16">
            <h2 className="text-3xl font-bold tracking-tight text-foreground sm:text-4xl">
              What happens after you label an issue
            </h2>
          </div>
          <div className="mx-auto max-w-3xl grid gap-4 sm:grid-cols-2">
            {[
              { icon: Bot, title: 'Reads the issue', desc: 'Understands the requirements, context, and acceptance criteria from your Linear issue.' },
              { icon: GitBranch, title: 'Creates a branch', desc: 'Forks from your default branch and sets up a clean working environment.' },
              { title: 'Writes & tests code', desc: 'Uses Claude Code (or our free-tier model) to implement the feature, fix, or refactor.', icon: Zap },
              { title: 'Opens a PR', desc: 'Pushes the changes and opens a pull request linked back to the Linear issue.', icon: ArrowRight },
            ].map(item => (
              <div key={item.title} className="rounded-xl border border-border bg-card p-5 hover:border-primary/30 transition-colors">
                <div className="mb-3 inline-flex h-9 w-9 items-center justify-center rounded-lg bg-primary/10 text-primary">
                  <item.icon className="h-4 w-4" />
                </div>
                <h3 className="text-sm font-semibold text-foreground mb-1.5">{item.title}</h3>
                <p className="text-xs text-muted-foreground leading-relaxed">{item.desc}</p>
              </div>
            ))}
          </div>
        </section>

        {/* ── Pricing ── */}
        <section id="pricing" className="border-y border-border bg-muted/20 py-24">
          <div className="mx-auto max-w-3xl px-4 sm:px-6 lg:px-8">
            <div className="text-center mb-12">
              <h2 className="text-3xl font-bold tracking-tight text-foreground sm:text-4xl">
                Simple pricing
              </h2>
              <p className="mt-4 text-lg text-muted-foreground">
                Start free. No credit card required.
              </p>
            </div>

            <div className="grid gap-6 sm:grid-cols-2">
              {/* Free tier */}
              <div className="rounded-xl border-2 border-primary/30 bg-gradient-to-b from-primary/5 to-transparent p-6">
                <Badge className="mb-3">Free tier</Badge>
                <div className="flex items-baseline gap-1 mb-2">
                  <span className="text-4xl font-bold text-foreground">$0</span>
                  <span className="text-muted-foreground text-sm">/month</span>
                </div>
                <p className="text-sm text-muted-foreground mb-4">
                  Perfect for trying Gradient or small projects.
                </p>
                <ul className="space-y-2.5 text-sm text-muted-foreground">
                  {[
                    'Free AI model (no API key needed)',
                    '20 agent hours / month',
                    'Unlimited Linear issues',
                    'GitHub PR creation',
                  ].map(item => (
                    <li key={item} className="flex items-start gap-2">
                      <Check className="h-4 w-4 text-primary shrink-0 mt-0.5" /> {item}
                    </li>
                  ))}
                </ul>
              </div>

              {/* Pro tier */}
              <div className="rounded-xl border border-border bg-card p-6">
                <Badge variant="secondary" className="mb-3">Pro</Badge>
                <div className="flex items-baseline gap-1 mb-2">
                  <span className="text-4xl font-bold text-foreground">$49</span>
                  <span className="text-muted-foreground text-sm">/month</span>
                </div>
                <p className="text-sm text-muted-foreground mb-4">
                  For teams shipping fast with their own API keys.
                </p>
                <ul className="space-y-2.5 text-sm text-muted-foreground">
                  {[
                    'Bring your own Claude Code API key',
                    'Unlimited agent hours',
                    'Priority queue',
                    'Custom environment sizes',
                  ].map(item => (
                    <li key={item} className="flex items-start gap-2">
                      <Check className="h-4 w-4 text-primary shrink-0 mt-0.5" /> {item}
                    </li>
                  ))}
                </ul>
              </div>
            </div>
          </div>
        </section>

        {/* ── CTA ── */}
        <section className="py-24">
          <div className="mx-auto max-w-2xl px-4 sm:px-6 lg:px-8 text-center">
            <h2 className="text-3xl font-bold tracking-tight text-foreground sm:text-4xl">
              Stop writing boilerplate. Start shipping.
            </h2>
            <p className="mt-4 text-lg text-muted-foreground">
              Connect Linear, label an issue, and let the AI agent handle the implementation.
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
              <Link to="/docs">
                <Button variant="outline" size="lg" className="gap-2">
                  <BookOpen className="h-4 w-4" /> Read the docs
                </Button>
              </Link>
            </div>
          </div>
        </section>
      </main>

      {/* ── Footer ── */}
      <footer className="border-t border-border" role="contentinfo">
        <div className="mx-auto max-w-screen-xl px-4 sm:px-6 lg:px-8 py-12">
          <div className="grid gap-8 sm:grid-cols-2 lg:grid-cols-3">
            <div>
              <div className="flex items-center gap-2 mb-4">
                <img src="/logo.svg" alt="Gradient" className="h-6 w-auto" />
                <span className="font-bold text-foreground">Gradient</span>
              </div>
              <p className="text-sm text-muted-foreground leading-relaxed">
                AI agent that turns Linear issues into pull requests.
              </p>
            </div>
            <div>
              <h4 className="text-sm font-semibold text-foreground mb-4">Product</h4>
              <ul className="space-y-3 text-sm text-muted-foreground">
                <li><a href="#how-it-works" className="transition-colors hover:text-foreground">How it works</a></li>
                <li><a href="#pricing" className="transition-colors hover:text-foreground">Pricing</a></li>
                <li><Link to="/docs" className="transition-colors hover:text-foreground">Documentation</Link></li>
              </ul>
            </div>
            <div>
              <h4 className="text-sm font-semibold text-foreground mb-4">Developers</h4>
              <ul className="space-y-3 text-sm text-muted-foreground">
                <li><Link to="/docs/getting-started/quickstart" className="transition-colors hover:text-foreground">Quickstart</Link></li>
                <li><Link to="/docs/api/authentication" className="transition-colors hover:text-foreground">API Reference</Link></li>
                <li><Link to="/docs/guides/mcp-agent" className="transition-colors hover:text-foreground">MCP / AI Agents</Link></li>
              </ul>
            </div>
          </div>
          <Separator className="my-8" />
          <div className="flex flex-col items-center justify-between gap-4 sm:flex-row text-xs text-muted-foreground">
            <p>&copy; {new Date().getFullYear()} Gradient. All rights reserved.</p>
            <p>Free tier available &middot; No credit card required</p>
          </div>
        </div>
      </footer>
    </div>
  )
}
