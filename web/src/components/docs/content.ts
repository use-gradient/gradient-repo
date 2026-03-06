/** All docs content as structured markdown, organized by section/page */

export interface DocsSection {
  id: string
  title: string
  pages: DocsPage[]
}

export interface DocsPage {
  id: string
  title: string
  content: string
}

export const docsSections: DocsSection[] = [
  {
    id: 'getting-started',
    title: 'Getting Started',
    pages: [
      {
        id: 'introduction',
        title: 'Introduction',
        content: `# Introduction

Gradient is a platform for **persistent, context-aware cloud development environments**. Every install, every test failure, every learned pattern is saved per branch and shared across your team in real-time.

## What problem does Gradient solve?

Every developer has experienced this: you spin up a new dev environment, and you have to reinstall everything, reconfigure everything, and rediscover every workaround. Gradient eliminates this by giving every branch a **persistent memory**.

### Key capabilities

- **Context Memory** — Every branch remembers installed packages, test failures, learned patterns, and config changes
- **Live Context Mesh** — Multiple environments on the same branch share discoveries in real-time via NATS JetStream
- **Auto Snapshots** — Automatic snapshots every 15 minutes, on git push, and on stop. Never lose work
- **GitHub Auto-Fork** — New branches automatically inherit parent branch context and snapshots
- **Smart Billing** — 20 free hours/month, per-second billing after that, with no hidden fees

## Architecture

\`\`\`
┌─────────┐     ┌──────────┐     ┌──────────────────────┐
│  CLI    │────▶│  API     │────▶│  Cloud Providers     │
│  (gc)   │     │  (Go)    │     │  (Hetzner/AWS/GCP)   │
└─────────┘     └────┬─────┘     └──────────────────────┘
                     │
          ┌──────────┼──────────┐
          ▼          ▼          ▼
     ┌─────────┐ ┌──────┐ ┌────────┐
     │Postgres │ │ NATS │ │ Vault  │
     │(context)│ │(mesh)│ │(secrets)│
     └─────────┘ └──────┘ └────────┘
\`\`\`

## Interfaces

| Interface | Description |
|-----------|-------------|
| CLI (\`gc\`) | Command-line tool for all operations |
| Dashboard | Web UI at \`/dashboard\` |
| REST API | HTTP endpoints at \`/api/v1/*\` |
| MCP Server | JSON-RPC stdio for AI agents (Cursor, Claude) |

## Next steps

- [Quickstart](/docs/getting-started/quickstart) — Get running in 5 minutes
- [Key Concepts](/docs/getting-started/concepts) — Understand environments, context, and mesh
- [CLI Reference](/docs/cli/installation) — Full CLI documentation`,
      },
      {
        id: 'quickstart',
        title: 'Quickstart',
        content: `# Quickstart

Get a Gradient environment running in under 5 minutes.

## Prerequisites

- macOS, Linux, or WSL
- Docker (for local services)
- A Hetzner API token (for cloud environments) or use \`make dev\` for local-only

## 1. Install the CLI

\`\`\`bash
# Build from source
git clone https://github.com/gradient-platform/gradient
cd gradient
make build
sudo cp bin/gc /usr/local/bin/gc
\`\`\`

## 2. Start local services

\`\`\`bash
make dev
# Starts PostgreSQL, NATS, Vault
# Builds binaries
# Runs migrations
# Starts API server on :6767
\`\`\`

## 3. Authenticate

\`\`\`bash
gc auth login
# Opens browser → sign in with Clerk → CLI authorized
\`\`\`

## 4. Create your first context

\`\`\`bash
# Save context for the main branch
gc context save --branch main --os ubuntu-24.04

# Add packages to the context
gc context save --branch main --packages python3=3.12,numpy=1.26.0
\`\`\`

## 5. Create an environment (requires Hetzner token)

\`\`\`bash
gc env create --name my-env --size small --region nbg1
# ✓ Environment created
# ID: env-xxxxx
# Status: creating (ready in ~90s)
\`\`\`

## 6. Watch live events

\`\`\`bash
# In another terminal
gc context live --branch main
# 🔴 Listening for events...
\`\`\`

## 7. Publish a discovery

\`\`\`bash
gc context publish --branch main --type package_installed --key torch --value "2.1.0"
# → Appears instantly in the live listener
\`\`\`

## What's next?

- Explore the [Dashboard](/dashboard) for a visual interface
- Read about [Context Sharing](/docs/guides/context-sharing) for team workflows
- Set up [GitHub Auto-Fork](/docs/guides/github-auto-fork) for automatic branch context`,
      },
      {
        id: 'concepts',
        title: 'Key Concepts',
        content: `# Key Concepts

## Environments

A Gradient environment is a Docker container running on a cloud server (Hetzner, AWS, or GCP). Each environment has:

- A **name** for identification
- A **size** determining resources (small/medium/large/gpu)
- A **region** for geographic placement
- An optional **context branch** linking it to a branch's memory

### Lifecycle

\`creating\` → \`running\` → \`stopped\`/\`destroyed\`

Environments are billed per second while running, with a 1-minute minimum.

## Context Store

The context store is a per-branch persistent memory in PostgreSQL. It tracks:

- **Installed packages** — name, version, manager, install time
- **Previous failures** — test name, error message, timestamp
- **Attempted fixes** — what was tried, whether it worked
- **Learned patterns** — key-value pairs of discovered knowledge (e.g., "OOM fix" → "reduce batch to 32")
- **Global configs** — environment variables and settings
- **Base OS** — the operating system of the environment

When you create a new branch from \`main\`, the context can be auto-forked so the new branch starts with all of \`main\`'s knowledge.

## Live Context Mesh

The mesh is a real-time event bus powered by NATS JetStream. When one environment discovers something (installs a package, encounters a failure), it publishes an event. All other environments on the same branch receive it instantly.

### Event types

| Type | Description | Example |
|------|-------------|---------|
| \`package_installed\` | A package was installed | torch 2.1.0 |
| \`test_failed\` | A test failed | test_auth: assertion error |
| \`test_fixed\` | A test was fixed | test_auth: fixed |
| \`pattern_learned\` | A pattern was discovered | OOM → reduce batch to 32 |
| \`config_changed\` | Configuration was modified | CUDA_VISIBLE_DEVICES=0,1 |
| \`error_encountered\` | An error occurred | segfault in libcuda.so |
| \`custom\` | Custom event | any key-value |

### Delivery methods

- **SSE** — Server-Sent Events via \`GET /api/v1/events/stream?branch=X\`
- **WebSocket** — Bidirectional via \`ws://host/api/v1/ws?branch=X\`
- **NATS direct** — Direct JetStream subscription for agents

## Snapshots

Snapshots are Docker container diffs saved to a registry (ECR by default, or your own). They capture:

- All filesystem changes since the base image
- Installed packages, compiled binaries, model weights
- Configuration files

### Trigger types

- **Periodic** — Every 15 minutes by the gradient-agent
- **On push** — When a git push is detected via webhook
- **On stop** — Pre-destroy snapshot before environment teardown
- **Manual** — Via \`gc snapshot create\`

## Organizations

Organizations in Gradient map to Clerk organizations. They provide:

- Team membership and role-based access (admin, member)
- Shared billing under one Stripe account
- Isolated context stores per org
- Optional custom container registries`,
      },
    ],
  },
  {
    id: 'cli',
    title: 'CLI Reference',
    pages: [
      {
        id: 'installation',
        title: 'Installation',
        content: `# CLI Installation

## Install from source

\`\`\`bash
git clone https://github.com/gradient-platform/gradient
cd gradient
make build
sudo cp bin/gc /usr/local/bin/gc
\`\`\`

## Verify installation

\`\`\`bash
gc --version
gc auth status
\`\`\`

## Shell completion

\`\`\`bash
# Bash
gc completion bash > /etc/bash_completion.d/gc

# Zsh
gc completion zsh > "\${fpath[1]}/_gc"

# Fish
gc completion fish > ~/.config/fish/completions/gc.fish
\`\`\`

## Configuration

The CLI stores configuration in \`~/.gradient/config.json\`:

\`\`\`json
{
  "token": "your-jwt-token",
  "api_url": "http://localhost:6767",
  "org_id": "org_xxxxx"
}
\`\`\``,
      },
      {
        id: 'auth',
        title: 'gc auth',
        content: `# gc auth

Authentication commands for the Gradient CLI.

## gc auth login

Opens your browser to sign in via Clerk. After authentication, the CLI stores your JWT token locally.

\`\`\`bash
gc auth login
# Opens browser → sign in → CLI authorized
\`\`\`

## gc auth status

Show current authentication status.

\`\`\`bash
gc auth status
# Status:       ✓ logged in
# Name:         Vin Vadoothker
# Email:        vinvadoothker@gmail.com
# API URL:      http://localhost:6767
# Active Org:   org_3AVtyCog7U59jeGJMDaxWgyWP3K
\`\`\`

### Verbose mode

\`\`\`bash
gc auth status -v
# Includes environment count, billing info, mesh health
\`\`\`

## gc auth logout

Clear local credentials.

\`\`\`bash
gc auth logout
\`\`\``,
      },
      {
        id: 'env',
        title: 'gc env',
        content: `# gc env

Environment management commands.

## gc env create

Create a new cloud development environment.

\`\`\`bash
gc env create --name <name> --size <size> --region <region>
\`\`\`

### Flags

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| \`--name\` | Yes | — | Environment name |
| \`--size\` | Yes | — | Size: small, medium, large, gpu |
| \`--region\` | Yes | — | Region: nbg1, fsn1, hel1 |
| \`--branch\` | No | — | Context branch to link |

### Sizes

| Size | vCPU | RAM | Rate |
|------|------|-----|------|
| small | 2 | 4 GB | $0.15/hr |
| medium | 4 | 8 GB | $0.35/hr |
| large | 8 | 16 GB | $0.70/hr |
| gpu | GPU | 16 GB VRAM | $3.50/hr |

### Examples

\`\`\`bash
gc env create --name dev-api --size small --region nbg1
gc env create --name ml-training --size gpu --region nbg1
gc env create --name shared-env --size medium --region hel1 --branch main
\`\`\`

## gc env list

List all environments for the current organization.

\`\`\`bash
gc env list
\`\`\`

## gc env status

Get detailed status of an environment.

\`\`\`bash
gc env status <env-id>
\`\`\`

## gc env ssh

SSH into a running environment.

\`\`\`bash
gc env ssh <env-id>
\`\`\`

## gc env exec

Run a command remotely.

\`\`\`bash
gc env exec <env-id> -- "pip install torch"
gc env exec <env-id> -- "python train.py"
\`\`\`

## gc env logs

View container logs.

\`\`\`bash
gc env logs <env-id>
\`\`\`

## gc env health

Check environment health (CPU, memory, disk, agent status).

\`\`\`bash
gc env health <env-id>
\`\`\`

## gc env destroy

Stop and destroy an environment. Takes a final snapshot, stops billing.

\`\`\`bash
gc env destroy <env-id>
\`\`\`

## gc env autoscale

Manage auto-scaling policies.

\`\`\`bash
# Enable autoscaling
gc env autoscale enable <env-id> --min 1 --max 5 --target-cpu 0.7

# Check status
gc env autoscale status <env-id>

# View scaling history
gc env autoscale history <env-id>

# Disable
gc env autoscale disable <env-id>
\`\`\``,
      },
      {
        id: 'context',
        title: 'gc context',
        content: `# gc context

Context store and live mesh commands.

## gc context save

Save or update context for a branch.

\`\`\`bash
gc context save --branch <branch> [--os <os>] [--commit <sha>]
gc context save --branch main --packages python3=3.12,numpy=1.26.0
gc context save --branch main --failures "test_auth:assertion error"
gc context save --branch main --patterns "retry=exponential backoff"
\`\`\`

### Flags

| Flag | Required | Description |
|------|----------|-------------|
| \`--branch\` | Yes | Git branch name |
| \`--os\` | No | Base OS (ubuntu-24.04, debian-12, alpine-3.19, fedora-40) |
| \`--commit\` | No | Commit SHA |
| \`--packages\` | No | Packages as name=version pairs |
| \`--failures\` | No | Test failures as test:error pairs |
| \`--patterns\` | No | Learned patterns as key=value pairs |

## gc context show

Show full context for a branch.

\`\`\`bash
gc context show --branch main
# Returns JSON with packages, failures, patterns, etc.
\`\`\`

## gc context list

List all branches with context.

\`\`\`bash
gc context list
\`\`\`

## gc context delete

Delete context for a branch.

\`\`\`bash
gc context delete --branch feature/old
\`\`\`

## gc context publish

Publish an event to the live context mesh.

\`\`\`bash
gc context publish --branch main --type <event_type> --key <key> --value <value>
\`\`\`

### Event types

- \`package_installed\` — Package install (key=package, value=version)
- \`test_failed\` — Test failure (key=test name, value=error)
- \`test_fixed\` — Test fix (key=test name, value=fix description)
- \`pattern_learned\` — Pattern discovery (key=pattern name, value=description)
- \`config_changed\` — Config change (key=config key, value=new value)
- \`error_encountered\` — Error (key=error type, value=message)
- \`custom\` — Custom event (any key-value)

## gc context events

Query event history.

\`\`\`bash
gc context events --branch main
gc context events --branch main --types package_installed,test_failed
gc context events --branch main --since 2026-03-04T00:00:00Z
gc context events --branch main --limit 10
\`\`\`

## gc context live

Stream events in real-time via SSE.

\`\`\`bash
gc context live --branch main
# 🔴 Listening for events on branch 'main'...
\`\`\`

## gc context mesh-health

Check the live context mesh health.

\`\`\`bash
gc context mesh-health
# ✓ Status:    ok
#   Bus Type:   nats
#   Connected:  true
#   Messages:   3
\`\`\`

## gc context stats

Show event statistics.

\`\`\`bash
gc context stats
\`\`\``,
      },
      {
        id: 'billing',
        title: 'gc billing',
        content: `# gc billing

Billing and usage commands.

## gc billing status

Show current billing tier, payment status, and free tier usage.

\`\`\`bash
gc billing status
# Billing Status
# ──────────────────────
#   Tier:           free
#   Payment Method: ✗ none
#   Free Hours:     2.50 / 20.00
#   Allowed Sizes:  small
#   Stripe:         ✓ configured
\`\`\`

## gc billing usage

Show usage for the current billing period.

\`\`\`bash
gc billing usage
# Usage Summary (2026-03)
# ─────────────────────────────
#   Small hours:   2.50  ($0.38)
#   Medium hours:  0.00  ($0.00)
#   Large hours:   0.00  ($0.00)
#   GPU hours:     0.00  ($0.00)
# ─────────────────────────────
#   Total:         2.50 hrs  $0.38
\`\`\`

## gc billing setup

Set up Stripe billing for your organization.

\`\`\`bash
gc billing setup --name "My Startup" --email billing@company.com
\`\`\`

This creates a Stripe customer, configures metered subscriptions, and upgrades your org to the paid tier.

## gc billing invoices

List invoices.

\`\`\`bash
gc billing invoices
\`\`\`

## Free Tier Rules

- **20 hours/month** of compute time
- **Starter (small) instances only**
- No credit card required
- Resets on the 1st of each month

## Paid Tier

- All sizes unlocked (small, medium, large, GPU)
- Per-second billing with 1-minute minimum
- Usage reported to Stripe in minute increments
- Invoiced monthly`,
      },
      {
        id: 'other',
        title: 'Other Commands',
        content: `# Other CLI Commands

## gc org

Organization management.

\`\`\`bash
gc org create "My Team"                     # Create org
gc org list                                 # List orgs
gc org switch <org-id>                      # Switch active org
gc org current                              # Show current
gc org members                              # List members
gc org invite user@email.com                # Invite member
gc org invite admin@co.com --role org:admin # Invite as admin
gc org remove <user-id>                     # Remove member
gc org invitations                          # List pending
gc org invitations revoke <id>              # Revoke invite
\`\`\`

### Container Registry

\`\`\`bash
gc org registry get                         # Show current registry
gc org registry set --url ghcr.io/... \\
  --user x --pass y                         # Set custom registry
gc org registry clear                       # Revert to default
\`\`\`

## gc snapshot

\`\`\`bash
gc snapshot list --branch main              # List snapshots
gc snapshot create --env <env-id>           # Manual snapshot
\`\`\`

## gc repo

\`\`\`bash
gc repo connect --repo owner/repo          # Connect repo
gc repo list                               # List connected
gc repo disconnect <repo-id>               # Disconnect
\`\`\`

## gc secret

\`\`\`bash
gc secret sync <env-id> \\
  --keys DB_PASSWORD \\
  --backend vault \\
  --path secret/data/myapp                 # Sync from Vault
\`\`\``,
      },
    ],
  },
  {
    id: 'api',
    title: 'API Reference',
    pages: [
      {
        id: 'authentication',
        title: 'Authentication',
        content: `# API Authentication

The Gradient API requires authentication via JWT tokens from Clerk.

## Base URL

\`\`\`
http://localhost:6767/api/v1
\`\`\`

## Headers

| Header | Required | Description |
|--------|----------|-------------|
| \`Authorization\` | Yes | \`Bearer <jwt-token>\` |
| \`X-Org-ID\` | Optional | Override organization (dev mode) |
| \`Content-Type\` | Yes (POST) | \`application/json\` |

## Getting a token

### Via CLI

\`\`\`bash
gc auth login
TOKEN=$(cat ~/.gradient/config.json | python3 -c "import sys,json; print(json.load(sys.stdin)['token'])")
\`\`\`

### Via Clerk SDK

\`\`\`javascript
import { useAuth } from '@clerk/clerk-react'

const { getToken } = useAuth()
const token = await getToken()
\`\`\`

## Error responses

All errors follow this format:

\`\`\`json
{
  "error": "error_code",
  "message": "Human-readable description"
}
\`\`\`

### Status codes

| Code | Meaning |
|------|---------|
| 400 | Bad request — invalid parameters |
| 401 | Unauthorized — missing or invalid token |
| 402 | Payment required — billing gate |
| 404 | Not found |
| 429 | Rate limited |
| 500 | Internal server error |`,
      },
      {
        id: 'endpoints',
        title: 'Endpoints',
        content: `# API Endpoints

## Health

\`\`\`
GET /api/v1/health
\`\`\`

No auth required. Returns server status.

## Environments

\`\`\`
GET    /api/v1/environments                    # List all
POST   /api/v1/environments                    # Create
GET    /api/v1/environments/:id                # Get details
DELETE /api/v1/environments/:id                # Destroy
GET    /api/v1/environments/:id/health         # Health check
POST   /api/v1/environments/:id/autoscale      # Enable autoscale
GET    /api/v1/environments/:id/autoscale/status    # Autoscale status
GET    /api/v1/environments/:id/autoscale/history   # Scaling history
DELETE /api/v1/environments/:id/autoscale      # Disable autoscale
\`\`\`

### Create environment body

\`\`\`json
{
  "name": "my-env",
  "size": "small",
  "region": "nbg1",
  "context_branch": "main"
}
\`\`\`

## Context

\`\`\`
GET    /api/v1/contexts                        # List all
POST   /api/v1/contexts                        # Save/update
GET    /api/v1/contexts/:branch                # Get by branch
DELETE /api/v1/contexts/:branch                # Delete
\`\`\`

### Save context body

\`\`\`json
{
  "branch": "main",
  "base_os": "ubuntu-24.04",
  "installed_packages": [
    {"name": "python3", "version": "3.12"}
  ],
  "previous_failures": [
    {"test": "test_auth", "error": "assertion error"}
  ],
  "patterns": {
    "oom_fix": "reduce batch to 32"
  }
}
\`\`\`

## Events

\`\`\`
GET    /api/v1/events?branch=main&types=...    # List events
POST   /api/v1/events                          # Publish event
GET    /api/v1/events/stream?branch=main       # SSE stream
GET    /api/v1/events/stats                    # Event stats
GET    /api/v1/mesh/health                     # Mesh health
WS     /api/v1/ws?branch=main                  # WebSocket
\`\`\`

### Publish event body

\`\`\`json
{
  "branch": "main",
  "event_type": "package_installed",
  "data": {"manager": "pip", "name": "torch", "version": "2.1.0"},
  "source_env": "env-xxxx"
}
\`\`\`

## Billing

\`\`\`
GET    /api/v1/billing/usage                   # Usage summary
GET    /api/v1/billing/status                  # Billing status
POST   /api/v1/billing/setup                   # Setup Stripe
GET    /api/v1/billing/invoices                # List invoices
\`\`\`

## Snapshots

\`\`\`
GET    /api/v1/snapshots?branch=main           # List
POST   /api/v1/snapshots                       # Create manual
\`\`\`

## Repos

\`\`\`
GET    /api/v1/repos                           # List connected
POST   /api/v1/repos                           # Connect
DELETE /api/v1/repos/:id                       # Disconnect
POST   /api/v1/webhooks/github                 # GitHub webhook
\`\`\`

## Secrets

\`\`\`
POST   /api/v1/environments/:id/secrets/sync   # Sync secrets
\`\`\``,
      },
    ],
  },
  {
    id: 'guides',
    title: 'Guides',
    pages: [
      {
        id: 'local-dev',
        title: 'Local Development',
        content: `# Local Development Guide

## Prerequisites

- Go 1.21+
- Docker & Docker Compose
- A Clerk account (for auth)
- A Stripe account (for billing, test keys work)

## Quick start

\`\`\`bash
# Clone the repo
git clone https://github.com/gradient-platform/gradient
cd gradient

# Copy env template
cp .env.example .env
# Edit .env with your Clerk and Stripe keys

# Start everything
make dev
\`\`\`

This runs \`scripts/dev-setup.sh\` which:

1. Starts Docker Compose services (PostgreSQL, NATS, Vault)
2. Waits for services to be healthy
3. Runs database migrations
4. Creates Stripe products/prices (if not already done)
5. Builds all binaries
6. Starts the API server on \`:6767\`

## Services

| Service | Port | Description |
|---------|------|-------------|
| API | 6767 | Main HTTP API |
| PostgreSQL | 5432 | Database |
| NATS | 4222 / 8222 | Message bus / monitoring |
| Vault | 8200 | Secrets (optional) |
| Dashboard | 5173 | Vite dev server (optional) |

## Dashboard

\`\`\`bash
# Start the dashboard UI
cd web && npm install && npm run dev
# Available at http://localhost:5173
\`\`\`

## What works without Hetzner

Everything except provisioning cloud servers:

| Feature | Works? |
|---------|--------|
| Auth (login/logout/status) | ✅ |
| Organizations | ✅ |
| Context store | ✅ |
| Live Context Mesh | ✅ |
| Snapshots (metadata) | ✅ |
| Billing | ✅ |
| Environment provisioning | ❌ Needs HETZNER_API_TOKEN |`,
      },
      {
        id: 'context-sharing',
        title: 'Context Sharing',
        content: `# Context Sharing

One of Gradient's core features is real-time context sharing between environments on the same branch.

## How it works

1. **Environment A** on branch \`main\` installs \`torch==2.1.0\`
2. The gradient-agent publishes a \`package_installed\` event to the mesh
3. **Environment B** on branch \`main\` receives the event instantly via SSE/WebSocket
4. Environment B's agent can automatically use this knowledge

## Setting it up

### Save initial context

\`\`\`bash
gc context save --branch main --os ubuntu-24.04
\`\`\`

### Create multiple environments on the same branch

\`\`\`bash
gc env create --name env-1 --size small --region nbg1 --branch main
gc env create --name env-2 --size small --region nbg1 --branch main
\`\`\`

### Watch live events

\`\`\`bash
# In terminal 1
gc context live --branch main

# In terminal 2 — publish an event
gc context publish --branch main --type package_installed --key numpy --value "1.26.0"
# → Appears instantly in terminal 1
\`\`\`

## Context persistence

When you destroy an environment, its context remains in the store. The next environment on the same branch boots with all previous knowledge intact.

\`\`\`bash
gc env destroy <env-1-id>
# Context still exists:
gc context show --branch main
# → Shows all packages, failures, patterns

gc env create --name env-3 --size small --region nbg1 --branch main
# → Boots with full context from main
\`\`\``,
      },
      {
        id: 'github-auto-fork',
        title: 'GitHub Auto-Fork',
        content: `# GitHub Auto-Fork

When you connect a GitHub repository to Gradient, creating a new branch automatically inherits the parent branch's context and snapshot pointers.

## Setup

### 1. Install the Gradient GitHub App

Install the Gradient GitHub App on your repository from the GitHub Marketplace.

### 2. Connect the repo

\`\`\`bash
gc repo connect --repo myorg/myapp
\`\`\`

### 3. Create a branch

\`\`\`bash
git checkout -b feature/new-auth
git push origin feature/new-auth
\`\`\`

### What happens automatically

1. GitHub webhook fires a \`create\` event for the new branch
2. Gradient identifies the parent branch (usually \`main\`)
3. Copies the parent's context (packages, failures, patterns) to the new branch
4. Copies snapshot pointers so the new branch can restore from the parent's state

### How to verify

\`\`\`bash
gc context show --branch feature/new-auth
# Should show inherited context from main
\`\`\``,
      },
      {
        id: 'mcp-agent',
        title: 'MCP / AI Agent Interface',
        content: `# MCP Server — AI Agent Interface

Gradient includes a **Model Context Protocol (MCP)** server that allows AI agents (Cursor, Claude, etc.) to manage environments and context programmatically.

## Starting the MCP server

\`\`\`bash
./bin/gradient-mcp
# Accepts JSON-RPC over stdio
\`\`\`

## Available tools

| Tool | Description |
|------|-------------|
| \`gradient_env_create\` | Create a new environment |
| \`gradient_env_list\` | List environments |
| \`gradient_env_status\` | Get environment status |
| \`gradient_env_destroy\` | Destroy an environment |
| \`gradient_context_save\` | Save branch context |
| \`gradient_context_get\` | Get branch context |
| \`gradient_context_events\` | Query event history |
| \`gradient_context_publish\` | Publish a context event |
| \`gradient_billing_usage\` | Check billing usage |
| \`gradient_snapshot_list\` | List snapshots |
| \`gradient_snapshot_create\` | Create a snapshot |
| \`gradient_secret_sync\` | Sync secrets to an environment |

## Integration with Cursor

Add to your Cursor MCP settings:

\`\`\`json
{
  "mcpServers": {
    "gradient": {
      "command": "/path/to/gradient-mcp",
      "args": [],
      "env": {
        "GRADIENT_API_URL": "http://localhost:6767",
        "GRADIENT_TOKEN": "your-token"
      }
    }
  }
}
\`\`\`

## Request format

\`\`\`json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "gradient_env_list",
    "arguments": {}
  }
}
\`\`\``,
      },
    ],
  },
  {
    id: 'dashboard',
    title: 'Dashboard',
    pages: [
      {
        id: 'environments',
        title: 'Environments',
        content: `# Environments — Dashboard

The **Environments** tab is your central hub for managing cloud development environments.

## Creating an environment

Click **New Environment** to open the creation wizard:

1. **Name** — Give your environment a descriptive name (e.g. \`dev-api\`, \`ml-training\`)
2. **Size** — Choose an instance size:
   - **Starter (small)** — 2 vCPU, 4 GB RAM, $0.15/hr *(free tier eligible)*
   - **Standard (medium)** — 4 vCPU, 8 GB RAM, $0.35/hr
   - **Pro (large)** — 8 vCPU, 16 GB RAM, $0.70/hr
   - **GPU** — GPU with 16 GB VRAM, $3.50/hr
3. **Region** — Select a datacenter region (nbg1, fsn1, hel1)
4. **Branch** — Optionally link to a context branch so the environment starts with full context memory

> **Note:** Free tier users can only create **Starter (small)** instances. Upgrade by adding a payment method in the Billing tab.

## Environment cards

Each running environment shows:

- **Status dot** — Green (running), yellow (creating), red (error)
- **Name and ID** — Click the ID to copy it
- **Size and region** labels
- **Uptime** — How long the environment has been running
- **Actions** — Health check, SSH command, and destroy

## Health panel

Click the health icon on any environment card to expand the health panel showing:

- **CPU** usage percentage with progress bar
- **Memory** usage percentage with progress bar
- **Disk** usage percentage with progress bar
- **Agent version** — The gradient-agent version running inside
- **Container status** — Docker container state

## Destroying an environment

Click the destroy button to:

1. Take a final snapshot (preserving all state)
2. Stop billing
3. Remove the cloud server

Your **context is preserved** — the next environment on the same branch starts with everything the previous one knew.

## CLI equivalents

| Dashboard action | CLI command |
|-----------------|-------------|
| Create environment | \`gc env create --name X --size small --region nbg1\` |
| List environments | \`gc env list\` |
| Check health | \`gc env health <env-id>\` |
| SSH into environment | \`gc env ssh <env-id>\` |
| Destroy environment | \`gc env destroy <env-id>\` |`,
      },
      {
        id: 'context',
        title: 'Context',
        content: `# Context — Dashboard

The **Context** tab lets you view, manage, and interact with your branch-level persistent memory and the live context mesh.

## Branch list

The left panel shows all branches that have context stored. Each entry displays:

- **Branch name** — Click to view its full context
- **Package count** — Number of installed packages tracked
- **Last updated** — When the context was last modified

## Context detail view

Selecting a branch shows its full context:

### Installed packages

A table of all tracked packages with:
- Package name
- Version
- Package manager (pip, npm, apt, etc.)
- Install timestamp

### Previous failures

Test failures recorded on this branch:
- Test name
- Error message
- When it occurred

### Learned patterns

Key-value pairs of knowledge the branch has accumulated. Examples:
- \`oom_fix\` → "Reduce batch_size to 32 when GPU OOMs at 64"
- \`auth_workaround\` → "Add X-Request-ID header for rate limit bypass"

## Live event stream

The **Live** section connects to the real-time context mesh via Server-Sent Events (SSE). You'll see events appear instantly as they happen:

- 🟢 **package_installed** — A package was installed
- 🔴 **test_failed** — A test failed
- ✅ **test_fixed** — A test was fixed
- 💡 **pattern_learned** — A new pattern was discovered
- ⚙️ **config_changed** — Configuration was modified
- ⚠️ **error_encountered** — An error occurred

Each event shows the branch, event type, data payload, source environment, and timestamp.

## Publishing events

Use the **Publish Event** form to manually send events to the mesh:

1. Select the **branch**
2. Choose the **event type**
3. Enter **key** and **value**
4. Click **Publish**

This is useful for testing, manual context updates, or sharing discoveries with your team.

## CLI equivalents

| Dashboard action | CLI command |
|-----------------|-------------|
| List branches | \`gc context list\` |
| View branch context | \`gc context show --branch main\` |
| Save context | \`gc context save --branch main --packages torch=2.1.0\` |
| Watch live events | \`gc context live --branch main\` |
| Publish event | \`gc context publish --branch main --type package_installed --key torch --value 2.1.0\` |
| Event history | \`gc context events --branch main\` |`,
      },
      {
        id: 'billing',
        title: 'Billing',
        content: `# Billing — Dashboard

The **Billing** tab shows your usage, costs, and subscription status.

## Free tier status

The prominent ring chart shows your free tier usage:

- **20 hours/month** included for free
- **Starter (small) instances only** on the free tier
- Resets on the 1st of each month
- Progress ring turns yellow at 75% and red at 90%

## Usage breakdown

A breakdown by instance size showing:
- Hours used per size (small, medium, large, GPU)
- Cost per size
- Total cost for the billing period

## Upgrade banner

If you're on the free tier, an upgrade banner explains:
- What you get by adding a payment method
- All instance sizes unlocked
- No monthly hour limit
- Per-second billing

Click **Set up billing** to configure Stripe for your organization.

## Invoices

A table of past invoices showing:
- Invoice date
- Amount
- Status (paid, pending, draft)
- Link to the Stripe invoice

## Billing setup

When you click **Set up billing**, the system:

1. Creates a Stripe customer for your organization
2. Sets up metered subscriptions for each instance size
3. Upgrades your org from \`free\` to \`paid\` tier
4. Unlocks all instance sizes

## CLI equivalents

| Dashboard action | CLI command |
|-----------------|-------------|
| View billing status | \`gc billing status\` |
| View usage | \`gc billing usage\` |
| Set up billing | \`gc billing setup --name "Company" --email billing@co.com\` |
| View invoices | \`gc billing invoices\` |`,
      },
      {
        id: 'repos',
        title: 'Repos & Snapshots',
        content: `# Repos & Snapshots — Dashboard

The **Repos** tab manages your connected GitHub repositories and environment snapshots.

## Repositories

### Connecting a repo

Click **Connect Repository** and enter the \`owner/repo\` format (e.g. \`myorg/myapp\`). This:

1. Registers the repo with Gradient
2. Sets up webhook listeners for branch events
3. Enables **auto-fork** — new branches automatically inherit parent context

### Connected repos list

Each connected repo shows:
- Repository name (\`owner/repo\`)
- Connection status
- When it was connected
- Actions (disconnect)

### Auto-fork explained

When a developer creates a new branch from \`main\`:

1. GitHub sends a \`create\` webhook to Gradient
2. Gradient copies the parent branch's context (packages, failures, patterns)
3. Gradient copies snapshot pointers
4. The new branch starts with full inherited knowledge

This means feature branches don't start from scratch — they carry forward everything the team has learned.

## Snapshots

Switch to the **Snapshots** sub-tab to see all snapshots:

- **Environment name** — Which environment the snapshot is from
- **Branch** — The linked context branch
- **Trigger** — How the snapshot was created:
  - \`periodic\` — Automatic every 15 minutes
  - \`push\` — On git push
  - \`stop\` — Before environment destruction
  - \`manual\` — Via CLI or dashboard
- **Size** — Snapshot size in MB
- **Created at** — Timestamp

## CLI equivalents

| Dashboard action | CLI command |
|-----------------|-------------|
| Connect repo | \`gc repo connect --repo owner/repo\` |
| List repos | \`gc repo list\` |
| Disconnect repo | \`gc repo disconnect <repo-id>\` |
| List snapshots | \`gc snapshot list --branch main\` |
| Create snapshot | \`gc snapshot create --env <env-id>\` |`,
      },
      {
        id: 'settings',
        title: 'Settings',
        content: `# Settings — Dashboard

The **Settings** tab manages your organization, team members, and CLI/API access.

## Organization

### Current organization

Shows your active organization with:
- Name
- Slug
- Organization ID (copyable)
- Member count
- Creation date
- Your role (Admin or Member)

### Organization list

All organizations you belong to are listed. For each:
- **Active indicator** — Green highlight and "Active" badge for the current org
- **Switch button** — One click to switch to a different org
- **Role** — Shows Admin or Member badge
- **Member count** — How many people are in the org

### Creating an organization

Click **New** or **Create Organization** to open the creation dialog. This uses Clerk's organization management under the hood.

## Members

### Inviting members

Enter an email address and select a role:
- **Member** — Can create environments, view context, use billing
- **Admin** — Full access including org settings, member management, billing setup

### Members list

Shows all current members with:
- Name and email
- Role badge
- Join date

### Pending invitations

Lists outstanding invitations with the option to revoke them.

## CLI & API

### CLI installation

Step-by-step guide to install the \`gc\` CLI:

\`\`\`bash
# Install
curl -fsSL https://get.gradient.dev | sh

# Authenticate
gc auth login

# Verify
gc auth status
\`\`\`

### API access

Shows how to use the REST API with JWT tokens:

\`\`\`bash
TOKEN=$(cat ~/.gradient/config.json | python3 -c "import sys,json; print(json.load(sys.stdin)['token'])")
curl -H "Authorization: Bearer $TOKEN" http://localhost:6767/api/v1/environments
\`\`\`

### MCP server

Information about the AI agent interface for Cursor, Claude, and other LLM tools.

### Quick reference

A compact grid of the most common CLI commands organized by category:
- **Environments**: create, list, ssh
- **Context**: save, show, live
- **Billing**: status, usage, setup
- **Repos**: connect, list

## CLI equivalents

| Dashboard action | CLI command |
|-----------------|-------------|
| List orgs | \`gc org list\` |
| Create org | \`gc org create "Team Name"\` |
| Switch org | \`gc org switch <org-id>\` |
| List members | \`gc org members\` |
| Invite member | \`gc org invite user@email.com\` |`,
      },
    ],
  },
  {
    id: 'billing-docs',
    title: 'Billing',
    pages: [
      {
        id: 'pricing',
        title: 'Pricing',
        content: `# Pricing

## Environment sizes and rates

| Size | Label | vCPU | RAM | Disk | Rate |
|------|-------|------|-----|------|------|
| small | Starter | 2 | 4 GB | 40 GB | $0.15/hr |
| medium | Standard | 4 | 8 GB | 80 GB | $0.35/hr |
| large | Pro | 8 | 16 GB | 160 GB | $0.70/hr |
| gpu | GPU | GPU | 16 GB VRAM | 160 GB | $3.50/hr |

## Billing mechanics

- **Per-second billing** with a **1-minute minimum**
- Usage is reported to Stripe in minute increments (rounded up)
- Billing starts when the environment status becomes \`running\`
- Billing stops when the environment is destroyed

## Free tier

- **20 hours/month** of compute time
- **Starter (small) instances only**
- No credit card required
- Resets on the 1st of each month
- If you exceed 20 hours, you must add a payment method to continue

## Paid tier

Adding a payment method upgrades you to the paid tier:

- All sizes unlocked (small, medium, large, GPU)
- No monthly hour limit
- Monthly invoicing via Stripe
- Usage-based — only pay for what you use`,
      },
    ],
  },
  {
    id: 'reference',
    title: 'Reference',
    pages: [
      {
        id: 'event-types',
        title: 'Event Types',
        content: `# Event Types Reference

All events in the live context mesh follow this schema:

\`\`\`json
{
  "id": "uuid",
  "org_id": "org_xxx",
  "branch": "main",
  "event_type": "package_installed",
  "data": { ... },
  "source_env": "env-xxx",
  "sequence": 1,
  "created_at": "2026-03-05T14:23:01Z"
}
\`\`\`

## Types

### package_installed

\`\`\`json
{"data": {"manager": "pip", "name": "torch", "version": "2.1.0"}}
\`\`\`

### test_failed

\`\`\`json
{"data": {"test": "test_auth", "error": "AssertionError: expected 200 got 401"}}
\`\`\`

### test_fixed

\`\`\`json
{"data": {"test": "test_auth", "fix": "Added auth header"}}
\`\`\`

### pattern_learned

\`\`\`json
{"data": {"key": "oom_fix", "value": "Reduce batch_size to 32 when GPU OOMs at 64"}}
\`\`\`

### config_changed

\`\`\`json
{"data": {"key": "CUDA_VISIBLE_DEVICES", "value": "0,1"}}
\`\`\`

### error_encountered

\`\`\`json
{"data": {"error": "segfault", "details": "libcuda.so.535.129.03"}}
\`\`\`

### custom

\`\`\`json
{"data": {"key": "any", "value": "anything"}}
\`\`\``,
      },
      {
        id: 'error-codes',
        title: 'Error Codes',
        content: `# Error Codes

## HTTP Status Codes

| Code | Error | Description |
|------|-------|-------------|
| 400 | \`bad_request\` | Invalid request parameters |
| 401 | \`unauthorized\` | Missing or invalid auth token |
| 402 | \`payment_required\` | Billing gate — free tier exceeded or payment required |
| 403 | \`forbidden\` | Insufficient permissions |
| 404 | \`not_found\` | Resource not found |
| 409 | \`conflict\` | Resource already exists |
| 429 | \`rate_limited\` | Too many requests — try again later |
| 500 | \`internal_error\` | Server error |

## Billing-specific errors

| Error | Meaning | Resolution |
|-------|---------|------------|
| \`free_tier_exhausted\` | 20 free hours used this month | Add a payment method |
| \`payment_method_required\` | Requested size requires payment | Set up billing |
| \`size_not_allowed\` | Free tier only allows small | Upgrade or use small |
| \`stripe_not_configured\` | Server missing Stripe keys | Configure STRIPE_SECRET_KEY |`,
      },
    ],
  },
]

/** Generate full markdown for all docs (for LLM copy) */
export function generateFullDocsMarkdown(): string {
  const lines: string[] = [
    '# Gradient Documentation',
    '',
    '> Complete documentation for the Gradient platform — cloud dev environments that remember everything.',
    '',
    '---',
    '',
  ]

  for (const section of docsSections) {
    for (const page of section.pages) {
      lines.push(page.content)
      lines.push('')
      lines.push('---')
      lines.push('')
    }
  }

  return lines.join('\n')
}

/** Find a page by section/page ID */
export function findDocsPage(sectionId: string, pageId: string): DocsPage | null {
  const section = docsSections.find(s => s.id === sectionId)
  if (!section) return null
  return section.pages.find(p => p.id === pageId) || null
}

/** Get the default page */
export function getDefaultPage(): { sectionId: string; pageId: string } {
  return { sectionId: 'getting-started', pageId: 'introduction' }
}

/** Get prev/next page for navigation */
export function getAdjacentPages(sectionId: string, pageId: string): { prev: { sectionId: string; pageId: string; title: string } | null; next: { sectionId: string; pageId: string; title: string } | null } {
  const allPages: { sectionId: string; pageId: string; title: string }[] = []
  for (const section of docsSections) {
    for (const page of section.pages) {
      allPages.push({ sectionId: section.id, pageId: page.id, title: page.title })
    }
  }

  const idx = allPages.findIndex(p => p.sectionId === sectionId && p.pageId === pageId)
  return {
    prev: idx > 0 ? allPages[idx - 1] : null,
    next: idx < allPages.length - 1 ? allPages[idx + 1] : null,
  }
}
