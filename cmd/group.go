package cmd

import (
	"fmt"
	"github.com/DragonOps-io/orchestrator/internal/cmdRunners/group"
	magicmodel "github.com/Ilios-LLC/magicmodel-go/model"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/rs/zerolog/log"
	"os"

	"github.com/spf13/cobra"
)

func newGroupCmd() *cobra.Command {
	environmentCmd := &cobra.Command{
		Use:   "group",
		Short: "Interact with groups",
	}
	environmentCmd.AddCommand(newGroupApplyCmd())
	environmentCmd.AddCommand(newGroupDestroyCmd())
	return environmentCmd
}

func newGroupApplyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply a group stack",
		Run: func(cmd *cobra.Command, args []string) {
			isDryRun, _ := cmd.Flags().GetBool("dry-run")

			payload, err := group.GetPayload()
			if err != nil {
				log.Error().Str("GetPayload", err.Error()).Msg(fmt.Sprintf("Encountered an err: %s", err))
				os.Exit(1)
			}

			mm, err := magicmodel.NewMagicModelOperator(cmd.Context(), "dragonops-orchestrator", config.WithRegion(payload.Region))
			if err != nil {
				log.Error().Str("InstantiateMagicModelOperator", "ApplyGroup").Msg(fmt.Sprintf("Encountered an err: %s", err))
				os.Exit(1)
			}

			err = group.Apply(cmd.Context(), *payload, mm, isDryRun)
			if err != nil {
				log.Error().Str("ApplyGroup", err.Error()).Msg(fmt.Sprintf("Encountered an err with applying group with id %s: %s", payload.GroupID, err))
				os.Exit(1)
			}
		},
	}
	return cmd
}

func newGroupDestroyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "destroy",
		Short: "Destroy a group stack",
		Run: func(cmd *cobra.Command, args []string) {
			isDryRun, _ := cmd.Flags().GetBool("dry-run")

			payload, err := group.GetPayload()
			if err != nil {
				log.Error().Str("GetPayload", err.Error()).Msg(fmt.Sprintf("Encountered an err: %s", err))
				os.Exit(1)
			}

			mm, err := magicmodel.NewMagicModelOperator(cmd.Context(), "dragonops-orchestrator", config.WithRegion(payload.Region))
			if err != nil {
				log.Error().Str("InstantiateMagicModelOperator", "DestroyGroup").Msg(fmt.Sprintf("Encountered an err: %s", err))
				os.Exit(1)
			}

			err = group.Destroy(cmd.Context(), *payload, mm, isDryRun)
			if err != nil {
				log.Error().Str("DestroyGroup", err.Error()).Msg(fmt.Sprintf("Encountered an err with removing group with id %s: %s", payload.GroupID, err))
				os.Exit(1)
			}
		},
	}
	return cmd
}
