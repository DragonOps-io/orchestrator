package plan

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/DragonOps-io/orchestrator/internal/terraform"
	"github.com/DragonOps-io/orchestrator/internal/utils"
	"github.com/DragonOps-io/types"
	magicmodel "github.com/Ilios-LLC/magicmodel-go/model"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/rs/zerolog/log"
)

func GroupPlan(ctx context.Context, payload Payload, mm *magicmodel.Operator) error {
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

	err = formatWithWorkerAndPlanGroup(ctx, *cfg, masterAccount.AwsRegion, *masterAccount.StateBucketName, mm, group, execPath, roleToAssume, payload.PlanId, payload)
	if err != nil {
		group.Status = "APPLY_FAILED"
		group.FailedReason = err.Error()
		so := mm.Save(&group)
		if so.Err != nil {
			return so.Err
		}
		return err
	}

	log.Info().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Updating group status")
	group.Status = "APPLIED"
	group.FailedReason = ""
	o = mm.Save(&group)
	if o.Err != nil {
		return o.Err
	}

	log.Info().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Finished planning group!")
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

func formatWithWorkerAndPlanGroup(ctx context.Context, awsCfg aws.Config, masterAcctRegion string, stateBucketName string, mm *magicmodel.Operator, group types.Group, execPath *string, roleToAssume *string, planId string, payload Payload) error {
	//msg, err := utils.RunWorkerResourcesList(group, payload.JobId)
	//if err != nil {
	//	// TODO
	//	return err
	//}
	//
	//terraformDirectoryPath := filepath.Join(os.Getenv("DRAGONOPS_TERRAFORM_DESTINATION"), fmt.Sprintf("group/%s", group.ResourceLabel))
	//var resources utils.Resources
	//err = json.Unmarshal([]byte(*msg), &resources.Data)
	//if err != nil {
	//	// TODO
	//	return err
	//}
	//
	//allResourcesToDelete, err := utils.GetAllResourcesToDeleteByGroupId(mm, group.ID)
	//if err != nil {
	//	return fmt.Errorf("error retrieving resources to delete: %v", err)
	//}
	//
	//terraformResourcesToDelete := utils.GetExactTerraformResourceNames(allResourcesToDelete, resources)

	//if len(terraformResourcesToDelete) > 0 {
	//	err = runWorkerGroupApply(mm, group, payload.JobId, masterAcctRegion)
	//	if err != nil {
	//		// TODO
	//		return err
	//	}
	//
	//	log.Info().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Destroying resources removed from config...")
	//	err = terraform.PlanGroupTerraformWithDestroyTargets(ctx, *cfg, "", "bucketName", terraformDirectoryPath, *execPath, terraformResourcesToDelete, roleToAssume)
	//	if err != nil {
	//		return fmt.Errorf("error destroying resources with terraform targeting: %v", err)
	//	}
	//
	//	err = deleteGroupResources(mm, allResourcesToDelete)
	//	if err != nil {
	//		return fmt.Errorf("error deleting resources from dynamo: %v", err)
	//	}
	//}

	//// template with worker a second time, now that resources are destroyed
	//err = runWorkerGroupApply(mm, group, payload.JobId, masterAcctRegion)
	//if err != nil {
	//	return fmt.Errorf("error templating terraform: %v", err)
	//}
	return nil
	//	log.Info().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Applying terraform...")
	//	out, err := terraform.ApplyTerraform(ctx, terraformDirectoryPath, *execPath, roleToAssume)
	//	if err != nil {
	//		return fmt.Errorf("error applying terraform: %v", err)
	//
	//	}
	//
	//	log.Info().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Saving terraform outputs...")
	//	err = handleTerraformOutputs(mm, group, out, cfg)
	//	if err != nil {
	//		return fmt.Errorf("error sa: %v", err)
	//	}
	//
	//	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Templating Terraform with correct values")
	//
	//	command := fmt.Sprintf("/app/worker group apply --group-id %s --table-region %s", group.ID, masterAcctRegion)
	//	if os.Getenv("IS_LOCAL") == "true" {
	//		command = fmt.Sprintf("./app/worker group apply --group-id %s --table-region %s", group.ID, masterAcctRegion)
	//	}
	//
	//	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Running command %s", command))
	//	msg, err := utils.RunOSCommandOrFail(command)
	//	if err != nil {
	//		o := mm.Update(&group, "Status", "APPLY_FAILED")
	//		if o.Err != nil {
	//			return o.Err
	//		}
	//		o = mm.Update(&group, "FailedReason", err.Error())
	//		if o.Err != nil {
	//			return o.Err
	//		}
	//		return fmt.Errorf("Error running `worker group apply` for group with id %s: %s: %s", group.ID, err, *msg)
	//	}
	//
	//	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("planning group Terraform")
	//	// can't use a for loop because we need to do it in order
	//	// plan networks all together
	//	err = plan(ctx, awsCfg, group, stateBucketName, execPath, roleToAssume, "network", planId, payload)
	//	if err != nil {
	//		o := mm.Update(&group, "Status", "APPLY_FAILED")
	//		if o.Err != nil {
	//			return o.Err
	//		}
	//		o = mm.Update(&group, "FailedReason", err.Error())
	//		if o.Err != nil {
	//			return o.Err
	//		}
	//		return fmt.Errorf("Error running plan for network stacks in group with id %s: %s: %s", group.ID, err, *msg)
	//	}
	//
	//	// plan clusters all together
	//	err = plan(ctx, awsCfg, group, stateBucketName, execPath, roleToAssume, "cluster", planId, payload)
	//	if err != nil {
	//		o := mm.Update(&group, "Status", "APPLY_FAILED")
	//		if o.Err != nil {
	//			return o.Err
	//		}
	//		o = mm.Update(&group, "FailedReason", err.Error())
	//		if o.Err != nil {
	//			return o.Err
	//		}
	//		return fmt.Errorf("Error running plan for cluster stacks in group with id %s: %s: %s", group.ID, err, *msg)
	//	}
	//
	//	// plan cluster grafana all together
	//	err = plan(ctx, awsCfg, group, stateBucketName, execPath, roleToAssume, "cluster_grafana", planId, payload)
	//	if err != nil {
	//		o := mm.Update(&group, "Status", "APPLY_FAILED")
	//		if o.Err != nil {
	//			return o.Err
	//		}
	//		o = mm.Update(&group, "FailedReason", err.Error())
	//		if o.Err != nil {
	//			return o.Err
	//		}
	//		return fmt.Errorf("Error running plan for cluster_grafana stacks in group with id %s: %s: %s", group.ID, err, *msg)
	//	}
	//
	//	// plan environments all together
	//	err = plan(ctx, awsCfg, group, stateBucketName, execPath, roleToAssume, "environment", planId, payload)
	//	if err != nil {
	//		o := mm.Update(&group, "Status", "APPLY_FAILED")
	//		if o.Err != nil {
	//			return o.Err
	//		}
	//		o = mm.Update(&group, "FailedReason", err.Error())
	//		if o.Err != nil {
	//			return o.Err
	//		}
	//		return fmt.Errorf("Error running plan for environment stacks in group with id %s: %s: %s", group.ID, err, *msg)
	//	}
	//
	//	// plan static environments all together
	//	err = plan(ctx, awsCfg, group, stateBucketName, execPath, roleToAssume, "environment-static", planId, payload)
	//	if err != nil {
	//		o := mm.Update(&group, "Status", "APPLY_FAILED")
	//		if o.Err != nil {
	//			return o.Err
	//		}
	//		o = mm.Update(&group, "FailedReason", err.Error())
	//		if o.Err != nil {
	//			return o.Err
	//		}
	//		return fmt.Errorf("Error running plan for environment stacks in group with id %s: %s: %s", group.ID, err, *msg)
	//	}
	//
	//	// plan environments all together
	//	err = plan(ctx, awsCfg, group, stateBucketName, execPath, roleToAssume, "database", planId, payload)
	//	if err != nil {
	//		o := mm.Update(&group, "Status", "APPLY_FAILED")
	//		if o.Err != nil {
	//			return o.Err
	//		}
	//		o = mm.Update(&group, "FailedReason", err.Error())
	//		if o.Err != nil {
	//			return o.Err
	//		}
	//		return fmt.Errorf("Error running plan for database stacks in group with id %s: %s: %s", group.ID, err, *msg)
	//	}
	//	return nil
	//}
	//
	//func plan(ctx context.Context, awsCfg aws.Config, group types.Group, stateBucketName string, execPath *string, roleToAssume *string, dirName string, planId string, payload Payload) error {
	//	directoryPath := filepath.Join(os.Getenv("DRAGONOPS_TERRAFORM_DESTINATION"), dirName)
	//	// /groups/groupId/network --> directoryPath
	//	directories, _ := os.ReadDir(directoryPath)
	//	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Planning all %ss", dirName))
	//
	//	// go routine setup stuff
	//	wg := &sync.WaitGroup{}
	//	errors := make(chan error, 0)
	//
	//	// run all the applies in parallel in each folder
	//	for _, d := range directories {
	//		wg.Add(1)
	//		log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Planning %s %s", dirName, d.Name()))
	//		path, _ := filepath.Abs(filepath.Join(directoryPath, d.Name()))
	//		// /groups/groupId/network/network_resource_label_here/terraform files --> path
	//		go func(dir os.DirEntry) {
	//			defer wg.Done()
	//			// plan terraform or return an error
	//			err := terraform.PlanGroupTerraform(ctx, awsCfg, planId, stateBucketName, path, *execPath, roleToAssume)
	//			if err != nil {
	//				errors <- fmt.Errorf("error for %s %s: %v", dirName, dir.Name(), err)
	//				return
	//			}
	//		}(d)
	//	}
	//
	//	go func() {
	//		wg.Wait()
	//		close(errors)
	//	}()
	//
	//	errs := make([]error, 0)
	//	for err := range errors {
	//		errs = append(errs, err)
	//	}
	//	if len(errs) > 0 {
	//		err := fmt.Errorf("errors occurred with planning resources in group %s: %v", group.ResourceLabel, errs)
	//		return err
	//	}
	//	return nil
}
