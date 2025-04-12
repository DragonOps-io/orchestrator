package lambda

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
		Str("LambdaID", payload.LambdaID).
		Msg("Attempting to destroy lambda environment")

	lambda := types.Lambda{}
	o := mm.Find(&lambda, payload.LambdaID)
	if o.Err != nil {
		log.Err(o.Err).Str("LambdaID", payload.LambdaID).Msg("Error finding app")
		return fmt.Errorf("an error occurred when trying to find the item with id %s: %s", payload.LambdaID, o.Err)
	}
	log.Debug().Str("LambdaID", lambda.ID).Msg("Found app")

	var appEnvironmentsToDestroy []types.Environment
	for _, envId := range payload.EnvironmentIDs {
		env := types.Environment{}
		o = mm.Find(&env, envId)
		if o.Err != nil {
			log.Err(o.Err).Str("LambdaID", payload.LambdaID).Msg(o.Err.Error())
			return o.Err
		}
		appEnvironmentsToDestroy = append(appEnvironmentsToDestroy, env)
	}
	log.Debug().Str("LambdaID", lambda.ID).Msg("Retrieved environments to destroy")

	receiptHandle := os.Getenv("RECEIPT_HANDLE")
	if receiptHandle == "" {
		ue := updateEnvironmentStatusesToDestroyFailed(lambda, appEnvironmentsToDestroy, mm, fmt.Errorf("no RECEIPT_HANDLE variable found").Error())
		if ue != nil {
			log.Err(ue).Str("LambdaID", payload.LambdaID).Msg(ue.Error())
			return ue
		}
		return fmt.Errorf("Error retrieving RECEIPT_HANDLE from queue. Cannot continue.")
	}

	var accounts []types.Account
	o = mm.Where(&accounts, "IsMasterAccount", aws.Bool(true))
	if o.Err != nil {
		log.Err(o.Err).Str("LambdaID", payload.LambdaID).Msg(o.Err.Error())
		o = mm.Update(&lambda, "Status", "DELETE_FAILED")
		if o.Err != nil {
			log.Err(o.Err).Str("LambdaID", payload.LambdaID).Msg(o.Err.Error())
			return o.Err
		}
		return fmt.Errorf("an error occurred when trying to find the Master Account: %s", o.Err)
	}

	log.Debug().Str("LambdaID", payload.LambdaID).Msg("Found Master Account")
	// get the doApiKey from secrets manager, not the payload

	cfg, err := config.LoadDefaultConfig(ctx, func(options *config.LoadOptions) error {
		config.WithRegion(accounts[0].AwsRegion)
		return nil
	})
	if err != nil {
		log.Err(err).Str("LambdaID", payload.LambdaID).Msg(err.Error())
		return err
	}

	doApiKey, err := utils.GetDoApiKeyFromSecretsManager(ctx, cfg, payload.UserName)
	if err != nil {
		log.Err(err).Str("LambdaID", payload.LambdaID).Msg(err.Error())
		o = mm.Update(&lambda, "Status", "DELETE_FAILED")
		if o.Err != nil {
			log.Err(o.Err).Str("LambdaID", payload.LambdaID).Msg(o.Err.Error())
			return o.Err
		}
		return fmt.Errorf("an error occurred when trying to find the Do Api Key: %s", o.Err)
	}
	authResponse, err := utils.IsApiKeyValid(*doApiKey)
	if err != nil {
		log.Err(err).Str("LambdaID", payload.LambdaID).Msg(err.Error())
		aco := mm.Update(&lambda, "Status", "APPLY_FAILED")
		if aco.Err != nil {
			log.Err(aco.Err).Str("LambdaID", payload.LambdaID).Msg(aco.Err.Error())
			return o.Err
		}
		return fmt.Errorf("error verifying validity of DragonOps Api Key: %v", err)
	}
	if !authResponse.IsValid {
		aco := mm.Update(&lambda, "Status", "APPLY_FAILED")
		if aco.Err != nil {
			log.Err(aco.Err).Str("LambdaID", payload.LambdaID).Msg(aco.Err.Error())
			return aco.Err
		}
		return fmt.Errorf("The DragonOps api key provided is not valid. Please reach out to DragonOps support for help.")
	}

	sqsClient := sqs.NewFromConfig(cfg, func(o *sqs.Options) {
		o.Region = accounts[0].AwsRegion
	})

	if !isDryRun {
		os.Setenv("DRAGONOPS_TERRAFORM_ARTIFACT", "/app/tmpl.tgz.age")

		log.Debug().Str("LambdaID", lambda.ID).Msg("Preparing Terraform")
		log.Debug().Str("LambdaID", lambda.ID).Msg(fmt.Sprintf("The artifact path is %s", os.Getenv("DRAGONOPS_TERRAFORM_ARTIFACT")))

		var execPath *string
		execPath, err = terraform.PrepareTerraform(ctx)
		if err != nil {
			log.Err(err).Str("LambdaID", payload.LambdaID).Msg(err.Error())
			ue := updateEnvironmentStatusesToDestroyFailed(lambda, appEnvironmentsToDestroy, mm, err.Error())
			if ue != nil {
				log.Err(ue).Str("LambdaID", payload.LambdaID).Msg(ue.Error())
				return ue
			}
			return err
		}

		log.Debug().Str("LambdaID", lambda.ID).Msg("Dry run is false. Running terraform")

		err = formatWithWorkerAndDestroy(ctx, accounts[0].AwsRegion, mm, lambda, appEnvironmentsToDestroy, execPath)
		if err != nil {
			log.Err(err).Str("LambdaID", payload.LambdaID).Msg(err.Error())
			ue := updateEnvironmentStatusesToDestroyFailed(lambda, appEnvironmentsToDestroy, mm, err.Error())
			if ue != nil {
				log.Err(ue).Str("LambdaID", payload.LambdaID).Msg(ue.Error())
				return ue
			}
			return err
		}
	} else {
		for _, env := range appEnvironmentsToDestroy {
			for idx := range lambda.Environments {
				if lambda.Environments[idx].Environment == env.ResourceLabel && lambda.Environments[idx].Group == env.Group.ResourceLabel {
					lambda.Environments[idx].Status = "DESTROYED"
					lambda.Environments[idx].Endpoint = ""
				}
			}
			o = mm.Save(&lambda)
			if o.Err != nil {
				log.Err(o.Err).Str("LambdaID", payload.LambdaID).Msg(o.Err.Error())
				return o.Err
			}
			log.Debug().Str("LambdaID", lambda.ID).Msg("Lambda environment status updated")
		}
	}

	queueParts := strings.Split(*lambda.AppSqsArn, ":")
	queueUrl := fmt.Sprintf("https://%s.%s.amazonaws.com/%s/%s", queueParts[2], queueParts[3], queueParts[4], queueParts[5])

	log.Debug().Str("LambdaID", lambda.ID).Msg(fmt.Sprintf("Queue url is %s", queueUrl))

	_, err = sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      &queueUrl,
		ReceiptHandle: &receiptHandle,
	})
	if err != nil {
		log.Err(err).Str("LambdaID", payload.LambdaID).Msg(err.Error())
		return err
	}

	return nil
}

func updateEnvironmentStatusesToDestroyFailed(lambda types.Lambda, environmentsToApply []types.Environment, mm *magicmodel.Operator, errMsg string) error {
	for _, env := range environmentsToApply {
		for idx := range lambda.Environments {
			if lambda.Environments[idx].Environment == env.ResourceLabel && lambda.Environments[idx].Group == env.Group.ResourceLabel && lambda.Environments[idx].Status == "DESTROYING" {
				lambda.Environments[idx].Status = "DESTROY_FAILED"
				lambda.Environments[idx].FailedReason = errMsg
			}
		}
	}
	aco := mm.Update(&lambda, "Environments", lambda.Environments)
	if aco.Err != nil {
		return aco.Err
	}
	return nil
}

func formatWithWorkerAndDestroy(ctx context.Context, masterAcctRegion string, mm *magicmodel.Operator, lambda types.Lambda, environments []types.Environment, execPath *string) error {
	log.Debug().Str("LambdaID", lambda.ID).Msg("Templating Terraform with correct values")

	for _, env := range environments {
		appPath := fmt.Sprintf("/lambdas/%s/%s", lambda.ID, env.ID)
		command := fmt.Sprintf("/app/worker lambda apply --lambda-id %s --environment-id %s --table-region %s", lambda.ID, env.ID, masterAcctRegion)
		os.Setenv("DRAGONOPS_TERRAFORM_DESTINATION", appPath)

		if os.Getenv("IS_LOCAL") == "true" {
			appPath = fmt.Sprintf("./lambdas/%s/%s", lambda.ID, env.ID)
			os.Setenv("DRAGONOPS_TERRAFORM_DESTINATION", appPath)
			command = fmt.Sprintf("./app/worker lambda apply --lambda-id %s --environment-id %s --table-region %s", lambda.ID, env.ID, masterAcctRegion)
		}

		log.Debug().Str("LambdaID", lambda.ID).Msg(appPath)
		log.Debug().Str("LambdaID", lambda.ID).Msg(fmt.Sprintf("Applying lambda files found at %s", os.Getenv("DRAGONOPS_TERRAFORM_DESTINATION")))

		log.Debug().Str("LambdaID", lambda.ID).Msg(fmt.Sprintf("Running command %s", command))
		msg, err := utils.RunOSCommandOrFail(command)
		if err != nil {
			ue := updateEnvironmentStatusesToDestroyFailed(lambda, environments, mm, err.Error())
			if ue != nil {
				return ue
			}
			return fmt.Errorf("Error running `worker lambda apply` with lambda with id %s and environment with id %s: %v", lambda.ID, env.ID, err)
		}
		log.Debug().Str("LambdaID", lambda.ID).Msg(*msg)

		var roleToAssume *string
		if env.Group.Account.CrossAccountRoleArn != nil {
			roleToAssume = env.Group.Account.CrossAccountRoleArn
		}

		_, err = terraform.DestroyTerraform(ctx, fmt.Sprintf("%s/lambda", appPath), *execPath, roleToAssume)
		if err != nil {
			ue := updateEnvironmentStatusesToDestroyFailed(lambda, environments, mm, err.Error())
			if ue != nil {
				return ue
			}
			return fmt.Errorf("Error running apply with lambda with id %s and environment with id %s: %v", lambda.ID, env.ID, err)
		}

		log.Debug().Str("LambdaID", lambda.ID).Msg("Updating lambda status")

		for idx := range lambda.Environments {
			if lambda.Environments[idx].Environment == env.ResourceLabel && lambda.Environments[idx].Group == env.Group.ResourceLabel {
				lambda.Environments[idx].Status = "DESTROYED"
				lambda.Environments[idx].Endpoint = ""
				break
			}
		}
		o := mm.Save(&lambda)
		if o.Err != nil {
			return o.Err
		}
		log.Debug().Str("LambdaID", lambda.ID).Msg("Lambda status updated")
		return nil
	}
	return nil
}
