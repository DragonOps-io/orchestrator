package app

import (
	"context"
	"fmt"
	"github.com/DragonOps-io/orchestrator/internal/terraform"
	"github.com/DragonOps-io/orchestrator/internal/utils"
	"github.com/DragonOps-io/types"
	magicmodel "github.com/Ilios-LLC/magicmodel-go/model"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/rs/zerolog/log"
	"os"
	"strings"
)

func Destroy(ctx context.Context, payload Payload, mm *magicmodel.Operator, isDryRun bool) error {
	log.Debug().
		Str("AppID", payload.AppID).
		Msg("Attempting to remove app")

	app := types.App{}
	o := mm.Find(&app, payload.AppID)
	if o.Err != nil {
		return fmt.Errorf("an error occurred when trying to find the item with id %s: %s", payload.AppID, o.Err)
	}
	log.Debug().Str("AppID", app.ID).Msg("Found app")

	var appEnvironmentsToDestroy []types.Environment
	for _, envId := range payload.EnvironmentIDs {
		env := types.Environment{}
		o = mm.Find(&env, envId)
		if o.Err != nil {
			return o.Err
		}
		appEnvironmentsToDestroy = append(appEnvironmentsToDestroy, env)
	}
	log.Debug().Str("AppID", app.ID).Msg("Retrieved environments to destroy")

	receiptHandle := os.Getenv("RECEIPT_HANDLE")
	if receiptHandle == "" {
		ue := updateEnvironmentStatusesToDestroyFailed(app, appEnvironmentsToDestroy, mm, fmt.Errorf("no RECEIPT_HANDLE variable found").Error())
		if ue != nil {
			return ue
		}
		return fmt.Errorf("Error retrieving RECEIPT_HANDLE from queue. Cannot continue.")
	}

	var accounts []types.Account
	o = mm.Where(&accounts, "IsMasterAccount", aws.Bool(true))
	if o.Err != nil {
		o = mm.Update(&app, "Status", "DELETE_FAILED")
		if o.Err != nil {
			return o.Err
		}
		return fmt.Errorf("an error occurred when trying to find the Master Account: %s", o.Err)
	}

	log.Debug().Str("AppID", payload.AppID).Msg("Found Master Account")
	// get the doApiKey from secrets manager, not the payload

	cfg, err := config.LoadDefaultConfig(ctx, func(options *config.LoadOptions) error {
		config.WithRegion(accounts[0].AwsRegion)
		return nil
	})
	if err != nil {
		return err
	}

	doApiKey, err := utils.GetDoApiKeyFromSecretsManager(ctx, cfg, payload.UserName)
	if err != nil {
		o = mm.Update(&app, "Status", "DELETE_FAILED")
		if o.Err != nil {
			return o.Err
		}
		return fmt.Errorf("an error occurred when trying to find the Do Api Key: %s", o.Err)
	}
	authResponse, err := utils.IsApiKeyValid(*doApiKey)
	if err != nil {
		aco := mm.Update(&app, "Status", "APPLY_FAILED")
		if aco.Err != nil {
			return o.Err
		}
		return fmt.Errorf("error verifying validity of DragonOps Api Key: %v", err)
	}
	if !authResponse.IsValid {
		aco := mm.Update(&app, "Status", "APPLY_FAILED")
		if aco.Err != nil {
			return o.Err
		}
		return fmt.Errorf("The DragonOps api key provided is not valid. Please reach out to DragonOps support for help.")
	}

	sqsClient := sqs.NewFromConfig(cfg, func(o *sqs.Options) {
		o.Region = accounts[0].AwsRegion
	})

	if !isDryRun {
		os.Setenv("DRAGONOPS_TERRAFORM_ARTIFACT", "/app/tmpl.tgz.age")

		log.Debug().Str("AppID", app.ID).Msg("Preparing Terraform")
		log.Debug().Str("AppID", app.ID).Msg(fmt.Sprintf("The artifact path is %s", os.Getenv("DRAGONOPS_TERRAFORM_ARTIFACT")))

		var execPath *string
		execPath, err = terraform.PrepareTerraform(ctx)
		if err != nil {
			ue := updateEnvironmentStatusesToDestroyFailed(app, appEnvironmentsToDestroy, mm, err.Error())
			if ue != nil {
				return ue
			}
			return err
		}

		log.Debug().Str("AppID", app.ID).Msg("Dry run is false. Running terraform")

		err = formatWithWorkerAndDestroy(ctx, accounts[0].AwsRegion, mm, app, appEnvironmentsToDestroy, execPath)
		if err != nil {
			ue := updateEnvironmentStatusesToDestroyFailed(app, appEnvironmentsToDestroy, mm, err.Error())
			if ue != nil {
				return ue
			}
			return err
		}
	} else {
		for _, env := range appEnvironmentsToDestroy {
			for idx := range app.Environments {
				if app.Environments[idx].Environment == env.ResourceLabel && app.Environments[idx].Group == env.Group.ResourceLabel {
					app.Environments[idx].Status = "DESTROYED"
					app.Environments[idx].Endpoint = ""
				}
			}
			o = mm.Save(&app)
			if o.Err != nil {
				return o.Err
			}
			log.Debug().Str("AppID", app.ID).Msg("App environment status updated")
		}
	}

	queueParts := strings.Split(*app.AppSqsArn, ":")
	queueUrl := fmt.Sprintf("https://%s.%s.amazonaws.com/%s/%s", queueParts[2], queueParts[3], queueParts[4], queueParts[5])

	log.Debug().Str("AppID", app.ID).Msg(fmt.Sprintf("Queue url is %s", queueUrl))

	_, err = sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      &queueUrl,
		ReceiptHandle: &receiptHandle,
	})
	if err != nil {
		return err
	}

	return nil
}

func updateEnvironmentStatusesToDestroyFailed(app types.App, environmentsToApply []types.Environment, mm *magicmodel.Operator, errMsg string) error {
	for _, env := range environmentsToApply {
		for idx := range app.Environments {
			if app.Environments[idx].Environment == env.ResourceLabel && app.Environments[idx].Group == env.Group.ResourceLabel && app.Environments[idx].Status == "DESTROYING" {
				app.Environments[idx].Status = "DESTROY_FAILED"
				app.Environments[idx].FailedReason = errMsg
				//app.Environments[idx] = appEnvConfig
			}
		}
	}
	aco := mm.Update(&app, "Environments", app.Environments)
	if aco.Err != nil {
		return aco.Err
	}
	return nil
}

func formatWithWorkerAndDestroy(ctx context.Context, masterAcctRegion string, mm *magicmodel.Operator, app types.App, environments []types.Environment, execPath *string) error {
	log.Debug().Str("AppID", app.ID).Msg("Templating Terraform with correct values")

	for _, env := range environments {
		appPath := fmt.Sprintf("/apps/%s/%s", app.ID, env.ID)
		command := fmt.Sprintf("/app/worker app apply --app-id %s --environment-id %s --table-region %s", app.ID, env.ID, masterAcctRegion)
		os.Setenv("DRAGONOPS_TERRAFORM_DESTINATION", appPath)

		if os.Getenv("IS_LOCAL") == "true" {
			appPath = fmt.Sprintf("./apps/%s/%s", app.ID, env.ID)
			os.Setenv("DRAGONOPS_TERRAFORM_DESTINATION", appPath)
			command = fmt.Sprintf("./app/worker app apply --app-id %s --environment-id %s --table-region %s", app.ID, env.ID, masterAcctRegion)
		}

		log.Debug().Str("AppID", app.ID).Msg(appPath)
		log.Debug().Str("AppID", app.ID).Msg(fmt.Sprintf("Applying application files found at %s", os.Getenv("DRAGONOPS_TERRAFORM_DESTINATION")))

		log.Debug().Str("AppID", app.ID).Msg(fmt.Sprintf("Running command %s", command))
		msg, err := utils.RunOSCommandOrFail(command)
		if err != nil {
			ue := updateEnvironmentStatusesToDestroyFailed(app, environments, mm, err.Error())
			if ue != nil {
				return ue
			}
			return fmt.Errorf("Error running `worker app apply` with app with id %s and environment with id %s: %v", app.ID, env.ID, err)
		}
		log.Debug().Str("AppID", app.ID).Msg(*msg)

		var roleToAssume *string
		if env.Group.Account.CrossAccountRoleArn != nil {
			roleToAssume = env.Group.Account.CrossAccountRoleArn
		}
		// apply terraform or return an error
		_, err = terraform.DestroyTerraform(ctx, fmt.Sprintf("%s/application", appPath), *execPath, roleToAssume)
		if err != nil {
			ue := updateEnvironmentStatusesToDestroyFailed(app, environments, mm, err.Error())
			if ue != nil {
				return ue
			}
			return fmt.Errorf("Error running apply with app with id %s and environment with id %s: %v", app.ID, env.ID, err)
		}

		log.Debug().Str("AppID", app.ID).Msg("Updating app status")

		for idx := range app.Environments {
			if app.Environments[idx].Environment == env.ResourceLabel && app.Environments[idx].Group == env.Group.ResourceLabel {
				app.Environments[idx].Status = "DESTROYED"
				app.Environments[idx].Endpoint = ""
				break
			}
		}
		o := mm.Save(&app)
		if o.Err != nil {
			return o.Err
		}
		log.Debug().Str("AppID", app.ID).Msg("App status updated")
		return nil
	}
	return nil
}
