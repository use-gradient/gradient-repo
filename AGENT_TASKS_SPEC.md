# Gradient Agent Tasks: Linear + Claude Code Integration

## Complete Engineering Specification

**Version:** 1.0
**Date:** 2026-03-06
**Status:** Draft

---

## Table of Contents

1. [How the GitHub Integration Works Today](#1-how-the-github-integration-works-today)
2. [Vision: The Agent Task Flow](#2-vision-the-agent-task-flow)
3. [Architecture Overview](#3-architecture-overview)
4. [Linear Integration](#4-linear-integration)
5. [Claude Code Integration](#5-claude-code-integration)
6. [Task Orchestrator Service](#6-task-orchestrator-service)
7. [Database Schema](#7-database-schema)
8. [API Endpoints](#8-api-endpoints)
9. [CLI Commands](#9-cli-commands)
10. [Web Dashboard](#10-web-dashboard)
11. [Context & Live Sharing Flow](#11-context--live-sharing-flow)
12. [Security & Secrets](#12-security--secrets)
13. [Token Strategy: What Goes Where](#13-token-strategy-what-goes-where)
14. [What We Need From You](#14-what-we-need-from-you)
15. [Implementation Plan](#15-implementation-plan)

---

## 1. How the GitHub Integration Works Today

### Architecture

```
GitHub App (webhook) ──→ POST /api/v1/webhooks/github ──→ RepoService
                                                              │
                                                    ┌────────┴─────────┐
                                                    │                  │
                                              installation        create (branch)
                                              events              events
                                                    │                  │
                                              Save to DB        autoForkContext()
                                              (github_         autoForkSnapshot()
                                              installations)         │
                                                              Copies parent branch
                                                              context + snapshots
                                                              to new branch
```

### Flow

1. **Install GitHub App** → GitHub sends `installation` webhook → Gradient stores installation ID + repo list in `github_installations` table.
2. **Connect repo** → User runs `gc repo connect --repo owner/repo` → API looks up the installation, creates a `repo_connections` row linking org ↔ repo. Auto-fork and auto-snapshot are enabled by default.
3. **Branch created** → GitHub sends `create` webhook (ref_type=branch) → `handleBranchCreate` finds all orgs connected to that repo → for each org with `auto_fork_enabled`, copies the parent branch's context (installed packages, failures, fixes, patterns, configs) and latest snapshot pointer to the new branch. This is a cheap fork — no image copy, just a new DB row pointing to the same container image.
4. **Push event** → GitHub sends `push` webhook → updates `commit_sha` on the context row → if `auto_snapshot_on_push` is enabled and there's a running environment for that branch, triggers an async container commit snapshot.
5. **Branch deleted** → GitHub sends `delete` webhook → soft-delete (context/snapshots preserved for history, just logged).

### Key DB Tables

- **`github_installations`** — Raw GitHub App data (installation_id, account_login, repo list)
- **`repo_connections`** — Links org_id ↔ repo (installation_id, auto_fork_enabled, auto_snapshot_on_push)

### Key Patterns We Reuse

- **Webhook-driven** — External service calls us, we verify signature, dispatch to handler
- **One org ↔ one repo connection** (UNIQUE constraint on org_id + repo_full_name)
- **Auto-fork on branch create** — Copy context from parent → child
- **Async operations** — Snapshots triggered in goroutines
- **Live Context Mesh** — NATS JetStream for real-time event sharing between environments

---

## 2. Vision: The Agent Task Flow

```
┌──────────┐     webhook      ┌────────────┐    spawn env    ┌──────────────────┐
│  Linear  │ ──────────────→  │  Gradient   │ ─────────────→ │  Gradient Env    │
│  (tasks) │                  │  Task       │                │  (Docker on EC2/ │
│          │ ←────────────── │  Orchestrator│ ←───────────── │   Hetzner)       │
│  status  │   update issue   │             │   context out  │                  │
│  updates │                  │             │                │  Claude Code CLI │
└──────────┘                  └─────┬───────┘                │  runs inside     │
                                    │                        │  with repo cloned│
                                    │                        │  + secrets injected│
                                    │                        └──────────────────┘
                                    │
                              ┌─────┴───────┐
                              │  Live       │
                              │  Context    │
                              │  Mesh       │
                              │  (NATS)     │
                              └─────────────┘
```

### The Complete Loop

1. User creates tasks in **Linear** (or bulk-creates via Linear API)
2. Linear webhook fires → Gradient receives it → **Task Orchestrator** picks up new/updated issues
3. For each task, Orchestrator:
   - Spins up a **Gradient environment** (or reuses one)
   - Clones the connected **GitHub repo** into the environment
   - Injects **secrets** (Anthropic API key, GitHub token, env vars)
   - Launches **Claude Code CLI** in headless mode (`claude --print` or the Claude Code SDK) with:
     - The task description as the prompt
     - The repo context
     - Gradient's saved context (packages, patterns, previous failures)
   - Claude Code works on the task, makes changes, commits
4. During execution, **Live Context Mesh** streams events (packages installed, errors, files changed) to all other environments on the same branch
5. When Claude Code finishes:
   - **Context is saved** (what was installed, what was tried, what worked)
   - **Summary is generated** (what changed, what was done)
   - **Linear issue is updated** (status → "Done", comment with summary)
   - **PR is created** on GitHub (optional, configurable)
   - **Snapshot is taken** for instant replay next time
6. User can watch all of this in real-time on the Gradient dashboard

---

## 3. Architecture Overview

### New Services

```
gradient/
├── internal/
│   ├── services/
│   │   ├── linear_service.go       # Linear API + webhook handling
│   │   ├── task_service.go         # Task orchestration (the brain)
│   │   └── claude_service.go       # Claude Code execution management
│   ├── api/
│   │   └── server.go               # New routes: /tasks, /integrations/linear, etc.
│   └── models/
│       └── models.go               # New models: Task, LinearConnection, ClaudeConfig
├── cmd/
│   └── cli/commands/
│       ├── task.go                  # gc task list/run/status/cancel
│       └── integration.go          # gc integration linear/claude
└── internal/db/
    └── schema.sql                   # New tables
```

---

## 4. Linear Integration

### How Linear's API Works

- **API**: GraphQL at `https://api.linear.app/graphql`
- **Authentication**: Two options:
  - **Personal API Key** — User generates at linear.app/settings/api → simple Bearer token
  - **OAuth2** — Linear OAuth app, user authorizes, gets access_token + refresh_token
- **Webhooks**: Linear can send webhooks on issue create/update/delete to a URL you configure
  - Webhook payload includes: action, type, data (the issue object), url, createdAt
  - Signed with a shared secret (HMAC)
  - Configured per-workspace at linear.app/settings/api → Webhooks

### Linear Data Model

- **Workspace** → Teams → Projects → Issues (with labels, assignees, states, etc.)
- **Issue states**: Backlog, Todo, In Progress, Done, Cancelled (customizable per team)
- **Labels**: User-defined. We'll use a label like `gradient-agent` or `auto-task` to identify which issues should be picked up

### Our Linear Integration Design

**One Linear workspace connection per org.** This mirrors the GitHub pattern (one repo per org).

#### Connection Flow

1. User goes to Settings → Integrations → "Connect Linear"
2. Gradient initiates OAuth2 flow → user authorizes → we get access_token + refresh_token
3. We store the tokens encrypted in `linear_connections` table
4. We auto-create a webhook in their Linear workspace pointing to `POST /api/v1/webhooks/linear`
5. User configures which team/project/label to watch (or "all issues assigned to me")

#### Webhook Events We Handle

| Event | Action |
|-------|--------|
| `Issue.create` | If issue matches filter (label/team/project) → queue task |
| `Issue.update` | If state changed to a "trigger" state (e.g., "Todo") → queue task. If cancelled → cancel running task |
| `Issue.delete` | Cancel running task if any |

#### OAuth2 Flow (Recommended over Personal API Key)

**Why OAuth2 over Personal API Keys:**
- Scoped permissions (read issues, write comments)
- Refresh tokens (don't expire)
- Revocable per-app (not per-user)
- Shows up in Linear's "Authorized applications" for visibility

**Linear OAuth2 Parameters:**

| Parameter | Value |
|-----------|-------|
| Authorization URL | `https://linear.app/oauth/authorize` |
| Token URL | `https://api.linear.app/oauth/token` |
| Scopes | `read`, `write`, `issues:create`, `comments:create` |
| Redirect URI | `https://app.usegradient.dev/integrations/linear/callback` |
| Grant Type | `authorization_code` |

**What we need from Linear:**
- Create an OAuth application at https://linear.app/settings/api → "OAuth applications"
- This gives us a `client_id` and `client_secret`
- Set the redirect URI to our callback endpoint

### Linear GraphQL Queries We'll Use

```graphql
# Get issue details
query Issue($id: String!) {
  issue(id: $id) {
    id
    identifier    # e.g. "GRAD-42"
    title
    description
    state { name }
    assignee { name email }
    labels { nodes { name } }
    team { name key }
    project { name }
    branchName    # Linear auto-generates branch names!
    url
  }
}

# Update issue state
mutation UpdateIssue($id: String!, $stateId: String!) {
  issueUpdate(id: $id, input: { stateId: $stateId }) {
    success
    issue { id state { name } }
  }
}

# Add comment with summary
mutation CreateComment($issueId: String!, $body: String!) {
  commentCreate(input: { issueId: $issueId, body: $body }) {
    success
    comment { id body }
  }
}

# List team workflow states (to find "In Progress", "Done" state IDs)
query TeamStates($teamId: String!) {
  team(id: $teamId) {
    states { nodes { id name type } }
  }
}
```

---

## 5. Claude Code Integration

### How Claude Code CLI Works

Claude Code is Anthropic's agentic coding tool that runs in the terminal. Key capabilities:

- **Interactive mode**: `claude` — opens a REPL
- **Headless/print mode**: `claude --print "prompt"` or `claude -p "prompt"` — runs non-interactively, outputs result, exits
- **SDK mode**: `claude-code-sdk` (TypeScript/Python) — programmatic control
- **Authentication**: Uses `ANTHROPIC_API_KEY` environment variable
- **Model selection**: `--model claude-sonnet-4-20250514` flag or `CLAUDE_MODEL` env var
- **Context**: Can read files, run commands, edit files, use tools — all within the working directory
- **Settings**: `.claude/settings.json` for allowed/denied tools, permissions
- **Session**: Supports `--session-id` for resuming conversations
- **Output**: `--output-format json` for structured output parsing

### Headless Execution Pattern

```bash
# Inside the Gradient environment:
cd /workspace/repo

# Run Claude Code headlessly on a task
claude -p "$(cat /tmp/task-prompt.md)" \
  --model claude-sonnet-4-20250514 \
  --output-format json \
  --allowedTools "Edit,Write,Bash,Read" \
  --max-turns 50 \
  2>/tmp/claude-stderr.log \
  1>/tmp/claude-output.json
```

### What Claude Code Needs in the Environment

1. **Node.js 18+** — Claude Code is a Node.js CLI tool
2. **`ANTHROPIC_API_KEY`** — The API key for the Anthropic API
3. **Git configured** — user.name, user.email, credentials for pushing
4. **Repo cloned** — The working directory with the codebase
5. **Tool permissions** — `.claude/settings.json` with allowed tools

### Token Strategy for Claude Code

**Recommendation: One Anthropic API key per org.**

| Option | Pros | Cons |
|--------|------|------|
| **Per-org** (recommended) | Simple, one bill, easy to manage, shared usage pool | Single point of failure, can't per-user rate limit |
| Per-user | Per-user billing, per-user rate limits | Complex, users manage own keys, hard to audit |
| Per-task | Maximum isolation | Impractical, too many keys |

**Why per-org:**
- Org admins manage one API key
- Usage is billed to the org (which maps to Gradient's billing model)
- Can be rotated in one place
- Rate limits are per-key anyway — one org shouldn't need multiple
- Users who want their own key can override via task config

**Fallback:** Allow per-user override. If a user provides their own Anthropic key in their profile, use that instead. This handles the case where a user's personal key has higher rate limits or a different tier.

---

## 6. Task Orchestrator Service

The **Task Service** is the brain. It coordinates Linear issues → Gradient environments → Claude Code execution.

### Task Lifecycle

```
┌──────────┐     ┌──────────┐     ┌──────────┐     ┌──────────┐     ┌──────────┐
│ PENDING  │ ──→ │ QUEUED   │ ──→ │ RUNNING  │ ──→ │ COMPLETE │     │ FAILED   │
│          │     │          │     │          │     │          │     │          │
│ Linear   │     │ Env is   │     │ Claude   │     │ Summary  │     │ Retried  │
│ issue    │     │ spinning │     │ Code is  │     │ posted   │     │ or       │
│ received │     │ up       │     │ working  │     │ to Linear│     │ cancelled│
└──────────┘     └──────────┘     └──────────┘     └──────────┘     └──────────┘
                                        │
                                        ├──→ FAILED (can retry)
                                        └──→ CANCELLED (by user or Linear)
```

### Task States

| State | Description |
|-------|-------------|
| `pending` | Issue received from Linear, waiting to be scheduled |
| `queued` | Environment is being provisioned |
| `running` | Claude Code is actively working |
| `complete` | Task finished successfully, summary posted |
| `failed` | Task failed (timeout, Claude error, env error) |
| `cancelled` | User or system cancelled the task |

### Execution Strategy: Instance Allocation

**User-configurable per-org:**

| Strategy | Description | Best for |
|----------|-------------|----------|
| `one_per_task` (default) | Each task gets its own environment | Isolation, parallel work on different branches |
| `shared_branch` | Tasks on the same branch share one environment | Sequential work, lower cost |
| `single_instance` | All tasks share one environment | Budget-conscious, simple repos |

**Auto mode** (`auto`): Gradient decides based on:
- If tasks are on different branches → `one_per_task`
- If tasks are on the same branch → `shared_branch`
- If the org is on free tier → `single_instance`

### Concurrency Control

- **Per-org concurrency limit**: Default 3 concurrent tasks (configurable, tied to billing tier)
- **Free tier**: 1 concurrent task
- **Paid tier**: Up to 10 concurrent tasks
- **Queue**: Tasks beyond the limit are queued (FIFO)

### Task Execution Script

When a task is scheduled, the Orchestrator:

```
1. Create/reuse Gradient environment (with branch context + snapshot)
2. Wait for env to be ready (SSH accessible)
3. SSH into the environment and run the setup script:
   a. Clone the repo (if not already cloned from snapshot)
      git clone https://<github-token>@github.com/<org>/<repo>.git /workspace/repo
      cd /workspace/repo
      git checkout <branch>  # branch from Linear issue, or default
   b. Inject secrets
      export ANTHROPIC_API_KEY=<org's key>
      export GITHUB_TOKEN=<org's GitHub token>
   c. Install Claude Code (if not in snapshot)
      npm install -g @anthropic-ai/claude-code
   d. Write the task prompt
      cat > /tmp/task-prompt.md << 'EOF'
      ## Task: <Linear issue title>
      <Linear issue description>
      
      ## Context
      - Repository: <repo>
      - Branch: <branch>
      - Previous context: <Gradient context summary>
      
      ## Instructions
      - Make the necessary code changes
      - Run tests if applicable
      - Commit your changes with a descriptive message
      - Reference the Linear issue: <identifier>
      EOF
   e. Run Claude Code
      claude -p "$(cat /tmp/task-prompt.md)" \
        --output-format json \
        --max-turns 50 \
        > /tmp/claude-output.json 2>/tmp/claude-stderr.log
   f. Parse output, extract summary
   g. Push changes to GitHub
      git add -A
      git commit -m "<identifier>: <summary>"
      git push origin <branch>
   h. Optionally create PR
4. Save context (packages installed, patterns learned, etc.)
5. Take snapshot
6. Post summary to Linear as a comment
7. Update Linear issue state → "Done" (or "Failed")
8. Destroy environment (if one_per_task strategy) or keep alive
```

---

## 7. Database Schema

### New Tables

```sql
-- Linear workspace connections (one per org)
CREATE TABLE IF NOT EXISTS linear_connections (
    id VARCHAR(255) PRIMARY KEY,
    org_id VARCHAR(255) NOT NULL UNIQUE,  -- one Linear connection per org
    
    -- OAuth2 tokens (encrypted at rest)
    access_token TEXT NOT NULL,
    refresh_token TEXT,
    token_expires_at TIMESTAMP,
    
    -- Workspace info (cached from Linear API)
    workspace_id VARCHAR(255),
    workspace_name VARCHAR(255),
    
    -- Webhook config
    webhook_id VARCHAR(255),          -- Linear webhook ID (for cleanup)
    webhook_secret VARCHAR(255),      -- HMAC secret for verifying Linear webhooks
    
    -- Filter config: which issues to pick up
    filter_team_ids JSONB DEFAULT '[]',       -- ["team-id-1", "team-id-2"] or empty = all
    filter_project_ids JSONB DEFAULT '[]',    -- specific projects or empty = all
    filter_label_names JSONB DEFAULT '["gradient-agent"]',  -- label filter
    trigger_state VARCHAR(255) DEFAULT 'Todo', -- which state triggers task creation
    
    -- Status
    status VARCHAR(50) NOT NULL DEFAULT 'active', -- active, paused, disconnected
    
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- Claude Code configuration (one per org, with optional per-user overrides)
CREATE TABLE IF NOT EXISTS claude_configs (
    id VARCHAR(255) PRIMARY KEY,
    org_id VARCHAR(255) NOT NULL,
    user_id VARCHAR(255),            -- NULL = org default, non-NULL = user override
    
    -- Anthropic API key (encrypted at rest)
    anthropic_api_key TEXT NOT NULL,
    
    -- Model preferences
    model VARCHAR(100) DEFAULT 'claude-sonnet-4-20250514',
    max_turns INTEGER DEFAULT 50,
    
    -- Tool permissions
    allowed_tools JSONB DEFAULT '["Edit","Write","Bash","Read"]',
    
    -- Cost controls
    max_cost_per_task NUMERIC(10,2),          -- max $ per task (estimated from tokens)
    max_tokens_per_task INTEGER DEFAULT 100000, -- max output tokens per task
    
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),
    
    UNIQUE(org_id, user_id)  -- one config per org (user_id=NULL) or per user
);

-- Agent tasks (the core table)
CREATE TABLE IF NOT EXISTS agent_tasks (
    id VARCHAR(255) PRIMARY KEY,
    org_id VARCHAR(255) NOT NULL,
    
    -- Linear issue link
    linear_issue_id VARCHAR(255),
    linear_identifier VARCHAR(50),    -- e.g. "GRAD-42"
    linear_url VARCHAR(500),
    
    -- Task content
    title VARCHAR(500) NOT NULL,
    description TEXT,
    prompt TEXT,                       -- the full prompt sent to Claude Code
    
    -- Execution
    environment_id VARCHAR(255),       -- Gradient environment used
    branch VARCHAR(255),               -- git branch
    repo_full_name VARCHAR(500),       -- GitHub repo
    
    -- Status
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    -- pending, queued, running, complete, failed, cancelled
    
    -- Results
    output_summary TEXT,               -- AI-generated summary of what was done
    output_json JSONB,                 -- full Claude Code JSON output
    commit_sha VARCHAR(40),            -- commit SHA of the changes
    pr_url VARCHAR(500),               -- PR URL if created
    error_message TEXT,                -- error if failed
    
    -- Execution metadata
    started_at TIMESTAMP,
    completed_at TIMESTAMP,
    duration_seconds INTEGER,
    tokens_used INTEGER,
    estimated_cost NUMERIC(10,4),
    retry_count INTEGER DEFAULT 0,
    max_retries INTEGER DEFAULT 2,
    
    -- Context
    context_saved BOOLEAN DEFAULT false,
    snapshot_taken BOOLEAN DEFAULT false,
    
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_agent_tasks_org ON agent_tasks(org_id);
CREATE INDEX IF NOT EXISTS idx_agent_tasks_status ON agent_tasks(org_id, status);
CREATE INDEX IF NOT EXISTS idx_agent_tasks_linear ON agent_tasks(linear_issue_id);
CREATE INDEX IF NOT EXISTS idx_agent_tasks_env ON agent_tasks(environment_id);

-- Task execution log (detailed step-by-step log)
CREATE TABLE IF NOT EXISTS task_execution_log (
    id VARCHAR(255) PRIMARY KEY,
    task_id VARCHAR(255) NOT NULL REFERENCES agent_tasks(id),
    step VARCHAR(100) NOT NULL,       -- 'env_create', 'repo_clone', 'claude_start', 'claude_done', etc.
    status VARCHAR(50) NOT NULL,      -- 'started', 'completed', 'failed'
    message TEXT,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_task_log_task ON task_execution_log(task_id, created_at);

-- Org integration settings (task execution preferences)
CREATE TABLE IF NOT EXISTS task_settings (
    org_id VARCHAR(255) PRIMARY KEY,
    
    -- Instance allocation strategy
    instance_strategy VARCHAR(50) DEFAULT 'one_per_task',
    -- one_per_task, shared_branch, single_instance, auto
    
    -- Concurrency
    max_concurrent_tasks INTEGER DEFAULT 3,
    
    -- Environment defaults
    default_env_size VARCHAR(50) DEFAULT 'small',
    default_env_provider VARCHAR(50) DEFAULT 'hetzner',
    default_env_region VARCHAR(100) DEFAULT 'fsn1',
    
    -- Auto-PR
    auto_create_pr BOOLEAN DEFAULT true,
    pr_base_branch VARCHAR(255) DEFAULT 'main',
    
    -- Auto-destroy
    auto_destroy_env BOOLEAN DEFAULT true,  -- destroy env after task completes
    env_ttl_minutes INTEGER DEFAULT 30,     -- keep env alive for N minutes after completion
    
    -- Notifications
    notify_on_complete BOOLEAN DEFAULT true,
    notify_on_failure BOOLEAN DEFAULT true,
    
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);
```

---

## 8. API Endpoints

### Integration Management

```
# Linear
POST   /api/v1/integrations/linear/connect      # Start OAuth flow
GET    /api/v1/integrations/linear/callback       # OAuth callback
GET    /api/v1/integrations/linear/status          # Get connection status
PUT    /api/v1/integrations/linear/config          # Update filter/trigger config
DELETE /api/v1/integrations/linear/disconnect      # Disconnect Linear
POST   /api/v1/webhooks/linear                     # Linear webhook receiver (NO AUTH, HMAC verified)

# Claude Code
POST   /api/v1/integrations/claude/config          # Set Anthropic API key + settings
GET    /api/v1/integrations/claude/config           # Get config (key masked)
DELETE /api/v1/integrations/claude/config           # Remove config
POST   /api/v1/integrations/claude/test             # Test API key validity
```

### Task Management

```
GET    /api/v1/tasks                               # List tasks (with filters: status, branch, since)
GET    /api/v1/tasks/:id                            # Get task details
POST   /api/v1/tasks                                # Create task manually (without Linear)
POST   /api/v1/tasks/:id/cancel                     # Cancel a running task
POST   /api/v1/tasks/:id/retry                      # Retry a failed task
GET    /api/v1/tasks/:id/logs                        # Get execution logs
GET    /api/v1/tasks/:id/output                      # Get Claude Code output
GET    /api/v1/tasks/stream                          # SSE stream of task updates

# Task settings
GET    /api/v1/tasks/settings                        # Get org task settings
PUT    /api/v1/tasks/settings                        # Update org task settings
```

### Webhook Handler

```
POST   /api/v1/webhooks/linear                      # Receives Linear webhook events
```

The Linear webhook handler mirrors the GitHub pattern:

```go
func (s *Server) handleLinearWebhook(w http.ResponseWriter, r *http.Request) {
    body, _ := io.ReadAll(r.Body)
    signature := r.Header.Get("Linear-Signature")
    
    // Verify HMAC signature
    if !s.linearService.VerifyWebhookSignature(body, signature) {
        writeError(w, http.StatusUnauthorized, "invalid webhook signature")
        return
    }
    
    // Parse and dispatch
    if err := s.linearService.HandleWebhookEvent(r.Context(), body); err != nil {
        writeError(w, http.StatusInternalServerError, "webhook processing failed")
        return
    }
    
    w.WriteHeader(http.StatusOK)
}
```

---

## 9. CLI Commands

```bash
# Task management
gc task list                          # List tasks (--status pending/running/complete/failed)
gc task run --title "Fix bug"         # Create + run a task manually (no Linear needed)
gc task run --linear GRAD-42          # Run a specific Linear issue
gc task status <task-id>              # Get task status + output summary
gc task cancel <task-id>              # Cancel a running task
gc task retry <task-id>               # Retry a failed task
gc task logs <task-id>                # Stream execution logs

# Integration management
gc integration linear connect         # Start Linear OAuth flow (opens browser)
gc integration linear status          # Show connection status + filter config
gc integration linear config \
  --team "Backend" \
  --label "gradient-agent" \
  --trigger-state "Todo"              # Configure which issues to pick up
gc integration linear disconnect      # Disconnect Linear

gc integration claude set-key         # Set Anthropic API key (prompts securely)
gc integration claude config \
  --model claude-sonnet-4-20250514 \
  --max-turns 50                      # Configure Claude Code settings
gc integration claude test            # Test API key validity

# Task settings
gc task settings                      # Show current settings
gc task settings set \
  --strategy one_per_task \
  --concurrency 5 \
  --auto-pr true \
  --env-size medium                   # Update settings
```

---

## 10. Web Dashboard

### New UI Sections

#### Integrations Page (Settings → Integrations)

```
┌─────────────────────────────────────────────────────────────┐
│ Integrations                                                 │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│ ┌─────────────────────────┐  ┌─────────────────────────┐   │
│ │ 🔗 GitHub               │  │ 📋 Linear               │   │
│ │ Connected: owner/repo   │  │ Connected: My Workspace  │   │
│ │ Auto-fork: ✅           │  │ Watching: #gradient-agent│   │
│ │ [Manage]                │  │ [Manage]                 │   │
│ └─────────────────────────┘  └─────────────────────────┘   │
│                                                              │
│ ┌─────────────────────────┐  ┌─────────────────────────┐   │
│ │ 🤖 Claude Code          │  │ 📦 More coming soon     │   │
│ │ API Key: sk-...•••••    │  │ Jira, Notion, Slack     │   │
│ │ Model: claude-sonnet    │  │                         │   │
│ │ [Manage]                │  │                         │   │
│ └─────────────────────────┘  └─────────────────────────┘   │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

#### Tasks Page (New top-level tab)

```
┌─────────────────────────────────────────────────────────────┐
│ Agent Tasks                                    [+ New Task]  │
├─────────────────────────────────────────────────────────────┤
│ ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐        │
│ │ All (12) │ │ Running 3│ │ Queued 2 │ │ Done 7   │        │
│ └──────────┘ └──────────┘ └──────────┘ └──────────┘        │
│                                                              │
│ ┌───┬────────────┬───────────┬──────────┬─────────┬──────┐ │
│ │ ● │ GRAD-42    │ Fix auth  │ running  │ 3m 12s  │ env-a│ │
│ │ ● │ GRAD-43    │ Add tests │ running  │ 1m 45s  │ env-b│ │
│ │ ● │ GRAD-44    │ Refactor  │ queued   │ --      │ --   │ │
│ │ ✓ │ GRAD-41    │ Fix CSS   │ complete │ 5m 22s  │ --   │ │
│ │ ✓ │ GRAD-40    │ Add API   │ complete │ 8m 11s  │ --   │ │
│ └───┴────────────┴───────────┴──────────┴─────────┴──────┘ │
└─────────────────────────────────────────────────────────────┘
```

#### Task Detail View

```
┌─────────────────────────────────────────────────────────────┐
│ ← Back    GRAD-42: Fix authentication timeout bug            │
├─────────────────────────────────────────────────────────────┤
│ Status: ● Running (3m 12s)                                   │
│ Branch: fix/auth-timeout                                     │
│ Environment: env-abc123 (small, hetzner/fsn1)               │
│ Linear: https://linear.app/team/issue/GRAD-42               │
├─────────────────────────────────────────────────────────────┤
│ Execution Log (live):                                        │
│ 15:03:21 ✓ Environment created (2.1s)                       │
│ 15:03:24 ✓ Repo cloned (1.8s)                              │
│ 15:03:26 ✓ Context restored (0.3s)                          │
│ 15:03:27 ● Claude Code started...                           │
│ 15:04:12   → Edited src/auth/session.go                     │
│ 15:04:15   → Edited src/auth/middleware.go                   │
│ 15:04:22   → Running tests...                               │
│ 15:04:45   → All tests passing                              │
│ 15:04:50   → Committing changes                             │
├─────────────────────────────────────────────────────────────┤
│ Live Context (via NATS mesh):                                │
│ 📦 package_installed: go-jwt v4.5.0                         │
│ 📝 file_changed: src/auth/session.go                        │
│ ✅ test_fixed: TestSessionTimeout                           │
└─────────────────────────────────────────────────────────────┘
```

---

## 11. Context & Live Sharing Flow

### How Context Flows During Task Execution

```
┌─────────────┐    save context    ┌──────────────┐
│ Claude Code  │ ──────────────→  │ Gradient      │
│ (in env)     │                  │ Agent         │
│              │ ←────────────── │ (in env)      │
│              │   restore ctx    │              │
│              │                  │   ↕ NATS     │
│              │                  │   events     │
│              │                  └──────┬───────┘
│              │                         │
│              │                  ┌──────┴───────┐
│              │                  │ Other envs   │
│              │                  │ on same      │
│              │                  │ branch get   │
│              │                  │ live updates │
│              │                  └──────────────┘
```

1. **Before task starts**: Gradient Agent in the environment loads saved context (from `contexts` table) and writes it to `/workspace/.gradient/context.json`
2. **During execution**: The Gradient Agent watches for file changes, package installs, test results, etc. and publishes events to the Live Context Mesh (NATS)
3. **Other environments** on the same branch receive these events in real-time
4. **After task completes**: Full context is saved back to the database, snapshot is taken

### Context Passed to Claude Code

The task prompt includes Gradient context:

```markdown
## Gradient Context (auto-generated)
- **Branch**: feature/auth-fix
- **Previously installed packages**: express@4.18.2, jwt@9.0.0, pg@8.11.3
- **Previous test failures on this branch**: TestSessionTimeout (fixed by increasing TTL)
- **Patterns learned**: This project uses Go 1.22, PostgreSQL, JWT auth middleware
- **Last successful changes**: Added rate limiting to /api/v1/auth endpoints
```

---

## 12. Security & Secrets

### What Gets Stored (Encrypted at Rest)

| Secret | Stored Where | Scope |
|--------|-------------|-------|
| Linear OAuth access_token | `linear_connections.access_token` | Per-org |
| Linear OAuth refresh_token | `linear_connections.refresh_token` | Per-org |
| Linear webhook secret | `linear_connections.webhook_secret` | Per-org |
| Anthropic API key | `claude_configs.anthropic_api_key` | Per-org (+ optional per-user override) |
| GitHub token (for repo clone) | From GitHub App installation token | Per-repo via GitHub App |

### Secrets Injection into Environment

When a task runs, secrets are injected via environment variables (not written to disk):

```bash
# Injected by Gradient Agent, NOT written to any file
export ANTHROPIC_API_KEY="sk-ant-..."
export GITHUB_TOKEN="ghs_..."  # Installation token from GitHub App
```

The Gradient Agent cleans these up when the task completes.

### Encryption

- All tokens/keys stored in PostgreSQL are encrypted using AES-256-GCM
- Encryption key from `GRADIENT_ENCRYPTION_KEY` env var (on the API server)
- Keys are decrypted only in-memory when needed, never logged

---

## 13. Token Strategy: What Goes Where

### Per-Org Tokens (Recommended Defaults)

| Token | Scope | Who Provides It | How |
|-------|-------|----------------|-----|
| **Linear OAuth** | Per-org, one workspace | Org admin | OAuth flow (Settings → Integrations → Connect Linear) |
| **Anthropic API Key** | Per-org, one key | Org admin | Settings → Integrations → Claude Code → Set API Key |
| **GitHub App Token** | Per-repo, auto-generated | Automatic | GitHub App installation generates tokens per-repo |

### Per-User Overrides

Users can optionally provide their own Anthropic API key:
- Settings → Profile → "Use my own Anthropic API key"
- This key is used instead of the org key when that user creates tasks
- Stored in `claude_configs` with `user_id` set

### Why This Structure

```
Org: "Acme Corp"
├── Linear Connection: acme-corp.linear.app (OAuth, one workspace)
├── Claude Config: sk-ant-org-key... (org default)
│   ├── User Override: alice → sk-ant-alice-key... (optional)
│   └── User Override: bob → (none, uses org default)
├── GitHub Repo: acme-corp/backend (via GitHub App)
└── Task Settings:
    ├── Strategy: one_per_task
    ├── Concurrency: 5
    └── Auto-PR: true
```

---

## 14. What We Need From You

### API Keys & Accounts to Create

| Service | What to Create | Where | Result |
|---------|---------------|-------|--------|
| **Linear** | OAuth Application | https://linear.app/settings/api → "Create OAuth application" | `LINEAR_CLIENT_ID`, `LINEAR_CLIENT_SECRET` |
| **Linear** | (nothing else — webhook is created programmatically via API) | — | — |
| **Anthropic** | API Key for testing | https://console.anthropic.com/settings/keys | `ANTHROPIC_API_KEY` (for your test org) |

### Environment Variables to Add to Gradient API Server

```bash
# Linear OAuth (for the OAuth flow)
LINEAR_CLIENT_ID=<from Linear OAuth app>
LINEAR_CLIENT_SECRET=<from Linear OAuth app>
LINEAR_REDIRECT_URI=https://app.usegradient.dev/integrations/linear/callback

# Encryption key for storing secrets (generate with: openssl rand -hex 32)
GRADIENT_ENCRYPTION_KEY=<64-char hex string>

# (Anthropic key is per-org, stored in DB — not a server env var)
```

### That's It

- **No Linear API key needed server-side** — each customer org provides their own via OAuth
- **No Anthropic key needed server-side** — each customer org provides their own via Settings
- **GitHub token is automatic** — GitHub App installations auto-generate tokens per-repo
- The only server-side secrets are `LINEAR_CLIENT_ID`, `LINEAR_CLIENT_SECRET`, and `GRADIENT_ENCRYPTION_KEY`

---

## 15. Implementation Plan

### Phase 1: Foundation (3-5 days)

1. **Database migrations** — Add new tables (`linear_connections`, `claude_configs`, `agent_tasks`, `task_execution_log`, `task_settings`)
2. **Linear OAuth flow** — Backend endpoints for connect/callback/disconnect
3. **Linear webhook handler** — Receive and verify webhooks, parse issue events
4. **Claude config CRUD** — API key storage with encryption, config endpoints
5. **Task model** — Basic CRUD for tasks

### Phase 2: Task Orchestrator (5-7 days)

6. **Task service** — The core orchestration engine
7. **Environment provisioning** — Reuse existing `EnvService` with task-specific setup
8. **Execution script** — Clone repo, inject secrets, run Claude Code, capture output
9. **Context integration** — Feed Gradient context into Claude prompt, save context after
10. **Linear status updates** — Update issue state + post summary comments

### Phase 3: Dashboard & CLI (3-5 days)

11. **Web UI** — Integrations page, Tasks list, Task detail with live logs
12. **CLI commands** — `gc task`, `gc integration`
13. **SSE streaming** — Real-time task status updates in dashboard

### Phase 4: Polish (2-3 days)

14. **Retry logic** — Auto-retry failed tasks with exponential backoff
15. **Cost controls** — Token/cost limits, budget alerts
16. **Concurrency** — Queue management, fair scheduling
17. **PR creation** — Auto-create GitHub PRs from completed tasks

### Total: ~2-3 weeks

---

## Appendix A: Linear Webhook Payload Examples

### Issue Created

```json
{
  "action": "create",
  "type": "Issue",
  "createdAt": "2026-03-06T15:30:00.000Z",
  "data": {
    "id": "issue-uuid",
    "identifier": "GRAD-42",
    "title": "Fix authentication timeout bug",
    "description": "The session expires after 5 minutes...",
    "state": {
      "id": "state-uuid",
      "name": "Todo",
      "type": "unstarted"
    },
    "team": {
      "id": "team-uuid",
      "key": "GRAD"
    },
    "labels": [
      { "id": "label-uuid", "name": "gradient-agent" }
    ],
    "assignee": {
      "id": "user-uuid",
      "name": "Alice"
    },
    "branchName": "alice/grad-42-fix-authentication-timeout-bug",
    "url": "https://linear.app/acme/issue/GRAD-42"
  },
  "url": "https://linear.app/acme/issue/GRAD-42"
}
```

### Issue Updated (State Change)

```json
{
  "action": "update",
  "type": "Issue",
  "data": {
    "id": "issue-uuid",
    "identifier": "GRAD-42",
    "state": {
      "id": "state-uuid",
      "name": "In Progress",
      "type": "started"
    }
  },
  "updatedFrom": {
    "stateId": "old-state-uuid"
  }
}
```

## Appendix B: Claude Code Output Format

When run with `--output-format json`, Claude Code outputs:

```json
{
  "type": "result",
  "subtype": "success",
  "result": "I've fixed the authentication timeout issue by...",
  "session_id": "session-uuid",
  "cost_usd": 0.0834,
  "duration_ms": 45000,
  "num_turns": 12,
  "total_input_tokens": 28500,
  "total_output_tokens": 4200
}
```

For errors:

```json
{
  "type": "result",
  "subtype": "error_max_turns",
  "result": "Reached maximum number of turns...",
  "session_id": "session-uuid",
  "cost_usd": 0.15,
  "duration_ms": 120000,
  "num_turns": 50
}
```

## Appendix C: Full System Diagram

```
                          ┌──────────────────────────────────────────────────┐
                          │                 Gradient Platform                  │
                          │                                                    │
  ┌────────┐   webhook    │  ┌──────────────────────────────────────────┐     │
  │ Linear │ ──────────→ │  │ API Server (Go)                          │     │
  │        │ ←─────────  │  │  ├─ LinearService    (webhook + GraphQL) │     │
  │        │  update      │  │  ├─ TaskService      (orchestration)    │     │
  └────────┘  issue       │  │  ├─ ClaudeService    (exec management)  │     │
                          │  │  ├─ EnvService       (env lifecycle)    │     │
  ┌────────┐              │  │  ├─ ContextService   (context CRUD)     │     │
  │ GitHub │ ←─────────  │  │  ├─ RepoService      (auto-fork)        │     │
  │  App   │  push PR     │  │  └─ BillingService   (Stripe)          │     │
  │        │ ──────────→ │  └──────────────┬───────────────────────────┘     │
  └────────┘   webhook    │                 │                                  │
                          │        ┌────────┴────────┐                        │
                          │        │  PostgreSQL      │                        │
                          │        │  (all state)     │                        │
                          │        └────────┬────────┘                        │
                          │                 │                                  │
                          │        ┌────────┴────────┐                        │
                          │        │  NATS JetStream  │                        │
                          │        │  (live mesh)     │                        │
                          │        └────────┬────────┘                        │
                          │                 │                                  │
                          │    ┌────────────┴────────────┐                    │
                          │    │                         │                    │
                          │  ┌─┴──────────┐   ┌─────────┴──┐                 │
                          │  │ Env A      │   │ Env B       │                 │
                          │  │ (Hetzner)  │   │ (Hetzner)   │                 │
                          │  │            │   │             │                 │
                          │  │ gradient-  │   │ gradient-   │                 │
                          │  │ agent      │   │ agent       │                 │
                          │  │   ↕        │   │   ↕         │                 │
                          │  │ claude -p  │   │ claude -p   │                 │
                          │  │ (task 1)   │   │ (task 2)    │                 │
                          │  │            │   │             │                 │
                          │  │ /repo      │   │ /repo       │                 │
                          │  │ (cloned)   │   │ (cloned)    │                 │
                          │  └────────────┘   └─────────────┘                 │
                          │                                                    │
                          └──────────────────────────────────────────────────┘
```
