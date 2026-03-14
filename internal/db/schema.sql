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
    summary_text TEXT DEFAULT '',
    change_log_text TEXT DEFAULT '',
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

-- GitHub OAuth connections (one per org — stores user access token)
CREATE TABLE IF NOT EXISTS github_connections (
    id VARCHAR(255) PRIMARY KEY,
    org_id VARCHAR(255) NOT NULL UNIQUE,
    access_token TEXT NOT NULL,
    github_user VARCHAR(255),
    github_avatar VARCHAR(500),
    scopes TEXT DEFAULT '',
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- Repo connections (links GitHub repo → Gradient org for auto-fork)
CREATE TABLE IF NOT EXISTS repo_connections (
    id VARCHAR(255) PRIMARY KEY,
    org_id VARCHAR(255) NOT NULL,
    installation_id BIGINT NOT NULL DEFAULT 0,
    repo_full_name VARCHAR(500) NOT NULL,
    default_branch VARCHAR(255) DEFAULT 'main',
    auto_fork_enabled BOOLEAN DEFAULT true,
    auto_snapshot_on_push BOOLEAN DEFAULT true,
    webhook_id BIGINT,
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

-- webhook_id for repo connections (GitHub OAuth flow creates webhooks per-repo)
DO $$ BEGIN
    ALTER TABLE repo_connections ADD COLUMN IF NOT EXISTS webhook_id BIGINT;
EXCEPTION WHEN others THEN NULL;
END $$;

-- Make installation_id optional (OAuth flow doesn't use GitHub App installations)
DO $$ BEGIN
    ALTER TABLE repo_connections ALTER COLUMN installation_id SET DEFAULT 0;
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
    max_turns INTEGER DEFAULT 250,

    -- Tool permissions
    allowed_tools JSONB DEFAULT '["Edit","Write","Bash","Read"]',

    -- Agent teams (experimental: spawn sub-agents for complex tasks)
    enable_teams BOOLEAN NOT NULL DEFAULT true,

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

-- parent_task_id: links sub-tasks back to the orchestrating parent
DO $$ BEGIN
    ALTER TABLE agent_tasks ADD COLUMN IF NOT EXISTS parent_task_id VARCHAR(255) REFERENCES agent_tasks(id);
EXCEPTION WHEN others THEN NULL;
END $$;

CREATE INDEX IF NOT EXISTS idx_agent_tasks_parent ON agent_tasks(parent_task_id) WHERE parent_task_id IS NOT NULL;

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

-- ═══════════════════════════════════════════════════════════════════
-- Agent-Native VCS: Sessions, Change Bundles, Contracts, Context Objects
-- ═══════════════════════════════════════════════════════════════════

-- Agent sessions: bounded work units for each agent
CREATE TABLE IF NOT EXISTS agent_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id VARCHAR(255) REFERENCES agent_tasks(id),
    parent_session_id UUID REFERENCES agent_sessions(id),
    org_id TEXT NOT NULL,
    agent_role TEXT NOT NULL DEFAULT 'worker',
    scope JSONB NOT NULL DEFAULT '{}',
    initial_sha TEXT,
    branch_name TEXT,
    environment_id VARCHAR(255),
    status TEXT NOT NULL DEFAULT 'pending',
    contracts JSONB DEFAULT '[]',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_agent_sessions_task ON agent_sessions(task_id);
CREATE INDEX IF NOT EXISTS idx_agent_sessions_org ON agent_sessions(org_id);
CREATE INDEX IF NOT EXISTS idx_agent_sessions_status ON agent_sessions(org_id, status);
CREATE INDEX IF NOT EXISTS idx_agent_sessions_parent ON agent_sessions(parent_session_id);

-- Change bundles: atomic code+context+decision units
CREATE TABLE IF NOT EXISTS change_bundles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id UUID REFERENCES agent_sessions(id) NOT NULL,
    sequence INT NOT NULL,
    git_patch TEXT,
    commit_sha TEXT,
    context_diff JSONB DEFAULT '{}',
    decision_diff JSONB DEFAULT '{}',
    test_results JSONB DEFAULT '[]',
    policy_results JSONB DEFAULT '[]',
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(session_id, sequence)
);

CREATE INDEX IF NOT EXISTS idx_change_bundles_session ON change_bundles(session_id, sequence);
CREATE INDEX IF NOT EXISTS idx_change_bundles_status ON change_bundles(status);

-- Contracts: inter-agent agreements on API shapes, invariants, schemas
CREATE TABLE IF NOT EXISTS contracts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id TEXT NOT NULL,
    task_id VARCHAR(255) REFERENCES agent_tasks(id),
    type TEXT NOT NULL,
    scope TEXT NOT NULL,
    spec JSONB NOT NULL,
    owner_session_id UUID REFERENCES agent_sessions(id),
    consumers TEXT[] DEFAULT '{}',
    version INT DEFAULT 1,
    status TEXT NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_contracts_org ON contracts(org_id);
CREATE INDEX IF NOT EXISTS idx_contracts_task ON contracts(task_id);
CREATE INDEX IF NOT EXISTS idx_contracts_owner ON contracts(owner_session_id);

-- Context objects: structured, queryable context per branch
CREATE TABLE IF NOT EXISTS context_objects (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id TEXT NOT NULL,
    branch TEXT NOT NULL,
    type TEXT NOT NULL,
    key TEXT NOT NULL,
    value JSONB NOT NULL,
    source_session UUID REFERENCES agent_sessions(id),
    version INT DEFAULT 1,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(org_id, branch, type, key)
);

CREATE INDEX IF NOT EXISTS idx_context_objects_branch ON context_objects(org_id, branch);
CREATE INDEX IF NOT EXISTS idx_context_objects_type ON context_objects(org_id, branch, type);
CREATE INDEX IF NOT EXISTS idx_context_objects_source ON context_objects(source_session);

-- Memory tips: durable, attributable guidance distilled from trajectories
CREATE TABLE IF NOT EXISTS memory_tips (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id TEXT NOT NULL,
    repo_full_name TEXT NOT NULL,
    source_branch TEXT NOT NULL DEFAULT '',
    tip_type TEXT NOT NULL,              -- strategy, recovery, optimization
    scope TEXT NOT NULL DEFAULT 'task',  -- task, subtask
    title TEXT NOT NULL,
    content TEXT NOT NULL,
    trigger_condition TEXT DEFAULT '',
    action_steps JSONB NOT NULL DEFAULT '[]',
    priority TEXT NOT NULL DEFAULT 'medium',
    confidence NUMERIC(5,4) NOT NULL DEFAULT 0.5000,
    canonical_key TEXT NOT NULL,
    failure_signature TEXT DEFAULT '',
    task_fingerprint TEXT DEFAULT '',
    keywords JSONB NOT NULL DEFAULT '[]',
    search_text TEXT NOT NULL DEFAULT '',
    semantic_summary TEXT NOT NULL DEFAULT '',
    outcome_class TEXT NOT NULL DEFAULT '',
    embedding_status TEXT NOT NULL DEFAULT 'disabled',
    embedding_model TEXT DEFAULT '',
    embedding_updated_at TIMESTAMPTZ,
    evidence_count INTEGER NOT NULL DEFAULT 1,
    use_count INTEGER NOT NULL DEFAULT 0,
    last_retrieved_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(org_id, repo_full_name, canonical_key, tip_type)
);

CREATE INDEX IF NOT EXISTS idx_memory_tips_repo ON memory_tips(org_id, repo_full_name, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_memory_tips_type ON memory_tips(org_id, repo_full_name, tip_type);
CREATE INDEX IF NOT EXISTS idx_memory_tips_branch ON memory_tips(org_id, repo_full_name, source_branch);
CREATE INDEX IF NOT EXISTS idx_memory_tips_failure_signature ON memory_tips(org_id, repo_full_name, failure_signature);

DO $$ BEGIN
    ALTER TABLE memory_tips ADD COLUMN IF NOT EXISTS semantic_summary TEXT NOT NULL DEFAULT '';
    ALTER TABLE memory_tips ADD COLUMN IF NOT EXISTS embedding_status TEXT NOT NULL DEFAULT 'disabled';
    ALTER TABLE memory_tips ADD COLUMN IF NOT EXISTS embedding_model TEXT DEFAULT '';
    ALTER TABLE memory_tips ADD COLUMN IF NOT EXISTS embedding_updated_at TIMESTAMPTZ;
EXCEPTION WHEN others THEN NULL;
END $$;

-- Provenance records for how a memory tip was generated
CREATE TABLE IF NOT EXISTS memory_tip_sources (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tip_id UUID NOT NULL REFERENCES memory_tips(id) ON DELETE CASCADE,
    task_id VARCHAR(255) REFERENCES agent_tasks(id),
    session_id UUID REFERENCES agent_sessions(id),
    bundle_id UUID REFERENCES change_bundles(id),
    event_id VARCHAR(255),
    source_kind TEXT NOT NULL DEFAULT 'trajectory',
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_memory_tip_sources_tip ON memory_tip_sources(tip_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_memory_tip_sources_task ON memory_tip_sources(task_id, created_at DESC);

-- Retrieval audit for prompt injection/debugging
CREATE TABLE IF NOT EXISTS memory_tip_retrievals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tip_id UUID NOT NULL REFERENCES memory_tips(id) ON DELETE CASCADE,
    task_id VARCHAR(255) REFERENCES agent_tasks(id),
    session_id UUID REFERENCES agent_sessions(id),
    score NUMERIC(10,4) NOT NULL DEFAULT 0,
    reason TEXT DEFAULT '',
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_memory_tip_retrievals_tip ON memory_tip_retrievals(tip_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_memory_tip_retrievals_task ON memory_tip_retrievals(task_id, created_at DESC);

-- Trajectory analyses: normalized, attributable execution summaries
CREATE TABLE IF NOT EXISTS trajectory_analyses (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id TEXT NOT NULL,
    repo_full_name TEXT NOT NULL,
    task_id VARCHAR(255) NOT NULL REFERENCES agent_tasks(id) ON DELETE CASCADE,
    session_id UUID REFERENCES agent_sessions(id) ON DELETE SET NULL,
    source_branch TEXT NOT NULL DEFAULT '',
    trajectory_summary TEXT NOT NULL DEFAULT '',
    outcome_class TEXT NOT NULL DEFAULT '',
    immediate_cause TEXT NOT NULL DEFAULT '',
    proximate_cause TEXT NOT NULL DEFAULT '',
    root_cause TEXT NOT NULL DEFAULT '',
    recovery_action TEXT NOT NULL DEFAULT '',
    recovery_reason TEXT NOT NULL DEFAULT '',
    inefficiency_pattern TEXT NOT NULL DEFAULT '',
    recommended_alternative TEXT NOT NULL DEFAULT '',
    subtask_analyses JSONB NOT NULL DEFAULT '[]',
    analyzer_version TEXT NOT NULL DEFAULT 'v1',
    model_name TEXT NOT NULL DEFAULT '',
    confidence NUMERIC(5,4) NOT NULL DEFAULT 0.5000,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(task_id, session_id)
);

CREATE INDEX IF NOT EXISTS idx_trajectory_analyses_repo ON trajectory_analyses(org_id, repo_full_name, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_trajectory_analyses_task ON trajectory_analyses(task_id, created_at DESC);

-- Retrieval runs: audit full candidate / rerank / selection flow
CREATE TABLE IF NOT EXISTS retrieval_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id TEXT NOT NULL,
    repo_full_name TEXT NOT NULL,
    task_id VARCHAR(255) REFERENCES agent_tasks(id) ON DELETE SET NULL,
    session_id UUID REFERENCES agent_sessions(id) ON DELETE SET NULL,
    query_text TEXT NOT NULL DEFAULT '',
    subtask TEXT NOT NULL DEFAULT '',
    failure_signature TEXT NOT NULL DEFAULT '',
    candidate_tip_ids JSONB NOT NULL DEFAULT '[]',
    reranked_tip_ids JSONB NOT NULL DEFAULT '[]',
    selected_tip_ids JSONB NOT NULL DEFAULT '[]',
    vector_search_used BOOLEAN NOT NULL DEFAULT FALSE,
    reranker_model TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'completed',
    latency_ms INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_retrieval_runs_repo ON retrieval_runs(org_id, repo_full_name, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_retrieval_runs_task ON retrieval_runs(task_id, created_at DESC);

DO $$ BEGIN
    CREATE EXTENSION IF NOT EXISTS vector;
EXCEPTION WHEN undefined_file THEN
    NULL;
WHEN feature_not_supported THEN
    NULL;
WHEN insufficient_privilege THEN
    NULL;
WHEN others THEN
    NULL;
END $$;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'vector') THEN
        EXECUTE '
            CREATE TABLE IF NOT EXISTS memory_tip_embeddings (
                id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
                tip_id UUID NOT NULL REFERENCES memory_tips(id) ON DELETE CASCADE,
                provider TEXT NOT NULL DEFAULT '''',
                model TEXT NOT NULL DEFAULT '''',
                dimensions INTEGER NOT NULL DEFAULT 0,
                embedding_vector vector(1536),
                created_at TIMESTAMPTZ DEFAULT NOW(),
                updated_at TIMESTAMPTZ DEFAULT NOW(),
                UNIQUE(tip_id, provider, model)
            )';
        EXECUTE 'CREATE INDEX IF NOT EXISTS idx_memory_tip_embeddings_tip ON memory_tip_embeddings(tip_id, updated_at DESC)';
    ELSE
        EXECUTE '
            CREATE TABLE IF NOT EXISTS memory_tip_embeddings (
                id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
                tip_id UUID NOT NULL REFERENCES memory_tips(id) ON DELETE CASCADE,
                provider TEXT NOT NULL DEFAULT '''',
                model TEXT NOT NULL DEFAULT '''',
                dimensions INTEGER NOT NULL DEFAULT 0,
                embedding_vector_json JSONB NOT NULL DEFAULT ''[]'',
                created_at TIMESTAMPTZ DEFAULT NOW(),
                updated_at TIMESTAMPTZ DEFAULT NOW(),
                UNIQUE(tip_id, provider, model)
            )';
        EXECUTE 'CREATE INDEX IF NOT EXISTS idx_memory_tip_embeddings_tip ON memory_tip_embeddings(tip_id, updated_at DESC)';
    END IF;
END $$;

-- ═══════════════════════════════════════════════════════════════
-- Repo-scoped context mesh: add repo_full_name to environments,
-- contexts, context_events, context_objects for per-repo isolation
-- ═══════════════════════════════════════════════════════════════

DO $$ BEGIN
    ALTER TABLE environments ADD COLUMN IF NOT EXISTS repo_full_name VARCHAR(500) DEFAULT '';
EXCEPTION WHEN others THEN NULL;
END $$;

DO $$ BEGIN
    ALTER TABLE contexts ADD COLUMN IF NOT EXISTS repo_full_name VARCHAR(500) DEFAULT '';
EXCEPTION WHEN others THEN NULL;
END $$;

DO $$ BEGIN
    ALTER TABLE contexts ADD COLUMN IF NOT EXISTS summary_text TEXT DEFAULT '';
EXCEPTION WHEN others THEN NULL;
END $$;

DO $$ BEGIN
    ALTER TABLE contexts ADD COLUMN IF NOT EXISTS change_log_text TEXT DEFAULT '';
EXCEPTION WHEN others THEN NULL;
END $$;

DO $$ BEGIN
    ALTER TABLE context_events ADD COLUMN IF NOT EXISTS repo_full_name VARCHAR(500) DEFAULT '';
EXCEPTION WHEN others THEN NULL;
END $$;

DO $$ BEGIN
    ALTER TABLE context_objects ADD COLUMN IF NOT EXISTS repo_full_name TEXT DEFAULT '';
EXCEPTION WHEN others THEN NULL;
END $$;

-- Replace the old unique constraint on contexts with repo-scoped one
DO $$ BEGIN
    ALTER TABLE contexts DROP CONSTRAINT IF EXISTS contexts_org_id_branch_key;
    ALTER TABLE contexts ADD CONSTRAINT contexts_org_repo_branch_key UNIQUE (org_id, repo_full_name, branch);
EXCEPTION WHEN others THEN NULL;
END $$;

-- Replace the old unique constraint on context_objects with repo-scoped one
DO $$ BEGIN
    ALTER TABLE context_objects DROP CONSTRAINT IF EXISTS context_objects_org_id_branch_type_key_key;
    ALTER TABLE context_objects ADD CONSTRAINT context_objects_org_repo_branch_type_key_key UNIQUE (org_id, repo_full_name, branch, type, key);
EXCEPTION WHEN others THEN NULL;
END $$;

-- Indexes for repo-scoped queries
CREATE INDEX IF NOT EXISTS idx_environments_org_repo_branch ON environments(org_id, repo_full_name, context_branch, status);
CREATE INDEX IF NOT EXISTS idx_context_events_repo_branch ON context_events(org_id, repo_full_name, branch, sequence);
CREATE INDEX IF NOT EXISTS idx_contexts_org_repo ON contexts(org_id, repo_full_name);
CREATE INDEX IF NOT EXISTS idx_context_objects_org_repo ON context_objects(org_id, repo_full_name, branch);

-- Add enable_teams to claude_configs (agent-teams experimental feature)
DO $$ BEGIN
    ALTER TABLE claude_configs ADD COLUMN IF NOT EXISTS enable_teams BOOLEAN NOT NULL DEFAULT true;
EXCEPTION WHEN others THEN NULL;
END $$;
