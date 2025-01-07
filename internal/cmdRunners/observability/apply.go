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
		aco := mm.Update(&masterAccount, "Observability.Status", "APPLY_FAILED")
		if aco.Err != nil {
			log.Err(aco.Err).Str("JobId", payload.JobId).Msg(aco.Err.Error())
			return aco.Err
		}
		aco = mm.Update(&masterAccount, "Observability.FailedReason", "No RECEIPT_HANDLE variable found.")
		if aco.Err != nil {
			log.Err(aco.Err).Str("JobId", payload.JobId).Msg(aco.Err.Error())
			return aco.Err
		}
		return fmt.Errorf("Error retrieving RECEIPT_HANDLE from queue. Cannot continue.")
	}

	cfg, err := config.LoadDefaultConfig(ctx, func(options *config.LoadOptions) error {
		config.WithRegion(accounts[0].AwsRegion)
		return nil
	})
	if err != nil {
		log.Err(err).Str("JobId", payload.JobId).Msg(err.Error())
		aco := mm.Update(&masterAccount, "Observability.Status", "APPLY_FAILED")
		if aco.Err != nil {
			log.Err(aco.Err).Str("JobId", payload.JobId).Msg(aco.Err.Error())
			return aco.Err
		}
		aco = mm.Update(&masterAccount, "Observability.FailedReason", err.Error())
		if aco.Err != nil {
			log.Err(aco.Err).Str("JobId", payload.JobId).Msg(aco.Err.Error())
			return aco.Err
		}
		return err
	}

	// get the doApiKey from secrets manager, not the payload
	doApiKey, err := utils.GetDoApiKeyFromSecretsManager(ctx, cfg, payload.UserName)
	if err != nil {
		log.Err(err).Str("JobId", payload.JobId).Msg(err.Error())
		aco := mm.Update(&masterAccount, "Observability.Status", "APPLY_FAILED")
		if aco.Err != nil {
			log.Err(aco.Err).Str("JobId", payload.JobId).Msg(aco.Err.Error())
			return aco.Err
		}
		aco = mm.Update(&masterAccount, "Observability.FailedReason", err.Error())
		if aco.Err != nil {
			log.Err(aco.Err).Str("JobId", payload.JobId).Msg(aco.Err.Error())
			return aco.Err
		}
		return err
	}

	authResponse, err := utils.IsApiKeyValid(*doApiKey)
	if err != nil {
		log.Err(err).Str("JobId", payload.JobId).Msg(err.Error())
		aco := mm.Update(&masterAccount, "Observability.Status", "APPLY_FAILED")
		if aco.Err != nil {
			log.Err(aco.Err).Str("JobId", payload.JobId).Msg(aco.Err.Error())
			return aco.Err
		}
		aco = mm.Update(&masterAccount, "Observability.FailedReason", err.Error())
		if aco.Err != nil {
			log.Err(aco.Err).Str("JobId", payload.JobId).Msg(aco.Err.Error())
			return aco.Err
		}
		return fmt.Errorf("error verifying validity of DragonOps Api Key: %v", err)
	}

	if !authResponse.IsValid {
		log.Err(fmt.Errorf("The DragonOps api key provided is not valid. Please reach out to DragonOps support for help.")).Str("JobId", payload.JobId).Msg("The DragonOps api key provided is not valid. Please reach out to DragonOps support for help.")
		aco := mm.Update(&masterAccount, "Observability.Status", "APPLY_FAILED")
		if aco.Err != nil {
			log.Err(aco.Err).Str("JobId", payload.JobId).Msg(aco.Err.Error())
			return aco.Err
		}
		aco = mm.Update(&masterAccount, "Observability.FailedReason", fmt.Errorf("The DragonOps api key provided is not valid. Please reach out to DragonOps support for help."))
		if aco.Err != nil {
			log.Err(aco.Err).Str("JobId", payload.JobId).Msg(aco.Err.Error())
			return aco.Err
		}
		return fmt.Errorf("The DragonOps api key provided is not valid. Please reach out to DragonOps support for help.")
	}

	sqsClient := sqs.NewFromConfig(cfg, func(o *sqs.Options) {
		o.Region = accounts[0].AwsRegion
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
			aco := mm.Update(&masterAccount, "Observability.Status", "APPLY_FAILED")
			if aco.Err != nil {
				log.Err(aco.Err).Str("JobId", payload.JobId).Msg(aco.Err.Error())
				return aco.Err
			}
			aco = mm.Update(&masterAccount, "Observability.FailedReason", err.Error())
			if aco.Err != nil {
				log.Err(aco.Err).Str("JobId", payload.JobId).Msg(aco.Err.Error())
				return aco.Err
			}
			return err
		}

		log.Debug().Str("JobId", payload.JobId).Msg(fmt.Sprintf("Region for magic model is: %s", accounts[0].AwsRegion))
		// TODO
		err = formatWithWorkerAndApply(ctx, accounts[0].AwsRegion, mm, masterAccount, execPath, cfg, payload)
		if err != nil {

			log.Err(err).Str("JobId", payload.JobId).Msg(err.Error())
			aco := mm.Update(&masterAccount, "Observability.Status", "APPLY_FAILED")
			if aco.Err != nil {
				log.Err(aco.Err).Str("JobId", payload.JobId).Msg(aco.Err.Error())
				return aco.Err
			}
			aco = mm.Update(&masterAccount, "Observability.FailedReason", err.Error())
			if aco.Err != nil {
				log.Err(aco.Err).Str("JobId", payload.JobId).Msg(aco.Err.Error())
				return aco.Err
			}
			return err
		}
	}

	log.Debug().Str("JobId", payload.JobId).Msg("Updating observability status")
	o = mm.Update(&masterAccount, "Observability.Status", "APPLIED")
	if o.Err != nil {
		log.Err(o.Err).Str("JobId", payload.JobId).Msg(o.Err.Error())
		return o.Err
	}
	o = mm.Update(&masterAccount, "FailedReason", "")
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

func formatWithWorkerAndApply(ctx context.Context, masterAcctRegion string, mm *magicmodel.Operator, account types.Account, execPath *string, cfg aws.Config, payload Payload) error {
	log.Debug().Str("JobId", payload.JobId).Msg("Templating Terraform with correct values")

	// TODO implement in worker
	command := fmt.Sprintf("/app/worker observability apply --table-region %s", masterAcctRegion)
	if os.Getenv("IS_LOCAL") == "true" {
		command = fmt.Sprintf("./app/worker observability apply --table-region %s", masterAcctRegion)
	}

	log.Debug().Str("JobId", payload.JobId).Msg(fmt.Sprintf("Running command %s", command))
	msg, err := utils.RunOSCommandOrFail(command)
	if err != nil {
		o := mm.Update(&account, "Observability.Status", "APPLY_FAILED")
		if o.Err != nil {
			return o.Err
		}
		o = mm.Update(&account, "Observability.Status", err.Error())
		if o.Err != nil {
			return o.Err
		}
		// avoid nil pointer deference error
		strMsg := ""
		if msg != nil {
			strMsg = *msg
		}
		log.Err(err).Str("JobId", payload.JobId).Msg(strMsg)
		return fmt.Errorf("Error running `worker observability apply` for account: %s: %s", err, strMsg)
	}

	log.Debug().Str("JobId", payload.JobId).Msg("Applying observability Terraform")

	err = apply(ctx, mm, account, execPath, nil, "observability", cfg, payload)
	if err != nil {
		aco := mm.Update(&account, "Observability.Status", "APPLY_FAILED")
		if aco.Err != nil {
			log.Err(aco.Err).Str("JobId", payload.JobId).Msg(aco.Err.Error())
			return aco.Err
		}
		aco = mm.Update(&account, "Observability.FailedReason", err.Error())
		if aco.Err != nil {
			log.Err(aco.Err).Str("JobId", payload.JobId).Msg(aco.Err.Error())
			return aco.Err
		}
		return fmt.Errorf("Error running apply for observability stack: %s", err)
	}
	return nil
}

func apply(ctx context.Context, mm *magicmodel.Operator, account types.Account, execPath *string, roleToAssume *string, dirName string, cfg aws.Config, payload Payload) error {
	directoryPath := filepath.Join(os.Getenv("DRAGONOPS_TERRAFORM_DESTINATION"), dirName)
	path, _ := filepath.Abs(filepath.Join(directoryPath, directoryPath))

	log.Debug().Str("JobId", payload.JobId).Msg(path)
	out, err := terraform.ApplyTerraform(ctx, path, *execPath, roleToAssume)
	if err != nil {
		//errors <- fmt.Errorf("error for %s %s: %v", dirName, dir.Name(), err)
		return err
	}

	//var network *types.Network
	err = saveOutputs(mm, out, account, path)
	if err != nil {
		//errors <- fmt.Errorf("error saving outputs for %s %s: %v", dirName, dir.Name(), err)
		return err
	}
	//
	//directories, _ := os.ReadDir(directoryPath)
	//log.Debug().Str("JobId", payload.JobId).Msg(fmt.Sprintf("Applying all %ss", dirName))
	//
	//// go routine setup stuff
	////wg := &sync.WaitGroup{}
	////errors := make(chan error, 0)
	//
	//// run all the applies in parallel in each folder
	//for _, d := range directories {
	//	wg.Add(1)
	//	log.Debug().Str("JobId", payload.JobId).Msg(fmt.Sprintf("Applying %s %s", dirName, d.Name()))
	//	path, _ := filepath.Abs(filepath.Join(directoryPath, d.Name()))
	//
	//	go func(dir os.DirEntry) {
	//		defer wg.Done()
	//		// apply terraform or return an error
	//		log.Debug().Str("JobId", payload.JobId).Msg(path)
	//		out, err := terraform.ApplyTerraform(ctx, path, *execPath, roleToAssume)
	//		if err != nil {
	//			errors <- fmt.Errorf("error for %s %s: %v", dirName, dir.Name(), err)
	//			return
	//		}
	//
	//		// handle network outputs
	//		if dirName == "network" {
	//			var network *types.Network
	//			network, err = saveNetworkOutputs(mm, out, group.ID, dir.Name())
	//			if err != nil {
	//				errors <- fmt.Errorf("error saving outputs for %s %s: %v", dirName, dir.Name(), err)
	//				return
	//			}
	//
	//			err = handleWireguardUpdates(mm, *network, cfg)
	//			if err != nil {
	//				errors <- fmt.Errorf("error updating wireguard for %s %s: %v", dirName, dir.Name(), err)
	//				return
	//			}
	//
	//		}
	//
	//		// handle output for grafana credentials
	//		if dirName == "cluster_grafana" {
	//			err = saveCredsToCluster(mm, out, group.ID, dir.Name())
	//			if err != nil {
	//				errors <- fmt.Errorf("error for %s %s: %v", dirName, dir.Name(), err)
	//				return
	//			}
	//		}
	//		// handle output for alb dns name for environment
	//		if dirName == "environment" {
	//			err = saveEnvironmentAlbDnsName(mm, out, group.ID, dir.Name())
	//			if err != nil {
	//				errors <- fmt.Errorf("error for %s %s: %v", dirName, dir.Name(), err)
	//				return
	//			}
	//		}
	//		// handle output for rds endpoint for environment
	//		if dirName == "rds" {
	//			err = saveRdsEndpointAndSecret(mm, out, group.ID, dir.Name(), cfg)
	//			if err != nil {
	//				errors <- fmt.Errorf("error for %s %s: %v", dirName, dir.Name(), err)
	//				return
	//			}
	//		}
	//	}(d)
	//}
	//
	//go func() {
	//	wg.Wait()
	//	close(errors)
	//}()
	//
	//errs := make([]error, 0)
	//for err := range errors {
	//	errs = append(errs, err)
	//}
	//if len(errs) > 0 {
	//	err := fmt.Errorf("errors occurred with applying resources in group %s: %v", group.ResourceLabel, errs)
	//	return err
	//}
	return nil
}

func saveOutputs(mm *magicmodel.Operator, outputs map[string]tfexec.OutputMeta, account types.Account, networkResourceLabel string) error {
	//var wireguardInstanceID string
	//if err := json.Unmarshal(outputs["wireguard_instance_id"].Value, &wireguardInstanceID); err != nil {
	//	return nil, fmt.Errorf("Error decoding output value for key %s: %s\n", "wireguard_instance_id", err)
	//}
	//
	//var wireguardPublicIP string
	//if err := json.Unmarshal(outputs["wireguard_public_ip"].Value, &wireguardPublicIP); err != nil {
	//	return nil, fmt.Errorf("Error decoding output value for key %s: %s\n", "wireguard_public_ip", err)
	//}
	//
	//var vpcMap map[string]interface{}
	//if err := json.Unmarshal(outputs["vpc"].Value, &vpcMap); err != nil {
	//	return nil, fmt.Errorf("Error decoding output value for key %s: %s\n", "vpc", err)
	//}
	//// TODO save outputs
	//account.OrchestratorStackOutputs = vpcMap["vpc_id"].(string)
	//network.WireguardInstanceID = wireguardInstanceID
	//network.WireguardPublicIP = wireguardPublicIP
	//
	//o = mm.Save(&network)
	//if o.Err != nil {
	//	return nil, o.Err
	//}

	return nil
}
