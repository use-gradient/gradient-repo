package main

import (
	"fmt"
	"os"

	"github.com/gradient/gradient/cmd/cli/commands"
	"github.com/spf13/cobra"
)

var Version = "dev"

func main() {
	var rootCmd = &cobra.Command{
		Use:     "gc",
		Short:   "Gradient CLI — The Infrastructure Platform That AI Agents Can Actually Use",
		Long:    "Gradient CLI for managing environments, contexts, snapshots, repos, secrets, and billing across cloud providers.",
		Version: Version,
	}

	rootCmd.AddCommand(commands.NewEnvCmd())
	rootCmd.AddCommand(commands.NewContextCmd())
	rootCmd.AddCommand(commands.NewOrgCmd())
	rootCmd.AddCommand(commands.NewSecretCmd())
	rootCmd.AddCommand(commands.NewBillingCmd())
	rootCmd.AddCommand(commands.NewAuthCmd())
	rootCmd.AddCommand(commands.NewRepoCmd())
	rootCmd.AddCommand(commands.NewSnapshotCmd())
	rootCmd.AddCommand(commands.NewTaskCmd())
	rootCmd.AddCommand(commands.NewIntegrationCmd())

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
