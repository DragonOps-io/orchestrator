package plan

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/DragonOps-io/orchestrator/internal/terraform"
	"github.com/DragonOps-io/orchestrator/internal/utils"
	"github.com/DragonOps-io/types"
	magicmodel "github.com/Ilios-LLC/magicmodel-go/model"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/rs/zerolog/log"
)

func AppPlan(ctx context.Context, payload Payload, mm *magicmodel.Operator) error {
	log.Debug().
		Str("AppID", payload.AppID).
		Str("JobId", payload.JobId).
		Msg("Looking for app with matching ID")

	app := types.App{}
	o := mm.Find(&app, payload.AppID)
	if o.Err != nil {
		log.Err(o.Err).Str("AppID", payload.AppID).Str("JobId", payload.JobId).Msg("Error finding app")
		return fmt.Errorf("an error occurred when trying to find the app with id %s: %s", payload.AppID, o.Err)
	}
	log.Debug().Str("AppID", app.ID).Str("JobId", payload.JobId).Msg("Found app")

	var appEnvironmentsToPlan []types.Environment
	for _, envId := range payload.EnvironmentIDs {
		env := types.Environment{}
		o = mm.Find(&env, envId)
		if o.Err != nil {
			log.Err(o.Err).Str("AppID", app.ID).Str("JobId", payload.JobId).Str("EnvironmentID", envId).Msg("Error finding environment")
			return o.Err
		}
		appEnvironmentsToPlan = append(appEnvironmentsToPlan, env)
	}
	log.Debug().Str("AppID", app.ID).Str("JobId", payload.JobId).Msg("Retrieved environments to plan")

	receiptHandle := os.Getenv("RECEIPT_HANDLE")
	if receiptHandle == "" {
		log.Err(o.Err).Str("AppID", app.ID).Str("JobId", payload.JobId).Msg("Error finding RECEIPT_HANDLE env var.")
		ue := updateEnvironmentStatusesToApplyFailed(app, appEnvironmentsToPlan, mm, fmt.Errorf("no RECEIPT_HANDLE variable found"))
		if ue != nil {
			log.Err(o.Err).Str("AppID", app.ID).Str("JobId", payload.JobId).Msg(ue.Error())
			return ue
		}
		return fmt.Errorf("Error retrieving RECEIPT_HANDLE from queue. Cannot continue.")
	}

	var accounts []types.Account
	o = mm.Where(&accounts, "IsMasterAccount", aws.Bool(true))
	if o.Err != nil {
		log.Err(o.Err).Str("AppID", payload.AppID).Str("JobId", payload.JobId).Msg(o.Err.Error())
		ue := updateEnvironmentStatusesToApplyFailed(app, appEnvironmentsToPlan, mm, o.Err)
		if ue != nil {
			log.Err(o.Err).Str("AppID", app.ID).Str("JobId", payload.JobId).Msg(ue.Error())
			return ue
		}
		return fmt.Errorf("an error occurred when trying to find the MasterAccount: %s", o.Err)
	}
	log.Debug().Str("AppID", payload.AppID).Str("JobId", payload.JobId).Msg("Found Master Account")

	cfg, err := config.LoadDefaultConfig(ctx, func(options *config.LoadOptions) error {
		config.WithRegion(accounts[0].AwsRegion)
		return nil
	})
	if err != nil {
		log.Err(o.Err).Str("AppID", payload.AppID).Str("JobId", payload.JobId).Msg(err.Error())
		ue := updateEnvironmentStatusesToApplyFailed(app, appEnvironmentsToPlan, mm, err)
		if ue != nil {
			log.Err(o.Err).Str("AppID", app.ID).Str("JobId", payload.JobId).Msg(ue.Error())
			return ue
		}
		return err
	}

	// get the doApiKey from secrets manager, not the payload
	doApiKey, err := utils.GetDoApiKeyFromSecretsManager(ctx, cfg, payload.UserName)
	if err != nil {
		log.Err(o.Err).Str("AppID", payload.AppID).Str("JobId", payload.JobId).Msg(err.Error())
		ue := updateEnvironmentStatusesToApplyFailed(app, appEnvironmentsToPlan, mm, err)
		if ue != nil {
			log.Err(o.Err).Str("AppID", app.ID).Str("JobId", payload.JobId).Msg(ue.Error())
			return ue
		}
		return err
	}

	authResponse, err := utils.IsApiKeyValid(*doApiKey)
	if err != nil {
		log.Err(o.Err).Str("AppID", payload.AppID).Str("JobId", payload.JobId).Msg(err.Error())
		ue := updateEnvironmentStatusesToApplyFailed(app, appEnvironmentsToPlan, mm, err)
		if ue != nil {
			log.Err(o.Err).Str("AppID", app.ID).Str("JobId", payload.JobId).Msg(ue.Error())
			return ue
		}
		return fmt.Errorf("error verifying validity of DragonOps Api Key: %v", err)
	}
	if !authResponse.IsValid {
		log.Err(o.Err).Str("AppID", app.ID).Str("JobId", payload.JobId).Msg("Invalid do api key provided.")
		ue := updateEnvironmentStatusesToApplyFailed(app, appEnvironmentsToPlan, mm, fmt.Errorf("the DragonOps api key provided is not valid. Please reach out to DragonOps support for help"))
		if ue != nil {
			log.Err(o.Err).Str("AppID", app.ID).Str("JobId", payload.JobId).Msg(ue.Error())
			return ue
		}
		return fmt.Errorf("The DragonOps api key provided is not valid. Please reach out to DragonOps support for help.")
	}

	sqsClient := sqs.NewFromConfig(cfg, func(o *sqs.Options) {
		o.Region = accounts[0].AwsRegion
	})

	if os.Getenv("IS_LOCAL") == "true" {
		os.Setenv("DRAGONOPS_TERRAFORM_ARTIFACT", "./app/tmpl.tgz.age")
	} else {
		os.Setenv("DRAGONOPS_TERRAFORM_ARTIFACT", "/app/tmpl.tgz.age")
	}

	log.Debug().Str("AppID", app.ID).Str("JobId", payload.JobId).Msg("Preparing Terraform")

	var execPath *string
	execPath, err = terraform.PrepareTerraform(ctx)
	if err != nil {
		log.Err(o.Err).Str("AppID", payload.AppID).Str("JobId", payload.JobId).Msg(err.Error())
		ue := updateEnvironmentStatusesToApplyFailed(app, appEnvironmentsToPlan, mm, err)
		if ue != nil {
			log.Err(o.Err).Str("AppID", app.ID).Str("JobId", payload.JobId).Msg(ue.Error())
			return ue
		}
		return err
	}

	log.Debug().Str("AppID", app.ID).Str("JobId", payload.JobId).Msg("Dry run is false. Running terraform")

	err = formatWithWorkerAndApply(ctx, accounts[0].AwsRegion, mm, app, appEnvironmentsToPlan, execPath, cfg, payload.PlanId, *accounts[0].StateBucketName, payload)
	if err != nil {
		log.Err(o.Err).Str("AppID", payload.AppID).Str("JobId", payload.JobId).Msg(err.Error())
		ue := updateEnvironmentStatusesToApplyFailed(app, appEnvironmentsToPlan, mm, err)
		if ue != nil {
			log.Err(o.Err).Str("AppID", app.ID).Str("JobId", payload.JobId).Msg(ue.Error())
			return ue
		}
		return err
	}

	queueParts := strings.Split(*app.AppSqsArn, ":")
	queueUrl := fmt.Sprintf("https://%s.%s.amazonaws.com/%s/%s", queueParts[2], queueParts[3], queueParts[4], queueParts[5])

	log.Debug().Str("AppID", app.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Queue url is %s", queueUrl))

	_, err = sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      &queueUrl,
		ReceiptHandle: &receiptHandle,
	})
	if err != nil {
		log.Err(err).Str("AppID", app.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Error deleting message from queue: %s", err.Error()))
		return err
	}

	log.Info().Str("AppID", app.ID).Msg("Successfully planned app!")
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
				app.Environments[idx].Status = "APPLY_FAILED"
				app.Environments[idx].FailedReason = err.Error()
			}
		}
	}
	aco := mm.Update(&app, "Environments", app.Environments)
	if aco.Err != nil {
		return aco.Err
	}
	return nil
}

func formatWithWorkerAndApply(ctx context.Context, masterAcctRegion string, mm *magicmodel.Operator, app types.App, environments []types.Environment, execPath *string, awsConfig aws.Config, planId string, stateBucketName string, payload Payload) error {
	log.Debug().Str("AppID", app.ID).Str("JobId", payload.JobId).Msg("Templating Terraform with correct values")

	for _, env := range environments {
		appPath := fmt.Sprintf("/apps/%s/%s", app.ID, env.ID)
		command := fmt.Sprintf("/app/worker app apply --app-id %s --environment-id %s --table-region %s", app.ID, env.ID, masterAcctRegion)
		os.Setenv("DRAGONOPS_TERRAFORM_DESTINATION", appPath)

		if os.Getenv("IS_LOCAL") == "true" {
			appPath = fmt.Sprintf("./apps/%s/%s", app.ID, env.ID)
			os.Setenv("DRAGONOPS_TERRAFORM_DESTINATION", appPath)
			command = fmt.Sprintf("./app/worker app apply --app-id %s --environment-id %s --table-region %s", app.ID, env.ID, masterAcctRegion)
		}

		log.Debug().Str("AppID", app.ID).Str("JobId", payload.JobId).Msg(appPath)
		log.Debug().Str("AppID", app.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Applying application files found at %s", os.Getenv("DRAGONOPS_TERRAFORM_DESTINATION")))

		log.Debug().Str("AppID", app.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Running command %s", command))
		msg, err := utils.RunOSCommandOrFail(command)
		if err != nil {
			ue := updateEnvironmentStatusesToApplyFailed(app, environments, mm, err)
			if ue != nil {
				return ue
			}
			return fmt.Errorf("Error running `worker app apply` with app with id %s and environment with id %s: %v", app.ID, env.ID, err)
		}
		log.Debug().Str("AppID", app.ID).Str("JobId", payload.JobId).Msg(*msg)

		var roleToAssume *string
		if env.Group.Account.CrossAccountRoleArn != nil {
			roleToAssume = env.Group.Account.CrossAccountRoleArn
		}

		err = terraform.PlanAppTerraform(ctx, awsConfig, planId, stateBucketName, fmt.Sprintf("%s/application", appPath), *execPath, roleToAssume)
		if err != nil {
			ue := updateEnvironmentStatusesToApplyFailed(app, environments, mm, err)
			if ue != nil {
				return ue
			}
			return fmt.Errorf("Error running plan with app with id %s and environment with id %s: %v", app.ID, env.ID, err)
		}

		log.Debug().Str("AppID", app.ID).Str("JobId", payload.JobId).Msg("Updating app status")

		for idx := range app.Environments {
			if app.Environments[idx].Environment == env.ResourceLabel && app.Environments[idx].Group == env.Group.ResourceLabel {
				app.Environments[idx].Status = "APPLIED"
				break
			}
		}
		o := mm.Save(&app)
		if o.Err != nil {
			return o.Err
		}
		log.Debug().Str("AppID", app.ID).Str("JobId", payload.JobId).Msg("App status updated")
	}
	return nil
}
