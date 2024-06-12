package cmd

import (
	"fmt"
	"github.com/DragonOps-io/orchestrator/internal/cmdRunners/plan"
	magicmodel "github.com/Ilios-LLC/magicmodel-go/model"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"os"
)

func newPlanCmd() *cobra.Command {
	environmentCmd := &cobra.Command{
		Use: "plan",
	}
	environmentCmd.AddCommand(newPlanGroupCmd())
	return environmentCmd
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

			mm, err := magicmodel.NewMagicModelOperator(cmd.Context(), "dragonops-orchestrator", config.WithRegion(payload.Region))
			if err != nil {
				log.Error().Str("InstantiateMagicModelOperator", "PlanGroup").Msg(fmt.Sprintf("Encountered an err: %s", err))
				os.Exit(1)
			}

			err = plan.Plan(cmd.Context(), *payload, mm)
			if err != nil {
				log.Error().Str("PlanGroup", err.Error()).Msg(fmt.Sprintf("Encountered an err with planning group with id %s: %s", payload.GroupID, err))
				os.Exit(1)
			}
		},
	}
	return cmd
}
