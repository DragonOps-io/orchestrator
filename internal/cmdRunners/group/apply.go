package group

import (
	"context"
	"fmt"
	"github.com/DragonOps-io/orchestrator/internal/terraform"
	"github.com/DragonOps-io/orchestrator/internal/utils"
	"github.com/DragonOps-io/types"
	magicmodel "github.com/Ilios-LLC/magicmodel-go/model"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/rs/zerolog/log"
	"os"
	"path/filepath"
	"strings"
)

func Apply(ctx context.Context, payload Payload, mm *magicmodel.Operator, isDryRun bool) error {
	log.Info().
		Str("GroupID", payload.GroupID).
		Msg("Retrieving group...")

	group := types.Group{}
	o := mm.Find(&group, payload.GroupID)
	if o.Err != nil {
		return fmt.Errorf("error when trying to retrieve group with id %s: %s", payload.GroupID, o.Err)
	}

	masterAccount, cfg, err := utils.CommonStartupTasks(ctx, mm, payload.UserName)
	if err != nil {
		group.Status = "APPLY_FAILED"
		group.FailedReason = err.Error()
		so := mm.Save(&group)
		if so.Err != nil {
			return so.Err
		}
		return fmt.Errorf("error during common startup tasks: %v", err)
	}

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
		execPath, err := terraform.PrepareTerraform(ctx)
		if err != nil {
			group.Status = "APPLY_FAILED"
			group.FailedReason = err.Error()
			so := mm.Save(&group)
			if so.Err != nil {
				return so.Err
			}
			return err
		}

		err = formatWithWorkerAndApply(ctx, masterAccount.AwsRegion, mm, group, execPath, roleToAssume, *cfg, payload, *masterAccount)
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

	log.Info().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Updating group status")
	group.Status = "APPLIED"
	group.FailedReason = ""
	o = mm.Save(&group)
	if o.Err != nil {
		return o.Err
	}

	log.Info().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Finished applying group!")
	queueParts := strings.Split(group.Account.GroupSqsArn, ":")
	queueUrl := fmt.Sprintf("https://%s.%s.amazonaws.com/%s/%s", queueParts[2], queueParts[3], queueParts[4], queueParts[5])

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

func formatWithWorkerAndApply(ctx context.Context, masterAcctRegion string, mm *magicmodel.Operator, group types.Group, execPath *string, roleToAssume *string, cfg aws.Config, payload Payload, masterAccount types.Account) error {
	//msg, err := utils.RunWorkerResourcesList(group, payload.JobId)
	//if err != nil {
	//	// TODO
	//	return err
	//}

	terraformDirectoryPath := filepath.Join(os.Getenv("DRAGONOPS_TERRAFORM_DESTINATION"), fmt.Sprintf("group/%s", group.ResourceLabel))
	//var resources utils.Resources
	//err = json.Unmarshal([]byte(*msg), &resources.Data)
	//if err != nil {
	//	// TODO
	//	return err
	//}

	//
	//terraformResourcesToDelete := utils.GetExactTerraformResourceNames(allResourcesToDelete, resources)
	//
	//if len(terraformResourcesToDelete) > 0 {
	//	err = runWorkerGroupApply(mm, group, payload.JobId, masterAcctRegion)
	//	if err != nil {
	//		// TODO
	//		return err
	//	}
	//
	//	log.Info().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Destroying resources removed from config...")
	//	err = terraform.DestroyTerraformTargets(ctx, terraformDirectoryPath, *execPath, terraformResourcesToDelete, roleToAssume)
	//	if err != nil {
	//		return fmt.Errorf("error destroying resources with terraform targeting: %v", err)
	//	}
	//

	//}

	// template with worker a second time, now that resources are destroyed
	err := runWorkerGroupApply(mm, group, payload.JobId, masterAcctRegion)
	if err != nil {
		return fmt.Errorf("error templating terraform: %v", err)
	}

	log.Info().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Applying terraform...")
	out, err := terraform.ApplyTerraform(ctx, terraformDirectoryPath, *execPath, roleToAssume)
	if err != nil {
		return fmt.Errorf("error applying terraform: %v", err)

	}

	// After successful apply, delete resources that are no longer needed
	allResourcesToDelete, err := utils.GetAllResourcesToDeleteByGroupId(mm, group.ID)
	if err != nil {
		return fmt.Errorf("error retrieving resources to delete: %v", err)
	}

	err = deleteGroupResources(mm, allResourcesToDelete)
	if err != nil {
		return fmt.Errorf("error deleting resources from dynamo: %v", err)
	}

	log.Info().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Saving terraform outputs...")
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
