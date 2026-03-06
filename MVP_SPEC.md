# Gradient MVP Spec — Brutal & Honest

> **Ship in 6 months with 3–5 engineers. No hand-waving.**

---

## What Gradient Is

The infrastructure platform that AI agents can actually use. Persistent memory across every environment and cloud. Auto-fork on new branches so state is inherited automatically.

**Core Magic:**
1. Every environment captures its full filesystem state (container commit diffs) — automatically, silently
2. New branches inherit that state instantly (auto-fork via GitHub App)
3. AI agents get persistent context (what failed, what fixed it, what's installed) across every session

---

## What's In v1

| Feature | Details |
|---|---|
| **Cloud** | AWS only (GCP month 2) |
| **Infra** | Container-first: Docker on EC2 (not K8s pods) |
| **Capture** | Container commit diffs — `docker commit` on the running container |
| **Snapshots** | Periodic (every 15–30 min via gradient-agent), on push (via GitHub webhook), on stop, manual |
| **Boot** | 90–180s warm (base AMI + pull snapshot image), 3–5 min cold (fresh pull + package install) |
| **Auto-fork** | GitHub App webhook: branch create → copy context + snapshot pointer from parent branch |
| **Context** | Full context store: installed packages, test failures, fixes, patterns, configs — per branch per org |
| **Secrets** | Vault + AWS Secrets Manager sync (thin orchestrator, metadata only) |
| **Auth** | Clerk (org owner + member roles) |
| **Billing** | Stripe per-hour metered: $0.15/hr small, $0.35/hr medium, $0.70/hr large, $3.50/hr GPU |
| **Interfaces** | CLI (`gc`), REST API, MCP server (stdio JSON-RPC) |
| **Dashboard** | None in v1. CLI + API only. |
| **Database** | PostgreSQL (envs, contexts, snapshots, usage, repos, org settings) |

---

## What's Cut

- **GCP**: Month 2. AWS only at launch.
- **Kubernetes**: Replaced with Docker on EC2. Simpler, faster, lower ops debt.
- **Dashboard/Web UI**: No web UI. Not even a status page. CLI or API only.
- **Multi-cloud groups**: No cross-cloud logical grouping.
- **Real-time hooks**: No inotify/eBPF package watchers. Periodic container commit only.
- **Bare metal**: Not in scope.
- **Live migration**: Not in scope.
- **GCP/Infisical secrets**: Vault + AWS Secrets Manager only.

---

## Architecture

```
┌─────────────┐    ┌─────────────┐    ┌─────────────┐
│   CLI (gc)   │    │   REST API  │    │  MCP Server │
│   Go/Cobra   │    │  Go/Mux     │    │  stdio JSONRPC│
└──────┬──────┘    └──────┬──────┘    └──────┬──────┘
       │                  │                   │
       └──────────────────┼───────────────────┘
                          │
                    ┌─────┴─────┐
                    │  API Server │
                    │  (Go)      │
                    └─────┬─────┘
              ┌───────────┼───────────────┐
              │           │               │
        ┌─────┴────┐ ┌───┴────┐  ┌───────┴──────┐
        │ EnvService│ │Context │  │ RepoService  │
        │           │ │Service │  │ (GitHub App) │
        └─────┬────┘ └───┬────┘  └───────┬──────┘
              │           │               │
        ┌─────┴────┐ ┌───┴────┐  ┌───────┴──────┐
        │AWS Provider│ │Postgres│  │SnapshotStore │
        │ EC2+Docker │ │        │  │              │
        └──────────┘ └────────┘  └──────────────┘
```

---

## Container-First Infrastructure (Not K8s)

### Why Not K8s
- 70% of AI agent workflows want a simple shell, not full K8s
- EKS/GKE cluster overhead: 10–15 min just to spin up a cluster
- K8s ops debt with one engineer is suicidal

### What We Actually Do
1. **Pre-baked AMI** (Ubuntu 24.04): Docker engine, SSM agent, gradient-agent pre-installed
2. **EC2 launch**: RunInstances with UserData that pulls base image + starts container
3. **Environment = Docker container** running in privileged mode on EC2 with host networking
4. **SSH access**: Through EC2 instance (host network mode, SSH server in container)
5. **Snapshot**: `docker commit gradient-env $ECR_URI:$TAG && docker push` via SSM RunCommand

### Boot Times (Honest)
| Scenario | Time | How |
|---|---|---|
| **Warm boot** (snapshot exists) | 90–180s | EC2 launch (~60s) + pull ECR image (~30–60s) + start container (~5s) |
| **Cold boot** (no snapshot) | 3–5 min | EC2 launch (~60s) + pull ubuntu:24.04 (~30s) + package install (2–4 min) |
| **Instant re-attach** (env still running) | <5s | Just reconnect SSH |

---

## Auto-Fork: The Killer Feature

### How It Works
1. User installs Gradient GitHub App on their repo
2. User runs `gc repo connect --repo owner/repo` → links repo to Gradient org
3. When a new branch is created:
   - GitHub webhook fires `create` event
   - Gradient copies parent branch's **context** (installed packages, test failures, patterns) to new branch
   - Gradient creates a **snapshot pointer** on new branch pointing to parent's latest snapshot image
4. When `gc env create --context-branch feature/new-algo` is run:
   - API finds the snapshot for that branch → launches EC2 with that image
   - Environment boots with all the parent's installed packages already there

### What Gets Forked (Cheap — No Image Copy)
- **Context record** (DB copy): installed packages, test failures, attempted fixes, patterns
- **Snapshot pointer** (DB record pointing to same ECR image): no data duplication

### GitHub App Permissions Needed
- **Repository events**: `create` (branch), `push`, `delete`
- **Metadata**: Read-only

### Webhook Events Handled
| Event | Action |
|---|---|
| `installation.created` | Store installation + repo list |
| `installation.deleted` | Remove installation + repo connections |
| `create` (branch) | Auto-fork context + snapshot from parent branch |
| `push` | Update context commit SHA, trigger auto-snapshot if env running |
| `delete` (branch) | Log for audit (preserve context/snapshots for history) |

---

## Capture Mechanism: Container Commit Diffs

### Why Not dpkg/apt/pip Tracking
- Misses custom installs, compiled binaries, config file changes
- Misses `curl | bash` installs
- Misses language-specific global installs (cargo install, go install, etc.)
- Fundamentally 2018 thinking

### What We Actually Do
- **Container runs in Docker**: All filesystem changes are captured in the container layer
- **`docker commit`**: Creates a new image from the container's current state — everything included
- **Push to ECR**: Versioned, immutable snapshots stored in AWS ECR

### Snapshot Triggers
| Trigger | Mechanism |
|---|---|
| **Periodic** (every 15–30 min) | gradient-agent cron on EC2 instance |
| **On push** (git push to connected branch) | GitHub webhook → SSM RunCommand |
| **On stop** (env destroy) | Pre-destroy hook in env service |
| **Manual** | `gc snapshot create --env <id>` or API call |

### What's Captured
- Every installed package (apt, pip, npm, cargo, go, rustup, custom)
- Every config file change (/etc/*, dotfiles, ~/.bashrc, etc.)
- Every global tool install
- PATH modifications
- Language runtime versions
- Everything in the container filesystem

---

## Context Store

### What's Stored Per Branch Per Org
```json
{
  "branch": "feature/new-algo",
  "commit_sha": "abc123",
  "installed_packages": [
    {"manager": "apt", "name": "libssl-dev", "version": "3.0.13"},
    {"manager": "pip", "name": "torch", "version": "2.1.0"},
    {"manager": "custom", "name": "ripgrep", "source": "cargo install"}
  ],
  "previous_failures": [
    {"test": "test_model_convergence", "error": "OOM at batch_size=64", "commit": "def456"}
  ],
  "attempted_fixes": [
    {"fix": "reduced batch_size to 32", "success": true, "commit": "ghi789"}
  ],
  "patterns": {
    "gpu_oom_threshold": "batch_size > 48 on t3.medium causes OOM"
  },
  "global_configs": {
    "CUDA_VISIBLE_DEVICES": "0"
  },
  "base_os": "ubuntu-24.04"
}
```

### Context Is Separate From Snapshots
- **Context** = structured metadata (JSON in Postgres). Fast to query. Used by AI agents.
- **Snapshot** = full filesystem state (Docker image in ECR). Used for environment restore.
- Both fork independently on new branches.

---

## Pricing

| Size | vCPU | RAM | Hourly Rate |
|---|---|---|---|
| Small | 2 | 4 GB | $0.15/hr |
| Medium | 4 | 8 GB | $0.35/hr |
| Large | 8 | 16 GB | $0.70/hr |
| GPU | 8 + GPU | 16 GB | $3.50/hr |

### Billing Model
- **Per-org metered, billed per second (minimum 1 minute)**
- Org owner gets billed (Stripe card on file)
- Members have full permissions, usage billed to org
- Usage tracked per-second, displayed per-hour
- Stripe invoices at end of billing period

### Free Tier
- **20 free hours/month** per org, "small" environments only
- No payment method required for free tier
- **Hard limit**: after 20 hours, must add payment method to continue
- Free tier cannot create medium, large, or GPU environments
- Upgrade: `gc billing setup --email owner@example.com --name "Org Name"`
- Check status: `gc billing status`

---

## Org Model

- **Clerk** handles auth, user management, org management
- **Owner**: One per org. Gets billed. Manages Stripe payment method.
- **Members**: Full permissions. Everything they run is billed to the org.
- User can be member of multiple orgs. `gc org switch <org>` to switch active org.

---

## Database Schema (PostgreSQL)

| Table | Purpose |
|---|---|
| `environments` | Env metadata: name, org, provider, region, size, status, EC2 instance ID |
| `contexts` | Branch context: packages, failures, fixes, patterns (JSONB) |
| `snapshots` | Container snapshot records: branch, image ref, type, parent lineage |
| `usage_events` | Billing usage: start/stop times, billed seconds, size |
| `org_settings` | Stripe customer ID, subscription ID, owner info |
| `secret_syncs` | Secret sync metadata: which secrets synced to which envs |
| `github_installations` | Raw GitHub App installation data from webhooks |
| `repo_connections` | Links GitHub repo → Gradient org for auto-fork |

---

## CLI Commands

```bash
# Auth
gc auth login
gc auth logout
gc auth status

# Environments
gc env create --name my-env --region us-east-1 --size medium --context-branch main
gc env list
gc env status --id <env-id>
gc env destroy --id <env-id>

# Context
gc context sync --branch main
gc context show --branch main
gc context list
gc context delete --branch feature/old

# Snapshots
gc snapshot create --env <env-id> --tag v1.0
gc snapshot list --branch main

# Repos (GitHub auto-fork)
gc repo connect --repo owner/repo
gc repo list
gc repo disconnect --id <conn-id>

# Secrets
gc secret sync --env <env-id> --key DB_PASSWORD --backend vault --path secret/db

# Billing
gc billing usage --month 2026-03
gc billing invoices
gc billing setup --email owner@example.com --name "My Org"

# Org
gc org list
gc org switch <org-id>
```

---

## MCP Tools (AI Agent Interface)

| Tool | Description |
|---|---|
| `gradient_env_create` | Create environment with optional context branch + snapshot restore |
| `gradient_env_list` | List active environments |
| `gradient_env_destroy` | Destroy environment |
| `gradient_env_status` | Get environment status |
| `gradient_env_snapshot` | Take container commit snapshot |
| `gradient_context_get` | Get branch context (packages, failures, patterns) |
| `gradient_context_save` | Save/update branch context |
| `gradient_repo_connect` | Connect GitHub repo for auto-fork |
| `gradient_repo_list` | List connected repos |
| `gradient_snapshot_list` | List snapshots for a branch |
| `gradient_billing_usage` | Get billing usage summary |
| `gradient_secret_sync` | Sync secret to environment |

---

## The 90-Second Demo

```bash
# 1. Connect GitHub repo
$ gc repo connect --repo acme/ml-pipeline
✓ Connected. Auto-fork enabled.

# 2. Create environment on main branch
$ gc env create --name ml-dev --region us-east-1 --size medium --context-branch main
✓ Environment creating... (ID: abc-123)

# 3. [Work happens: install packages, run tests, fix bugs]
# Container automatically captures everything via docker commit

# 4. New branch created on GitHub: feature/new-algo
# → Gradient auto-forks context + snapshot from main

# 5. AI agent creates env for the new branch
$ gc env create --name new-algo-env --region us-east-1 --context-branch feature/new-algo
✓ Environment creating from snapshot...
✓ All packages from main already installed
✓ AI agent sees: "last time test_model_convergence failed with OOM at batch_size=64,
   fix was reducing to 32"

# 6. Check billing
$ gc billing usage
Small:  0.0 hrs  ($0.00)
Medium: 4.2 hrs  ($1.47)
Total:  $1.47
```

---

## Infrastructure Requirements (AWS)

### Pre-baked AMI (build once)
- Ubuntu 24.04
- Docker Engine
- AWS SSM Agent (for snapshot commands)
- SSH server

### AWS Resources Needed
| Resource | Purpose |
|---|---|
| EC2 AMI | Pre-baked base image |
| ECR Repository | Store container snapshots |
| VPC + Subnet | Network for EC2 instances |
| Security Group | Allow SSH (port 22) inbound |
| IAM Instance Profile | EC2 → SSM + ECR permissions |
| IAM User/Role | API server → EC2 + SSM + ECR permissions |

### GitHub App Setup
1. Create GitHub App at https://github.com/settings/apps
2. Set webhook URL: `https://your-api-domain/api/v1/webhooks/github`
3. Subscribe to events: `Create`, `Push`, `Delete`, `Installation`
4. Set permissions: Repository metadata (read), Contents (read)
5. Generate webhook secret, set as `GITHUB_APP_WEBHOOK_SECRET`

---

## Timeline (6 months, 3–5 engineers)

| Month | Milestone |
|---|---|
| **1** | Core API + CLI + DB. EC2 provider (create/destroy). Context store. Dev mode auth. |
| **2** | Container snapshots (docker commit + ECR). Snapshot restore on env create. Stripe billing. |
| **3** | GitHub App integration. Auto-fork on branch create. Webhook handling. |
| **4** | Clerk auth integration. MCP server. Periodic snapshot agent. |
| **5** | Hardening: error handling, retries, cleanup. Load testing. Security audit. |
| **6** | GCP provider (month 2 of GCP). Documentation. Beta launch. |

---

## What We're NOT Building

- Container orchestration (we just run Docker on EC2)
- A cloud provider (we use AWS)
- A CI/CD system (use GitHub Actions for that)
- A code editor (use Cursor/VS Code)
- A Kubernetes distribution
- A VM manager
- A PaaS

We're building **the memory layer between environments**: capture everything, fork it on new branches, replay it when needed. That's it.
