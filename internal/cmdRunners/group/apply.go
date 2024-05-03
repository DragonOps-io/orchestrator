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
	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/rs/zerolog/log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

func Apply(ctx context.Context, payload Payload, mm *magicmodel.Operator, isDryRun bool) error {
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

	authResponse, err := utils.IsApiKeyValid(payload.DoApiKey)
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
		err = formatWithWorkerAndApply(ctx, accounts[0].AwsRegion, mm, group, execPath, roleToAssume)
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

	log.Debug().Str("GroupID", group.ID).Msg("Finished applying group!")
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

func formatWithWorkerAndApply(ctx context.Context, masterAcctRegion string, mm *magicmodel.Operator, group types.Group, execPath *string, roleToAssume *string) error {
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
		o = mm.Update(&group, "FailedReason", err.Error())
		if o.Err != nil {
			return o.Err
		}
		return fmt.Errorf("Error running `worker group apply` for group with id %s: %s: %s", group.ID, err, *msg)
	}

	log.Debug().Str("GroupID", group.ID).Msg("Applying group Terraform")
	// can't use a for loop because we need to do it in order
	// apply networks all together
	err = apply(ctx, mm, group, execPath, roleToAssume, "network")
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
	err = apply(ctx, mm, group, execPath, roleToAssume, "cluster")
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
	err = apply(ctx, mm, group, execPath, roleToAssume, "cluster_grafana")
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
	err = apply(ctx, mm, group, execPath, roleToAssume, "environment")
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
	err = apply(ctx, mm, group, execPath, roleToAssume, "environment-static")
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
	err = apply(ctx, mm, group, execPath, roleToAssume, "rds")
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

func apply(ctx context.Context, mm *magicmodel.Operator, group types.Group, execPath *string, roleToAssume *string, dirName string) error {
	//errs, ctx := errgroup.WithContext(ctx)
	var wg sync.WaitGroup
	errors := make(chan error, 0)
	directoryPath := filepath.Join(os.Getenv("DRAGONOPS_TERRAFORM_DESTINATION"), dirName)
	directories, _ := os.ReadDir(directoryPath)
	log.Debug().Str("GroupID", group.ID).Msg(fmt.Sprintf("Applying all %ss", dirName))

	// run all the applies in parallel in each folder
	for _, d := range directories {
		log.Debug().Str("GroupID", group.ID).Msg(fmt.Sprintf("Applying %s %s", dirName, d.Name()))
		//time.Sleep(5 * time.Second)
		path, _ := filepath.Abs(filepath.Join(directoryPath, d.Name()))
		wg.Add(1)
		d := d
		go func() {
			defer wg.Done()
			// apply terraform or return an error
			log.Debug().Str("GroupID", group.ID).Msg(path)
			out, err := terraform.ApplyTerraform(ctx, path, *execPath, roleToAssume)
			if err != nil {
				o := mm.Update(&group, "Status", "APPLY_FAILED")
				if o.Err != nil {
					errors <- o.Err
					return
				}
				o = mm.Update(&group, "FailedReason", err.Error())
				if o.Err != nil {
					errors <- o.Err
					return
				}
				errors <- fmt.Errorf("error for %s %s: %v", dirName, d.Name(), err)
				return
			}
			// handle output for argocd credentials
			if dirName == "cluster" {
				err = saveArgoCdCredsToCluster(mm, out, group.ID, d.Name())
				if err != nil {
					o := mm.Update(&group, "Status", "APPLY_FAILED")
					if o.Err != nil {
						errors <- o.Err
						return
					}
					o = mm.Update(&group, "FailedReason", err.Error())
					if o.Err != nil {
						errors <- o.Err
						return
					}
					errors <- fmt.Errorf("error for %s %s: %v", dirName, d.Name(), err)
					return
				}
			}
			// handle output for grafana credentials
			if dirName == "cluster_grafana" {
				err = saveGrafanaCredsToCluster(mm, out, group.ID, d.Name())
				if err != nil {
					o := mm.Update(&group, "Status", "APPLY_FAILED")
					if o.Err != nil {
						errors <- o.Err
						return
					}
					o = mm.Update(&group, "FailedReason", err.Error())
					if o.Err != nil {
						errors <- o.Err
						return
					}
					errors <- fmt.Errorf("error for %s %s: %v", dirName, d.Name(), err)
					return
				}
			}
			//return nil
			//result, err := test(3)
			//if err != nil {
			//	errors <- err
			//	return
			//}
			//results <- result
		}()

		//errs.Go(func() error {
		//	// apply terraform or return an error
		//	log.Debug().Str("GroupID", group.ID).Msg(path)
		//	out, err := terraform.ApplyTerraform(ctx, path, *execPath, roleToAssume)
		//	if err != nil {
		//		o := mm.Update(&group, "Status", "APPLY_FAILED")
		//		if o.Err != nil {
		//			return o.Err
		//		}
		//		o = mm.Update(&group, "FailedReason", err.Error())
		//		if o.Err != nil {
		//			return o.Err
		//		}
		//		return fmt.Errorf("error for %s %s: %v", dirName, d.Name(), err)
		//	}
		//	// handle output for argocd credentials
		//	if dirName == "cluster" {
		//		err = saveArgoCdCredsToCluster(mm, out, group.ID, d.Name())
		//		if err != nil {
		//			o := mm.Update(&group, "Status", "APPLY_FAILED")
		//			if o.Err != nil {
		//				return o.Err
		//			}
		//			o = mm.Update(&group, "FailedReason", err.Error())
		//			if o.Err != nil {
		//				return o.Err
		//			}
		//			return fmt.Errorf("error for %s %s: %v", dirName, d.Name(), err)
		//		}
		//	}
		//	// handle output for grafana credentials
		//	if dirName == "cluster_grafana" {
		//		err = saveGrafanaCredsToCluster(mm, out, group.ID, d.Name())
		//		if err != nil {
		//			o := mm.Update(&group, "Status", "APPLY_FAILED")
		//			if o.Err != nil {
		//				return o.Err
		//			}
		//			o = mm.Update(&group, "FailedReason", err.Error())
		//			if o.Err != nil {
		//				return o.Err
		//			}
		//			return fmt.Errorf("error for %s %s: %v", dirName, d.Name(), err)
		//		}
		//	}
		//	return nil
		//})
	}
	//go func() {
	wg.Wait()
	close(errors)
	//}()
	// Wait for completion and return the first error (if any)
	//return errs.Wait()
	var err error
	if len(errors) > 0 {
		err = fmt.Errorf("multiple errors occurred with applying resources in group %s: %v", group.ResourceLabel, errors)
	}
	return err
}

func saveGrafanaCredsToCluster(mm *magicmodel.Operator, outputs map[string]tfexec.OutputMeta, groupID string, clusterResourceLabel string) error {
	for key, output := range outputs {
		if key == "cluster_credentials" {
			var creds types.GrafanaCredentials
			if err := json.Unmarshal(output.Value, &creds); err != nil {
				return err
			}
			var clusters []types.Cluster
			o := mm.Where(&clusters, "Group.ID", groupID)
			if o.Err != nil {
				return o.Err
			}
			for _, cluster := range clusters {
				if cluster.ResourceLabel == clusterResourceLabel {
					cluster.Metadata.Grafana.GrafanaCredentials = creds
					o = mm.Update(&cluster, "Metadata", cluster.Metadata)
					if o.Err != nil {
						return o.Err
					}
					break
				}
			}
		}
		if key == "cluster_metadata" {
			var endpoint struct {
				GrafanaUrl string `json:"grafana_url"`
			}
			if err := json.Unmarshal(output.Value, &endpoint); err != nil {
				return err
			}
			var clusters []types.Cluster
			o := mm.Where(&clusters, "Group.ID", groupID)
			if o.Err != nil {
				return o.Err
			}
			for _, cluster := range clusters {
				if cluster.ResourceLabel == clusterResourceLabel {
					cluster.Metadata.Grafana.EndpointMetadata = types.EndpointMetadata{RootDomain: endpoint.GrafanaUrl}
					o = mm.Update(&cluster, "Metadata", cluster.Metadata)
					if o.Err != nil {
						return o.Err
					}
					break
				}
			}
		}
	}

	return nil
}

func saveArgoCdCredsToCluster(mm *magicmodel.Operator, outputs map[string]tfexec.OutputMeta, groupID string, clusterResourceLabel string) error {
	for key, output := range outputs {
		if key == "cluster_credentials" {
			var creds types.ArgoCdCredentials
			if err := json.Unmarshal(output.Value, &creds); err != nil {
				return err
			}
			creds.Username = "admin"
			var clusters []types.Cluster
			o := mm.Where(&clusters, "Group.ID", groupID)
			if o.Err != nil {
				return o.Err
			}
			for _, cluster := range clusters {
				if cluster.ResourceLabel == clusterResourceLabel {
					cluster.Metadata.ArgoCd = types.ArgoCdMetadata{
						ArgoCdCredentials: creds,
					}
					o = mm.Update(&cluster, "Metadata", cluster.Metadata)
					if o.Err != nil {
						return o.Err
					}
					break
				}
			}
		}
		if key == "endpoints" {
			var endpoint struct {
				ArgoCdUrl string `json:"argocd"`
			}
			if err := json.Unmarshal(output.Value, &endpoint); err != nil {
				return err
			}
			var clusters []types.Cluster
			o := mm.Where(&clusters, "Group.ID", groupID)
			if o.Err != nil {
				return o.Err
			}
			for _, cluster := range clusters {
				if cluster.ResourceLabel == clusterResourceLabel {
					cluster.Metadata.ArgoCd.EndpointMetadata = types.EndpointMetadata{RootDomain: endpoint.ArgoCdUrl}
					o = mm.Update(&cluster, "Metadata", cluster.Metadata)
					if o.Err != nil {
						return o.Err
					}
					break
				}
			}
		}
	}

	return nil
}
