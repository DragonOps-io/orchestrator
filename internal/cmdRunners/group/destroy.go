package group

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DragonOps-io/orchestrator/internal/terraform"
	"github.com/DragonOps-io/orchestrator/internal/utils"
	"github.com/DragonOps-io/types"
	magicmodel "github.com/Ilios-LLC/magicmodel-go/model"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2Types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	tagTypes "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	r53Types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/rs/zerolog/log"
)

func Destroy(ctx context.Context, payload Payload, mm *magicmodel.Operator, isDryRun bool) error {
	log.Debug().
		Str("GroupID", payload.GroupID).
		Str("JobId", payload.JobId).
		Msg("Attempting to destroy group")

	group := types.Group{}
	o := mm.Find(&group, payload.GroupID)
	if o.Err != nil {
		log.Err(o.Err).Str("GroupID", payload.GroupID).Str("JobId", payload.JobId).Msg("Error finding group")
		return fmt.Errorf("Error when trying to retrieve group with id %s: %s", payload.GroupID, o.Err)
	}
	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Found group")

	receiptHandle := os.Getenv("RECEIPT_HANDLE")
	if receiptHandle == "" {
		log.Err(fmt.Errorf("no RECEIPT_HANDLE variable found")).Str("GroupID", group.ID).Msg("Error retrieving RECEIPT_HANDLE from queue. Cannot continue.")
		aco := mm.Update(&group, "Status", "DESTROY_FAILED")
		if aco.Err != nil {
			log.Err(aco.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Error updating group status")
			return o.Err
		}
		aco = mm.Update(&group, "FailedReason", "No RECEIPT_HANDLE variable found.")
		if aco.Err != nil {
			log.Err(aco.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Error updating group status")
			return aco.Err
		}
		return fmt.Errorf("Error retrieving RECEIPT_HANDLE from queue. Cannot continue.")
	}

	var accounts []types.Account
	o = mm.Where(&accounts, "IsMasterAccount", aws.Bool(true))
	if o.Err != nil {
		log.Err(o.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Error updating group status")
		aco := mm.Update(&group, "Status", "DESTROY_FAILED")
		if aco.Err != nil {
			log.Err(aco.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Error updating group status")
			return aco.Err
		}
		o = mm.Update(&group, "FailedReason", o.Err.Error())
		if o.Err != nil {
			log.Err(o.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Error updating group status")
			return o.Err
		}
		return fmt.Errorf("an error occurred when trying to find the MasterAccount: %s", o.Err)
	}
	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Found MasterAccount")

	cfg, err := config.LoadDefaultConfig(ctx, func(options *config.LoadOptions) error {
		config.WithRegion(accounts[0].AwsRegion)
		return nil
	})
	if err != nil {
		log.Err(err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Error loading default config")
		aco := mm.Update(&group, "Status", "APPLY_FAILED")
		if aco.Err != nil {
			log.Err(aco.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Error updating group status")
			return aco.Err
		}
		aco = mm.Update(&group, "FailedReason", err.Error())
		if aco.Err != nil {
			log.Err(aco.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Error updating group status")
			return aco.Err
		}
		return err
	}

	// get the doApiKey from secrets manager, not the payload
	doApiKey, err := utils.GetDoApiKeyFromSecretsManager(ctx, cfg, payload.UserName)
	if err != nil {
		log.Err(err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(err.Error())
		aco := mm.Update(&group, "Status", "APPLY_FAILED")
		if aco.Err != nil {
			log.Err(aco.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Error updating group status")
			return aco.Err
		}
		aco = mm.Update(&group, "FailedReason", err.Error())
		if aco.Err != nil {
			log.Err(aco.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Error updating group status")
			return aco.Err
		}
		return err
	}

	authResponse, err := utils.IsApiKeyValid(*doApiKey)
	if err != nil {
		log.Err(err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(err.Error())
		aco := mm.Update(&group, "Status", "DESTROY_FAILED")
		if aco.Err != nil {
			log.Err(aco.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Error updating group status")
			return aco.Err
		}
		return fmt.Errorf("error verifying validity of DragonOps Api Key: %v", err)
	}

	if !authResponse.IsValid {
		log.Err(fmt.Errorf("The DragonOps api key provided is not valid. Please reach out to DragonOps support for help.")).Str("GroupID", group.ID).Msg("The DragonOps api key provided is not valid. Please reach out to DragonOps support for help.")
		aco := mm.Update(&group, "Status", "DESTROY_FAILED")
		if aco.Err != nil {
			log.Err(aco.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Error updating group status")
			return aco.Err
		}
		aco = mm.Update(&group, "FailedReason", "The DragonOps api key provided is not valid. Please reach out to DragonOps support for help.")
		if aco.Err != nil {
			log.Err(aco.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Error updating group status")
			return aco.Err
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
			log.Err(err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Error assuming cross account role")
			o = mm.Update(&group, "Status", "DESTROY_FAILED")
			if o.Err != nil {
				log.Err(o.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Error updating group status")
				return o.Err
			}
			o = mm.Update(&group, "FailedReason", err.Error())
			if o.Err != nil {
				log.Err(o.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Error updating group status")
				return o.Err
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
			log.Err(err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(err.Error())
			o = mm.Update(&group, "Status", "DESTROY_FAILED")
			if o.Err != nil {
				log.Err(o.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Error updating group status")
				return o.Err
			}
			o = mm.Update(&group, "FailedReason", err.Error())
			if o.Err != nil {
				log.Err(o.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Error updating group status")
				return o.Err
			}
			return err
		}

		err = formatWithWorkerAndDestroy(ctx, accounts[0].AwsRegion, mm, group, execPath, roleToAssume, cfg, payload)
		if err != nil {
			log.Err(err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(err.Error())
			o = mm.Update(&group, "Status", "DESTROY_FAILED")
			if o.Err != nil {
				log.Err(o.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Error updating group status")
				return o.Err
			}
			o = mm.Update(&group, "FailedReason", err.Error())
			if o.Err != nil {
				log.Err(o.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Error updating group status")
				return o.Err
			}
			return err
		}
	}

	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Finished destroying group Terraform! Cleaning up other resources now.")

	if group.DragonOpsRoute53 != nil {
		route53Client := route53.NewFromConfig(cfg, func(o *route53.Options) {
			o.Region = accounts[0].AwsRegion
		})
		log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Deleting hosted zone.")
		_, err = route53Client.DeleteHostedZone(ctx, &route53.DeleteHostedZoneInput{Id: &group.DragonOpsRoute53.HostedZoneId})
		if err != nil {
			log.Err(err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Error deleting hosted zone.")
			if strings.Contains(err.Error(), fmt.Sprintf("The specified hosted zone contains non-required resource record sets and so cannot be deleted")) {
				// delete all non-SOA and Name server records
				// TODO need to paginate?
				var output *route53.ListResourceRecordSetsOutput
				output, err = route53Client.ListResourceRecordSets(ctx, &route53.ListResourceRecordSetsInput{
					HostedZoneId: &group.DragonOpsRoute53.HostedZoneId,
				})
				if err != nil {
					log.Err(err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Error listing resource record sets.")
					o = mm.Update(&group, "Status", "DESTROY_FAILED")
					if o.Err != nil {
						log.Err(o.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Error updating group status")
						return o.Err
					}
					o = mm.Update(&group, "FailedReason", err.Error())
					if o.Err != nil {
						log.Err(o.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Error updating group status")
						return o.Err
					}
					return err
				}
				for _, record := range output.ResourceRecordSets {
					_, err = route53Client.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
						ChangeBatch: &r53Types.ChangeBatch{
							Changes: []r53Types.Change{{
								Action:            r53Types.ChangeActionDelete,
								ResourceRecordSet: &record}},
						},
						HostedZoneId: &group.DragonOpsRoute53.HostedZoneId,
					})
					if err != nil {
						log.Err(err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Error deleting resource record set.")
					}
				}
			} else if !strings.Contains(err.Error(), fmt.Sprintf("No hosted zone found with ID: %s", group.DragonOpsRoute53.HostedZoneId)) {
				log.Err(err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Error deleting hosted zone.")
				o = mm.Update(&group, "Status", "DESTROY_FAILED")
				if o.Err != nil {
					log.Err(o.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Error updating group status")
					return o.Err
				}
				o = mm.Update(&group, "FailedReason", err.Error())
				if o.Err != nil {
					log.Err(o.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Error updating group status")
					return o.Err
				}
				return err
			}
			err = nil
		}

		log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Deleting name servers from DragonOps.")
		err = deleteNameServersFromDragonOps(*doApiKey, authResponse.MasterAccountAccessRoleArn, authResponse.MasterAccountRegion, authResponse.Team, group.DragonOpsRoute53.NameServers, group.DragonOpsRoute53.RootDomain)
		if err != nil {
			log.Err(err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Error deleting name servers from DragonOps.")
			o = mm.Update(&group, "Status", "DESTROY_FAILED")
			if o.Err != nil {
				log.Err(o.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Error updating group status")
				return o.Err
			}
			o = mm.Update(&group, "FailedReason", err.Error())
			if o.Err != nil {
				log.Err(o.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Error updating group status")
				return o.Err
			}
			return err
		}
	}

	// Get all clusters, networks, and environments where GroupID is X and delete them
	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Retrieving all cluster, network and environment records to delete.")
	var clusters []types.Cluster
	o = mm.Where(&clusters, "Group.ID", group.ID)
	if o.Err != nil {
		log.Err(o.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(o.Err.Error())
		return o.Err
	}
	for _, cluster := range clusters {
		log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Deleting cluster %s record from DynamoDb.", cluster.Name))
		o = mm.SoftDelete(&cluster)
		if o.Err != nil {
			log.Err(o.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(o.Err.Error())
			return o.Err
		}
	}

	var networks []types.Network
	o = mm.Where(&networks, "Group.ID", group.ID)
	if o.Err != nil {
		log.Err(o.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(o.Err.Error())
		return o.Err
	}
	for _, network := range networks {
		client := ssm.NewFromConfig(cfg)
		log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Deleting network parameters for network %s.", network.Name))
		_, err = client.DeleteParameters(ctx, &ssm.DeleteParametersInput{
			Names: []string{
				fmt.Sprintf("/%s/wireguard/public_key", network.ID),
				fmt.Sprintf("/%s/wireguard/private_key", network.ID),
				fmt.Sprintf("/%s/wireguard/config_file", network.ID),
			},
		})
		if err != nil {
			return err
		}

		var clients []types.VpnClient
		operator := mm.All(&clients)
		if operator.Err != nil {
			return operator.Err
		}

		for _, c := range clients {
			for j := 0; j < len(c.Networks); j++ {
				if c.Networks[j].ID == network.ID {
					c.Networks = append(c.Networks[:j], c.Networks[j+1:]...)
					// Adjust the loop index since we modified the slice
					j--
				}
			}
		}

		log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Deleting network %s record from DynamoDb.", network.Name))
		o = mm.SoftDelete(&network)
		if o.Err != nil {
			log.Err(o.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(o.Err.Error())
			return o.Err
		}
	}

	var environments []types.Environment
	o = mm.Where(&environments, "Group.ID", group.ID)
	if o.Err != nil {
		log.Err(o.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(o.Err.Error())
		return o.Err
	}
	for _, env := range environments {
		log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Deleting environment %s record from DynamoDb.", env.Name))
		o = mm.SoftDelete(&env)
		if o.Err != nil {
			log.Err(o.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(o.Err.Error())
			return o.Err
		}
	}

	queueParts := strings.Split(group.Account.GroupSqsArn, ":")
	queueUrl := fmt.Sprintf("https://%s.%s.amazonaws.com/%s/%s", queueParts[2], queueParts[3], queueParts[4], queueParts[5])

	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Queue url is %s", queueUrl))

	sqsClient := sqs.NewFromConfig(cfg, func(o *sqs.Options) {
		o.Region = accounts[0].AwsRegion
	})
	_, err = sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      &queueUrl,
		ReceiptHandle: &receiptHandle,
	})
	if err != nil {
		log.Err(err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(err.Error())
		aco := mm.Update(&group, "Status", "DESTROY_FAILED")
		if aco.Err != nil {
			log.Err(aco.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Error updating group status")
			return aco.Err
		}
		o = mm.Update(&group, "FailedReason", err.Error())
		if o.Err != nil {
			log.Err(o.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Error updating group status")
			return o.Err
		}
		return err
	}
	aco := mm.Update(&group, "Status", "DESTROYED")
	if aco.Err != nil {
		log.Err(aco.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Error updating group status")
		return aco.Err
	}
	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Group destroyed. Deleting record from DynamoDb.")
	o = mm.SoftDelete(&group)
	if o.Err != nil {
		log.Err(o.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(o.Err.Error())
		return o.Err
	}
	return nil
}

type addNameServersPayload struct {
	NameServers []string `json:"name_servers"`
	Domain      string   `json:"domain"`
}

func deleteNameServersFromDragonOps(apiKey string, roleArn string, region string, orgId int, nameServers []string, domain string) error {
	postBody := addNameServersPayload{
		NameServers: nameServers,
		Domain:      domain,
	}

	jsonBody, err := json.Marshal(postBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/name-servers/delete", os.Getenv("DRAGONOPS_API")), bytes.NewBuffer(jsonBody))
	if err != nil {
		return err
	}

	q := req.URL.Query()
	q.Add("master_account_region", region)
	q.Add("role_arn", roleArn)
	q.Add("org_id", "0"+strconv.Itoa(orgId))

	req.URL.RawQuery = q.Encode()
	req.Header.Add("do-api-key", apiKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		if strings.Contains(string(body), "but it was not found") {
			return nil
		}
		return fmt.Errorf("Error deleting name servers from DragonOps. Error code: %d, Message: %s.", resp.StatusCode, body)
	}
	return nil
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

	command := fmt.Sprintf("/app/worker group apply --group-id %s --table-region %s", group.ID, masterAcctRegion)
	if os.Getenv("IS_LOCAL") == "true" {
		command = fmt.Sprintf("./app/worker group apply --group-id %s --table-region %s", group.ID, masterAcctRegion)
	}

	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Running command %s", command))
	msg, err := utils.RunOSCommandOrFail(command)
	if err != nil {
		o := mm.Update(&group, "Status", "DESTROY_FAILED")
		if o.Err != nil {
			return o.Err
		}
		o = mm.Update(&group, "FailedReason", err.Error())
		if o.Err != nil {
			return o.Err
		}
		return fmt.Errorf("Error running `worker group apply` for group with id %s: %s: %s", group.ID, err, *msg)
	}
	err = destroy(ctx, mm, group, execPath, roleToAssume, "environment-static", cfg, payload)
	if err != nil {
		o := mm.Update(&group, "Status", "DESTROY_FAILED")
		if o.Err != nil {
			return o.Err
		}
		o = mm.Update(&group, "FailedReason", err.Error())
		if o.Err != nil {
			return o.Err
		}
		return fmt.Errorf("Error running destroy for environment-static stacks in group with id %s: %s: %s", group.ID, err, *msg)
	}
	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Destroying group Terraform")
	// can't use a for loop because we need to do it in order
	// destroy environments all together
	err = destroy(ctx, mm, group, execPath, roleToAssume, "environment", cfg, payload)
	if err != nil {
		o := mm.Update(&group, "Status", "DESTROY_FAILED")
		if o.Err != nil {
			return o.Err
		}
		o = mm.Update(&group, "FailedReason", err.Error())
		if o.Err != nil {
			return o.Err
		}
		return fmt.Errorf("Error running destroy for environment stacks in group with id %s: %s: %s", group.ID, err, *msg)
	}
	err = destroy(ctx, mm, group, execPath, roleToAssume, "rds", cfg, payload)
	if err != nil {
		o := mm.Update(&group, "Status", "DESTROY_FAILED")
		if o.Err != nil {
			return o.Err
		}
		o = mm.Update(&group, "FailedReason", err.Error())
		if o.Err != nil {
			return o.Err
		}
		return fmt.Errorf("Error running destroy for rds stacks in group with id %s: %s: %s", group.ID, err, *msg)
	}
	// destroy cluster grafana all together
	err = destroy(ctx, mm, group, execPath, roleToAssume, "cluster_grafana", cfg, payload)
	if err != nil {
		o := mm.Update(&group, "Status", "DESTROY_FAILED")
		if o.Err != nil {
			return o.Err
		}
		o = mm.Update(&group, "FailedReason", err.Error())
		if o.Err != nil {
			return o.Err
		}
		return fmt.Errorf("Error running destroy for cluster_grafana stacks in group with id %s: %s: %s", group.ID, err, *msg)
	}

	// destroy clusters all together
	err = destroy(ctx, mm, group, execPath, roleToAssume, "cluster", cfg, payload)
	if err != nil {
		o := mm.Update(&group, "Status", "DESTROY_FAILED")
		if o.Err != nil {
			return o.Err
		}
		o = mm.Update(&group, "FailedReason", err.Error())
		if o.Err != nil {
			return o.Err
		}
		return fmt.Errorf("Error running destroy for cluster stacks in group with id %s: %s: %s", group.ID, err, *msg)
	}
	// destroy all networks together
	err = destroy(ctx, mm, group, execPath, roleToAssume, "network", cfg, payload)
	if err != nil {
		o := mm.Update(&group, "Status", "DESTROY_FAILED")
		if o.Err != nil {
			return o.Err
		}
		o = mm.Update(&group, "FailedReason", err.Error())
		if o.Err != nil {
			return o.Err
		}
		return fmt.Errorf("Error running destroy for network stacks in group with id %s: %s: %s", group.ID, err, *msg)
	}

	return nil
}

func destroy(ctx context.Context, mm *magicmodel.Operator, group types.Group, execPath *string, roleToAssume *string, dirName string, cfg aws.Config, payload Payload) error {
	directoryPath := filepath.Join(os.Getenv("DRAGONOPS_TERRAFORM_DESTINATION"), dirName)
	directories, _ := os.ReadDir(directoryPath)
	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Destroying all %ss", dirName))

	// go routine setup stuff
	wg := &sync.WaitGroup{}
	errors := make(chan error, 0)
	cancelCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, d := range directories {
		wg.Add(1)
		log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Destroying %s %s", dirName, d.Name()))
		path, _ := filepath.Abs(filepath.Join(directoryPath, d.Name()))

		go func(dir os.DirEntry) {
			defer wg.Done()
			if strings.Contains(path, "cluster") && !strings.Contains(path, "cluster_grafana") {
				fmt.Println(dir.Name())
				clusterResourceLabel := strings.Split(strings.Split(path, "/groups/")[1], "/")[2]
				// setup the client before the go routine?
				ec2Client := ec2.NewFromConfig(cfg, func(o *ec2.Options) { o.Region = cfg.Region })
				tagClient := resourcegroupstaggingapi.NewFromConfig(cfg, func(o *resourcegroupstaggingapi.Options) { o.Region = cfg.Region })
				// get the cluster so we have the name
				var clusters []types.Cluster
				var cluster *types.Cluster
				o := mm.Where(&clusters, "ResourceLabel", clusterResourceLabel)
				if o.Err != nil {
					log.Warn().Msg(fmt.Sprintf("error getting clusters: %s", o.Err.Error()))
				}
				for idx := range clusters {
					if clusters[idx].Group.ResourceLabel == group.ResourceLabel {
						cluster = &clusters[idx]
					}
				}
				if cluster == nil {
					// cluster not found what then?
					log.Warn().Str("GroupID", group.ID).Msg("cluster not found and cannot delete spot instances")
				} else {
					go func() {
						select {
						case <-cancelCtx.Done():
							return
						default:
						}
						time.Sleep(30 * time.Second)
						deleteSpotInstances(cancelCtx, group.Name, group.ID, cluster.Name, ec2Client, tagClient, payload)
					}()
				}
			}
			// destroy terraform or return an error
			_, err := terraform.DestroyTerraform(ctx, path, *execPath, roleToAssume)
			if err != nil {
				errors <- fmt.Errorf("error for %s %s: %v", dirName, dir.Name(), err)
				return
			}
			return
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
		err := fmt.Errorf("errors occurred with destroying resources in group %s: %v", group.ResourceLabel, errs)
		return err
	}
	return nil
}

func deleteSpotInstances(ctx context.Context, groupName string, groupId string, clusterName string, ec2Client *ec2.Client, taggingClient *resourcegroupstaggingapi.Client, payload Payload) {
	// first retrieve all instances with sepcific tag
	instanceIds := getSpotInstanceIds(ctx, taggingClient, groupName, clusterName)
	log.Info().Str("GroupID", groupId).Msg(fmt.Sprintf("Found %d instances to delete: %v", len(instanceIds), instanceIds))
	if len(instanceIds) > 0 {
		_, err := ec2Client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
			InstanceIds: instanceIds,
		})
		if err != nil {
			log.Warn().Str("GroupID", groupId).Msg(err.Error())
			err = nil
		}
	}

	// get the eni ids and try to delete
	eniIds := getEniIds(ctx, ec2Client, groupName, clusterName)
	for _, eniId := range eniIds {
		log.Debug().Str("GroupID", groupId).Str("JobId", payload.JobId).Msg("attempting to delete ENIs")
		_, err := ec2Client.DeleteNetworkInterface(ctx, &ec2.DeleteNetworkInterfaceInput{
			NetworkInterfaceId: &eniId,
		})
		if err != nil {
			log.Warn().Str("GroupID", groupId).Msg(err.Error())
			err = nil
		}
	}

	// get the security group ids
	sgIds := getSecurityGroupIds(ctx, ec2Client, groupName, clusterName)
	// try to delete the security group
	for _, sgId := range sgIds {
		_, err := ec2Client.DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{
			GroupId: &sgId,
		})
		if err != nil {
			log.Warn().Str("GroupID", groupId).Msg(err.Error())
			err = nil
		}
	}

	time.Sleep(30 * time.Second)
	deleteSpotInstances(ctx, groupName, groupId, clusterName, ec2Client, taggingClient, payload)
}

func getEniIds(ctx context.Context, ec2Client *ec2.Client, groupName string, clusterName string) []string {
	var eniIds []string
	paginator := ec2.NewDescribeNetworkInterfacesPaginator(ec2Client, &ec2.DescribeNetworkInterfacesInput{
		Filters: []ec2Types.Filter{{
			Name:   aws.String("tag:cluster.k8s.amazonaws.com/name"),
			Values: []string{fmt.Sprintf("%s-%s", groupName, clusterName)},
		}}})

	eniPageNum := 0
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			err = nil
			continue
		}
		for _, value := range output.NetworkInterfaces {
			eniIds = append(eniIds, *value.NetworkInterfaceId)
		}
		eniPageNum++
	}
	return eniIds
}

func getSecurityGroupIds(ctx context.Context, ec2Client *ec2.Client, groupName string, clusterName string) []string {
	var sgIds []string
	sgPaginator := ec2.NewDescribeSecurityGroupsPaginator(ec2Client, &ec2.DescribeSecurityGroupsInput{
		Filters: []ec2Types.Filter{{
			Name:   aws.String("tag:aws:eks:cluster-name"),
			Values: []string{fmt.Sprintf("%s-%s", groupName, clusterName)},
		}},
	}, func(o *ec2.DescribeSecurityGroupsPaginatorOptions) {
		o.StopOnDuplicateToken = true
	})

	pageNum := 0
	for sgPaginator.HasMorePages() {
		output, err := sgPaginator.NextPage(ctx)
		if err != nil {
			err = nil
			continue
		}
		for _, value := range output.SecurityGroups {
			sgIds = append(sgIds, *value.GroupId)
		}
		pageNum++
	}
	return sgIds
}

func getSpotInstanceIds(ctx context.Context, taggingClient *resourcegroupstaggingapi.Client, groupName string, clusterName string) []string {
	var instanceIds []string
	paginator := resourcegroupstaggingapi.NewGetResourcesPaginator(taggingClient, &resourcegroupstaggingapi.GetResourcesInput{
		ResourceTypeFilters: []string{"ec2:instance"},
		TagFilters: []tagTypes.TagFilter{{
			Key:    aws.String("aws:eks:cluster-name"),
			Values: []string{fmt.Sprintf("%s-%s", groupName, clusterName)},
		}},
	}, func(o *resourcegroupstaggingapi.GetResourcesPaginatorOptions) {
		o.StopOnDuplicateToken = true
	})

	pageNum := 0
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			err = nil
			continue
		}
		for _, value := range output.ResourceTagMappingList {
			instanceIds = append(instanceIds, strings.Split(*value.ResourceARN, "/")[1])
		}
		pageNum++
	}
	return instanceIds
}
