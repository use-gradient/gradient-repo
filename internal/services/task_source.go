package services

import (
	"context"
	"fmt"
	"time"
)

// TaskSource is the pluggable interface for task management integrations.
// Linear is the first implementation; Jira, GitHub Issues, etc. can follow.
type TaskSource interface {
	Name() string
	ParseEvent(payload []byte) (*IncomingTask, error)
	UpdateStatus(ctx context.Context, orgID, taskID, status string) error
	AddComment(ctx context.Context, orgID, taskID, comment string) error
	GetTaskDetails(ctx context.Context, orgID, taskID string) (*TaskDetails, error)
}

// IncomingTask represents a task received from an external source.
type IncomingTask struct {
	ExternalID   string            `json:"external_id"`
	Identifier   string            `json:"identifier"`
	ExternalURL  string            `json:"external_url"`
	Title        string            `json:"title"`
	Description  string            `json:"description"`
	Labels       []string          `json:"labels"`
	Priority     string            `json:"priority"`
	Assignee     string            `json:"assignee,omitempty"`
	RepoFullName string            `json:"repo_full_name,omitempty"`
	Branch       string            `json:"branch,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
}

// TaskDetails contains enriched information about a task.
type TaskDetails struct {
	ExternalID  string   `json:"external_id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	State       string   `json:"state"`
	Labels      []string `json:"labels"`
	Comments    []string `json:"comments,omitempty"`
}

// LinearTaskSource adapts LinearService to the TaskSource interface.
type LinearTaskSource struct {
	linear *LinearService
}

func NewLinearTaskSource(linear *LinearService) *LinearTaskSource {
	return &LinearTaskSource{linear: linear}
}

func (s *LinearTaskSource) Name() string { return "linear" }

func (s *LinearTaskSource) ParseEvent(payload []byte) (*IncomingTask, error) {
	action, data, err := s.linear.ParseWebhookEvent(payload)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, fmt.Errorf("missing event data")
	}
	// Handle both "create" (issue created with label) and "update" (label added later).
	// Removes and other actions are ignored — deduplication in the webhook handler
	// prevents double-processing if both create and update fire for the same issue.
	if action != "create" && action != "update" {
		return nil, nil
	}

	task := &IncomingTask{
		CreatedAt: time.Now(),
	}
	if id, ok := data["id"].(string); ok {
		task.ExternalID = id
	}
	if ident, ok := data["identifier"].(string); ok {
		task.Identifier = ident
	}
	if title, ok := data["title"].(string); ok {
		task.Title = title
	}
	if desc, ok := data["description"].(string); ok {
		task.Description = desc
	}
	if url, ok := data["url"].(string); ok {
		task.ExternalURL = url
	}
	if labels, ok := data["labels"].([]interface{}); ok {
		for _, l := range labels {
			if lm, ok := l.(map[string]interface{}); ok {
				if name, ok := lm["name"].(string); ok {
					task.Labels = append(task.Labels, name)
				}
			}
		}
	}
	return task, nil
}

func (s *LinearTaskSource) UpdateStatus(ctx context.Context, orgID, taskID, status string) error {
	return s.linear.UpdateIssueState(ctx, orgID, taskID, status)
}

func (s *LinearTaskSource) AddComment(ctx context.Context, orgID, taskID, comment string) error {
	return s.linear.AddComment(ctx, orgID, taskID, comment)
}

func (s *LinearTaskSource) GetTaskDetails(ctx context.Context, orgID, taskID string) (*TaskDetails, error) {
	return &TaskDetails{ExternalID: taskID}, nil
}
