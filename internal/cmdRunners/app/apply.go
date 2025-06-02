package app

import (
	"context"
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
	"sync"
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
		Str("AppID", payload.AppID).
		Msg("Looking for app with matching ID")

	app := types.App{}
	o := mm.Find(&app, payload.AppID)
	if o.Err != nil {
		return fmt.Errorf("an error occurred when trying to find the app with id %s: %s", payload.AppID, o.Err)
	}
	log.Debug().Str("AppID", app.ID).Msg("Found app")

	appEnvironmentsToApply := payload.EnvironmentNames
	log.Debug().Str("AppID", app.ID).Msg("Retrieved environments to apply")

	receiptHandle := os.Getenv("RECEIPT_HANDLE")
	if receiptHandle == "" {
		ue := utils.UpdateAllEnvironmentStatuses(app, appEnvironmentsToApply, "APPLY_FAILED", mm, fmt.Errorf("no RECEIPT_HANDLE variable found").Error())
		if ue != nil {
			return ue
		}
		return fmt.Errorf("Error retrieving RECEIPT_HANDLE from queue. Cannot continue.")
	}

	var accounts []types.Account
	o = mm.Where(&accounts, "IsMasterAccount", aws.Bool(true))
	if o.Err != nil {
		ue := utils.UpdateAllEnvironmentStatuses(app, appEnvironmentsToApply, "APPLY_FAILED", mm, o.Err.Error())
		if ue != nil {
			return ue
		}
		return fmt.Errorf("an error occurred when trying to find the MasterAccount: %s", o.Err)
	}
	log.Debug().Str("AppID", payload.AppID).Msg("Found Master Account")

	cfg, err := config.LoadDefaultConfig(ctx, func(options *config.LoadOptions) error {
		config.WithRegion(accounts[0].AwsRegion)
		return nil
	})
	if err != nil {
		ue := utils.UpdateAllEnvironmentStatuses(app, appEnvironmentsToApply, "APPLY_FAILED", mm, err.Error())
		if ue != nil {
			return ue
		}
		return err
	}

	// get the doApiKey from secrets manager, not the payload
	doApiKey, err := utils.GetDoApiKeyFromSecretsManager(ctx, cfg, payload.UserName)
	if err != nil {
		ue := utils.UpdateAllEnvironmentStatuses(app, appEnvironmentsToApply, "APPLY_FAILED", mm, err.Error())
		if ue != nil {
			return ue
		}
		return err
	}

	authResponse, err := utils.IsApiKeyValid(*doApiKey)
	if err != nil {
		ue := utils.UpdateAllEnvironmentStatuses(app, appEnvironmentsToApply, "APPLY_FAILED", mm, err.Error())
		if ue != nil {
			return ue
		}
		return fmt.Errorf("error verifying validity of DragonOps Api Key: %v", err)
	}

	if !authResponse.IsValid {
		ue := utils.UpdateAllEnvironmentStatuses(app, appEnvironmentsToApply, "APPLY_FAILED", mm, fmt.Errorf("the DragonOps api key provided is not valid. Please reach out to DragonOps support for help").Error())
		if ue != nil {
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

		log.Debug().Str("AppID", app.ID).Msg("Preparing Terraform")

		var execPath *string
		execPath, err = terraform.PrepareTerraform(ctx)
		if err != nil {
			ue := utils.UpdateAllEnvironmentStatuses(app, appEnvironmentsToApply, "APPLY_FAILED", mm, err.Error())
			if ue != nil {
				return ue
			}
			return err
		}

		log.Debug().Str("AppID", app.ID).Msg("Dry run is false. Running terraform")

		err = formatWithWorkerAndApply(ctx, accounts[0].AwsRegion, mm, app, appEnvironmentsToApply, execPath, &cfg)
		if err != nil {
			return err
		}
	}

	queueParts := strings.Split(*app.AppSqsArn, ":")
	queueUrl := fmt.Sprintf("https://%s.%s.amazonaws.com/%s/%s", queueParts[2], queueParts[3], queueParts[4], queueParts[5])

	log.Debug().Str("AppID", app.ID).Msg(fmt.Sprintf("Queue url is %s", queueUrl))

	_, err = sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      &queueUrl,
		ReceiptHandle: &receiptHandle,
	})
	if err != nil {
		return err
	}

	log.Info().Str("AppID", app.ID).Msg("Finished applying app!")
	// TODO github.com/aws/aws-sdk-go-v2/service/organizations --> to get the organization. if we don't have an organization.... i guess we can update the policy by getting it first, then adding the target account id to it.
	// so, 1. check for org. if exists, all good. set flag on master account saying IsOrganization
	// 2. if doesn't exist/not an org, set flag saying IsOrganization is false, see if target account is master account. If yes, just have policy say master account can pull. If NO, have policy saying master account & target account can pull
	// 3. every time we deploy to a new group, need to do this check if IsOrganization is false.
	return nil
}

func formatWithWorkerAndApply(ctx context.Context, masterAcctRegion string, mm *magicmodel.Operator, app types.App, environments []string, execPath *string, awsCfg *aws.Config) error {
	wg := &sync.WaitGroup{}
	errors := make(chan error, 0)

	for _, env := range environments {
		wg.Add(1)

		go func(e string) {
			defer wg.Done()

			var roleToAssume *string
			// TODO how does cross-account work with this new env stuff?
			//if env.Group.Account.CrossAccountRoleArn != nil {
			//	roleToAssume = env.Group.Account.CrossAccountRoleArn
			//}

			appEnvPath := fmt.Sprintf("/apps/%s/%s", app.ID, env)

			err := utils.RunWorkerAppApply(mm, app, appEnvPath, env, masterAcctRegion)
			if err != nil {
				ue := utils.UpdateSingleEnvironmentStatus(app, env, "APPLY_FAILED", mm, err.Error())
				if ue != nil {
					errors <- fmt.Errorf("error updating status for env %s: %v", env, err)
					return
				}
				errors <- fmt.Errorf("error for env %s: %v", env, err)
				return
			}

			var out map[string]tfexec.OutputMeta
			out, err = terraform.ApplyTerraform(ctx, fmt.Sprintf("%s/application", appEnvPath), *execPath, roleToAssume)
			if err != nil {
				ue := utils.UpdateSingleEnvironmentStatus(app, env, "APPLY_FAILED", mm, err.Error())
				if ue != nil {
					errors <- fmt.Errorf("error updating status for env %s: %v", env, ue)
				}
				errors <- fmt.Errorf("error for env %s: %v", env, err)
				return
			}

			log.Debug().Str("AppID", app.ID).Msg("Terraform applied! Updating app status")

			err = handleAppEnvironmentOutputs(ctx, app, env, mm, out, awsCfg, albZoneMap)
			o := mm.Save(&app)
			if o.Err != nil {
				errors <- fmt.Errorf("error updating status for env %s: %v", env, o.Err)
				return
			}

			err = utils.UpdateSingleEnvironmentStatus(app, env, "APPLIED", mm, "")
			if err != nil {
				errors <- fmt.Errorf("error updating status for env %s: %v", env, err)
				return
			}

			log.Debug().Str("AppID", app.ID).Msg("App status updated")
			return
		}(env)
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
		err := fmt.Errorf("errors occurred with applying environments for app %s: %v", app.ResourceLabel, errs)
		return err
	}
	return nil
}

func handleRoute53Domains(r53Domains []types.DomainNameConfig, cfOrAlbDnsName string, awsCfg *aws.Config, ctx context.Context, cfOrAlbZoneId string, appId string) error {
	for di := range r53Domains {
		// TODO when we support multi-account come back to this
		//if r53Domains[di].AwsAccountId != nil {
		// get the account that matches so that if it's not the master account we know and can search correctly
		//var accounts []types.Account
		//o := mm.WhereV2(false, &accounts, "AwsAccountId", *dnsAwsAccountId)
		//if o.Err != nil {
		//	ue := utils.UpdateAllEnvironmentStatuses(app, environments, mm, err)
		//	if ue != nil {
		//		return ue
		//	}
		//	return fmt.Errorf("Error for app with id %s and environment with id %s: %v", app.ID, env.ID, o.Err)
		//}
		//
		//if len(accounts) == 0 {
		//	ue := utils.UpdateAllEnvironmentStatuses(app, environments, mm, err)
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
						DNSName:      &cfOrAlbDnsName,
						HostedZoneId: &cfOrAlbZoneId,
					}
				} else if foundRecord.Type == r53Types.RRTypeCname {
					foundRecord.ResourceRecords = []r53Types.ResourceRecord{
						{
							Value: &cfOrAlbDnsName,
						},
					}
				} else {
					return fmt.Errorf("unsupported record type %s for domain %s", foundRecord.Type, r53Domains[di].DomainName)
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
					return err
				}
			}
			fmt.Println("Overwrite is false, skipping update for domain:", r53Domains[di].DomainName)
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
									DNSName:      &cfOrAlbDnsName,
									HostedZoneId: &cfOrAlbZoneId,
								},
							},
						},
					},
				},
				HostedZoneId: r53Domains[di].HostedZoneId,
			})
			if err != nil {
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
