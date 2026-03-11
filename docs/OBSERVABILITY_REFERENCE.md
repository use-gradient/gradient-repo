# Gradient Observability Reference

All logs, status checks, and monitoring signals available in the Gradient system during task execution. Organized by lifecycle phase, with grep-friendly patterns and example database queries.

---

## 1. Task Lifecycle

### 1.1 Linear Webhook Receipt and Org Matching

When a Linear issue triggers a webhook, the API server logs the receipt, org resolution, and deduplication check.

| Log Pattern | File | Meaning |
|---|---|---|
| `[webhook/linear] ignoring event: ...` | `internal/api/server.go` | Event was not a valid task trigger (wrong action, missing data) |
| `[webhook/linear] received task: <title> (<external_id>)` | `internal/api/server.go` | Valid task parsed from Linear webhook |
| `[webhook/linear] no active Linear connections found` | `internal/api/server.go` | No org has Linear connected — task cannot be routed |
| `[webhook/linear] matched org <org_id> (filter match)` | `internal/api/server.go` | Org matched via label/state filter on the Linear connection |
| `[webhook/linear] matched org <org_id> (has connected repo)` | `internal/api/server.go` | Org matched because it has a connected GitHub repo |
| `[webhook/linear] matched org <org_id> (fallback to first)` | `internal/api/server.go` | No filter or repo match — using first available org |
| `[webhook/linear] task already exists for issue <id>, skipping` | `internal/api/server.go` | Duplicate Linear issue — task already created |
| `[webhook/linear] using repo <owner/repo>` | `internal/api/server.go` | Resolved which GitHub repo the task will use |
| `[webhook/linear] no connected repos, task will run without a repo` | `internal/api/server.go` | No repo linked — Claude will work in a bare environment |
| `[webhook/linear] created task <id>, classifying...` | `internal/api/server.go` | Task created in DB, classification starting |
| `[webhook/linear] failed to create task: ...` | `internal/api/server.go` | Task creation failed (check Claude config, DB) |

**Example:**

```bash
grep '\[webhook/linear\]' server.log
```

### 1.2 Task Classification

The classifier calls the Claude API to determine if a task is short (single agent) or long (multi-agent decomposition).

| Log Pattern | File | Meaning |
|---|---|---|
| `[webhook/linear] classification failed, defaulting to short: ...` | `internal/api/server.go` | Claude API call failed; task treated as short |
| `[webhook/linear] classification: <complexity> (<reasoning>)` | `internal/api/server.go` | Classification result (e.g., `short (simple bug fix)`) |
| `[classifier] Claude API call failed: ...` | `internal/services/task_classifier.go` | Anthropic API returned an error during classification |
| `[classifier] Failed to parse classification: ...` | `internal/services/task_classifier.go` | Claude response wasn't valid JSON — defaults to short |

**Example:**

```bash
grep '\[classifier\]' server.log
grep 'classification:' server.log
```

### 1.3 Multi-Agent Orchestration

When a task is classified as "long" with multiple sub-tasks, the orchestrator spawns parallel agent sessions.

| Log Pattern | File | Meaning |
|---|---|---|
| `[orchestrate] long task <id>: spawning <N> sub-agents` | `internal/api/server.go` | Multi-agent decomposition starting |
| `[orchestrate] failed to create manager session: ...` | `internal/api/server.go` | Manager session creation failed — falls back to single agent |
| `[orchestrate] failed to spawn sessions: ..., falling back to single agent` | `internal/api/server.go` | Parallel session creation failed |
| `[orchestrate] failed to create sub-task for role <role>: ...` | `internal/api/server.go` | Individual sub-task creation failed |
| `[orchestrate] sub-task <id> (role=<role>, branch=<branch>)` | `internal/api/server.go` | Sub-task created and about to be launched |
| `[orchestrate] all <N> sub-agents launched for task <id>, merge simulation started` | `internal/api/server.go` | All parallel agents dispatched, conflict detection active |

**Example:**

```bash
grep '\[orchestrate\]' server.log
```

### 1.4 Task Status Transitions

Tasks move through: `pending` → `running` → `complete` / `failed` / `cancelled`.

| Log Pattern | File | Meaning |
|---|---|---|
| `[task] failed to add log for task <id>: ...` | `internal/services/task_service.go` | Execution log write failed (DB issue) |

**Task execution log entries** (stored in `task_execution_log` table):

| Step | Status | Meaning |
|---|---|---|
| `created` | `completed` | Task record inserted |
| `execution_started` | `started` | Status set to `running`, `started_at` populated |
| `queued_for_execution` | `completed` | Handed off to the executor goroutine |
| `provisioning_env` | `started` | Executor looking for an environment |
| `env_waking` | `started` | Waking a sleeping environment |
| `env_provisioned` | `completed` | New environment created |
| `waiting_for_ssm` | `started` | Waiting for instance to accept remote commands |
| `waiting_for_docker` | `started` / `completed` | Cloud-init Docker/container readiness |
| `cloning_repo` | `started` | Git clone starting |
| `repo_cloned` | `completed` | Clone and branch creation succeeded |
| `claude_executing` | `started` | Claude Code invocation starting |
| `claude_done` | `completed` | Claude Code finished |
| `creating_pr` | `started` | Pull request creation starting |
| `pr_created` | `completed` | PR created successfully |
| `pr_failed` | `failed` | PR creation failed |
| `completed` | `completed` | Task finished |
| `failed` | `failed` | Task failed with error message |
| `cancelled` | `completed` | Task cancelled by user |
| `retried` | `completed` | Task retried (retry count incremented) |
| `throttled` | `failed` | Max concurrent tasks reached |

**Database queries:**

```sql
-- Current status of all tasks for an org
SELECT id, title, status, error_message, created_at, started_at, completed_at,
       duration_seconds, pr_url, commit_sha
FROM agent_tasks
WHERE org_id = '<org_id>'
ORDER BY created_at DESC
LIMIT 20;

-- Execution log for a specific task
SELECT step, status, message, created_at
FROM task_execution_log
WHERE task_id = '<task_id>'
ORDER BY created_at ASC;

-- Task stats
SELECT status, COUNT(*) FROM agent_tasks
WHERE org_id = '<org_id>'
GROUP BY status;

-- Running tasks with their environments
SELECT t.id, t.title, t.status, t.environment_id, e.provider, e.cluster_name, e.ip_address
FROM agent_tasks t
LEFT JOIN environments e ON t.environment_id = e.id
WHERE t.status = 'running';
```

---

## 2. Environment Lifecycle

### 2.1 Provider Initialization (Server Startup)

Logged once during server boot in `internal/api/server.go`:

| Log Pattern | File | Meaning |
|---|---|---|
| `[init] Hetzner provider initialized` | `internal/api/server.go` | Hetzner API token valid, provider ready |
| `[init] Hetzner provider not available: ...` | `internal/api/server.go` | Hetzner setup failed (bad token, missing config) |
| `[init] AWS provider initialized` | `internal/api/server.go` | AWS credentials and AMI valid, provider ready |
| `[init] AWS provider not available: ...` | `internal/api/server.go` | AWS setup failed |
| `[init] NATS event bus connected (Live Context Mesh enabled)` | `internal/api/server.go` | NATS connected, mesh fully operational |
| `[init] NATS event bus not available: ... (using local bus)` | `internal/api/server.go` | NATS failed, using in-memory fallback |
| `[init] NATS not configured — using local event bus` | `internal/api/server.go` | No NATS_URL set |
| `[init] ✓ Stripe billing configured` | `internal/api/server.go` | Stripe keys present |
| `[init] ⚠️  WARNING: STRIPE_SECRET_KEY is not set` | `internal/api/server.go` | Billing will not work |
| `[init] ✓ GitHub OAuth configured` | `internal/api/server.go` | GitHub OAuth ready |
| `[init] ✓ Linear integration configured` | `internal/api/server.go` | Linear OAuth ready |
| `[init] ✓ Claude Code service initialized` | `internal/api/server.go` | Claude service ready |
| `[init] ✓ Agent task service initialized` | `internal/api/server.go` | Task executor ready |
| `[init] ✓ Agent-Native VCS services initialized` | `internal/api/server.go` | Sessions, merge, classifier, propagation ready |
| `[init] ✓ Environment idle monitor started (check every 5m, sleep after 30m)` | `internal/api/server.go` | Idle monitor background loop running |
| `[init] Vault secret syncer initialized` | `internal/api/server.go` | Vault client connected |

**Example:**

```bash
grep '\[init\]' server.log
```

### 2.2 Environment Creation

| Log Pattern | File | Meaning |
|---|---|---|
| `Hetzner: Creating server (type=<type>, location=<loc>) for env <name>` | `pkg/env/hetzner_provider.go` | Hetzner API call to create server |
| `Hetzner: Server <name> (ID: <id>) created for env <name>` | `pkg/env/hetzner_provider.go` | Server creation API returned success |
| `Hetzner: Warning — server creation action error: ...` | `pkg/env/hetzner_provider.go` | Server action (boot) had a warning |
| `AWS: Creating EC2 instance (type=<type>, AMI=<ami>) for env <name>` | `pkg/env/aws_provider.go` | EC2 RunInstances call starting |
| `AWS: EC2 instance <id> launched for env <name>` | `pkg/env/aws_provider.go` | EC2 instance ID returned |
| `Failed to create environment <id>: ...` | `internal/services/env_service.go` | Provider creation failed |
| `Environment <id> is now running on <ref> (cold boot: <duration>)` | `internal/services/env_service.go` | Cold boot completed with timing |
| `[warm-pool] Got warm server <id> for env <id> — configuring...` | `internal/services/env_service.go` | Warm pool server acquired |
| `[warm-pool] Environment <id> running on warm server <id> (boot: <duration>)` | `internal/services/env_service.go` | Warm boot completed (much faster) |
| `[warm-pool] Failed to start container on warm server: ...` | `internal/services/env_service.go` | Warm boot failed, falling back to cold |

**Example:**

```bash
grep 'Hetzner:' server.log
grep 'AWS:' server.log
grep 'cold boot\|warm server' server.log
```

### 2.3 Cloud-Init Progress

Cloud-init runs on the newly created server. The executor polls `/tmp/gradient-status` for readiness.

| Log Pattern | File | Meaning |
|---|---|---|
| `[executor] Waiting for cloud-init on <ref> (output: <status>)...` | `internal/services/task_executor.go` | Polling cloud-init; output is current status file content |

The cloud-init script itself logs to `/var/log/gradient-init.log` on the server. Key milestones:

```
Gradient environment init starting: <env-name>
Installing Docker and base packages...
Waiting for Docker daemon...
Pulling image: <image>
Installing tools inside container...
Gradient environment ready: <env-name>
```

The readiness signal is `echo "ready" > /tmp/gradient-status`.

**To check cloud-init on a Hetzner server:**

```bash
ssh root@<server-ip> cat /var/log/gradient-init.log
ssh root@<server-ip> cat /tmp/gradient-status
```

### 2.4 SSH/SSM Readiness

| Log Pattern | File | Meaning |
|---|---|---|
| `Hetzner: Waiting for SSH on <ip>...` | `pkg/env/hetzner_provider.go` | SSH connection attempt failed, retrying |
| `AWS: Instance <id> SSM agent is ready` | `pkg/env/aws_provider.go` | SSM agent responding to commands |

### 2.5 Environment Sleeping and Waking

| Log Pattern | File | Meaning |
|---|---|---|
| `[sleep] Snapshot taken for env <id>: <imageRef>` | `internal/services/env_service.go` | Pre-sleep snapshot saved |
| `[sleep] Failed to stop VM for env <id>: ...` | `internal/services/env_service.go` | VM destroy during sleep failed |
| `[wake] Failed to wake environment <id>: ...` | `internal/services/env_service.go` | Wake (re-create) failed, reverts to sleeping |
| `[wake] Environment <id> woken up on <ref>` | `internal/services/env_service.go` | Environment successfully woken |
| `[executor] Failed to wake env <id>: ..., creating new` | `internal/services/task_executor.go` | Wake failed in executor, will cold-boot instead |
| `[executor] Environment <id> put to sleep after task completion` | `internal/services/task_executor.go` | Post-task sleep initiated |
| `[executor] Failed to sleep env <id> after task: ...` | `internal/services/task_executor.go` | Post-task sleep failed |

**Example:**

```bash
grep '\[sleep\]\|\[wake\]' server.log
```

### 2.6 Snapshot and Destruction

| Log Pattern | File | Meaning |
|---|---|---|
| `Hetzner: Taking snapshot of server <ref> → <imageRef>` | `pkg/env/hetzner_provider.go` | Docker commit + push starting |
| `Hetzner: Snapshot <imageRef> completed successfully` | `pkg/env/hetzner_provider.go` | Snapshot pushed to registry |
| `Hetzner: Creating server-level snapshot for server <id>...` | `pkg/env/hetzner_provider.go` | Full server disk snapshot (slower, more thorough) |
| `Hetzner: Server snapshot created (image ID: <id>) for server <id>` | `pkg/env/hetzner_provider.go` | Server snapshot complete |
| `Hetzner: Exporting container on server <ref> → <imageRef>` | `pkg/env/hetzner_provider.go` | Docker export (more reliable than commit) |
| `Hetzner: Container export <imageRef> completed successfully` | `pkg/env/hetzner_provider.go` | Export + push done |
| `Hetzner: Deleting server <id>` | `pkg/env/hetzner_provider.go` | Server deletion API call |
| `Hetzner: Server <id> deletion initiated` | `pkg/env/hetzner_provider.go` | Deletion accepted |
| `AWS: Taking snapshot of instance <id> → <imageRef>` | `pkg/env/aws_provider.go` | SSM docker commit command sent |
| `AWS: Snapshot <imageRef> completed successfully` | `pkg/env/aws_provider.go` | SSM command succeeded |
| `AWS: Terminating EC2 instance <id>` | `pkg/env/aws_provider.go` | TerminateInstances call |
| `AWS: EC2 instance <id> termination initiated` | `pkg/env/aws_provider.go` | Termination accepted |
| `[pre-destroy] Container export completed: <imageRef> for env <id>` | `internal/services/env_service.go` | Pre-destroy export to org registry |
| `[pre-destroy] Container export failed for env <id>, trying docker commit...` | `internal/services/env_service.go` | Export failed, falling back |
| `[pre-destroy] Docker commit snapshot: <imageRef> for env <id>` | `internal/services/env_service.go` | Fallback commit succeeded |
| `[pre-destroy] Docker commit snapshot failed for env <id>: ...` | `internal/services/env_service.go` | Both snapshot strategies failed |
| `[pre-destroy] Org registry export failed for env <id>, trying provider default...` | `internal/services/env_service.go` | Org-specific registry push failed |
| `[destroy] pre-destroy snapshot failed for env <id>: ...` | `internal/api/server.go` | Pre-destroy snapshot failed (continuing with destroy) |
| `[destroy] pre-destroy snapshot saved: <envId> → <imageRef>` | `internal/api/server.go` | Pre-destroy snapshot record saved |
| `Environment <id> server recycled to warm pool` | `internal/services/env_service.go` | Server returned to warm pool instead of destroyed |
| `[executor] cleanup failed for env <id>: ...` | `internal/services/task_executor.go` | Post-task environment cleanup failed |

**Example:**

```bash
grep '\[pre-destroy\]\|Deleting server\|Terminating EC2' server.log
```

### 2.7 Idle Monitoring

The idle monitor runs every 5 minutes and sleeps repo-scoped environments idle for 30+ minutes.

| Log Pattern | File | Meaning |
|---|---|---|
| `[idle-monitor] Failed to list running environments: ...` | `internal/services/env_idle_monitor.go` | DB query failed |
| `[idle-monitor] Sleeping idle environment <id> (repo=<repo>, branch=<branch>, idle since <time>)` | `internal/services/env_idle_monitor.go` | Environment being put to sleep |
| `[idle-monitor] Failed to sleep env <id>: ...` | `internal/services/env_idle_monitor.go` | Sleep operation failed |

**Example:**

```bash
grep '\[idle-monitor\]' server.log
```

**Database query to find idle environments:**

```sql
SELECT id, name, repo_full_name, context_branch, status, updated_at,
       NOW() - updated_at AS idle_duration
FROM environments
WHERE status = 'running'
  AND repo_full_name IS NOT NULL
  AND updated_at < NOW() - INTERVAL '30 minutes'
ORDER BY updated_at ASC;
```

---

## 3. Agent Execution

### 3.1 Claude CLI Check

| Log Pattern | File | Meaning |
|---|---|---|
| `[executor] Claude CLI not found in container, attempting install...` | `internal/services/task_executor.go` | `which claude` returned empty; installing Node + Claude CLI |
| `[executor] Claude CLI install failed: ...` | `internal/services/task_executor.go` | npm install of Claude CLI failed |
| `[executor] Claude CLI installed successfully` | `internal/services/task_executor.go` | Fresh install succeeded |
| `[executor] Claude CLI found at: <path>` | `internal/services/task_executor.go` | Claude CLI already present (from snapshot/cloud-init) |

### 3.2 Claude Code Invocation

The actual command run inside the container:

```bash
export ANTHROPIC_API_KEY="<key>" && \
  cd /workspace/repo && \
  claude -p "$(cat /gradient/task-prompt.md)" \
    --output-format text \
    --model <model> \
    --max-turns <N> \
    --allowedTools "<tools>" \
    --verbose 2>&1; exit 0
```

| Log Pattern | File | Meaning |
|---|---|---|
| `[executor] Running Claude Code for task <id>...` | `internal/services/task_executor.go` | Claude invocation starting (45min timeout) |
| `[executor] Claude Code returned error (may still have results): ...` | `internal/services/task_executor.go` | Claude exited non-zero (often still has useful output) |
| `[executor] Claude Code output for task <id>: <truncated>` | `internal/services/task_executor.go` | First 1000 chars of Claude output |
| `[executor] task <id> not found: ...` | `internal/services/task_executor.go` | Task disappeared from DB during execution |
| `[executor] Detected default branch for <repo>: <branch>` | `internal/services/task_executor.go` | Auto-detected default branch from GitHub API |

**Example:**

```bash
grep '\[executor\]' server.log
grep 'Running Claude Code\|Claude Code output\|Claude Code returned error' server.log
```

### 3.3 SSH Into a Running Environment

To inspect a running agent environment:

```bash
# Get SSH info from the API
curl -H "Authorization: Bearer <token>" \
  https://<api>/api/v1/environments/<env-id>/ssh-info

# SSH directly (Hetzner)
ssh root@<server-ip> -t 'docker exec -it gradient-env /bin/bash'

# Once inside the container:
cat /gradient/task-prompt.md           # The prompt sent to Claude
cat /var/log/gradient-init.log         # Cloud-init log (on host, not container)
ls /workspace/repo                     # Cloned repo
git log --oneline                      # Claude's commits
ps aux | grep claude                   # Check if Claude is running
```

**For AWS (SSM):**

```bash
aws ssm start-session --target <instance-id>
# Then inside:
docker exec -it gradient-env /bin/bash
```

---

## 4. Git Operations

### 4.1 Repo Cloning

| Log Pattern (addLog step) | File | Meaning |
|---|---|---|
| `cloning_repo` / `started` | `internal/services/task_executor.go` | Clone starting (`git clone --depth=50`) |
| `repo_cloned` / `completed` | `internal/services/task_executor.go` | Clone succeeded, branch created |

The clone script checks for `CLONE_OK` in the output. For reused environments, it does `git fetch` + checkout instead of a fresh clone.

### 4.2 Git Diff and Commit Check

| Log Pattern | File | Meaning |
|---|---|---|
| `[executor] Git diff check for task <id>: <output>` | `internal/services/task_executor.go` | Shows commits between base and HEAD after Claude runs |

The diff check command: `git log --oneline <taskBranch>..HEAD`. Output `NO_NEW_COMMITS` means Claude didn't commit anything.

### 4.3 Git Push

| Log Pattern | File | Meaning |
|---|---|---|
| `[executor] git push warning: <output>` | `internal/services/task_executor.go` | Push returned `PUSH_FAILED` |
| `[executor] git push for task <id>: <output>` | `internal/services/task_executor.go` | Push output (success) |

### 4.4 PR Creation

| Log Pattern | File | Meaning |
|---|---|---|
| `[executor] PR creation failed: ...` | `internal/services/task_executor.go` | GitHub API error creating PR |
| `[executor] Task <id> completed (PR: <url>, SHA: <sha>)` | `internal/services/task_executor.go` | Full pipeline done |

Common PR creation errors:
- **GitHub API 422**: Usually means the head branch has no commits ahead of base, or PR already exists
- **GitHub API 404**: Repo not found or token lacks permissions
- **GitHub not connected**: No OAuth token stored

**Example:**

```bash
grep 'git push\|PR creation\|GitHub API' server.log
```

---

## 5. Context Mesh

### 5.1 Event Publishing

The executor publishes mesh events at key milestones:

| Event Key | When Published | Data |
|---|---|---|
| `repo_cloned` | After successful git clone | `{repo, branch}` |
| `task_started` | Before Claude invocation | `{task_id, title, env_id}` |
| `task_completed` | After task completion | `{task_id, commit_sha, pr_url, summary}` |
| `pr_created` (type: `EventPRCreated`) | After PR created | `{pr_url, head, base, task_id}` |

All events are published with `source: "executor"`.

### 5.2 Event Propagation

| Log Pattern | File | Meaning |
|---|---|---|
| `[propagation] Bug discovered by session <id>: <desc>` | `internal/services/event_propagation.go` | Bug event received, finding affected sessions |
| `[propagation] Notifying session <id> (<role>) about bug in <files>` | `internal/services/event_propagation.go` | Cross-session bug notification |
| `[propagation] Contract <id> <action> by session <id>` | `internal/services/event_propagation.go` | Contract update/violation detected |
| `[propagation] Contract violation affects session <id> (<role>)` | `internal/services/event_propagation.go` | Active session affected by violation |
| `[propagation] Conflict detected between sessions <a> and <b>: <desc>` | `internal/services/event_propagation.go` | Merge conflict between parallel agents |
| `[propagation] Stale context event on branch <branch>` | `internal/services/event_propagation.go` | Context data is outdated |
| `[propagation] Propagated <type> event from <branch> to <branch> (repo <repo>)` | `internal/services/event_propagation.go` | Cross-branch event propagation succeeded |
| `[propagation] Failed to propagate event <id> to branch <branch>: ...` | `internal/services/event_propagation.go` | Propagation failed |

**Propagatable event types:** `package_installed`, `pattern_learned`, `config_changed`, `dependency_added`, `bug_discovered`, `contract_updated`, `test_failed`, `test_fixed`.

**Example:**

```bash
grep '\[propagation\]' server.log
```

### 5.3 NATS Connection Status

| Log Pattern | File | Meaning |
|---|---|---|
| `[livectx] Event bus connected to NATS at <url> (stream: GRADIENT_CTX)` | `pkg/livectx/bus.go` | Initial connection established |
| `[livectx] NATS disconnected: ...` | `pkg/livectx/bus.go` | Connection lost |
| `[livectx] NATS reconnected to <url>` | `pkg/livectx/bus.go` | Auto-reconnected |
| `[livectx] NATS connection closed` | `pkg/livectx/bus.go` | Connection permanently closed |
| `[livectx] NATS error: ...` | `pkg/livectx/bus.go` | General NATS error |
| `[livectx] Subscribed to <subject> (consumer: <name>)` | `pkg/livectx/bus.go` | New durable consumer created |
| `[livectx] Subscribed to all events for org <id> (subject: <subject>)` | `pkg/livectx/bus.go` | Wildcard subscriber started |
| `[livectx] Unsubscribed from <subject>` | `pkg/livectx/bus.go` | Consumer stopped |
| `[livectx] Failed to unmarshal event from <subject>: ...` | `pkg/livectx/bus.go` | Corrupt event on the bus |
| `[livectx] Handler error for event <id> on <subject>: ...` | `pkg/livectx/bus.go` | Event handler returned an error |
| `[livectx] Failed to persist event <id> to store: ...` | `pkg/livectx/bus.go` | PostgreSQL write failed (event still broadcast) |
| `[livectx] Failed to broadcast event <id>: ...` | `pkg/livectx/bus.go` | NATS publish failed |
| `[livectx] Event bus closed` | `pkg/livectx/bus.go` | Graceful shutdown |
| `[livectx-local] Handler error: ...` | `pkg/livectx/bus.go` | Error in local (non-NATS) bus handler |
| `[sse] Dropping event <id> for slow client on <org>/<branch>` | `internal/api/server.go` | SSE client too slow, event dropped |
| `[sse] Failed to subscribe for SSE: ...` | `internal/api/server.go` | SSE NATS subscription failed |

**Example:**

```bash
grep '\[livectx\]' server.log
```

**Mesh health API endpoint:**

```bash
curl -H "Authorization: Bearer <token>" https://<api>/api/v1/mesh/health
```

Returns: `{"status":"ok","bus":"nats","connected":true,"stream":{...}}` or `{"status":"ok","bus":"local"}`.

---

## 6. GitHub Webhooks (Inbound)

### 6.1 Webhook Processing

| Log Pattern | File | Meaning |
|---|---|---|
| `[webhook] Error handling <type> event: ...` | `internal/api/server.go` | Webhook handler returned an error |

### 6.2 Branch Create

| Log Pattern | File | Meaning |
|---|---|---|
| `[repo] Branch created: <branch> (parent: <default>) in <repo>` | `internal/services/repo_service.go` | New branch detected |
| `[repo] Auto-forked context: <parent> → <new> (org <id>)` | `internal/services/repo_service.go` | Context copied to new branch |
| `[repo] Auto-forked snapshot: <parent> → <new> (image: <ref>, org: <id>)` | `internal/services/repo_service.go` | Snapshot pointer copied |
| `[repo] Auto-forked environment: <parent> → <new> (env <id>, repo <repo>)` | `internal/services/repo_service.go` | New environment created for branch |
| `[repo] No context found for parent branch ...` | `internal/services/repo_service.go` | Parent has no context to fork |
| `[repo] No snapshot found for parent branch ...` | `internal/services/repo_service.go` | Parent has no snapshot to fork |
| `[repo] No environment found for parent branch ...` | `internal/services/repo_service.go` | No parent env to base fork on |

### 6.3 Push Events

| Log Pattern | File | Meaning |
|---|---|---|
| `[repo] Push to <branch>@<sha> in <repo>` | `internal/services/repo_service.go` | Push event received |
| `[repo] Auto-snapshot on push: env=<id>, image=<ref>` | `internal/services/repo_service.go` | Auto-snapshot triggered by push |
| `[repo] Auto-snapshot failed for env <id>: ...` | `internal/services/repo_service.go` | Auto-snapshot failed |

### 6.4 Branch Delete

| Log Pattern | File | Meaning |
|---|---|---|
| `[repo] Branch deleted: <branch> in <repo>` | `internal/services/repo_service.go` | Branch deletion detected |
| `[repo] Branch <branch> deleted in org <id>. Context and snapshots preserved for history.` | `internal/services/repo_service.go` | Soft-delete — no cleanup |

### 6.5 Pull Request Merge

| Log Pattern | File | Meaning |
|---|---|---|
| `[repo] PR merged: <head> → <base> in <repo>` | `internal/services/repo_service.go` | PR merge detected |
| `[repo] Processing PR merge cleanup for org <id>: <head> → <base>` | `internal/services/repo_service.go` | Cleanup starting |
| `[collapse] Context merged from <head> into <base> for repo <repo>` | `internal/services/repo_service.go` | Context data merged |
| `[collapse] No context found for merged branch <branch>, skipping collapse` | `internal/services/repo_service.go` | Nothing to merge |
| `[collapse] No base context for <branch>, creating from head` | `internal/services/repo_service.go` | Base branch had no context, using head's |
| `[cleanup] Destroyed environment <id> for merged branch <branch>` | `internal/services/repo_service.go` | Branch environment cleaned up |
| `[cleanup] Failed to destroy environment <id> for branch <branch>: ...` | `internal/services/repo_service.go` | Environment cleanup failed |
| `[cleanup] Cleaned up resources for merged branch <branch> in repo <repo>` | `internal/services/repo_service.go` | Full cleanup complete |

### 6.6 GitHub App Installation

| Log Pattern | File | Meaning |
|---|---|---|
| `[repo] GitHub App installed: installation=<id>, account=<login>, repos=[...]` | `internal/services/repo_service.go` | App installed on new account |
| `[repo] GitHub App uninstalled: installation=<id>` | `internal/services/repo_service.go` | App removed |
| `[repo] WARNING: GITHUB_APP_WEBHOOK_SECRET not set, skipping signature verification` | `internal/services/repo_service.go` | Webhook signatures not being verified |
| `[repo] Connected repo <repo> to org <id> (webhook <webhook_id>)` | `internal/services/repo_service.go` | Repo connection created with webhook |
| `[repo] Ignoring GitHub event: <type>` | `internal/services/repo_service.go` | Unhandled event type |

**Example:**

```bash
grep '\[repo\]\|\[collapse\]\|\[cleanup\]' server.log
```

---

## 7. Where to Look

### 7.1 Server Terminal Logs

The main API process writes to stdout. All `log.Printf` and `fmt.Printf` output goes here.

```bash
# If running directly
grep '<pattern>' server.log

# If running via Docker
docker logs gradient-api 2>&1 | grep '<pattern>'

# If running via systemd
journalctl -u gradient-api --since "1 hour ago" | grep '<pattern>'
```

### 7.2 Database Queries

```sql
-- Environment status overview
SELECT status, provider, COUNT(*) FROM environments GROUP BY status, provider;

-- Find a specific environment
SELECT id, name, status, provider, cluster_name, ip_address, context_branch,
       config->>'boot_time_ms' AS boot_ms, config->>'boot_type' AS boot_type,
       created_at, updated_at
FROM environments WHERE id = '<env-id>';

-- Task pipeline status
SELECT id, title, status, environment_id, branch, error_message,
       started_at, completed_at, duration_seconds, pr_url
FROM agent_tasks WHERE org_id = '<org-id>'
ORDER BY created_at DESC LIMIT 10;

-- Execution log timeline
SELECT step, status, message, created_at
FROM task_execution_log WHERE task_id = '<task-id>'
ORDER BY created_at ASC;

-- Context events for a branch
SELECT id, event_type, source, data, timestamp, sequence
FROM context_events
WHERE org_id = '<org-id>' AND branch = '<branch>'
ORDER BY sequence DESC LIMIT 20;

-- Agent sessions (multi-agent)
SELECT id, task_id, agent_role, branch_name, status, created_at
FROM agent_sessions WHERE task_id = '<task-id>';

-- Snapshot history
SELECT id, branch, snapshot_type, image_ref, created_at
FROM snapshots WHERE org_id = '<org-id>'
ORDER BY created_at DESC LIMIT 10;
```

### 7.3 Hetzner Cloud Console / API

```bash
# List Gradient-managed servers
curl -H "Authorization: Bearer $HETZNER_API_TOKEN" \
  'https://api.hetzner.cloud/v1/servers?label_selector=managed-by=gradient'

# Get specific server
curl -H "Authorization: Bearer $HETZNER_API_TOKEN" \
  "https://api.hetzner.cloud/v1/servers/<server-id>"
```

### 7.4 AWS CLI for EC2

```bash
# List Gradient-managed instances
aws ec2 describe-instances \
  --filters "Name=tag:managed-by,Values=gradient" \
  --query 'Reservations[].Instances[].{ID:InstanceId,State:State.Name,IP:PublicIpAddress,Type:InstanceType}'

# Check SSM connectivity
aws ssm describe-instance-information \
  --filters "Key=InstanceIds,Values=<instance-id>"

# Get cloud-init log via SSM
aws ssm send-command \
  --instance-ids <instance-id> \
  --document-name AWS-RunShellScript \
  --parameters 'commands=["cat /var/log/gradient-init.log"]'
```

### 7.5 Frontend Polling Endpoints

The web frontend polls these endpoints for real-time updates:

| Endpoint | Method | Purpose |
|---|---|---|
| `GET /api/v1/tasks` | Polling | List tasks with status |
| `GET /api/v1/tasks/<id>` | Polling | Single task status |
| `GET /api/v1/tasks/<id>/logs` | Polling | Execution log entries |
| `GET /api/v1/tasks/stats` | Polling | Aggregate task statistics |
| `GET /api/v1/environments` | Polling | Environment list with status |
| `GET /api/v1/environments/<id>` | Polling | Single environment details |
| `GET /api/v1/environments/<id>/health` | Polling | Agent health proxy |
| `GET /api/v1/events/stream?branch=<branch>` | SSE | Real-time event stream |
| `GET /api/v1/events/ws?branch=<branch>` | WebSocket | Real-time event stream |
| `GET /api/v1/mesh/health` | Polling | NATS/mesh health |
| `GET /metrics` | Scrape | Prometheus-compatible metrics |

### 7.6 Prometheus Metrics

The `/metrics` endpoint exposes:

| Metric | Type | Description |
|---|---|---|
| `gradient_environments_total{status}` | gauge | Environments by status (running, creating, failed, destroyed) |
| `gradient_boot_time_avg_ms{type}` | gauge | Average boot time (warm vs cold) |
| `gradient_boot_time_p95_ms{type}` | gauge | P95 boot time |
| `gradient_boot_count{type}` | gauge | Boot count by type |
| `gradient_warm_pool_size{status}` | gauge | Warm pool servers (ready, warming, assigned) |
| `gradient_snapshots_total` | counter | Total snapshots taken |
| `gradient_autoscale_policies_enabled` | gauge | Active autoscale policies |

---

## 8. Common Failure Modes

### 8.1 Claude API Credits Exhausted

**Symptoms:**
- `[classifier] Claude API call failed: Anthropic API 429: ...`
- `[executor] Claude Code returned error` with "rate limit" or "insufficient credits" in output
- Tasks fail at the `claude_executing` step

**Grep:**

```bash
grep '429\|rate limit\|insufficient\|credits' server.log
```

**Fix:** Check your Anthropic API key balance. Update the key in Integrations → Claude Code.

### 8.2 Hetzner Location/Type Mismatch

Hetzner uses different server series by location: `cx` for EU (fsn1/nbg1/hel1) and `cpx` for US/APAC (ash/hil).

**Symptoms:**
- `failed to create Hetzner server: ... (409)` — server type not available in location
- `Hetzner: Creating server (type=cx23, location=ash)` followed by error

**Grep:**

```bash
grep 'failed to create Hetzner server' server.log
```

**Fix:** Check that `HETZNER_LOCATION` matches available server types. EU locations use `cx` series, US locations use `cpx` series.

### 8.3 Cloud-Init Timeout

**Symptoms:**
- Task fails with: `Cloud-init did not complete within 5 minutes (Docker not installed)`
- Repeated: `[executor] Waiting for cloud-init on <ref> (output: waiting)...`

**Grep:**

```bash
grep 'Cloud-init did not complete\|Waiting for cloud-init' server.log
```

**Debug:** SSH into the server and check `/var/log/gradient-init.log`. Common causes:
- `apt-get update` hung on a lock or slow mirror
- Docker daemon failed to start
- Image pull failed (bad registry credentials)

### 8.4 Git Command Not Found

**Symptoms:**
- Repo clone fails with `git: command not found`
- This happens when running git on the host instead of inside the container

**Grep:**

```bash
grep 'git: command not found\|Repo clone failed' server.log
```

**Fix:** All git operations should run inside the `gradient-env` container via `docker exec gradient-env bash -c '...'`. If git is missing inside the container, cloud-init package install may have failed.

### 8.5 Docker Registry Auth Failures

**Symptoms:**
- Cloud-init stalls at `Pulling image: ...`
- Snapshot push fails with `denied` or `unauthorized`
- `[pre-destroy] Org registry export failed`

**Grep:**

```bash
grep 'denied\|unauthorized\|login.*failed\|registry' server.log
```

**Fix:** Verify registry credentials in `.env` (`HETZNER_REGISTRY_URL`, `HETZNER_REGISTRY_USER`, `HETZNER_REGISTRY_PASS`) or org-level settings (`PUT /api/v1/orgs/settings/registry`).

### 8.6 PR Creation 422 Errors

**Symptoms:**
- `[executor] PR creation failed: GitHub API 422: ...`
- Common 422 reasons:
  - "No commits between base and head" — Claude didn't push any changes
  - "A pull request already exists" — duplicate PR for same branches
  - "Validation failed" — branch doesn't exist on remote

**Grep:**

```bash
grep 'PR creation failed\|GitHub API 422' server.log
```

**Debug:** Check the git diff output logged just before the PR attempt:

```bash
grep 'Git diff check for task' server.log
```

If it shows `NO_NEW_COMMITS`, Claude didn't make any changes.

### 8.7 SSM/SSH Never Ready

**Symptoms:**
- Task fails with: `Instance never became SSM-ready: ...`
- Hetzner: repeated `Hetzner: Waiting for SSH on <ip>...` then timeout

**Grep:**

```bash
grep 'SSM-ready\|Waiting for SSH' server.log
```

**Debug for AWS:** Check the instance state in EC2 console. Verify the IAM instance profile has SSM permissions and the security group allows SSM traffic.

**Debug for Hetzner:** Verify the SSH key IDs in `HETZNER_SSH_KEY_IDS` match keys registered in Hetzner Cloud. Try `ssh root@<ip>` manually.

### 8.8 Max Concurrent Tasks

**Symptoms:**
- Task immediately fails with step `throttled`
- Log: `Max concurrent tasks (5) reached`

**Grep:**

```bash
grep 'throttled\|Max concurrent tasks' server.log
```

**Fix:** Wait for running tasks to complete, or increase `maxConcurrent` in the executor configuration.

---

## Quick Grep Cheatsheet

```bash
# Full task lifecycle for a specific task ID
grep '<task-id-prefix>' server.log

# All executor activity
grep '\[executor\]' server.log

# All webhook processing
grep '\[webhook' server.log

# All environment operations
grep 'Hetzner:\|AWS:\|\[sleep\]\|\[wake\]\|\[warm-pool\]\|\[idle-monitor\]' server.log

# All errors and failures
grep -i 'failed\|error\|panic\|timeout' server.log

# Context mesh activity
grep '\[livectx\]\|\[propagation\]' server.log

# Git and PR operations
grep 'git push\|PR creation\|CLONE_OK\|PUSH_FAILED\|Git diff check' server.log

# Initialization / startup
grep '\[init\]' server.log

# GitHub webhook events
grep '\[repo\]\|\[collapse\]\|\[cleanup\]' server.log

# Classification
grep '\[classifier\]\|classification:' server.log

# Billing
grep '\[billing\]' server.log

# Secrets
grep '\[secrets\]' server.log

# Health checks
grep '\[health\]' server.log

# API errors
grep '\[api\]\|\[PANIC\]' server.log

# SSE stream issues
grep '\[sse\]' server.log
```
