package cmd

import (
	"github.com/spf13/cobra"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "orchestrator",
	Short: "An engine for managing DragonOps resources.",
	Long: `The DragonOps orchestrator runs in ECS and is invoked whenever users choose to apply changes to DragonOps
resources.`,
}

func NewRootCommand() *cobra.Command {
	rootCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
	rootCmd.PersistentFlags().BoolP("dry-run", "d", false, "Used to skip terraform deploy/deletion. For development purposes only.")
	rootCmd.AddCommand(newGroupCmd())
	rootCmd.AddCommand(newAppCommand())
	rootCmd.AddCommand(newPlanCmd())
	rootCmd.AddCommand(newObservabilityCmd())
	return rootCmd
}
