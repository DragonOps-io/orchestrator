package group

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
	"github.com/rs/zerolog/log"
	"os"
	"path/filepath"
	"strings"
)

func Apply(ctx context.Context, payload Payload, mm *magicmodel.Operator, isDryRun bool) error {
	log.Debug().
		Str("GroupID", payload.GroupID).
		Msg("Looking for group with matching ID")

	group := types.Group{}
	o := mm.Find(&group, payload.GroupID)
	if o.Err != nil {
		return fmt.Errorf("error when trying to retrieve group with id %s: %s", payload.GroupID, o.Err)
	}
	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Found group")

	receiptHandle := os.Getenv("RECEIPT_HANDLE")
	if receiptHandle == "" {
		group.Status = "APPLY_FAILED"
		group.FailedReason = "No RECEIPT_HANDLE variable found."
		so := mm.Save(&group)
		if so.Err != nil {
			return so.Err
		}
		return fmt.Errorf("error retrieving RECEIPT_HANDLE from queue. Cannot continue")
	}

	var accounts []types.Account
	o = mm.Where(&accounts, "IsMasterAccount", aws.Bool(true))
	if o.Err != nil {
		group.Status = "APPLY_FAILED"
		group.FailedReason = o.Err.Error()
		so := mm.Save(&group)
		if so.Err != nil {
			return so.Err
		}
		return fmt.Errorf("an error occurred when trying to find the MasterAccount: %s", o.Err)
	}
	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Found MasterAccount")

	cfg, err := config.LoadDefaultConfig(ctx, func(options *config.LoadOptions) error {
		config.WithRegion(accounts[0].AwsRegion)
		return nil
	})
	if err != nil {
		group.Status = "APPLY_FAILED"
		group.FailedReason = err.Error()
		so := mm.Save(&group)
		if so.Err != nil {
			return so.Err
		}
		return err
	}

	// get the doApiKey from secrets manager, not the payload
	doApiKey, err := utils.GetDoApiKeyFromSecretsManager(ctx, cfg, payload.UserName)
	if err != nil {
		group.Status = "APPLY_FAILED"
		group.FailedReason = err.Error()
		so := mm.Save(&group)
		if so.Err != nil {
			return so.Err
		}
		return err
	}

	authResponse, err := utils.IsApiKeyValid(*doApiKey)
	if err != nil {
		group.Status = "APPLY_FAILED"
		group.FailedReason = err.Error()
		so := mm.Save(&group)
		if so.Err != nil {
			return so.Err
		}
		return fmt.Errorf("error verifying validity of DragonOps Api Key: %v", err)
	}

	if !authResponse.IsValid {
		group.Status = "APPLY_FAILED"
		group.FailedReason = "The DragonOps api key provided is not valid. Please reach out to DragonOps support for help."
		so := mm.Save(&group)
		if so.Err != nil {
			return so.Err
		}
		return fmt.Errorf("the DragonOps api key provided is not valid. Please reach out to DragonOps support for help")
	}
	sqsClient := sqs.NewFromConfig(cfg, func(o *sqs.Options) {
		o.Region = accounts[0].AwsRegion
	})

	if !isDryRun {
		if os.Getenv("IS_LOCAL") == "true" {
			os.Setenv("DRAGONOPS_TERRAFORM_DESTINATION", fmt.Sprintf("./groups/%s", group.ID))
			os.Setenv("DRAGONOPS_TERRAFORM_ARTIFACT", "./app/tmpl.tgz.age")
		} else {
			os.Setenv("DRAGONOPS_TERRAFORM_ARTIFACT", "/app/tmpl.tgz.age")
			os.Setenv("DRAGONOPS_TERRAFORM_DESTINATION", fmt.Sprintf("/groups/%s", group.ID))
		}

		var roleToAssume *string
		if group.Account.CrossAccountRoleArn != nil {
			roleToAssume = group.Account.CrossAccountRoleArn
		}

		var execPath *string
		log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Preparing Terraform")
		execPath, err = terraform.PrepareTerraform(ctx)
		if err != nil {
			group.Status = "APPLY_FAILED"
			group.FailedReason = err.Error()
			so := mm.Save(&group)
			if so.Err != nil {
				return so.Err
			}
			return err
		}

		err = formatWithWorkerAndApply(ctx, accounts[0].AwsRegion, mm, group, execPath, roleToAssume, cfg, payload, accounts[0])
		if err != nil {
			group.Status = "APPLY_FAILED"
			group.FailedReason = err.Error()
			so := mm.Save(&group)
			if so.Err != nil {
				return so.Err
			}
			return err
		}
	}

	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Updating group status")
	group.Status = "APPLIED"
	group.FailedReason = ""
	o = mm.Save(&group)
	if o.Err != nil {
		return o.Err
	}

	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Finished applying group!")
	queueParts := strings.Split(group.Account.GroupSqsArn, ":")
	queueUrl := fmt.Sprintf("https://%s.%s.amazonaws.com/%s/%s", queueParts[2], queueParts[3], queueParts[4], queueParts[5])

	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Queue url is %s", queueUrl))

	_, err = sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      &queueUrl,
		ReceiptHandle: &receiptHandle,
	})
	if err != nil {
		return err
	}
	return nil
}

type Resources struct {
	Data map[string][]string
}

func formatWithWorkerAndApply(ctx context.Context, masterAcctRegion string, mm *magicmodel.Operator, group types.Group, execPath *string, roleToAssume *string, cfg aws.Config, payload Payload, masterAccount types.Account) error {
	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Retrieving terraform resource list...")
	msg, err := runWorkerResourcesList(group, mm, payload.JobId)
	if err != nil {
		// TODO
		return err
	}
	terraformDirectoryPath := filepath.Join(os.Getenv("DRAGONOPS_TERRAFORM_DESTINATION"), fmt.Sprintf("group/%s", group.ResourceLabel))

	// Parse the JSON output
	var resources Resources
	err = json.Unmarshal([]byte(*msg), &resources.Data)
	if err != nil {
		fmt.Println("Error parsing JSON:", err)
		// TODO
		return err
	}
	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Retrieving resources to destroy (if any)...")
	allResourcesToDelete, err := getAllResourcesToDeleteByGroupId(mm, group.ID)
	if err != nil {
		return fmt.Errorf("error retrieving resources to delete: %v", err)
	}

	terraformResourcesToDelete := getExactTerraformResourceNames(allResourcesToDelete, resources)

	if len(terraformResourcesToDelete) > 0 {
		log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Templating Terraform for destroy with targeting...")
		err = runWorkerGroupApply(mm, group, payload.JobId, masterAcctRegion)
		if err != nil {
			// TODO
			return err
		}

		log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Destroying resources removed from config...")
		err = terraform.DestroyTerraformTargets(ctx, terraformDirectoryPath, *execPath, terraformResourcesToDelete, roleToAssume)
		if err != nil {
			return fmt.Errorf("error destroying resources with terraform targeting: %v", err)
		}

		log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Deleting resources from Dynamo...")
		err = deleteGroupResources(mm, allResourcesToDelete)
		if err != nil {
			return fmt.Errorf("error deleting resources from dynamo: %v", err)
		}
	}

	// template with worker a second time, now that resources are destroyed
	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Templating Terraform for apply...")
	err = runWorkerGroupApply(mm, group, payload.JobId, masterAcctRegion)
	if err != nil {
		return fmt.Errorf("error templating terraform: %v", err)
	}

	//// TODO: If we track a status or InitialApply or something for resources, we could skip this step if there are no new resources.
	//allResourcesToApplyWithTargeting, err := getAllResourcesForApplyTargetingByGroupId(mm, group.ID)
	//if err != nil {
	//	return fmt.Errorf("error retrieving resources to apply: %v", err)
	//}
	//terraformResourcesToTarget := getExactTerraformResourceNamesForTargetApply(allResourcesToApplyWithTargeting)
	//if len(terraformResourcesToTarget) > 0 {
	//	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Applying terraform with targeting...")
	//	_, err = terraform.ApplyTerraformWithTargets(ctx, terraformDirectoryPath, *execPath, terraformResourcesToTarget, roleToAssume)
	//	if err != nil {
	//		return fmt.Errorf("error applying terraform with targeting: %v", err)
	//	}
	//}

	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Applying all terraform resources...")
	out, err := terraform.ApplyTerraform(ctx, terraformDirectoryPath, *execPath, roleToAssume)
	if err != nil {
		return fmt.Errorf("error applying terraform: %v", err)

	}

	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Saving terraform outputs...")
	err = handleTerraformOutputs(mm, group, out, cfg)
	if err != nil {
		return fmt.Errorf("error sa: %v", err)
	}

	return nil
}

// TODO This is currently in terraform but not sure if we need it here or not currently.
//func handleVpcConnectionsForObservability(ctx context.Context, network types.Network, awsCfg aws.Config, masterAccount types.Account) error {
//	client := ec2.NewFromConfig(awsCfg)
//	err := acceptPendingConnections(ctx, client, masterAccount.Observability.VpcEndpointServiceId, network.VpcEndpointId)
//	if err != nil {
//		var apiErr smithy.APIError
//		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "InvalidState" {
//			log.Info().Str("GroupID", network.Group.ID).Msg("Ignoring InvalidState error")
//			return nil
//		}
//		return fmt.Errorf("failed to accept connection: %w", err)
//	}
//	return nil
//}
//func acceptPendingConnections(ctx context.Context, client *ec2.Client, serviceID string, endpointId string) error {
//	// Accept VPC endpoint connections
//	_, err := client.AcceptVpcEndpointConnections(ctx, &ec2.AcceptVpcEndpointConnectionsInput{
//		ServiceId:      aws.String(serviceID),
//		VpcEndpointIds: []string{endpointId},
//	})
//	return err
//}
