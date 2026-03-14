package services

import (
	"strings"
	"testing"

	"github.com/gradient/gradient/internal/models"
)

func TestBuildTaskPromptUsesMaterializedContextGuidanceAndFallbackMCP(t *testing.T) {
	executor := &TaskExecutorService{}
	task := &models.AgentTask{
		Title:       "Audit billing flow",
		Description: "Review billing lifecycle and document gaps.",
	}

	prompt := executor.buildTaskPrompt(
		task,
		&models.TaskSettings{},
		true,
		"long",
		"Touches billing, docs, and runtime behavior",
		"## Retrieved Guidance\n\n[HIGH][RECOVERY] Re-run targeted billing checks first.",
		"## Materialized Branch Context\n\nCurrent state\n- Billing credits are enabled.",
	)

	if !strings.Contains(prompt, "Claude Teams is enabled for this run") {
		t.Fatalf("expected long-task prompt to enable Claude Teams, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "## Retrieved Guidance") {
		t.Fatalf("expected prompt to include retrieved guidance, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "## Materialized Branch Context") {
		t.Fatalf("expected prompt to include materialized branch context, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Call `get_memory_guidance` at most once") {
		t.Fatalf("expected prompt to restrict memory guidance calls, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "`get_context_updates` is fallback-only") {
		t.Fatalf("expected prompt to describe fallback-only context updates, got:\n%s", prompt)
	}
	if strings.Contains(prompt, "After completing each sub-task") {
		t.Fatalf("prompt should not require repeated context update polling, got:\n%s", prompt)
	}
}

func TestAllowedToolsForExecutionAddsGradientMCPToolsOnce(t *testing.T) {
	cfg := &models.ClaudeConfig{
		AllowedTools: []string{
			"Read",
			"Write",
			"mcp__gradient-context__get_context_updates",
			"Read",
		},
	}

	tools := allowedToolsForExecution(cfg, true)
	joined := strings.Join(tools, ",")

	for _, required := range gradientMCPAllowedTools {
		if !strings.Contains(joined, required) {
			t.Fatalf("expected required MCP tool %q in %v", required, tools)
		}
	}
	if countExact(tools, "Read") != 1 {
		t.Fatalf("expected duplicate tools to be de-duplicated, got %v", tools)
	}
	if countExact(tools, "mcp__gradient-context__get_context_updates") != 1 {
		t.Fatalf("expected gradient MCP tool to appear once, got %v", tools)
	}
}

func TestBuildClaudeScriptEnablesTeamsOnlyWhenRequested(t *testing.T) {
	executor := &TaskExecutorService{}
	cfg := &models.ClaudeConfig{
		AnthropicAPIKey: "sk-ant-test",
		Model:           "claude-sonnet-test",
		MaxTurns:        25,
		AllowedTools:    []string{"Read"},
	}

	withTeams := executor.buildClaudeScript(cfg, true, true, true)
	withoutTeams := executor.buildClaudeScript(cfg, true, true, false)

	if !strings.Contains(withTeams, "CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1") {
		t.Fatalf("expected teams-enabled script to export Claude Teams flag, got:\n%s", withTeams)
	}
	if strings.Contains(withoutTeams, "CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1") {
		t.Fatalf("expected non-team script to omit Claude Teams flag, got:\n%s", withoutTeams)
	}
	if !strings.Contains(withTeams, "--mcp-config /gradient/mcp-config.json") {
		t.Fatalf("expected MCP-enabled script to include MCP config, got:\n%s", withTeams)
	}
}

func countExact(items []string, target string) int {
	count := 0
	for _, item := range items {
		if item == target {
			count++
		}
	}
	return count
}
