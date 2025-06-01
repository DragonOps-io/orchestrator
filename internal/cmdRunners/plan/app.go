package plan

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

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

	appEnvironmentsToPlan := payload.EnvironmentNames

	log.Debug().Str("AppID", app.ID).Str("JobId", payload.JobId).Msg("Retrieved environments to plan")

	receiptHandle := os.Getenv("RECEIPT_HANDLE")
	if receiptHandle == "" {
		log.Err(o.Err).Str("AppID", app.ID).Str("JobId", payload.JobId).Msg("Error finding RECEIPT_HANDLE env var.")
		ue := utils.UpdateAllEnvironmentStatuses(app, appEnvironmentsToPlan, "APPLY_FAILED", mm, fmt.Errorf("no RECEIPT_HANDLE variable found").Error())
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
		ue := utils.UpdateAllEnvironmentStatuses(app, appEnvironmentsToPlan, "APPLY_FAILED", mm, o.Err.Error())
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
		ue := utils.UpdateAllEnvironmentStatuses(app, appEnvironmentsToPlan, "APPLY_FAILED", mm, err.Error())
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
		ue := utils.UpdateAllEnvironmentStatuses(app, appEnvironmentsToPlan, "APPLY_FAILED", mm, err.Error())
		if ue != nil {
			log.Err(o.Err).Str("AppID", app.ID).Str("JobId", payload.JobId).Msg(ue.Error())
			return ue
		}
		return err
	}

	authResponse, err := utils.IsApiKeyValid(*doApiKey)
	if err != nil {
		log.Err(o.Err).Str("AppID", payload.AppID).Str("JobId", payload.JobId).Msg(err.Error())
		ue := utils.UpdateAllEnvironmentStatuses(app, appEnvironmentsToPlan, "APPLY_FAILED", mm, err.Error())
		if ue != nil {
			log.Err(o.Err).Str("AppID", app.ID).Str("JobId", payload.JobId).Msg(ue.Error())
			return ue
		}
		return fmt.Errorf("error verifying validity of DragonOps Api Key: %v", err)
	}
	if !authResponse.IsValid {
		log.Err(o.Err).Str("AppID", app.ID).Str("JobId", payload.JobId).Msg("Invalid do api key provided.")
		ue := utils.UpdateAllEnvironmentStatuses(app, appEnvironmentsToPlan, "APPLY_FAILED", mm, fmt.Errorf("the DragonOps api key provided is not valid. Please reach out to DragonOps support for help").Error())
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
		ue := utils.UpdateAllEnvironmentStatuses(app, appEnvironmentsToPlan, "APPLY_FAILED", mm, err.Error())
		if ue != nil {
			log.Err(o.Err).Str("AppID", app.ID).Str("JobId", payload.JobId).Msg(ue.Error())
			return ue
		}
		return err
	}

	log.Debug().Str("AppID", app.ID).Str("JobId", payload.JobId).Msg("Dry run is false. Running terraform")

	err = formatWithWorkerAndPlanApp(ctx, accounts[0].AwsRegion, mm, app, appEnvironmentsToPlan, execPath, cfg, payload.PlanId, *accounts[0].StateBucketName, payload)
	if err != nil {
		log.Err(o.Err).Str("AppID", payload.AppID).Str("JobId", payload.JobId).Msg(err.Error())
		ue := utils.UpdateAllEnvironmentStatuses(app, appEnvironmentsToPlan, "APPLY_FAILED", mm, err.Error())
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

func formatWithWorkerAndPlanApp(ctx context.Context, masterAcctRegion string, mm *magicmodel.Operator, app types.App, environments []string, execPath *string, awsConfig aws.Config, planId string, stateBucketName string, payload Payload) error {
	log.Debug().Str("AppID", app.ID).Str("JobId", payload.JobId).Msg("Templating Terraform with correct values")

	wg := &sync.WaitGroup{}
	errors := make(chan error, 0)

	for _, env := range environments {
		wg.Add(1)

		go func(e string) {
			defer wg.Done()

			var roleToAssume *string
			// TODO how does cross-account work with this new env stuff?
			//if env.Group.Account.CrossAccountRoleArn != nil {
			//	roleToAssume = env.Group.Account.CrossAccountRoleArn
			//}

			appEnvPath := fmt.Sprintf("/apps/%s/%s", app.ID, env)

			err := utils.RunWorkerAppApply(mm, app, appEnvPath, env, masterAcctRegion)
			if err != nil {
				ue := utils.UpdateSingleEnvironmentStatus(app, env, "APPLY_FAILED", mm, err.Error())
				if ue != nil {
					errors <- fmt.Errorf("error updating status for env %s: %v", env, err)
					return
				}
				errors <- fmt.Errorf("error for env %s: %v", env, err)
				return
			}

			err = terraform.PlanAppTerraform(ctx, awsConfig, planId, stateBucketName, fmt.Sprintf("%s/application", appEnvPath), *execPath, roleToAssume)
			if err != nil {
				ue := utils.UpdateSingleEnvironmentStatus(app, env, "APPLY_FAILED", mm, err.Error())
				if ue != nil {
					errors <- fmt.Errorf("error updating status for env %s: %v", env, ue)
				}
				errors <- fmt.Errorf("error for env %s: %v", env, err)
				return
			}

			log.Debug().Str("AppID", app.ID).Str("JobId", payload.JobId).Msg("Updating app status")

			err = utils.UpdateSingleEnvironmentStatus(app, env, "APPLIED", mm, "")
			if err != nil {
				errors <- fmt.Errorf("error updating status for env %s: %v", env, err)
				return
			}

			log.Debug().Str("AppID", app.ID).Msg("App status updated")
			return
		}(env)
	}

	go func() {
		wg.Wait()
		close(errors)
	}()

	errs := make([]error, 0)
	for err := range errors {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		err := fmt.Errorf("errors occurred with applying environments for app %s: %v", app.ResourceLabel, errs)
		return err
	}
	return nil
}
