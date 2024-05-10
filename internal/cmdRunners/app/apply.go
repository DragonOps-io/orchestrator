package app

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/DragonOps-io/orchestrator/internal/terraform"
	"github.com/DragonOps-io/orchestrator/internal/utils"
	"github.com/DragonOps-io/types"
	magicmodel "github.com/Ilios-LLC/magicmodel-go/model"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/rs/zerolog/log"
	"os"
	"strings"
)

type AppUrl string

func Apply(ctx context.Context, payload Payload, mm *magicmodel.Operator, isDryRun bool) error {
	log.Debug().
		Str("AppID", payload.AppID).
		Msg("Looking for app with matching ID")

	app := types.App{}
	o := mm.Find(&app, payload.AppID)
	if o.Err != nil {
		return fmt.Errorf("an error occurred when trying to find the app with id %s: %s", payload.AppID, o.Err)
	}
	log.Debug().Str("AppID", app.ID).Msg("Found app")

	var appEnvironmentsToApply []types.Environment
	for _, envId := range payload.EnvironmentIDs {
		env := types.Environment{}
		o = mm.Find(&env, envId)
		if o.Err != nil {
			return o.Err
		}
		appEnvironmentsToApply = append(appEnvironmentsToApply, env)
	}
	log.Debug().Str("AppID", app.ID).Msg("Retrieved environments to apply")

	receiptHandle := os.Getenv("RECEIPT_HANDLE")
	if receiptHandle == "" {
		ue := updateEnvironmentStatusesToApplyFailed(app, appEnvironmentsToApply, mm, fmt.Errorf("no RECEIPT_HANDLE variable found"))
		if ue != nil {
			return ue
		}
		return fmt.Errorf("Error retrieving RECEIPT_HANDLE from queue. Cannot continue.")
	}

	var accounts []types.Account
	o = mm.Where(&accounts, "IsMasterAccount", aws.Bool(true))
	if o.Err != nil {
		ue := updateEnvironmentStatusesToApplyFailed(app, appEnvironmentsToApply, mm, o.Err)
		if ue != nil {
			return ue
		}
		return fmt.Errorf("an error occurred when trying to find the MasterAccount: %s", o.Err)
	}
	log.Debug().Str("AppID", payload.AppID).Msg("Found Master Account")

	cfg, err := config.LoadDefaultConfig(ctx, func(options *config.LoadOptions) error {
		config.WithRegion(accounts[0].AwsRegion)
		return nil
	})
	if err != nil {
		ue := updateEnvironmentStatusesToApplyFailed(app, appEnvironmentsToApply, mm, err)
		if ue != nil {
			return ue
		}
		return err
	}

	// get the doApiKey from secrets manager, not the payload
	doApiKey, err := utils.GetDoApiKeyFromSecretsManager(ctx, cfg, payload.UserName)
	if err != nil {
		ue := updateEnvironmentStatusesToApplyFailed(app, appEnvironmentsToApply, mm, err)
		if ue != nil {
			return ue
		}
		return err
	}

	authResponse, err := utils.IsApiKeyValid(*doApiKey)
	if err != nil {
		ue := updateEnvironmentStatusesToApplyFailed(app, appEnvironmentsToApply, mm, err)
		if ue != nil {
			return ue
		}
		return fmt.Errorf("error verifying validity of DragonOps Api Key: %v", err)
	}
	if !authResponse.IsValid {
		ue := updateEnvironmentStatusesToApplyFailed(app, appEnvironmentsToApply, mm, fmt.Errorf("the DragonOps api key provided is not valid. Please reach out to DragonOps support for help"))
		if ue != nil {
			return ue
		}
		return fmt.Errorf("The DragonOps api key provided is not valid. Please reach out to DragonOps support for help.")
	}

	sqsClient := sqs.NewFromConfig(cfg, func(o *sqs.Options) {
		o.Region = accounts[0].AwsRegion
	})

	if !isDryRun {
		if os.Getenv("IS_LOCAL") == "true" {
			os.Setenv("DRAGONOPS_TERRAFORM_ARTIFACT", "./app/tmpl.tgz.age")
		} else {
			os.Setenv("DRAGONOPS_TERRAFORM_ARTIFACT", "/app/tmpl.tgz.age")
		}

		log.Debug().Str("AppID", app.ID).Msg("Preparing Terraform")

		var execPath *string
		execPath, err = terraform.PrepareTerraform(ctx)
		if err != nil {
			ue := updateEnvironmentStatusesToApplyFailed(app, appEnvironmentsToApply, mm, err)
			if ue != nil {
				return ue
			}
			return err
		}

		log.Debug().Str("AppID", app.ID).Msg("Dry run is false. Running terraform")

		err = formatWithWorkerAndApply(ctx, accounts[0].AwsRegion, mm, app, appEnvironmentsToApply, execPath)
		if err != nil {
			ue := updateEnvironmentStatusesToApplyFailed(app, appEnvironmentsToApply, mm, err)
			if ue != nil {
				return ue
			}
			return err
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
		ue := updateEnvironmentStatusesToApplyFailed(app, appEnvironmentsToApply, mm, err)
		if ue != nil {
			return ue
		}
		return err
	}
	err = updateEnvironmentStatusesToApplied(app, appEnvironmentsToApply, mm)
	if err != nil {
		return err
	}

	// TODO github.com/aws/aws-sdk-go-v2/service/organizations --> to get the organization. if we don't have an organization.... i guess we can update the policy by getting it first, then adding the target account id to it.
	// so, 1. check for org. if exists, all good. set flag on master account saying IsOrganization
	// 2. if doesn't exist/not an org, set flag saying IsOrganization is false, see if target account is master account. If yes, just have policy say master account can pull. If NO, have policy saying master account & target account can pull
	// 3. every time we deploy to a new group, need to do this check if IsOrganization is false.
	return nil
}

func updateEnvironmentStatusesToApplyFailed(app types.App, environmentsToApply []types.Environment, mm *magicmodel.Operator, err error) error {
	for _, env := range environmentsToApply {
		for idx := range app.Environments {
			if app.Environments[idx].Environment == env.ResourceLabel && app.Environments[idx].Group == env.Group.ResourceLabel && app.Environments[idx].Status == "APPLYING" {
				//if env.ResourceLabel == k && appEnvConfig.Status == "APPLYING" {
				app.Environments[idx].Status = "APPLY_FAILED"
				app.Environments[idx].FailedReason = err.Error()
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

func updateEnvironmentStatusesToApplied(app types.App, environmentsToApply []types.Environment, mm *magicmodel.Operator) error {
	for _, env := range environmentsToApply {
		for idx := range app.Environments {
			if app.Environments[idx].Environment == env.ResourceLabel && app.Environments[idx].Group == env.Group.ResourceLabel && app.Environments[idx].Status == "APPLYING" {
				app.Environments[idx].Status = "APPLIED"
				app.Environments[idx].FailedReason = ""
			}
		}
	}
	aco := mm.Update(&app, "Environments", app.Environments)
	if aco.Err != nil {
		return aco.Err
	}
	return nil
}

func formatWithWorkerAndApply(ctx context.Context, masterAcctRegion string, mm *magicmodel.Operator, app types.App, environments []types.Environment, execPath *string) error {
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
			ue := updateEnvironmentStatusesToApplyFailed(app, environments, mm, err)
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

		var out map[string]tfexec.OutputMeta
		out, err = terraform.ApplyTerraform(ctx, fmt.Sprintf("%s/application", appPath), *execPath, roleToAssume)
		if err != nil {
			ue := updateEnvironmentStatusesToApplyFailed(app, environments, mm, err)
			if ue != nil {
				return ue
			}
			return fmt.Errorf("Error running apply with app with id %s and environment with id %s: %v", app.ID, env.ID, err)
		}

		log.Debug().Str("AppID", app.ID).Msg("Updating app status")

		for idx := range app.Environments {
			if app.Environments[idx].Environment == env.ResourceLabel && app.Environments[idx].Group == env.Group.ResourceLabel {
				app.Environments[idx].Status = "APPLIED"
				var appUrl AppUrl
				if err = json.Unmarshal(out["app_url"].Value, &appUrl); err != nil {
					fmt.Printf("Error decoding output value for key %s: %s\n", "app_url", err)
				}
				app.Environments[idx].Endpoint = string(appUrl)
				break
			}
		}
		o := mm.Save(&app)
		if o.Err != nil {
			return o.Err
		}
		log.Debug().Str("AppID", app.ID).Msg("App status updated")
	}
	return nil
}
