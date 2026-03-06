package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func NewRepoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repo",
		Short: "Manage GitHub repo connections for auto-fork",
		Long:  "Connect GitHub repositories to enable auto-fork: new branches automatically inherit environment state (context + snapshots).",
	}

	cmd.AddCommand(newRepoConnectCmd())
	cmd.AddCommand(newRepoListCmd())
	cmd.AddCommand(newRepoDisconnectCmd())

	return cmd
}

func newRepoConnectCmd() *cobra.Command {
	var repo string

	cmd := &cobra.Command{
		Use:   "connect",
		Short: "Connect a GitHub repo to the current org",
		Long:  "Links a GitHub repo to your Gradient org for auto-fork. Requires the Gradient GitHub App to be installed.",
		Example: `  gc repo connect --repo owner/repo
  gc repo connect --repo my-org/my-app`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if repo == "" {
				return fmt.Errorf("--repo is required (format: owner/repo)")
			}

			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			body := map[string]string{"repo": repo}
			var result map[string]interface{}
			err = client.DoJSON("POST", "/api/v1/repos", body, &result)
			if err != nil {
				return err
			}

			fmt.Printf("Connected repo: %s\n", repo)
			fmt.Println("Auto-fork is enabled. New branches will automatically inherit context + snapshots.")
			return nil
		},
	}

	cmd.Flags().StringVar(&repo, "repo", "", "GitHub repository (format: owner/repo)")
	cmd.MarkFlagRequired("repo")

	return cmd
}

func newRepoListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List connected repos",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			var repos []map[string]interface{}
			err = client.DoJSON("GET", "/api/v1/repos", nil, &repos)
			if err != nil {
				return err
			}

			if len(repos) == 0 {
				fmt.Println("No repos connected. Run: gc repo connect --repo owner/repo")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tREPO\tAUTO-FORK\tAUTO-SNAPSHOT\tCONNECTED")
			for _, r := range repos {
				id, _ := r["id"].(string)
				name, _ := r["repo_full_name"].(string)
				autoFork := "on"
				if af, ok := r["auto_fork_enabled"].(bool); ok && !af {
					autoFork = "off"
				}
				autoSnap := "on"
				if as, ok := r["auto_snapshot_on_push"].(bool); ok && !as {
					autoSnap = "off"
				}
				created, _ := r["created_at"].(string)
				if len(created) > 10 {
					created = created[:10]
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", short(id), name, autoFork, autoSnap, created)
			}
			w.Flush()
			return nil
		},
	}
}

func newRepoDisconnectCmd() *cobra.Command {
	var id string

	cmd := &cobra.Command{
		Use:   "disconnect",
		Short: "Disconnect a repo",
		RunE: func(cmd *cobra.Command, args []string) error {
			if id == "" {
				return fmt.Errorf("--id is required")
			}

			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			var result map[string]interface{}
			err = client.DoJSON("DELETE", "/api/v1/repos/"+id, nil, &result)
			if err != nil {
				return err
			}

			fmt.Printf("Disconnected repo: %s\n", id)
			return nil
		},
	}

	cmd.Flags().StringVar(&id, "id", "", "Repo connection ID")
	cmd.MarkFlagRequired("id")

	return cmd
}

// NewSnapshotCmd creates the snapshot CLI command group
func NewSnapshotCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Manage environment snapshots",
	}

	cmd.AddCommand(newSnapshotCreateCmd())
	cmd.AddCommand(newSnapshotListCmd())

	return cmd
}

func newSnapshotCreateCmd() *cobra.Command {
	var envID string
	var tag string

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Take a snapshot of a running environment",
		RunE: func(cmd *cobra.Command, args []string) error {
			if envID == "" {
				return fmt.Errorf("--env is required")
			}

			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			body := map[string]string{"tag": tag}
			var result map[string]interface{}
			err = client.DoJSON("POST", "/api/v1/environments/"+envID+"/snapshot", body, &result)
			if err != nil {
				return err
			}

			data, _ := json.MarshalIndent(result, "", "  ")
			fmt.Println(string(data))
			return nil
		},
	}

	cmd.Flags().StringVar(&envID, "env", "", "Environment ID")
	cmd.Flags().StringVar(&tag, "tag", "", "Snapshot tag (auto-generated if empty)")
	cmd.MarkFlagRequired("env")

	return cmd
}

func newSnapshotListCmd() *cobra.Command {
	var branch string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List snapshots for a branch",
		RunE: func(cmd *cobra.Command, args []string) error {
			if branch == "" {
				return fmt.Errorf("--branch is required")
			}

			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			var snapshots []map[string]interface{}
			err = client.DoJSON("GET", "/api/v1/snapshots?branch="+branch, nil, &snapshots)
			if err != nil {
				return err
			}

			if len(snapshots) == 0 {
				fmt.Println("No snapshots found for branch:", branch)
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tBRANCH\tTYPE\tIMAGE\tCREATED")
			for _, s := range snapshots {
				id, _ := s["id"].(string)
				br, _ := s["branch"].(string)
				stype, _ := s["snapshot_type"].(string)
				img, _ := s["image_ref"].(string)
				created, _ := s["created_at"].(string)
				if len(created) > 19 {
					created = created[:19]
				}
				if len(img) > 50 {
					img = img[:50] + "..."
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", short(id), br, stype, img, created)
			}
			w.Flush()
			return nil
		},
	}

	cmd.Flags().StringVar(&branch, "branch", "", "Git branch name")
	cmd.MarkFlagRequired("branch")

	return cmd
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
