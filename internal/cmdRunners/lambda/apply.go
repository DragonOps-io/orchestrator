package lambda

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
	"github.com/aws/aws-sdk-go-v2/service/route53"
	r53Types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/rs/zerolog/log"
	"os"
	"strings"
)

type AppUrl string
type CloudfrontDistroID string

var albZoneMap = map[string]string{
	"us-east-1": "Z35SXDOTRQ7X7K",
	"us-east-2": "Z3AADJGX6KTTL2",
	"us-west-1": "Z368ELLRRE2KJ0",
	"us-west-2": "Z1H1FL5HABSF5",
}

func Apply(ctx context.Context, payload Payload, mm *magicmodel.Operator, isDryRun bool) error {
	log.Debug().
		Str("LambdaID", payload.LambdaID).
		Msg("Looking for lambda with matching ID")

	app := types.Lambda{}
	o := mm.Find(&app, payload.LambdaID)
	if o.Err != nil {
		log.Err(o.Err).Str("LambdaID", payload.LambdaID).Msg("Error finding lambda")
		return fmt.Errorf("an error occurred when trying to find the lambda with id %s: %s", payload.LambdaID, o.Err)
	}
	log.Debug().Str("LambdaID", app.ID).Msg("Found lambda")

	var appEnvironmentsToApply []types.Environment
	for _, envId := range payload.EnvironmentIDs {
		env := types.Environment{}
		o = mm.Find(&env, envId)
		if o.Err != nil {
			log.Err(o.Err).Str("LambdaID", app.ID).Str("EnvironmentID", envId).Msg("Error finding environment")
			return o.Err
		}
		appEnvironmentsToApply = append(appEnvironmentsToApply, env)
	}
	log.Debug().Str("LambdaID", app.ID).Msg("Retrieved environments to apply")

	receiptHandle := os.Getenv("RECEIPT_HANDLE")
	if receiptHandle == "" {
		log.Err(o.Err).Str("LambdaID", app.ID).Msg("Error finding RECEIPT_HANDLE env var.")
		ue := updateEnvironmentStatusesToApplyFailed(app, appEnvironmentsToApply, mm, fmt.Errorf("no RECEIPT_HANDLE variable found"))
		if ue != nil {
			log.Err(o.Err).Str("LambdaID", app.ID).Msg(ue.Error())
			return ue
		}
		return fmt.Errorf("Error retrieving RECEIPT_HANDLE from queue. Cannot continue.")
	}

	var accounts []types.Account
	o = mm.Where(&accounts, "IsMasterAccount", aws.Bool(true))
	if o.Err != nil {
		log.Err(o.Err).Str("LambdaID", payload.LambdaID).Msg(o.Err.Error())
		ue := updateEnvironmentStatusesToApplyFailed(app, appEnvironmentsToApply, mm, o.Err)
		if ue != nil {
			log.Err(o.Err).Str("LambdaID", app.ID).Msg(ue.Error())
			return ue
		}
		return fmt.Errorf("an error occurred when trying to find the MasterAccount: %s", o.Err)
	}
	log.Debug().Str("LambdaID", payload.LambdaID).Msg("Found Master Account")

	cfg, err := config.LoadDefaultConfig(ctx, func(options *config.LoadOptions) error {
		config.WithRegion(accounts[0].AwsRegion)
		return nil
	})
	if err != nil {
		log.Err(o.Err).Str("LambdaID", payload.LambdaID).Msg(err.Error())
		ue := updateEnvironmentStatusesToApplyFailed(app, appEnvironmentsToApply, mm, err)
		if ue != nil {
			log.Err(o.Err).Str("LambdaID", app.ID).Msg(ue.Error())
			return ue
		}
		return err
	}

	// get the doApiKey from secrets manager, not the payload
	doApiKey, err := utils.GetDoApiKeyFromSecretsManager(ctx, cfg, payload.UserName)
	if err != nil {
		log.Err(o.Err).Str("LambdaID", payload.LambdaID).Msg(err.Error())
		ue := updateEnvironmentStatusesToApplyFailed(app, appEnvironmentsToApply, mm, err)
		if ue != nil {
			log.Err(o.Err).Str("LambdaID", app.ID).Msg(ue.Error())
			return ue
		}
		return err
	}

	authResponse, err := utils.IsApiKeyValid(*doApiKey)
	if err != nil {
		log.Err(o.Err).Str("LambdaID", payload.LambdaID).Msg(err.Error())
		ue := updateEnvironmentStatusesToApplyFailed(app, appEnvironmentsToApply, mm, err)
		if ue != nil {
			log.Err(o.Err).Str("LambdaID", app.ID).Msg(ue.Error())
			return ue
		}
		return fmt.Errorf("error verifying validity of DragonOps Api Key: %v", err)
	}

	if !authResponse.IsValid {
		log.Err(o.Err).Str("LambdaID", app.ID).Msg("Invalid do api key provided.")
		ue := updateEnvironmentStatusesToApplyFailed(app, appEnvironmentsToApply, mm, fmt.Errorf("the DragonOps api key provided is not valid. Please reach out to DragonOps support for help"))
		if ue != nil {
			log.Err(o.Err).Str("LambdaID", app.ID).Msg(ue.Error())
			return ue
		}
		return fmt.Errorf("The DragonOps api key provided is not valid. Please reach out to DragonOps support for help.")
	}

	sqsClient := sqs.NewFromConfig(cfg, func(o *sqs.Options) {
		o.Region = accounts[0].AwsRegion
	})

	if !isDryRun {
		if os.Getenv("IS_LOCAL") == "true" {
			os.Setenv("DRAGONOPS_TERRAFORM_ARTIFACT", "./app/tmpl.tgz.age")
		} else {
			os.Setenv("DRAGONOPS_TERRAFORM_ARTIFACT", "/app/tmpl.tgz.age")
		}

		log.Debug().Str("LambdaID", app.ID).Msg("Preparing Terraform")

		var execPath *string
		execPath, err = terraform.PrepareTerraform(ctx)
		if err != nil {
			log.Err(o.Err).Str("LambdaID", payload.LambdaID).Msg(err.Error())
			ue := updateEnvironmentStatusesToApplyFailed(app, appEnvironmentsToApply, mm, err)
			if ue != nil {
				log.Err(o.Err).Str("LambdaID", app.ID).Msg(ue.Error())
				return ue
			}
			return err
		}

		log.Debug().Str("LambdaID", app.ID).Msg("Dry run is false. Running terraform")

		// TODO how do i get the master account role arn and the org id
		// TODO can't get it from stytch, so i guess we need to make cross-account roles allow admin access from the master account
		err = formatWithWorkerAndApply(ctx, accounts[0].AwsRegion, mm, app, appEnvironmentsToApply, execPath, &cfg)
		if err != nil {
			log.Err(o.Err).Str("LambdaID", payload.LambdaID).Msg(err.Error())
			ue := updateEnvironmentStatusesToApplyFailed(app, appEnvironmentsToApply, mm, err)
			if ue != nil {
				log.Err(o.Err).Str("LambdaID", app.ID).Msg(ue.Error())
				return ue
			}
			return err
		}
	} else {
		err = updateEnvironmentStatusesToApplied(app, appEnvironmentsToApply, mm)
		if err != nil {
			log.Err(o.Err).Str("LambdaID", payload.LambdaID).Msg(err.Error())
			return err
		}
	}

	queueParts := strings.Split(*app.AppSqsArn, ":")
	queueUrl := fmt.Sprintf("https://%s.%s.amazonaws.com/%s/%s", queueParts[2], queueParts[3], queueParts[4], queueParts[5])

	log.Debug().Str("LambdaID", app.ID).Msg(fmt.Sprintf("Queue url is %s", queueUrl))

	_, err = sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      &queueUrl,
		ReceiptHandle: &receiptHandle,
	})
	if err != nil {
		log.Err(err).Str("LambdaID", app.ID).Msg(fmt.Sprintf("Error deleting message from queue: %s", err.Error()))
		return err
	}
	//err = updateEnvironmentStatusesToApplied(app, appEnvironmentsToApply, mm)
	//if err != nil {
	//	return err
	//}
	log.Info().Str("LambdaID", app.ID).Msg("Successfully applied app")
	// TODO github.com/aws/aws-sdk-go-v2/service/organizations --> to get the organization. if we don't have an organization.... i guess we can update the policy by getting it first, then adding the target account id to it.
	// so, 1. check for org. if exists, all good. set flag on master account saying IsOrganization
	// 2. if doesn't exist/not an org, set flag saying IsOrganization is false, see if target account is master account. If yes, just have policy say master account can pull. If NO, have policy saying master account & target account can pull
	// 3. every time we deploy to a new group, need to do this check if IsOrganization is false.
	return nil
}

func updateEnvironmentStatusesToApplyFailed(app types.Lambda, environmentsToApply []types.Environment, mm *magicmodel.Operator, err error) error {
	for _, env := range environmentsToApply {
		for idx := range app.Environments {
			if app.Environments[idx].Environment == env.ResourceLabel && app.Environments[idx].Group == env.Group.ResourceLabel && app.Environments[idx].Status == "APPLYING" {
				app.Environments[idx].Status = "APPLY_FAILED"
				app.Environments[idx].FailedReason = err.Error()
			}
		}
	}
	aco := mm.Update(&app, "Environments", app.Environments)
	if aco.Err != nil {
		return aco.Err
	}
	return nil
}

func updateEnvironmentStatusesToApplied(app types.Lambda, environmentsToApply []types.Environment, mm *magicmodel.Operator) error {
	for _, env := range environmentsToApply {
		for idx := range app.Environments {
			if app.Environments[idx].Environment == env.ResourceLabel && app.Environments[idx].Group == env.Group.ResourceLabel && app.Environments[idx].Status == "APPLYING" {
				app.Environments[idx].Status = "APPLIED"
				app.Environments[idx].FailedReason = ""
			}
		}
	}
	aco := mm.Update(&app, "Environments", app.Environments)
	if aco.Err != nil {
		return aco.Err
	}
	return nil
}

func formatWithWorkerAndApply(ctx context.Context, masterAcctRegion string, mm *magicmodel.Operator, app types.Lambda, environments []types.Environment, execPath *string, awsCfg *aws.Config) error {
	log.Debug().Str("LambdaID", app.ID).Msg("Templating Terraform with correct values")

	for _, env := range environments {
		appPath := fmt.Sprintf("/lambdas/%s/%s", app.ID, env.ID)
		command := fmt.Sprintf("/app/worker lambda apply --lambda-id %s --environment-id %s --table-region %s", app.ID, env.ID, masterAcctRegion)
		os.Setenv("DRAGONOPS_TERRAFORM_DESTINATION", appPath)

		if os.Getenv("IS_LOCAL") == "true" {
			appPath = fmt.Sprintf("./lambdas/%s/%s", app.ID, env.ID)
			os.Setenv("DRAGONOPS_TERRAFORM_DESTINATION", appPath)
			command = fmt.Sprintf("./app/worker lambda apply --lambda-id %s --environment-id %s --table-region %s", app.ID, env.ID, masterAcctRegion)
		}

		log.Debug().Str("LambdaID", app.ID).Msg(appPath)
		log.Debug().Str("LambdaID", app.ID).Msg(fmt.Sprintf("Applying lambda files found at %s", os.Getenv("DRAGONOPS_TERRAFORM_DESTINATION")))

		log.Debug().Str("LambdaID", app.ID).Msg(fmt.Sprintf("Running command %s", command))
		msg, err := utils.RunOSCommandOrFail(command)
		if err != nil {
			ue := updateEnvironmentStatusesToApplyFailed(app, environments, mm, fmt.Errorf("Error running `worker lambda apply` with app with id %s and environment with id %s: %v - %v", app.ID, env.ID, err, msg))
			if ue != nil {
				return ue
			}
			return fmt.Errorf("Error running `worker lambda apply` with app with id %s and environment with id %s: %v - %v", app.ID, env.ID, err, msg)
		}
		log.Debug().Str("LambdaID", app.ID).Msg(*msg)

		var roleToAssume *string
		if env.Group.Account.CrossAccountRoleArn != nil {
			roleToAssume = env.Group.Account.CrossAccountRoleArn
		}

		var out map[string]tfexec.OutputMeta
		out, err = terraform.ApplyTerraform(ctx, fmt.Sprintf("%s/lambda", appPath), *execPath, roleToAssume)
		if err != nil {
			ue := updateEnvironmentStatusesToApplyFailed(app, environments, mm, err)
			if ue != nil {
				return ue
			}
			return fmt.Errorf("Error running apply with lambda with id %s and environment with id %s: %v", app.ID, env.ID, err)
		}

		log.Debug().Str("LambdaID", app.ID).Msg("Updating app status")

		for idx := range app.Environments {
			if app.Environments[idx].Environment == env.ResourceLabel && app.Environments[idx].Group == env.Group.ResourceLabel {
				app.Environments[idx].Status = "APPLIED"
				app.Environments[idx].FailedReason = ""
				var appUrl AppUrl
				if err = json.Unmarshal(out["app_url"].Value, &appUrl); err != nil {
					fmt.Printf("Error decoding output value for key %s: %s\n", "app_url", err)
				}
				app.Environments[idx].Endpoint = string(appUrl)

				// handle the dashboard outputs
				// TODO this is awkward because the dashboard url is per environment but really shouldn't be
				var appDashboardUrl string
				if err = json.Unmarshal(out["app_dashboard_url"].Value, &appDashboardUrl); err != nil {
					fmt.Printf("Error decoding output value for key %s: %s\n", "app_dashboard_url", err)
				}

				app.ObservabilityUrls = &types.ObservabilityUrls{
					UnifiedDashboard: appDashboardUrl,
				}

				if app.SubType == "server" {
					// TODO
					//err = handleRoute53Domains(app.Environments[idx].Route53DomainNames, cluster.AlbDnsName, awsCfg, ctx, albZoneMap[env.Group.Account.Region], app.ID)
					//if err != nil {
					//	ue := updateEnvironmentStatusesToApplyFailed(app, environments, mm, err)
					//	if ue != nil {
					//		return ue
					//	}
					//	return fmt.Errorf("Error handling route53 domains for app with id %s and environment with id %s: %v", app.ID, env.ID, err)
					//}
				}
				break
			}
		}
		o := mm.Save(&app)
		if o.Err != nil {
			return o.Err
		}
		log.Debug().Str("LambdaID", app.ID).Msg("App status updated")
	}
	return nil
}

func handleRoute53Domains(r53Domains []types.DomainNameConfig, cfDnsName string, awsCfg *aws.Config, ctx context.Context, cfOrAlbZoneId string, LambdaID string) error {
	for di := range r53Domains {
		// TODO when we support multi-account come back to this
		//if r53Domains[di].AwsAccountId != nil {
		// get the account that matches so that if it's not the master account we know and can search correctly
		//var accounts []types.Account
		//o := mm.WhereV2(false, &accounts, "AwsAccountId", *dnsAwsAccountId)
		//if o.Err != nil {
		//	ue := updateEnvironmentStatusesToApplyFailed(app, environments, mm, err)
		//	if ue != nil {
		//		return ue
		//	}
		//	return fmt.Errorf("Error for app with id %s and environment with id %s: %v", app.ID, env.ID, o.Err)
		//}
		//
		//if len(accounts) == 0 {
		//	ue := updateEnvironmentStatusesToApplyFailed(app, environments, mm, err)
		//	if ue != nil {
		//		return ue
		//	}
		//	return fmt.Errorf("Error for app with id %s and environment with id %s: %v", app.ID, env.ID, o.Err)
		//}

		//if !*accounts[0].IsMasterAccount {
		//	// assume master account role arn
		//	masterAcctStsClient := sts.NewFromConfig(*awsCfg)
		//	masterAccountRoleOutput, err := masterAcctStsClient.AssumeRole(ctx, &sts.AssumeRoleInput{
		//		RoleArn:         aws.String(masterAccountRoleArn),
		//		RoleSessionName: aws.String("dragonops-" + strconv.Itoa(10000+rand.Intn(25000))),
		//		ExternalId:      aws.String(orgId),
		//	})
		//	if err != nil {
		//		return err
		//	}
		//
		//	loadOptions := []func(options *config.LoadOptions) error{
		//		config.WithRegion(accounts[0].AwsRegion),
		//		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
		//			*masterAccountRoleOutput.Credentials.AccessKeyId,
		//			*masterAccountRoleOutput.Credentials.SecretAccessKey,
		//			*masterAccountRoleOutput.Credentials.SessionToken)),
		//	}
		//
		//	// create account config and set to awsConfig
		//	dnsAccountCfg, err := config.LoadDefaultConfig(ctx, loadOptions...)
		//	if err != nil {
		//		// TODO
		//		return err
		//	}
		//	awsCfg = &dnsAccountCfg
		//}
		//}

		// check to see if record exists and if overwrite is true, then overwrite any existing record.
		dnsClient := route53.NewFromConfig(*awsCfg)
		foundRecord, err := findMatchingRecordSet(dnsClient, ctx, *r53Domains[di].HostedZoneId, r53Domains[di].DomainName)
		if err != nil {
			return err
		}
		if foundRecord != nil {
			// update the record but only if overwrite is true
			if r53Domains[di].Overwrite != nil && *r53Domains[di].Overwrite {
				if foundRecord.Type == r53Types.RRTypeA {
					foundRecord.AliasTarget = &r53Types.AliasTarget{
						DNSName:      &cfDnsName,
						HostedZoneId: &cfOrAlbZoneId,
					}
				} else if foundRecord.Type == r53Types.RRTypeCname {
					foundRecord.ResourceRecords = []r53Types.ResourceRecord{
						{
							Value: &cfDnsName,
						},
					}
				} else {
					log.Err(fmt.Errorf("The route53 record found is not one of A or CNAME. Cannot continue with overwrite.")).Str("LambdaID", LambdaID).Msg("Unsupported record type")
				}

				_, err = dnsClient.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
					ChangeBatch: &r53Types.ChangeBatch{
						Changes: []r53Types.Change{
							{
								Action:            r53Types.ChangeActionUpsert,
								ResourceRecordSet: foundRecord,
							},
						},
					},
					HostedZoneId: r53Domains[di].HostedZoneId,
				})
				if err != nil {
					log.Err(err).Str("LambdaID", LambdaID).Msg(err.Error())
					return err
				}
			}
		} else {
			_, err = dnsClient.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
				ChangeBatch: &r53Types.ChangeBatch{
					Changes: []r53Types.Change{
						{
							Action: r53Types.ChangeActionCreate,
							ResourceRecordSet: &r53Types.ResourceRecordSet{
								Name: &r53Domains[di].DomainName,
								Type: r53Types.RRTypeA,
								AliasTarget: &r53Types.AliasTarget{
									DNSName:      &cfDnsName,
									HostedZoneId: &cfOrAlbZoneId,
								},
							},
						},
					},
				},
				HostedZoneId: r53Domains[di].HostedZoneId,
			})
			if err != nil {
				log.Err(err).Str("LambdaID", LambdaID).Msg(err.Error())
				return err
			}
		}
	}
	return nil
}

func findMatchingRecordSet(dnsClient *route53.Client, ctx context.Context, hostedZoneId string, domainName string) (*r53Types.ResourceRecordSet, error) {
	recordsOut, err := dnsClient.ListResourceRecordSets(ctx, &route53.ListResourceRecordSetsInput{HostedZoneId: &hostedZoneId})
	if err != nil {
		return nil, err
	}
	for i := range recordsOut.ResourceRecordSets {
		if *recordsOut.ResourceRecordSets[i].Name == domainName+"." {
			return &recordsOut.ResourceRecordSets[i], nil
		}
	}
	if recordsOut.NextRecordName != nil {
		return findMatchingRecordSet(dnsClient, ctx, hostedZoneId, domainName)
	}
	return nil, nil
}
