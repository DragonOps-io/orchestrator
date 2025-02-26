package cmd

import (
	"fmt"
	"os"

	"github.com/DragonOps-io/orchestrator/internal/cmdRunners/observability"
	magicmodel "github.com/Ilios-LLC/magicmodel-go/model"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func newObservabilityCmd() *cobra.Command {
	cmmd := &cobra.Command{
		Use:   "observability",
		Short: "Interact with observability",
	}
	cmmd.AddCommand(newObservabilityApplyCmd())
	cmmd.AddCommand(newObservabilityDestroyCmd())
	return cmmd
}

func newObservabilityApplyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply an observability stack",
		Run: func(cmd *cobra.Command, args []string) {
			isDryRun, _ := cmd.Flags().GetBool("dry-run")
			payload, err := observability.GetPayload()
			if err != nil {
				log.Error().Str("GetPayload", err.Error()).Msg(fmt.Sprintf("Encountered an err: %s", err))
				os.Exit(1)
			}

			mm, err := magicmodel.NewMagicModelOperator(cmd.Context(), "dragonops-orchestrator", config.WithRegion(payload.Region))
			if err != nil {
				log.Error().Str("InstantiateMagicModelOperator", "ApplyObservability").Msg(fmt.Sprintf("Encountered an err: %s", err))
				os.Exit(1)
			}

			err = observability.Apply(cmd.Context(), *payload, mm, isDryRun)
			if err != nil {
				log.Error().Str("ApplyObservability", err.Error()).Msg("Encountered an err with applying")
				os.Exit(1)
			}
		},
	}
	return cmd
}

func newObservabilityDestroyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "destroy",
		Short: "Destroy an observability stack",
		Run: func(cmd *cobra.Command, args []string) {
			isDryRun, _ := cmd.Flags().GetBool("dry-run")

			payload, err := observability.GetPayload()
			if err != nil {
				log.Error().Str("GetPayload", err.Error()).Msg(fmt.Sprintf("Encountered an err: %s", err))
				os.Exit(1)
			}

			mm, err := magicmodel.NewMagicModelOperator(cmd.Context(), "dragonops-orchestrator", config.WithRegion(payload.Region))
			if err != nil {
				log.Error().Str("InstantiateMagicModelOperator", "DestroyObservability").Msg(fmt.Sprintf("Encountered an err: %s", err))
				os.Exit(1)
			}

			err = observability.Destroy(cmd.Context(), *payload, mm, isDryRun)
			if err != nil {
				log.Error().Str("DestroyObservability", err.Error()).Msg(fmt.Sprintf("Encountered an err with removing observability stack with id: %s", err.Error()))
				os.Exit(1)
			}
		},
	}
	return cmd
}
