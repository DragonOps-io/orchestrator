package cmd

import (
	"fmt"
	"os"

	"github.com/DragonOps-io/orchestrator/internal/cmdRunners/app"
	magicmodel "github.com/Ilios-LLC/magicmodel-go/model"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func newAppCommand() *cobra.Command {
	appCmd := &cobra.Command{
		Use:   "app",
		Short: "Interact with apps",
	}
	appCmd.AddCommand(newAppApplyCmd())
	appCmd.AddCommand(newAppDestroyCmd())
	appCmd.AddCommand(newAppRemoveCmd())
	return appCmd
}

func newAppApplyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply an app stack",
		Run: func(cmd *cobra.Command, args []string) {
			payload, err := app.GetPayload()
			if err != nil {
				log.Error().Str("GeAtPayload", err.Error()).Msg(fmt.Sprintf("Encountered an err: %s", err))
				os.Exit(1)
			}

			mm, err := magicmodel.NewMagicModelOperator(cmd.Context(), "dragonops-orchestrator", config.WithRegion(payload.Region))
			if err != nil {
				log.Error().Str("InstantiateMagicModelOperator", "ApplyApp").Msg(fmt.Sprintf("Encountered an err: %s", err))
				os.Exit(1)
			}
			isDryRun, err := cmd.Flags().GetBool("dry-run")
			log.Debug().Str("ApplyApp", "isDryRunValue").Str("JobId", "TEST").Msg(fmt.Sprintf("%v", isDryRun))

			err = app.Apply(cmd.Context(), *payload, mm, isDryRun)
			if err != nil {
				log.Error().Str("ApplyApp", err.Error()).Msg(fmt.Sprintf("Encountered an err with applying app with id %s: %s", payload.AppID, err))
				os.Exit(1)
			}
		},
	}
	return cmd
}

func newAppDestroyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "destroy",
		Short: "Destroy an app stack",
		Run: func(cmd *cobra.Command, args []string) {
			isDryRun, err := cmd.Flags().GetBool("dry-run")

			payload, err := app.GetPayload()
			if err != nil {
				log.Error().Str("GetPayload", err.Error()).Msg(fmt.Sprintf("Encountered an err: %s", err))
				os.Exit(1)
			}

			mm, err := magicmodel.NewMagicModelOperator(cmd.Context(), "dragonops-orchestrator", config.WithRegion(payload.Region))
			if err != nil {
				log.Error().Str("InstantiateMagicModelOperator", "DestroyApp").Msg(fmt.Sprintf("Encountered an err: %s", err))
				os.Exit(1)
			}

			err = app.Destroy(cmd.Context(), *payload, mm, isDryRun)
			if err != nil {
				log.Error().Str("DestroyApp", err.Error()).Msg(fmt.Sprintf("Encountered an err with destroying app with id %s: %s", payload.AppID, err))
				os.Exit(1)
			}
		},
	}
	return cmd
}

func newAppRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove",
		Short: "Remove an app completely",
		Run: func(cmd *cobra.Command, args []string) {
			isDryRun, err := cmd.Flags().GetBool("dry-run")

			payload, err := app.GetPayload()
			if err != nil {
				log.Error().Str("GetPayload", err.Error()).Msg(fmt.Sprintf("Encountered an err: %s", err))
				os.Exit(1)
			}

			mm, err := magicmodel.NewMagicModelOperator(cmd.Context(), "dragonops-orchestrator", config.WithRegion(payload.Region))
			if err != nil {
				log.Error().Str("InstantiateMagicModelOperator", "RemoveApp").Msg(fmt.Sprintf("Encountered an err: %s", err))
				os.Exit(1)
			}

			err = app.Remove(cmd.Context(), *payload, mm, isDryRun)
			if err != nil {
				log.Error().Str("RemoveApp", err.Error()).Msg(fmt.Sprintf("Encountered an err with destroying app with id %s: %s", payload.AppID, err))
				os.Exit(1)
			}
		},
	}
	return cmd
}
