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
	db            *db.DB
	envService    *EnvService
	claudeService *ClaudeService
	taskService   *TaskService
	githubService *GitHubService
	linearService *LinearService
	meshPublisher *livectx.MeshPublisher
	snapshotStore *SnapshotStore

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
	meshPublisher *livectx.MeshPublisher,
	snapshotStore *SnapshotStore,
) *TaskExecutorService {
	return &TaskExecutorService{
		db:            database,
		envService:    envService,
		claudeService: claudeService,
		taskService:   taskService,
		githubService: githubService,
		linearService: linearService,
		meshPublisher: meshPublisher,
		snapshotStore: snapshotStore,
		activeTasks:   make(map[string]context.CancelFunc),
		maxConcurrent: 5,
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

	// ── Step 1: Provision environment ──
	e.taskService.addLog(ctx, taskID, "provisioning_env", "started", "Provisioning environment...", nil)

	var snapshotRef string
	if snap, snapErr := e.snapshotStore.GetLatestByBranch(ctx, orgID, baseBranch); snapErr == nil && snap != nil {
		snapshotRef = snap.ImageRef
	}

	newEnv, err := e.envService.CreateEnvironment(ctx, &CreateEnvRequest{
		Name:          fmt.Sprintf("task-%s", taskID[:8]),
		OrgID:         orgID,
		Provider:      settings.DefaultEnvProvider,
		Region:        settings.DefaultEnvRegion,
		Size:          settings.DefaultEnvSize,
		ContextBranch: baseBranch,
		SnapshotRef:   snapshotRef,
	})
	if err != nil {
		e.failTask(ctx, orgID, taskID, fmt.Sprintf("Failed to provision environment: %v", err))
		return
	}

	e.db.Pool.Exec(ctx, `UPDATE agent_tasks SET environment_id = $1, branch = $2, updated_at = NOW() WHERE id = $3`,
		newEnv.ID, baseBranch, taskID)

	e.taskService.addLog(ctx, taskID, "env_provisioned", "completed",
		fmt.Sprintf("Environment %s created (%s/%s)", newEnv.ID, settings.DefaultEnvProvider, settings.DefaultEnvSize), nil)

	// Wait for environment to be running
	if !e.waitForEnvReady(ctx, newEnv.ID, 5*time.Minute) {
		e.failTask(ctx, orgID, taskID, "Environment failed to become ready within 5 minutes")
		e.cleanupEnv(orgID, newEnv.ID)
		return
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

	// ── Step 2: Clone repo and create task branch ──
	taskBranch := fmt.Sprintf("task/%s", taskID[:8])
	repoCloned := false

	if task.RepoFullName != "" {
		ghConn, ghErr := e.githubService.GetConnection(ctx, orgID)
		if ghErr == nil && ghConn != nil {
			e.taskService.addLog(ctx, taskID, "cloning_repo", "started",
				fmt.Sprintf("Cloning %s", task.RepoFullName), nil)

			repoURL := fmt.Sprintf("https://x-access-token:%s@github.com/%s.git", ghConn.AccessToken, task.RepoFullName)
			cloneScript := fmt.Sprintf(
				`set -e; cd /workspace; git clone --depth=50 %s repo 2>&1; cd repo; git config user.email "agent@gradient.dev"; git config user.name "Gradient Agent"; git checkout -b %s 2>/dev/null || git checkout %s; echo "CLONE_OK"`,
				repoURL, taskBranch, taskBranch)

			output, cloneErr := e.execInContainer(ctx, executor, runningEnv.ClusterName, cloneScript, 5*time.Minute)
			if cloneErr != nil || !strings.Contains(output, "CLONE_OK") {
				e.failTask(ctx, orgID, taskID, fmt.Sprintf("Repo clone failed: %v (%s)", cloneErr, truncate(output, 500)))
				e.cleanupEnv(orgID, newEnv.ID)
				return
			}
			repoCloned = true
			e.taskService.addLog(ctx, taskID, "repo_cloned", "completed", "Repo cloned, branch created", nil)
		}
	}

	// ── Step 3: Write task prompt into the environment ──
	taskPrompt := e.buildTaskPrompt(task, settings)
	writePromptScript := fmt.Sprintf(`mkdir -p /gradient; cat > /gradient/task-prompt.md << 'GRADIENT_EOF'
%s
GRADIENT_EOF
echo "PROMPT_OK"`, taskPrompt)
	e.execInContainer(ctx, executor, runningEnv.ClusterName, writePromptScript, 30*time.Second)

	// ── Step 4: Run Claude Code headless ──
	e.taskService.addLog(ctx, taskID, "claude_executing", "started", "Running Claude Code...", nil)

	if e.meshPublisher != nil {
		evt, _ := livectx.NewEvent(livectx.EventCustom, orgID, baseBranch, newEnv.ID, map[string]interface{}{
			"key":   "task_started",
			"value": map[string]interface{}{"task_id": taskID, "title": task.Title},
		})
		if evt != nil {
			e.meshPublisher.Publish(ctx, evt.WithSource("executor"))
		}
	}

	claudeScript := e.buildClaudeScript(claudeCfg, repoCloned)
	claudeOutput, claudeErr := e.execInContainer(ctx, executor, runningEnv.ClusterName, claudeScript, 45*time.Minute)
	if claudeErr != nil {
		log.Printf("[executor] Claude Code returned error (may still have results): %v", claudeErr)
	}

	e.taskService.addLog(ctx, taskID, "claude_done", "completed", "Claude Code finished", nil)

	// ── Step 5: Get commit SHA and push ──
	var commitSHA, prURL string

	if repoCloned {
		shaOut, _ := e.execInContainer(ctx, executor, runningEnv.ClusterName,
			"cd /workspace/repo && git rev-parse HEAD 2>/dev/null", 15*time.Second)
		commitSHA = strings.TrimSpace(shaOut)

		pushScript := fmt.Sprintf("cd /workspace/repo && git push origin %s 2>&1 || echo 'PUSH_FAILED'", taskBranch)
		pushOut, _ := e.execInContainer(ctx, executor, runningEnv.ClusterName, pushScript, 2*time.Minute)
		if strings.Contains(pushOut, "PUSH_FAILED") {
			log.Printf("[executor] git push warning: %s", truncate(pushOut, 300))
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
			}
		}
	}

	// ── Step 6: Snapshot and complete ──
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
		ContextSaved:  true,
		SnapshotTaken: snapshotTaken,
	})

	if e.meshPublisher != nil {
		evt, _ := livectx.NewEvent(livectx.EventCustom, orgID, baseBranch, newEnv.ID, map[string]interface{}{
			"key": "task_completed",
			"value": map[string]interface{}{
				"task_id": taskID, "commit_sha": commitSHA, "pr_url": prURL,
			},
		})
		if evt != nil {
			e.meshPublisher.Publish(ctx, evt.WithSource("executor"))
		}
	}

	if settings.AutoDestroyEnv {
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
	return sb.String()
}

func (e *TaskExecutorService) buildClaudeScript(cfg *models.ClaudeConfig, hasRepo bool) string {
	workDir := "/workspace"
	if hasRepo {
		workDir = "/workspace/repo"
	}
	tools := strings.Join(cfg.AllowedTools, ",")

	return fmt.Sprintf(
		`export ANTHROPIC_API_KEY="%s" && cd %s && claude --print --output-format stream-json --model %s --max-turns %d --allowedTools "%s" --prompt "$(cat /gradient/task-prompt.md)" 2>&1; exit 0`,
		cfg.AnthropicAPIKey, workDir, cfg.Model, cfg.MaxTurns, tools,
	)
}

func (e *TaskExecutorService) createPR(ctx context.Context, orgID string, task *models.AgentTask, head, base string) (string, error) {
	ghConn, err := e.githubService.GetConnection(ctx, orgID)
	if err != nil || ghConn == nil {
		return "", fmt.Errorf("GitHub not connected")
	}
	if base == "" {
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
