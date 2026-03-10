package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// TaskClassifier uses an LLM to classify tasks as short or long-running
// and decompose long tasks into parallel sub-tasks with explicit scopes.
type TaskClassifier struct {
	claudeService *ClaudeService
}

func NewTaskClassifier(claude *ClaudeService) *TaskClassifier {
	return &TaskClassifier{claudeService: claude}
}

// TaskClassification is the result of classifying a task.
type TaskClassification struct {
	Complexity        string              `json:"complexity"`
	EstimatedDuration string              `json:"estimated_duration"`
	SubTasks          []SubTaskDefinition `json:"sub_tasks,omitempty"`
	Contracts         []ContractDef       `json:"contracts,omitempty"`
	Reasoning         string              `json:"reasoning"`
}

// SubTaskDefinition describes a decomposed sub-task for parallel execution.
type SubTaskDefinition struct {
	Role        string    `json:"role"`
	Description string    `json:"description"`
	Scope       TaskScope `json:"scope"`
	DependsOn   []string  `json:"depends_on"`
}

// TaskScope defines what files/modules a sub-task agent may touch.
type TaskScope struct {
	OwnedPaths    []string `json:"owned_paths"`
	ReadOnlyPaths []string `json:"read_only_paths,omitempty"`
	TestSuites    []string `json:"test_suites,omitempty"`
}

// ContractDef defines an inter-agent agreement produced during classification.
type ContractDef struct {
	Type  string          `json:"type"`
	Scope string          `json:"scope"`
	Spec  json.RawMessage `json:"spec"`
}

// ClassifyTask sends the task to Claude for classification and decomposition.
func (c *TaskClassifier) ClassifyTask(ctx context.Context, orgID string, task *IncomingTask, repoTree string) (*TaskClassification, error) {
	cfg, err := c.claudeService.GetConfig(ctx, orgID, "")
	if err != nil || cfg == nil {
		return nil, fmt.Errorf("Claude not configured")
	}

	prompt := c.buildClassificationPrompt(task, repoTree)

	result, err := c.callClaude(ctx, cfg.AnthropicAPIKey, cfg.Model, prompt)
	if err != nil {
		log.Printf("[classifier] Claude API call failed: %v, falling back to short task", err)
		return &TaskClassification{
			Complexity:        "short",
			EstimatedDuration: "30m",
			Reasoning:         "Classification failed, defaulting to short task",
		}, nil
	}

	var classification TaskClassification
	if err := json.Unmarshal([]byte(result), &classification); err != nil {
		log.Printf("[classifier] Failed to parse classification: %v, response: %s", err, truncate(result, 500))
		return &TaskClassification{
			Complexity:        "short",
			EstimatedDuration: "30m",
			Reasoning:         "Parse failed, defaulting to short task",
		}, nil
	}

	return &classification, nil
}

func (c *TaskClassifier) buildClassificationPrompt(task *IncomingTask, repoTree string) string {
	treeSection := ""
	if repoTree != "" {
		treeSection = fmt.Sprintf("\n## Repository File Tree\n\n```\n%s\n```\n", repoTree)
	}

	return fmt.Sprintf(`You are a task classifier for a software engineering agent system. Analyze the following task and classify it.

## Task

Title: %s
Description: %s
Labels: %v
%s
## Instructions

Respond with ONLY a JSON object (no markdown, no explanation) with this structure:
{
  "complexity": "short" or "long",
  "estimated_duration": "e.g. 30m, 2h, 4h",
  "reasoning": "brief explanation of why this is short/long",
  "sub_tasks": [
    {
      "role": "backend|frontend|tests|infra|docs",
      "description": "what this sub-task does",
      "scope": {
        "owned_paths": ["paths this agent owns"],
        "test_suites": ["test files to run"]
      },
      "depends_on": ["roles this depends on"]
    }
  ],
  "contracts": [
    {
      "type": "api_shape|invariant|schema",
      "scope": "what this covers",
      "spec": {}
    }
  ]
}

Rules:
- "short": single file/module change, simple bug fix, config change, docs update (<1hr)
- "long": cross-cutting changes, new features spanning multiple modules, major refactors (>1hr)
- For short tasks, sub_tasks should be empty or have exactly one entry
- For long tasks, decompose into 2-5 sub-tasks that can run in parallel where possible
- Each sub-task must have explicit owned_paths so agents don't conflict`, task.Title, task.Description, task.Labels, treeSection)
}

func (c *TaskClassifier) callClaude(ctx context.Context, apiKey, model, prompt string) (string, error) {
	reqBody := map[string]interface{}{
		"model":      model,
		"max_tokens": 4096,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}
	bodyJSON, _ := json.Marshal(reqBody)

	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(bodyJSON))
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("Anthropic API request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("Anthropic API %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("failed to parse Claude response: %w", err)
	}

	for _, c := range result.Content {
		if c.Type == "text" {
			return c.Text, nil
		}
	}
	return "", fmt.Errorf("no text content in Claude response")
}
