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
	log.Debug().
		Str("GroupID", payload.GroupID).
		Str("JobId", payload.JobId).
		Msg("Beginning group destroy")

	group := types.Group{}
	o := mm.Find(&group, payload.GroupID)
	if o.Err != nil {
		return fmt.Errorf("error when trying to retrieve group with id %s: %s", payload.GroupID, o.Err)
	}
	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Found group")

	receiptHandle := os.Getenv("RECEIPT_HANDLE")
	if receiptHandle == "" {
		group.Status = "DESTROY_FAILED"
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
		group.Status = "DESTROY_FAILED"
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
		group.Status = "DESTROY_FAILED"
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
		group.Status = "DESTROY_FAILED"
		group.FailedReason = err.Error()
		so := mm.Save(&group)
		if so.Err != nil {
			return so.Err
		}
		return err
	}

	authResponse, err := utils.IsApiKeyValid(*doApiKey)
	if err != nil {
		group.Status = "DESTROY_FAILED"
		group.FailedReason = err.Error()
		so := mm.Save(&group)
		if so.Err != nil {
			return so.Err
		}
		return fmt.Errorf("error verifying validity of DragonOps Api Key: %v", err)
	}

	if !authResponse.IsValid {
		group.Status = "DESTROY_FAILED"
		group.FailedReason = fmt.Errorf("the DragonOps api key provided is not valid. Please reach out to DragonOps support for help").Error()
		so := mm.Save(&group)
		if so.Err != nil {
			return so.Err
		}
		return fmt.Errorf("The DragonOps api key provided is not valid. Please reach out to DragonOps support for help.")
	}

	var roleToAssume *string
	if group.Account.CrossAccountRoleArn != nil {
		roleToAssume = group.Account.CrossAccountRoleArn
	}

	if roleToAssume != nil {
		log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Assuming cross account role.")
		cfg, err = getCrossAccountConfig(ctx, cfg, *roleToAssume, group.Account.AwsAccountId, group.Account.Region)
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

	if !isDryRun {
		if os.Getenv("IS_LOCAL") == "true" {
			os.Setenv("DRAGONOPS_TERRAFORM_DESTINATION", fmt.Sprintf("./groups/%s", group.ID))
			os.Setenv("DRAGONOPS_TERRAFORM_ARTIFACT", "./app/tmpl.tgz.age")
		} else {
			os.Setenv("DRAGONOPS_TERRAFORM_DESTINATION", fmt.Sprintf("/groups/%s", group.ID))
			os.Setenv("DRAGONOPS_TERRAFORM_ARTIFACT", "/app/tmpl.tgz.age")
		}

		log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Preparing Terraform")
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

		err = formatWithWorkerAndDestroy(ctx, accounts[0].AwsRegion, mm, group, execPath, roleToAssume, cfg, payload)
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

	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Finished destroying group Terraform! Cleaning up other resources...")

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
	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Deleting resources...")
	err = deleteResourcesFromDynamo(ctx, resources, mm, group, payload, cfg)
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

	sqsClient := sqs.NewFromConfig(cfg, func(o *sqs.Options) {
		o.Region = accounts[0].AwsRegion
	})
	_, err = sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      &queueUrl,
		ReceiptHandle: &receiptHandle,
	})
	if err != nil {
		return err
	}

	group.Status = "DESTROYED"
	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Group destroyed. Deleting record from DynamoDb.")
	o = mm.SoftDelete(&group)
	if o.Err != nil {
		return o.Err
	}
	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Finished deleting group.")
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
	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Templating Terraform with correct values")
	err := runWorkerGroupApply(mm, group, payload.JobId, masterAcctRegion)
	if err != nil {
		return err
	}

	terraformDirectoryPath := filepath.Join(os.Getenv("DRAGONOPS_TERRAFORM_DESTINATION"), fmt.Sprintf("group/%s", group.ResourceLabel))

	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Running terraform destroy for group.")
	_, err = terraform.DestroyTerraform(ctx, terraformDirectoryPath, *execPath, roleToAssume)
	if err != nil {
		return err
	}

	return nil
}
