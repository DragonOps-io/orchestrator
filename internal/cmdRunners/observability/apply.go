package observability

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
	"path/filepath"
	"strings"
)

func Apply(ctx context.Context, payload Payload, mm *magicmodel.Operator, isDryRun bool) error {
	log.Debug().
		Str("JobID", payload.JobId).
		Msg("Getting Master Account")

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
		log.Err(fmt.Errorf("no RECEIPT_HANDLE variable found")).Str("JobId", payload.JobId).Msg("Error retrieving RECEIPT_HANDLE from queue. Cannot continue.")
		masterAccount.Observability.Status = "APPLY_FAILED"
		masterAccount.Observability.FailedReason = fmt.Errorf("no RECEIPT_HANDLE variable found").Error()
		o = mm.Save(&masterAccount)
		if o.Err != nil {
			log.Err(o.Err).Str("JobId", payload.JobId).Msg(o.Err.Error())
			return o.Err
		}
		return fmt.Errorf("Error retrieving RECEIPT_HANDLE from queue. Cannot continue.")
	}

	cfg, err := config.LoadDefaultConfig(ctx, func(options *config.LoadOptions) error {
		config.WithRegion(masterAccount.AwsRegion)
		return nil
	})
	if err != nil {
		masterAccount.Observability.Status = "APPLY_FAILED"
		masterAccount.Observability.FailedReason = err.Error()
		o = mm.Save(&masterAccount)
		if o.Err != nil {
			log.Err(o.Err).Str("JobId", payload.JobId).Msg(o.Err.Error())
			return o.Err
		}
		return err
	}

	// get the doApiKey from secrets manager, not the payload
	doApiKey, err := utils.GetDoApiKeyFromSecretsManager(ctx, cfg, payload.UserName)
	if err != nil {
		log.Err(err).Str("JobId", payload.JobId).Msg(err.Error())
		masterAccount.Observability.Status = "APPLY_FAILED"
		masterAccount.Observability.FailedReason = err.Error()
		o = mm.Save(&masterAccount)
		if o.Err != nil {
			log.Err(o.Err).Str("JobId", payload.JobId).Msg(o.Err.Error())
			return o.Err
		}
		return err
	}

	authResponse, err := utils.IsApiKeyValid(*doApiKey)
	if err != nil {
		masterAccount.Observability.Status = "APPLY_FAILED"
		masterAccount.Observability.FailedReason = err.Error()
		o = mm.Save(&masterAccount)
		if o.Err != nil {
			log.Err(o.Err).Str("JobId", payload.JobId).Msg(o.Err.Error())
			return o.Err
		}
		return fmt.Errorf("error verifying validity of DragonOps Api Key: %v", err)
	}

	if !authResponse.IsValid {
		log.Err(fmt.Errorf("The DragonOps api key provided is not valid. Please reach out to DragonOps support for help.")).Str("JobId", payload.JobId).Msg("The DragonOps api key provided is not valid. Please reach out to DragonOps support for help.")
		masterAccount.Observability.Status = "APPLY_FAILED"
		masterAccount.Observability.FailedReason = fmt.Errorf("The DragonOps api key provided is not valid. Please reach out to DragonOps support for help.").Error()
		o = mm.Save(&masterAccount)
		if o.Err != nil {
			log.Err(o.Err).Str("JobId", payload.JobId).Msg(o.Err.Error())
			return o.Err
		}
		return fmt.Errorf("The DragonOps api key provided is not valid. Please reach out to DragonOps support for help.")
	}

	sqsClient := sqs.NewFromConfig(cfg, func(o *sqs.Options) {
		o.Region = masterAccount.AwsRegion
	})

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
			masterAccount.Observability.Status = "APPLY_FAILED"
			masterAccount.Observability.FailedReason = err.Error()
			o = mm.Save(&masterAccount)
			if o.Err != nil {
				log.Err(o.Err).Str("JobId", payload.JobId).Msg(o.Err.Error())
				return o.Err
			}
			return err
		}

		log.Debug().Str("JobId", payload.JobId).Msg(fmt.Sprintf("Region for magic model is: %s", masterAccount.AwsRegion))
		updatedAccount, err := formatWithWorkerAndApply(ctx, masterAccount.AwsRegion, mm, masterAccount, execPath, payload)
		if err != nil {
			log.Err(err).Str("JobId", payload.JobId).Msg(err.Error())
			masterAccount.Observability.Status = "APPLY_FAILED"
			masterAccount.Observability.FailedReason = err.Error()
			o = mm.Save(&masterAccount)
			if o.Err != nil {
				log.Err(o.Err).Str("JobId", payload.JobId).Msg(o.Err.Error())
				return o.Err
			}
			return err
		}
		masterAccount = *updatedAccount
	}

	log.Debug().Str("JobId", payload.JobId).Msg("Updating observability status")

	masterAccount.Observability.Enabled = true
	masterAccount.Observability.Status = "APPLIED"
	masterAccount.Observability.FailedReason = ""
	o = mm.Save(&masterAccount)
	if o.Err != nil {
		log.Err(o.Err).Str("JobId", payload.JobId).Msg(o.Err.Error())
		return o.Err
	}

	log.Debug().Str("JobId", payload.JobId).Msg("Finished applying group!")
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

func formatWithWorkerAndApply(ctx context.Context, masterAcctRegion string, mm *magicmodel.Operator, account types.Account, execPath *string, payload Payload) (*types.Account, error) {
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
		account.Observability.Status = "APPLY_FAILED"
		account.Observability.FailedReason = fmt.Sprintf("%s: %s", err, strMsg)
		o := mm.Save(&account)
		if o.Err != nil {
			log.Err(o.Err).Str("JobId", payload.JobId).Msg(o.Err.Error())
			return nil, o.Err
		}
		return nil, fmt.Errorf("Error running `worker observability apply` for account: %s: %s", err, strMsg)
	}

	log.Debug().Str("JobId", payload.JobId).Msg("Applying observability Terraform")

	updatedAccount, err := apply(ctx, mm, account, execPath, nil, account.AwsAccountId, payload)
	if err != nil {
		log.Err(err).Str("JobId", payload.JobId).Msg(err.Error())
		account.Observability.Status = "APPLY_FAILED"
		account.Observability.FailedReason = err.Error()
		o := mm.Save(&account)
		if o.Err != nil {
			log.Err(o.Err).Str("JobId", payload.JobId).Msg(o.Err.Error())
			return nil, o.Err
		}
		return nil, fmt.Errorf("Error running apply for observability stack: %s", err)
	}
	return updatedAccount, nil
}

func apply(ctx context.Context, mm *magicmodel.Operator, account types.Account, execPath *string, roleToAssume *string, dirName string, payload Payload) (*types.Account, error) {
	path, _ := filepath.Abs(fmt.Sprintf("%s/%s", os.Getenv("DRAGONOPS_TERRAFORM_DESTINATION"), dirName))

	log.Debug().Str("JobId", payload.JobId).Msg(path)
	out, err := terraform.ApplyTerraform(ctx, path, *execPath, roleToAssume)
	if err != nil {
		//errors <- fmt.Errorf("error for %s %s: %v", dirName, dir.Name(), err)
		account.Observability.Status = "APPLY_FAILED"
		account.Observability.FailedReason = err.Error()
		o := mm.Save(&account)
		if o.Err != nil {
			log.Err(o.Err).Str("JobId", payload.JobId).Msg(o.Err.Error())
			return nil, o.Err
		}
		return nil, err
	}

	updatedAccount, err := saveOutputs(mm, out, account)
	if err != nil {
		//errors <- fmt.Errorf("error saving outputs for %s %s: %v", dirName, dir.Name(), err)
		account.Observability.Status = "APPLY_FAILED"
		account.Observability.FailedReason = err.Error()
		o := mm.Save(&account)
		if o.Err != nil {
			log.Err(o.Err).Str("JobId", payload.JobId).Msg(o.Err.Error())
			return nil, o.Err
		}
		return nil, err
	}

	return updatedAccount, nil
}

func saveOutputs(mm *magicmodel.Operator, outputs map[string]tfexec.OutputMeta, account types.Account) (*types.Account, error) {
	var vpcEndpointServiceName string
	if err := json.Unmarshal(outputs["nlb_vpc_endpoint_service_name"].Value, &vpcEndpointServiceName); err != nil {
		return nil, fmt.Errorf("Error decoding output value for key %s: %s\n", "nlb_vpc_endpoint_service_name", err)
	}

	var vpcEndpointServiceId string
	if err := json.Unmarshal(outputs["nlb_vpc_endpoint_service_id"].Value, &vpcEndpointServiceId); err != nil {
		return nil, fmt.Errorf("Error decoding output value for key %s: %s\n", "nlb_vpc_endpoint_service_id", err)
	}

	var vpcEndpointServicePrivateDnsName string
	if err := json.Unmarshal(outputs["nlb_vpc_endpoint_service_private_dns_name"].Value, &vpcEndpointServicePrivateDnsName); err != nil {
		return nil, fmt.Errorf("Error decoding output value for key %s: %s\n", "nlb_vpc_endpoint_service_private_dns_name", err)
	}

	var nlbInternalDnsName string
	if err := json.Unmarshal(outputs["nlb_internal_dns_name"].Value, &nlbInternalDnsName); err != nil {
		return nil, fmt.Errorf("Error decoding output value for key %s: %s\n", "nlb_internal_dns_name", err)
	}

	account.Observability.VpcEndpointServiceName = vpcEndpointServiceName
	account.Observability.VpcEndpointServiceId = vpcEndpointServiceId
	account.Observability.VpcEndpointServicePrivateDns = vpcEndpointServicePrivateDnsName
	account.Observability.NlbInternalDnsName = nlbInternalDnsName

	o := mm.Save(&account)
	if o.Err != nil {
		return nil, o.Err
	}

	return &account, nil
}
