-- Gradient Database Schema

-- Environments table
CREATE TABLE IF NOT EXISTS environments (
    id VARCHAR(255) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    org_id VARCHAR(255) NOT NULL,
    provider VARCHAR(50) NOT NULL,
    region VARCHAR(100) NOT NULL,
    size VARCHAR(50) NOT NULL DEFAULT 'small',
    cluster_name VARCHAR(255),
    ip_address VARCHAR(45),
    status VARCHAR(50) NOT NULL DEFAULT 'creating',
    resources JSONB DEFAULT '{}',
    config JSONB DEFAULT '{}',
    context_branch VARCHAR(255),
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),
    destroyed_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_environments_org_id ON environments(org_id);
CREATE INDEX IF NOT EXISTS idx_environments_status ON environments(status);
CREATE INDEX IF NOT EXISTS idx_environments_context_branch ON environments(context_branch);
CREATE INDEX IF NOT EXISTS idx_environments_org_branch ON environments(org_id, context_branch, status);

-- Contexts table
CREATE TABLE IF NOT EXISTS contexts (
    id VARCHAR(255) PRIMARY KEY,
    branch VARCHAR(255) NOT NULL,
    org_id VARCHAR(255) NOT NULL,
    commit_sha VARCHAR(40),
    installed_packages JSONB DEFAULT '[]',
    previous_failures JSONB DEFAULT '[]',
    attempted_fixes JSONB DEFAULT '[]',
    patterns JSONB DEFAULT '{}',
    global_configs JSONB DEFAULT '{}',
    base_os VARCHAR(50) DEFAULT 'ubuntu-24.04',
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),
    UNIQUE(org_id, branch)
);

CREATE INDEX IF NOT EXISTS idx_contexts_branch_org ON contexts(branch, org_id);
CREATE INDEX IF NOT EXISTS idx_contexts_org_id ON contexts(org_id);

-- Usage events table
CREATE TABLE IF NOT EXISTS usage_events (
    id VARCHAR(255) PRIMARY KEY,
    environment_id VARCHAR(255) NOT NULL,
    org_id VARCHAR(255) NOT NULL,
    size VARCHAR(50) NOT NULL DEFAULT 'small',
    started_at TIMESTAMP NOT NULL DEFAULT NOW(),
    stopped_at TIMESTAMP,
    billed_seconds INTEGER DEFAULT 0,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_usage_events_org_id ON usage_events(org_id);
CREATE INDEX IF NOT EXISTS idx_usage_events_environment_id ON usage_events(environment_id);
CREATE INDEX IF NOT EXISTS idx_usage_events_started_at ON usage_events(started_at);

-- Org settings table (billing, registry, provider preferences)
CREATE TABLE IF NOT EXISTS org_settings (
    org_id VARCHAR(255) PRIMARY KEY,
    stripe_customer_id VARCHAR(255),
    stripe_subscription_id VARCHAR(255),
    owner_email VARCHAR(255),
    owner_user_id VARCHAR(255),
    -- Per-org container registry (enterprise isolation).
    -- NULL = use platform default registry from env vars.
    -- Set these for orgs that need their own registry (compliance, data sovereignty, etc.)
    registry_url VARCHAR(500),
    registry_user VARCHAR(255),
    registry_pass VARCHAR(500),   -- encrypted at rest in production
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- Secret syncs metadata table
CREATE TABLE IF NOT EXISTS secret_syncs (
    id VARCHAR(255) PRIMARY KEY,
    environment_id VARCHAR(255) NOT NULL,
    org_id VARCHAR(255) NOT NULL,
    secret_key VARCHAR(255) NOT NULL,
    backend VARCHAR(50) NOT NULL,
    backend_path VARCHAR(500),
    synced_at TIMESTAMP DEFAULT NOW(),
    UNIQUE(environment_id, secret_key)
);

CREATE INDEX IF NOT EXISTS idx_secret_syncs_environment_id ON secret_syncs(environment_id);
CREATE INDEX IF NOT EXISTS idx_secret_syncs_org_id ON secret_syncs(org_id);

-- Snapshots table (container commit filesystem deltas)
CREATE TABLE IF NOT EXISTS snapshots (
    id VARCHAR(255) PRIMARY KEY,
    org_id VARCHAR(255) NOT NULL,
    branch VARCHAR(255) NOT NULL,
    environment_id VARCHAR(255),
    snapshot_type VARCHAR(50) NOT NULL,
    image_ref VARCHAR(500),
    size_bytes BIGINT DEFAULT 0,
    parent_snapshot_id VARCHAR(255),
    commit_sha VARCHAR(40),
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_snapshots_org_branch ON snapshots(org_id, branch);
CREATE INDEX IF NOT EXISTS idx_snapshots_environment_id ON snapshots(environment_id);

-- GitHub App installations (raw webhook data)
CREATE TABLE IF NOT EXISTS github_installations (
    installation_id BIGINT PRIMARY KEY,
    account_login VARCHAR(255) NOT NULL,
    repos JSONB DEFAULT '[]',
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- Repo connections (links GitHub repo → Gradient org for auto-fork)
CREATE TABLE IF NOT EXISTS repo_connections (
    id VARCHAR(255) PRIMARY KEY,
    org_id VARCHAR(255) NOT NULL,
    installation_id BIGINT NOT NULL,
    repo_full_name VARCHAR(500) NOT NULL,
    default_branch VARCHAR(255) DEFAULT 'main',
    auto_fork_enabled BOOLEAN DEFAULT true,
    auto_snapshot_on_push BOOLEAN DEFAULT true,
    created_at TIMESTAMP DEFAULT NOW(),
    UNIQUE(org_id, repo_full_name)
);

CREATE INDEX IF NOT EXISTS idx_repo_connections_org_id ON repo_connections(org_id);
CREATE INDEX IF NOT EXISTS idx_repo_connections_repo ON repo_connections(repo_full_name);

-- Autoscale policies table
CREATE TABLE IF NOT EXISTS autoscale_policies (
    id VARCHAR(255) PRIMARY KEY,
    environment_id VARCHAR(255) NOT NULL,
    org_id VARCHAR(255) NOT NULL,
    min_replicas INTEGER NOT NULL DEFAULT 1,
    max_replicas INTEGER NOT NULL DEFAULT 10,
    target_cpu NUMERIC(5,4) NOT NULL DEFAULT 0.7000,
    target_memory NUMERIC(5,4) NOT NULL DEFAULT 0.8000,
    scale_up_threshold NUMERIC(5,4) NOT NULL DEFAULT 0.8500,
    scale_down_threshold NUMERIC(5,4) NOT NULL DEFAULT 0.3000,
    cooldown_secs INTEGER NOT NULL DEFAULT 300,
    current_replicas INTEGER NOT NULL DEFAULT 1,
    enabled BOOLEAN NOT NULL DEFAULT true,
    last_scale_at TIMESTAMP,
    last_scale_direction VARCHAR(10),
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),
    UNIQUE(environment_id)
);

CREATE INDEX IF NOT EXISTS idx_autoscale_policies_org ON autoscale_policies(org_id);
CREATE INDEX IF NOT EXISTS idx_autoscale_policies_enabled ON autoscale_policies(enabled) WHERE enabled = true;

-- Autoscale events (audit log of scaling actions)
CREATE TABLE IF NOT EXISTS autoscale_events (
    id VARCHAR(255) PRIMARY KEY,
    environment_id VARCHAR(255) NOT NULL,
    org_id VARCHAR(255) NOT NULL,
    direction VARCHAR(10) NOT NULL,
    from_replicas INTEGER NOT NULL,
    to_replicas INTEGER NOT NULL,
    trigger_cpu NUMERIC(5,4),
    trigger_memory NUMERIC(5,4),
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_autoscale_events_env ON autoscale_events(environment_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_autoscale_events_org ON autoscale_events(org_id, created_at DESC);

-- Live Context Mesh: context events (append-only, CRDT-friendly event log)
CREATE TABLE IF NOT EXISTS context_events (
    -- Identity
    id VARCHAR(255) PRIMARY KEY,
    schema_version INTEGER NOT NULL DEFAULT 1,
    event_type VARCHAR(50) NOT NULL,

    -- Scoping
    org_id VARCHAR(255) NOT NULL,
    branch VARCHAR(255) NOT NULL,
    env_id VARCHAR(255) NOT NULL,
    source VARCHAR(50) NOT NULL DEFAULT 'agent',

    -- Payload (type-specific structured data)
    data JSONB NOT NULL DEFAULT '{}',

    -- Deduplication
    idempotency_key VARCHAR(255),

    -- Causality & ordering
    timestamp TIMESTAMP NOT NULL,
    sequence BIGSERIAL,      -- server-assigned monotonic sequence for cursor-based pagination
    causal_id VARCHAR(255),  -- optional: ID of event that caused this one
    parent_id VARCHAR(255),  -- optional: parent event for threading

    -- Metadata
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMP,    -- optional TTL for automatic cleanup
    acked BOOLEAN NOT NULL DEFAULT false,

    -- Dedup constraint: at most one event per idempotency_key per branch per org
    UNIQUE(org_id, branch, idempotency_key)
);

-- Primary query path: events for a branch, ordered by sequence (cursor-based pagination)
CREATE INDEX IF NOT EXISTS idx_context_events_branch_seq ON context_events(org_id, branch, sequence);

-- Filter by type
CREATE INDEX IF NOT EXISTS idx_context_events_type ON context_events(org_id, branch, event_type);

-- Filter by env
CREATE INDEX IF NOT EXISTS idx_context_events_env ON context_events(org_id, env_id, sequence);

-- Time-based queries (since/until filters)
CREATE INDEX IF NOT EXISTS idx_context_events_timestamp ON context_events(org_id, branch, timestamp);

-- TTL cleanup
CREATE INDEX IF NOT EXISTS idx_context_events_expires ON context_events(expires_at) WHERE expires_at IS NOT NULL;

-- Source-based filtering
CREATE INDEX IF NOT EXISTS idx_context_events_source ON context_events(org_id, branch, source);

-- Warm pool: pre-booted servers waiting for instant environment assignment
-- Provider-agnostic: works with any cloud provider (hetzner, aws, gcp, etc.)
CREATE TABLE IF NOT EXISTS warm_pool (
    id VARCHAR(255) PRIMARY KEY,
    provider_id VARCHAR(255),        -- provider-specific server/instance ID
    ip_address VARCHAR(45),          -- public IP (if available)
    provider VARCHAR(50) NOT NULL,   -- cloud provider name (hetzner, aws, gcp, etc.)
    size VARCHAR(50) NOT NULL,       -- environment size (small, medium, large)
    region VARCHAR(100) NOT NULL,    -- provider-specific region/location
    status VARCHAR(20) NOT NULL DEFAULT 'warming',  -- warming, ready, assigned, draining
    assigned_to VARCHAR(255),        -- environment ID if assigned
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    ready_at TIMESTAMP,
    assigned_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_warm_pool_acquire ON warm_pool(provider, size, region, status, ready_at)
    WHERE status = 'ready';
CREATE INDEX IF NOT EXISTS idx_warm_pool_status ON warm_pool(status);
CREATE INDEX IF NOT EXISTS idx_warm_pool_provider_id ON warm_pool(provider_id);

-- ── Schema migrations (idempotent ALTER TABLE statements) ──────────────────
-- These handle adding columns that didn't exist in earlier versions of the schema.
-- Safe to run repeatedly — DO NOTHING if column already exists.

DO $$ BEGIN
    ALTER TABLE environments ADD COLUMN IF NOT EXISTS ip_address VARCHAR(45);
EXCEPTION WHEN others THEN NULL;
END $$;

-- Billing tier: "free" (20hr/mo limit, small only) or "paid" (any size, payment method required)
DO $$ BEGIN
    ALTER TABLE org_settings ADD COLUMN IF NOT EXISTS billing_tier VARCHAR(20) DEFAULT 'free';
EXCEPTION WHEN others THEN NULL;
END $$;

-- ═══════════════════════════════════════════════════════════════════
-- Agent Tasks: Linear + Claude Code Integration
-- ═══════════════════════════════════════════════════════════════════

-- Linear workspace connections (one per org)
CREATE TABLE IF NOT EXISTS linear_connections (
    id VARCHAR(255) PRIMARY KEY,
    org_id VARCHAR(255) NOT NULL UNIQUE,

    -- OAuth2 tokens
    access_token TEXT NOT NULL DEFAULT '',
    refresh_token TEXT,
    token_expires_at TIMESTAMP,

    -- Workspace info (cached from Linear API)
    workspace_id VARCHAR(255),
    workspace_name VARCHAR(255),

    -- Webhook config
    webhook_id VARCHAR(255),
    webhook_secret VARCHAR(255),

    -- Filter config: which issues to pick up
    filter_team_ids JSONB DEFAULT '[]',
    filter_project_ids JSONB DEFAULT '[]',
    filter_label_names JSONB DEFAULT '["gradient-agent"]',
    trigger_state VARCHAR(255) DEFAULT 'Todo',

    -- Status
    status VARCHAR(50) NOT NULL DEFAULT 'active',

    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- Claude Code configuration (one per org, with optional per-user overrides)
CREATE TABLE IF NOT EXISTS claude_configs (
    id VARCHAR(255) PRIMARY KEY,
    org_id VARCHAR(255) NOT NULL,
    user_id VARCHAR(255),

    -- Anthropic API key
    anthropic_api_key TEXT NOT NULL DEFAULT '',

    -- Model preferences
    model VARCHAR(100) DEFAULT 'claude-sonnet-4-20250514',
    max_turns INTEGER DEFAULT 50,

    -- Tool permissions
    allowed_tools JSONB DEFAULT '["Edit","Write","Bash","Read"]',

    -- Cost controls
    max_cost_per_task NUMERIC(10,2),
    max_tokens_per_task INTEGER DEFAULT 100000,

    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),

    UNIQUE(org_id, user_id)
);

-- Agent tasks (the core table)
CREATE TABLE IF NOT EXISTS agent_tasks (
    id VARCHAR(255) PRIMARY KEY,
    org_id VARCHAR(255) NOT NULL,

    -- Linear issue link
    linear_issue_id VARCHAR(255),
    linear_identifier VARCHAR(50),
    linear_url VARCHAR(500),

    -- Task content
    title VARCHAR(500) NOT NULL,
    description TEXT,
    prompt TEXT,

    -- Execution
    environment_id VARCHAR(255),
    branch VARCHAR(255),
    repo_full_name VARCHAR(500),

    -- Status: pending, queued, running, complete, failed, cancelled
    status VARCHAR(50) NOT NULL DEFAULT 'pending',

    -- Results
    output_summary TEXT,
    output_json JSONB,
    commit_sha VARCHAR(40),
    pr_url VARCHAR(500),
    error_message TEXT,

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

-- Task execution log (step-by-step audit log)
CREATE TABLE IF NOT EXISTS task_execution_log (
    id VARCHAR(255) PRIMARY KEY,
    task_id VARCHAR(255) NOT NULL,
    step VARCHAR(100) NOT NULL,
    status VARCHAR(50) NOT NULL,
    message TEXT,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_task_log_task ON task_execution_log(task_id, created_at);

-- Org task execution settings
CREATE TABLE IF NOT EXISTS task_settings (
    org_id VARCHAR(255) PRIMARY KEY,
    instance_strategy VARCHAR(50) DEFAULT 'one_per_task',
    max_concurrent_tasks INTEGER DEFAULT 3,
    default_env_size VARCHAR(50) DEFAULT 'small',
    default_env_provider VARCHAR(50) DEFAULT 'hetzner',
    default_env_region VARCHAR(100) DEFAULT 'fsn1',
    auto_create_pr BOOLEAN DEFAULT true,
    pr_base_branch VARCHAR(255) DEFAULT 'main',
    auto_destroy_env BOOLEAN DEFAULT true,
    env_ttl_minutes INTEGER DEFAULT 30,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);
