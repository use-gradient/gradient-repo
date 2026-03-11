package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
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
	claudeService  *ClaudeService
	taskService    *TaskService
	githubService  *GitHubService
	linearService  *LinearService
	contextService *ContextService
	meshPublisher  *livectx.MeshPublisher
	snapshotStore  *SnapshotStore

	mu            sync.Mutex
	activeTasks   map[string]context.CancelFunc
	maxConcurrent int
}

func NewTaskExecutorService(
	database *db.DB,
	envService *EnvService,
	claudeService *ClaudeService,
	taskService *TaskService,
	githubService *GitHubService,
	linearService *LinearService,
	contextService *ContextService,
	meshPublisher *livectx.MeshPublisher,
	snapshotStore *SnapshotStore,
) *TaskExecutorService {
	return &TaskExecutorService{
		db:             database,
		envService:     envService,
		claudeService:  claudeService,
		taskService:    taskService,
		githubService:  githubService,
		linearService:  linearService,
		contextService: contextService,
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

	// ── Step 1: Provision or reuse environment ──
	e.taskService.addLog(ctx, taskID, "provisioning_env", "started", "Looking for environment...", nil)

	var newEnv *models.Environment
	reusedEnv := false

	if task.RepoFullName != "" {
		existing, _ := e.envService.GetByOrgRepoAndBranch(ctx, orgID, task.RepoFullName, baseBranch)
		if existing != nil && existing.Status == "sleeping" {
			e.taskService.addLog(ctx, taskID, "env_waking", "started",
				fmt.Sprintf("Waking sleeping environment %s", existing.ID), nil)
			if wakeErr := e.envService.WakeEnvironment(ctx, existing.ID, orgID); wakeErr == nil {
				newEnv = existing
				reusedEnv = true
			} else {
				log.Printf("[executor] Failed to wake env %s: %v, creating new", existing.ID, wakeErr)
			}
		}
	}

	if newEnv == nil {
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
			e.failTask(ctx, orgID, taskID, fmt.Sprintf("Failed to provision environment: %v", createErr))
			return
		}

		e.taskService.addLog(ctx, taskID, "env_provisioned", "completed",
			fmt.Sprintf("Environment %s created (%s/%s)", newEnv.ID, settings.DefaultEnvProvider, settings.DefaultEnvSize), nil)
	}

	e.db.Pool.Exec(ctx, `UPDATE agent_tasks SET environment_id = $1, branch = $2, updated_at = NOW() WHERE id = $3`,
		newEnv.ID, baseBranch, taskID)

	if !reusedEnv || newEnv.Status != "running" {
		if !e.waitForEnvReady(ctx, newEnv.ID, 5*time.Minute) {
			e.failTask(ctx, orgID, taskID, "Environment failed to become ready within 5 minutes")
			if !reusedEnv {
				e.cleanupEnv(orgID, newEnv.ID)
			}
			return
		}
	}

	runningEnv, _ := e.envService.GetEnvironment(ctx, newEnv.ID)
	if runningEnv == nil || runningEnv.ClusterName == "" {
		e.failTask(ctx, orgID, taskID, "Environment has no provider reference")
		e.cleanupEnv(orgID, newEnv.ID)
		return
	}

	provider, err := e.envService.GetProviderByName(runningEnv.Provider)
	if err != nil {
		e.failTask(ctx, orgID, taskID, fmt.Sprintf("Provider unavailable: %v", err))
		e.cleanupEnv(orgID, newEnv.ID)
		return
	}
	executor, ok := env.AsRemoteExecutor(provider)
	if !ok {
		e.failTask(ctx, orgID, taskID, "Provider does not support remote execution")
		e.cleanupEnv(orgID, newEnv.ID)
		return
	}

	// Wait for SSM agent, then for cloud-init to finish (Docker install, container start)
	e.taskService.addLog(ctx, taskID, "waiting_for_ssm", "started", "Waiting for instance to accept commands...", nil)
	if err := executor.WaitForReady(ctx, runningEnv.ClusterName, 5*time.Minute); err != nil {
		e.failTask(ctx, orgID, taskID, fmt.Sprintf("Instance never became SSM-ready: %v", err))
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
		e.failTask(ctx, orgID, taskID, "Cloud-init did not complete within 5 minutes (Docker not installed)")
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
				e.failTask(ctx, orgID, taskID, fmt.Sprintf("Repo clone failed: %v (%s)", cloneErr, truncate(output, 500)))
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

	// ── Step 3: Load existing context and write task prompt ──
	var existingContext string
	if e.contextService != nil && task.RepoFullName != "" {
		if ctxObj, err := e.contextService.GetContext(ctx, orgID, baseBranch); err == nil && ctxObj != nil {
			existingContext = e.formatContextForPrompt(ctxObj)
		}
	}

	taskPrompt := e.buildTaskPrompt(task, settings)
	if existingContext != "" {
		taskPrompt += "\n\n" + existingContext
	}

	writePromptScript := fmt.Sprintf(`mkdir -p /gradient; cat > /gradient/task-prompt.md << 'GRADIENT_EOF'
%s
GRADIENT_EOF
echo "PROMPT_OK"`, taskPrompt)
	e.execInContainer(ctx, executor, runningEnv.ClusterName, writePromptScript, 30*time.Second)

	// ── Step 3.5: Set up MCP context server for cross-agent communication ──
	mcpSetupScript := `mkdir -p /gradient/context && cat > /gradient/mcp-context.js << 'MCPEOF'
const fs = require('fs');
const readline = require('readline');
const LIVE = '/gradient/context/live.json';
const OUTBOX = '/gradient/context/outbox.jsonl';

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
  if (req.method === 'initialize') return {jsonrpc:"2.0",id:req.id,result:{protocolVersion:"2024-11-05",capabilities:{tools:{}},serverInfo:{name:"gradient-context",version:"0.1.0"}}};
  if (req.method === 'notifications/initialized') return null;
  if (req.method === 'tools/list') return {jsonrpc:"2.0",id:req.id,result:{tools:[
    {name:"get_context_updates",description:"Check for updates from other agents working on the same task. Returns packages installed, errors encountered, patterns learned. Call periodically.",inputSchema:{type:"object",properties:{}}},
    {name:"publish_event",description:"Share a discovery with other agents: errors, patterns, packages, decisions.",inputSchema:{type:"object",properties:{event_type:{type:"string",description:"error_encountered, pattern_learned, package_installed, config_changed, decision_made"},message:{type:"string",description:"Description"},data:{type:"object",description:"Structured data"}},required:["event_type","message"]}}
  ]}};
  if (req.method === 'tools/call') return toolCall(req);
  return {jsonrpc:"2.0",id:req.id,error:{code:-32601,message:"method not found: "+req.method}};
}

function toolCall(req) {
  const p = JSON.parse(JSON.stringify(req.params || {}));
  const name = p.name;
  const args = p.arguments || {};
  if (name === 'get_context_updates') return getUpdates(req.id);
  if (name === 'publish_event') return publish(req.id, args);
  return {jsonrpc:"2.0",id:req.id,error:{code:-32602,message:"unknown tool: "+name}};
}

function getUpdates(id) {
  try {
    const data = JSON.parse(fs.readFileSync(LIVE, 'utf8'));
    let out = '## Context Updates from Other Agents\n\n';
    if (data.last_update) out += 'Last update: ' + data.last_update + '\n\n';
    if (data.packages && Object.keys(data.packages).length) {
      out += '### Packages Installed by Peers\n';
      for (const [n,v] of Object.entries(data.packages)) out += '- ' + n + ': ' + v + '\n';
      out += '\n';
    }
    if (data.errors && data.errors.length) {
      out += '### Errors (' + data.errors.length + ')\n';
      data.errors.slice(-10).forEach(e => out += '- ' + JSON.stringify(e) + '\n');
      out += '\n';
    }
    if (data.patterns && Object.keys(data.patterns).length) {
      out += '### Patterns\n';
      for (const [k,v] of Object.entries(data.patterns)) out += '- ' + k + ': ' + v + '\n';
      out += '\n';
    }
    const evts = data.events || [];
    if (evts.length) {
      const show = evts.slice(-10);
      out += '### Recent Events (' + evts.length + ' total, last ' + show.length + ')\n';
      show.forEach(e => out += '- ' + JSON.stringify(e) + '\n');
    }
    return tr(id, out || 'No updates from other agents yet.');
  } catch(e) { return tr(id, 'No context updates available yet.'); }
}

function publish(id, args) {
  if (!args.event_type || !args.message) return er(id, 'event_type and message required');
  const entry = {type:args.event_type, message:args.message, timestamp:new Date().toISOString()};
  if (args.data) entry.data = args.data;
  try {
    fs.mkdirSync('/gradient/context', {recursive:true});
    fs.appendFileSync(OUTBOX, JSON.stringify(entry) + '\n');
    return tr(id, 'Event published: [' + args.event_type + '] ' + args.message);
  } catch(e) { return er(id, 'Write failed: ' + e.message); }
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
		e.taskService.addLog(ctx, taskID, "mcp_setup", "completed", "MCP context server configured", nil)
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
			e.failTask(ctx, orgID, taskID, fmt.Sprintf("Claude CLI install failed: %s", truncate(installOut, 300)))
			e.cleanupEnv(orgID, newEnv.ID)
			return
		}
		log.Printf("[executor] Claude CLI installed successfully")
	} else {
		log.Printf("[executor] Claude CLI found at: %s", claudeCheckTrimmed)
	}

	claudeScript := e.buildClaudeScript(claudeCfg, repoCloned, mcpReady)
	log.Printf("[executor] Running Claude Code for task %s...", taskID[:8])
	claudeOutput, claudeErr := e.execInContainer(ctx, executor, runningEnv.ClusterName, claudeScript, 45*time.Minute)
	if claudeErr != nil {
		log.Printf("[executor] Claude Code returned error (may still have results): %v", claudeErr)
	}
	log.Printf("[executor] Claude Code output for task %s: %s", taskID[:8], truncate(claudeOutput, 1000))

	e.taskService.addLog(ctx, taskID, "claude_done", "completed",
		fmt.Sprintf("Claude Code finished (%d bytes output)", len(claudeOutput)), nil)

	// ── Step 5: Get commit SHA and push ──
	var commitSHA, prURL string

	if repoCloned {
		shaOut, _ := e.execInContainer(ctx, executor, runningEnv.ClusterName,
			"cd /workspace/repo && git rev-parse HEAD 2>/dev/null", 15*time.Second)
		commitSHA = strings.TrimSpace(shaOut)

		// Check if Claude actually made any commits
		diffCheck, _ := e.execInContainer(ctx, executor, runningEnv.ClusterName,
			fmt.Sprintf("cd /workspace/repo && git log --oneline %s..HEAD 2>/dev/null || echo NO_NEW_COMMITS", taskBranch), 15*time.Second)
		log.Printf("[executor] Git diff check for task %s: %s", taskID[:8], truncate(strings.TrimSpace(diffCheck), 300))

		pushScript := fmt.Sprintf("cd /workspace/repo && git push origin %s 2>&1 || echo 'PUSH_FAILED'", taskBranch)
		pushOut, _ := e.execInContainer(ctx, executor, runningEnv.ClusterName, pushScript, 2*time.Minute)
		if strings.Contains(pushOut, "PUSH_FAILED") {
			log.Printf("[executor] git push warning: %s", truncate(pushOut, 300))
		} else {
			log.Printf("[executor] git push for task %s: %s", taskID[:8], truncate(pushOut, 300))
		}

		// Create PR
		if settings.AutoCreatePR && task.RepoFullName != "" {
			e.taskService.addLog(ctx, taskID, "creating_pr", "started", "Creating pull request...", nil)
			prURL, err = e.createPR(ctx, orgID, task, taskBranch, baseBranch)
			if err != nil {
				log.Printf("[executor] PR creation failed: %v", err)
				e.taskService.addLog(ctx, taskID, "pr_failed", "failed", fmt.Sprintf("PR creation failed: %v", err), nil)
			} else {
				e.taskService.addLog(ctx, taskID, "pr_created", "completed", fmt.Sprintf("PR: %s", prURL), nil)

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

	e.taskService.CompleteTask(ctx, orgID, taskID, CompleteTaskRequest{
		OutputSummary: outputSummary,
		CommitSHA:     commitSHA,
		PRURL:         prURL,
		ContextSaved:  contextSaved,
		SnapshotTaken: snapshotTaken,
	})

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
		}
	} else if settings.AutoDestroyEnv {
		e.cleanupEnv(orgID, newEnv.ID)
	}

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

func (e *TaskExecutorService) buildTaskPrompt(task *models.AgentTask, settings *models.TaskSettings) string {
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

	if task.ParentTaskID != "" {
		sb.WriteString("\n## Cross-Agent Collaboration\n\n")
		sb.WriteString("You are one of several agents working on this task in parallel. Other agents are handling different parts.\n")
		sb.WriteString("- Use the `get_context_updates` tool periodically to check what other agents have done\n")
		sb.WriteString("- Use the `publish_event` tool to share important discoveries, errors, or decisions\n")
		sb.WriteString("- If another agent has installed packages or changed configs, take that into account\n")
		sb.WriteString("- If you encounter an error that might affect other agents, publish it immediately\n")
	}

	return sb.String()
}

func (e *TaskExecutorService) formatContextForPrompt(ctxObj *models.Context) string {
	var sb strings.Builder
	sb.WriteString("## Existing Context\n\n")
	sb.WriteString("Previous work has been done on this branch. Here is the accumulated context:\n\n")
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
	if len(ctxObj.PreviousFailures) > 0 {
		sb.WriteString("**Previous failures:**\n")
		for _, f := range ctxObj.PreviousFailures {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", f.Test, f.Error))
		}
		sb.WriteString("\n")
	}
	for k, v := range ctxObj.Patterns {
		sb.WriteString(fmt.Sprintf("**Pattern** %s: %v\n", k, v))
	}
	return sb.String()
}

func (e *TaskExecutorService) buildClaudeScript(cfg *models.ClaudeConfig, hasRepo bool, mcpEnabled bool) string {
	workDir := "/workspace"
	if hasRepo {
		workDir = "/workspace/repo"
	}
	tools := strings.Join(cfg.AllowedTools, ",")

	mcpFlag := ""
	if mcpEnabled {
		mcpFlag = "--mcp-config /gradient/mcp-config.json "
	}

	return fmt.Sprintf(
		`export ANTHROPIC_API_KEY="%s" && cd %s && claude -p "$(cat /gradient/task-prompt.md)" --output-format text --model %s --max-turns %d --allowedTools "%s" %s--verbose 2>&1; exit 0`,
		cfg.AnthropicAPIKey, workDir, cfg.Model, cfg.MaxTurns, tools, mcpFlag,
	)
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

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
