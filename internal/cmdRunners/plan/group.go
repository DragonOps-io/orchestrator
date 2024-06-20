package plan

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
	"sync"
)

func Plan(ctx context.Context, payload Payload, mm *magicmodel.Operator) error {
	log.Debug().
		Str("GroupID", payload.GroupID).
		Msg("Looking for group with matching ID")
	// todo should save status of current group and then readdply it?
	// for example, fi the status is APPLY_FAILED, then we PLAN, we kind of want the status to go back to APPLY_FAILED, not APPLIED
	group := types.Group{}
	o := mm.Find(&group, payload.GroupID)
	if o.Err != nil {
		log.Err(o.Err).Str("GroupID", payload.GroupID).Msg("Error finding group")
		return fmt.Errorf("Error when trying to retrieve group with id %s: %s", payload.GroupID, o.Err)
	}
	log.Debug().Str("GroupID", group.ID).Msg("Found group")

	receiptHandle := os.Getenv("RECEIPT_HANDLE")
	if receiptHandle == "" {
		log.Err(fmt.Errorf("no RECEIPT_HANDLE variable found")).Str("GroupID", group.ID).Msg("Error retrieving RECEIPT_HANDLE from queue. Cannot continue.")
		aco := mm.Update(&group, "Status", "APPLY_FAILED")
		if aco.Err != nil {
			log.Err(aco.Err).Str("GroupID", group.ID).Msg(aco.Err.Error())
			return aco.Err
		}
		aco = mm.Update(&group, "FailedReason", "No RECEIPT_HANDLE variable found.")
		if aco.Err != nil {
			log.Err(aco.Err).Str("GroupID", group.ID).Msg(aco.Err.Error())
			return aco.Err
		}
		return fmt.Errorf("Error retrieving RECEIPT_HANDLE from queue. Cannot continue.")
	}

	var accounts []types.Account
	o = mm.Where(&accounts, "IsMasterAccount", aws.Bool(true))
	if o.Err != nil {
		log.Err(o.Err).Str("GroupID", group.ID).Msg(o.Err.Error())
		aco := mm.Update(&group, "Status", "APPLY_FAILED")
		if aco.Err != nil {
			log.Err(aco.Err).Str("GroupID", group.ID).Msg(aco.Err.Error())
			return aco.Err
		}
		aco = mm.Update(&group, "FailedReason", o.Err.Error())
		if aco.Err != nil {
			log.Err(aco.Err).Str("GroupID", group.ID).Msg(aco.Err.Error())
			return aco.Err
		}
		return fmt.Errorf("an error occurred when trying to find the MasterAccount: %s", aco.Err)
	}
	log.Debug().Str("GroupID", group.ID).Msg("Found MasterAccount")

	cfg, err := config.LoadDefaultConfig(ctx, func(options *config.LoadOptions) error {
		config.WithRegion(accounts[0].AwsRegion)
		return nil
	})
	if err != nil {
		log.Err(err).Str("GroupID", group.ID).Msg(err.Error())
		aco := mm.Update(&group, "Status", "APPLY_FAILED")
		if aco.Err != nil {
			log.Err(aco.Err).Str("GroupID", group.ID).Msg(aco.Err.Error())
			return aco.Err
		}
		aco = mm.Update(&group, "FailedReason", err.Error())
		if aco.Err != nil {
			log.Err(aco.Err).Str("GroupID", group.ID).Msg(aco.Err.Error())
			return aco.Err
		}
		return err
	}
	// get the doApiKey from secrets manager, not the payload
	doApiKey, err := utils.GetDoApiKeyFromSecretsManager(ctx, cfg, payload.UserName)
	if err != nil {
		log.Err(err).Str("GroupID", group.ID).Msg(err.Error())
		aco := mm.Update(&group, "Status", "APPLY_FAILED")
		if aco.Err != nil {
			log.Err(aco.Err).Str("GroupID", group.ID).Msg(aco.Err.Error())
			return aco.Err
		}
		aco = mm.Update(&group, "FailedReason", err.Error())
		if aco.Err != nil {
			log.Err(aco.Err).Str("GroupID", group.ID).Msg(aco.Err.Error())
			return aco.Err
		}
		return err
	}

	authResponse, err := utils.IsApiKeyValid(*doApiKey)
	if err != nil {
		log.Err(err).Str("GroupID", group.ID).Msg(err.Error())
		aco := mm.Update(&group, "Status", "APPLY_FAILED")
		if aco.Err != nil {
			log.Err(aco.Err).Str("GroupID", group.ID).Msg(aco.Err.Error())
			return aco.Err
		}
		aco = mm.Update(&group, "FailedReason", err.Error())
		if aco.Err != nil {
			log.Err(aco.Err).Str("GroupID", group.ID).Msg(aco.Err.Error())
			return aco.Err
		}
		return fmt.Errorf("error verifying validity of DragonOps Api Key: %v", err)
	}

	if !authResponse.IsValid {
		log.Err(fmt.Errorf("The DragonOps api key provided is not valid. Please reach out to DragonOps support for help.")).Str("GroupID", group.ID).Msg("The DragonOps api key provided is not valid. Please reach out to DragonOps support for help.")
		aco := mm.Update(&group, "Status", "APPLY_FAILED")
		if aco.Err != nil {
			log.Err(aco.Err).Str("GroupID", group.ID).Msg(aco.Err.Error())
			return aco.Err
		}
		aco = mm.Update(&group, "FailedReason", "The DragonOps api key provided is not valid. Please reach out to DragonOps support for help.")
		if aco.Err != nil {
			log.Err(aco.Err).Str("GroupID", group.ID).Msg(aco.Err.Error())
			return aco.Err
		}
		return fmt.Errorf("The DragonOps api key provided is not valid. Please reach out to DragonOps support for help.")
	}

	sqsClient := sqs.NewFromConfig(cfg, func(o *sqs.Options) {
		o.Region = accounts[0].AwsRegion
	})

	var groupClusters []types.Cluster
	o = mm.Where(&groupClusters, "Group.ID", group.ID)
	if o.Err != nil {
		log.Err(o.Err).Str("GroupID", group.ID).Msg(o.Err.Error())
		po := mm.Update(&group, "Status", "APPLY_FAILED")
		if po.Err != nil {
			log.Err(po.Err).Str("GroupID", group.ID).Msg(po.Err.Error())
			return po.Err
		}
		po = mm.Update(&group, "FailedReason", o.Err.Error())
		if po.Err != nil {
			log.Err(po.Err).Str("GroupID", group.ID).Msg(po.Err.Error())
			return po.Err
		}
		return o.Err
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
	log.Debug().Str("GroupID", group.ID).Msg("Preparing Terraform")
	execPath, err = terraform.PrepareTerraform(ctx)
	if err != nil {
		log.Err(err).Str("GroupID", group.ID).Msg(err.Error())
		o = mm.Update(&group, "Status", "APPLY_FAILED")
		if o.Err != nil {
			log.Err(o.Err).Str("GroupID", group.ID).Msg(o.Err.Error())
			return o.Err
		}
		o = mm.Update(&group, "FailedReason", err.Error())
		if o.Err != nil {
			log.Err(o.Err).Str("GroupID", group.ID).Msg(o.Err.Error())
			return o.Err
		}
		return err
	}

	log.Debug().Str("GroupID", group.ID).Msg(fmt.Sprintf("Region for magic model is: %s", accounts[0].AwsRegion))
	err = formatWithWorkerAndPlan(ctx, cfg, accounts[0].AwsRegion, *accounts[0].StateBucketName, mm, group, execPath, roleToAssume, payload.PlanId)
	if err != nil {
		log.Err(err).Str("GroupID", group.ID).Msg(err.Error())
		o = mm.Update(&group, "Status", "APPLY_FAILED")
		if o.Err != nil {
			log.Err(o.Err).Str("GroupID", group.ID).Msg(o.Err.Error())
			return o.Err
		}
		o = mm.Update(&group, "FailedReason", err.Error())
		if o.Err != nil {
			log.Err(o.Err).Str("GroupID", group.ID).Msg(o.Err.Error())
			return o.Err
		}
		return err
	}

	log.Debug().Str("GroupID", group.ID).Msg("Updating group status")
	o = mm.Update(&group, "Status", "APPLIED")
	if o.Err != nil {
		log.Err(o.Err).Str("GroupID", group.ID).Msg(o.Err.Error())
		return o.Err
	}
	o = mm.Update(&group, "FailedReason", "")
	if o.Err != nil {
		log.Err(o.Err).Str("GroupID", group.ID).Msg(o.Err.Error())
		return o.Err
	}

	log.Debug().Str("GroupID", group.ID).Msg("Finished planning group!")
	queueParts := strings.Split(group.Account.GroupSqsArn, ":")
	queueUrl := fmt.Sprintf("https://%s.%s.amazonaws.com/%s/%s", queueParts[2], queueParts[3], queueParts[4], queueParts[5])

	log.Debug().Str("GroupID", group.ID).Msg(fmt.Sprintf("Queue url is %s", queueUrl))

	_, err = sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      &queueUrl,
		ReceiptHandle: &receiptHandle,
	})
	if err != nil {
		log.Err(err).Str("GroupID", group.ID).Msg(err.Error())
		return err
	}
	return nil
}

func formatWithWorkerAndPlan(ctx context.Context, awsCfg aws.Config, masterAcctRegion string, stateBucketName string, mm *magicmodel.Operator, group types.Group, execPath *string, roleToAssume *string, planId string) error {
	log.Debug().Str("GroupID", group.ID).Msg("Templating Terraform with correct values")

	command := fmt.Sprintf("/app/worker group apply --group-id %s --table-region %s", group.ID, masterAcctRegion)
	if os.Getenv("IS_LOCAL") == "true" {
		command = fmt.Sprintf("./app/worker group apply --group-id %s --table-region %s", group.ID, masterAcctRegion)
	}

	log.Debug().Str("GroupID", group.ID).Msg(fmt.Sprintf("Running command %s", command))
	msg, err := utils.RunOSCommandOrFail(command)
	if err != nil {
		o := mm.Update(&group, "Status", "APPLY_FAILED")
		if o.Err != nil {
			return o.Err
		}
		o = mm.Update(&group, "FailedReason", err.Error())
		if o.Err != nil {
			return o.Err
		}
		return fmt.Errorf("Error running `worker group apply` for group with id %s: %s: %s", group.ID, err, *msg)
	}

	log.Debug().Str("GroupID", group.ID).Msg("planning group Terraform")
	// can't use a for loop because we need to do it in order
	// plan networks all together
	err = plan(ctx, awsCfg, group, stateBucketName, execPath, roleToAssume, "network", planId)
	if err != nil {
		o := mm.Update(&group, "Status", "APPLY_FAILED")
		if o.Err != nil {
			return o.Err
		}
		o = mm.Update(&group, "FailedReason", err.Error())
		if o.Err != nil {
			return o.Err
		}
		return fmt.Errorf("Error running plan for network stacks in group with id %s: %s: %s", group.ID, err, *msg)
	}

	// plan clusters all together
	err = plan(ctx, awsCfg, group, stateBucketName, execPath, roleToAssume, "cluster", planId)
	if err != nil {
		o := mm.Update(&group, "Status", "APPLY_FAILED")
		if o.Err != nil {
			return o.Err
		}
		o = mm.Update(&group, "FailedReason", err.Error())
		if o.Err != nil {
			return o.Err
		}
		return fmt.Errorf("Error running plan for cluster stacks in group with id %s: %s: %s", group.ID, err, *msg)
	}

	// plan cluster grafana all together
	err = plan(ctx, awsCfg, group, stateBucketName, execPath, roleToAssume, "cluster_grafana", planId)
	if err != nil {
		o := mm.Update(&group, "Status", "APPLY_FAILED")
		if o.Err != nil {
			return o.Err
		}
		o = mm.Update(&group, "FailedReason", err.Error())
		if o.Err != nil {
			return o.Err
		}
		return fmt.Errorf("Error running plan for cluster_grafana stacks in group with id %s: %s: %s", group.ID, err, *msg)
	}

	// plan environments all together
	err = plan(ctx, awsCfg, group, stateBucketName, execPath, roleToAssume, "environment", planId)
	if err != nil {
		o := mm.Update(&group, "Status", "APPLY_FAILED")
		if o.Err != nil {
			return o.Err
		}
		o = mm.Update(&group, "FailedReason", err.Error())
		if o.Err != nil {
			return o.Err
		}
		return fmt.Errorf("Error running plan for environment stacks in group with id %s: %s: %s", group.ID, err, *msg)
	}

	// plan static environments all together
	err = plan(ctx, awsCfg, group, stateBucketName, execPath, roleToAssume, "environment-static", planId)
	if err != nil {
		o := mm.Update(&group, "Status", "APPLY_FAILED")
		if o.Err != nil {
			return o.Err
		}
		o = mm.Update(&group, "FailedReason", err.Error())
		if o.Err != nil {
			return o.Err
		}
		return fmt.Errorf("Error running plan for environment stacks in group with id %s: %s: %s", group.ID, err, *msg)
	}

	// plan environments all together
	err = plan(ctx, awsCfg, group, stateBucketName, execPath, roleToAssume, "rds", planId)
	if err != nil {
		o := mm.Update(&group, "Status", "APPLY_FAILED")
		if o.Err != nil {
			return o.Err
		}
		o = mm.Update(&group, "FailedReason", err.Error())
		if o.Err != nil {
			return o.Err
		}
		return fmt.Errorf("Error running plan for environment stacks in group with id %s: %s: %s", group.ID, err, *msg)
	}
	return nil
}

func plan(ctx context.Context, awsCfg aws.Config, group types.Group, stateBucketName string, execPath *string, roleToAssume *string, dirName string, planId string) error {
	directoryPath := filepath.Join(os.Getenv("DRAGONOPS_TERRAFORM_DESTINATION"), dirName)
	// /groups/groupId/network --> directoryPath
	directories, _ := os.ReadDir(directoryPath)
	log.Debug().Str("GroupID", group.ID).Msg(fmt.Sprintf("Planning all %ss", dirName))

	// go routine setup stuff
	wg := &sync.WaitGroup{}
	errors := make(chan error, 0)

	// run all the applies in parallel in each folder
	for _, d := range directories {
		wg.Add(1)
		log.Debug().Str("GroupID", group.ID).Msg(fmt.Sprintf("Planning %s %s", dirName, d.Name()))
		path, _ := filepath.Abs(filepath.Join(directoryPath, d.Name()))
		// /groups/groupId/network/network_resource_label_here/terraform files --> path
		go func(dir os.DirEntry) {
			defer wg.Done()
			// plan terraform or return an error
			log.Debug().Str("GroupID", group.ID).Msg(path)
			err := terraform.PlanTerraform(ctx, awsCfg, planId, stateBucketName, path, *execPath, roleToAssume)
			if err != nil {
				errors <- fmt.Errorf("error for %s %s: %v", dirName, dir.Name(), err)
				return
			}
		}(d)
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
		err := fmt.Errorf("errors occurred with planning resources in group %s: %v", group.ResourceLabel, errs)
		return err
	}
	return nil
}
