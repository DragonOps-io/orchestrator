package app

import (
	"context"
	"fmt"
	"github.com/DragonOps-io/orchestrator/internal/terraform"
	"github.com/DragonOps-io/orchestrator/internal/utils"
	"github.com/DragonOps-io/types"
	magicmodel "github.com/Ilios-LLC/magicmodel-go/model"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/rs/zerolog/log"
	"os"
	"strings"
	"sync"
)

func Destroy(ctx context.Context, payload Payload, mm *magicmodel.Operator, isDryRun bool) error {
	log.Info().
		Str("AppID", payload.AppID).
		Msg("Retrieving app to destroy...")

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
		log.Info().Str("AppID", app.ID).Msg("Preparing Terraform")

		var execPath *string
		execPath, err = terraform.PrepareTerraform(ctx)
		if err != nil {
			ue := utils.UpdateAllEnvironmentStatuses(app, appEnvironmentsToDestroy, "DESTROY_FAILED", mm, o.Err.Error())
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

	return nil
}

func formatWithWorkerAndDestroy(ctx context.Context, masterAcctRegion string, mm *magicmodel.Operator, app types.App, environments []string, execPath *string) error {
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
				ue := utils.UpdateSingleEnvironmentStatus(app, env, "DESTROY_FAILED", mm, err.Error())
				if ue != nil {
					errors <- fmt.Errorf("error updating status for env %s: %v", env, err)
					return
				}
				errors <- fmt.Errorf("error for env %s: %v", env, err)
				return
			}

			_, err = terraform.DestroyTerraform(ctx, fmt.Sprintf("%s/application", appEnvPath), *execPath, roleToAssume)
			if err != nil {
				ue := utils.UpdateSingleEnvironmentStatus(app, env, "DESTROY_FAILED", mm, err.Error())
				if ue != nil {
					errors <- fmt.Errorf("error updating status for env %s: %v", env, ue)
				}
				errors <- fmt.Errorf("error for env %s: %v", env, err)
				return
			}

			log.Info().Str("AppID", app.ID).Msg("Terraform applied! Saving outputs...")

			err = utils.UpdateSingleEnvironmentStatus(app, env, "DESTROYED", mm, "")
			if err != nil {
				errors <- fmt.Errorf("error updating status for env %s: %v", env, err)
				return
			}
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
		err := fmt.Errorf("errors occurred with destroying environments for app %s: %v", app.ResourceLabel, errs)
		return err
	}
	return nil
}
