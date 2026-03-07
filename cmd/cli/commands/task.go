package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

func NewTaskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Manage agent tasks (AI-powered development)",
		Long:  "Create, monitor, and manage tasks that are executed by Claude Code on Gradient cloud environments.",
	}

	cmd.AddCommand(newTaskCreateCmd())
	cmd.AddCommand(newTaskListCmd())
	cmd.AddCommand(newTaskGetCmd())
	cmd.AddCommand(newTaskLogsCmd())
	cmd.AddCommand(newTaskStartCmd())
	cmd.AddCommand(newTaskCancelCmd())
	cmd.AddCommand(newTaskRetryCmd())
	cmd.AddCommand(newTaskStatsCmd())

	return cmd
}

func newTaskCreateCmd() *cobra.Command {
	var (
		title       string
		description string
		branch      string
		repo        string
		autoStart   bool
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new agent task",
		Long:  "Creates a task for the AI agent to work on. Optionally auto-starts execution.",
		Example: `  gc task create --title "Add dark mode toggle"
  gc task create --title "Fix auth bug" --description "Users can't login via SSO" --branch fix/auth-sso
  gc task create --title "Add tests" --auto-start`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if title == "" {
				return fmt.Errorf("--title is required")
			}

			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			body := map[string]interface{}{
				"title":       title,
				"description": description,
				"branch":      branch,
			}
			if repo != "" {
				body["repo_full_name"] = repo
			}

			path := "/api/v1/tasks"
			if autoStart {
				path += "?auto_start=true"
			}

			var result map[string]interface{}
			if err := client.DoJSON("POST", path, body, &result); err != nil {
				return fmt.Errorf("failed to create task: %w", err)
			}

			fmt.Println("✓ Task created")
			fmt.Printf("  ID:     %s\n", result["id"])
			fmt.Printf("  Title:  %s\n", result["title"])
			fmt.Printf("  Status: %s\n", result["status"])
			if branch != "" {
				fmt.Printf("  Branch: %s\n", branch)
			}
			if autoStart {
				fmt.Println("  🚀 Auto-start enabled — task will begin shortly")
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&title, "title", "", "Task title (required)")
	cmd.Flags().StringVar(&description, "description", "", "Detailed description/instructions")
	cmd.Flags().StringVar(&branch, "branch", "", "Git branch to work on")
	cmd.Flags().StringVar(&repo, "repo", "", "Repository (owner/repo)")
	cmd.Flags().BoolVar(&autoStart, "auto-start", false, "Automatically start execution")

	return cmd
}

func newTaskListCmd() *cobra.Command {
	var (
		status string
		limit  int
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List agent tasks",
		Example: `  gc task list
  gc task list --status running
  gc task list --limit 10`,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			path := fmt.Sprintf("/api/v1/tasks?limit=%d", limit)
			if status != "" {
				path += "&status=" + status
			}

			var tasks []map[string]interface{}
			if err := client.DoJSON("GET", path, nil, &tasks); err != nil {
				return fmt.Errorf("failed to list tasks: %w", err)
			}

			if len(tasks) == 0 {
				fmt.Println("No tasks found. Create one with: gc task create --title \"...\"")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tSTATUS\tTITLE\tBRANCH\tCREATED")
			for _, t := range tasks {
				id := truncate(str(t["id"]), 12)
				st := str(t["status"])
				title := truncate(str(t["title"]), 40)
				branch := str(t["branch"])
				created := formatTimeAgo(str(t["created_at"]))

				statusIcon := "○"
				switch st {
				case "running":
					statusIcon = "●"
				case "complete":
					statusIcon = "✓"
				case "failed":
					statusIcon = "✗"
				case "cancelled":
					statusIcon = "◌"
				}

				fmt.Fprintf(w, "%s\t%s %s\t%s\t%s\t%s\n", id, statusIcon, st, title, branch, created)
			}
			w.Flush()

			return nil
		},
	}

	cmd.Flags().StringVar(&status, "status", "", "Filter by status (pending, running, complete, failed)")
	cmd.Flags().IntVar(&limit, "limit", 20, "Max results")

	return cmd
}

func newTaskGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get [task-id]",
		Short: "Get task details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			var task map[string]interface{}
			if err := client.DoJSON("GET", "/api/v1/tasks/"+args[0], nil, &task); err != nil {
				return fmt.Errorf("failed to get task: %w", err)
			}

			data, _ := json.MarshalIndent(task, "", "  ")
			fmt.Println(string(data))
			return nil
		},
	}
	return cmd
}

func newTaskLogsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs [task-id]",
		Short: "Get task execution logs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			var logs []map[string]interface{}
			if err := client.DoJSON("GET", "/api/v1/tasks/"+args[0]+"/logs", nil, &logs); err != nil {
				return fmt.Errorf("failed to get logs: %w", err)
			}

			if len(logs) == 0 {
				fmt.Println("No log entries yet.")
				return nil
			}

			for _, log := range logs {
				status := str(log["status"])
				icon := "○"
				switch status {
				case "completed":
					icon = "✓"
				case "failed":
					icon = "✗"
				case "started":
					icon = "●"
				}
				fmt.Printf("  %s [%s] %s — %s\n", icon, str(log["step"]), str(log["message"]), formatTimeAgo(str(log["created_at"])))
			}
			return nil
		},
	}
	return cmd
}

func newTaskStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start [task-id]",
		Short: "Start a pending task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			if err := client.DoJSON("POST", "/api/v1/tasks/"+args[0]+"/start", map[string]string{}, nil); err != nil {
				return fmt.Errorf("failed to start task: %w", err)
			}

			fmt.Println("✓ Task started")
			return nil
		},
	}
	return cmd
}

func newTaskCancelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cancel [task-id]",
		Short: "Cancel a running or pending task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			if err := client.DoJSON("POST", "/api/v1/tasks/"+args[0]+"/cancel", map[string]string{}, nil); err != nil {
				return fmt.Errorf("failed to cancel task: %w", err)
			}

			fmt.Println("✓ Task cancelled")
			return nil
		},
	}
	return cmd
}

func newTaskRetryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "retry [task-id]",
		Short: "Retry a failed task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			if err := client.DoJSON("POST", "/api/v1/tasks/"+args[0]+"/retry", map[string]string{}, nil); err != nil {
				return fmt.Errorf("failed to retry task: %w", err)
			}

			fmt.Println("✓ Task queued for retry")
			return nil
		},
	}
	return cmd
}

func newTaskStatsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show task statistics",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			var stats map[string]interface{}
			if err := client.DoJSON("GET", "/api/v1/tasks/stats", nil, &stats); err != nil {
				return fmt.Errorf("failed to get stats: %w", err)
			}

			fmt.Println("Agent Task Statistics")
			fmt.Println("─────────────────────")
			fmt.Printf("  Total:     %.0f\n", stats["total"])
			fmt.Printf("  Running:   %.0f\n", stats["running"])
			fmt.Printf("  Completed: %.0f\n", stats["complete"])
			fmt.Printf("  Failed:    %.0f\n", stats["failed"])
			fmt.Printf("  Pending:   %.0f\n", stats["pending"])
			if cost, ok := stats["total_cost"].(float64); ok && cost > 0 {
				fmt.Printf("  Total Cost: $%.4f\n", cost)
			}
			return nil
		},
	}
	return cmd
}

// helpers

func str(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func formatTimeAgo(ts string) string {
	if ts == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		// Try other formats
		t, err = time.Parse(time.RFC3339, ts)
		if err != nil {
			return ts
		}
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// suppress unused imports
var _ = strings.Join
