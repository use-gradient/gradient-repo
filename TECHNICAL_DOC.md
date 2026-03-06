# Gradient — Technical Documentation

> **Version**: 0.1.0 (MVP)
> **Last Updated**: March 2026
> **Platform**: Hetzner Cloud (Linux only)
> **Language**: Go 1.25+

---

## Scope & Honest Status

This is v0.1.0. Here's what's real and what's planned:

| Feature | Status | Notes |
|---------|--------|-------|
| Hetzner Cloud environments | ✅ Shipping | Linux Docker containers on Hetzner VPS |
| Warm pool (~5-15s boot) | ✅ Shipping | Default 3 servers, configurable (max 8), idle timeout 30m |
| Snapshots (export + commit) | ✅ Shipping | `docker export` primary, `docker commit` fallback |
| Extra path scanning (CUDA/Nix/conda) | ✅ Shipping | Scanned on every snapshot, stored in context |
| Pre-destroy snapshots | ✅ Shipping | Automatic before teardown |
| Branch context (packages, failures) | ✅ Shipping | PostgreSQL JSONB |
| Auto-fork on branch create | ✅ Shipping | GitHub webhooks |
| Live Context Mesh (NATS) | ✅ Shipping | Single-region, single NATS node |
| CLI (`gc`) | ✅ Shipping | All subcommands |
| MCP server (AI agent tools) | ✅ Shipping | 17 tools via JSON-RPC stdio |
| Stripe billing | ✅ Shipping | Per-hour metered |
| Per-org container registry | ✅ Shipping | Enterprise snapshot isolation via `org_settings` |
| Autoscaling | ✅ Container-only | Adds containers on same server; NO new servers |
| `/metrics` endpoint | ✅ Shipping | Prometheus-compatible |
| Panic recovery middleware | ✅ Shipping | Catches crashes, returns 500 |
| AWS provider | ⚠️ Legacy | Works but not actively tested |
| GCP provider | 🚫 v0.2 | Interface exists, no implementation yet |
| macOS/Windows agent | 🚫 v0.2 | Agent is Linux-only |
| Server-level snapshots | 🚫 v0.2 | Interface exists, not used in v0.1 |
| Server-level autoscaling | 🚫 v0.2 | Container-only in v0.1 |
| Multi-region NATS gateway | 🚫 v0.2 | Config exists but disabled |
| gVisor / Firecracker | 🚫 v0.2+ | Docker + seccomp for now (gVisor prototyped locally) |

### Known Limitations (v0.1)

- **Boot time**: Cold boot is 2-6 minutes. Warm boot is ~5-15s. Not competitive with Daytona (sub-90ms) or E2B (~150ms). This is our biggest weakness. **v0.2 plan**: Firecracker microVMs for sub-second boot.
- **Capture gaps**: `docker export` + `docker commit` captures the filesystem but may miss runtime state (open files, running processes). The agent watches pip/npm/apt changes and scans for CUDA/Nix/conda/custom binaries on every snapshot — but can't auto-replay non-package-managed installs. **v0.2 plan**: Server-level snapshots + CRIU for full state capture.
- **Isolation**: Docker + seccomp + capability dropping. No gVisor or Firecracker. Not enterprise-grade sandbox isolation. **v0.2 will add gVisor** (already prototyped locally). Firecracker in v0.3.
- **Warm pool cost**: We pay for idle warm servers. Default: 3 servers (~$2.40/day at Hetzner CX22 rates). Configurable via `WARM_POOL_MAX_SIZE` (hard cap: 8 = ~$6.40/day max). Idle servers are auto-destroyed after 30 minutes (configurable via `WARM_POOL_IDLE_TIMEOUT`). Customers are billed from assignment, not from server boot — idle pool cost is ours.
- **Single region**: NATS and warm pool are single-region. No cross-region mesh routing. **v0.2 plan**: NATS gateway for multi-region.

### Warm Pool Cost Math

| Pool Size | Hetzner CX22 Cost | Daily Cost | Monthly Cost |
|-----------|-------------------|------------|--------------|
| 1 server | ~$0.03–0.04/hr | ~$0.80/day | ~$24/month |
| 3 servers (default) | ~$0.10/hr | ~$2.40/day | ~$72/month |
| 5 servers | ~$0.17/hr | ~$4.00/day | ~$120/month |
| 8 servers (hard cap) | ~$0.27/hr | ~$6.40/day | ~$192/month |

**Replenish policy**: Every 30s, check each size/region. If `ready < MinReady` and `total < MaxSize` → create servers. If `ready > MinReady` and server idle > `IdleTimeout` → destroy excess. Warming servers stuck >15min → delete (cloud API probably failed).

**Billing boundary**: Customer billing starts at `assigned_at` (when the warm server is given to their environment), NOT at server boot time. All idle pool cost is our cost of doing business.

---

## Table of Contents

1. [System Overview](#1-system-overview)
2. [Architecture](#2-architecture)
3. [Component Inventory](#3-component-inventory)
4. [Database Schema](#4-database-schema)
5. [API Reference](#5-api-reference)
6. [CLI Reference](#6-cli-reference)
7. [MCP Server (AI Agent Interface)](#7-mcp-server-ai-agent-interface)
8. [Live Context Mesh](#8-live-context-mesh)
9. [gradient-agent](#9-gradient-agent)
10. [Cloud Providers](#10-cloud-providers)
11. [Authentication & Authorization](#11-authentication--authorization)
12. [Billing & Usage Tracking](#12-billing--usage-tracking)
13. [Secret Management](#13-secret-management)
14. [Autoscaling](#14-autoscaling)
15. [Rate Limiting](#15-rate-limiting)
16. [GitHub Integration](#16-github-integration)
17. [Infrastructure Setup](#17-infrastructure-setup)
18. [Workflows](#18-workflows)
19. [Configuration Reference](#19-configuration-reference)
20. [Development Setup](#20-development-setup)

---

## 1. System Overview

Gradient is a platform that gives AI coding agents persistent, branch-aware development environments. Every git branch gets its own environment with full memory — installed packages, test failures, fixes, configuration — that persists across sessions and forks automatically to new branches.

**Core Value Proposition**: "The branch has memory."

**Key Capabilities (v0.1 — what actually ships)**:
- **Container-First Infrastructure**: Docker containers on Hetzner Cloud servers with seccomp profiles, capability dropping, and network isolation
- **Warm Pool**: Up to 5 pre-booted servers for ~5-15s environment assignment (vs 2-6min cold boot)
- **Snapshots**: `docker export` (primary) + `docker commit` (fallback) — automatic before destroy
- **Branch-Aware Context**: Per-branch structured metadata (packages, failures, patterns) in PostgreSQL
- **Auto-Fork**: GitHub webhooks copy context + snapshots from parent branches to new branches
- **Live Context Mesh**: Real-time context sharing via NATS JetStream (single region, single node)
- **MCP Interface**: JSON-RPC stdio server for AI agent integration (17 tools)
- **Metered Billing**: Per-hour Stripe usage tracking with automatic invoice generation
- **Autoscaling**: Container-level only — adds containers on the same server (no new server creation)
- **Observability**: `/metrics` endpoint (Prometheus-compatible), panic recovery middleware

---

## 2. Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                      User / AI Agent                        │
│                                                             │
│   gc CLI ←→ API Server ←→ MCP Server (JSON-RPC stdio)      │
└────┬────────────┬───────────────────────────────────────────┘
     │            │
     │    ┌───────┴─────────────────────────────────────┐
     │    │              API Server (Go/Mux)             │
     │    │              :6767                            │
     │    │                                              │
     │    │  ┌─────────┬────────────┬─────────────────┐  │
     │    │  │ EnvSvc  │ CtxSvc    │  BillingSvc      │  │
     │    │  │ OrgSvc  │ RepoSvc   │  AutoscaleSvc    │  │
     │    │  │ SnapSvc │ SecretSync │  RateLimiter     │  │
     │    │  └─────────┴────────────┴─────────────────┘  │
     │    │                                              │
     │    │  ┌──────────────────────────────────────────┐ │
     │    │  │  Live Context Mesh                       │ │
     │    │  │  EventStore (PG) + EventBus (NATS)       │ │
     │    │  │  MeshPublisher (dual write)              │ │
     │    │  └──────────────────────────────────────────┘ │
     │    └───┬──────────┬──────────┬────────────────────┘
     │        │          │          │
     │   ┌────┴───┐ ┌────┴───┐ ┌───┴────────┐
     │   │Postgres│ │ NATS   │ │ Vault      │
     │   │  :5432 │ │ :4222  │ │ :8200      │
     │   └────────┘ └────────┘ └────────────┘
     │
     │   ┌────────────────────────────────────────────────┐
     │   │          Hetzner Cloud Servers                  │
     │   │                                                │
     │   │  ┌──────────────────────────────────────────┐  │
     │   │  │  gradient-agent (:8090)                   │  │
     │   │  │    ├── Periodic snapshots (15m)            │  │
     │   │  │    ├── Health reports → API (1m)           │  │
     │   │  │    ├── NATS subscriber (branch-scoped)     │  │
     │   │  │    ├── Filesystem watcher (10s)            │  │
     │   │  │    └── Context replay on boot              │  │
     │   │  │                                            │  │
     │   │  │  Docker container (gradient-env)           │  │
     │   │  │    └── Development environment             │  │
     │   │  └──────────────────────────────────────────┘  │
     │   └────────────────────────────────────────────────┘
     │
     │   ┌────────────────────────────┐
     │   │  Container Registry        │
     │   │  (Docker Hub / GHCR)       │
     │   │  Per-org or platform-wide  │
     │   └────────────────────────────┘
     │
     │   ┌────────────────────────────┐
     │   │  Stripe                    │
     │   │  Metered billing           │
     │   └────────────────────────────┘
     │
     │   ┌────────────────────────────┐
     │   │  Clerk                     │
     │   │  Auth + Org management     │
     │   └────────────────────────────┘
     │
     └── ┌────────────────────────────┐
         │  GitHub                     │
         │  Webhooks (auto-fork)       │
         └────────────────────────────┘
```

**Data Flow Summary**:

| Flow | Path |
|------|------|
| CLI → API | HTTP REST (JSON) over `:6767` |
| MCP → API | JSON-RPC over stdio → HTTP to API |
| Agent → API | HTTP POST (health, snapshots) |
| Agent ↔ NATS | Pub/sub over `nats://` (Live Context Mesh) |
| API → NATS | Publish events, relay to subscribers |
| API → PostgreSQL | All persistent state (environments, contexts, events, billing, autoscale) |
| API → Stripe | Customer creation, subscription management, usage records |
| API → Clerk | Organization management (members, invites, roles) |
| API → Vault | Secret read/write (KV v2) |
| API → Hetzner | Server lifecycle (create, destroy, snapshot) |
| GitHub → API | Webhooks (branch create, push, delete) |
| Client → API (SSE) | `GET /events/stream` — Server-Sent Events |
| Client ↔ API (WS) | `GET /events/ws` — WebSocket (bidirectional) |

---

## 3. Component Inventory

### Binaries

| Binary | Source | Description |
|--------|--------|-------------|
| `gradient-api` | `cmd/api/main.go` | API server (HTTP :6767) |
| `gc` | `cmd/cli/main.go` | CLI tool for developers/agents |
| `gradient-mcp` | `cmd/mcp/main.go` | MCP server (JSON-RPC stdio) |
| `gradient-agent` | `cmd/agent/main.go` | In-environment daemon (snapshots, health, mesh) |

### Packages

| Package | Path | Description |
|---------|------|-------------|
| `api` | `internal/api/` | HTTP handlers, middleware, auth, rate limiting, WebSocket |
| `config` | `internal/config/` | Environment-based configuration loading |
| `db` | `internal/db/` | PostgreSQL connection pool, auto-migration |
| `models` | `internal/models/` | Core data structures (Environment, Context, Snapshot, etc.) |
| `services` | `internal/services/` | Business logic (EnvService, BillingService, OrgService, AutoscaleService, etc.) |
| `mcp` | `internal/mcp/` | MCP JSON-RPC server implementation |
| `env` | `pkg/env/` | Cloud provider interfaces + implementations (Hetzner, AWS) |
| `context` | `pkg/context/` | Branch context CRUD store |
| `livectx` | `pkg/livectx/` | Live Context Mesh (events, NATS bus, PG store) |
| `secrets` | `pkg/secrets/` | Vault client + secret syncer |

### Scripts

| Script | Description |
|--------|-------------|
| `scripts/dev-setup.sh` | Full local development setup (Docker, DB, NATS, build) |
| `scripts/setup-hetzner-infra.sh` | Creates Hetzner SSH key, firewall, network |
| `scripts/build-hetzner-snapshot.sh` | Builds pre-baked golden image with Docker + agent + tools |
| `scripts/setup-nats-cluster.sh` | Deploys NATS JetStream server with multi-region gateway |
| `scripts/nats-gateway.conf` | NATS configuration template for multi-region mesh |

---

## 4. Database Schema

PostgreSQL with auto-migration on startup via `internal/db/schema.sql`.

### Tables

#### `environments`
```
id              TEXT PRIMARY KEY
name            TEXT NOT NULL
org_id          TEXT NOT NULL
provider        TEXT NOT NULL DEFAULT 'hetzner'
region          TEXT NOT NULL
size            TEXT NOT NULL DEFAULT 'small'
status          TEXT NOT NULL DEFAULT 'creating'
provider_id     TEXT            -- Hetzner server ID or AWS instance ID
ip_address      TEXT
resources       JSONB           -- {cpu, memory_mb, disk_gb}
config          JSONB           -- Arbitrary config, agent health metrics stored here
context_branch  TEXT
created_at      TIMESTAMPTZ
updated_at      TIMESTAMPTZ
```

#### `contexts`
```
id              TEXT PRIMARY KEY
org_id          TEXT NOT NULL
branch          TEXT NOT NULL
commit_sha      TEXT
base_os         TEXT DEFAULT 'ubuntu-24.04'
data            JSONB           -- {installed_packages, test_failures, fixes, patterns, environment_vars}
created_at      TIMESTAMPTZ
updated_at      TIMESTAMPTZ
UNIQUE(org_id, branch)
```

#### `snapshots`
```
id              TEXT PRIMARY KEY
environment_id  TEXT NOT NULL
org_id          TEXT NOT NULL
branch          TEXT
image_ref       TEXT            -- Registry image reference (e.g. registry.example.com/gradient/env-abc:snap-xxx)
trigger         TEXT            -- 'manual', 'periodic', 'on_push', 'on_destroy', 'auto'
size_bytes      BIGINT
created_at      TIMESTAMPTZ
```

#### `repos`
```
id              TEXT PRIMARY KEY
org_id          TEXT NOT NULL
owner           TEXT NOT NULL
name            TEXT NOT NULL
installation_id BIGINT
default_branch  TEXT DEFAULT 'main'
auto_fork       BOOLEAN DEFAULT true
created_at      TIMESTAMPTZ
UNIQUE(org_id, owner, name)
```

#### `usage_events`
```
id              TEXT PRIMARY KEY
environment_id  TEXT NOT NULL
org_id          TEXT NOT NULL
size            TEXT
started_at      TIMESTAMPTZ
stopped_at      TIMESTAMPTZ     -- NULL while running
hours           NUMERIC(10,4)
cost_cents      INTEGER
created_at      TIMESTAMPTZ
```

#### `secret_syncs`
```
id              TEXT PRIMARY KEY
environment_id  TEXT NOT NULL
org_id          TEXT NOT NULL
secret_key      TEXT NOT NULL
backend         TEXT            -- 'vault'
status          TEXT DEFAULT 'synced'
synced_at       TIMESTAMPTZ
```

#### `org_settings`
```
org_id              TEXT PRIMARY KEY
stripe_customer_id  TEXT
stripe_sub_id       TEXT
created_at          TIMESTAMPTZ
updated_at          TIMESTAMPTZ
```

#### `context_events` (Live Context Mesh)
```
id              TEXT PRIMARY KEY (UUID)
org_id          TEXT NOT NULL
branch          TEXT NOT NULL
env_id          TEXT NOT NULL
type            TEXT NOT NULL    -- package_installed, test_failed, pattern_learned, etc.
data            JSONB NOT NULL   -- Event payload
schema_version  INTEGER DEFAULT 1
timestamp       TIMESTAMPTZ NOT NULL
source          TEXT             -- 'agent', 'api', 'cli'
idempotency_key TEXT
sequence        BIGSERIAL        -- Auto-incrementing sequence for cursor-based pagination
acknowledged_at TIMESTAMPTZ
expires_at      TIMESTAMPTZ      -- TTL

UNIQUE(org_id, branch, idempotency_key)  -- Deduplication
```
**Indexes**: org_id+branch+timestamp, org_id+branch+type, org_id+branch+sequence, org_id+branch+env_id, expires_at (TTL cleanup), source, acknowledged_at.

#### `autoscale_policies`
```
id                  TEXT PRIMARY KEY
environment_id      TEXT NOT NULL UNIQUE
org_id              TEXT NOT NULL
min_replicas        INTEGER DEFAULT 1
max_replicas        INTEGER DEFAULT 10
target_cpu          REAL DEFAULT 0.70
target_memory       REAL DEFAULT 0.80
scale_up_threshold  REAL DEFAULT 0.85
scale_down_threshold REAL DEFAULT 0.30
cooldown_secs       INTEGER DEFAULT 300
current_replicas    INTEGER DEFAULT 1
enabled             BOOLEAN DEFAULT true
last_scale_at       TIMESTAMPTZ
last_scale_direction TEXT
created_at          TIMESTAMPTZ
updated_at          TIMESTAMPTZ
```

#### `autoscale_events`
```
id              TEXT PRIMARY KEY
environment_id  TEXT NOT NULL
org_id          TEXT NOT NULL
direction       TEXT NOT NULL    -- 'up' or 'down'
from_replicas   INTEGER
to_replicas     INTEGER
trigger_cpu     REAL
trigger_memory  REAL
created_at      TIMESTAMPTZ
```

---

## 5. API Reference

**Base URL**: `http://localhost:6767/api/v1`

All authenticated endpoints require either:
- `Authorization: Bearer <JWT>` (production, Clerk-issued)
- `X-Org-ID` + `X-User-ID` headers (dev mode, when `CLERK_SECRET_KEY` is unset)

### Unauthenticated Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/v1/health` | Health check → `{status, version, time}` |
| `POST` | `/api/v1/auth/device` | Start device auth flow → `{device_code, user_code, verification_url}` |
| `GET` | `/api/v1/auth/device/poll?code=<code>` | Poll for device auth completion → `{status, token, org_id}` |
| `GET` | `/auth/cli` | Browser auth page (HTML) |
| `POST` | `/auth/cli/approve` | Approve device auth (browser → API) |
| `POST` | `/api/v1/auth/logout` | Server-side logout (audit log, best-effort) |
| `POST` | `/api/v1/webhooks/github` | GitHub webhook receiver (HMAC-verified) |

### Environment Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/environments` | Create environment → `{id, name, status, provider, region, size, ip_address}` |
| `GET` | `/environments` | List all environments for the org |
| `GET` | `/environments/{id}` | Get environment by ID |
| `DELETE` | `/environments/{id}` | Destroy environment (triggers pre-destroy snapshot) |
| `POST` | `/environments/{id}/snapshot` | Manual snapshot → `{snapshot_id, image_ref}` |
| `GET` | `/environments/{id}/ssh-info` | SSH connection details → `{host, user, port, command}` |
| `GET` | `/environments/{id}/health` | Proxy health check to agent → `{cpu, memory, disk, uptime, ...}` |
| `POST` | `/environments/{id}/agent-health` | Receive agent health report (agent → API) |

**Create Environment Request**:
```json
{
  "name": "my-env",
  "provider": "hetzner",      // "hetzner" (default) or "aws"
  "region": "fsn1",            // Hetzner: fsn1, nbg1, hel1, ash, hil
  "size": "small",             // small (CX22), medium (CX32), large (CX42), gpu (CCX33)
  "context_branch": "feature/x" // Optional: replay context from this branch
}
```

**Environment Sizes**:

| Size | Hetzner Type | CPU | Memory | Disk |
|------|-------------|-----|--------|------|
| `small` | CX22 | 2 vCPU | 4 GB | 40 GB |
| `medium` | CX32 | 4 vCPU | 8 GB | 80 GB |
| `large` | CX42 | 8 vCPU | 16 GB | 160 GB |
| `gpu` | CCX33 | 8 vCPU | 32 GB | 240 GB |

### Context Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/contexts` | Save/update context for a branch |
| `GET` | `/contexts` | List all contexts for the org |
| `GET` | `/contexts/{branch}` | Get context for a specific branch |
| `DELETE` | `/contexts/{branch}` | Delete context for a branch |

**Context Data Structure** (JSONB):
```json
{
  "installed_packages": [
    {"manager": "pip", "name": "torch", "version": "2.1.0"}
  ],
  "test_failures": [
    {"test": "test_model", "error": "OOM at batch=64", "fixed_at": null}
  ],
  "fixes": [
    {"issue": "OOM at batch=64", "fix": "reduce batch_size to 32"}
  ],
  "patterns": {
    "oom_fix": "reduce batch to 32",
    "flaky_api_retry": "use exponential backoff"
  },
  "environment_vars": {
    "CUDA_VISIBLE_DEVICES": "0,1"
  }
}
```

### Snapshot Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/snapshots?branch=<branch>` | List snapshots (optionally filtered by branch) |

### Repository Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/repos` | Connect a GitHub repo for auto-fork |
| `GET` | `/repos` | List connected repos |
| `DELETE` | `/repos/{id}` | Disconnect a repo |

### Billing Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/billing/usage?month=YYYY-MM` | Get usage summary for a month |
| `GET` | `/billing/invoices` | List Stripe invoices |
| `POST` | `/billing/setup` | Set up Stripe customer + metered subscription |

### Organization Endpoints (Clerk)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/orgs` | List organizations |
| `GET` | `/orgs/members` | List org members |
| `POST` | `/orgs/invite` | Invite a member `{email, role}` |
| `DELETE` | `/orgs/members/{user_id}` | Remove a member |
| `PATCH` | `/orgs/members/{user_id}/role` | Update member role `{role}` |
| `GET` | `/orgs/invitations` | List pending invitations |
| `POST` | `/orgs/invitations/{id}/revoke` | Revoke an invitation |
| `GET` | `/orgs/settings/registry` | Get org's registry config (or platform default indicator) |
| `PUT` | `/orgs/settings/registry` | Set custom registry `{registry_url, registry_user, registry_pass}` |
| `DELETE` | `/orgs/settings/registry` | Clear custom registry (revert to platform default) |

### Secret Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/secrets/sync` | Sync secret from Vault to environment |

**Request**:
```json
{
  "environment_id": "env-abc",
  "secret_key": "DB_PASSWORD",
  "backend": "vault",
  "backend_path": "secret/data/myapp"
}
```
**Flow**: API reads from Vault → SSH into Hetzner server → `docker exec` to inject env vars into the running container.

### Autoscaling Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `PUT` | `/environments/{id}/autoscale` | Create/update autoscale policy |
| `GET` | `/environments/{id}/autoscale` | Get autoscale policy |
| `DELETE` | `/environments/{id}/autoscale` | Delete autoscale policy |
| `GET` | `/environments/{id}/autoscale/status` | Get current scaling status + metrics |
| `GET` | `/environments/{id}/autoscale/history` | Get scaling event history |
| `GET` | `/autoscale/policies` | List all autoscale policies for the org |

**Autoscale Policy Request**:
```json
{
  "min_replicas": 1,
  "max_replicas": 5,
  "target_cpu": 0.70,
  "target_memory": 0.80,
  "scale_up_threshold": 0.85,
  "scale_down_threshold": 0.30,
  "cooldown_secs": 300,
  "enabled": true
}
```

### Live Context Mesh Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/events` | Publish a context event |
| `GET` | `/events?branch=<b>&type=<t>&limit=<n>&after_seq=<s>` | Query events (cursor pagination) |
| `GET` | `/events/{id}` | Get event by ID |
| `POST` | `/events/{id}/ack` | Acknowledge an event |
| `GET` | `/events/stream?branch=<b>` | **SSE** — real-time event stream |
| `GET` | `/events/ws?branch=<b>` | **WebSocket** — bidirectional real-time events |
| `GET` | `/events/stats?branch=<b>` | Event statistics (counts by type, latest seq) |
| `GET` | `/mesh/health` | Mesh health (bus type, connection, stream info) |

**Publish Event Request**:
```json
{
  "branch": "feature/new-algo",
  "env_id": "env-abc",
  "type": "package_installed",
  "data": {
    "manager": "pip",
    "name": "torch",
    "version": "2.1.0"
  }
}
```

### Admin Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/admin/ratelimit` | Rate limiter stats (tracked IPs, orgs, config) |

---

## 6. CLI Reference

Binary: `gc` (installed to `$GOPATH/bin/gc`)

### `gc auth`

| Command | Description |
|---------|-------------|
| `gc auth login` | Browser-based device auth flow. Flags: `--token` (direct), `--api-url`, `--org` |
| `gc auth logout` | Clear credentials + server-side revocation. Flag: `--force` (skip server call) |
| `gc auth status` | Show auth status, API health, token info. Flag: `-v` (verbose: envs, billing, mesh) |

### `gc env`

| Command | Description |
|---------|-------------|
| `gc env create --name <n> --region <r>` | Create env. Flags: `--provider` (hetzner/aws), `--size`, `--branch` |
| `gc env list` | List all environments |
| `gc env status <id>` | Get environment status |
| `gc env destroy <id>` | Destroy (with pre-destroy snapshot) |
| `gc env ssh <id>` | Get SSH info + auto-connect |
| `gc env health <id>` | Show agent health metrics (CPU, memory, disk, uptime, snapshots, mesh) |
| `gc env logs <id>` | Show container logs. Flag: `--tail <n>` |
| `gc env exec <id> -- <cmd>` | Execute command in container |
| `gc env autoscale <subcommand>` | Manage autoscaling (see below) |

### `gc env autoscale`

| Command | Description |
|---------|-------------|
| `gc env autoscale enable <id>` | Enable autoscaling. Flags: `--min`, `--max`, `--target-cpu`, `--target-memory`, `--cooldown` |
| `gc env autoscale disable <id>` | Disable autoscaling |
| `gc env autoscale status <id>` | Show autoscale status + current metrics |
| `gc env autoscale history <id>` | Show scaling event history. Flag: `--limit` |

### `gc context`

| Command | Description |
|---------|-------------|
| `gc context show --branch <b>` | Show saved context for a branch |
| `gc context list` | List all contexts |
| `gc context save --branch <b>` | Save context data (stdin JSON) |
| `gc context delete --branch <b>` | Delete context for a branch |
| `gc context events --branch <b>` | Query Live Context Mesh events. Flags: `--type`, `--limit`, `--env-id`, `--after-seq` |
| `gc context live --branch <b>` | Live-stream events (SSE). Flags: `--type`, `--env-id` |
| `gc context publish --branch <b> --type <t>` | Publish an event. Flag: `--data` (JSON) |
| `gc context stats --branch <b>` | Event statistics |
| `gc context mesh-health` | Mesh bus health |
| `gc context ws --branch <b>` | WebSocket connection info |

### `gc snapshot`

| Command | Description |
|---------|-------------|
| `gc snapshot list --branch <b>` | List snapshots for a branch |
| `gc snapshot create <env-id>` | Manual snapshot. Flag: `--tag` |

### `gc repo`

| Command | Description |
|---------|-------------|
| `gc repo connect --repo <owner/name>` | Connect GitHub repo for auto-fork |
| `gc repo list` | List connected repos |
| `gc repo disconnect <id>` | Disconnect a repo |
| `gc repo status <id>` | Show repo status |

### `gc billing`

| Command | Description |
|---------|-------------|
| `gc billing usage --month <YYYY-MM>` | Show usage summary |
| `gc billing invoices` | List Stripe invoices |
| `gc billing setup` | Set up Stripe billing for the org |

### `gc org`

| Command | Description |
|---------|-------------|
| `gc org list` | List organizations |
| `gc org switch <id>` | Switch active organization |
| `gc org current` | Show current org |
| `gc org members` | List members |
| `gc org invite --email <e> --role <r>` | Invite member (`admin`, `member`) |
| `gc org remove <user_id>` | Remove member |
| `gc org invitations` | List pending invitations |
| `gc org registry get` | Show current registry (default or custom) |
| `gc org registry set --url <url>` | Set custom container registry (enterprise) |
| `gc org registry clear` | Revert to platform default registry |

---

## 7. MCP Server (AI Agent Interface)

**Binary**: `gradient-mcp`
**Protocol**: JSON-RPC 2.0 over stdio (stdin/stdout)
**Protocol Version**: `2024-11-05`

The MCP server enables AI coding agents (Claude, GPT, etc.) to interact with Gradient programmatically. It proxies all calls to the API server via HTTP.

### Available Tools (17)

| Tool | Description |
|------|-------------|
| `gradient_env_create` | Create a Hetzner environment with size selection and context replay |
| `gradient_env_list` | List all active environments |
| `gradient_env_destroy` | Destroy environment by ID |
| `gradient_env_status` | Get environment status |
| `gradient_env_snapshot` | Take a container commit snapshot |
| `gradient_env_ssh` | Get SSH connection details |
| `gradient_env_autoscale` | Configure horizontal autoscaling (enable, disable, status) |
| `gradient_context_get` | Get saved context for a branch |
| `gradient_context_save` | Save/update context for a branch |
| `gradient_context_events` | Query Live Context Mesh events |
| `gradient_context_publish` | Publish an event to the mesh |
| `gradient_mesh_health` | Check mesh bus health |
| `gradient_repo_connect` | Connect GitHub repo for auto-fork |
| `gradient_repo_list` | List connected repos |
| `gradient_snapshot_list` | List snapshots for a branch |
| `gradient_billing_usage` | Get billing usage summary |
| `gradient_secret_sync` | Sync secret from Vault to environment |

### MCP Server Configuration

Set in the AI agent's config (e.g. `~/.cursor/mcp.json`):
```json
{
  "mcpServers": {
    "gradient": {
      "command": "/path/to/gradient-mcp",
      "args": [],
      "env": {
        "GRADIENT_API_URL": "http://localhost:6767",
        "GRADIENT_TOKEN": "<your-token>",
        "GRADIENT_ORG_ID": "<your-org-id>"
      }
    }
  }
}
```

---

## 8. Live Context Mesh

The Live Context Mesh enables real-time, structured context sharing between all running environments on the same branch. When one agent discovers something (installs a package, encounters a test failure, learns a pattern), all sibling environments receive the information immediately.

### Event Types

| Type | Data Fields | Description |
|------|-------------|-------------|
| `package_installed` | `manager, name, version` | Package was installed |
| `package_removed` | `manager, name` | Package was removed |
| `test_failed` | `test, error, file, line` | Test failure detected |
| `test_fixed` | `test, fix` | Test was fixed |
| `pattern_learned` | `key, value, category` | Pattern or workaround discovered |
| `config_changed` | `key, value, previous_value` | Config/env var changed |
| `dependency_added` | `file, name, version` | Dependency added to manifest |
| `error_encountered` | `error, context, stack_trace` | Error encountered |
| `command_ran` | `command, exit_code, output` | Command executed |
| `file_changed` | `path, action, content_hash` | File modified/created/deleted |
| `custom` | `key, value` | Custom event |

### Event Schema

```json
{
  "id": "uuid-v4",
  "org_id": "org-123",
  "branch": "feature/new-algo",
  "env_id": "env-abc",
  "type": "package_installed",
  "data": {
    "manager": "pip",
    "name": "torch",
    "version": "2.1.0"
  },
  "schema_version": 1,
  "timestamp": "2026-03-03T10:30:00Z",
  "source": "agent",
  "idempotency_key": "sha256-hash-of-org+branch+type+data",
  "sequence": 42
}
```

### NATS Architecture

- **Stream**: `GRADIENT_CTX` with subject filter `ctx.>`
- **Subject Pattern**: `ctx.<org_id>.<branch>` (dots in branch name replaced with `-`)
- **Retention**: 7 days (configurable via `NATS_MAX_AGE`)
- **Max Messages per Subject**: 10,000
- **Max Event Size**: 64 KB
- **Consumer**: Durable, named `gradient-agent-<suffix>`
- **Delivery**: At-least-once with idempotency keys for dedup
- **Deduplication Window**: 5 minutes (NATS-level)
- **Rate Limiting**: 100 events/second per publisher

### Dual-Write Pattern

The `MeshPublisher` ensures every event is both:
1. **Broadcast** to NATS (real-time delivery to all subscribers)
2. **Persisted** to PostgreSQL (durable log for replay, query, audit)

If NATS is unavailable, events are still persisted to PostgreSQL (degraded but not lost).

### Conflict Resolution

Events are modeled as a grow-only, append-only log (CRDT-friendly). Conflicts are resolved by consumers:

| Conflict | Resolution |
|----------|-----------|
| Same package, different versions | Last-write-wins (highest sequence) |
| Contradictory patterns | Both stored with env_id attribution |
| Config conflicts | Both stored; agent applies latest |

### Connection Options

**SSE (Server-Sent Events)**:
```
GET /api/v1/events/stream?branch=feature/x&type=package_installed
Accept: text/event-stream
```

**WebSocket**:
```
GET /api/v1/events/ws?branch=feature/x
Upgrade: websocket
```
WebSocket supports bidirectional: receive events + publish events as JSON frames.

### Local Bus Fallback

When NATS is not configured, a `LocalBus` provides in-process pub/sub with goroutine-based delivery. Events are still persisted to PostgreSQL. This enables development without a NATS server.

**v0.2 plan**: Multi-region NATS gateway (automatic event propagation across regions). CRDT-based conflict resolution for concurrent package installs. Event deduplication with content-addressable hashing.

---

## 9. gradient-agent

The agent is a daemon that runs on each cloud server (host level, not inside the container). It is deployed via cloud-init when the server is created. **The agent is Linux-only for v0.1** — macOS/Windows support is planned for v0.2+ when those platforms are actually supported.

### Responsibilities

1. **Periodic Snapshots** (every 15 minutes):
   - `docker commit gradient-env` → `docker tag` → `docker push` to registry
   - On shutdown: uses container export strategy via API (more reliable than commit)
   - **Extra path scan** on every snapshot: checks for CUDA, Nix, conda, custom binaries, Rust/cargo, Go binaries
   - Reports snapshot + extra paths to API: `POST /api/v1/environments/{id}/snapshot`

2. **Health Reporting** (every 1 minute):
   - Collects CPU, memory, disk usage, uptime (Linux commands only)
   - Reports to API: `POST /api/v1/environments/{id}/agent-health`
   - Metrics stored in environment's `config` JSONB field

3. **Live Context Mesh Participation**:
   - Connects to NATS, subscribes to `ctx.<org_id>.<branch>`
   - **Filesystem Watcher** (every 10 seconds):
     - Detects `pip`, `npm`, `apt` package changes
     - Detects environment variable changes
     - Publishes `package_installed`, `package_removed`, `config_changed` events
   - **Event Handler**: Processes received events:
     - `package_installed` → auto-installs in the container
     - `config_changed` → applies env vars via `/etc/profile.d/gradient-mesh.sh`
     - All events → persisted to `/gradient/context/live.json`

4. **Context Replay on Boot**:
   - On startup, fetches saved context from API: `GET /api/v1/contexts/<branch>`
   - Replays `installed_packages` (installs each via pip/npm/apt)
   - Replays `environment_vars` (sets each env var in the container)

5. **Local HTTP Server** (`:8090`):
   - `GET /health` → detailed system metrics
   - `GET /context` → current `live.json` contents

### Configuration (Environment Variables)

| Variable | Required | Description |
|----------|----------|-------------|
| `GRADIENT_API_URL` | Yes | API server URL |
| `GRADIENT_ENV_ID` | Yes | This environment's ID |
| `GRADIENT_ORG_ID` | Yes | Organization ID |
| `GRADIENT_AUTH_TOKEN` | Yes | Auth token for API calls |
| `GRADIENT_ENV_NAME` | No | Environment name (for tagging) |
| `GRADIENT_BRANCH` | No | Git branch (for mesh scoping) |
| `GRADIENT_REGISTRY_URL` | Yes | Container registry URL |
| `GRADIENT_REGISTRY_USER` | Yes | Registry username |
| `GRADIENT_REGISTRY_PASS` | Yes | Registry password |
| `GRADIENT_SNAPSHOT_INTERVAL` | No | Snapshot interval (default: 15m) |
| `GRADIENT_HEALTH_INTERVAL` | No | Health report interval (default: 1m) |
| `GRADIENT_NATS_URL` | No | NATS URL (enables mesh) |
| `GRADIENT_NATS_AUTH_TOKEN` | No | NATS auth token |
| `GRADIENT_CONTEXT_DIR` | No | Context write dir (default: /gradient/context) |
| `GRADIENT_WATCH_INTERVAL` | No | Filesystem scan interval (default: 10s) |

### Package Detection (v0.1 — Linux only)

The agent detects package changes by diffing snapshots of installed packages:

| Manager | Detection Method |
|---------|-----------------|
| pip | `pip list --format=json` inside container |
| npm | `npm list --json -g` inside container |
| apt/dpkg | `dpkg-query -W -f='${Package}=${Version}\n'` |

**Known limitation**: Custom binaries, Nix, conda, CUDA kernels, font caches, and ldconfig changes are **not** detected by the watcher. They are captured by periodic `docker commit` and pre-destroy `docker export`.

### System Metrics (Linux only)

| Metric | Command |
|--------|---------|
| CPU Usage | `top -bn1 \| head -3 \| grep Cpu \| awk '{print $2}'` |
| Memory Usage | `free \| awk 'NR==2{printf "%.1f", $3/$2*100}'` |
| Disk Usage | `df --output=pcent / \| tail -1 \| tr -d ' %'` |
| Uptime | `cat /proc/uptime \| awk '{print int($1)}'` |

### Supported Package Managers (v0.1 — Context Replay)

When replaying context (auto-installing packages from branch context), the agent supports:

| Manager | Install Command |
|---------|----------------|
| pip | `pip install <pkg>==<version>` |
| npm | `npm install -g <pkg>@<version>` |
| apt | `apt-get install -y <pkg>=<version>` |

**v0.2 plan**: yum, dnf, apk, brew, cargo, go, conda, nix. macOS/Windows system metrics. Server-level snapshot trigger from agent.

---

## 10. Cloud Providers

### Provider Abstraction

All cloud interactions go through generic interfaces defined in `pkg/env/provider.go`. This means adding a new cloud provider (GCP, Azure, bare-metal) requires implementing these interfaces — zero changes to the rest of the codebase.

| Interface | Methods | Purpose | v0.1 Status |
|-----------|---------|---------|-------------|
| `Provider` | `CreateEnvironment`, `DestroyEnvironment`, `GetEnvironmentStatus` | Core lifecycle | ✅ Used |
| `Snapshotter` | `SnapshotEnvironment`, `RestoreFromSnapshot` | Container commit snapshots | ✅ Used |
| `HybridSnapshotter` | `ServerSnapshot`, `ExportContainer` | Server-level + export snapshots | ⚠️ Interface exists, `ExportContainer` used in pre-destroy, `ServerSnapshot` deferred to v0.2 |
| `RemoteExecutor` | `ExecCommand`, `WaitForReady` | Run commands on instances (SSH/SSM/gcloud) | ✅ Used |
| `NetworkInfo` | `GetServerIP` | Get instance IP/hostname | ✅ Used |

Helper functions for safe type assertions: `AsRemoteExecutor()`, `AsNetworkInfo()`, `AsSnapshotter()`, `AsHybridSnapshotter()`.

`EnvService` manages providers via a `map[string]env.Provider` — any provider can be registered at runtime with `RegisterProvider()`. **For v0.1, only Hetzner is actively used.**

### Hetzner Cloud (Primary)

**Implementation**: `pkg/env/hetzner_provider.go`

| Operation | How |
|-----------|-----|
| Create Server | Hetzner API → cloud-init (Docker + agent + security hardening) |
| Destroy Server | Hetzner API delete (or return to warm pool) |
| Snapshot (commit) | SSH → `docker commit` → `docker push` to registry |
| Snapshot (export) | SSH → `docker export` → `docker import` → `docker push` (more reliable) |
| Snapshot (server) | Hetzner Image API → full server-level disk snapshot |
| Restore | Cloud-init pulls snapshot image from registry |
| SSH Exec | `golang.org/x/crypto/ssh` direct connection |

**Cloud-Init Flow**:
1. Install Docker (if not pre-baked image)
2. Install base dev packages (git, curl, build-essential, Python, Node.js, Go)
3. Create isolated bridge network (`gradient-net` — not host network)
4. Write seccomp profile to `/etc/gradient/seccomp.json`
5. Download + install `gradient-agent`
6. Configure agent as systemd service with all env vars
7. Docker login to registry
8. Pull snapshot image (if restoring) or start Ubuntu base container
9. Start container with security hardening (see Security section below)
10. Start `gradient-agent`

**Pre-Baked Image**: `scripts/build-hetzner-snapshot.sh` creates a Hetzner snapshot with Docker, agent, and dev tools pre-installed. New servers boot in seconds instead of minutes.

### AWS (Legacy)

**Implementation**: `pkg/env/aws_provider.go`

Uses EC2 + SSM for command execution. Kept for backward compatibility but Hetzner is the default.

### GCP (Future)

Not implemented for v0.1. The provider abstraction layer means adding GCP Compute Engine support requires implementing the `Provider`, `Snapshotter`, `RemoteExecutor`, and `NetworkInfo` interfaces — no changes to the rest of the codebase.

### Speed & Boot Times

**Warm Pool** (`internal/services/warm_pool.go`): Pre-boots servers and keeps them idle, ready for instant assignment.

| Boot Type | Time | How |
|-----------|------|-----|
| Warm (from pool) | ~5-15s | Pre-booted server → `docker run` → ready |
| Cold (no pool) | ~2-6min | Create server → cloud-init → Docker install → pull image → start |
| Pre-baked warm | ~3-8s | Pre-baked image + warm pool → `docker run` → ready |

**v0.2 plan**: Firecracker microVMs for sub-second boot times. CRIU for instant container restore from checkpoint.

**Boot Time Tracking**: Every environment creation records `boot_time_ms` and `boot_type` (warm/cold) in the environment's config JSONB. The `ip_address` is also stored.

**Warm Pool Configuration** (via environment variables):

| Variable | Default | Description |
|----------|---------|-------------|
| `WARM_POOL_DEFAULT_SIZE` | `3` | Target number of warm servers |
| `WARM_POOL_MAX_SIZE` | `3` | Hard cap (clamped to 0–8 in code) |
| `WARM_POOL_IDLE_TIMEOUT` | `30m` | Destroy idle servers after this |

**Warm Pool Replenish Policy**:
1. `WarmPoolService.replenish()` runs every 30 seconds
2. For each size/region: if `ready < MinReady` and `total < MaxSize` → create new servers
3. Idle cleanup: if `ready > MinReady` and server idle > `IdleTimeout` → destroy excess (saves money)
4. Stale cleanup: servers stuck in "warming" >15 min → delete (cloud API probably failed)
5. Global hard cap: never exceed `WARM_POOL_MAX_SIZE` total servers (absolute ceiling: 8)

**Warm Pool Assignment Flow**:
1. `CreateEnvironment()` calls `AcquireServer()` first — returns instantly if a warm server exists
2. Container starts on warm server via `ExecCommand()` (fast path — server already running)
3. If no warm server available, falls back to cold boot via `provider.CreateEnvironment()`
4. On destroy, server can be returned to warm pool via `ReturnServer()` instead of deleted
5. **Billing**: Customer charged from `assigned_at` — idle pool cost is ours, not theirs

### Capture Reliability (v0.1 — Export + Commit)

**v0.1 uses two capture strategies**. Server-level snapshots (Hetzner Image API) are implemented in code but deferred to v0.2 for simplicity.

| Strategy | Method | Speed | Reliability | Used in v0.1 |
|----------|--------|-------|-------------|--------------|
| `docker commit` | `SnapshotEnvironment()` | Fast (~10s) | Medium — flaky with open files, tmpfs, running processes | ✅ Periodic (every 15min) |
| `docker export` | `ExportContainer()` | Medium (~30s) | High — full filesystem tar, no issues with open files | ✅ Pre-destroy |
| Server snapshot | `ServerSnapshot()` | Slow (~30-120s) | Highest — captures everything including system-level changes | 🚫 v0.2 |

**Pre-Destroy Snapshot** (`EnvService.preDestroySnapshot()`):
1. Try `docker export` via `HybridSnapshotter.ExportContainer()` (most reliable for running containers)
2. Fall back to `docker commit` via `Snapshotter.SnapshotEnvironment()` if export not available
3. Provider doesn't support snapshots → log warning, state is lost

**Extra Path Scanning**: On every snapshot, the agent runs `scanExtraPaths()` which checks for:
- `/usr/local/cuda*` — CUDA toolkit installs
- `/nix`, `/root/.nix-profile` — Nix package manager
- `/opt/conda`, `/root/.conda`, `/root/miniconda*` — Conda environments
- `/usr/local/bin/*` (newer than container start) — Custom compiled binaries
- `/root/.cargo/bin/` — Rust/Cargo binaries
- `/root/go/bin/` — Go binaries

These paths are stored in snapshot metadata (`extra_paths` field) and context, so agents can see what's installed even if the watcher can't auto-replay those installs.

**Known capture gaps**: `docker export` + `docker commit` capture the filesystem but can't auto-replay non-package-managed installs (custom CUDA kernels, ldconfig changes, font caches, Nix closures). The extra path scanner makes these *visible* in context, but not *replayable*. **v0.2 plan**: Server-level snapshots + CRIU for full state capture including these edge cases.

**Periodic Snapshots** (via gradient-agent): Every 15 minutes, `docker commit` for speed + extra path scan. On shutdown, `docker export` for reliability + extra path scan.

### Security & Isolation (Container Hardening)

Containers are launched with defense-in-depth security:

```
docker run -d \
    --name gradient-env \
    --security-opt seccomp=/etc/gradient/seccomp.json \
    --security-opt no-new-privileges \
    --cap-drop ALL \
    --cap-add CHOWN --cap-add DAC_OVERRIDE --cap-add FSETID \
    --cap-add FOWNER --cap-add SETGID --cap-add SETUID \
    --cap-add NET_BIND_SERVICE --cap-add SYS_PTRACE \
    --cap-add KILL --cap-add AUDIT_WRITE --cap-add NET_RAW \
    --network gradient-net \
    -p 2222:22 -p 6767:6767 \
    --restart unless-stopped \
    -v /home/gradient/workspace:/workspace \
    $IMAGE tail -f /dev/null
```

| Layer | What | Why |
|-------|------|-----|
| `--cap-drop ALL` | Drop all Linux capabilities | Minimal privilege |
| `--cap-add ...` | Add only needed caps | `SYS_PTRACE` for debugging, `DAC_OVERRIDE` for package installs |
| `seccomp` profile | Whitelist syscalls | Blocks kernel module loading, raw disk access, etc. |
| `no-new-privileges` | Prevent privilege escalation | Blocks setuid/setgid binaries from gaining root |
| Bridge network | Isolated `gradient-net` | Not host network — containers can't sniff host traffic |
| Port mapping | `-p 2222:22 -p 6767:6767` | Only expose SSH and dev server ports |

**Not `--privileged`**: Previous versions used `--privileged --network host` — removed in favor of specific capabilities and bridge networking.

**v0.2 plan**: gVisor (`runsc`) as an alternative container runtime — already prototyped locally, provides syscall-level sandboxing without full VM overhead. Firecracker microVMs planned for v0.3 for maximum isolation + sub-second boot.

---

## 11. Authentication & Authorization

### Modes

| Mode | Trigger | Behavior |
|------|---------|----------|
| **Dev Mode** | `CLERK_SECRET_KEY` not set | Trusts `X-Org-ID`, `X-User-ID` headers (defaults to `dev-org`, `dev-user`) |
| **Production** | `CLERK_SECRET_KEY` set | Validates JWT in `Authorization: Bearer <token>` header using Clerk PEM public key |

### Device Auth Flow (CLI Login)

```
CLI                    API Server               Browser
 │                        │                        │
 ├── POST /auth/device ──►│                        │
 │◄── {device_code,       │                        │
 │     user_code,         │                        │
 │     verification_url}  │                        │
 │                        │                        │
 │ Opens browser ─────────┼───────────────────────►│
 │                        │   GET /auth/cli        │
 │                        │◄── HTML page with form │
 │                        │                        │
 │                        │   POST /auth/cli/approve
 │                        │◄──────────────────────│
 │                        │   (user_code match)    │
 │                        │                        │
 │── GET /auth/device/poll?code=<device_code> ────►│
 │◄── {status: "completed", token, org_id}         │
 │                        │                        │
 │ Saves to ~/.gradient/config.json                │
```

### CLI Config

Stored at `~/.gradient/config.json`:
```json
{
  "api_url": "http://localhost:6767",
  "token": "eyJhbGci...",
  "active_org": "org-123"
}
```

---

## 12. Billing & Usage Tracking

### Local Usage Tracking

Every environment lifecycle is tracked in `usage_events`:
- `TrackUsageStart()` — called on env create
- `TrackUsageStop()` — called on env destroy, calculates hours + cost, reports to Stripe

### Cost Rates

| Size | Cost per Hour |
|------|--------------|
| small | $0.05 |
| medium | $0.10 |
| large | $0.20 |
| gpu | $0.50 |

### Stripe Integration

| Capability | Implementation |
|------------|---------------|
| Customer Creation | `EnsureStripeCustomer()` — creates/retrieves Stripe customer from org_id |
| Metered Subscription | `CreateMeteredSubscription()` — creates subscription with usage-based pricing |
| Usage Reporting | `ReportUsageToStripe()` — reports usage quantity to Stripe's usage record API |
| Invoice Listing | `GetStripeInvoices()` — lists all invoices for the Stripe customer |

**Stripe Price IDs**: Configured via `STRIPE_PRICE_SMALL_ID`, `STRIPE_PRICE_MEDIUM_ID`, `STRIPE_PRICE_LARGE_ID` environment variables.

**v0.2 plan**: Per-second billing granularity. Free tier (20h/org/month). Usage dashboards in CLI and web UI.

---

## 13. Secret Management

### Vault Integration

**Implementation**: `pkg/secrets/vault.go`

Uses HashiCorp Vault's HTTP API directly (KV v2 engine):

| Operation | Vault Endpoint |
|-----------|---------------|
| Read | `GET /v1/{path}` |
| Write | `POST /v1/{path}` with `{data: {...}}` |
| Delete | `DELETE /v1/{path}` |
| List | `LIST /v1/{path}` |
| Health | `GET /v1/sys/health` |

### Secret Injection Flow

```
CLI / MCP                API Server                Vault              Cloud Server
    │                        │                       │                      │
    ├── POST /secrets/sync ─►│                       │                      │
    │   {env_id, key,        │                       │                      │
    │    backend: "vault",   │                       │                      │
    │    backend_path}       ├── GET /v1/{path} ────►│                      │
    │                        │◄── {data: {k: v}}     │                      │
    │                        │                       │                      │
    │                        ├── RemoteExecutor.ExecCommand() ────────────►│
    │                        │   docker exec gradient-env                   │
    │                        │   env KEY=VALUE ...                          │
    │                        │◄────────────────────────────────────────────│
    │◄── {status: "synced"}  │                       │                      │
```

The API server uses the `RemoteExecutor` interface — it doesn't care whether the underlying transport is SSH (Hetzner), SSM (AWS), or gcloud SSH (GCP).

**v0.2 plan**: AWS Secrets Manager backend. Secret rotation. Encrypted secret injection (not plaintext env vars).

---

## 14. Autoscaling

### How It Works

1. **Policy Configuration**: Set min/max replicas and CPU/memory thresholds per environment
2. **Metric Collection**: Agent reports CPU/memory every 1 minute → stored in environment's `config` JSONB
3. **Monitor Loop**: `AutoscaleService` evaluates all enabled policies every 30 seconds
4. **Scale Decision**:
   - **Scale Up**: If CPU *or* memory exceeds `scale_up_threshold` → calculate desired replicas based on `targetCPU/targetMemory` ratio
   - **Scale Down**: If CPU *and* memory below `scale_down_threshold` → reduce by 1
   - Replicas clamped to `[min_replicas, max_replicas]`
5. **Cooldown**: No scaling within `cooldown_secs` of the last scale event

### Scaling Strategy (v0.1 — Container-Only)

**v0.1 only scales by adding/removing Docker containers on the existing server.** Server-level scaling (creating new servers via warm pool or cold boot) is explicitly disabled and deferred to v0.2.

**Scale Up**:
- Add more Docker containers on the same server (~5s, no extra cost)
- Check server capacity: `maxPerServer` varies by size (small=4, medium=6, large=10, gpu=2)
- Start container with same security hardening (seccomp, cap-drop, bridge network)
- Track as `scale_type: "container"` in DB
- If server is at container capacity → log warning, cannot scale further in v0.1

**Scale Down**:
- Remove container replicas (instant, server stays running)
- Server is never destroyed by autoscaler in v0.1

**Max replicas**: 10 (hard cap for v0.1).

### Replica Metadata

**Container Replicas**:
```json
{
  "autoscale_parent": "env-original-id",
  "scale_type": "container",
  "container_name": "gradient-replica-abc12345-3"
}
```

Replicas inherit the parent's provider, region, size, and context branch.

### Max Containers Per Server

| Size | Max Containers | Rationale |
|------|---------------|-----------|
| small (2 vCPU, 4 GB) | 4 | Leave headroom for host + agent |
| medium (4 vCPU, 8 GB) | 6 | More cores for concurrent containers |
| large (8 vCPU, 16 GB) | 10 | High concurrency workloads |
| gpu (16 vCPU, 32 GB) | 2 | GPU workloads need dedicated resources |

**v0.2 plan**: Server-level autoscaling (warm pool → cold boot when container capacity is exhausted). Kubernetes-style HPA with custom metrics. Pod-level scaling with Firecracker microVMs.

---

## 15. Rate Limiting

**Implementation**: `internal/api/ratelimit.go`

Three-tier token bucket rate limiting applied as global middleware:

| Tier | Default Rate | Burst | Scope |
|------|-------------|-------|-------|
| Per-IP | 20 req/s | 40 | Every unique client IP |
| Per-Org | 100 req/s | 200 | Every authenticated org (except `dev-org`) |
| Global | 1000 req/s | 2000 | All requests combined |

**Excluded**: Health checks (`/api/v1/health`) bypass rate limiting.

**Response on Limit**: HTTP 429 with:
```json
{
  "error": "rate limit exceeded for IP x.x.x.x",
  "retry_after_seconds": 1
}
```

**Headers**: `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `Retry-After`

**Cleanup**: Stale limiter entries (not seen in 10 minutes) are garbage collected every 5 minutes.

**IP Detection**: `X-Forwarded-For` → `X-Real-IP` → `RemoteAddr` (for reverse proxy compatibility).

---

## 16. GitHub Integration

### Webhook Events

| Event | Action |
|-------|--------|
| `installation` | Record GitHub App installation ID |
| `create` (branch) | **Auto-Fork**: Copy context + snapshot pointers from parent branch |
| `push` | Trigger auto-snapshot (docker commit + push) on the environment running that branch |
| `delete` (branch) | Clean up context and snapshots for the deleted branch |

### Auto-Fork Flow

```
Developer creates branch feature/new-algo from main
                          │
GitHub sends webhook ─────┤
                          │
API receives "create" ────┤
                          │
  1. Find context for parent branch (main)
  2. Copy context to new branch (feature/new-algo)
  3. Find latest snapshot for parent branch
  4. Copy snapshot pointer to new branch
                          │
Result: feature/new-algo has full memory from main
```

### Webhook Signature Verification

All webhooks are verified using HMAC-SHA256 with the `GITHUB_APP_WEBHOOK_SECRET`.

---

## 17. Infrastructure Setup

### Quick Setup (Hetzner)

```bash
# 1. Set up Hetzner infrastructure (SSH key, firewall, network)
HETZNER_API_TOKEN=xxxx ./scripts/setup-hetzner-infra.sh

# 2. Build pre-baked image (optional, speeds up env creation)
HETZNER_API_TOKEN=xxxx ./scripts/build-hetzner-snapshot.sh

# 3. Set up NATS cluster (for Live Context Mesh)
./scripts/setup-nats-cluster.sh --region fsn1 --token your-nats-token

# 4. Copy output env vars to .env file
# 5. Start the stack
make stack-up     # PostgreSQL + NATS
make run-api      # API server
```

### Hetzner Firewall Rules

| Port | Protocol | Direction | Purpose | v0.1 |
|------|----------|-----------|---------|------|
| 22 | TCP | In | SSH access | ✅ |
| 8090 | TCP | In | Agent health endpoint | ✅ |
| 4222 | TCP | In | NATS client connections | ✅ |
| 6222 | TCP | In | NATS cluster routing | ✅ |
| 7222 | TCP | In | NATS gateway (multi-region) | 🚫 Disabled in v0.1 |

### NATS Configuration (v0.1 — Single Region)

v0.1 runs a single NATS JetStream node in one region (default: `fsn1`). Multi-region gateway routing is implemented in config files (`scripts/nats-gateway.conf`) but **disabled** — the `gateway {}` block is commented out.

Multi-region support (automatic event propagation across Hetzner regions like `fsn1`, `nbg1`, `hel1`, `ash`) is planned for v0.2.

---

## 18. Workflows

### Workflow 1: First-Time Setup

```bash
# Install the CLI
make install-cli

# Configure API URL and login
gc auth login --api-url http://localhost:6767

# Connect a GitHub repo for auto-fork
gc repo connect --repo myorg/myproject

# Verify
gc auth status -v
```

### Workflow 2: Create & Use an Environment

```bash
# Create an environment (provider defaults to first available, e.g. hetzner)
gc env create --name dev-env --region fsn1 --size medium --branch feature/x

# What happens under the hood:
# 1. If warm pool has a pre-booted server → assign it (~5-15s)
# 2. Otherwise cold boot: create server → cloud-init → Docker → agent (~2-6min)
# 3. Container starts with security hardening (seccomp, cap-drop, bridge network)
# 4. Boot time is recorded in environment config (boot_time_ms, boot_type: warm/cold)
# 5. gradient-agent starts: context replay, periodic snapshots, mesh subscription

# Check status (wait for "running")
gc env status <env-id>

# SSH into the environment
gc env ssh <env-id>

# Check agent health
gc env health <env-id>

# View container logs
gc env logs <env-id> --tail 100

# Execute a command in the container
gc env exec <env-id> -- pip install torch

# Take a manual snapshot
gc snapshot create <env-id> --tag "before-refactor"

# Destroy when done
gc env destroy <env-id>
# What happens on destroy:
# 1. Pre-destroy snapshot: docker export (primary) → docker commit (fallback)
# 2. Try returning server to warm pool (saves boot time for next env)
# 3. If warm pool full → delete server
# 4. Environment marked as destroyed in DB
```

### Workflow 3: Branch-Aware Context

```bash
# Save context for a branch (from AI agent or CLI)
gc context save --branch feature/x < context.json

# View context for a branch
gc context show --branch feature/x

# Create new env from branch (auto-restores snapshot + replays context)
gc env create --name new-env --region fsn1 --branch feature/x
# → Agent boots, replays installed packages + env vars from context
# → If snapshot exists, container starts from snapshot image
```

### Workflow 4: Live Context Mesh (Multi-Agent Collaboration)

```bash
# Agent 1 installs a package → automatically detected and published
# (happens inside the environment via gradient-agent filesystem watcher)

# View live events for a branch
gc context events --branch feature/x --type package_installed

# Stream events in real-time (SSE)
gc context live --branch feature/x

# Manually publish an event (from CLI or MCP)
gc context publish --branch feature/x --type pattern_learned \
  --data '{"key":"oom_fix","value":"reduce batch to 32"}'

# Check mesh health
gc context mesh-health

# View event statistics
gc context stats --branch feature/x

# WebSocket connection (for custom clients)
gc context ws --branch feature/x
# → Prints: ws://localhost:6767/api/v1/events/ws?branch=feature/x
```

**What happens when Agent 1 installs torch**:
1. Agent 1's gradient-agent detects new pip package (filesystem watcher)
2. Agent publishes `package_installed` event to NATS (`ctx.org-123.feature-x`)
3. NATS broadcasts to all subscribers on that subject
4. Agent 2 and 3 receive the event
5. Their gradient-agents auto-install torch in their containers
6. Event is persisted to PostgreSQL via MeshPublisher

### Workflow 5: Auto-Fork (New Branch Gets Full Memory)

```bash
# Developer creates branch in GitHub
git checkout -b feature/new-algo
git push origin feature/new-algo

# GitHub sends webhook to Gradient API
# API auto-forks:
#   1. Copies context from parent branch (main) to feature/new-algo
#   2. Copies latest snapshot pointer

# When agent creates env for the new branch:
gc env create --name algo-env --region fsn1 --branch feature/new-algo
# → Container starts from parent's snapshot
# → Context (packages, patterns, env vars) replayed on boot
# → Agent immediately has the parent branch's full memory
```

### Workflow 6: Secret Injection

```bash
# Store a secret in Vault
vault kv put secret/data/myapp DB_PASSWORD=s3cret API_KEY=abc123

# Sync to a running environment
gc secret sync --env <env-id> --key DB_PASSWORD \
  --backend vault --path secret/data/myapp

# What happens:
# 1. API reads from Vault (GET /v1/secret/data/myapp)
# 2. API uses RemoteExecutor interface to connect to cloud server
#    (SSH for Hetzner, SSM for AWS, gcloud ssh for GCP — transparent)
# 3. Runs: docker exec gradient-env env DB_PASSWORD=s3cret API_KEY=abc123
# 4. Records sync in secret_syncs table
```

### Workflow 7: Autoscaling

```bash
# Enable autoscaling for an environment
gc env autoscale enable <env-id> \
  --min 1 --max 5 \
  --target-cpu 0.70 --target-memory 0.80 \
  --cooldown 300

# Monitor scaling status
gc env autoscale status <env-id>

# View scaling history
gc env autoscale history <env-id>

# What happens automatically:
# 1. Agent reports CPU/memory every minute → stored in env config
# 2. AutoscaleService checks every 30 seconds
# 3. If CPU > 85%:
#    → Add containers on the same server (~5s, free)
#    → If server is at container capacity → log warning (server scaling disabled in v0.1)
# 4. If CPU < 30% AND memory < 30%:
#    → Remove container replicas (instant, server stays running)
# 5. Cooldown prevents thrashing (default 5 minutes)
# 6. Replicas inherit parent's security hardening (seccomp, capability drops)
# 7. Max 10 replicas per policy (v0.1 cap)
```

### Workflow 8: Billing

```bash
# Set up Stripe billing for the org
gc billing setup

# Check usage for current month
gc billing usage --month 2026-03

# List invoices
gc billing invoices

# Behind the scenes:
# - Every env create → TrackUsageStart() → usage_events row
# - Every env destroy → TrackUsageStop() → calculates hours, cost
# - If Stripe configured → ReportUsageToStripe() → Stripe usage records
# - Stripe generates invoices automatically based on usage records
```

### Workflow 9: Organization Management

```bash
# List your organizations
gc org list

# Switch active org
gc org switch org-456

# Invite a team member
gc org invite --email colleague@company.com --role member

# List members
gc org members

# List pending invitations
gc org invitations
```

### Workflow 10: MCP Agent Integration

An AI coding agent (e.g., in Cursor) uses the MCP server:

```json
// Agent calls gradient_env_create
{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{
  "name":"gradient_env_create",
  "arguments":{"name":"agent-env","region":"fsn1","size":"medium","context_branch":"feature/x"}
}}

// Agent calls gradient_context_get to read branch memory
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{
  "name":"gradient_context_get",
  "arguments":{"branch":"feature/x"}
}}

// Agent publishes a discovery
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{
  "name":"gradient_context_publish",
  "arguments":{
    "branch":"feature/x",
    "type":"pattern_learned",
    "data":{"key":"oom_fix","value":"reduce batch_size to 32"}
  }
}}

// Agent checks mesh health
{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{
  "name":"gradient_mesh_health",
  "arguments":{}
}}
```

---

## 19. Configuration Reference

All configuration is via environment variables (loaded in `internal/config/config.go`):

### Core

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `PORT` | No | `6767` | API server port |
| `DATABASE_URL` | Yes | — | PostgreSQL connection string |
| `API_URL` | No | `http://localhost:6767` | API URL (for agent callbacks) |

### Authentication (Clerk)

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `CLERK_SECRET_KEY` | No | — | Enables production auth (unset = dev mode) |
| `CLERK_PEM_PUBLIC_KEY` | No | — | PEM public key for JWT verification |

### Hetzner Cloud

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `HETZNER_API_TOKEN` | Yes* | — | Hetzner Cloud API token |
| `HETZNER_LOCATION` | No | `fsn1` | Default datacenter |
| `HETZNER_SSH_KEY_IDS` | No | — | Comma-separated SSH key IDs |
| `HETZNER_SSH_PRIVATE_KEY` | No | — | SSH private key PEM (for remote exec) |
| `HETZNER_FIREWALL_ID` | No | — | Firewall ID to attach to servers |
| `HETZNER_NETWORK_ID` | No | — | Private network ID |
| `HETZNER_IMAGE_ID` | No | — | Pre-baked snapshot ID (0 = ubuntu-24.04) |

### Platform Default Container Registry

These are the **platform-level** defaults. Used for orgs that don't configure their own.
Enterprise orgs override per-org via `PUT /api/v1/orgs/settings/registry` or `gc org registry set`.

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `REGISTRY_URL` | Yes | — | Docker registry URL (e.g. `ghcr.io/yourorg/gradient-envs`) |
| `REGISTRY_USER` | Yes | — | Registry username |
| `REGISTRY_PASS` | Yes | — | Registry password/token |
| `AGENT_DOWNLOAD_URL` | Yes | — | URL to download gradient-agent binary |

#### Per-Org Registry (Enterprise Isolation)

Enterprise orgs can configure their own container registry in `org_settings`:

```
PUT /api/v1/orgs/settings/registry
{
  "registry_url": "ghcr.io/enterprise-co/gradient-envs",
  "registry_user": "deploy-bot",
  "registry_pass": "ghp_xxxx"
}
```

**Resolution order**: Per-org registry → Platform default registry.

When an environment is created, `EnvService.resolveRegistry()` checks `org_settings.registry_url`.
If set, that registry is injected into the agent's environment file and used for all snapshots.
If not set, the platform default from env vars is used.

**CLI**: `gc org registry get | set --url <url> | clear`

### AWS (Legacy)

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `AWS_ACCESS_KEY_ID` | No | — | AWS access key |
| `AWS_SECRET_ACCESS_KEY` | No | — | AWS secret key |
| `AWS_REGION` | No | `us-east-1` | AWS region |
| `AWS_AMI_ID` | No | — | Pre-baked AMI ID |
| `AWS_SECURITY_GROUP_ID` | No | — | Security group |
| `AWS_SUBNET_ID` | No | — | Subnet |
| `AWS_KEY_PAIR_NAME` | No | — | SSH key pair name |
| `AWS_ECR_REPO_URI` | No | — | ECR repository URI |
| `AWS_INSTANCE_PROFILE` | No | — | IAM instance profile |

### Billing (Stripe)

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `STRIPE_SECRET_KEY` | No | — | Stripe API key (unset = billing disabled) |
| `STRIPE_PRICE_SMALL_ID` | No | — | Stripe Price ID for small tier |
| `STRIPE_PRICE_MEDIUM_ID` | No | — | Stripe Price ID for medium tier |
| `STRIPE_PRICE_LARGE_ID` | No | — | Stripe Price ID for large tier |

### Secrets (Vault)

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `VAULT_ADDR` | No | — | Vault server address |
| `VAULT_TOKEN` | No | — | Vault authentication token |

### Warm Pool

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `WARM_POOL_DEFAULT_SIZE` | No | `3` | Number of warm servers to maintain |
| `WARM_POOL_MAX_SIZE` | No | `3` | Hard cap on warm servers (clamped to 0–8) |
| `WARM_POOL_IDLE_TIMEOUT` | No | `30m` | Destroy idle warm servers after this duration |

**Cost guardrail**: `WARM_POOL_MAX_SIZE` is clamped to a hard max of 8 in code. Even if you set `WARM_POOL_MAX_SIZE=100`, you'll get 8. Set to `0` to disable the warm pool entirely (all boots will be cold).

### Live Context Mesh (NATS)

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `NATS_URL` | No | — | NATS server URL (unset = local bus) |
| `NATS_AUTH_TOKEN` | No | — | NATS authentication token |
| `NATS_MAX_AGE` | No | `168h` | Event retention period in JetStream |

### GitHub

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `GITHUB_APP_WEBHOOK_SECRET` | No | — | HMAC secret for webhook verification |

---

## 20. Development Setup

### Prerequisites

- Go 1.25+
- Docker + Docker Compose
- `hcloud` CLI (for Hetzner infrastructure)
- PostgreSQL 15+ (via Docker)
- NATS 2.10+ (via Docker)

### Quick Start

```bash
# Clone and enter the project
cd gradient

# Run the dev setup script (creates .env, starts Docker, builds everything)
./scripts/dev-setup.sh

# Or manually:
make stack-up          # Start PostgreSQL + NATS
make build             # Build all binaries
make run-api           # Start API server on :6767
make install-cli       # Install gc CLI to $GOPATH/bin
```

### Makefile Targets

| Target | Description |
|--------|-------------|
| `make build` | Build all binaries (api, cli, mcp) |
| `make build-agent-linux` | Cross-compile agent for Linux (amd64) |
| `make run-api` | Start API server |
| `make run-mcp` | Start MCP server |
| `make install-cli` | Install `gc` to `$GOPATH/bin` |
| `make test` | Run all tests |
| `make vet` | Run `go vet` |
| `make lint` | Run linter |
| `make db-up` | Start PostgreSQL + NATS via Docker Compose |
| `make db-down` | Stop Docker Compose services |
| `make stack-up` | Start PostgreSQL + NATS |
| `make stack-up-full` | Start PostgreSQL + NATS + Vault |
| `make setup-hetzner` | Run Hetzner infra setup script |
| `make setup-nats` | Run NATS cluster setup script |

### Docker Compose Services

| Service | Port | Description |
|---------|------|-------------|
| `postgres` | 5432 | PostgreSQL 15 with `gradient` database |
| `nats` | 4222 (client), 8222 (monitoring), 6222 (cluster) | NATS JetStream (single node, gateway port 7222 disabled in v0.1) |
| `vault` | 8200 | HashiCorp Vault (dev mode, optional via `secrets` profile) |

### Running Tests

```bash
make test
# 236 tests across all packages
# Tests cover: models, services, API handlers, MCP tools, Live Context Mesh events/bus, rate limiting, autoscaling
```

### Project Structure

```
gradient/
├── cmd/
│   ├── api/main.go              # API server entry point
│   ├── cli/
│   │   ├── main.go              # CLI entry point
│   │   └── commands/
│   │       ├── auth.go          # gc auth (login, logout, status)
│   │       ├── env.go           # gc env (create, list, ssh, health, logs, exec, autoscale)
│   │       ├── context.go       # gc context (show, events, live, publish, stats, ws)
│   │       ├── repo.go          # gc repo (connect, list, disconnect)
│   │       ├── billing.go       # gc billing (usage, invoices, setup)
│   │       ├── org.go           # gc org (list, switch, members, invite)
│   │       ├── client.go        # HTTP client for API calls
│   │       └── config.go        # CLI config management (~/.gradient/config.json)
│   ├── mcp/main.go              # MCP server entry point
│   └── agent/main.go            # gradient-agent entry point
├── internal/
│   ├── api/
│   │   ├── server.go            # Router, handlers, initialization (~1650 lines)
│   │   ├── middleware.go         # Auth middleware (JWT/dev mode)
│   │   ├── ratelimit.go         # Rate limiting middleware
│   │   ├── websocket.go         # WebSocket handler
│   │   └── device_auth.go      # Device auth flow
│   ├── config/config.go         # Environment variable loading
│   ├── db/
│   │   ├── db.go                # PostgreSQL pool + migration
│   │   └── schema.sql           # DDL for all tables
│   ├── models/models.go         # Core structs (Environment, Context, Snapshot, etc.)
│   ├── services/
│   │   ├── env_service.go       # Environment lifecycle (warm pool integration, pre-destroy snapshot)
│   │   ├── context_service.go   # Branch context CRUD
│   │   ├── billing_service.go   # Usage tracking + Stripe
│   │   ├── repo_service.go      # GitHub repo management + webhooks
│   │   ├── org_service.go       # Clerk org management
│   │   ├── snapshot_store.go    # Snapshot metadata
│   │   ├── autoscale_service.go # Container-level autoscaling (server scaling disabled in v0.1)
│   │   └── warm_pool.go         # Pre-booted server pool for fast boot
│   └── mcp/
│       ├── server.go            # MCP JSON-RPC handler (17 tools)
│       └── server_test.go
├── pkg/
│   ├── env/
│   │   ├── provider.go          # Provider + Snapshotter + HybridSnapshotter + RemoteExecutor + NetworkInfo interfaces
│   │   ├── hetzner_provider.go  # Hetzner Cloud implementation (all interfaces)
│   │   ├── aws_provider.go      # AWS EC2 implementation (legacy)
│   │   └── repository.go        # Environment DB repository
│   ├── context/
│   │   └── store.go             # Branch context PostgreSQL store
│   ├── livectx/
│   │   ├── events.go            # Event types, validation, schema
│   │   ├── bus.go               # NATS JetStream event bus + local fallback
│   │   ├── store.go             # PostgreSQL event store (durable log)
│   │   ├── events_test.go
│   │   └── bus_test.go
│   └── secrets/
│       └── vault.go             # Vault HTTP client + SecretSyncer
├── scripts/
│   ├── dev-setup.sh
│   ├── setup-hetzner-infra.sh
│   ├── build-hetzner-snapshot.sh
│   ├── setup-nats-cluster.sh
│   └── nats-gateway.conf
├── docker-compose.yml
├── Makefile
├── go.mod
├── go.sum
├── MVP_SPEC.md
├── ENGINEERING_SPEC.md
└── TECHNICAL_DOC.md             # This file
```

---

## Appendix: Technology Stack

| Component | Technology | Version |
|-----------|-----------|---------|
| Language | Go | 1.25+ |
| HTTP Router | gorilla/mux | v1.8.1 |
| WebSocket | gorilla/websocket | v1.5.3 |
| CLI Framework | cobra | v1.8.1 |
| Database Driver | pgx/v5 | v5.7.4 |
| JWT | golang-jwt/v5 | v5.2.1 |
| Billing | stripe-go/v76 | v76.25.0 |
| Cloud (Primary) | hcloud-go/v2 | v2.20.0 |
| Cloud (Legacy) | aws-sdk-go-v2 | v1.34.0 |
| Event Bus | nats.go | v1.39.1 |
| SSH | golang.org/x/crypto | v0.36.0 |
| Rate Limiting | golang.org/x/time/rate | — |
| Secrets | HashiCorp Vault (HTTP API) | — |
| Auth | Clerk (JWT + Backend API) | — |
| CI/CD | GitHub (webhooks) | — |
| Database | PostgreSQL | 15+ |
| Container Runtime | Docker | — |
| Infra | Hetzner Cloud | — |
