# Gradient — Local Dev Guide

Everything you can do right now on `localhost:6767` with the `gc` CLI.

## Prerequisites

```bash
make dev          # Starts Postgres, NATS, Vault, builds binaries, runs migrations
gc auth login     # Opens browser → sign in with Clerk → CLI authorized
```

---

## 1. Authentication

```bash
# Sign in (opens browser, Clerk sign-in, returns to CLI)
gc auth login

# Check who you are
gc auth status
#   Status:       ✓ logged in
#   Name:         Vin Vadoothker
#   Email:        vinvadoothker@gmail.com
#   API URL:      http://localhost:6767
#   Active Org:   org_3AVtyCog7U59jeGJMDaxWgyWP3K

# Verbose: includes environment count, billing, mesh health
gc auth status -v

# Sign out (clears local credentials)
gc auth logout
```

---

## 2. Organizations

```bash
# Create a new org (auto-switches to it, you're admin)
gc org create "My Startup"
gc org create "Acme Corp" --slug acme       # custom slug
gc org create "Test Org" --switch=false      # don't auto-switch

# List your orgs
gc org list
#   ID                               NAME        SLUG         ACTIVE
#   org_3AVtyCog7U59jeGJMDaxWgyWP3K  My Team     my-team-dev  ✓

# Switch active org (all commands use this for billing/access)
gc org switch <org-id>

# Show current org
gc org current

# List members
gc org members

# Invite someone
gc org invite teammate@gmail.com
gc org invite admin@company.com --role org:admin

# Remove a member
gc org remove <user-id>

# Pending invitations
gc org invitations
gc org invitations revoke <invitation-id>

# Container registry (enterprise snapshot isolation)
gc org registry get                                     # show current
gc org registry set --url ghcr.io/myco/envs --user x --pass y  # custom
gc org registry clear                                   # revert to default
```

---

## 3. Environments

> **Note:** `gc env create` provisions a real Hetzner server. Requires valid `HETZNER_API_TOKEN` in `.env`. Costs real money (~$0.01/hr for small).

```bash
# Create an environment
gc env create --name my-env --size small --region fsn1
gc env create --name ml-box --size large --region fsn1
gc env create --name gpu-env --size gpu --region fsn1

# Sizes: small ($0.15/hr), medium ($0.35/hr), large ($0.70/hr), gpu (custom)
# Regions: fsn1 (Falkenstein), nbg1 (Nuremberg), hel1 (Helsinki)

# List all environments
gc env list

# Get details
gc env status <env-id>

# SSH into a running environment
gc env ssh <env-id>

# Run a command remotely
gc env exec <env-id> -- "pip install torch numpy"
gc env exec <env-id> -- "python train.py"

# View container logs
gc env logs <env-id>

# Health check (agent status, CPU, memory, disk)
gc env health <env-id>

# Stop (takes pre-destroy snapshot, stops billing)
gc env destroy <env-id>

# Autoscaling
gc env autoscale enable <env-id> --min 1 --max 5 --target-cpu 0.7
gc env autoscale status <env-id>
gc env autoscale history <env-id>
gc env autoscale disable <env-id>
```

---

## 4. Context Store (Branch Memory)

The context store is Gradient's core feature — it gives every branch persistent memory of packages, failures, patterns, and config.

```bash
# Save context for a branch
gc context save --branch main
gc context save --branch feature/auth --commit abc123

# List all contexts
gc context list
#   main                            OS: ubuntu-24.04    Packages: 0

# Show full context (JSON)
gc context show --branch main

# Delete context
gc context delete --branch feature/old
```

---

## 5. Live Context Mesh (Real-Time Sharing)

The mesh lets multiple environments on the same branch share discoveries in real-time via NATS.

```bash
# Check mesh health
gc context mesh-health
#   ✓ Status:    ok
#     Bus Type:   nats
#     Connected:  true
#     Messages:   3

# Publish events manually (or agents do this automatically)
gc context publish --branch main --type package_installed --key torch --value "2.1.0"
gc context publish --branch main --type pattern_learned --key "oom_fix" --value "Reduce batch to 32"
gc context publish --branch main --type config_changed --key "CUDA_VISIBLE_DEVICES" --value "0,1"
gc context publish --branch main --type test_failed --key "test_model" --value "OOM at batch=64"

# Event types: package_installed, test_failed, pattern_learned,
#              config_changed, error_encountered, custom

# Query event history
gc context events --branch main
#   [05:53:16] package_installed    env=cli          seq=1
#          {"manager":"manual","name":"torch","version":"2.1.0"}
#   [05:53:16] pattern_learned      env=cli          seq=2
#          {"key":"oom_fix","value":"Reduce batch_size to 32 when GPU OOMs at 64"}

# Filter events
gc context events --branch main --types package_installed,test_failed
gc context events --branch main --since 2026-03-04T00:00:00Z
gc context events --branch main --limit 10

# Stream events in real-time (SSE)
gc context live --branch main

# Stream events via WebSocket (bidirectional)
gc context ws --branch main
```

---

## 6. Snapshots

```bash
# List snapshots for a branch
gc snapshot list --branch main

# Take a manual snapshot of a running environment
gc snapshot create --env <env-id>

# Snapshots happen automatically:
#   - Every 15 min (gradient-agent cron)
#   - On git push (webhook trigger)
#   - On env destroy (pre-destroy hook)
```

---

## 7. Repos (GitHub Auto-Fork)

Connect GitHub repos so new branches automatically inherit context + snapshots from their parent.

```bash
# Connect a repo (requires Gradient GitHub App installed on the repo)
gc repo connect --repo myorg/myapp

# List connected repos
gc repo list

# Disconnect
gc repo disconnect <repo-id>

# After connecting:
#   git checkout -b feature/new-algo    ← webhook fires
#   → Gradient copies main's context + snapshot pointers to feature/new-algo
#   → New environments on this branch boot with main's state
```

---

## 8. Secrets

```bash
# Sync secrets from Vault to an environment
gc secret sync <env-id> --keys DB_PASSWORD --backend vault --path secret/data/myapp

# Secrets are managed in Vault (http://localhost:8200 for local dev)
# The sync command injects them into the environment's container
```

---

## 9. Billing

```bash
# View current month usage
gc billing usage
#   Usage Summary (2026-03)
#   ─────────────────────────────────
#     Small hours:   0.00  ($0.00)
#     Medium hours:  0.00  ($0.00)
#     Large hours:   0.00  ($0.00)
#     GPU hours:     0.00  ($0.00)
#   ─────────────────────────────────
#     Total:         0.00 hrs  $0.00

# Set up Stripe billing for your org
gc billing setup --name "My Startup" --email billing@mystartup.com

# List invoices
gc billing invoices
```

---

## 10. Agent Tasks (AI-Powered Development)

Use Claude Code to autonomously work on Linear issues or custom tasks.

```bash
# Check integration status
gc integration status

# Configure Claude Code (your Anthropic API key)
gc integration claude --api-key sk-ant-...

# View Linear connection
gc integration linear

# Create a task manually
gc task create --title "Add dark mode toggle" --branch feature/dark-mode

# Create and auto-start
gc task create --title "Fix auth bug" --description "SSO users can't login" --auto-start

# List tasks
gc task list
gc task list --status running

# Get task details
gc task get <task-id>

# View execution log
gc task logs <task-id>

# Start / Cancel / Retry
gc task start <task-id>
gc task cancel <task-id>
gc task retry <task-id>

# Statistics
gc task stats
```

### Linear Integration

1. Create an OAuth app at [linear.app/settings/api](https://linear.app/settings/api)
2. Set `LINEAR_CLIENT_ID`, `LINEAR_CLIENT_SECRET`, `LINEAR_REDIRECT_URI` in `.env`
3. Connect via dashboard: `/dashboard/integrations`
4. Label issues with `gradient-agent` and move to "Todo"
5. Gradient picks them up automatically

### Environment Variables for Agent Tasks

```bash
# Required for Linear integration
LINEAR_CLIENT_ID=lin_oauth_...
LINEAR_CLIENT_SECRET=...
LINEAR_REDIRECT_URI=https://api.usegradient.dev/api/v1/integrations/linear/callback

# Claude Code config is stored per-org in the database (no env var needed)
# Anthropic API key is set per-org via:
#   gc integration claude --api-key sk-ant-...
#   or via dashboard: /dashboard/integrations
```

---

## 11. API Endpoints (for integrations)

The API runs at `http://localhost:6767`. All endpoints except health/auth require `Authorization: Bearer <token>`.

```bash
# Health check
curl http://localhost:6767/api/v1/health

# List environments (with auth)
TOKEN=$(cat ~/.gradient/config.json | python3 -c "import sys,json; print(json.load(sys.stdin)['token'])")
curl -H "Authorization: Bearer $TOKEN" http://localhost:6767/api/v1/environments

# Integrations
curl -H "Authorization: Bearer $TOKEN" -H "X-Org-ID: $ORG_ID" http://localhost:6767/api/v1/integrations/linear
curl -H "Authorization: Bearer $TOKEN" -H "X-Org-ID: $ORG_ID" http://localhost:6767/api/v1/integrations/claude

# Tasks
curl -H "Authorization: Bearer $TOKEN" -H "X-Org-ID: $ORG_ID" http://localhost:6767/api/v1/tasks
curl -H "Authorization: Bearer $TOKEN" -H "X-Org-ID: $ORG_ID" http://localhost:6767/api/v1/tasks/<task-id>/events

# Onboarding status
curl -H "Authorization: Bearer $TOKEN" -H "X-Org-ID: $ORG_ID" http://localhost:6767/api/v1/onboarding/status

# Prometheus metrics
curl http://localhost:6767/metrics
```

---

## Typical Local Dev Workflow

```bash
# 1. Start everything
make dev

# 2. Login
gc auth login

# 3. Create an org
gc org create "Dev Team"

# 4. Connect a repo
gc repo connect --repo myorg/myapp

# 5. Save initial context for main branch
gc context save --branch main

# 6. Create an environment (requires Hetzner token)
gc env create --name dev-env --size small --region fsn1

# 7. Publish context events as you discover things
gc context publish --branch main --type package_installed --key numpy --value "1.26.0"
gc context publish --branch main --type pattern_learned --key "cuda_fix" --value "Set CUDA_VISIBLE_DEVICES=0"

# 8. Check what's been shared
gc context events --branch main

# 9. Stream events in real-time
gc context live --branch main

# 10. Check billing
gc billing usage
```

---

## What Works Without Hetzner

Everything except actually provisioning servers:

| Feature | Works Locally? | Notes |
|---------|---------------|-------|
| Auth (login/logout/status) | ✅ | Full Clerk integration |
| Orgs (create/list/invite) | ✅ | Via Clerk API |
| Context store (save/show/list) | ✅ | PostgreSQL |
| Live Context Mesh | ✅ | NATS JetStream |
| Event publishing/streaming | ✅ | Real-time via NATS |
| Snapshots (list/metadata) | ✅ | PostgreSQL |
| Repos (connect/list) | ✅ | PostgreSQL |
| Billing (usage/setup) | ✅ | Stripe |
| API + Prometheus metrics | ✅ | Go server |
| MCP server (AI agent tools) | ✅ | JSON-RPC stdio |
| Linear integration | ✅ | Needs `LINEAR_CLIENT_ID` + `LINEAR_CLIENT_SECRET` |
| Claude integration | ✅ | Per-org API key stored in DB |
| Agent tasks (create/list/run) | ✅ | PostgreSQL + Claude API |
| Onboarding wizard | ✅ | Web dashboard |
| Environment provisioning | ❌ | Needs `HETZNER_API_TOKEN` |
| SSH into environments | ❌ | Needs running server |
| Agent health/snapshots | ❌ | Needs running server |
