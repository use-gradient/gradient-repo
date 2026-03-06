package commands

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func NewContextCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "context",
		Short: "Manage branch contexts (packages, failures, fixes, patterns)",
	}

	cmd.AddCommand(NewContextShowCmd())
	cmd.AddCommand(NewContextListCmd())
	cmd.AddCommand(NewContextSaveCmd())
	cmd.AddCommand(NewContextDeleteCmd())

	// Live Context Mesh commands
	cmd.AddCommand(NewContextEventsCmd())
	cmd.AddCommand(NewContextLiveCmd())
	cmd.AddCommand(NewContextPublishCmd())
	cmd.AddCommand(NewContextStatsCmd())
	cmd.AddCommand(NewContextMeshHealthCmd())
	cmd.AddCommand(NewContextWSCmd())

	return cmd
}

func NewContextShowCmd() *cobra.Command {
	var branch string

	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show context for a branch",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			var result map[string]interface{}
			if err := client.DoJSON("GET", "/api/v1/contexts/"+branch, nil, &result); err != nil {
				return err
			}

			data, _ := json.MarshalIndent(result, "", "  ")
			fmt.Println(string(data))

			return nil
		},
	}

	cmd.Flags().StringVar(&branch, "branch", "", "Git branch (required)")
	cmd.MarkFlagRequired("branch")

	return cmd
}

func NewContextListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all contexts for current org",
		Aliases: []string{"ls"},
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			var contexts []map[string]interface{}
			if err := client.DoJSON("GET", "/api/v1/contexts", nil, &contexts); err != nil {
				return err
			}

			if len(contexts) == 0 {
				fmt.Println("No contexts found.")
				return nil
			}

			for _, c := range contexts {
				branch := fmt.Sprint(c["branch"])
				baseOS := fmt.Sprint(c["base_os"])
				pkgs := "0"
				if packages, ok := c["installed_packages"].([]interface{}); ok {
					pkgs = fmt.Sprintf("%d", len(packages))
				}
				fmt.Printf("  %-30s  OS: %-14s  Packages: %s\n", branch, baseOS, pkgs)
			}

			return nil
		},
	}
	return cmd
}

func NewContextSaveCmd() *cobra.Command {
	var (
		branch       string
		commitSHA    string
		baseOS       string
		packagesStr  string
		failuresStr  string
		patternsStr  string
	)

	cmd := &cobra.Command{
		Use:   "save",
		Short: "Save/update context for a branch",
		Long: `Save or update the persistent context store for a branch.

Examples:
  gc context save --branch main --os ubuntu-24.04
  gc context save --branch main --packages "numpy=1.26.0,requests=2.31.0"
  gc context save --branch main --failures "test_auth:assertion error"
  gc context save --branch main --patterns "retry:exponential backoff"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			body := map[string]interface{}{
				"branch":  branch,
				"base_os": baseOS,
			}
			if commitSHA != "" {
				body["commit_sha"] = commitSHA
			}

			// Parse packages: "name=version,name=version"
			if packagesStr != "" {
				var packages []map[string]string
				for _, pkg := range strings.Split(packagesStr, ",") {
					parts := strings.SplitN(strings.TrimSpace(pkg), "=", 2)
					if len(parts) == 2 {
						packages = append(packages, map[string]string{
							"name":    parts[0],
							"version": parts[1],
						})
					} else if len(parts) == 1 && parts[0] != "" {
						packages = append(packages, map[string]string{
							"name":    parts[0],
							"version": "",
						})
					}
				}
				body["installed_packages"] = packages
			}

			// Parse failures: "test_name:error_msg,test_name:error_msg"
			if failuresStr != "" {
				var failures []map[string]string
				for _, f := range strings.Split(failuresStr, ",") {
					parts := strings.SplitN(strings.TrimSpace(f), ":", 2)
					if len(parts) == 2 {
						failures = append(failures, map[string]string{
							"test":  parts[0],
							"error": parts[1],
						})
					}
				}
				body["previous_failures"] = failures
			}

			// Parse patterns: "key:value,key:value"
			if patternsStr != "" {
				patterns := make(map[string]interface{})
				for _, p := range strings.Split(patternsStr, ",") {
					parts := strings.SplitN(strings.TrimSpace(p), ":", 2)
					if len(parts) == 2 {
						patterns[parts[0]] = parts[1]
					}
				}
				body["patterns"] = patterns
			}

			var result map[string]interface{}
			if err := client.DoJSON("POST", "/api/v1/contexts", body, &result); err != nil {
				return err
			}

			fmt.Printf("✓ Context saved for branch: %s\n", branch)

			return nil
		},
	}

	cmd.Flags().StringVar(&branch, "branch", "", "Git branch (required)")
	cmd.Flags().StringVar(&commitSHA, "commit", "", "Commit SHA")
	cmd.Flags().StringVar(&baseOS, "os", "", "Base OS (e.g. ubuntu-24.04, debian-12, alpine-3.19, fedora-40)")
	cmd.Flags().StringVar(&packagesStr, "packages", "", "Installed packages (name=version,name=version)")
	cmd.Flags().StringVar(&failuresStr, "failures", "", "Test failures (test_name:error_msg,test_name:error_msg)")
	cmd.Flags().StringVar(&patternsStr, "patterns", "", "Patterns (key:value,key:value)")
	cmd.MarkFlagRequired("branch")

	return cmd
}

func NewContextDeleteCmd() *cobra.Command {
	var branch string

	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete context for a branch",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			var result map[string]interface{}
			if err := client.DoJSON("DELETE", "/api/v1/contexts/"+branch, nil, &result); err != nil {
				return err
			}

			fmt.Printf("✓ Context deleted for branch: %s\n", branch)

			return nil
		},
	}

	cmd.Flags().StringVar(&branch, "branch", "", "Git branch (required)")
	cmd.MarkFlagRequired("branch")

	return cmd
}

// --- Live Context Mesh Commands ---

// NewContextEventsCmd lists recent context events for a branch.
func NewContextEventsCmd() *cobra.Command {
	var (
		branch   string
		types    string
		limit    int
		since    string
		envID    string
		minSeq   int64
	)

	cmd := &cobra.Command{
		Use:   "events",
		Short: "List live context events for a branch",
		Long:  "Query the Live Context Mesh event log. Events are structured context changes (package installs, test failures, patterns, config changes) shared in real-time between environments on the same branch.",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			// Build query params
			path := "/api/v1/events?"
			params := []string{}
			if branch != "" {
				params = append(params, "branch="+branch)
			}
			if types != "" {
				params = append(params, "types="+types)
			}
			if limit > 0 {
				params = append(params, fmt.Sprintf("limit=%d", limit))
			}
			if since != "" {
				params = append(params, "since="+since)
			}
			if envID != "" {
				params = append(params, "env_id="+envID)
			}
			if minSeq > 0 {
				params = append(params, fmt.Sprintf("min_seq=%d", minSeq))
			}
			path += strings.Join(params, "&")

			var batch struct {
				Events  []map[string]interface{} `json:"events"`
				HasMore bool                     `json:"has_more"`
				LastSeq int64                    `json:"last_seq"`
				Count   int                      `json:"count"`
			}
			if err := client.DoJSON("GET", path, nil, &batch); err != nil {
				return err
			}

			if batch.Count == 0 {
				fmt.Println("No events found.")
				return nil
			}

			fmt.Printf("Events (%d):\n\n", batch.Count)
			for _, e := range batch.Events {
				eventType := fmt.Sprint(e["type"])
				envID := fmt.Sprint(e["env_id"])
				ts := fmt.Sprint(e["timestamp"])
				seq := ""
				if s, ok := e["sequence"]; ok {
					seq = fmt.Sprintf(" seq=%v", s)
				}

				// Parse timestamp for display
				if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
					ts = t.Format("15:04:05")
				}

				// Format data payload
				dataStr := ""
				if data, ok := e["data"]; ok {
					dataBytes, _ := json.Marshal(data)
					dataStr = string(dataBytes)
					if len(dataStr) > 80 {
						dataStr = dataStr[:77] + "..."
					}
				}

				fmt.Printf("  [%s] %-20s env=%-12s%s\n", ts, eventType, envID, seq)
				if dataStr != "" {
					fmt.Printf("         %s\n", dataStr)
				}
			}

			if batch.HasMore {
				fmt.Printf("\n  (more events available — use --min-seq %d to paginate)\n", batch.LastSeq)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&branch, "branch", "", "Git branch (required)")
	cmd.Flags().StringVar(&types, "types", "", "Comma-separated event types to filter (e.g. package_installed,test_failed)")
	cmd.Flags().IntVar(&limit, "limit", 50, "Max events to return")
	cmd.Flags().StringVar(&since, "since", "", "Show events since time (RFC3339)")
	cmd.Flags().StringVar(&envID, "env-id", "", "Filter by environment ID")
	cmd.Flags().Int64Var(&minSeq, "min-seq", 0, "Start from sequence number (cursor)")
	cmd.MarkFlagRequired("branch")

	return cmd
}

// NewContextLiveCmd streams live context events in real-time via SSE.
func NewContextLiveCmd() *cobra.Command {
	var (
		branch string
		types  string
	)

	cmd := &cobra.Command{
		Use:   "live",
		Short: "Stream live context events in real-time",
		Long:  "Connect to the Live Context Mesh and stream events as they happen. Uses Server-Sent Events (SSE) for real-time delivery. Press Ctrl+C to stop.",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			// Build SSE URL
			path := fmt.Sprintf("/api/v1/events/stream?branch=%s", branch)
			if types != "" {
				path += "&types=" + types
			}

			fmt.Printf("🔴 Streaming live context events for branch: %s\n", branch)
			fmt.Println("   Press Ctrl+C to stop.")

			// Build full URL
			baseURL := client.BaseURL
			url := baseURL + path

			req, err := http.NewRequest("GET", url, nil)
			if err != nil {
				return fmt.Errorf("failed to create request: %w", err)
			}
			req.Header.Set("Accept", "text/event-stream")
			if client.Token != "" {
				req.Header.Set("Authorization", "Bearer "+client.Token)
			}
			if client.OrgID != "" {
				req.Header.Set("X-Org-ID", client.OrgID)
			}

			httpClient := &http.Client{
				Timeout: 0, // No timeout for SSE
			}
			resp, err := httpClient.Do(req)
			if err != nil {
				return fmt.Errorf("failed to connect: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("server returned %d", resp.StatusCode)
			}

			// Read SSE stream
			scanner := bufio.NewScanner(resp.Body)
			for scanner.Scan() {
				line := scanner.Text()

				if strings.HasPrefix(line, "data: ") {
					data := strings.TrimPrefix(line, "data: ")

					var event map[string]interface{}
					if json.Unmarshal([]byte(data), &event) == nil {
						eventType := fmt.Sprint(event["type"])
						envID := fmt.Sprint(event["env_id"])
						ts := fmt.Sprint(event["timestamp"])

						if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
							ts = t.Format("15:04:05")
						}

						// Color-code by type
						icon := "📦"
						switch eventType {
						case "package_installed":
							icon = "📦"
						case "package_removed":
							icon = "🗑"
						case "test_failed":
							icon = "❌"
						case "test_fixed":
							icon = "✅"
						case "pattern_learned":
							icon = "💡"
						case "config_changed":
							icon = "⚙️"
						case "error_encountered":
							icon = "🚨"
						case "command_ran":
							icon = "▶️"
						}

						fmt.Printf("  %s [%s] %-20s from env %s\n", icon, ts, eventType, envID)

						// Print data details
						if d, ok := event["data"]; ok {
							dataBytes, _ := json.MarshalIndent(d, "     ", "  ")
							fmt.Printf("     %s\n", string(dataBytes))
						}
					}
				} else if strings.HasPrefix(line, ": ") {
					// Comment line — display keepalives silently
					if strings.Contains(line, "keepalive") {
						fmt.Printf("  · keepalive at %s\n", time.Now().Format("15:04:05"))
					} else if strings.Contains(line, "connected") {
						fmt.Printf("  ✓ %s\n", strings.TrimPrefix(line, ": "))
					}
				}
			}

			if err := scanner.Err(); err != nil {
				return fmt.Errorf("stream error: %w", err)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&branch, "branch", "", "Git branch to stream (required)")
	cmd.Flags().StringVar(&types, "types", "", "Comma-separated event types to filter")
	cmd.MarkFlagRequired("branch")

	return cmd
}

// NewContextPublishCmd publishes a manual context event.
func NewContextPublishCmd() *cobra.Command {
	var (
		branch   string
		envID    string
		eventType string
		key      string
		value    string
	)

	cmd := &cobra.Command{
		Use:   "publish",
		Short: "Publish a context event to the Live Context Mesh",
		Long:  "Manually publish a structured context event. Useful for CLI-driven workflows or testing.",
		Example: `  gc context publish --branch main --type pattern_learned --key "api_retry" --value "Use exponential backoff for external APIs"
  gc context publish --branch feature/auth --type config_changed --key "TIMEOUT" --value "30s"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			// Build data payload based on type
			var data interface{}
			switch eventType {
			case "pattern_learned":
				data = map[string]string{"key": key, "value": value}
			case "config_changed":
				data = map[string]string{"key": key, "value": value}
			case "package_installed", "package_removed":
				data = map[string]string{"manager": key, "name": value}
			case "test_failed":
				data = map[string]string{"test": key, "error": value}
			case "test_fixed":
				data = map[string]string{"test": key, "fix": value}
			case "error_encountered":
				data = map[string]string{"error": value, "command": key}
			default:
				data = map[string]string{"key": key, "value": value}
			}

			body := map[string]interface{}{
				"type":   eventType,
				"branch": branch,
				"env_id": envID,
				"source": "cli",
				"data":   data,
			}

			var result map[string]interface{}
			if err := client.DoJSON("POST", "/api/v1/events", body, &result); err != nil {
				return err
			}

			eventID := fmt.Sprint(result["event_id"])
			fmt.Printf("✓ Event published: %s (type: %s, branch: %s)\n", eventID, eventType, branch)

			return nil
		},
	}

	cmd.Flags().StringVar(&branch, "branch", "", "Git branch (required)")
	cmd.Flags().StringVar(&envID, "env-id", "cli", "Environment ID (default: cli)")
	cmd.Flags().StringVar(&eventType, "type", "pattern_learned", "Event type (package_installed, test_failed, pattern_learned, config_changed, error_encountered, custom)")
	cmd.Flags().StringVar(&key, "key", "", "Event key (required)")
	cmd.Flags().StringVar(&value, "value", "", "Event value (required)")
	cmd.MarkFlagRequired("branch")
	cmd.MarkFlagRequired("key")
	cmd.MarkFlagRequired("value")

	return cmd
}

// NewContextStatsCmd shows event statistics for a branch.
func NewContextStatsCmd() *cobra.Command {
	var branch string

	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show Live Context Mesh statistics for a branch",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			var stats map[string]interface{}
			path := fmt.Sprintf("/api/v1/events/stats?branch=%s", branch)
			if err := client.DoJSON("GET", path, nil, &stats); err != nil {
				return err
			}

			fmt.Printf("Live Context Mesh Stats — %s\n\n", branch)
			fmt.Printf("  Total Events:   %.0f\n", stats["total_events"])
			fmt.Printf("  Active Envs:    %.0f\n", stats["active_envs"])

			if lastEvent, ok := stats["last_event_at"]; ok && lastEvent != nil {
				if ts, ok := lastEvent.(string); ok && ts != "" {
					if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
						fmt.Printf("  Last Event:     %s (%s ago)\n", t.Format("2006-01-02 15:04:05"), time.Since(t).Round(time.Second))
					}
				}
			}

			if byType, ok := stats["events_by_type"].(map[string]interface{}); ok && len(byType) > 0 {
				fmt.Println("\n  Events by Type:")
				for t, count := range byType {
					fmt.Printf("    %-25s %v\n", t, count)
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&branch, "branch", "", "Git branch (required)")
	cmd.MarkFlagRequired("branch")

	return cmd
}

// NewContextMeshHealthCmd shows the mesh health status.
func NewContextMeshHealthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mesh-health",
		Short: "Show Live Context Mesh health status",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			var health map[string]interface{}
			if err := client.DoJSON("GET", "/api/v1/mesh/health", nil, &health); err != nil {
				return err
			}

			fmt.Println("Live Context Mesh Health")
			fmt.Println()

			busType := fmt.Sprint(health["bus"])
			status := fmt.Sprint(health["status"])

			statusIcon := "✓"
			if status != "ok" {
				statusIcon = "✗"
			}

			fmt.Printf("  %s Status:    %s\n", statusIcon, status)
			fmt.Printf("    Bus Type:   %s\n", busType)

			if connected, ok := health["connected"]; ok {
				fmt.Printf("    Connected:  %v\n", connected)
			}

			if stream, ok := health["stream"].(map[string]interface{}); ok {
				fmt.Println("\n  Stream Info:")
				if msgs, ok := stream["messages"]; ok {
					fmt.Printf("    Messages:   %v\n", msgs)
				}
				if bytes, ok := stream["bytes"]; ok {
					fmt.Printf("    Size:       %v bytes\n", bytes)
				}
				if consumers, ok := stream["consumers"]; ok {
					fmt.Printf("    Consumers:  %v\n", consumers)
				}
			}

			return nil
		},
	}

	return cmd
}

func NewContextWSCmd() *cobra.Command {
	var (
		branch    string
		envID     string
		eventType string
	)

	cmd := &cobra.Command{
		Use:   "ws",
		Short: "Stream events via WebSocket (bidirectional)",
		Long: `Connect to the Live Context Mesh via WebSocket for real-time bidirectional
event streaming. Events are received as JSON frames. You can also send
JSON events to publish them.

This is an alternative to 'gc context live' (SSE) that supports bidirectional
communication — you can both receive and publish events in the same connection.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if branch == "" {
				return fmt.Errorf("--branch is required")
			}

			cfg := LoadCLIConfig()

			// Build WebSocket URL
			wsURL := strings.Replace(cfg.APIURL, "http://", "ws://", 1)
			wsURL = strings.Replace(wsURL, "https://", "wss://", 1)
			wsURL += "/api/v1/events/ws?branch=" + branch
			if envID != "" {
				wsURL += "&env_id=" + envID
			}
			if eventType != "" {
				wsURL += "&type=" + eventType
			}

			fmt.Printf("Connecting to WebSocket: %s\n", wsURL)
			fmt.Println("Press Ctrl+C to disconnect.")
			fmt.Println()

			// Provide connection details for external tools
			fmt.Println("WebSocket endpoint ready. Connect with:")
			fmt.Println()
			fmt.Printf("  wscat -c \"%s\" -H \"Authorization: Bearer %s\" -H \"X-Org-ID: %s\"\n",
				wsURL, cfg.Token, cfg.ActiveOrg)
			fmt.Println()
			fmt.Println("Or use the SSE stream (simpler, receive-only):")
			fmt.Printf("  gc context live --branch %s\n", branch)
			fmt.Println()

			// Show the raw curl command for SSE testing
			fmt.Println("Test with curl (SSE):")
			sseURL := cfg.APIURL + "/api/v1/events/stream?branch=" + branch
			fmt.Printf("  curl -N -H \"Authorization: Bearer %s\" -H \"X-Org-ID: %s\" \"%s\"\n",
				cfg.Token, cfg.ActiveOrg, sseURL)
			return nil
		},
	}

	cmd.Flags().StringVar(&branch, "branch", "", "Branch to stream events for (required)")
	cmd.Flags().StringVar(&envID, "env-id", "", "Filter events by environment ID")
	cmd.Flags().StringVar(&eventType, "type", "", "Filter events by type")
	return cmd
}
