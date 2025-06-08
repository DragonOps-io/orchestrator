package app

import (
	"context"
	"fmt"
	"github.com/DragonOps-io/orchestrator/internal/terraform"
	"github.com/DragonOps-io/orchestrator/internal/utils"
	"github.com/DragonOps-io/types"
	magicmodel "github.com/Ilios-LLC/magicmodel-go/model"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/rs/zerolog/log"
	"os"
	"strings"
)

func Remove(ctx context.Context, payload Payload, mm *magicmodel.Operator, isDryRun bool) error {
	log.Info().
		Str("AppID", payload.AppID).
		Msg("Retrieving app to remove...")

	app := types.App{}
	o := mm.Find(&app, payload.AppID)
	if o.Err != nil {
		return fmt.Errorf("an error occurred when trying to find the item with id %s: %s", payload.AppID, o.Err)
	}

	appEnvironmentsToDestroy := payload.EnvironmentNames

	masterAccount, cfg, err := utils.CommonStartupTasks(ctx, mm, payload.UserName)
	if err != nil {
		ue := utils.UpdateAllEnvironmentStatuses(app, appEnvironmentsToDestroy, "DESTROY_FAILED", mm, err.Error())
		if ue != nil {
			return ue
		}
		return fmt.Errorf("error during common startup tasks: %v", err)
	}

	if !isDryRun {
		if os.Getenv("IS_LOCAL") == "true" {
			os.Setenv("DRAGONOPS_TERRAFORM_ARTIFACT", "./app/tmpl.tgz.age")
		} else {
			os.Setenv("DRAGONOPS_TERRAFORM_ARTIFACT", "/app/tmpl.tgz.age")
		}

		var execPath *string
		execPath, err = terraform.PrepareTerraform(ctx)
		if err != nil {
			ue := utils.UpdateAllEnvironmentStatuses(app, appEnvironmentsToDestroy, "DESTROY_FAILED", mm, err.Error())
			if ue != nil {
				return ue
			}
			return err
		}

		log.Info().Str("AppID", app.ID).Msg("Running terraform...")

		err = formatWithWorkerAndDestroy(ctx, masterAccount.AwsRegion, mm, app, appEnvironmentsToDestroy, execPath)
		if err != nil {
			return err
		}
	} else {
		err = utils.UpdateAllEnvironmentStatuses(app, appEnvironmentsToDestroy, "DESTROYED", mm, "")
		if err != nil {
			return fmt.Errorf("error updating environment statuses to destroyed: %v", err)
		}
	}

	queueParts := strings.Split(*app.AppSqsArn, ":")
	queueUrl := fmt.Sprintf("https://%s.%s.amazonaws.com/%s/%s", queueParts[2], queueParts[3], queueParts[4], queueParts[5])

	log.Info().Str("AppID", app.ID).Msg("App updated")

	sqsClient := sqs.NewFromConfig(*cfg, func(o *sqs.Options) {
		o.Region = masterAccount.AwsRegion
	})
	receiptHandle := os.Getenv("RECEIPT_HANDLE")
	_, err = sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      &queueUrl,
		ReceiptHandle: &receiptHandle,
	})
	if err != nil {
		return err
	}

	// TODO Delete OIDC repo entry from policy

	// Need to have a flag for this or something.
	//ecrClient := ecr.NewFromConfig(*cfg)
	//err = DeleteECRRepository(ctx, ecrClient, app.Name, true)
	//if err != nil {
	//	return err
	//}

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
