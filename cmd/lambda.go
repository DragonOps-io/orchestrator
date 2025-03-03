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
	//appCmd.AddCommand(newAppDestroyCmd())
	//appCmd.AddCommand(newAppRemoveCmd())
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

			mm, err := magicmodel.NewMagicModelOperator(cmd.Context(), "dragonops-orchestrator", config.WithRegion(payload.Region))
			if err != nil {
				log.Error().Str("InstantiateMagicModelOperator", "ApplyApp").Msg(fmt.Sprintf("Encountered an err: %s", err))
				os.Exit(1)
			}
			isDryRun, err := cmd.Flags().GetBool("dry-run")
			log.Debug().Str("ApplyApp", "isDryRunValue").Str("JobId", payload.JobId).Msg(fmt.Sprintf("%v", isDryRun))

			err = lambda.Apply(cmd.Context(), *payload, mm, isDryRun)
			if err != nil {
				log.Error().Str("ApplyApp", err.Error()).Msg(fmt.Sprintf("Encountered an err with applying app with id %s: %s", payload.LambdaID, err))
				os.Exit(1)
			}
		},
	}
	return cmd
}

//func newAppDestroyCmd() *cobra.Command {
//	cmd := &cobra.Command{
//		Use:   "destroy",
//		Short: "Destroy an app stack",
//		Run: func(cmd *cobra.Command, args []string) {
//			isDryRun, err := cmd.Flags().GetBool("dry-run")
//
//			payload, err := app.GetPayload()
//			if err != nil {
//				log.Error().Str("GetPayload", err.Error()).Msg(fmt.Sprintf("Encountered an err: %s", err))
//				os.Exit(1)
//			}
//
//			mm, err := magicmodel.NewMagicModelOperator(cmd.Context(), "dragonops-orchestrator", config.WithRegion(payload.Region))
//			if err != nil {
//				log.Error().Str("InstantiateMagicModelOperator", "DestroyApp").Msg(fmt.Sprintf("Encountered an err: %s", err))
//				os.Exit(1)
//			}
//
//			err = app.Destroy(cmd.Context(), *payload, mm, isDryRun)
//			if err != nil {
//				log.Error().Str("DestroyApp", err.Error()).Msg(fmt.Sprintf("Encountered an err with destroying app with id %s: %s", payload.AppID, err))
//				os.Exit(1)
//			}
//		},
//	}
//	return cmd
//}
//
//func newAppRemoveCmd() *cobra.Command {
//	cmd := &cobra.Command{
//		Use:   "remove",
//		Short: "Remove an app completely",
//		Run: func(cmd *cobra.Command, args []string) {
//			isDryRun, err := cmd.Flags().GetBool("dry-run")
//
//			payload, err := app.GetPayload()
//			if err != nil {
//				log.Error().Str("GetPayload", err.Error()).Msg(fmt.Sprintf("Encountered an err: %s", err))
//				os.Exit(1)
//			}
//
//			mm, err := magicmodel.NewMagicModelOperator(cmd.Context(), "dragonops-orchestrator", config.WithRegion(payload.Region))
//			if err != nil {
//				log.Error().Str("InstantiateMagicModelOperator", "RemoveApp").Msg(fmt.Sprintf("Encountered an err: %s", err))
//				os.Exit(1)
//			}
//
//			err = app.Remove(cmd.Context(), *payload, mm, isDryRun)
//			if err != nil {
//				log.Error().Str("RemoveApp", err.Error()).Msg(fmt.Sprintf("Encountered an err with destroying app with id %s: %s", payload.AppID, err))
//				os.Exit(1)
//			}
//		},
//	}
//	return cmd
//}
