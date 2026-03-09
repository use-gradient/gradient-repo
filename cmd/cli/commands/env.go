package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// gradientSSHKeyPath returns the path to the gradient SSH key, if it exists.
func gradientSSHKeyPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	keyPath := filepath.Join(home, ".ssh", "gradient-key")
	if _, err := os.Stat(keyPath); err == nil {
		return keyPath
	}
	return ""
}

func NewEnvCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "env",
		Short: "Manage environments",
	}

	cmd.AddCommand(NewEnvCreateCmd())
	cmd.AddCommand(NewEnvListCmd())
	cmd.AddCommand(NewEnvStatusCmd())
	cmd.AddCommand(NewEnvDestroyCmd())
	cmd.AddCommand(NewEnvSSHCmd())
	cmd.AddCommand(NewEnvHealthCmd())
	cmd.AddCommand(NewEnvLogsCmd())
	cmd.AddCommand(NewEnvExecCmd())
	cmd.AddCommand(NewEnvAutoscaleCmd())

	return cmd
}

func NewEnvCreateCmd() *cobra.Command {
	var (
		name          string
		provider      string
		region        string
		contextBranch string
		size          string
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new environment",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			body := map[string]string{
				"name":     name,
				"provider": provider,
				"region":   region,
				"size":     size,
			}
			if contextBranch != "" {
				body["context_branch"] = contextBranch
			}

			var result map[string]interface{}
			if err := client.DoJSON("POST", "/api/v1/environments", body, &result); err != nil {
				return err
			}

			fmt.Printf("✓ Environment created\n")
			fmt.Printf("  ID:       %s\n", result["id"])
			fmt.Printf("  Name:     %s\n", result["name"])
			fmt.Printf("  Provider: %s\n", result["provider"])
			fmt.Printf("  Region:   %s\n", result["region"])
			fmt.Printf("  Size:     %s\n", result["size"])
			fmt.Printf("  Status:   %s\n", result["status"])

			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Environment name (required)")
	cmd.Flags().StringVar(&provider, "provider", "", "Cloud provider (hetzner, aws, gcp, etc. — defaults to primary configured provider)")
	cmd.Flags().StringVar(&region, "region", "", "Region (required)")
	cmd.Flags().StringVar(&contextBranch, "context-branch", "", "Git branch for context replay")
	cmd.Flags().StringVar(&size, "size", "small", "Size: small, medium, large, gpu")

	cmd.MarkFlagRequired("name")
	cmd.MarkFlagRequired("region")

	return cmd
}

func NewEnvListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List environments",
		Aliases: []string{"ls"},
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			var envs []map[string]interface{}
			if err := client.DoJSON("GET", "/api/v1/environments", nil, &envs); err != nil {
				return err
			}

			if len(envs) == 0 {
				fmt.Println("No environments found.")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tPROVIDER\tREGION\tSIZE\tSTATUS")
			for _, e := range envs {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
					e["id"], e["name"], e["provider"], e["region"], e["size"], e["status"])
			}
			w.Flush()

			return nil
		},
	}
	return cmd
}

func NewEnvStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status [env-id]",
		Short: "Get environment status",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			var result map[string]interface{}
			if err := client.DoJSON("GET", "/api/v1/environments/"+args[0], nil, &result); err != nil {
				return err
			}

			data, _ := json.MarshalIndent(result, "", "  ")
			fmt.Println(string(data))

			return nil
		},
	}
	return cmd
}

func NewEnvDestroyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "destroy [env-id]",
		Short: "Destroy an environment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			var result map[string]interface{}
			if err := client.DoJSON("DELETE", "/api/v1/environments/"+args[0], nil, &result); err != nil {
				return err
			}

			fmt.Printf("✓ Environment %s destruction initiated\n", args[0])

			return nil
		},
	}
	return cmd
}

func NewEnvSSHCmd() *cobra.Command {
	var container bool
	var infoOnly bool

	cmd := &cobra.Command{
		Use:   "ssh [env-id]",
		Short: "SSH into a running environment",
		Long:  `Connect to a running environment via SSH. Requires the environment to be in "running" state.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			var sshInfo map[string]interface{}
			if err := client.DoJSON("GET", "/api/v1/environments/"+args[0]+"/ssh-info", nil, &sshInfo); err != nil {
				return fmt.Errorf("failed to get SSH info: %w", err)
			}

			host := fmt.Sprint(sshInfo["host"])
			user := fmt.Sprint(sshInfo["user"])

			keyPath := gradientSSHKeyPath()

			if infoOnly {
				keyFlag := ""
				if keyPath != "" {
					keyFlag = fmt.Sprintf(" -i %s", keyPath)
				}
				fmt.Printf("SSH connection info for environment %s:\n\n", args[0])
				fmt.Printf("  Host:     %s\n", host)
				fmt.Printf("  User:     %s\n", user)
				fmt.Printf("  Port:     22\n\n")
				fmt.Printf("  Connect to host:\n")
				fmt.Printf("    ssh%s %s@%s\n\n", keyFlag, user, host)
				fmt.Printf("  Connect directly into container:\n")
				fmt.Printf("    ssh%s %s@%s -t 'docker exec -it gradient-env /bin/bash'\n", keyFlag, user, host)
				return nil
			}

			sshArgs := []string{
				"-o", "StrictHostKeyChecking=no",
				"-o", "UserKnownHostsFile=/dev/null",
				"-o", "LogLevel=ERROR",
			}
			if keyPath != "" {
				sshArgs = append(sshArgs, "-i", keyPath)
			}

			if container {
				sshArgs = append(sshArgs, "-t", fmt.Sprintf("%s@%s", user, host),
					"docker exec -it gradient-env /bin/bash")
			} else {
				sshArgs = append(sshArgs, fmt.Sprintf("%s@%s", user, host))
			}

			sshBin, err := exec.LookPath("ssh")
			if err != nil {
				return fmt.Errorf("ssh not found in PATH: %w", err)
			}

			proc := exec.Command(sshBin, sshArgs...)
			proc.Stdin = os.Stdin
			proc.Stdout = os.Stdout
			proc.Stderr = os.Stderr
			return proc.Run()
		},
	}

	cmd.Flags().BoolVarP(&container, "container", "c", false, "SSH directly into the Docker container")
	cmd.Flags().BoolVar(&infoOnly, "info", false, "Print connection info instead of connecting")
	return cmd
}

func NewEnvHealthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "health [env-id]",
		Short: "Get environment health (agent status, system metrics)",
		Long:  `Query the gradient-agent running in the environment for health status, including CPU, memory, disk usage, container status, and mesh connectivity.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			var result map[string]interface{}
			if err := client.DoJSON("GET", "/api/v1/environments/"+args[0]+"/health", nil, &result); err != nil {
				return fmt.Errorf("failed to get health: %w", err)
			}

			status, _ := result["status"].(string)
			agent, _ := result["agent"].(string)

			if agent == "unreachable" {
				fmt.Printf("⚠  Environment %s — agent unreachable\n", args[0])
				fmt.Printf("   Status:  %s\n", status)
				if msg, ok := result["message"].(string); ok {
					fmt.Printf("   Reason:  %s\n", msg)
				}
				return nil
			}

			// Format nicely
			fmt.Printf("Environment Health: %s\n\n", args[0])

			containerUp, _ := result["container_up"].(bool)
			containerSymbol := "✗"
			if containerUp {
				containerSymbol = "✓"
			}
			fmt.Printf("  Container:     %s (%s)\n", containerSymbol, status)
			version := "unknown"
			if v, ok := result["version"].(string); ok && v != "" {
				version = v
			}
			fmt.Printf("  Agent:         %s (v%s)\n", "✓", version)

			if disk, ok := result["disk_usage_pct"].(float64); ok {
				fmt.Printf("  Disk:          %.1f%%\n", disk)
			}
			if mem, ok := result["mem_usage_pct"].(float64); ok {
				fmt.Printf("  Memory:        %.1f%%\n", mem)
			}
			if cpu, ok := result["cpu_usage_pct"].(float64); ok {
				fmt.Printf("  CPU:           %.1f%%\n", cpu)
			}
			if uptime, ok := result["uptime_sec"].(float64); ok {
				hours := int(uptime) / 3600
				mins := (int(uptime) % 3600) / 60
				fmt.Printf("  Uptime:        %dh %dm\n", hours, mins)
			}
			if snapshots, ok := result["snapshot_count"].(float64); ok {
				fmt.Printf("  Snapshots:     %d\n", int(snapshots))
			}
			if mesh, ok := result["mesh"].(string); ok {
				fmt.Printf("  Mesh:          %s\n", mesh)
			}
			if branch, ok := result["branch"].(string); ok && branch != "" {
				fmt.Printf("  Branch:        %s\n", branch)
			}

			return nil
		},
	}
	return cmd
}

func NewEnvLogsCmd() *cobra.Command {
	var tail int

	cmd := &cobra.Command{
		Use:   "logs [env-id]",
		Short: "View environment container logs",
		Long:  `Fetch logs from the gradient-env container running in the environment via SSH.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			// Get SSH info
			var sshInfo map[string]interface{}
			if err := client.DoJSON("GET", "/api/v1/environments/"+args[0]+"/ssh-info", nil, &sshInfo); err != nil {
				return fmt.Errorf("failed to get SSH info: %w", err)
			}

			host := fmt.Sprint(sshInfo["host"])

			// Execute docker logs via SSH
			tailFlag := ""
			if tail > 0 {
				tailFlag = fmt.Sprintf("--tail %d", tail)
			}
			sshArgs := []string{
				"-o", "StrictHostKeyChecking=no",
				"-o", "ConnectTimeout=10",
			}
			if keyPath := gradientSSHKeyPath(); keyPath != "" {
				sshArgs = append(sshArgs, "-i", keyPath)
			}
			sshArgs = append(sshArgs,
				fmt.Sprintf("root@%s", host),
				fmt.Sprintf("docker logs gradient-env %s 2>&1", tailFlag),
			)
			sshCmd := exec.Command("ssh", sshArgs...)
			sshCmd.Stdout = os.Stdout
			sshCmd.Stderr = os.Stderr
			return sshCmd.Run()
		},
	}

	cmd.Flags().IntVar(&tail, "tail", 100, "Number of lines to show (0 for all)")
	return cmd
}

func NewEnvExecCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exec [env-id] -- [command...]",
		Short: "Execute a command in the environment container",
		Long: `Run a command inside the gradient-env Docker container via SSH.

Examples:
  gc env exec abc123 -- ls -la /workspace
  gc env exec abc123 -- pip install torch
  gc env exec abc123 -- python -c "print('hello')"`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			envID := args[0]

			// Find the command after "--"
			cmdArgs := []string{"/bin/bash"}
			dashIdx := cmd.ArgsLenAtDash()
			if dashIdx >= 0 && dashIdx < len(args) {
				cmdArgs = args[dashIdx:]
			} else if len(args) > 1 {
				cmdArgs = args[1:]
			}

			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			// Get SSH info
			var sshInfo map[string]interface{}
			if err := client.DoJSON("GET", "/api/v1/environments/"+envID+"/ssh-info", nil, &sshInfo); err != nil {
				return fmt.Errorf("failed to get SSH info: %w", err)
			}

			host := fmt.Sprint(sshInfo["host"])

			// Build docker exec command — use -t only when we have a terminal
			dockerFlag := "-i"
			if fileInfo, _ := os.Stdin.Stat(); fileInfo.Mode()&os.ModeCharDevice != 0 {
				dockerFlag = "-it"
			}
			dockerCmd := fmt.Sprintf("docker exec %s gradient-env %s", dockerFlag, strings.Join(cmdArgs, " "))

			sshArgs := []string{
				"-o", "StrictHostKeyChecking=no",
				"-o", "UserKnownHostsFile=/dev/null",
				"-o", "LogLevel=ERROR",
				"-o", "ConnectTimeout=10",
			}
			if fileInfo, _ := os.Stdin.Stat(); fileInfo.Mode()&os.ModeCharDevice != 0 {
				sshArgs = append(sshArgs, "-t")
			}
			if keyPath := gradientSSHKeyPath(); keyPath != "" {
				sshArgs = append(sshArgs, "-i", keyPath)
			}
			sshArgs = append(sshArgs, fmt.Sprintf("root@%s", host), dockerCmd)
			sshCmd := exec.Command("ssh", sshArgs...)
			sshCmd.Stdin = os.Stdin
			sshCmd.Stdout = os.Stdout
			sshCmd.Stderr = os.Stderr
			return sshCmd.Run()
		},
	}
	return cmd
}

func NewEnvAutoscaleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "autoscale",
		Short: "Manage auto-scaling for environments",
	}

	cmd.AddCommand(NewAutoscaleEnableCmd())
	cmd.AddCommand(NewAutoscaleDisableCmd())
	cmd.AddCommand(NewAutoscaleStatusCmd())
	cmd.AddCommand(NewAutoscaleHistoryCmd())

	return cmd
}

func NewAutoscaleEnableCmd() *cobra.Command {
	var (
		minReplicas        int
		maxReplicas        int
		targetCPU          float64
		targetMem          float64
		scaleUpThreshold   float64
		scaleDownThreshold float64
		cooldown           int
	)

	cmd := &cobra.Command{
		Use:   "enable [env-id]",
		Short: "Enable auto-scaling for an environment",
		Long:  "Configure and enable horizontal autoscaling. Gradient monitors CPU/memory and scales replicas automatically.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			policy := map[string]interface{}{
				"min_replicas":         minReplicas,
				"max_replicas":         maxReplicas,
				"target_cpu":           targetCPU,
				"target_memory":        targetMem,
				"scale_up_threshold":   scaleUpThreshold,
				"scale_down_threshold": scaleDownThreshold,
				"cooldown_secs":        cooldown,
				"enabled":              true,
			}

			var result map[string]interface{}
			if err := client.DoJSON("PUT", "/api/v1/environments/"+args[0]+"/autoscale", policy, &result); err != nil {
				return fmt.Errorf("failed to enable autoscale: %w", err)
			}

			fmt.Printf("✓ Auto-scaling enabled for %s\n", args[0])
			fmt.Printf("  Min replicas:        %d\n", minReplicas)
			fmt.Printf("  Max replicas:        %d\n", maxReplicas)
			fmt.Printf("  Target CPU:          %.0f%%\n", targetCPU*100)
			fmt.Printf("  Target Memory:       %.0f%%\n", targetMem*100)
			fmt.Printf("  Scale-up threshold:  %.0f%%\n", scaleUpThreshold*100)
			fmt.Printf("  Scale-down threshold:%.0f%%\n", scaleDownThreshold*100)
			fmt.Printf("  Cooldown:            %ds\n", cooldown)
			return nil
		},
	}

	cmd.Flags().IntVar(&minReplicas, "min", 1, "Minimum replicas")
	cmd.Flags().IntVar(&maxReplicas, "max", 10, "Maximum replicas")
	cmd.Flags().Float64Var(&targetCPU, "target-cpu", 0.7, "Target CPU utilization (0-1)")
	cmd.Flags().Float64Var(&targetMem, "target-memory", 0.8, "Target memory utilization (0-1)")
	cmd.Flags().Float64Var(&scaleUpThreshold, "scale-up", 0.85, "Scale-up trigger threshold (0-1)")
	cmd.Flags().Float64Var(&scaleDownThreshold, "scale-down", 0.30, "Scale-down trigger threshold (0-1)")
	cmd.Flags().IntVar(&cooldown, "cooldown", 300, "Cooldown period in seconds between scaling actions")

	return cmd
}

func NewAutoscaleDisableCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "disable [env-id]",
		Short: "Disable auto-scaling for an environment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			if err := client.DoJSON("DELETE", "/api/v1/environments/"+args[0]+"/autoscale", nil, nil); err != nil {
				return fmt.Errorf("failed to disable autoscale: %w", err)
			}

			fmt.Printf("✓ Auto-scaling disabled for %s\n", args[0])
			return nil
		},
	}
	return cmd
}

func NewAutoscaleStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status [env-id]",
		Short: "Show auto-scaling status for an environment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			var status map[string]interface{}
			if err := client.DoJSON("GET", "/api/v1/environments/"+args[0]+"/autoscale/status", nil, &status); err != nil {
				if strings.Contains(err.Error(), "404") {
					fmt.Println("No auto-scaling policy configured for this environment.")
					return nil
				}
				return fmt.Errorf("failed to get autoscale status: %w", err)
			}

			data, _ := json.MarshalIndent(status, "", "  ")
			fmt.Println(string(data))
			return nil
		},
	}
	return cmd
}

func NewAutoscaleHistoryCmd() *cobra.Command {
	var limit int

	cmd := &cobra.Command{
		Use:   "history [env-id]",
		Short: "Show auto-scaling history for an environment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			var events []map[string]interface{}
			path := fmt.Sprintf("/api/v1/environments/%s/autoscale/history?limit=%d", args[0], limit)
			if err := client.DoJSON("GET", path, nil, &events); err != nil {
				return fmt.Errorf("failed to get autoscale history: %w", err)
			}

			if len(events) == 0 {
				fmt.Println("No scaling events recorded.")
				return nil
			}

			fmt.Printf("%-12s %-6s %-6s %-6s %-8s %-8s %s\n",
				"Direction", "From", "To", "CPU%", "Mem%", "When", "ID")
			for _, e := range events {
				dir, _ := e["direction"].(string)
				from := e["from_replicas"]
				to := e["to_replicas"]
				cpu := e["trigger_cpu"]
				mem := e["trigger_memory"]
				id, _ := e["id"].(string)
				when, _ := e["created_at"].(string)
				if len(id) > 8 {
					id = id[:8]
				}
				if len(when) > 19 {
					when = when[:19]
				}
				fmt.Printf("%-12s %-6v %-6v %-6v %-8v %-19s %s\n",
					dir, from, to, cpu, mem, when, id)
			}
			return nil
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 20, "Number of events to show")
	return cmd
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
