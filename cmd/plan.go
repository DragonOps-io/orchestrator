package cmd

import (
	"fmt"
	"os"

	"github.com/DragonOps-io/orchestrator/internal/cmdRunners/plan"
	magicmodel "github.com/Ilios-LLC/magicmodel-go/model"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func newPlanCmd() *cobra.Command {
	planCmd := &cobra.Command{
		Use: "plan",
	}
	planCmd.AddCommand(newPlanGroupCmd())
	planCmd.AddCommand(newPlanAppCmd())
	return planCmd
}

func newPlanGroupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "group",
		Run: func(cmd *cobra.Command, args []string) {
			payload, err := plan.GetPayload()
			if err != nil {
				log.Error().Str("GetPayload", err.Error()).Msg(fmt.Sprintf("Encountered an err: %s", err))
				os.Exit(1)
			}
			mm, err := magicmodel.NewMagicModelOperator(cmd.Context(), "dragonops-orchestrator", nil, config.WithRegion(payload.Region))
			if err != nil {
				log.Error().Str("InstantiateMagicModelOperator", "PlanGroup").Msg(fmt.Sprintf("Encountered an err: %s", err))
				os.Exit(1)
			}

			err = plan.GroupPlan(cmd.Context(), *payload, mm)
			if err != nil {
				log.Error().Str("PlanGroup", err.Error()).Msg(fmt.Sprintf("Encountered an err with planning group with id %s: %s", payload.GroupID, err))
				os.Exit(1)
			}
		},
	}
	return cmd
}

func newPlanAppCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "app",
		Run: func(cmd *cobra.Command, args []string) {
			payload, err := plan.GetPayload()
			if err != nil {
				log.Error().Str("GetPayload", err.Error()).Msg(fmt.Sprintf("Encountered an err: %s", err))
				os.Exit(1)
			}

			mm, err := magicmodel.NewMagicModelOperator(cmd.Context(), "dragonops-orchestrator", nil, config.WithRegion(payload.Region))
			if err != nil {
				log.Error().Str("InstantiateMagicModelOperator", "PlanApp").Msg(fmt.Sprintf("Encountered an err: %s", err))
				os.Exit(1)
			}

			err = plan.AppPlan(cmd.Context(), *payload, mm)
			if err != nil {
				log.Error().Str("PlanApp", err.Error()).Msg(fmt.Sprintf("Encountered an err with planning app with id %s: %s", payload.AppID, err))
				os.Exit(1)
			}
		},
	}
	return cmd
}
