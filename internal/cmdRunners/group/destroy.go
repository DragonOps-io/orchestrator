package group

import (
	"context"
	"fmt"
	"github.com/DragonOps-io/orchestrator/internal/terraform"
	"github.com/DragonOps-io/orchestrator/internal/utils"
	"github.com/DragonOps-io/types"
	magicmodel "github.com/Ilios-LLC/magicmodel-go/model"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/rs/zerolog/log"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func Destroy(ctx context.Context, payload Payload, mm *magicmodel.Operator, isDryRun bool) error {
	log.Info().
		Str("GroupID", payload.GroupID).
		Str("JobId", payload.JobId).
		Msg("Retrieving group for destroy")

	group := types.Group{}
	o := mm.Find(&group, payload.GroupID)
	if o.Err != nil {
		return fmt.Errorf("error when trying to retrieve group with id %s: %s", payload.GroupID, o.Err)
	}

	masterAccount, cfg, err := utils.CommonStartupTasks(ctx, mm, payload.UserName)
	if err != nil {
		group.Status = "DESTROY_FAILED"
		group.FailedReason = err.Error()
		so := mm.Save(&group)
		if so.Err != nil {
			return so.Err
		}
		return fmt.Errorf("error during common startup tasks: %v", err)
	}

	var roleToAssume *string
	if group.Account.CrossAccountRoleArn != nil {
		roleToAssume = group.Account.CrossAccountRoleArn
	}

	if !isDryRun {
		if os.Getenv("IS_LOCAL") == "true" {
			os.Setenv("DRAGONOPS_TERRAFORM_DESTINATION", fmt.Sprintf("./groups/%s", group.ID))
			os.Setenv("DRAGONOPS_TERRAFORM_ARTIFACT", "./app/tmpl.tgz.age")
		} else {
			os.Setenv("DRAGONOPS_TERRAFORM_DESTINATION", fmt.Sprintf("/groups/%s", group.ID))
			os.Setenv("DRAGONOPS_TERRAFORM_ARTIFACT", "/app/tmpl.tgz.age")
		}

		var execPath *string
		execPath, err = terraform.PrepareTerraform(ctx)
		if err != nil {
			group.Status = "DESTROY_FAILED"
			group.FailedReason = err.Error()
			so := mm.Save(&group)
			if so.Err != nil {
				return so.Err
			}
			return err
		}

		err = formatWithWorkerAndDestroy(ctx, masterAccount.AwsRegion, mm, group, execPath, roleToAssume, *cfg, payload)
		if err != nil {
			group.Status = "DESTROY_FAILED"
			group.FailedReason = err.Error()
			so := mm.Save(&group)
			if so.Err != nil {
				return so.Err
			}
			return err
		}
	}

	log.Info().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Finished Terraform destroy. Cleaning up other resources...")

	resources, err := getAllResourcesByGroupId(mm, group.ID)
	if err != nil {
		group.Status = "DESTROY_FAILED"
		group.FailedReason = err.Error()
		so := mm.Save(&group)
		if so.Err != nil {
			return so.Err
		}
		return err
	}

	err = deleteResourcesFromDynamo(ctx, resources, mm, *cfg)
	if err != nil {
		group.Status = "DESTROY_FAILED"
		group.FailedReason = err.Error()
		so := mm.Save(&group)
		if so.Err != nil {
			return so.Err
		}
		return err
	}
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

	group.Status = "DESTROYED"
	o = mm.SoftDelete(&group)
	if o.Err != nil {
		return o.Err
	}
	log.Info().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Finished deleting all resources for group")
	return nil
}

type addNameServersPayload struct {
	NameServers []string `json:"name_servers"`
	Domain      string   `json:"domain"`
}

func getCrossAccountConfig(ctx context.Context, cfg aws.Config, roleToAssumeArn string, awsAccountId string, awsRegion string) (aws.Config, error) {
	stsClient := sts.NewFromConfig(cfg)
	response, err := stsClient.AssumeRole(ctx, &sts.AssumeRoleInput{
		RoleArn:         &roleToAssumeArn,
		RoleSessionName: aws.String("dragonops-" + strconv.Itoa(10000+rand.Intn(25000))),
		ExternalId:      &awsAccountId,
	})
	if err != nil {
		return cfg, fmt.Errorf("unable to assume target role, %v", err)
	}

	var assumedRoleCreds = response.Credentials
	assumeRoleCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(awsRegion), config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(*assumedRoleCreds.AccessKeyId, *assumedRoleCreds.SecretAccessKey, *assumedRoleCreds.SessionToken)))
	if err != nil {
		return cfg, fmt.Errorf("unable to load static credentials for service client config, %v", err)
	}

	return assumeRoleCfg, nil
}

func formatWithWorkerAndDestroy(ctx context.Context, masterAcctRegion string, mm *magicmodel.Operator, group types.Group, execPath *string, roleToAssume *string, cfg aws.Config, payload Payload) error {
	err := runWorkerGroupApply(mm, group, payload.JobId, masterAcctRegion)
	if err != nil {
		return err
	}

	terraformDirectoryPath := filepath.Join(os.Getenv("DRAGONOPS_TERRAFORM_DESTINATION"), fmt.Sprintf("group/%s", group.ResourceLabel))

	// generate KUBECONFIG per cluster - used in null_resources to verify ordering
	clusters, err := utils.GetAllClustersByGroupId(mm, group.ID)
	if err != nil {
		return err
	}
	for _, cluster := range clusters {
		cluster.StackName = fmt.Sprintf("%s-%s", cluster.Group.Name, cluster.Name)
		_, err = terraform.GenerateKubeconfig(ctx, cfg, cluster.StackName, masterAcctRegion)
		if err != nil {
			return err
		}
	}

	log.Info().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Running terraform destroy...")
	_, err = terraform.DestroyTerraform(ctx, terraformDirectoryPath, *execPath, roleToAssume)
	if err != nil {
		return err
	}

	return nil
}
