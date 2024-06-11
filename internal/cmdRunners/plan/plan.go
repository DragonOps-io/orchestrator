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

	group := types.Group{}
	o := mm.Find(&group, payload.GroupID)
	if o.Err != nil {
		return fmt.Errorf("Error when trying to retrieve group with id %s: %s", payload.GroupID, o.Err)
	}
	log.Debug().Str("GroupID", group.ID).Msg("Found group")

	receiptHandle := os.Getenv("RECEIPT_HANDLE")
	if receiptHandle == "" {
		aco := mm.Update(&group, "Status", "APPLY_FAILED")
		if aco.Err != nil {
			return aco.Err
		}
		aco = mm.Update(&group, "FailedReason", "No RECEIPT_HANDLE variable found.")
		if aco.Err != nil {
			return aco.Err
		}
		return fmt.Errorf("Error retrieving RECEIPT_HANDLE from queue. Cannot continue.")
	}

	var accounts []types.Account
	o = mm.Where(&accounts, "IsMasterAccount", aws.Bool(true))
	if o.Err != nil {
		aco := mm.Update(&group, "Status", "APPLY_FAILED")
		if aco.Err != nil {
			return aco.Err
		}
		aco = mm.Update(&group, "FailedReason", o.Err.Error())
		if aco.Err != nil {
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
		aco := mm.Update(&group, "Status", "APPLY_FAILED")
		if aco.Err != nil {
			return aco.Err
		}
		aco = mm.Update(&group, "FailedReason", err.Error())
		if aco.Err != nil {
			return aco.Err
		}
		return err
	}
	// get the doApiKey from secrets manager, not the payload
	doApiKey, err := utils.GetDoApiKeyFromSecretsManager(ctx, cfg, payload.UserName)
	if err != nil {
		aco := mm.Update(&group, "Status", "APPLY_FAILED")
		if aco.Err != nil {
			return aco.Err
		}
		aco = mm.Update(&group, "FailedReason", err.Error())
		if aco.Err != nil {
			return aco.Err
		}
		return err
	}

	authResponse, err := utils.IsApiKeyValid(*doApiKey)
	if err != nil {
		aco := mm.Update(&group, "Status", "APPLY_FAILED")
		if aco.Err != nil {
			return aco.Err
		}
		aco = mm.Update(&group, "FailedReason", err.Error())
		if aco.Err != nil {
			return aco.Err
		}
		return fmt.Errorf("error verifying validity of DragonOps Api Key: %v", err)
	}

	if !authResponse.IsValid {
		aco := mm.Update(&group, "Status", "APPLY_FAILED")
		if aco.Err != nil {
			return aco.Err
		}
		aco = mm.Update(&group, "FailedReason", "The DragonOps api key provided is not valid. Please reach out to DragonOps support for help.")
		if aco.Err != nil {
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
		po := mm.Update(&group, "Status", "APPLY_FAILED")
		if po.Err != nil {
			return po.Err
		}
		po = mm.Update(&group, "FailedReason", o.Err.Error())
		if po.Err != nil {
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
		o = mm.Update(&group, "Status", "APPLY_FAILED")
		if o.Err != nil {
			return o.Err
		}
		o = mm.Update(&group, "FailedReason", err.Error())
		if o.Err != nil {
			return o.Err
		}
		return err
	}

	log.Debug().Str("GroupID", group.ID).Msg(fmt.Sprintf("Region for magic model is: %s", accounts[0].AwsRegion))
	err = formatWithWorkerAndPlan(ctx, accounts[0].AwsRegion, *accounts[0].StateBucketName, mm, group, execPath, roleToAssume, payload.PlanId)
	if err != nil {
		o = mm.Update(&group, "Status", "APPLY_FAILED")
		if o.Err != nil {
			return o.Err
		}
		o = mm.Update(&group, "FailedReason", err.Error())
		if o.Err != nil {
			return o.Err
		}
		return err
	}

	log.Debug().Str("GroupID", group.ID).Msg("Updating group status")
	o = mm.Update(&group, "Status", "APPLIED")
	if o.Err != nil {
		return o.Err
	}
	o = mm.Update(&group, "FailedReason", "")
	if o.Err != nil {
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
		return err
	}
	return nil
}

func formatWithWorkerAndPlan(ctx context.Context, masterAcctRegion string, stateBucketName string, mm *magicmodel.Operator, group types.Group, execPath *string, roleToAssume *string, planId string) error {
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
	// apply networks all together
	err = plan(ctx, group, stateBucketName, execPath, roleToAssume, "network", planId)
	if err != nil {
		o := mm.Update(&group, "Status", "APPLY_FAILED")
		if o.Err != nil {
			return o.Err
		}
		o = mm.Update(&group, "FailedReason", err.Error())
		if o.Err != nil {
			return o.Err
		}
		return fmt.Errorf("Error running apply for network stacks in group with id %s: %s: %s", group.ID, err, *msg)
	}

	// apply clusters all together
	err = plan(ctx, group, stateBucketName, execPath, roleToAssume, "cluster", planId)
	if err != nil {
		o := mm.Update(&group, "Status", "APPLY_FAILED")
		if o.Err != nil {
			return o.Err
		}
		o = mm.Update(&group, "FailedReason", err.Error())
		if o.Err != nil {
			return o.Err
		}
		return fmt.Errorf("Error running apply for cluster stacks in group with id %s: %s: %s", group.ID, err, *msg)
	}

	// apply cluster grafana all together
	err = plan(ctx, group, stateBucketName, execPath, roleToAssume, "cluster_grafana", planId)
	if err != nil {
		o := mm.Update(&group, "Status", "APPLY_FAILED")
		if o.Err != nil {
			return o.Err
		}
		o = mm.Update(&group, "FailedReason", err.Error())
		if o.Err != nil {
			return o.Err
		}
		return fmt.Errorf("Error running apply for cluster_grafana stacks in group with id %s: %s: %s", group.ID, err, *msg)
	}

	// apply environments all together
	err = plan(ctx, group, stateBucketName, execPath, roleToAssume, "environment", planId)
	if err != nil {
		o := mm.Update(&group, "Status", "APPLY_FAILED")
		if o.Err != nil {
			return o.Err
		}
		o = mm.Update(&group, "FailedReason", err.Error())
		if o.Err != nil {
			return o.Err
		}
		return fmt.Errorf("Error running apply for environment stacks in group with id %s: %s: %s", group.ID, err, *msg)
	}

	// apply static environments all together
	err = plan(ctx, group, stateBucketName, execPath, roleToAssume, "environment-static", planId)
	if err != nil {
		o := mm.Update(&group, "Status", "APPLY_FAILED")
		if o.Err != nil {
			return o.Err
		}
		o = mm.Update(&group, "FailedReason", err.Error())
		if o.Err != nil {
			return o.Err
		}
		return fmt.Errorf("Error running apply for environment stacks in group with id %s: %s: %s", group.ID, err, *msg)
	}

	// apply environments all together
	err = plan(ctx, group, stateBucketName, execPath, roleToAssume, "rds", planId)
	if err != nil {
		o := mm.Update(&group, "Status", "APPLY_FAILED")
		if o.Err != nil {
			return o.Err
		}
		o = mm.Update(&group, "FailedReason", err.Error())
		if o.Err != nil {
			return o.Err
		}
		return fmt.Errorf("Error running apply for environment stacks in group with id %s: %s: %s", group.ID, err, *msg)
	}
	return nil
}

func plan(ctx context.Context, group types.Group, stateBucketName string, execPath *string, roleToAssume *string, dirName string, planId string) error {
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
			// apply terraform or return an error
			log.Debug().Str("GroupID", group.ID).Msg(path)
			err := terraform.PlanTerraform(ctx, planId, stateBucketName, path, *execPath, roleToAssume)
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
