package cmd

import (
	"fmt"
	"github.com/DragonOps-io/orchestrator/internal/cmdRunners/lambda"
	"os"

	magicmodel "github.com/Ilios-LLC/magicmodel-go/model"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func newLambdaCommand() *cobra.Command {
	appCmd := &cobra.Command{
		Use:   "lambda",
		Short: "Interact with lambdas",
	}
	appCmd.AddCommand(newLambdaApplyCmd())
	appCmd.AddCommand(newLambdaDestroyCmd())
	appCmd.AddCommand(newLambdaRemoveCmd())
	return appCmd
}

func newLambdaApplyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply a lambda stack",
		Run: func(cmd *cobra.Command, args []string) {
			payload, err := lambda.GetPayload()
			if err != nil {
				log.Error().Str("GetPayload", err.Error()).Msg(fmt.Sprintf("Encountered an err: %s", err))
				os.Exit(1)
			}

			mm, err := magicmodel.NewMagicModelOperator(cmd.Context(), "dragonops-orchestrator", nil, config.WithRegion(payload.Region))
			if err != nil {
				log.Error().Str("InstantiateMagicModelOperator", "ApplyLambda").Msg(fmt.Sprintf("Encountered an err: %s", err))
				os.Exit(1)
			}
			isDryRun, err := cmd.Flags().GetBool("dry-run")
			log.Debug().Str("ApplyLambda", "isDryRunValue").Str("JobId", payload.JobId).Msg(fmt.Sprintf("%v", isDryRun))

			err = lambda.Apply(cmd.Context(), *payload, mm, isDryRun)
			if err != nil {
				log.Error().Str("ApplyLambda", err.Error()).Msg(fmt.Sprintf("Encountered an err with applying lambda with id %s: %s", payload.LambdaID, err))
				os.Exit(1)
			}
		},
	}
	return cmd
}

func newLambdaDestroyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "destroy",
		Short: "Destroy a lambda stack",
		Run: func(cmd *cobra.Command, args []string) {
			payload, err := lambda.GetPayload()
			if err != nil {
				log.Error().Str("GetPayload", err.Error()).Msg(fmt.Sprintf("Encountered an err: %s", err))
				os.Exit(1)
			}

			mm, err := magicmodel.NewMagicModelOperator(cmd.Context(), "dragonops-orchestrator", nil, config.WithRegion(payload.Region))
			if err != nil {
				log.Error().Str("InstantiateMagicModelOperator", "DestroyLambda").Msg(fmt.Sprintf("Encountered an err: %s", err))
				os.Exit(1)
			}
			isDryRun, err := cmd.Flags().GetBool("dry-run")
			log.Debug().Str("DestroyLambda", "isDryRunValue").Str("JobId", payload.JobId).Msg(fmt.Sprintf("%v", isDryRun))

			err = lambda.Destroy(cmd.Context(), *payload, mm, isDryRun)
			if err != nil {
				log.Error().Str("DestroyLambda", err.Error()).Msg(fmt.Sprintf("Encountered an err with destroying lambda with id %s: %s", payload.LambdaID, err))
				os.Exit(1)
			}
		},
	}
	return cmd
}

func newLambdaRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove",
		Short: "Remove an app completely",
		Run: func(cmd *cobra.Command, args []string) {
			payload, err := lambda.GetPayload()
			if err != nil {
				log.Error().Str("GetPayload", err.Error()).Msg(fmt.Sprintf("Encountered an err: %s", err))
				os.Exit(1)
			}

			mm, err := magicmodel.NewMagicModelOperator(cmd.Context(), "dragonops-orchestrator", nil, config.WithRegion(payload.Region))
			if err != nil {
				log.Error().Str("InstantiateMagicModelOperator", "RemoveLambda").Msg(fmt.Sprintf("Encountered an err: %s", err))
				os.Exit(1)
			}
			isDryRun, err := cmd.Flags().GetBool("dry-run")
			log.Debug().Str("RemoveLambda", "isDryRunValue").Str("JobId", payload.JobId).Msg(fmt.Sprintf("%v", isDryRun))

			err = lambda.Remove(cmd.Context(), *payload, mm, isDryRun)
			if err != nil {
				log.Error().Str("RemoveLambda", err.Error()).Msg(fmt.Sprintf("Encountered an err with removing lambda with id %s: %s", payload.LambdaID, err))
				os.Exit(1)
			}
		},
	}
	return cmd
}
