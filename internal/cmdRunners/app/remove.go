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
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/rs/zerolog/log"
	"os"
	"strings"
)

func Remove(ctx context.Context, payload Payload, mm *magicmodel.Operator, isDryRun bool) error {
	log.Debug().
		Str("AppID", payload.AppID).
		Msg("Beginning app remove")

	app := types.App{}
	o := mm.Find(&app, payload.AppID)
	if o.Err != nil {
		return fmt.Errorf("an error occurred when trying to find the item with id %s: %s", payload.AppID, o.Err)
	}
	log.Debug().Str("AppID", app.ID).Msg("Found app")

	appEnvironmentsToDestroy := payload.EnvironmentNames
	log.Debug().Str("AppID", app.ID).Msg("Retrieved environments to destroy")

	receiptHandle := os.Getenv("RECEIPT_HANDLE")
	if receiptHandle == "" {
		ue := utils.UpdateAllEnvironmentStatuses(app, appEnvironmentsToDestroy, "DESTROY_FAILED", mm, fmt.Errorf("no RECEIPT_HANDLE variable found").Error())
		if ue != nil {
			return ue
		}
		return fmt.Errorf("Error retrieving RECEIPT_HANDLE from queue. Cannot continue.")
	}

	var accounts []types.Account
	o = mm.Where(&accounts, "IsMasterAccount", aws.Bool(true))
	if o.Err != nil {
		ue := utils.UpdateAllEnvironmentStatuses(app, appEnvironmentsToDestroy, "DESTROY_FAILED", mm, o.Err.Error())
		if ue != nil {
			return ue
		}
		return fmt.Errorf("an error occurred when trying to find the Master Account: %s", o.Err)
	}

	log.Debug().Str("AppID", payload.AppID).Msg("Found Master Account")

	cfg, err := config.LoadDefaultConfig(ctx, func(options *config.LoadOptions) error {
		config.WithRegion(accounts[0].AwsRegion)
		return nil
	})
	if err != nil {
		ue := utils.UpdateAllEnvironmentStatuses(app, appEnvironmentsToDestroy, "DESTROY_FAILED", mm, err.Error())
		if ue != nil {
			return ue
		}
		return err
	}

	doApiKey, err := utils.GetDoApiKeyFromSecretsManager(ctx, cfg, payload.UserName)
	if err != nil {
		ue := utils.UpdateAllEnvironmentStatuses(app, appEnvironmentsToDestroy, "DESTROY_FAILED", mm, err.Error())
		if ue != nil {
			return ue
		}
		return fmt.Errorf("an error occurred when trying to find the Do Api Key: %s", o.Err)
	}

	authResponse, err := utils.IsApiKeyValid(*doApiKey)
	if err != nil {
		ue := utils.UpdateAllEnvironmentStatuses(app, appEnvironmentsToDestroy, "DESTROY_FAILED", mm, err.Error())
		if ue != nil {
			return ue
		}
		return fmt.Errorf("error verifying validity of DragonOps Api Key: %v", err)
	}
	if !authResponse.IsValid {
		ue := utils.UpdateAllEnvironmentStatuses(app, appEnvironmentsToDestroy, "DESTROY_FAILED", mm, "The DragonOps api key provided is not valid. Please reach out to DragonOps support for help.")
		if ue != nil {
			return ue
		}
		return fmt.Errorf("The DragonOps api key provided is not valid. Please reach out to DragonOps support for help.")
	}

	sqsClient := sqs.NewFromConfig(cfg, func(o *sqs.Options) {
		o.Region = accounts[0].AwsRegion
	})

	if !isDryRun {
		os.Setenv("DRAGONOPS_TERRAFORM_ARTIFACT", "/app/tmpl.tgz.age")

		log.Debug().Str("AppID", app.ID).Msg("Preparing Terraform")

		var execPath *string
		execPath, err = terraform.PrepareTerraform(ctx)
		if err != nil {
			ue := utils.UpdateAllEnvironmentStatuses(app, appEnvironmentsToDestroy, "DESTROY_FAILED", mm, err.Error())
			if ue != nil {
				return ue
			}
			return err
		}

		log.Debug().Str("AppID", app.ID).Msg("Dry run is false. Running terraform")

		err = formatWithWorkerAndDestroy(ctx, accounts[0].AwsRegion, mm, app, appEnvironmentsToDestroy, execPath)
		if err != nil {
			return err
		}
	} else {
		err = utils.UpdateAllEnvironmentStatuses(app, appEnvironmentsToDestroy, "DESTROYED", mm, "")
		if err != nil {
			return fmt.Errorf("error updating environment statuses to destroyed: %v", err)
		}
		log.Debug().Str("AppID", app.ID).Msg("App environment status updated")
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
	// TODO OIDC repo entry from policy

	ecrClient := ecr.NewFromConfig(cfg)

	err = DeleteECRRepository(ctx, ecrClient, app.Name, true)
	if err != nil {
		return err
	}

	o = mm.SoftDelete(&app)
	if o.Err != nil {
		return o.Err
	}
	return nil
}

func DeleteECRRepository(ctx context.Context, client *ecr.Client, repoName string, force bool) error {
	_, err := client.DeleteRepository(ctx, &ecr.DeleteRepositoryInput{
		RepositoryName: aws.String(repoName),
		Force:          force, // true allows deletion even if images exist
	})
	if err != nil {
		return fmt.Errorf("failed to delete ECR repo %q: %w", repoName, err)
	}
	return nil
}
