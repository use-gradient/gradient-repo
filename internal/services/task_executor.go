package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gradient/gradient/internal/db"
	"github.com/gradient/gradient/internal/models"
	"github.com/gradient/gradient/pkg/env"
	"github.com/gradient/gradient/pkg/livectx"
)

// TaskExecutorService provisions environments, clones repos, invokes Claude Code
// headless, creates PRs, and reports results — the full task execution pipeline.
type TaskExecutorService struct {
	db             *db.DB
	envService     *EnvService
	billingService *BillingService
	claudeService  *ClaudeService
	taskService    *TaskService
	githubService  *GitHubService
	linearService  *LinearService
	contextService *ContextService
	sessionService *SessionService
	memoryService  *TrajectoryMemoryService
	meshPublisher  *livectx.MeshPublisher
	snapshotStore  *SnapshotStore

	mu            sync.Mutex
	activeTasks   map[string]context.CancelFunc
	maxConcurrent int
}

var gradientMCPAllowedTools = []string{
	"mcp__gradient-context__get_context_updates",
	"mcp__gradient-context__get_memory_guidance",
	"mcp__gradient-context__publish_event",
	"mcp__gradient-context__mark_subtask",
}

func NewTaskExecutorService(
	database *db.DB,
	envService *EnvService,
	billingService *BillingService,
	claudeService *ClaudeService,
	taskService *TaskService,
	githubService *GitHubService,
	linearService *LinearService,
	contextService *ContextService,
	sessionService *SessionService,
	memoryService *TrajectoryMemoryService,
	meshPublisher *livectx.MeshPublisher,
	snapshotStore *SnapshotStore,
) *TaskExecutorService {
	return &TaskExecutorService{
		db:             database,
		envService:     envService,
		billingService: billingService,
		claudeService:  claudeService,
		taskService:    taskService,
		githubService:  githubService,
		linearService:  linearService,
		contextService: contextService,
		sessionService: sessionService,
		memoryService:  memoryService,
		meshPublisher:  meshPublisher,
		snapshotStore:  snapshotStore,
		activeTasks:    make(map[string]context.CancelFunc),
		maxConcurrent:  5,
	}
}

// ExecuteTask runs the full pipeline: provision env -> clone repo -> Claude Code -> PR.
func (e *TaskExecutorService) ExecuteTask(parentCtx context.Context, orgID, taskID string) {
	e.mu.Lock()
	if len(e.activeTasks) >= e.maxConcurrent {
		e.mu.Unlock()
		e.taskService.addLog(parentCtx, taskID, "throttled", "failed",
			fmt.Sprintf("Max concurrent tasks (%d) reached", e.maxConcurrent), nil)
		return
	}
	ctx, cancel := context.WithTimeout(parentCtx, 60*time.Minute)
	e.activeTasks[taskID] = cancel
	e.mu.Unlock()

	defer func() {
		cancel()
		e.mu.Lock()
		delete(e.activeTasks, taskID)
		e.mu.Unlock()
	}()

	task, err := e.taskService.GetTask(ctx, orgID, taskID)
	if err != nil || task == nil {
		log.Printf("[executor] task %s not found: %v", taskID, err)
		return
	}

	settings, _ := e.taskService.GetSettings(ctx, orgID)
	claudeCfg, err := e.claudeService.GetConfig(ctx, orgID, "")
	if err != nil || claudeCfg == nil {
		e.failTask(ctx, orgID, taskID, "Claude Code not configured")
		return
	}

	baseBranch := task.Branch
	if baseBranch == "" {
		baseBranch = settings.PRBaseBranch
		if baseBranch == "" {
			baseBranch = "main"
		}
	}
	envSize := strings.TrimSpace(settings.DefaultEnvSize)
	if envSize == "" {
		envSize = "small"
	}

	var (
		session         *models.AgentSession
		initialSHA      string
		retrievedTips   []RetrievedTip
		guidanceSection string
		envStateSection string
	)

	recordBundle := func(step, status, summary string, contextDiff, decisionDiff map[string]interface{}, testResults []models.TestResult) {
		if e.sessionService == nil || session == nil {
			return
		}
		if decisionDiff == nil {
			decisionDiff = map[string]interface{}{}
		}
		if contextDiff == nil {
			contextDiff = map[string]interface{}{}
		}
		decisionDiff["event"] = step
		if summary != "" && decisionDiff["summary"] == nil {
			decisionDiff["summary"] = summary
		}
		bundle := &models.ChangeBundle{
			SessionID:    session.ID,
			ContextDiff:  contextDiff,
			DecisionDiff: decisionDiff,
			TestResults:  testResults,
			Status:       status,
		}
		if _, err := e.sessionService.CreateBundle(ctx, bundle); err != nil {
			log.Printf("[executor] failed to create change bundle for %s: %v", step, err)
		}
	}

	finalizeSession := func(status string) {
		if e.sessionService == nil || session == nil {
			return
		}
		if err := e.sessionService.CloseSession(ctx, session.ID, status); err != nil {
			log.Printf("[executor] failed to close session %s: %v", session.ID, err)
		}
	}

	runMemoryPipeline := func() {
		if e.memoryService == nil || task.RepoFullName == "" {
			return
		}
		if _, err := e.memoryService.analysisService.AnalyzeTask(ctx, orgID, taskID); err != nil {
			log.Printf("[executor] failed to analyze trajectory for task %s: %v", taskID, err)
		}
		generatedTips, genErr := e.memoryService.GenerateTipsForTask(ctx, orgID, taskID)
		if genErr != nil {
			log.Printf("[executor] failed to generate trajectory memory for task %s: %v", taskID, genErr)
			return
		}
		if syncErr := e.memoryService.SyncEmbeddingsForTips(ctx, generatedTips); syncErr != nil {
			log.Printf("[executor] failed to sync embeddings for task %s: %v", taskID, syncErr)
		}
		if len(generatedTips) > 0 {
			e.taskService.addLog(ctx, taskID, "trajectory_memory_generated", "completed",
				fmt.Sprintf("Generated %d durable guidance tip(s)", len(generatedTips)), map[string]interface{}{
					"tip_count": len(generatedTips),
				})
		}
	}

	failExecution := func(message string) {
		recordBundle("task_failed", "failed", message, map[string]interface{}{
			"outcome": "failed",
		}, map[string]interface{}{
			"outcome":           "failed",
			"failure_signature": normalizedKey(message),
		}, nil)
		finalizeSession("failed")
		e.failTask(ctx, orgID, taskID, message)
		runMemoryPipeline()
	}

	// ── Step 1: Provision or reuse environment ──
	e.taskService.addLog(ctx, taskID, "provisioning_env", "started", "Looking for environment...", nil)

	var newEnv *models.Environment
	reusedEnv := false

	if task.RepoFullName != "" {
		existing, _ := e.envService.GetByOrgRepoAndBranch(ctx, orgID, task.RepoFullName, baseBranch)
		if existing != nil && existing.Status == "sleeping" {
			if existing.Size != "" {
				envSize = existing.Size
			}
			if e.billingService != nil {
				if billingErr := e.billingService.CheckBillingAllowed(ctx, orgID, envSize); billingErr != nil {
					failExecution(fmt.Sprintf("Billing blocked task execution: %v", billingErr))
					return
				}
			}
			e.taskService.addLog(ctx, taskID, "env_waking", "started",
				fmt.Sprintf("Waking sleeping environment %s", existing.ID), nil)
			if wakeErr := e.envService.WakeEnvironment(ctx, existing.ID, orgID); wakeErr == nil {
				newEnv = existing
				reusedEnv = true
				if e.billingService != nil {
					if trackErr := e.billingService.TrackUsageStart(ctx, existing.ID, orgID, envSize); trackErr != nil {
						log.Printf("[billing] failed to track usage start for task env wake %s: %v", existing.ID, trackErr)
					}
				}
			} else {
				log.Printf("[executor] Failed to wake env %s: %v, creating new", existing.ID, wakeErr)
			}
		}
	}

	if newEnv == nil {
		if e.billingService != nil {
			if billingErr := e.billingService.CheckBillingAllowed(ctx, orgID, envSize); billingErr != nil {
				failExecution(fmt.Sprintf("Billing blocked task execution: %v", billingErr))
				return
			}
		}
		var snapshotRef string
		if snap, snapErr := e.snapshotStore.GetLatestByBranch(ctx, orgID, baseBranch); snapErr == nil && snap != nil {
			snapshotRef = snap.ImageRef
		}

		var createErr error
		newEnv, createErr = e.envService.CreateEnvironment(ctx, &CreateEnvRequest{
			Name:          fmt.Sprintf("task-%s", taskID[:8]),
			OrgID:         orgID,
			Provider:      settings.DefaultEnvProvider,
			Region:        settings.DefaultEnvRegion,
			Size:          settings.DefaultEnvSize,
			ContextBranch: baseBranch,
			SnapshotRef:   snapshotRef,
			RepoFullName:  task.RepoFullName,
		})
		if createErr != nil {
			failExecution(fmt.Sprintf("Failed to provision environment: %v", createErr))
			return
		}
		if newEnv.Size != "" {
			envSize = newEnv.Size
		}
		if e.billingService != nil {
			if trackErr := e.billingService.TrackUsageStart(ctx, newEnv.ID, orgID, envSize); trackErr != nil {
				log.Printf("[billing] failed to track usage start for task env create %s: %v", newEnv.ID, trackErr)
			}
		}

		e.taskService.addLog(ctx, taskID, "env_provisioned", "completed",
			fmt.Sprintf("Environment %s created (%s/%s)", newEnv.ID, settings.DefaultEnvProvider, envSize), nil)
	}

	e.db.Pool.Exec(ctx, `UPDATE agent_tasks SET environment_id = $1, branch = $2, updated_at = NOW() WHERE id = $3`,
		newEnv.ID, baseBranch, taskID)

	if !reusedEnv || newEnv.Status != "running" {
		if !e.waitForEnvReady(ctx, newEnv.ID, 5*time.Minute) {
			failExecution("Environment failed to become ready within 5 minutes")
			if !reusedEnv {
				e.cleanupEnv(orgID, newEnv.ID)
			}
			return
		}
	}

	runningEnv, _ := e.envService.GetEnvironment(ctx, newEnv.ID)
	if runningEnv == nil || runningEnv.ClusterName == "" {
		failExecution("Environment has no provider reference")
		e.cleanupEnv(orgID, newEnv.ID)
		return
	}

	provider, err := e.envService.GetProviderByName(runningEnv.Provider)
	if err != nil {
		failExecution(fmt.Sprintf("Provider unavailable: %v", err))
		e.cleanupEnv(orgID, newEnv.ID)
		return
	}
	executor, ok := env.AsRemoteExecutor(provider)
	if !ok {
		failExecution("Provider does not support remote execution")
		e.cleanupEnv(orgID, newEnv.ID)
		return
	}

	// Wait for SSM agent, then for cloud-init to finish (Docker install, container start)
	e.taskService.addLog(ctx, taskID, "waiting_for_ssm", "started", "Waiting for instance to accept commands...", nil)
	if err := executor.WaitForReady(ctx, runningEnv.ClusterName, 5*time.Minute); err != nil {
		failExecution(fmt.Sprintf("Instance never became SSM-ready: %v", err))
		e.cleanupEnv(orgID, newEnv.ID)
		return
	}

	e.taskService.addLog(ctx, taskID, "waiting_for_docker", "started", "Waiting for Docker and container to be ready...", nil)
	dockerDeadline := time.Now().Add(5 * time.Minute)
	dockerReady := false
	for time.Now().Before(dockerDeadline) {
		output, err := executor.ExecCommand(ctx, runningEnv.ClusterName,
			`test -f /tmp/gradient-status && cat /tmp/gradient-status || echo "waiting"`, 15*time.Second)
		if err == nil && strings.TrimSpace(output) == "ready" {
			dockerReady = true
			break
		}
		log.Printf("[executor] Waiting for cloud-init on %s (output: %s)...", runningEnv.ClusterName, strings.TrimSpace(output))
		time.Sleep(10 * time.Second)
	}
	if !dockerReady {
		failExecution("Cloud-init did not complete within 5 minutes (Docker not installed)")
		e.cleanupEnv(orgID, newEnv.ID)
		return
	}
	e.taskService.addLog(ctx, taskID, "waiting_for_docker", "completed", "Instance fully ready", nil)

	// ── Step 2: Clone repo and create task branch ──
	taskBranch := fmt.Sprintf("task/%s", taskID[:8])
	repoCloned := false

	if task.RepoFullName != "" {
		ghConn, ghErr := e.githubService.GetConnection(ctx, orgID)
		if ghErr == nil && ghConn != nil {
			e.taskService.addLog(ctx, taskID, "cloning_repo", "started",
				fmt.Sprintf("Cloning %s", task.RepoFullName), nil)

			repoURL := fmt.Sprintf("https://x-access-token:%s@github.com/%s.git", ghConn.AccessToken, task.RepoFullName)
			var cloneScript string
			if reusedEnv {
				cloneScript = fmt.Sprintf(
					`set -e; cd /workspace; if [ -d repo/.git ]; then cd repo; git fetch origin 2>&1; git checkout -b %s origin/%s 2>/dev/null || git checkout %s 2>/dev/null || git checkout -b %s 2>/dev/null; else git clone --depth=50 %s repo 2>&1; cd repo; fi; git config user.email "agent@gradient.dev"; git config user.name "Gradient Agent"; echo "CLONE_OK"`,
					taskBranch, baseBranch, taskBranch, taskBranch, repoURL)
			} else {
				cloneScript = fmt.Sprintf(
					`set -e; cd /workspace; git clone --depth=50 %s repo 2>&1; cd repo; git config user.email "agent@gradient.dev"; git config user.name "Gradient Agent"; git checkout -b %s 2>/dev/null || git checkout %s; echo "CLONE_OK"`,
					repoURL, taskBranch, taskBranch)
			}

			output, cloneErr := e.execInContainer(ctx, executor, runningEnv.ClusterName, cloneScript, 5*time.Minute)
			if cloneErr != nil || !strings.Contains(output, "CLONE_OK") {
				failExecution(fmt.Sprintf("Repo clone failed: %v (%s)", cloneErr, truncate(output, 500)))
				e.cleanupEnv(orgID, newEnv.ID)
				return
			}
			repoCloned = true
			e.taskService.addLog(ctx, taskID, "repo_cloned", "completed", "Repo cloned, branch created", nil)

			if e.meshPublisher != nil {
				evt, _ := livectx.NewEvent(livectx.EventCustom, orgID, baseBranch, newEnv.ID, map[string]interface{}{
					"key":   "repo_cloned",
					"value": map[string]interface{}{"repo": task.RepoFullName, "branch": taskBranch},
				})
				if evt != nil {
					evt.RepoFullName = task.RepoFullName
					e.meshPublisher.Publish(ctx, evt.WithSource("executor"))
				}
			}
		}
	}

	if repoCloned {
		shaOut, _ := e.execInContainer(ctx, executor, runningEnv.ClusterName,
			"cd /workspace/repo && git rev-parse HEAD 2>/dev/null", 15*time.Second)
		initialSHA = strings.TrimSpace(shaOut)
	}

	if e.sessionService != nil {
		sessionModel, sessionErr := e.sessionService.CreateSession(ctx, &models.AgentSession{
			TaskID:        task.ID,
			OrgID:         orgID,
			AgentRole:     "coder",
			Scope:         models.SessionScope{},
			InitialSHA:    initialSHA,
			BranchName:    baseBranch,
			EnvironmentID: newEnv.ID,
			Status:        "active",
		})
		if sessionErr != nil {
			log.Printf("[executor] failed to create agent session for task %s: %v", taskID, sessionErr)
		} else {
			session = sessionModel
			e.taskService.addLog(ctx, taskID, "session_started", "completed", fmt.Sprintf("Trajectory session %s created", session.ID[:8]), map[string]interface{}{
				"session_id": session.ID,
			})
		}
	}

	// ── Step 3: Load environment state, seed memory, and build prompt ──
	if e.contextService != nil && task.RepoFullName != "" {
		if ctxObj, err := e.contextService.GetRepoContext(ctx, orgID, task.RepoFullName, baseBranch); err == nil && ctxObj != nil {
			envStateSection = e.formatEnvironmentStateForPrompt(ctxObj)
			if e.memoryService != nil {
				if seedErr := e.memoryService.SeedTipsFromContext(ctx, task.RepoFullName, baseBranch, ctxObj); seedErr != nil {
					log.Printf("[executor] failed to seed memory from context: %v", seedErr)
				}
			}
		}
	}

	if e.memoryService != nil && task.RepoFullName != "" {
		guidance, guidanceErr := e.memoryService.RetrieveGuidance(ctx, MemoryRetrieveRequest{
			OrgID:           orgID,
			RepoFullName:    task.RepoFullName,
			Branch:          baseBranch,
			TaskID:          task.ID,
			SessionID:       sessionIDValue(session),
			TaskTitle:       task.Title,
			TaskDescription: task.Description,
			TaskPrompt:      task.Prompt,
			Limit:           7,
		})
		if guidanceErr != nil {
			log.Printf("[executor] failed to retrieve guidance: %v", guidanceErr)
		} else {
			retrievedTips = guidance
			guidanceSection = e.memoryService.FormatGuidanceForPrompt(guidance)
		}
	}

	taskPrompt := e.buildTaskPrompt(task, settings, claudeCfg.EnableTeams, guidanceSection, envStateSection)

	e.taskService.addLog(ctx, taskID, "task_prompt_built", "completed", truncate(taskPrompt, 4000), map[string]interface{}{
		"prompt_length":   len(taskPrompt),
		"has_guidance":    guidanceSection != "",
		"guidance_count":  len(retrievedTips),
		"has_env_state":   envStateSection != "",
		"has_description": task.Description != "",
		"has_parent_task": task.ParentTaskID != "",
		"repo":            task.RepoFullName,
		"base_branch":     baseBranch,
	})
	recordBundle("task_prompt_built", "completed", "Prompt prepared with retrieved guidance and environment state", map[string]interface{}{
		"prompt_length":   len(taskPrompt),
		"guidance_count":  len(retrievedTips),
		"has_env_state":   envStateSection != "",
		"initial_sha":     initialSHA,
		"retrieval_scope": task.RepoFullName,
	}, map[string]interface{}{
		"outcome": "completed",
		"subtask": "prompt_preparation",
	}, nil)

	writePromptScript := fmt.Sprintf(`mkdir -p /gradient; cat > /gradient/task-prompt.md << 'GRADIENT_EOF'
%s
GRADIENT_EOF
echo "PROMPT_OK"`, taskPrompt)
	e.execInContainer(ctx, executor, runningEnv.ClusterName, writePromptScript, 30*time.Second)

	guidanceSnapshot := map[string]interface{}{
		"retrieved_at": time.Now().UTC().Format(time.RFC3339),
		"tips":         []map[string]interface{}{},
	}
	if e.memoryService != nil {
		guidanceSnapshot = e.memoryService.BuildGuidanceSnapshot(retrievedTips)
	}
	guidanceJSON, _ := json.MarshalIndent(guidanceSnapshot, "", "  ")

	sessionSnapshot := map[string]interface{}{
		"task_id":        task.ID,
		"repo_full_name": task.RepoFullName,
		"branch":         baseBranch,
	}
	if session != nil {
		sessionSnapshot["session_id"] = session.ID
	}
	sessionJSON, _ := json.MarshalIndent(sessionSnapshot, "", "  ")

	writeTrajectoryFilesScript := fmt.Sprintf(`mkdir -p /gradient/context; cat > /gradient/context/memory.json << 'GRADIENT_MEMORY'
%s
GRADIENT_MEMORY
cat > /gradient/context/session.json << 'GRADIENT_SESSION'
%s
GRADIENT_SESSION
echo "TRAJECTORY_FILES_OK"`, string(guidanceJSON), string(sessionJSON))
	e.execInContainer(ctx, executor, runningEnv.ClusterName, writeTrajectoryFilesScript, 30*time.Second)

	// ── Step 3.5: Set up MCP context server for cross-agent communication ──
	mcpSetupScript := `mkdir -p /gradient/context && cat > /gradient/mcp-context.js << 'MCPEOF'
const fs = require('fs');
const readline = require('readline');
const LIVE = '/gradient/context/live.json';
const OUTBOX = '/gradient/context/outbox.jsonl';
const MEMORY = '/gradient/context/memory.json';
const SESSION = '/gradient/context/session.json';
const CURSOR = '/gradient/context/read_cursor.json';
const FLAG = '/gradient/context/has_updates';

// Watch live.json for changes and set the update flag
try {
  fs.watchFile(LIVE, { interval: 2000 }, () => {
    try { fs.writeFileSync(FLAG, Date.now().toString()); } catch(_) {}
  });
} catch(_) {}

const rl = readline.createInterface({ input: process.stdin });
rl.on('line', line => {
  try {
    const req = JSON.parse(line);
    const resp = handle(req);
    if (resp) process.stdout.write(JSON.stringify(resp) + '\n');
  } catch(e) {
    process.stdout.write(JSON.stringify({jsonrpc:"2.0",error:{code:-32700,message:"parse error"}})+'\n');
  }
});

function handle(req) {
  if (req.method === 'initialize') return {jsonrpc:"2.0",id:req.id,result:{protocolVersion:"2024-11-05",capabilities:{tools:{}},serverInfo:{name:"gradient-context",version:"0.2.0"}}};
  if (req.method === 'notifications/initialized') return null;
  if (req.method === 'tools/list') return {jsonrpc:"2.0",id:req.id,result:{tools:[
    {name:"get_context_updates",description:"Return unread operational deltas from the live context mesh, including peer package changes, config updates, contract changes, and urgent issues.",inputSchema:{type:"object",properties:{}}},
    {name:"get_memory_guidance",description:"Inspect the retrieved durable guidance for this task or a specific subtask before making changes.",inputSchema:{type:"object",properties:{subtask:{type:"string",description:"Optional subtask name"},failure_signature:{type:"string",description:"Optional failure signature to filter recovery tips"},goal:{type:"string",description:"Optional current goal or intent for finer filtering"}},required:[]}},
    {name:"publish_event",description:"Share a structured discovery with other agents. Use for decisions, errors, package installs, config changes, contract updates, or custom telemetry.",inputSchema:{type:"object",properties:{event_type:{type:"string",description:"error_encountered, decision_made, package_installed, config_changed, contract_updated, custom"},message:{type:"string",description:"Short summary"},subtask:{type:"string",description:"Related subtask name"},outcome:{type:"string",description:"completed, failed, blocked, success"},failure_signature:{type:"string",description:"Stable failure signature"},related_files:{type:"array",items:{type:"string"},description:"Related files"},data:{type:"object",description:"Additional structured data"}},required:["event_type"]}},
    {name:"mark_subtask",description:"Record a subtask boundary and its outcome so the trajectory memory pipeline can learn from it.",inputSchema:{type:"object",properties:{name:{type:"string",description:"Subtask name"},outcome:{type:"string",description:"started, completed, failed, blocked"},summary:{type:"string",description:"Short result summary"},failure_signature:{type:"string",description:"Stable failure signature"},related_files:{type:"array",items:{type:"string"},description:"Files touched or relevant"}},required:["name","outcome"]}}
  ]}};
  if (req.method === 'tools/call') return toolCall(req);
  return {jsonrpc:"2.0",id:req.id,error:{code:-32601,message:"method not found: "+req.method}};
}

function toolCall(req) {
  const p = JSON.parse(JSON.stringify(req.params || {}));
  const name = p.name;
  const args = p.arguments || {};
  if (name === 'get_context_updates') return getUpdates(req.id);
  if (name === 'get_memory_guidance') return getMemoryGuidance(req.id, args);
  if (name === 'publish_event') return publish(req.id, args);
  if (name === 'mark_subtask') return markSubtask(req.id, args);
  return {jsonrpc:"2.0",id:req.id,error:{code:-32602,message:"unknown tool: "+name}};
}

function readJSON(path, fallback) {
  try { return JSON.parse(fs.readFileSync(path, 'utf8')); } catch(_) { return fallback; }
}

function writeJSON(path, value) {
  try { fs.writeFileSync(path, JSON.stringify(value, null, 2)); } catch(_) {}
}

function currentSession() {
  return readJSON(SESSION, {});
}

function getUpdates(id) {
  const live = readJSON(LIVE, {packages:{}, configs:{}, contracts:{}, urgent_issues:[], recent_updates:[], last_update:''});
  const cursor = readJSON(CURSOR, {last_seq: 0});
  const updates = Array.isArray(live.recent_updates) ? live.recent_updates.filter(u => Number(u.seq || 0) > Number(cursor.last_seq || 0)) : [];
  const maxSeq = updates.reduce((max, item) => Math.max(max, Number(item.seq || 0)), Number(cursor.last_seq || 0));
  writeJSON(CURSOR, {last_seq: maxSeq, read_at: new Date().toISOString()});
  try { fs.unlinkSync(FLAG); } catch(_) {}

  if (!updates.length && !(live.urgent_issues || []).length) {
    return tr(id, 'No new operational updates from peers.');
  }

  let out = '## Operational Updates\n\n';
  if (live.last_update) out += 'Last update: ' + live.last_update + '\n\n';
  if (Object.keys(live.packages || {}).length) {
    out += '### Shared Packages\n';
    for (const [n,v] of Object.entries(live.packages)) out += '- ' + n + ': ' + v + '\n';
    out += '\n';
  }
  if (Object.keys(live.configs || {}).length) {
    out += '### Shared Config\n';
    for (const [k,v] of Object.entries(live.configs)) out += '- ' + k + ' = ' + v + '\n';
    out += '\n';
  }
  if (Object.keys(live.contracts || {}).length) {
    out += '### Contract Changes\n';
    for (const [k,v] of Object.entries(live.contracts)) out += '- ' + k + ': ' + v + '\n';
    out += '\n';
  }
  const urgent = Array.isArray(live.urgent_issues) ? live.urgent_issues.slice(-5) : [];
  if (urgent.length) {
    out += '### Urgent Peer Issues\n';
    urgent.forEach(item => out += '- [' + (item.type || 'update') + '] ' + (item.summary || JSON.stringify(item)) + '\n');
    out += '\n';
  }
  if (updates.length) {
    out += '### New Unread Updates\n';
    updates.forEach(item => out += '- [' + (item.type || 'update') + '] ' + (item.summary || JSON.stringify(item)) + '\n');
  }
  return tr(id, out.trim());
}

function publish(id, args) {
  if (!args.event_type) return er(id, 'event_type is required');
  const session = currentSession();
  const payload = Object.assign({}, args.data || {});
  if (session.task_id && payload.task_id === undefined) payload.task_id = session.task_id;
  if (session.session_id && payload.session_id === undefined) payload.session_id = session.session_id;
  if (args.subtask) payload.subtask = args.subtask;
  if (args.outcome) payload.outcome = args.outcome;
  if (args.failure_signature) payload.failure_signature = args.failure_signature;
  if (Array.isArray(args.related_files)) payload.related_files = args.related_files;
  const entry = {
    type: args.event_type,
    message: args.message || args.summary || args.subtask || 'Structured event',
    timestamp: new Date().toISOString(),
    data: payload
  };
  try {
    fs.mkdirSync('/gradient/context', {recursive:true});
    fs.appendFileSync(OUTBOX, JSON.stringify(entry) + '\n');
    return tr(id, 'Event published: [' + args.event_type + '] ' + entry.message);
  } catch(e) { return er(id, 'Write failed: ' + e.message); }
}

function getMemoryGuidance(id, args) {
  const memory = readJSON(MEMORY, {tips: []});
  let tips = Array.isArray(memory.tips) ? memory.tips.slice() : [];
  const subtask = String(args.subtask || '').toLowerCase();
  const failure = String(args.failure_signature || '').toLowerCase();
  const goal = String(args.goal || '').toLowerCase();
  if (failure) {
    const exact = tips.filter(t => String(t.failure_signature || '').toLowerCase() === failure);
    if (exact.length) tips = exact;
  }
  if (subtask) {
    const filtered = tips.filter(t => [t.title, t.content, t.trigger_condition].some(v => String(v || '').toLowerCase().includes(subtask)));
    if (filtered.length) tips = filtered;
  }
  if (goal) {
    const filtered = tips.filter(t => [t.title, t.content, t.trigger_condition].some(v => String(v || '').toLowerCase().includes(goal)));
    if (filtered.length) tips = filtered;
  }
  if (!tips.length) return tr(id, 'No retrieved memory guidance matched this request.');
  let out = '## Retrieved Memory Guidance\n\n';
  tips.slice(0, 5).forEach(tip => {
    out += '[' + String(tip.priority || 'medium').toUpperCase() + '][' + String(tip.type || 'strategy').toUpperCase() + '] ' + String(tip.title || 'Guidance') + '\n';
    out += '- Guidance: ' + String(tip.content || '') + '\n';
    if (tip.reason) out += '- Why selected: ' + String(tip.reason) + '\n';
    if (tip.trigger_condition) out += '- Apply when: ' + String(tip.trigger_condition) + '\n';
    if (Array.isArray(tip.action_steps)) tip.action_steps.forEach((step, idx) => out += '  ' + (idx + 1) + '. ' + step + '\n');
    out += '\n';
  });
  return tr(id, out.trim());
}

function markSubtask(id, args) {
  if (!args.name || !args.outcome) return er(id, 'name and outcome are required');
  const session = currentSession();
  const entry = {
    type: 'subtask_marked',
    message: args.name + ': ' + args.outcome,
    timestamp: new Date().toISOString(),
    data: {
      task_id: session.task_id,
      session_id: session.session_id,
      subtask: args.name,
      outcome: args.outcome,
      summary: args.summary || '',
      failure_signature: args.failure_signature || '',
      related_files: Array.isArray(args.related_files) ? args.related_files : []
    }
  };
  try {
    fs.mkdirSync('/gradient/context', {recursive:true});
    fs.appendFileSync(OUTBOX, JSON.stringify(entry) + '\n');
    return tr(id, 'Subtask recorded: ' + args.name + ' (' + args.outcome + ')');
  } catch(e) {
    return er(id, 'Write failed: ' + e.message);
  }
}

function tr(id, text) { return {jsonrpc:"2.0",id,result:{content:[{type:"text",text}]}}; }
function er(id, msg) { return {jsonrpc:"2.0",id,result:{content:[{type:"text",text:"Error: "+msg}],isError:true}}; }
MCPEOF

cat > /gradient/mcp-config.json << 'CFGEOF'
{"mcpServers":{"gradient-context":{"command":"node","args":["/gradient/mcp-context.js"]}}}
CFGEOF
echo "MCP_SETUP_OK"`

	mcpOut, mcpErr := e.execInContainer(ctx, executor, runningEnv.ClusterName, mcpSetupScript, 30*time.Second)
	mcpReady := mcpErr == nil && strings.Contains(mcpOut, "MCP_SETUP_OK")
	if mcpReady {
		e.taskService.addLog(ctx, taskID, "mcp_setup", "completed", "MCP context server configured", map[string]interface{}{
			"config":           `{"mcpServers":{"gradient-context":{"command":"node","args":["/gradient/mcp-context.js"]}}}`,
			"tools_available":  []string{"get_context_updates", "get_memory_guidance", "publish_event", "mark_subtask"},
			"live_json_path":   "/gradient/context/live.json",
			"outbox_path":      "/gradient/context/outbox.jsonl",
			"memory_json_path": "/gradient/context/memory.json",
			"session_path":     "/gradient/context/session.json",
		})
	} else {
		log.Printf("[executor] MCP setup warning: %v (%s)", mcpErr, truncate(mcpOut, 200))
		e.taskService.addLog(ctx, taskID, "mcp_setup", "failed", "MCP context server setup failed (continuing without)", nil)
	}

	// ── Step 4: Run Claude Code headless ──
	e.taskService.addLog(ctx, taskID, "claude_executing", "started", "Running Claude Code...", nil)

	if e.meshPublisher != nil {
		evt, _ := livectx.NewEvent(livectx.EventCustom, orgID, baseBranch, newEnv.ID, map[string]interface{}{
			"key":   "task_started",
			"value": map[string]interface{}{"task_id": taskID, "title": task.Title, "env_id": newEnv.ID},
		})
		if evt != nil {
			evt.RepoFullName = task.RepoFullName
			e.meshPublisher.Publish(ctx, evt.WithSource("executor"))
		}
	}

	// Verify claude CLI is available
	claudeCheck, _ := e.execInContainer(ctx, executor, runningEnv.ClusterName,
		"which claude 2>/dev/null || (command -v claude 2>/dev/null) || echo CLAUDE_NOT_FOUND", 15*time.Second)
	claudeCheckTrimmed := strings.TrimSpace(claudeCheck)
	if claudeCheckTrimmed == "CLAUDE_NOT_FOUND" || claudeCheckTrimmed == "" {
		log.Printf("[executor] Claude CLI not found in container, attempting install...")
		installOut, installErr := e.execInContainer(ctx, executor, runningEnv.ClusterName,
			`curl -fsSL https://nodejs.org/dist/v20.11.1/node-v20.11.1-linux-x64.tar.xz | tar -xJ -C /usr/local --strip-components=1 && npm install -g @anthropic-ai/claude-code 2>&1 && echo INSTALL_OK`,
			5*time.Minute)
		if installErr != nil || !strings.Contains(installOut, "INSTALL_OK") {
			log.Printf("[executor] Claude CLI install failed: %v (%s)", installErr, truncate(installOut, 500))
			failExecution(fmt.Sprintf("Claude CLI install failed: %s", truncate(installOut, 300)))
			e.cleanupEnv(orgID, newEnv.ID)
			return
		}
		log.Printf("[executor] Claude CLI installed successfully")
	} else {
		log.Printf("[executor] Claude CLI found at: %s", claudeCheckTrimmed)
	}

	claudeScript := e.buildClaudeScript(claudeCfg, repoCloned, mcpReady)

	workDir := "/workspace"
	if repoCloned {
		workDir = "/workspace/repo"
	}
	turnsPerIter := claudeCfg.MaxTurns
	if turnsPerIter < 1 {
		turnsPerIter = 1
	}
	mcpFlagStr := ""
	if mcpReady {
		mcpFlagStr = "--mcp-config /gradient/mcp-config.json "
	}
	effectiveAllowedTools := allowedToolsForExecution(claudeCfg, mcpReady)
	teamsFlag := ""
	if claudeCfg.EnableTeams {
		teamsFlag = "CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1 "
	}
	redactedCmd := fmt.Sprintf(
		`%sexport ANTHROPIC_API_KEY="sk-ant-***REDACTED***" && cd %s && claude -p "$(cat /gradient/task-prompt.md)" --output-format text --model %s --max-turns %d --allowedTools "%s" %s--verbose`,
		teamsFlag, workDir, claudeCfg.Model, turnsPerIter, strings.Join(effectiveAllowedTools, ","), mcpFlagStr)
	e.taskService.addLog(ctx, taskID, "claude_invocation", "started", redactedCmd, map[string]interface{}{
		"model":               claudeCfg.Model,
		"max_turns_total":     claudeCfg.MaxTurns,
		"turns_per_iteration": turnsPerIter,
		"max_iterations":      1,
		"allowed_tools":       effectiveAllowedTools,
		"mcp_enabled":         mcpReady,
		"agent_teams_enabled": claudeCfg.EnableTeams,
		"work_dir":            workDir,
		"repo_cloned":         repoCloned,
	})

	log.Printf("[executor] Running Claude Code for task %s...", taskID[:8])
	claudeOutput, claudeErr := e.execInContainer(ctx, executor, runningEnv.ClusterName, claudeScript, 45*time.Minute)
	if claudeErr != nil {
		log.Printf("[executor] Claude Code returned error (may still have results): %v", claudeErr)
	}
	log.Printf("[executor] Claude Code output for task %s: %s", taskID[:8], truncate(claudeOutput, 1000))

	e.taskService.addLog(ctx, taskID, "claude_output", "completed", truncate(claudeOutput, 10000), map[string]interface{}{
		"output_bytes": len(claudeOutput),
		"truncated":    len(claudeOutput) > 10000,
		"had_error":    claudeErr != nil,
	})
	if fatalClaudeError := detectClaudeFatalError(claudeOutput, claudeErr); fatalClaudeError != "" {
		e.taskService.addLog(ctx, taskID, "claude_failed", "failed", fatalClaudeError, map[string]interface{}{
			"output_bytes": len(claudeOutput),
			"had_error":    claudeErr != nil,
		})
		recordBundle("claude_failed", "failed", fatalClaudeError, map[string]interface{}{
			"output_bytes": len(claudeOutput),
			"had_error":    claudeErr != nil,
			"mcp_enabled":  mcpReady,
		}, map[string]interface{}{
			"outcome":           "failed",
			"subtask":           "claude_execution",
			"summary":           fatalClaudeError,
			"failure_signature": normalizedKey(fatalClaudeError),
		}, nil)
		failExecution(fatalClaudeError)
		if task.RepoFullName != "" {
			if err := e.envService.SleepEnvironment(ctx, newEnv.ID, orgID); err != nil {
				log.Printf("[executor] Failed to sleep env %s after task failure: %v", newEnv.ID, err)
			} else if e.billingService != nil {
				if trackErr := e.billingService.TrackUsageStop(ctx, newEnv.ID); trackErr != nil {
					log.Printf("[billing] failed to track usage stop after task failure for env %s: %v", newEnv.ID, trackErr)
				}
			}
		} else if settings.AutoDestroyEnv {
			e.cleanupEnv(orgID, newEnv.ID)
		}
		return
	}
	e.taskService.addLog(ctx, taskID, "claude_done", "completed",
		fmt.Sprintf("Claude Code finished (%d bytes output)", len(claudeOutput)), nil)
	recordBundle("claude_done", "completed", "Claude execution finished", map[string]interface{}{
		"output_bytes": len(claudeOutput),
		"had_error":    claudeErr != nil,
		"mcp_enabled":  mcpReady,
	}, map[string]interface{}{
		"outcome": "completed",
		"subtask": "claude_execution",
		"summary": truncate(e.extractSummary(claudeOutput), 500),
	}, nil)

	// ── Step 5: Get commit SHA and push ──
	var commitSHA, prURL string

	if repoCloned {
		shaOut, _ := e.execInContainer(ctx, executor, runningEnv.ClusterName,
			"cd /workspace/repo && git rev-parse HEAD 2>/dev/null", 15*time.Second)
		commitSHA = strings.TrimSpace(shaOut)

		// Check if Claude actually made any new commits on the task branch
		diffCheck, _ := e.execInContainer(ctx, executor, runningEnv.ClusterName,
			fmt.Sprintf(`cd /workspace/repo && git log --oneline origin/%s..HEAD 2>/dev/null | head -20`, baseBranch), 15*time.Second)
		diffCheckTrimmed := strings.TrimSpace(diffCheck)
		log.Printf("[executor] Git diff check for task %s: %s", taskID[:8], truncate(diffCheckTrimmed, 300))
		hasNewCommits := diffCheckTrimmed != "" && diffCheckTrimmed != "NO_NEW_COMMITS"

		pushSucceeded := false
		if hasNewCommits {
			pushScript := fmt.Sprintf("cd /workspace/repo && git push origin %s 2>&1 && echo 'PUSH_OK'", taskBranch)
			pushOut, pushErr := e.execInContainer(ctx, executor, runningEnv.ClusterName, pushScript, 2*time.Minute)
			if pushErr != nil || !strings.Contains(pushOut, "PUSH_OK") {
				log.Printf("[executor] git push failed for task %s: %v (%s)", taskID[:8], pushErr, truncate(pushOut, 300))
				e.taskService.addLog(ctx, taskID, "push_failed", "failed",
					fmt.Sprintf("git push failed: %s", truncate(pushOut, 500)), nil)
				recordBundle("push_failed", "failed", truncate(pushOut, 500), map[string]interface{}{
					"commit_sha": commitSHA,
				}, map[string]interface{}{
					"outcome":           "failed",
					"subtask":           "git_push",
					"failure_signature": normalizedKey(pushOut),
				}, nil)
			} else {
				pushSucceeded = true
				log.Printf("[executor] git push for task %s: %s", taskID[:8], truncate(pushOut, 300))
				recordBundle("push_completed", "completed", "Changes pushed to remote branch", map[string]interface{}{
					"commit_sha": commitSHA,
				}, map[string]interface{}{
					"outcome": "completed",
					"subtask": "git_push",
				}, nil)
			}
		} else {
			log.Printf("[executor] No new commits for task %s, skipping push", taskID[:8])
			e.taskService.addLog(ctx, taskID, "push_skipped", "completed",
				"No new commits were made by Claude, skipping push and PR", nil)
		}

		if settings.AutoCreatePR && task.RepoFullName != "" && pushSucceeded {
			e.taskService.addLog(ctx, taskID, "creating_pr", "started", "Creating pull request...", nil)
			prURL, err = e.createPR(ctx, orgID, task, taskBranch, baseBranch)
			if err != nil {
				log.Printf("[executor] PR creation failed: %v", err)
				e.taskService.addLog(ctx, taskID, "pr_failed", "failed", fmt.Sprintf("PR creation failed: %v", err), nil)
				recordBundle("pr_failed", "failed", err.Error(), nil, map[string]interface{}{
					"outcome":           "failed",
					"subtask":           "pull_request",
					"failure_signature": normalizedKey(err.Error()),
				}, nil)
			} else {
				e.taskService.addLog(ctx, taskID, "pr_created", "completed", fmt.Sprintf("PR: %s", prURL), nil)
				recordBundle("pr_created", "completed", "Pull request created", map[string]interface{}{
					"pr_url":     prURL,
					"commit_sha": commitSHA,
				}, map[string]interface{}{
					"outcome": "completed",
					"subtask": "pull_request",
				}, nil)

				if e.meshPublisher != nil {
					prEvt, _ := livectx.NewEvent(livectx.EventPRCreated, orgID, baseBranch, newEnv.ID, map[string]interface{}{
						"pr_url":  prURL,
						"head":    taskBranch,
						"base":    baseBranch,
						"task_id": taskID,
					})
					if prEvt != nil {
						prEvt.RepoFullName = task.RepoFullName
						e.meshPublisher.Publish(ctx, prEvt.WithSource("executor"))
					}
				}
			}
		} else if settings.AutoCreatePR && !pushSucceeded && hasNewCommits {
			e.taskService.addLog(ctx, taskID, "pr_skipped", "failed",
				"Skipping PR creation because git push failed", nil)
		}
	}

	// ── Step 6: Save context, snapshot, and complete ──
	contextSaved := false
	if e.contextService != nil && task.RepoFullName != "" {
		_, saveErr := e.contextService.SaveContext(ctx, &SaveContextRequest{
			Branch:       baseBranch,
			OrgID:        orgID,
			CommitSHA:    commitSHA,
			RepoFullName: task.RepoFullName,
		})
		if saveErr != nil {
			log.Printf("[executor] Failed to save context: %v", saveErr)
		} else {
			contextSaved = true
		}
	}

	snapshotTaken := false
	tag := fmt.Sprintf("task-%s-%d", taskID[:8], time.Now().Unix())
	if _, snapErr := e.envService.SnapshotEnvironment(ctx, newEnv.ID, orgID, tag); snapErr == nil {
		snapshotTaken = true
	}

	outputSummary := e.extractSummary(claudeOutput)
	outputJSON := map[string]interface{}{
		"claude_output_markdown":  truncate(claudeOutput, 50000),
		"claude_output_bytes":     len(claudeOutput),
		"claude_output_truncated": len(claudeOutput) > 50000,
	}

	e.taskService.CompleteTask(ctx, orgID, taskID, CompleteTaskRequest{
		OutputSummary: outputSummary,
		OutputJSON:    outputJSON,
		CommitSHA:     commitSHA,
		PRURL:         prURL,
		ContextSaved:  contextSaved,
		SnapshotTaken: snapshotTaken,
	})
	recordBundle("task_completed", "completed", outputSummary, map[string]interface{}{
		"commit_sha":     commitSHA,
		"context_saved":  contextSaved,
		"snapshot_taken": snapshotTaken,
		"guidance_count": len(retrievedTips),
	}, map[string]interface{}{
		"outcome": "completed",
		"subtask": "task",
		"summary": outputSummary,
	}, nil)
	finalizeSession("completed")

	if e.meshPublisher != nil {
		evt, _ := livectx.NewEvent(livectx.EventCustom, orgID, baseBranch, newEnv.ID, map[string]interface{}{
			"key": "task_completed",
			"value": map[string]interface{}{
				"task_id": taskID, "commit_sha": commitSHA, "pr_url": prURL, "summary": outputSummary,
			},
		})
		if evt != nil {
			evt.RepoFullName = task.RepoFullName
			e.meshPublisher.Publish(ctx, evt.WithSource("executor"))
		}
	}

	if task.RepoFullName != "" {
		if err := e.envService.SleepEnvironment(ctx, newEnv.ID, orgID); err != nil {
			log.Printf("[executor] Failed to sleep env %s after task: %v", newEnv.ID, err)
		} else {
			log.Printf("[executor] Environment %s put to sleep after task completion", newEnv.ID)
			if e.billingService != nil {
				if trackErr := e.billingService.TrackUsageStop(ctx, newEnv.ID); trackErr != nil {
					log.Printf("[billing] failed to track usage stop after task completion for env %s: %v", newEnv.ID, trackErr)
				}
			}
		}
	} else if settings.AutoDestroyEnv {
		e.cleanupEnv(orgID, newEnv.ID)
	}

	runMemoryPipeline()

	log.Printf("[executor] Task %s completed (PR: %s, SHA: %s)", taskID, prURL, commitSHA)
}

// CancelTask cancels a running task.
func (e *TaskExecutorService) CancelTask(taskID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if cancel, ok := e.activeTasks[taskID]; ok {
		cancel()
		delete(e.activeTasks, taskID)
	}
}

// ActiveTaskCount returns current number of executing tasks.
func (e *TaskExecutorService) ActiveTaskCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.activeTasks)
}

// execInContainer runs a command inside the gradient-env container via remote exec.
func (e *TaskExecutorService) execInContainer(ctx context.Context, executor env.RemoteExecutor, providerRef, script string, timeout time.Duration) (string, error) {
	cmd := fmt.Sprintf(`docker exec gradient-env bash -c %s`, shellQuote(script))
	return executor.ExecCommand(ctx, providerRef, cmd, timeout)
}

func (e *TaskExecutorService) waitForEnvReady(ctx context.Context, envID string, timeout time.Duration) bool {
	deadline := time.After(timeout)
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-deadline:
			return false
		case <-ticker.C:
			en, err := e.envService.GetEnvironment(ctx, envID)
			if err != nil {
				continue
			}
			if en.Status == "running" {
				return true
			}
			if en.Status == "failed" || en.Status == "destroyed" {
				return false
			}
		}
	}
}

func (e *TaskExecutorService) buildTaskPrompt(task *models.AgentTask, settings *models.TaskSettings, enableTeams bool, guidanceSection, envStateSection string) string {
	var sb strings.Builder
	sb.WriteString("# Task\n\n")
	sb.WriteString(task.Title)
	sb.WriteString("\n\n")
	if task.Description != "" {
		sb.WriteString("## Description\n\n")
		sb.WriteString(task.Description)
		sb.WriteString("\n\n")
	}
	if task.Prompt != "" && task.Prompt != task.Title && task.Prompt != task.Title+"\n\n"+task.Description {
		sb.WriteString("## Additional Instructions\n\n")
		sb.WriteString(task.Prompt)
		sb.WriteString("\n\n")
	}
	sb.WriteString("## Requirements\n\n")
	sb.WriteString("- Work in /workspace/repo if present, otherwise /workspace\n")
	sb.WriteString("- Make all necessary code changes to complete the task\n")
	sb.WriteString("- Run existing tests to verify changes don't break anything\n")
	sb.WriteString("- Add tests for new functionality where appropriate\n")
	sb.WriteString("- Commit changes with a clear commit message\n")
	sb.WriteString("- Do NOT push to remote (the orchestrator handles this)\n")

	if enableTeams {
		sb.WriteString("\n## Agent Teams\n\n")
		sb.WriteString("Agent Teams are enabled. For complex tasks that involve multiple distinct areas of work ")
		sb.WriteString("(e.g., frontend + backend, multiple independent modules, docs + code + tests), ")
		sb.WriteString("you can and should spawn teammate agents to work in parallel.\n")
		sb.WriteString("- Decompose the task into independent subtasks and delegate them to teammates.\n")
		sb.WriteString("- Each teammate works in the same repo but on different areas — coordinate via file ownership.\n")
		sb.WriteString("- Use teams proactively for cross-cutting changes, multi-file refactors, or any task that benefits from parallelism.\n")
	}

	if guidanceSection != "" {
		sb.WriteString("\n")
		sb.WriteString(guidanceSection)
		sb.WriteString("\n\n")
	}
	if envStateSection != "" {
		sb.WriteString(envStateSection)
		sb.WriteString("\n\n")
	}

	sb.WriteString("\n## Context Awareness\n\n")
	sb.WriteString("You have access to `get_context_updates`, `get_memory_guidance`, `publish_event`, and `mark_subtask` MCP tools.\n")
	sb.WriteString("- Before starting a major subtask, call `get_memory_guidance` with the subtask name if you need targeted prior guidance.\n")
	sb.WriteString("- Call `mark_subtask` when a subtask starts, completes, fails, or becomes blocked.\n")
	sb.WriteString("**After completing each sub-task** (e.g., after a commit, after running tests, after installing packages), call `get_context_updates` to check for operational updates from the system or other agents.\n")
	sb.WriteString("- If `get_context_updates` returns new information, incorporate it before proceeding.\n")
	sb.WriteString("- Use `publish_event` to share important discoveries: errors encountered, package installs, configuration changes, contract updates, or key decisions.\n")

	if task.ParentTaskID != "" {
		sb.WriteString("\n## Cross-Agent Collaboration\n\n")
		sb.WriteString("You are one of several agents working on this task in parallel. Other agents are handling different parts.\n")
		sb.WriteString("- Call `get_context_updates` frequently — other agents may have installed packages, changed configs, or encountered errors that affect your work.\n")
		sb.WriteString("- Publish events immediately when you make changes that could affect other agents, and mark subtask boundaries so the shared memory stays attributable.\n")
	}

	return sb.String()
}

func (e *TaskExecutorService) formatEnvironmentStateForPrompt(ctxObj *models.Context) string {
	var sb strings.Builder
	sb.WriteString("## Environment State\n\n")
	sb.WriteString("This is replayable runtime state from previous work on this branch. Treat it as environment context, not as task strategy.\n\n")
	if len(ctxObj.InstalledPackages) > 0 {
		sb.WriteString("**Installed packages:** ")
		for i, pkg := range ctxObj.InstalledPackages {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("%s (%s)", pkg.Name, pkg.Manager))
		}
		sb.WriteString("\n\n")
	}
	if len(ctxObj.GlobalConfigs) > 0 {
		sb.WriteString("**Environment config:**\n")
		keys := make([]string, 0, len(ctxObj.GlobalConfigs))
		for key := range ctxObj.GlobalConfigs {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range firstNStrings(keys, 10) {
			sb.WriteString(fmt.Sprintf("- %s=%s\n", key, ctxObj.GlobalConfigs[key]))
		}
	}
	return strings.TrimSpace(sb.String())
}

func (e *TaskExecutorService) buildClaudeScript(cfg *models.ClaudeConfig, hasRepo bool, mcpEnabled bool) string {
	workDir := "/workspace"
	if hasRepo {
		workDir = "/workspace/repo"
	}
	tools := strings.Join(allowedToolsForExecution(cfg, mcpEnabled), ",")

	mcpFlag := ""
	if mcpEnabled {
		mcpFlag = "--mcp-config /gradient/mcp-config.json "
	}

	maxTurns := cfg.MaxTurns
	if maxTurns < 1 {
		maxTurns = 1
	}

	teamsExport := ""
	if cfg.EnableTeams {
		teamsExport = `export CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1
`
	}

	return fmt.Sprintf(`export ANTHROPIC_API_KEY="%s"
%scd %s

claude -p "$(cat /gradient/task-prompt.md)" --output-format text --model %s --max-turns %d --allowedTools "%s" %s--verbose 2>&1
STATUS=$?
echo "[gradient] Claude execution complete after 1 iteration(s)"
exit $STATUS`,
		cfg.AnthropicAPIKey, teamsExport, workDir,
		cfg.Model, maxTurns, tools, mcpFlag,
	)
}

func sessionIDValue(session *models.AgentSession) string {
	if session == nil {
		return ""
	}
	return session.ID
}

func (e *TaskExecutorService) createPR(ctx context.Context, orgID string, task *models.AgentTask, head, base string) (string, error) {
	ghConn, err := e.githubService.GetConnection(ctx, orgID)
	if err != nil || ghConn == nil {
		return "", fmt.Errorf("GitHub not connected")
	}
	detectedBase, detectErr := e.detectDefaultBranch(ctx, ghConn.AccessToken, task.RepoFullName)
	if detectErr == nil && detectedBase != "" {
		base = detectedBase
	} else if base == "" {
		base = "main"
	}

	body := fmt.Sprintf("## Summary\n\n%s\n\n---\n*Automated by Gradient Agent (`%s`)*",
		task.Description, task.ID[:8])
	if task.LinearURL != "" {
		body += fmt.Sprintf("\n\nLinear: %s", task.LinearURL)
	}

	payload, _ := json.Marshal(map[string]string{
		"title": task.Title,
		"head":  head,
		"base":  base,
		"body":  body,
	})

	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/pulls", task.RepoFullName)
	req, _ := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(payload))
	req.Header.Set("Authorization", "token "+ghConn.AccessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("GitHub API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 201 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("GitHub API %d: %s", resp.StatusCode, truncate(string(respBody), 500))
	}

	var pr struct {
		HTMLURL string `json:"html_url"`
	}
	json.NewDecoder(resp.Body).Decode(&pr)
	return pr.HTMLURL, nil
}

func (e *TaskExecutorService) detectDefaultBranch(ctx context.Context, token, repoFullName string) (string, error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s", repoFullName)
	req, _ := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var repo struct {
		DefaultBranch string `json:"default_branch"`
	}
	if json.NewDecoder(resp.Body).Decode(&repo) == nil && repo.DefaultBranch != "" {
		log.Printf("[executor] Detected default branch for %s: %s", repoFullName, repo.DefaultBranch)
		return repo.DefaultBranch, nil
	}
	return "", fmt.Errorf("could not detect default branch")
}

func (e *TaskExecutorService) extractSummary(output string) string {
	lines := strings.Split(output, "\n")
	if len(lines) > 50 {
		lines = lines[len(lines)-50:]
	}

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var msg map[string]interface{}
		if json.Unmarshal([]byte(line), &msg) == nil {
			if t, ok := msg["type"].(string); ok && t == "result" {
				if r, ok := msg["result"].(string); ok {
					return truncate(r, 2000)
				}
			}
		}
	}

	combined := strings.Join(lines, "\n")
	if combined == "" {
		return "Task completed"
	}
	return truncate(combined, 2000)
}

func (e *TaskExecutorService) failTask(ctx context.Context, orgID, taskID, msg string) {
	e.taskService.FailTask(ctx, orgID, taskID, msg)
}

func (e *TaskExecutorService) cleanupEnv(orgID, envID string) {
	if e.billingService != nil {
		if err := e.billingService.TrackUsageStop(context.Background(), envID); err != nil {
			log.Printf("[billing] failed to track usage stop during cleanup for env %s: %v", envID, err)
		}
	}
	if err := e.envService.DestroyEnvironment(context.Background(), envID, orgID); err != nil {
		log.Printf("[executor] cleanup failed for env %s: %v", envID, err)
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func detectClaudeFatalError(output string, execErr error) string {
	text := strings.TrimSpace(output)
	lower := strings.ToLower(text)

	fatalPatterns := []string{
		"reached max turns",
		"invalid api key",
		"fix external api key",
		"authentication failed",
		"authentication error",
		"unauthorized",
		"forbidden",
		"api key is invalid",
		"missing api key",
		"no valid session id",
		"--resume requires a valid session id",
		"need permission to access",
		"permission to access the operational updates tool",
		"grant permission for `mcp__gradient-context__get_context_updates`",
		"grant permission for mcp__gradient-context__get_context_updates",
	}

	for _, pattern := range fatalPatterns {
		if strings.Contains(lower, pattern) {
			for _, line := range strings.Split(text, "\n") {
				trimmed := strings.TrimSpace(line)
				if trimmed != "" && strings.Contains(strings.ToLower(trimmed), pattern) {
					return truncate(trimmed, 400)
				}
			}
			return truncate(text, 400)
		}
	}

	if execErr != nil {
		if text != "" {
			return truncate(text, 400)
		}
		return fmt.Sprintf("Claude Code execution failed: %v", execErr)
	}

	return ""
}

func allowedToolsForExecution(cfg *models.ClaudeConfig, mcpEnabled bool) []string {
	ordered := make([]string, 0, len(cfg.AllowedTools)+len(gradientMCPAllowedTools))
	seen := make(map[string]struct{})
	for _, tool := range cfg.AllowedTools {
		trimmed := strings.TrimSpace(tool)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		ordered = append(ordered, trimmed)
	}
	if mcpEnabled {
		for _, tool := range gradientMCPAllowedTools {
			if _, ok := seen[tool]; ok {
				continue
			}
			seen[tool] = struct{}{}
			ordered = append(ordered, tool)
		}
	}
	return ordered
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
