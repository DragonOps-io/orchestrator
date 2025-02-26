package observability

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
	"path/filepath"
	"strings"
)

func Destroy(ctx context.Context, payload Payload, mm *magicmodel.Operator, isDryRun bool) error {
	log.Debug().
		Str("JobId", payload.JobId).
		Msg("Attempting to destroy observability stack.")
	accounts := []types.Account{}
	o := mm.WhereV2(false, &accounts, "IsMasterAccount", aws.Bool(true))
	if o.Err != nil {
		log.Err(o.Err).Str("JobId", payload.JobId).Msg("Error finding MasterAccount")
		return fmt.Errorf("Error when trying to retrieve master account: %s", o.Err)
	}

	if len(accounts) == 0 {
		log.Debug().Str("JobId", payload.JobId).Msg("No Master Account found")
		return fmt.Errorf("No Master Account found")
	}

	log.Debug().Str("JobId", payload.JobId).Msg("Found Master Account")
	masterAccount := accounts[0]

	receiptHandle := os.Getenv("RECEIPT_HANDLE")
	if receiptHandle == "" {
		//log.Err(fmt.Errorf("no RECEIPT_HANDLE variable found")).Str("JobId", payload.JobId).Msg("Error retrieving RECEIPT_HANDLE from queue. Cannot continue.")
		masterAccount.Observability.Status = "DESTROY_FAILED"
		masterAccount.Observability.FailedReason = fmt.Errorf("no RECEIPT_HANDLE variable found").Error()
		o = mm.Save(&masterAccount)
		if o.Err != nil {
			//log.Err(o.Err).Str("JobId", payload.JobId).Msg(o.Err.Error())
			return o.Err
		}
		return fmt.Errorf("Error retrieving RECEIPT_HANDLE from queue. Cannot continue.")
	}

	cfg, err := config.LoadDefaultConfig(ctx, func(options *config.LoadOptions) error {
		config.WithRegion(accounts[0].AwsRegion)
		return nil
	})
	if err != nil {
		o = mm.Save(&masterAccount)
		if o.Err != nil {
			log.Err(o.Err).Str("JobId", payload.JobId).Msg(o.Err.Error())
			return o.Err
		}
		return err
	}

	sqsClient := sqs.NewFromConfig(cfg, func(o *sqs.Options) {
		o.Region = masterAccount.AwsRegion
	})

	// get the doApiKey from secrets manager, not the payload
	doApiKey, err := utils.GetDoApiKeyFromSecretsManager(ctx, cfg, payload.UserName)
	if err != nil {
		log.Err(err).Str("JobId", payload.JobId).Msg(err.Error())
		o = mm.Save(&masterAccount)
		if o.Err != nil {
			log.Err(o.Err).Str("JobId", payload.JobId).Msg(o.Err.Error())
			return o.Err
		}
		return err
	}

	authResponse, err := utils.IsApiKeyValid(*doApiKey)
	if err != nil {
		log.Err(err).Str("JobId", payload.JobId).Msg(err.Error())
		o = mm.Save(&masterAccount)
		if o.Err != nil {
			log.Err(o.Err).Str("JobId", payload.JobId).Msg(o.Err.Error())
			return o.Err
		}
		return fmt.Errorf("error verifying validity of DragonOps Api Key: %v", err)
	}

	if !authResponse.IsValid {
		log.Err(fmt.Errorf("The DragonOps api key provided is not valid. Please reach out to DragonOps support for help.")).Msg("The DragonOps api key provided is not valid. Please reach out to DragonOps support for help.")
		o = mm.Save(&masterAccount)
		if o.Err != nil {
			log.Err(o.Err).Str("JobId", payload.JobId).Msg(o.Err.Error())
			return o.Err
		}
		return fmt.Errorf("The DragonOps api key provided is not valid. Please reach out to DragonOps support for help.")
	}
	o = mm.Save(&masterAccount)
	if o.Err != nil {
		log.Err(o.Err).Str("JobId", payload.JobId).Msg(o.Err.Error())
		return o.Err
	}

	if !isDryRun {
		if os.Getenv("IS_LOCAL") == "true" {
			os.Setenv("DRAGONOPS_TERRAFORM_DESTINATION", fmt.Sprintf("./observability/%s", masterAccount.ID))
			os.Setenv("DRAGONOPS_TERRAFORM_ARTIFACT", "./app/tmpl.tgz.age")
		} else {
			os.Setenv("DRAGONOPS_TERRAFORM_ARTIFACT", "/app/tmpl.tgz.age")
			os.Setenv("DRAGONOPS_TERRAFORM_DESTINATION", fmt.Sprintf("/observability/%s", masterAccount.ID))
		}

		var execPath *string
		log.Debug().Str("JobId", payload.JobId).Msg("Preparing Terraform")
		execPath, err = terraform.PrepareTerraform(ctx)
		if err != nil {
			log.Err(err).Str("JobId", payload.JobId).Msg(err.Error())
			masterAccount.Observability.Status = "DESTROY_FAILED"
			masterAccount.Observability.FailedReason = err.Error()
			o = mm.Save(&masterAccount)
			if o.Err != nil {
				log.Err(o.Err).Str("JobId", payload.JobId).Msg(o.Err.Error())
				return o.Err
			}
			return err
		}

		log.Debug().Str("JobId", payload.JobId).Msg(fmt.Sprintf("Region for magic model is: %s", masterAccount.AwsRegion))
		err = formatWithWorkerAndDestroy(ctx, masterAccount.AwsRegion, mm, masterAccount, execPath, cfg, payload)
		if err != nil {
			log.Err(err).Str("JobId", payload.JobId).Msg(err.Error())
			masterAccount.Observability.Status = "DESTROY_FAILED"
			masterAccount.Observability.FailedReason = err.Error()
			o = mm.Save(&masterAccount)
			if o.Err != nil {
				log.Err(o.Err).Str("JobId", payload.JobId).Msg(o.Err.Error())
				return o.Err
			}
			return err
		}
	}

	log.Debug().Str("JobId", payload.JobId).Msg("Updating observability status")

	masterAccount.Observability.Enabled = false
	masterAccount.Observability.Status = "DESTROYED"
	masterAccount.Observability.FailedReason = ""
	o = mm.Save(&masterAccount)
	if o.Err != nil {
		log.Err(o.Err).Str("JobId", payload.JobId).Msg(o.Err.Error())
		return o.Err
	}

	log.Debug().Str("JobId", payload.JobId).Msg("Finished destroying observability stack!")
	queueParts := strings.Split(*masterAccount.GroupSqsArn, ":") // TODO might lead to nil pointer deference
	queueUrl := fmt.Sprintf("https://%s.%s.amazonaws.com/%s/%s", queueParts[2], queueParts[3], queueParts[4], queueParts[5])

	log.Debug().Str("JobId", payload.JobId).Msg(fmt.Sprintf("Queue url is %s", queueUrl))

	_, err = sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      &queueUrl,
		ReceiptHandle: &receiptHandle,
	})
	if err != nil {
		log.Err(err).Str("JobId", payload.JobId).Msg(err.Error())
		return err
	}
	return nil
}

func formatWithWorkerAndDestroy(ctx context.Context, masterAcctRegion string, mm *magicmodel.Operator, account types.Account, execPath *string, cfg aws.Config, payload Payload) error {
	log.Debug().Str("JobId", payload.JobId).Msg("Templating Terraform with correct values")

	command := fmt.Sprintf("/app/worker observability apply --table-region %s", masterAcctRegion)
	if os.Getenv("IS_LOCAL") == "true" {
		command = fmt.Sprintf("./app/worker observability apply --table-region %s", masterAcctRegion)
	}

	log.Debug().Str("JobId", payload.JobId).Msg(fmt.Sprintf("Running command %s", command))
	msg, err := utils.RunOSCommandOrFail(command)
	if err != nil {
		// avoid nil pointer deference error
		strMsg := ""
		if msg != nil {
			strMsg = *msg
		}
		log.Err(err).Str("JobId", payload.JobId).Msg(strMsg)
		return fmt.Errorf("Error running `worker observability apply` for account: %s: %s", err, strMsg)
	}

	err = destroy(ctx, cfg, mm, execPath, nil, account.AwsAccountId, payload)
	if err != nil {
		log.Err(err).Str("JobId", payload.JobId).Msg(err.Error())
		return fmt.Errorf("Error running destroy for observability stack %s", err)
	}

	return nil
}

func destroy(ctx context.Context, cfg aws.Config, mm *magicmodel.Operator, execPath *string, roleToAssume *string, dirName string, payload Payload) error {
	path, _ := filepath.Abs(fmt.Sprintf("%s/%s", os.Getenv("DRAGONOPS_TERRAFORM_DESTINATION"), dirName))

	log.Debug().Str("JobId", payload.JobId).Msg(path)
	_, err := terraform.DestroyTerraform(ctx, path, *execPath, roleToAssume)
	if err != nil {
		log.Err(err).Str("JobId", payload.JobId).Msg(err.Error())
		return err
	}

	// unset the outputs
	var orchestratorNetwork []types.Network
	o := mm.WhereV2(false, &orchestratorNetwork, "Name", aws.String("dragonops-orchestrator"))
	if o.Err != nil {
		log.Err(o.Err).Str("JobId", payload.JobId).Msg(o.Err.Error())
		return o.Err
	}
	orchestratorNetwork[0].WireguardPublicIP = ""
	orchestratorNetwork[0].WireguardInstanceID = ""
	orchestratorNetwork[0].WireguardPrivateKey = ""
	orchestratorNetwork[0].WireguardPublicKey = ""

	o = mm.Save(&orchestratorNetwork[0])
	if o.Err != nil {
		log.Err(o.Err).Str("JobId", payload.JobId).Msg(o.Err.Error())
		return o.Err
	}

	err = types.DeleteServerConfigFileParameter(ctx, orchestratorNetwork[0].ID, cfg)
	if err != nil {
		return fmt.Errorf("error deleting network config file: %s", err.Error())
	}
	return nil
}
