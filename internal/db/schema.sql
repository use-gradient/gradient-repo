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
