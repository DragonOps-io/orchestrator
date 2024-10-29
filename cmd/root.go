package cmd

import (
	"os"

	"github.com/DragonOps-io/orchestrator/internal/utils"
	"github.com/bugsnag/bugsnag-go/v2"
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

	// Bugsnag stuff. Is there a better place for this that will only run once?
	devKey := "8707cf2b-d77e-4e3e-b227-790024844919"
	bugsnagApiKey, _ := utils.RetrieveBugsnagApiKey(devKey)
	// How do we return an error here?

	bugsnag.Configure(bugsnag.Configuration{
		APIKey:          *bugsnagApiKey,
		ReleaseStage:    os.Getenv("DRAGONOPS_ENVIRONMENT"),
		ProjectPackages: []string{"main", "github.com/DragonOps-io/worker"},
	})

	return rootCmd
}
