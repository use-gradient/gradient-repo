# Gradient: The Infrastructure Platform That AI Agents Can Actually Use
## Engineering Specification

**Version:** 1.0  
**Last Updated:** 2025-01-XX  
**Status:** Draft  
**Authors:** Gradient Team

---

## Pricing

**Gradient Pricing – One plan, zero bullshit**

| Tier | Price per hour | What you get | Best for |
|------|----------------|--------------|----------|
| **Free** | 20 hours/month | Small Linux only, basic capture | Solo devs, testing |
| **Pay-as-you-go** | $0.15 / hour (small)<br>$0.35 / hour (medium)<br>$0.70 / hour (large)<br>$3.50 / hour (1× GPU) | Any size/OS/cloud, full capture + context + MCP + Karmada | Everyone else |

**Billing Details:**
- **Billed per second, minimum 1 minute**
- **One single invoice.** No separate cloud bills. No surprise egress. No "plus storage" nickel-and-diming.
- **Context storage:** Free up to 500 MB per branch, then $0.15/GB/month (almost nobody hits this)
- **Enterprise:** Same rates, just annual contract + SLA + support (20-30% discount)

**Why This Works:**
- **Per-hour aligns with agent usage:** Agents spin 50 short-lived test envs? You only pay for actual minutes. Someone leaves a big env running 24/7? You pay for it (and learn to stop it).
- **The "OH SHIT" capture feature is baked into the hourly rate** — users pay the premium because it saves them hours of agent time every day.
- **Per-second billing is non-negotiable:** Agents are bursty. A 12-minute test run should cost $0.03, not $0.15. Users expect this now.
- **Healthy margins:** You absorb + markup the real AWS/GCP/Azure cost (you pay ~40-60% of what you charge → healthy 40-60% gross margin).

**That's it. Four lines on the pricing page. No "plans", no "seats", no "enterprise contact sales" fluff.**

---

## Overview

**Gradient** is the infrastructure platform that makes AI agents actually work — by giving git branches living, reproducible runtime snapshots that get smarter every time an agent touches them.

### The "OH SHIT" Moment

**Your AI agent can go wild installing whatever the hell it wants** — `apt install`, `pip install`, `npm install`, `cargo build`, CUDA drivers, custom binaries, whatever — inside any environment… **and Gradient silently captures every single change.**

Then, the next time anyone (human or another agent) spins up any environment for that exact same git branch — on AWS, GCP, Azure, or any cloud — **it boots in <30 seconds (agent can start working immediately), with packages installing in background (2-5 min total).**

- **No Dockerfile.**
- **No devcontainer.json.**
- **No "wait 8 minutes while I pip install -r requirements.txt again".**
- **No version drift.**
- **No "works on my machine".**

**The branch itself now carries a living, reproducible runtime snapshot that gets smarter every time an agent touches it.**

### Why This Hits Like Crack in 2026

Every AI coding agent today (Claude Code, Cursor, Windsurf, etc.) still dies on the same stupid rock: **fresh environments are amnesiac.** The agent wastes 5–15 minutes and thousands of tokens reinstalling the same shit on every new session or cloud env.

- DesktopCommanderMCP does persistence only on your laptop.
- Gitpod/DevPod/Coder are declarative only (you have to write the list upfront).
- **No one auto-learns from what the agent actually did at runtime and replays it perfectly across clouds per branch.**

**Gradient does.** This is the feature you lead every single pitch, demo, and landing page with. It's the thing no one else ships in 2026, and the market will feel the pain harder every month as agents get more autonomous.

### Core Value Proposition

**THE Feature (The "OH SHIT" Moment):**
- **Runtime environment capture:** AI agents install whatever they want (apt, pip, npm, cargo, CUDA, custom binaries) → Gradient silently captures everything → next environment on any cloud boots in <60s with exact same state. No Dockerfile. No reinstall. No drift.

**The Delivery Mechanism:**
- **AI agent-native:** Persistent context shared across all environments and clouds for agentic workflows
- **Deploy anywhere:** Single CLI/API works across AWS, GCP, Azure
- **Auto-scaling:** Intelligent scaling decisions across all environments (powered by Karmada's FederatedHPA for K8s)
- **Secrets orchestration:** Thin layer on top of existing secret backends (Vault, AWS Secrets Manager, GCP Secret Manager) with cross-env sync
- **Context persistence:** Branch/PR context shared across environments — agents remember what worked, what didn't, and what packages were installed

**Interface Philosophy:**
- **CLI-first:** Primary interface for all operations (`gc` command)
- **API:** REST/gRPC for programmatic access and integrations
- **MCP:** Model Context Protocol for AI agent integration
- **Dashboard:** Simple read-only monitoring (no management operations)

---

## Architecture

### High-Level Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                    Control Plane (Go)                            │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │  Orchestration Engine                                     │  │
│  │  - Multi-cloud provider abstraction                       │  │
│  │  - Auto-scaling decisions                                 │  │
│  └──────────────────────────────────────────────────────────┘  │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │  Secret Manager (Thin Orchestrator)                      │  │
│  │  - Orchestrates Vault, AWS Secrets Manager, GCP, etc.    │  │
│  │  - Cross-environment sync                                 │  │
│  │  - Context-aware injection                                │  │
│  └──────────────────────────────────────────────────────────┘  │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │  Context Store (Distributed)                               │  │
│  │  - Branch/PR context persistence                          │  │
│  │  - Agent state sharing                                    │  │
│  │  - Cross-environment context sync                         │  │
│  └──────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
                            │
        ┌───────────────────┼───────────────────┐
        │                   │                   │
        ▼                   ▼                   ▼
┌──────────────┐    ┌──────────────┐    ┌──────────────┐
│  CLI (Go)    │    │  REST API     │    │  MCP Server  │
│  - gc cmd    │    │  (HTTP/gRPC)  │    │  (MCP)       │
└──────────────┘    └──────────────┘    └──────────────┘
        │                   │                   │
        └───────────────────┼───────────────────┘
                            │
                            ▼
                    ┌──────────────┐
                    │  Dashboard   │
                    │  (Read-only) │
                    │  Monitoring  │
                    └──────────────┘
                            │
                            │
        ┌───────────────────┼───────────────────┐
        │                   │                   │
        ▼                   ▼                   ▼
┌──────────────┐    ┌──────────────┐    ┌──────────────┐
│   AWS Envs   │    │   GCP Envs    │    │  Azure Envs  │
│  (EKS, EC2)  │    │  (GKE, GCE)   │    │  (AKS, VMs)  │
└──────────────┘    └──────────────┘    └──────────────┘
```

### Interface Philosophy

**Primary Interfaces (All Operations):**
- **CLI** (`gc`): Primary interface for engineers, scripts, CI/CD
- **REST API**: Programmatic access, integrations, automation
- **MCP Server**: AI agent integration (Claude, GPT, etc.)

**Secondary Interface (Read-Only):**
- **Dashboard**: Simple monitoring view (status, metrics, logs) - no management operations

### Component Breakdown

#### 1. Orchestration Engine (Go)

**Responsibilities:**
- Multi-cloud provider abstraction (AWS, GCP, Azure)
- Environment lifecycle management (create, destroy, scale)
- Auto-scaling decision engine
- Resource allocation and bin-packing
- Multi-cluster federation (using Karmada under the hood for K8s)

**Key Interfaces:**

```go
// Provider abstraction (AWS, GCP, Azure)
type Provider interface {
    // Environment management
    CreateEnv(config EnvConfig) (*Env, error)
    DestroyEnv(envID string) error
    GetEnv(envID string) (*Env, error)
    ListEnvs(filters EnvFilters) ([]*Env, error)
    
    // Scaling
    ScaleEnv(envID string, replicas int) error
    GetMetrics(envID string) (*Metrics, error)
    
    // Secrets
    InjectSecrets(envID string, secrets []Secret) error
    GetSecrets(envID string) ([]Secret, error)
    
    // Network
    UpdateRouting(envID string, routes []Route) error
    GetEndpoints(envID string) ([]Endpoint, error)
}

// Environment configuration
type EnvConfig struct {
    ID          string            `json:"id"`
    Name        string            `json:"name"`
    Provider    string            `json:"provider"` // "aws", "gcp", "azure"
    OS          string            `json:"os,omitempty"` // "ubuntu-24.04", "windows-2022", "macos-15" (default: ubuntu-24.04)
    Region      string            `json:"region"`
    Type        string            `json:"type"`     // "k8s", "vm", "container"
    Resources   ResourceSpec     `json:"resources"`
    Secrets     []SecretRef       `json:"secrets"`
    Context     ContextRef        `json:"context"`
    Labels      map[string]string `json:"labels"`
    Annotations map[string]string `json:"annotations"`
}

// Resource specification
type ResourceSpec struct {
    CPU    string `json:"cpu"`    // "2", "1000m"
    Memory string `json:"memory"`  // "4Gi", "8GB"
    Disk   string `json:"disk"`   // "100Gi"
    GPU    *GPUSpec `json:"gpu,omitempty"`
}

// Metrics for auto-scaling
type Metrics struct {
    CPUUsage    float64 `json:"cpu_usage"`     // 0.0-1.0
    MemoryUsage float64 `json:"memory_usage"`   // 0.0-1.0
    RequestRate float64 `json:"request_rate"`   // requests/second
    ErrorRate   float64 `json:"error_rate"`     // errors/second
    Timestamp   time.Time `json:"timestamp"`
}
```

#### 2. Secret Manager (Thin Orchestrator)

**Responsibilities:**
- Thin orchestration layer on top of existing secret backends (Vault, AWS Secrets Manager, GCP Secret Manager, Infisical)
- Cross-environment secret synchronization
- Unified API for multiple secret backends
- Context-aware secret injection (for agentic workflows)

**Key Interfaces:**

```go
// Secret backend abstraction
type SecretBackend interface {
    GetSecret(key string) (*Secret, error)
    SetSecret(key string, value []byte) error
    DeleteSecret(key string) error
    ListSecrets() ([]string, error)
}

// Supported backends
type VaultBackend struct { ... }
type AWSSecretsManagerBackend struct { ... }
type GCPSecretManagerBackend struct { ... }
type InfisicalBackend struct { ... }

type SecretManager struct {
    backends map[string]SecretBackend  // "vault", "aws", "gcp", "infisical"
    syncLayer *CrossEnvSyncLayer       // Our cross-environment sync
}

type SecretManager interface {
    // Secret CRUD (delegates to backend)
    CreateSecret(secret Secret, backend string) error  // backend: "vault", "aws", "gcp", etc.
    GetSecret(key string, backend string) (*Secret, error)
    UpdateSecret(secret Secret, backend string) error
    DeleteSecret(key string, backend string) error
    
    // Cross-environment sync (our value-add)
    SyncSecretsToEnv(envID string, secretKeys []string, backend string) error
    SyncSecretsFromEnv(envID string, backend string) error
    
    // Context-aware injection (for agentic workflows)
    InjectSecretsForContext(envID string, branch string, context Context) error
}

type Secret struct {
    Key       string            `json:"key"`
    Value     []byte            `json:"value"`      // Encrypted (by backend)
    Backend   string            `json:"backend"`    // "vault", "aws", "gcp", "infisical"
    EnvRefs   []string          `json:"env_refs"`   // Environments using this secret
    Metadata  map[string]string `json:"metadata"`
    CreatedAt time.Time         `json:"created_at"`
    UpdatedAt time.Time         `json:"updated_at"`
}
```

**Why This Approach:**
- **Don't reinvent:** Users already have Vault, AWS Secrets Manager, etc. We orchestrate, not replace.
- **Trust:** Users trust their existing secret backends (audit, HSM, compliance).
- **Value-add:** Our cross-environment sync and context-aware injection is the differentiator.

#### 3. Context Store (Go)

**Responsibilities:**
- Branch/PR context persistence (for agentic workflows)
- Runtime environment capture (OS, packages, tools)
- Cross-environment context sharing
- Context versioning and history
- Agent state management

**Key Interfaces:**

```go
type ContextStore interface {
    // Context CRUD
    SaveContext(branch string, context Context) error
    GetContext(branch string) (*Context, error)
    UpdateContext(branch string, context Context) error
    
    // Cross-environment sync
    SyncContextToEnv(envID string, branch string) error
    GetContextFromEnv(envID string, branch string) (*Context, error)
    
    // Package capture (automatic, no manual trigger needed)
    // CapturePackages is called automatically on env stop/destroy/periodic
    ReplayPackages(envID string, branch string) error
    
    // History
    GetContextHistory(branch string, limit int) ([]Context, error)
}

type Context struct {
    Branch        string                 `json:"branch"`
    CommitSHA     string                 `json:"commit_sha"`
    PreviousFailures []TestFailure       `json:"previous_failures"`
    AttemptedFixes []Fix                 `json:"attempted_fixes"`
    Patterns      map[string]interface{} `json:"patterns"`
    TestResults   []TestResult           `json:"test_results"`
    
    // Runtime environment state (NEW - the game-changer)
    BaseOS            string              `json:"base_os"`          // "ubuntu-24.04", "windows-2022", "macos-15" (default: ubuntu-24.04)
    InstalledPackages []InstalledPackage  `json:"installed_packages"`
    GlobalConfigs     map[string]string   `json:"global_configs,omitempty"` // PATH tweaks, env vars, etc.
    
    Metadata      map[string]string      `json:"metadata"`
    CreatedAt     time.Time              `json:"created_at"`
    UpdatedAt     time.Time              `json:"updated_at"`
}

type InstalledPackage struct {
    Manager string `json:"manager"` // "apt", "pip", "npm", "cargo", "go", "choco", "brew", ...
    Name    string `json:"name"`
    Version string `json:"version,omitempty"` // exact version if pinned, else latest-at-time
    Source  string `json:"source,omitempty"`  // pypi, npm registry, etc.
    InstalledAt time.Time `json:"installed_at,omitempty"`
}
```

**Why This Is Critical:**
- **Eliminates setup friction:** Agents never reinstall deps again — exact state is captured and replayed
- **Cumulative knowledge:** Agents remember not just "we fixed the timeout bug" but "we also installed pydantic-ai and ffmpeg"
- **Institutional memory:** Package state compounds forever per branch
- **No one else does this:** Current tools (Gitpod, Codespaces, DevPod) are declarative-only — you define packages upfront, not capture what agents actually installed

---

## Interfaces

### CLI (`gc` command)

**Primary interface for all operations.** Simple, scriptable, CI/CD-friendly.

#### Installation

```bash
# Install via package manager
brew install gradient
# or
curl -sSL https://gradient.io/install.sh | sh
```

#### Authentication

```bash
gc auth login
# Opens browser for OAuth, stores token in ~/.gradient/config
```

#### Environment Management

```bash
# Create environment
gc env create \
  --name trading-algo-prod \
  --provider aws \
  --region us-east-1 \
  --type vm \
  --cpu 4 \
  --memory 8Gi \
  --secrets db-password,api-key \
  --context-branch main

# List environments
gc env list
gc env list --provider aws --region us-east-1

# Get environment details
gc env get env-abc123

# Destroy environment
gc env destroy env-abc123
gc env destroy --all  # Destroy all environments
```

#### Scaling

```bash
# Manual scaling
gc env scale env-abc123 --replicas 5

# Enable auto-scaling
gc env autoscale enable env-abc123 \
  --min 2 \
  --max 10 \
  --target-cpu 0.7 \
  --target-memory 0.8

# Disable auto-scaling
gc env autoscale disable env-abc123
```


#### Secrets Management

```bash
# Create secret
gc secret create db-password --value "super-secret" --env env-abc123

# List secrets
gc secret list

# Sync secrets to environment
gc secret sync env-abc123 --keys db-password,api-key

# Rotate secret
gc secret rotate db-password
```

#### Context Management

```bash
# Save context
gc context save \
  --branch feature/new-algo \
  --commit abc123 \
  --file context.json

# Get context
gc context get --branch feature/new-algo

# Sync context to environment
gc context sync env-abc123 --branch feature/new-algo

# Create environment with context (replays exact OS + packages)
# Packages are automatically captured in the background — no manual step needed
gc env create \
  --name new-env \
  --context-branch feature/new-algo \
  --os ubuntu-24.04  # optional, defaults to ubuntu-24.04
  # --os windows-2022  # for Windows
  # --os macos-15      # for macOS
```

#### Status & Monitoring

```bash
# Get environment status
gc env status env-abc123

# Watch environment (real-time updates)
gc env watch env-abc123

# Get metrics
gc env metrics env-abc123 --cpu --memory --last 1h

# Stream logs
gc env logs env-abc123 --follow
```

#### Configuration

```bash
# Set provider credentials
gc config set aws.access-key-id AKIA...
gc config set aws.secret-access-key ...

# View config
gc config get

# Use config file
gc env create --config env.yaml
```

### REST API

**Simple HTTP/gRPC API for programmatic access.** All CLI operations are available via REST API.

### MCP Server

**Model Context Protocol server for AI agent integration.**

Enables AI agents (Claude, GPT, etc.) to manage Gradient environments through natural language.

#### MCP Tools

```typescript
// MCP tools exposed to AI agents
{
  "gradient_env_create": {
    "description": "Create a new environment",
    "parameters": {
      "name": "string",
      "provider": "aws|gcp|azure",
      "region": "string",
      "type": "vm|k8s|container",
      "os": "ubuntu-24.04|windows-2022|macos-15", // optional, defaults to ubuntu-24.04
      "resources": {...}
    }
  },
  "gradient_env_list": {
    "description": "List all environments"
  },
  "gradient_secret_create": {
    "description": "Create a secret"
  },
  "gradient_context_save": {
    "description": "Save branch/PR context"
  },
  // ... all CLI operations exposed as MCP tools
}
```

#### Example AI Agent Usage

```
User: "Create a new environment on AWS for my trading algorithm"

AI Agent (via MCP):
→ Calls gradient_env_create tool
→ Returns environment ID and status
→ User can continue conversation: "Now scale it to 5 replicas"
→ AI calls gradient_env_scale tool
```

### Dashboard (Read-Only)

**Simple web interface for monitoring only.** No management operations.

**Features:**
- View environment status
- View metrics (CPU, memory, request rate)
- View logs (read-only)
- View secrets list (no values, just keys)
- View context metadata (no full context, just branch/commit info)

**Access:**
```
https://dashboard.gradient.io
```

**No Operations:**
- Cannot create/destroy environments
- Cannot scale
- Cannot create secrets
- Cannot modify context

All management must be done via CLI, API, or MCP.

---

## API Design

### REST API

#### Environment Management

**Create Environment**
```http
POST /api/v1/environments
Content-Type: application/json

{
  "name": "trading-algo-prod",
  "provider": "aws",
  "region": "us-east-1",
  "type": "vm",
  "resources": {
    "cpu": "4",
    "memory": "8Gi",
    "disk": "100Gi"
  },
  "secrets": ["db-password", "api-key"],
  "context": {
    "branch": "main",
    "sync": true
  },
  "labels": {
    "team": "trading",
    "env": "production"
  }
}

Response: 201 Created
{
  "id": "env-abc123",
  "name": "trading-algo-prod",
  "provider": "aws",
  "status": "creating",
  "endpoints": [],
  "created_at": "2025-01-15T10:00:00Z"
}
```

**List Environments**
```http
GET /api/v1/environments?provider=aws&region=us-east-1&label=team=trading

Response: 200 OK
{
  "environments": [
    {
      "id": "env-abc123",
      "name": "trading-algo-prod",
      "provider": "aws",
      "region": "us-east-1",
      "status": "running",
      "resources": {
        "cpu": "4",
        "memory": "8Gi"
      },
      "metrics": {
        "cpu_usage": 0.45,
        "memory_usage": 0.62
      }
    }
  ],
  "total": 1
}
```

**Get Environment**
```http
GET /api/v1/environments/env-abc123

Response: 200 OK
{
  "id": "env-abc123",
  "name": "trading-algo-prod",
  "provider": "aws",
  "region": "us-east-1",
  "status": "running",
  "endpoints": [
    {
      "type": "ssh",
      "address": "trading-algo-prod.gradient.io",
      "port": 22
    }
  ],
  "metrics": {
    "cpu_usage": 0.45,
    "memory_usage": 0.62,
    "request_rate": 150.5,
    "error_rate": 0.01
  },
  "secrets": ["db-password", "api-key"],
  "context": {
    "branch": "main",
    "last_updated": "2025-01-15T09:30:00Z"
  }
}
```

**Destroy Environment**
```http
DELETE /api/v1/environments/env-abc123

Response: 202 Accepted
{
  "id": "env-abc123",
  "status": "destroying",
  "estimated_completion": "2025-01-15T10:05:00Z"
}
```

#### Scaling

**Scale Environment**
```http
POST /api/v1/environments/env-abc123/scale
Content-Type: application/json

{
  "replicas": 5,
  "strategy": "rolling" // "rolling", "blue-green", "canary"
}

Response: 202 Accepted
{
  "id": "env-abc123",
  "current_replicas": 3,
  "target_replicas": 5,
  "status": "scaling"
}
```

**Enable Auto-Scaling**
```http
POST /api/v1/environments/env-abc123/autoscale
Content-Type: application/json

{
  "enabled": true,
  "min_replicas": 2,
  "max_replicas": 10,
  "target_cpu": 0.7,
  "target_memory": 0.8,
  "scale_up_cooldown": "5m",
  "scale_down_cooldown": "10m"
}

Response: 200 OK
{
  "autoscale": {
    "enabled": true,
    "min_replicas": 2,
    "max_replicas": 10
  }
}
```

#### Secrets Management

**Create Secret**
```http
POST /api/v1/secrets
Content-Type: application/json

{
  "key": "db-password",
  "value": "super-secret-password",
  "env_refs": ["env-abc123"],
  "metadata": {
    "description": "Database password for trading algo"
  }
}

Response: 201 Created
{
  "key": "db-password",
  "created_at": "2025-01-15T10:00:00Z",
  "env_refs": ["env-abc123"]
}
```

**Sync Secrets to Environment**
```http
POST /api/v1/environments/env-abc123/secrets/sync
Content-Type: application/json

{
  "secret_keys": ["db-password", "api-key"],
  "force": false
}

Response: 200 OK
{
  "synced_secrets": ["db-password", "api-key"],
  "synced_at": "2025-01-15T10:00:00Z"
}
```

#### Context Management

**Save Context**
```http
POST /api/v1/context
Content-Type: application/json

{
  "branch": "feature/trading-algo-v2",
  "commit_sha": "abc123def456",
  "previous_failures": [
    {
      "test": "integration_test",
      "error": "Connection timeout",
      "timestamp": "2025-01-15T09:00:00Z"
    }
  ],
  "attempted_fixes": [
    {
      "fix": "Increased timeout to 30s",
      "success": true,
      "timestamp": "2025-01-15T09:15:00Z"
    }
  ],
  "patterns": {
    "common_failures": ["timeout", "oom"],
    "successful_fixes": ["increase_timeout", "add_retry"]
  }
}

Response: 201 Created
{
  "branch": "feature/trading-algo-v2",
  "saved_at": "2025-01-15T10:00:00Z"
}
```

**Get Context**
```http
GET /api/v1/context?branch=feature/trading-algo-v2

Response: 200 OK
{
  "branch": "feature/trading-algo-v2",
  "commit_sha": "abc123def456",
  "previous_failures": [...],
  "attempted_fixes": [...],
  "patterns": {...},
  "last_updated": "2025-01-15T10:00:00Z"
}
```

**Sync Context to Environment**
```http
POST /api/v1/environments/env-abc123/context/sync
Content-Type: application/json

{
  "branch": "feature/trading-algo-v2",
  "force": false
}

Response: 200 OK
{
  "synced": true,
  "branch": "feature/trading-algo-v2",
  "synced_at": "2025-01-15T10:00:00Z"
}
```

### WebSocket API (Real-time Updates)

**Connect to Environment Updates** (for dashboard and real-time monitoring)

```javascript
const ws = new WebSocket('wss://api.gradient.io/v1/environments/env-abc123/ws');

ws.onmessage = (event) => {
  const update = JSON.parse(event.data);
  
  switch (update.type) {
    case 'status':
      console.log(`Environment status: ${update.status}`);
      break;
    case 'metrics':
      console.log(`CPU: ${update.metrics.cpu_usage}`);
      break;
    case 'scaling':
      console.log(`Scaling to ${update.replicas} replicas`);
      break;
  }
};
```

**Note:** WebSocket is primarily for dashboard real-time updates. Use CLI `gc env watch` for CLI-based monitoring.

---

## Example Workflows

### Workflow 1: The "OH SHIT" Demo — Runtime Environment Capture

**Goal:** Show how an AI agent can install whatever it wants, and the next environment on any cloud boots with everything already there.

**The Demo That Makes Jaws Drop:**

**Steps (CLI):**

1. **Agent Starts on Fresh Environment**
```bash
# Linux (default)
gc env create \
  --name agent-session-1 \
  --provider aws \
  --region us-east-1 \
  --type vm \
  --context-branch feature/new-algo

# Or specify OS explicitly
gc env create \
  --name agent-session-1 \
  --provider aws \
  --os ubuntu-24.04 \
  --context-branch feature/new-algo

# Windows
gc env create \
  --name agent-session-1 \
  --provider aws \
  --os windows-2022 \
  --context-branch feature/new-algo

# macOS
gc env create \
  --name agent-session-1 \
  --provider aws \
  --os macos-15 \
  --context-branch feature/new-algo

# Output: Environment env-abc123 created, status: creating
# Boots in <30 seconds (agent can SSH in immediately)
# Uses pre-baked base image (Python, Node, Go, Rust, git, curl, jq, ripgrep, etc. already installed)
# Agent can start working immediately
```

2. **Agent Goes Wild Installing Everything**
```bash
# Agent runs in environment (via SSH or agent session)
ssh user@agent-session-1.gradient.io

# Agent does 47 different installs:
apt install ffmpeg imagemagick libcuda-dev
pip install langchain openai pydantic-ai torch
npm install -g turbo pnpm
cargo install ripgrep fd
# Builds custom FFmpeg wrapper
# Sets up CUDA 12.4
# Tweaks .bashrc, PATH, env vars
# Installs custom binaries from GitHub releases

# Gradient silently captures EVERY SINGLE CHANGE via container snapshots (every 15-30 min)
# Container layer diffs capture all changes reliably (no hook brittleness)
# Latest snapshot available for replication (15-30 min freshness)
```

3. **Replicate Running Environment (Shows It's Always Captured)**
```bash
# While env-abc123 is still running, create a replica on GCP
gc env create \
  --name agent-session-2 \
  --provider gcp \
  --region us-west-1 \
  --context-branch feature/new-algo

# Even though env-abc123 is still running, the new env has:
# - Latest snapshot (15-30 min freshness) with all 47 packages
# - CUDA 12.4 already set up (pre-baked in base image)
# - Everything from latest snapshot
# - Packages install in background (2-5 min) while agent can start working
# Context is current enough — no need to wait for env to stop
```

4. **Destroy Original Environment (Optional)**
```bash
gc env destroy env-abc123
# Everything was already captured in real-time
# The branch now carries a living, reproducible runtime snapshot
```

5. **Create New Environment on Different Cloud — Everything Already There**
```bash
# Create on GCP (different cloud, different region)
gc env create \
  --name agent-session-2 \
  --provider gcp \
  --region us-west-1 \
  --context-branch feature/new-algo

# Output: Environment env-def456 created
# Boots in <30 seconds (agent can SSH in immediately)
# Uses pre-baked base image (Python, Node, Go, Rust, git, curl, etc. already installed)
# Background installation (2-5 min total):
# - Same OS base (Linux/Windows/macOS) as source
# - All 47 packages installing in background (deltas from base image)
# - CUDA 12.4 already set up (if Linux, pre-baked in base image)
# - Custom binaries installing in background
# - Config files, PATH, env vars applying as packages install
# - Agent can start working immediately, packages appear as they install
# - NO REINSTALL. NO WAIT. NO DRIFT.
```

6. **Verify Everything Is There**
```bash
ssh user@agent-session-2.gradient.io

# Everything is already installed:
ffmpeg -version  # Already there!
python -c "import langchain; print('OK')"  # Already there!
turbo --version  # Already there!
nvcc --version  # CUDA already set up!
echo $PATH  # Custom paths already configured!

# Agent can start working immediately — zero setup time
# No "wait 8 minutes while I pip install -r requirements.txt again"
# No version drift. No "works on my machine".
```

**This is the "OH SHIT" moment.** The branch now carries a living, reproducible runtime snapshot. Every time an agent touches it, it gets smarter. No Dockerfile. No devcontainer.json. No reinstall. No drift.

**When you demo this live, people's jaws drop.** Because they instantly see: "Holy shit… the agents are finally not starting from zero every single time. The branch has memory of its own runtime now."

**This single capability is the "OH SHIT we need this for the next 5–10 years" moment.** Everything else in Gradient (MCP surface, context store of failures/fixes, multi-cloud abstraction) is just the delivery mechanism for this one mind-bending primitive.

**This is the "OH SHIT" moment.** The branch now carries a living, reproducible runtime snapshot. Every time an agent touches it, it gets smarter. No Dockerfile. No devcontainer.json. No reinstall. No drift.

**Alternative (REST API):**
```bash
curl -X POST https://api.gradient.io/v1/environments \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "trading-algo-dev",
    "provider": "aws",
    "region": "us-east-1",
    "type": "vm",
    "resources": {"cpu": "2", "memory": "4Gi", "disk": "50Gi"},
    "secrets": ["db-password", "api-key"],
    "context": {"branch": "feature/new-algo", "sync": true}
  }'
```

**Alternative (MCP - AI Agent):**
```
User: "Create a new environment on AWS for my trading algorithm with 2 CPU and 4GB memory"

AI Agent (via MCP):
→ Calls gradient_env_create tool
→ Returns: Environment env-abc123 created
```

3. **Secrets Automatically Synced**
- Gradient automatically injects secrets `db-password` and `api-key` into the environment
- Secrets are encrypted in transit and at rest
- Environment can access secrets via environment variables or mounted files

4. **Context Automatically Synced**
- Branch context for `feature/new-algo` is synced to the environment
- **Runtime environment automatically replayed:**
  - Base OS: Same as source (Ubuntu 24.04 for Linux, Windows Server 2022 for Windows, macOS 15 for macOS)
  - All packages from previous runs automatically installed
  - Global configs applied (PATH, env vars)
- Agent running in the environment can access:
  - Previous test failures
  - Attempted fixes
  - Learned patterns
  - **Exact same packages/tools as previous runs (no reinstall needed)**

5. **Agent Installs New Packages**
```bash
# Agent runs in environment
pip install pydantic-ai
apt install ffmpeg
npm install -g turbo

# Gradient automatically captures these (on env stop or periodic)
```

6. **Next Environment Creation Replays Everything**
```bash
# Create new environment on different cloud
gc env create \
  --name trading-algo-gcp \
  --provider gcp \
  --context-branch feature/new-algo

# Environment automatically has:
# - Ubuntu 24.04 base
# - pydantic-ai, ffmpeg, turbo already installed
# - Same global configs
# - Agent never needs to reinstall deps again!
```

7. **Environment Ready**
```bash
# SSH into environment
ssh user@trading-algo-dev.gradient.io

# Secrets available as env vars
echo $DB_PASSWORD
echo $API_KEY

# Context available at /gradient/context/branch.json
cat /gradient/context/branch.json

# All packages from context already installed
pip list | grep pydantic-ai  # Already there!
ffmpeg -version  # Already installed!
```

### Workflow 2: Auto-Scaling Based on Metrics

**Goal:** Enable auto-scaling for an environment that scales based on CPU/memory usage.

**Steps (CLI):**

1. **Enable Auto-Scaling**
```bash
gc env autoscale enable env-abc123 \
  --min 2 \
  --max 10 \
  --target-cpu 0.7 \
  --target-memory 0.8 \
  --scale-up-cooldown 5m \
  --scale-down-cooldown 10m

# Output: Auto-scaling enabled for env-abc123
```

**Alternative (REST API):**
```bash
curl -X POST https://api.gradient.io/v1/environments/env-abc123/autoscale \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"enabled": true, "min_replicas": 2, "max_replicas": 10, ...}'
```

**Alternative (MCP - AI Agent):**
```
User: "Enable auto-scaling for my environment, scale between 2 and 10 replicas"

AI Agent (via MCP):
→ Calls gradient_env_autoscale_enable tool
→ Returns: Auto-scaling enabled
```

2. **Gradient Monitors Metrics**
- Gradient collects metrics every 15 seconds:
  - CPU usage
  - Memory usage
  - Request rate
  - Error rate

3. **Auto-Scaling Decision**
- **Scale Up:** If CPU > 70% or memory > 80% for 5 minutes → increase replicas
- **Scale Down:** If CPU < 30% and memory < 40% for 10 minutes → decrease replicas

4. **Monitor Scaling**
```bash
# Watch environment for scaling events
gc env watch env-abc123

# Or check metrics
gc env metrics env-abc123 --cpu --memory
```

5. **Scaling Complete**
- Environment automatically scales based on metrics
- **It just works:** Turn it on and forget it. No Terraform, no manual ASG rules, no provider-specific YAML hell.
- **Battle-tested CNCF tech:** Karmada's FederatedHPA is production-ready, multi-cluster autoscaling
- Check status: `gc env status env-abc123`
- View in dashboard: https://dashboard.gradient.io/envs/env-abc123

**The Catch (Brutal Reality):**
- **Metrics-based, not ML-magic:** If your traffic is spiky, you'll still get some thrashing until you tune the cooldowns
- **First scale-up needs a node:** 1-3 minutes if Cluster Autoscaler/Karpenter has to provision new VMs
- **You still need to expose sane metrics:** CPU/memory or custom metrics from your app. Garbage in → garbage out.
- **But once you do `gc env autoscale enable`, you walk away and it just works.**

### Workflow 3: Agentic Testing with Context Persistence

**Goal:** Run agentic tests on a branch, with context inherited from previous commits.

**Steps (CLI):**

1. **Create Test Environment**
```bash
gc env create \
  --name test-feature-branch \
  --provider aws \
  --type k8s \
  --context-branch feature/new-algo

# Output: Environment env-test123 created
```

2. **Context Loaded & Runtime Environment Replayed**
- Gradient loads context for `feature/new-algo`:
  - Previous test failures (e.g., "Connection timeout in integration_test")
  - Attempted fixes (e.g., "Increased timeout to 30s" → success)
  - Learned patterns (e.g., "timeout issues → increase timeout")
  - **Runtime environment:**
    - Base OS: Ubuntu 24.04
    - All previously installed packages automatically installed
    - Global configs applied
- **Agent never needs to reinstall deps — everything is already there!**

3. **Agent Runs Tests**
- Agent in the environment runs comprehensive tests:
  - Integration tests
  - E2E tests
  - Stress tests
  - Load tests
- **No time wasted installing packages — they're already installed from context**

4. **Agent Uses Context**
- Agent sees previous failure: "Connection timeout in integration_test"
- Agent applies learned fix: "Increase timeout to 30s"
- Test passes (context helped!)

5. **Context Automatically Updated (Silent Package Capture)**
```bash
# Agent installs packages during test run
pip install langchain openai
apt install imagemagick

# Gradient automatically captures everything via container snapshots (every 15-30 min)
# Container layer diffs capture all changes reliably
# Latest snapshot available for replication (15-30 min freshness)
# Context now includes:
# - Test results
# - Attempted fixes
# - Installed packages (langchain, openai, imagemagick) — automatically captured
```

**Alternative (REST API):**
```bash
curl -X POST https://api.gradient.io/v1/context \
  -H "Authorization: Bearer $TOKEN" \
  -d @context-update.json

# Package capture is fully automatic — no API call needed
# Gradient captures in the background on env stop, destroy, or periodically
# Everything is already captured — you never have to think about it
```

6. **Next Commit Inherits Full Context**
- Next commit on `feature/new-algo` automatically inherits:
  - Updated context (test results, fixes, patterns)
  - **Exact runtime environment (OS + all packages)**
- Agent remembers:
  - What worked and what didn't
  - **What packages were needed (no reinstall)**
- Tests get smarter over time
- **Zero setup friction ever again**

### Workflow 4: Multi-Cloud Deployment

**Goal:** Deploy independent environments across multiple clouds, tagged as a logical group for redundancy. Users bring their own global load balancer (Cloudflare, etc.).

**Steps (CLI):**

1. **Create Multi-Cloud Environment Group**
```bash
gc env create \
  --name trading-algo-global \
  --providers aws,gcp \
  --regions us-east-1,us-west-1 \
  --type k8s \
  --replicas 3 \
  --strategy multi-cloud \
  --group trading-algo-global

# Output: 
# Environment env-aws-123 created on AWS (us-east-1)
# Environment env-gcp-456 created on GCP (us-west-1)
# Environments tagged as group: trading-algo-global
```

**Alternative (Config File):**
```bash
# env.yaml
name: trading-algo-global
providers: [aws, gcp]
regions: [us-east-1, us-west-1]
type: k8s
replicas: 3
strategy: multi-cloud
group: trading-algo-global

# Create from config
gc env create --config env.yaml
```

2. **Gradient Creates Independent Environments**
- Creates independent environment on AWS (us-east-1): `env-aws-123`
- Creates independent environment on GCP (us-west-1): `env-gcp-456`
- Tags both environments with group: `trading-algo-global`
- Each environment is fully independent (no automatic cross-cloud routing)

3. **Secrets Synced to All**
- Secrets automatically synced to all environments in the group
- Each environment has access to the same secrets (from your secret backend)

4. **Context Synced to All**
- Branch context synced to all environments in the group
- Agent in any environment has access to the same context

5. **User Configures Global Load Balancer**
- User configures their own global LB (Cloudflare, AWS Global Accelerator, etc.)
- User points LB to environment endpoints:
  - `env-aws-123.gradient.io` (AWS)
  - `env-gcp-456.gradient.io` (GCP)
- User handles traffic routing, failover, and anycast
- Gradient provides environment endpoints, user brings the LB

6. **List Environments in Group**
```bash
gc env list --group trading-algo-global

# Output:
# env-aws-123 (AWS, us-east-1, running)
# env-gcp-456 (GCP, us-west-1, running)
```

---

## Implementation Details

### Provider Implementations

#### AWS Provider

```go
type AWSProvider struct {
    ec2Client    *ec2.Client
    eksClient    *eks.Client
    route53Client *route53.Client
    region       string
}

func (p *AWSProvider) CreateEnv(config EnvConfig) (*Env, error) {
    switch config.Type {
    case "vm":
        return p.createEC2Instance(config)
    case "k8s":
        return p.createEKSCluster(config)
    case "container":
        return p.createECSCluster(config)
    }
}

```

#### GCP Provider

```go
type GCPProvider struct {
    computeClient *compute.Service
    gkeClient     *container.Service
    region        string
}

func (p *GCPProvider) CreateEnv(config EnvConfig) (*Env, error) {
    switch config.Type {
    case "vm":
        return p.createGCEInstance(config)
    case "k8s":
        return p.createGKECluster(config)
    }
}
```

#### Azure Provider

```go
type AzureProvider struct {
    computeClient *compute.VirtualMachinesClient
    aksClient     *containerservice.ManagedClustersClient
    region        string
}

func (p *AzureProvider) CreateEnv(config EnvConfig) (*Env, error) {
    switch config.Type {
    case "vm":
        return p.createVMInstance(config)
    case "k8s":
        return p.createAKSCluster(config)
    }
}
```

### Pre-Baked Base Images

**Base Image Strategy:**
- **One base image per OS per cloud provider:**
  - Linux (Ubuntu 24.04): AWS AMI, GCE image, Azure image
  - Windows (Server 2022): AWS AMI, Azure image, GCE image
  - macOS (15): AWS AMI, Azure image, GCE image
- **Common packages pre-installed:**
  - Language runtimes: Go 1.23, Python 3.12, Node.js 22, Rust
  - Build tools: git, curl, wget, build-essential
  - Useful tools: jq, ripgrep, fd, bat, fzf, htop
- **Base images updated monthly/quarterly** (not per context)
- **Stored in cloud provider image registries:**
  - AWS: AMIs in same region as environments
  - GCP: Images in same project/region
  - Azure: Images in same resource group/region
- **Fast boot (<30s):** Base image ready immediately, no package installation during boot

**Delta Installation:**
- **Containers (default):**
  - Dockerfile/container layers add deltas (new packages from context)
  - Container starts immediately, packages install in background layer
  - Agent can SSH in and start working while packages install
- **VMs (optional):**
  - cloud-init/cloud-config starts background systemd service
  - Service installs deltas (new packages from context) after boot
  - Agent can SSH in immediately, packages appear as they install
- **Why this works:**
  - Fast boot (<30s) — base image ready immediately
  - Lazy loading — packages install in background
  - No AMI baking per context — just one base image per OS per cloud
  - Works across clouds — same approach everywhere
  - Always current — latest snapshot applied at boot time

### Multi-Cluster Federation (K8s)

**For Kubernetes environments, Gradient uses Karmada (CNCF multi-cluster federation) under the hood.**

**Why Karmada:**
- **CNCF project:** Battle-tested, production-ready
- **Solves 70% of federation pain:** Resource propagation, scheduling, failover
- **Multi-cloud support:** Works across AWS EKS, GCP GKE, Azure AKS
- **No reinvention:** We orchestrate Karmada, don't build our own federation

**How It Works:**
1. User creates multi-cloud K8s environment group
2. Gradient creates independent K8s clusters on each cloud
3. Gradient installs Karmada control plane
4. Gradient registers clusters with Karmada
5. User deploys workloads via Karmada API (or Gradient CLI)
6. Karmada propagates workloads to all clusters
7. Karmada handles scheduling, failover, and resource management

**Gradient's Value-Add:**
- Unified CLI/API (user doesn't need to know Karmada)
- Context persistence across all clusters
- Secret sync across all clusters
- Auto-scaling decisions across all clusters (powered by Karmada's FederatedHPA)


### Secret Management

**Gradient's Secret Manager is a thin orchestrator on top of existing backends. We don't store secrets ourselves.**

#### Supported Backends

- **Vault** (HashiCorp Vault)
- **AWS Secrets Manager**
- **GCP Secret Manager**
- **Azure Key Vault**
- **Infisical**

#### How It Works

1. User configures their existing secret backend (Vault, AWS Secrets Manager, etc.)
2. Gradient connects to backend via backend's API
3. Gradient reads secrets from backend (user's backend handles encryption, audit, HSM, etc.)
4. Gradient syncs secrets to all referenced environments (our value-add)
5. Gradient injects secrets into environments (env vars, mounted files, K8s secrets)
6. Rotation handled by user's backend (Vault rotation, AWS Secrets Manager rotation, etc.)

**Why This Approach:**
- **Don't reinvent:** Users already have Vault, AWS Secrets Manager, etc. We orchestrate, not replace.
- **Trust:** Users trust their existing secret backends (audit, HSM, compliance).
- **Value-add:** Our cross-environment sync and context-aware injection is the differentiator.

#### Sync Mechanism

1. Secret created/updated in user's existing backend (Vault, AWS Secrets Manager, etc.)
2. Gradient reads from backend (via backend API)
3. Gradient syncs to all referenced environments (our value-add)
4. Injected into environment (env vars, mounted files, K8s secrets)
5. Rotation handled by user's backend (Vault rotation, AWS Secrets Manager rotation, etc.)

### Context Persistence

#### Storage Backend

- **etcd** (distributed, consistent)
- **PostgreSQL** (if SQL queries needed)
- **S3/GCS** (for large context data, compressed package lists)

#### Context Structure

```json
{
  "branch": "feature/new-algo",
  "commit_sha": "abc123",
  "previous_failures": [
    {
      "test": "integration_test",
      "error": "Connection timeout",
      "timestamp": "2025-01-15T09:00:00Z",
      "commit": "prev-commit-xyz"
    }
  ],
  "attempted_fixes": [
    {
      "fix": "Increased timeout to 30s",
      "success": true,
      "timestamp": "2025-01-15T09:15:00Z",
      "commit": "abc123"
    }
  ],
  "patterns": {
    "common_failures": ["timeout", "oom"],
    "successful_fixes": {
      "timeout": ["increase_timeout", "add_retry"],
      "oom": ["increase_memory", "reduce_concurrency"]
    }
  },
  "test_results": [
    {
      "test": "integration_test",
      "status": "passed",
      "duration": "2.3s",
      "timestamp": "2025-01-15T10:00:00Z"
    }
  ],
  "base_os": "ubuntu-24.04",
  "installed_packages": [
    {
      "manager": "apt",
      "name": "ffmpeg",
      "version": "7:6.0-1ubuntu2",
      "installed_at": "2025-01-15T09:30:00Z"
    },
    {
      "manager": "pip",
      "name": "pydantic-ai",
      "version": "0.0.14",
      "source": "pypi",
      "installed_at": "2025-01-15T09:45:00Z"
    },
    {
      "manager": "npm",
      "name": "turbo",
      "version": "2.3.0",
      "source": "npm",
      "installed_at": "2025-01-15T10:00:00Z"
    }
  ],
  "global_configs": {
    "PATH": "/usr/local/cuda-12.4/bin:$PATH",
    "CUDA_HOME": "/usr/local/cuda-12.4"
  }
}
```

#### Runtime Environment Capture

**THE Feature — The "OH SHIT" Moment:** Gradient automatically captures and replays the exact runtime environment state (OS, packages, tools, configs) as part of context. **Agents never reinstall deps again. The branch carries a living, reproducible runtime snapshot.**

**This is the feature you lead every single pitch, demo, and landing page with.** It's the thing no one else ships in 2026, and the market will feel the pain harder every month as agents get more autonomous.

**Ship the auto-capture MVP of just this (Linux/Windows/macOS), and you'll have the viral "you have to see this" moment that actually matters. The rest of the product is just scaffolding for it.**

**Why This Is The Killer Feature:**
- **Every AI agent today wastes 5–15 minutes reinstalling the same packages on every fresh environment**
- **Gradient captures what agents actually install at runtime (not what you declare upfront)**
- **Next environment on any cloud boots in <30 seconds (agent can start working), packages install in background (2-5 min total)**
- **No Dockerfile. No devcontainer.json. No reinstall. No drift.**
- **The branch itself becomes the runtime specification**
- **This is the dev-environment equivalent of Git replacing "copy the folder and email it"**

**How It Works:**

1. **Fully Automatic Capture (Container-Based):**
   - **Environments run in containers (Docker/Podman) by default** — more reliable than hooking package managers
   - **Periodic snapshots:** Every 15-30 minutes, captures container layer diffs (atomic, captures everything)
   - **On-stop final snapshot:** Final container commit captures any remaining changes
   - **Container layer diffs are reliable:** Captures all changes (packages, configs, binaries) without missing anything
   - Stores package list in context (only deltas from base OS)
   - **No manual steps. No "capture this". It's always captured.**
   - **Note:** May miss packages installed between snapshots (15-30 min windows), but most agents install in bursts

2. **Replay on Environment Create (Lazy Loading with Pre-Baked Base Images):**
   - When creating environment with `--context-branch`, Gradient:
     - **Pre-baked base images:** One base AMI/image per OS per cloud provider
       - Common packages pre-installed (Python, Node, Go, Rust, git, curl, jq, etc.)
       - Base images updated monthly/quarterly, not per context
       - Fast boot (<30s) — base image ready immediately
     - **Fast boot (<30s):** Starts with pre-baked base image immediately
     - **Agent can SSH in and start working** — doesn't wait for packages
     - **Background delta installation:** 
       - For containers: Dockerfile/container layers add deltas (new packages from context)
       - For VMs: cloud-init starts background systemd service that installs deltas
       - Packages install in background (2-5 min total)
       - Agent sees packages appear as they install
     - **Only installs deltas:** New packages from context are installed in background
     - Applies global configs (PATH, env vars) as packages install
   - **Works even if source environment is still running** — uses latest snapshot (15-30 min freshness)
   - **Cross-OS replication:** Can replicate Linux env to Windows/macOS (packages that work cross-platform are installed, OS-specific packages are skipped)

3. **Supported Package Managers:**
   - **Linux:** apt, pip, npm, cargo, go, brew (if installed)
   - **Windows:** choco, pip, npm, cargo, go
   - **macOS:** brew, pip, npm, cargo, go

4. **Capture Mechanism (Container-Based + Periodic Snapshots):**
   - **Container-based (default):** Environments run in Docker/Podman containers
     - Container layer diffs capture all changes (packages, configs, binaries)
     - More reliable than hooking package managers (no LD_PRELOAD, strace, eBPF brittleness)
     - Atomic commits capture everything at once
   - **Periodic snapshots:** Every 15-30 minutes (configurable)
     - Captures container layer diffs (fast, reliable)
     - Most agents install packages in bursts, not continuously
     - 15-30 min windows catch most changes
   - **On-stop final snapshot:** Final container commit before destroy
   - **For non-containerized VMs (optional):**
     - Use overlayfs snapshots (faster than full rsync)
     - Capture only changed files, not entire filesystem
     - Transfer diffs, not full state
   - **Nix/Devbox (optional):** Run everything under Nix for bulletproof reproducibility (future)

5. **Replay Mechanism (Pre-Baked Base Images + Background Deltas):**
   - **Pre-baked base images:** One base AMI/image per OS per cloud provider
     - Common packages pre-installed (Python, Node, Go, Rust, git, curl, jq, ripgrep, etc.)
     - Base images updated monthly/quarterly (not per context)
     - Stored in cloud provider image registries (AWS AMI, GCE image, Azure image)
   - **Delta installation:**
     - **Containers:** Dockerfile/container layers add deltas (new packages from context)
     - **VMs:** cloud-init/cloud-config starts background systemd service
     - Background service installs deltas (new packages from context)
     - Agent can SSH in immediately (<30s), packages appear as they install
   - **Why this works:**
     - Fast boot (<30s) — base image ready immediately
     - Lazy loading — packages install in background
     - No AMI baking per context — just one base image per OS per cloud
     - Works across clouds — same approach everywhere
     - Always current — latest snapshot applied at boot time

#### Default Base OS & Packages

**Linux (Default - Ubuntu 24.04 LTS):**
- **OS:** Ubuntu 24.04 LTS (rock-solid default, 95% of users never need to change)
- **Build Tools:** build-essential, git, curl, wget
- **Language Runtimes:**
  - Go 1.23
  - Python 3.12 + pip
  - Node.js 22 + npm + pnpm
  - Rust (via rustup)
- **Useful Tools:** jq, ripgrep, fd, bat, fzf, htop
- **Package Managers:** apt, pip, npm, cargo, go

**Windows (Windows Server 2022):**
- **OS:** Windows Server 2022
- **Build Tools:** Git for Windows, curl, PowerShell
- **Language Runtimes:**
  - Go 1.23
  - Python 3.12 + pip
  - Node.js 22 + npm + pnpm
  - Rust (via rustup)
- **Useful Tools:** ripgrep, fd, bat (via cargo/choco)
- **Package Managers:** choco, pip, npm, cargo, go

**macOS (macOS 15):**
- **OS:** macOS 15 (via cloud provider macOS VMs)
- **Build Tools:** Xcode Command Line Tools, git, curl
- **Language Runtimes:**
  - Go 1.23
  - Python 3.12 + pip
  - Node.js 22 + npm + pnpm
  - Rust (via rustup)
- **Useful Tools:** jq, ripgrep, fd, bat, fzf, htop (via brew)
- **Package Managers:** brew, pip, npm, cargo, go

**Why These Defaults:**
- **95% of users never need to customize:** Covers most dev workflows out of the box
- **Opinionated but flexible:** Can override with `--os` flag or custom base image
- **Pre-baked in base images:** Common packages pre-installed for fast boot
- **Small context size:** Only deltas from base are stored (keeps context small)
- **Fast boot:** Base images boot in <30s, packages install in background
- **Cross-platform:** Same language runtimes across all OSes for consistency

**Windows:**
- **OS:** Windows Server 2022
- **Package Managers:** choco, pip, npm, cargo, go
- **Cloud Support:** AWS (EC2 Windows instances), Azure (Windows VMs), GCP (Windows Server VMs)

**macOS:**
- **OS:** macOS 15 (via cloud provider macOS VMs)
- **Package Managers:** brew, pip, npm, cargo, go
- **Cloud Support:** AWS (macOS EC2 instances), Azure (macOS VMs), GCP (macOS VMs)

**The Key Insight:** We don't try to predict what agents need. We give them a solid base, let them install whatever they want, and capture it. The branch becomes the runtime specification.

#### Sync Mechanism

1. Context saved to Gradient (via API, agent, or auto-capture)
2. Stored in distributed backend (etcd/PostgreSQL)
3. Synced to environments that reference the branch
4. Available to agents running in environments
5. Updated on each commit/test run
6. **Package state automatically captured via container snapshots** — every 15-30 minutes, container layer diffs are captured
7. **Context is current enough** — latest snapshot (15-30 min freshness) available for replication, final snapshot on stop catches everything
8. **Container-based capture is reliable** — no hook brittleness, captures everything in container layers

#### Package Capture Details

**Capture Mechanism (Container-Based):**
- **Container-based (default):** All environments run in Docker/Podman containers
  - Container layer diffs capture all changes reliably (packages, configs, binaries, files)
  - No need to hook package managers (avoids brittleness of LD_PRELOAD, strace, eBPF)
  - Atomic commits capture everything at once
- **Periodic snapshots:** Every 15-30 minutes (configurable)
  - Captures container layer diffs (atomic, fast)
  - Most agents install packages in bursts, so 15-30 min windows catch most changes
- **On-stop final snapshot:** Final container commit before destroy
- **Best effort:** May miss packages installed between snapshots, but final snapshot catches everything
- **No manual steps:** Everything is captured automatically — you never have to think about it

**What Gets Captured (Everything That Matters):**
- **Container layer diffs:** Captures all changes (packages, configs, binaries, files)
- **Packages:** name, version, manager, source, installation timestamp
- **Custom binaries:** GitHub releases, manual installs (captured in container layers)
- **CUDA drivers:** Version, installation path, env vars (captured in container layers)
- **Global configs:** PATH tweaks, env vars, .bashrc/.zshrc changes (captured in container layers)
- **Only deltas from base OS** (keeps context small)
- **Container-based capture is reliable:** No missed packages, no hook brittleness

**What Doesn't Get Captured (v1):**
- **Container-based capture catches everything:** Packages, configs, binaries, file edits — all captured in container layers
- **Best effort between snapshots:** Packages installed between 15-30 min snapshots may be missed, but final snapshot catches everything
- **System-level changes outside containers:** Only applies to non-containerized VMs (optional)

**Context Size Management:**
- Compress container layer diffs (gzip)
- Store in S3/GCS for large contexts (>100 packages or large binaries)
- etcd/PostgreSQL for metadata, S3 for container layer diffs
- **Typical context size:** 50-200 packages = 10-50KB compressed (tiny)
- **Container layers are efficient:** Only changed files are stored, not full filesystem

**The Magic:** Agent installs 47 packages, we capture it via container snapshots, next env on any cloud boots in <30s using pre-baked base image (agent can start working immediately), deltas install in background (2-5 min total). **This is the "OH SHIT" moment.**

---

## Technology Stack

### Core Orchestration Engine
- **Language:** Go 1.21+
- **Libraries:**
  - AWS SDK: `github.com/aws/aws-sdk-go-v2`
  - GCP SDK: `cloud.google.com/go`
  - Azure SDK: `github.com/Azure/azure-sdk-for-go`
  - Kubernetes: `k8s.io/client-go`
  - gRPC: `google.golang.org/grpc`
  - etcd: `go.etcd.io/etcd/client/v3`
  - Docker: `github.com/docker/docker/client` (for container-based capture)
  - Podman: `github.com/containers/podman/v4/pkg/bindings` (alternative container runtime)

### CLI
- **Language:** Go
- **Framework:** Cobra (CLI framework)
- **Installation:** Homebrew, curl script, or direct binary

### API Layer
- **Framework:** Go (HTTP server + gRPC)
- **API:** REST API (HTTP) + gRPC (for high-performance)
- **Validation:** Go struct validation
- **Auth:** JWT tokens

### MCP Server
- **Language:** Go
- **Protocol:** Model Context Protocol (MCP)
- **Integration:** Exposes all CLI operations as MCP tools

### Dashboard
- **Framework:** Simple HTML/JS (or lightweight framework)
- **Purpose:** Read-only monitoring
- **Data Source:** REST API (read-only endpoints)

### Data Storage
- **Context:** etcd or Consul (for distributed context storage)
- **Metadata:** PostgreSQL (environment metadata, audit logs)
- **Secrets:** User's existing backend (Vault, AWS Secrets Manager, GCP Secret Manager, etc.)

---

## Security Considerations

### Authentication & Authorization
- API keys or OAuth 2.0 for API access
- RBAC for environment access control
- Provider credentials stored encrypted

### Encryption
- Secrets encrypted at rest (AES-256)
- All API traffic encrypted (TLS 1.3)

### Network Security
- Environments isolated by default
- VPN or private networking for cross-cloud
- Firewall rules enforced

### Audit Logging
- All operations logged (create, destroy, scale)
- Logs stored for compliance (7 years)
- Real-time alerting on suspicious activity

---

## Performance Targets

### Environment Operations
- **Create (boot):** < 30 seconds (container ready, agent can SSH in)
- **Create (full):** 2-5 minutes (packages install in background)
- **Destroy:** < 2 minutes
- **Scale:** < 1 minute per replica
- **K8s cluster:** < 10 minutes

### Interface Latency
- **CLI:** < 500ms (command execution, not including cloud operations)
- **REST API:** < 100ms (p95)
- **gRPC:** < 50ms (p95)
- **MCP:** < 200ms (p95)

---

## Future Enhancements

**Priority 1 (The "OH SHIT" Feature):**
1. **Enhanced OS Support:** Additional Windows/macOS base images and optimizations
2. **Config File Capture:** Capture .config/ directory, custom config files (not just packages)
3. **Package Security Scanning:** Scan captured packages for vulnerabilities before replay
4. **Nix/Devbox Integration:** Optional bulletproof reproducibility via Nix (for teams that want it)

**Priority 2 (Delivery Mechanism):**
5. **GPU Support:** GPU workload scaling and management
6. **Edge Computing:** Deploy to edge locations (Cloudflare Workers, AWS Lambda@Edge)
7. **Cost Optimization:** Automatic cost-based scaling (scale down during low usage)
8. **Multi-Region Replication:** Automatic replication across regions
9. **Disaster Recovery:** Automated backup and recovery
10. **Observability:** Integrated metrics, logs, traces
11. **Policy Engine:** Fine-grained policies for scaling, secrets, package allow-lists

**The Strategy:** Ship the Linux-default + auto-capture MVP of runtime environment capture first. This is the viral "you have to see this" moment. Everything else is scaffolding for it.

---

**Document Status:** This spec is a living document. Update as we implement and learn from real-world usage.
