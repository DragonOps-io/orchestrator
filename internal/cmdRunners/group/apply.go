package group

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2"

	"github.com/DragonOps-io/orchestrator/internal/terraform"
	"github.com/DragonOps-io/orchestrator/internal/utils"
	"github.com/DragonOps-io/types"
	magicmodel "github.com/Ilios-LLC/magicmodel-go/model"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/rs/zerolog/log"
)

func Apply(ctx context.Context, payload Payload, mm *magicmodel.Operator, isDryRun bool) error {
	log.Debug().
		Str("GroupID", payload.GroupID).
		Msg("Looking for group with matching ID")

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
		aco := mm.Update(&group, "Status", "APPLY_FAILED")
		if aco.Err != nil {
			log.Err(aco.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(aco.Err.Error())
			return aco.Err
		}
		aco = mm.Update(&group, "FailedReason", "No RECEIPT_HANDLE variable found.")
		if aco.Err != nil {
			log.Err(aco.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(aco.Err.Error())
			return aco.Err
		}
		return fmt.Errorf("Error retrieving RECEIPT_HANDLE from queue. Cannot continue.")
	}

	var accounts []types.Account
	o = mm.Where(&accounts, "IsMasterAccount", aws.Bool(true))
	if o.Err != nil {
		log.Err(o.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(o.Err.Error())
		aco := mm.Update(&group, "Status", "APPLY_FAILED")
		if aco.Err != nil {
			log.Err(aco.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(aco.Err.Error())
			return aco.Err
		}
		aco = mm.Update(&group, "FailedReason", o.Err.Error())
		if aco.Err != nil {
			log.Err(aco.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(aco.Err.Error())
			return aco.Err
		}
		return fmt.Errorf("an error occurred when trying to find the MasterAccount: %s", aco.Err)
	}
	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Found MasterAccount")

	cfg, err := config.LoadDefaultConfig(ctx, func(options *config.LoadOptions) error {
		config.WithRegion(accounts[0].AwsRegion)
		return nil
	})
	if err != nil {
		log.Err(err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(err.Error())
		aco := mm.Update(&group, "Status", "APPLY_FAILED")
		if aco.Err != nil {
			log.Err(aco.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(aco.Err.Error())
			return aco.Err
		}
		aco = mm.Update(&group, "FailedReason", err.Error())
		if aco.Err != nil {
			log.Err(aco.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(aco.Err.Error())
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
			log.Err(aco.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(aco.Err.Error())
			return aco.Err
		}
		aco = mm.Update(&group, "FailedReason", err.Error())
		if aco.Err != nil {
			log.Err(aco.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(aco.Err.Error())
			return aco.Err
		}
		return err
	}

	authResponse, err := utils.IsApiKeyValid(*doApiKey)
	if err != nil {
		log.Err(err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(err.Error())
		aco := mm.Update(&group, "Status", "APPLY_FAILED")
		if aco.Err != nil {
			log.Err(aco.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(aco.Err.Error())
			return aco.Err
		}
		aco = mm.Update(&group, "FailedReason", err.Error())
		if aco.Err != nil {
			log.Err(aco.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(aco.Err.Error())
			return aco.Err
		}
		return fmt.Errorf("error verifying validity of DragonOps Api Key: %v", err)
	}

	if !authResponse.IsValid {
		log.Err(fmt.Errorf("The DragonOps api key provided is not valid. Please reach out to DragonOps support for help.")).Str("GroupID", group.ID).Msg("The DragonOps api key provided is not valid. Please reach out to DragonOps support for help.")
		aco := mm.Update(&group, "Status", "APPLY_FAILED")
		if aco.Err != nil {
			log.Err(aco.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(aco.Err.Error())
			return aco.Err
		}
		aco = mm.Update(&group, "FailedReason", "The DragonOps api key provided is not valid. Please reach out to DragonOps support for help.")
		if aco.Err != nil {
			log.Err(aco.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(aco.Err.Error())
			return aco.Err
		}
		return fmt.Errorf("The DragonOps api key provided is not valid. Please reach out to DragonOps support for help.")
	}

	sqsClient := sqs.NewFromConfig(cfg, func(o *sqs.Options) {
		o.Region = accounts[0].AwsRegion
	})

	//var groupClusters []types.Cluster
	//o = mm.Where(&groupClusters, "Group.ID", group.ID)
	//if o.Err != nil {
	//	log.Err(o.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(o.Err.Error())
	//	group.Status = "APPLY_FAILED"
	//	group.FailedReason = o.Err.Error()
	//	po := mm.Save(&group)
	//	if po.Err != nil {
	//		log.Err(po.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(po.Err.Error())
	//		return po.Err
	//	}
	//	return o.Err
	//}

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
		log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Preparing Terraform")
		execPath, err = terraform.PrepareTerraform(ctx)
		if err != nil {
			log.Err(err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(err.Error())
			o = mm.Update(&group, "Status", "APPLY_FAILED")
			if o.Err != nil {
				log.Err(o.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(o.Err.Error())
				return o.Err
			}
			o = mm.Update(&group, "FailedReason", err.Error())
			if o.Err != nil {
				log.Err(o.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(o.Err.Error())
				return o.Err
			}
			return err
		}

		log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Region for magic model is: %s", accounts[0].AwsRegion))
		err = formatWithWorkerAndApply(ctx, accounts[0].AwsRegion, mm, group, execPath, roleToAssume, cfg, payload, accounts[0])
		if err != nil {
			log.Err(err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(err.Error())
			o = mm.Update(&group, "Status", "APPLY_FAILED")
			if o.Err != nil {
				log.Err(o.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(o.Err.Error())
				return o.Err
			}
			o = mm.Update(&group, "FailedReason", err.Error())
			if o.Err != nil {
				log.Err(o.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(o.Err.Error())
				return o.Err
			}
			return err
		}
	}

	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Updating group status")
	o = mm.Update(&group, "Status", "APPLIED")
	if o.Err != nil {
		log.Err(o.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(o.Err.Error())
		return o.Err
	}
	o = mm.Update(&group, "FailedReason", "")
	if o.Err != nil {
		log.Err(o.Err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(o.Err.Error())
		return o.Err
	}

	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Finished applying group!")
	queueParts := strings.Split(group.Account.GroupSqsArn, ":")
	queueUrl := fmt.Sprintf("https://%s.%s.amazonaws.com/%s/%s", queueParts[2], queueParts[3], queueParts[4], queueParts[5])

	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Queue url is %s", queueUrl))

	_, err = sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      &queueUrl,
		ReceiptHandle: &receiptHandle,
	})
	if err != nil {
		log.Err(err).Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(err.Error())
		return err
	}
	return nil
}

func formatWithWorkerAndApply(ctx context.Context, masterAcctRegion string, mm *magicmodel.Operator, group types.Group, execPath *string, roleToAssume *string, cfg aws.Config, payload Payload, masterAccount types.Account) error {
	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Templating Terraform with correct values")

	command := fmt.Sprintf("/app/worker group apply --group-id %s --table-region %s", group.ID, masterAcctRegion)
	if os.Getenv("IS_LOCAL") == "true" {
		command = fmt.Sprintf("./app/worker group apply --group-id %s --table-region %s", group.ID, masterAcctRegion)
	}

	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Running command %s", command))
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

	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg("Applying group Terraform")

	// If resource is marked for deletion, need to run terraform destroy on the stack. Look through and find all resources that are marked for deletion and destroy the terraform.
	rdsToApply, err := destroyAllRds(ctx, mm, group, execPath, roleToAssume, "rds", payload)
	if err != nil {
		group.Status = "APPLY_FAILED"
		group.FailedReason = err.Error()
		o := mm.Save(&group)
		if o.Err != nil {
			return o.Err
		}
		return fmt.Errorf("Error running apply for database stacks in group with id %s: %s: %s", group.ID, err, *msg)
	}

	envsToApply, err := destroyAllEnvironments(ctx, mm, group, execPath, roleToAssume, "environment", payload)
	if err != nil {
		group.Status = "APPLY_FAILED"
		group.FailedReason = err.Error()
		o := mm.Save(&group)
		if o.Err != nil {
			return o.Err
		}
		return fmt.Errorf("Error running apply for environment stacks in group with id %s: %s: %s", group.ID, err, *msg)
	}

	clusterGrafanasToApply, err := destroyAllClusterGrafanas(ctx, mm, group, execPath, roleToAssume, "cluster_grafana", payload)
	if err != nil {
		group.Status = "APPLY_FAILED"
		group.FailedReason = err.Error()
		o := mm.Save(&group)
		if o.Err != nil {
			return o.Err
		}
		return fmt.Errorf("Error running apply for cluster stacks in group with id %s: %s: %s", group.ID, err, *msg)
	}

	clustersToApply, err := destroyAllClusters(ctx, mm, group, execPath, roleToAssume, "cluster", payload)
	if err != nil {
		group.Status = "APPLY_FAILED"
		group.FailedReason = err.Error()
		o := mm.Save(&group)
		if o.Err != nil {
			return o.Err
		}
		return fmt.Errorf("Error running apply for cluster stacks in group with id %s: %s: %s", group.ID, err, *msg)
	}

	networksToApply, err := destroyAllNetworks(ctx, mm, group, execPath, roleToAssume, "network", payload, cfg)
	if err != nil {
		group.Status = "APPLY_FAILED"
		group.FailedReason = err.Error()
		o := mm.Save(&group)
		if o.Err != nil {
			return o.Err
		}
		return fmt.Errorf("Error running apply for network stacks in group with id %s: %s: %s", group.ID, err, *msg)
	}

	// now we apply stuff, but in the opposite order
	err = applyAllNetworks(ctx, mm, networksToApply, group, execPath, roleToAssume, "network", cfg, payload)
	if err != nil {
		group.Status = "APPLY_FAILED"
		group.FailedReason = err.Error()
		o := mm.Save(&group)
		if o.Err != nil {
			return o.Err
		}
		return fmt.Errorf("Error running apply for network stacks in group with id %s: %s: %s", group.ID, err, *msg)
	}

	err = applyAllClusters(ctx, mm, clustersToApply, group, execPath, roleToAssume, "cluster", payload)
	if err != nil {
		group.Status = "APPLY_FAILED"
		group.FailedReason = err.Error()
		o := mm.Save(&group)
		if o.Err != nil {
			return o.Err
		}
		return fmt.Errorf("Error running apply for cluster stacks in group with id %s: %s: %s", group.ID, err, *msg)
	}

	err = applyAllClusterGrafanas(ctx, mm, clusterGrafanasToApply, group, execPath, roleToAssume, "cluster_grafana", payload)
	if err != nil {
		group.Status = "APPLY_FAILED"
		group.FailedReason = err.Error()
		o := mm.Save(&group)
		if o.Err != nil {
			return o.Err
		}
		return fmt.Errorf("Error running apply for cluster stacks in group with id %s: %s: %s", group.ID, err, *msg)
	}

	err = applyAllEnvironments(ctx, envsToApply, group, execPath, roleToAssume, "environment", payload)
	if err != nil {
		group.Status = "APPLY_FAILED"
		group.FailedReason = err.Error()
		o := mm.Save(&group)
		if o.Err != nil {
			return o.Err
		}
		return fmt.Errorf("Error running apply for environment stacks in group with id %s: %s: %s", group.ID, err, *msg)
	}
	err = applyAllRds(ctx, mm, rdsToApply, group, execPath, roleToAssume, "rds", payload, cfg)
	if err != nil {
		group.Status = "APPLY_FAILED"
		group.FailedReason = err.Error()
		o := mm.Save(&group)
		if o.Err != nil {
			return o.Err
		}
		return fmt.Errorf("Error running apply for database stacks in group with id %s: %s: %s", group.ID, err, *msg)
	}
	return nil
}

type RdsToApply struct {
	types.Rds
	os.DirEntry
}
type EnvironmentToApply struct {
	types.Environment
	os.DirEntry
}
type ClusterToApply struct {
	types.Cluster
	os.DirEntry
}
type NetworkToApply struct {
	types.Network
	os.DirEntry
}

func destroyAllNetworks(ctx context.Context, mm *magicmodel.Operator, group types.Group, execPath *string, roleToAssume *string, dirName string, payload Payload, cfg aws.Config) ([]NetworkToApply, error) {
	directoryPath := filepath.Join(os.Getenv("DRAGONOPS_TERRAFORM_DESTINATION"), dirName)
	directories, _ := os.ReadDir(directoryPath)

	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Applying all %ss", dirName))

	// go routine setup stuff
	wg := &sync.WaitGroup{}
	errors := make(chan error, 0)
	networksToApplyChannel := make(chan NetworkToApply, 0)
	// run all the destroys in parallel in each folder
	for _, d := range directories {
		wg.Add(1)
		log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Applying %s %s", dirName, d.Name()))
		path, _ := filepath.Abs(filepath.Join(directoryPath, d.Name()))
		client := ssm.NewFromConfig(cfg)

		go func(dir os.DirEntry) {
			defer wg.Done()
			log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(path)

			var networks []types.Network
			o := mm.WhereV2(true, &networks, "Group.ID", group.ID).WhereV2(false, &networks, "ResourceLabel", dir.Name())
			if o.Err != nil {
				errors <- fmt.Errorf("Error finding network %s: %s", networks[0].ResourceLabel, o.Err.Error())
				return
			}
			if len(networks) != 1 {
				errors <- fmt.Errorf("network with resource label %s not found or too many returned", networks[0].ResourceLabel)
				return
			}
			if networks[0].MarkedForDeletion != nil && *networks[0].MarkedForDeletion {
				// run terraform destroy and delete network
				_, err := terraform.DestroyTerraform(ctx, path, *execPath, roleToAssume)
				if err != nil {
					errors <- fmt.Errorf("error for %s %s: %v", dirName, dir.Name(), err)
					return
				}
				_, err = client.DeleteParameters(ctx, &ssm.DeleteParametersInput{
					Names: []string{
						fmt.Sprintf("/%s/wireguard/public_key", networks[0].ID),
						fmt.Sprintf("/%s/wireguard/private_key", networks[0].ID),
						fmt.Sprintf("/%s/wireguard/config_file", networks[0].ID),
					},
				})
				if err != nil {
					errors <- fmt.Errorf("error deleting config parameter for network %s: %s", networks[0].ResourceLabel, err.Error())
					return
				}

				var clients []types.VpnClient
				operator := mm.All(&clients)
				if operator.Err != nil {
					errors <- fmt.Errorf("error getting VPN clients associated with network %s: %s", networks[0].ResourceLabel, operator.Err.Error())
					return
				}

				for _, c := range clients {
					for j := 0; j < len(c.Networks); j++ {
						if c.Networks[j].ID == networks[0].ID {
							c.Networks = append(c.Networks[:j], c.Networks[j+1:]...)
							// Adjust the loop index since we modified the slice
							j--
						}
					}
				}
				o = mm.SoftDelete(&networks[0])
				if o.Err != nil {
					errors <- fmt.Errorf("Error deleting network %s: %s", networks[0].ResourceLabel, o.Err.Error())
					return
				}
				return
			} else {
				networksToApplyChannel <- NetworkToApply{
					Network:  networks[0],
					DirEntry: d,
				}
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
		err := fmt.Errorf("errors occurred with destroying networks in group %s: %v", group.ResourceLabel, errs)
		return nil, err
	}
	networkDirectoriesToApply := make([]NetworkToApply, 0)
	for nd := range networksToApplyChannel {
		networkDirectoriesToApply = append(networkDirectoriesToApply, nd)
	}
	return networkDirectoriesToApply, nil
}

func destroyAllClusters(ctx context.Context, mm *magicmodel.Operator, group types.Group, execPath *string, roleToAssume *string, dirName string, payload Payload) ([]ClusterToApply, error) {
	directoryPath := filepath.Join(os.Getenv("DRAGONOPS_TERRAFORM_DESTINATION"), dirName)
	directories, _ := os.ReadDir(directoryPath)

	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Applying all %ss", dirName))

	// go routine setup stuff
	wg := &sync.WaitGroup{}
	errors := make(chan error, 0)
	resourcesToApplyChannel := make(chan ClusterToApply, 0)
	// run all the destroys in parallel in each folder
	for _, d := range directories {
		wg.Add(1)
		log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Applying %s %s", dirName, d.Name()))
		path, _ := filepath.Abs(filepath.Join(directoryPath, d.Name()))

		go func(dir os.DirEntry) {
			defer wg.Done()
			log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(path)

			var clusters []types.Cluster
			o := mm.WhereV2(true, &clusters, "Group.ID", group.ID).WhereV2(false, &clusters, "ResourceLabel", dir.Name())
			if o.Err != nil {
				errors <- fmt.Errorf("Error finding cluster %s: %s", clusters[0].ResourceLabel, o.Err.Error())
				return
			}
			if len(clusters) != 1 {
				errors <- fmt.Errorf("cluster with resource label %s not found or too many returned", clusters[0].ResourceLabel)
				return
			}

			if clusters[0].MarkedForDeletion != nil && *clusters[0].MarkedForDeletion {
				// run terraform destroy and delete network
				_, err := terraform.DestroyTerraform(ctx, path, *execPath, roleToAssume)
				if err != nil {
					errors <- fmt.Errorf("error for %s %s: %v", dirName, dir.Name(), err)
					return
				}
				o := mm.SoftDelete(&clusters[0])
				if o.Err != nil {
					errors <- fmt.Errorf("Error deleting cluster %s: %s", clusters[0].ResourceLabel, o.Err.Error())
					return
				}
				return
			} else {
				resourcesToApplyChannel <- ClusterToApply{
					Cluster:  clusters[0],
					DirEntry: d,
				}
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
		err := fmt.Errorf("errors occurred with destroying clusters in group %s: %v", group.ResourceLabel, errs)
		return nil, err
	}
	clusterDirectoriesToApply := make([]ClusterToApply, 0)
	for nd := range resourcesToApplyChannel {
		clusterDirectoriesToApply = append(clusterDirectoriesToApply, nd)
	}
	return clusterDirectoriesToApply, nil
}

func destroyAllClusterGrafanas(ctx context.Context, mm *magicmodel.Operator, group types.Group, execPath *string, roleToAssume *string, dirName string, payload Payload) ([]ClusterToApply, error) {
	directoryPath := filepath.Join(os.Getenv("DRAGONOPS_TERRAFORM_DESTINATION"), dirName)
	directories, _ := os.ReadDir(directoryPath)

	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Applying all %ss", dirName))

	// go routine setup stuff
	wg := &sync.WaitGroup{}
	errors := make(chan error, 0)
	resourcesToApplyChannel := make(chan ClusterToApply, 0)
	// run all the destroys in parallel in each folder
	for _, d := range directories {
		wg.Add(1)
		log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Applying %s %s", dirName, d.Name()))
		path, _ := filepath.Abs(filepath.Join(directoryPath, d.Name()))

		go func(dir os.DirEntry) {
			defer wg.Done()
			log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(path)

			var clusters []types.Cluster
			o := mm.WhereV2(true, &clusters, "Group.ID", group.ID).WhereV2(false, &clusters, "ResourceLabel", dir.Name())
			if o.Err != nil {
				errors <- fmt.Errorf("Error finding cluster %s: %s", clusters[0].ResourceLabel, o.Err.Error())
				return
			}
			if len(clusters) != 1 {
				errors <- fmt.Errorf("cluster with resource label %s not found or too many returned", clusters[0].ResourceLabel)
				return
			}

			if clusters[0].MarkedForDeletion != nil && *clusters[0].MarkedForDeletion {
				// run terraform destroy and delete network
				_, err := terraform.DestroyTerraform(ctx, path, *execPath, roleToAssume)
				if err != nil {
					errors <- fmt.Errorf("error for %s %s: %v", dirName, dir.Name(), err)
					return
				}
				return
			} else {
				resourcesToApplyChannel <- ClusterToApply{
					Cluster:  clusters[0],
					DirEntry: d,
				}
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
		err := fmt.Errorf("errors occurred with destroying clusters in group %s: %v", group.ResourceLabel, errs)
		return nil, err
	}
	directoriesToApply := make([]ClusterToApply, 0)
	for nd := range resourcesToApplyChannel {
		directoriesToApply = append(directoriesToApply, nd)
	}
	return directoriesToApply, nil
}

func destroyAllEnvironments(ctx context.Context, mm *magicmodel.Operator, group types.Group, execPath *string, roleToAssume *string, dirName string, payload Payload) ([]EnvironmentToApply, error) {
	directoryPath := filepath.Join(os.Getenv("DRAGONOPS_TERRAFORM_DESTINATION"), dirName)
	directories, _ := os.ReadDir(directoryPath)

	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Applying all %ss", dirName))

	// go routine setup stuff
	wg := &sync.WaitGroup{}
	errors := make(chan error, 0)
	resourcesToApplyChannel := make(chan EnvironmentToApply, 0)
	// run all the destroys in parallel in each folder
	for _, d := range directories {
		wg.Add(1)
		log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Applying %s %s", dirName, d.Name()))
		path, _ := filepath.Abs(filepath.Join(directoryPath, d.Name()))

		go func(dir os.DirEntry) {
			defer wg.Done()
			log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(path)

			var envs []types.Environment
			o := mm.WhereV2(true, &envs, "Group.ID", group.ID).WhereV2(false, &envs, "ResourceLabel", dir.Name())
			if o.Err != nil {
				errors <- fmt.Errorf("Error finding environment %s: %s", envs[0].ResourceLabel, o.Err.Error())
				return
			}
			if len(envs) != 1 {
				errors <- fmt.Errorf("environment with resource label %s not found or too many returned", envs[0].ResourceLabel)
				return
			}

			if envs[0].MarkedForDeletion != nil && *envs[0].MarkedForDeletion {
				// run terraform destroy and delete network
				_, err := terraform.DestroyTerraform(ctx, path, *execPath, roleToAssume)
				if err != nil {
					errors <- fmt.Errorf("error for %s %s: %v", dirName, dir.Name(), err)
					return
				}
				o := mm.SoftDelete(&envs[0])
				if o.Err != nil {
					errors <- fmt.Errorf("Error deleting environment %s: %s", envs[0].ResourceLabel, o.Err.Error())
					return
				}
				return
			} else {
				resourcesToApplyChannel <- EnvironmentToApply{
					Environment: envs[0],
					DirEntry:    d,
				}
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
		err := fmt.Errorf("errors occurred with destroying clusters in group %s: %v", group.ResourceLabel, errs)
		return nil, err
	}
	directoriesToApply := make([]EnvironmentToApply, 0)
	for nd := range resourcesToApplyChannel {
		directoriesToApply = append(directoriesToApply, nd)
	}
	return directoriesToApply, nil
}

func destroyAllRds(ctx context.Context, mm *magicmodel.Operator, group types.Group, execPath *string, roleToAssume *string, dirName string, payload Payload) ([]RdsToApply, error) {
	directoryPath := filepath.Join(os.Getenv("DRAGONOPS_TERRAFORM_DESTINATION"), dirName)
	directories, _ := os.ReadDir(directoryPath)

	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Applying all %ss", dirName))

	// go routine setup stuff
	wg := &sync.WaitGroup{}
	errors := make(chan error, 0)
	resourcesToApplyChannel := make(chan RdsToApply, 0)
	// run all the destroys in parallel in each folder
	for _, d := range directories {
		wg.Add(1)
		log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Applying %s %s", dirName, d.Name()))
		path, _ := filepath.Abs(filepath.Join(directoryPath, d.Name()))

		go func(dir os.DirEntry) {
			defer wg.Done()
			log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(path)

			var r []types.Rds
			o := mm.WhereV2(true, &r, "Group.ID", group.ID).WhereV2(false, &r, "ResourceLabel", dir.Name())
			if o.Err != nil {
				errors <- fmt.Errorf("Error finding database %s: %s", r[0].ResourceLabel, o.Err.Error())
				return
			}
			if len(r) != 1 {
				errors <- fmt.Errorf("database with resource label %s not found or too many returned", r[0].ResourceLabel)
				return
			}

			if r[0].MarkedForDeletion != nil && *r[0].MarkedForDeletion {
				// run terraform destroy and delete network
				_, err := terraform.DestroyTerraform(ctx, path, *execPath, roleToAssume)
				if err != nil {
					errors <- fmt.Errorf("error for %s %s: %v", dirName, dir.Name(), err)
					return
				}
				o := mm.SoftDelete(&r[0])
				if o.Err != nil {
					errors <- fmt.Errorf("Error deleting database %s: %s", r[0].ResourceLabel, o.Err.Error())
					return
				}
				return
			} else {
				resourcesToApplyChannel <- RdsToApply{
					Rds:      r[0],
					DirEntry: d,
				}
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
		err := fmt.Errorf("errors occurred with destroying databases in group %s: %v", group.ResourceLabel, errs)
		return nil, err
	}
	directoriesToApply := make([]RdsToApply, 0)
	for nd := range resourcesToApplyChannel {
		directoriesToApply = append(directoriesToApply, nd)
	}
	return directoriesToApply, nil
}

func applyAllNetworks(ctx context.Context, mm *magicmodel.Operator, networksToApply []NetworkToApply, group types.Group, execPath *string, roleToAssume *string, dirName string, cfg aws.Config, payload Payload) error {
	directoryPath := filepath.Join(os.Getenv("DRAGONOPS_TERRAFORM_DESTINATION"), dirName)
	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Applying all %ss", dirName))

	wg := &sync.WaitGroup{}
	errors := make(chan error, 0)

	for _, d := range networksToApply {
		// each "d" here is a single network, which is the network resource label
		wg.Add(1)
		log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Applying %s %s", dirName, d.DirEntry.Name()))
		path, _ := filepath.Abs(filepath.Join(directoryPath, d.DirEntry.Name()))

		go func(dir os.DirEntry) {
			defer wg.Done()
			out, err := terraform.ApplyTerraform(ctx, path, *execPath, roleToAssume)
			if err != nil {
				errors <- fmt.Errorf("error for %s %s: %v", dirName, dir.Name(), err)
				return
			}
			network, err := saveNetworkOutputs(mm, out, group.ID, d.Network)
			if err != nil {
				errors <- fmt.Errorf("error saving outputs for %s %s: %v", dirName, dir.Name(), err)
				return
			}
			err = handleWireguardUpdates(mm, *network, cfg)
			if err != nil {
				errors <- fmt.Errorf("error updating wireguard for %s %s: %v", dirName, dir.Name(), err)
				return
			}
			// TODO currently handled in terraform
			//if masterAccount.Observability != nil && masterAccount.Observability.Enabled && network.VpcEndpointId != "" {
			//	err = handleVpcConnectionsForObservability(ctx, *network, cfg, masterAccount)
			//	if err != nil {
			//		errors <- fmt.Errorf("error updating wireguard for %s %s: %v", dirName, dir.Name(), err)
			//		return
			//	}
			//}
		}(d.DirEntry)
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
		err := fmt.Errorf("errors occurred with applying resources in group %s: %v", group.ResourceLabel, errs)
		return err
	}
	return nil
}

func applyAllClusterGrafanas(ctx context.Context, mm *magicmodel.Operator, clustersToApply []ClusterToApply, group types.Group, execPath *string, roleToAssume *string, dirName string, payload Payload) error {
	directoryPath := filepath.Join(os.Getenv("DRAGONOPS_TERRAFORM_DESTINATION"), dirName)
	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Applying all %ss", dirName))

	wg := &sync.WaitGroup{}
	errors := make(chan error, 0)

	for _, d := range clustersToApply {
		// each "d" here is a single network, which is the network resource label
		wg.Add(1)
		log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Applying %s %s", dirName, d.DirEntry.Name()))
		path, _ := filepath.Abs(filepath.Join(directoryPath, d.DirEntry.Name()))

		go func(dir os.DirEntry) {
			defer wg.Done()
			out, err := terraform.ApplyTerraform(ctx, path, *execPath, roleToAssume)
			if err != nil {
				errors <- fmt.Errorf("error for %s %s: %v", dirName, dir.Name(), err)
				return
			}
			err = saveClusterGrafanaOutputs(mm, out, group.ID, dir.Name())
			if err != nil {
				errors <- fmt.Errorf("error for %s %s: %v", dirName, dir.Name(), err)
				return
			}
		}(d.DirEntry)
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
		err := fmt.Errorf("errors occurred with applying resources in group %s: %v", group.ResourceLabel, errs)
		return err
	}
	return nil
}

func applyAllClusters(ctx context.Context, mm *magicmodel.Operator, clustersToApply []ClusterToApply, group types.Group, execPath *string, roleToAssume *string, dirName string, payload Payload) error {
	directoryPath := filepath.Join(os.Getenv("DRAGONOPS_TERRAFORM_DESTINATION"), dirName)
	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Applying all %ss", dirName))

	wg := &sync.WaitGroup{}
	errors := make(chan error, 0)

	for _, d := range clustersToApply {
		// each "d" here is a single network, which is the network resource label
		wg.Add(1)
		log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Applying %s %s", dirName, d.DirEntry.Name()))
		path, _ := filepath.Abs(filepath.Join(directoryPath, d.DirEntry.Name()))

		go func(dir os.DirEntry) {
			defer wg.Done()
			out, err := terraform.ApplyTerraform(ctx, path, *execPath, roleToAssume)
			if err != nil {
				errors <- fmt.Errorf("error for %s %s: %v", dirName, dir.Name(), err)
				return
			}
			err = saveClusterOutputs(mm, out, group.ID, dir.Name())
			if err != nil {
				errors <- fmt.Errorf("error for %s %s: %v", dirName, dir.Name(), err)
				return
			}
		}(d.DirEntry)
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
		err := fmt.Errorf("errors occurred with applying resources in group %s: %v", group.ResourceLabel, errs)
		return err
	}
	return nil
}

func applyAllEnvironments(ctx context.Context, envsToApply []EnvironmentToApply, group types.Group, execPath *string, roleToAssume *string, dirName string, payload Payload) error {
	directoryPath := filepath.Join(os.Getenv("DRAGONOPS_TERRAFORM_DESTINATION"), dirName)
	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Applying all %ss", dirName))

	wg := &sync.WaitGroup{}
	errors := make(chan error, 0)

	for _, d := range envsToApply {
		// each "d" here is a single network, which is the network resource label
		wg.Add(1)
		log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Applying %s %s", dirName, d.DirEntry.Name()))
		path, _ := filepath.Abs(filepath.Join(directoryPath, d.DirEntry.Name()))

		go func(dir os.DirEntry) {
			defer wg.Done()
			_, err := terraform.ApplyTerraform(ctx, path, *execPath, roleToAssume)
			if err != nil {
				errors <- fmt.Errorf("error for %s %s: %v", dirName, dir.Name(), err)
				return
			}
		}(d.DirEntry)
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
		err := fmt.Errorf("errors occurred with applying resources in group %s: %v", group.ResourceLabel, errs)
		return err
	}
	return nil
}

func applyAllRds(ctx context.Context, mm *magicmodel.Operator, rdsToApply []RdsToApply, group types.Group, execPath *string, roleToAssume *string, dirName string, payload Payload, cfg aws.Config) error {
	directoryPath := filepath.Join(os.Getenv("DRAGONOPS_TERRAFORM_DESTINATION"), dirName)
	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Applying all %ss", dirName))

	wg := &sync.WaitGroup{}
	errors := make(chan error, 0)

	for _, d := range rdsToApply {
		// each "d" here is a single network, which is the network resource label
		wg.Add(1)
		log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Applying %s %s", dirName, d.DirEntry.Name()))
		path, _ := filepath.Abs(filepath.Join(directoryPath, d.DirEntry.Name()))

		go func(dir os.DirEntry) {
			defer wg.Done()
			out, err := terraform.ApplyTerraform(ctx, path, *execPath, roleToAssume)
			if err != nil {
				errors <- fmt.Errorf("error for %s %s: %v", dirName, dir.Name(), err)
				return
			}
			err = saveRdsEndpointAndSecret(mm, out, group.ID, dir.Name(), cfg)
			if err != nil {
				errors <- fmt.Errorf("error for %s %s: %v", dirName, dir.Name(), err)
				return
			}
		}(d.DirEntry)
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
		err := fmt.Errorf("errors occurred with applying resources in group %s: %v", group.ResourceLabel, errs)
		return err
	}
	return nil
}

//func apply(ctx context.Context, mm *magicmodel.Operator, group types.Group, execPath *string, roleToAssume *string, dirName string, cfg aws.Config, payload Payload, masterAccount types.Account) error {
//	directoryPath := filepath.Join(os.Getenv("DRAGONOPS_TERRAFORM_DESTINATION"), dirName)
//	directories, _ := os.ReadDir(directoryPath)
//	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Applying all %ss", dirName))
//
//	// directories is only the networks
//	// go routine setup stuff
//	wg := &sync.WaitGroup{}
//	errors := make(chan error, 0)
//	networksMarkedDeletion := make(chan NetworkToDelete, 0)
//	clustersToDelete := make(chan types.Cluster, 0)
//	envsMarkedDeletion := make(chan EnvToDelete, 0)
//
//	// run all the applies in parallel in each folder
//	for _, d := range directories {
//		// each "d" here is a single network, which is the network resource label
//		wg.Add(1)
//		log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Applying %s %s", dirName, d.Name()))
//		path, _ := filepath.Abs(filepath.Join(directoryPath, d.Name()))
//
//		go func(dir os.DirEntry) {
//			defer wg.Done()
//			// apply terraform or return an error
//			log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(path)
//			//out, err := terraform.ApplyTerraform(ctx, path, *execPath, roleToAssume)
//			//if err != nil {
//			//	errors <- fmt.Errorf("error for %s %s: %v", dirName, dir.Name(), err)
//			//	return
//			//}
//
//			// handle network outputs
//			if dirName == "network" {
//				var networks []types.Network
//				o := mm.WhereV2(true, &networks, "Group.ID", group.ID).WhereV2(false, &networks, "ResourceLabel", dir.Name())
//				if o.Err != nil {
//					errors <- fmt.Errorf("Error finding network %s: %s", networks[0].ResourceLabel, o.Err.Error())
//					return
//				}
//				if len(networks) != 1 {
//					errors <- fmt.Errorf("network with resource label %s not found or too many returned", networks[0].ResourceLabel)
//					return
//				}
//				if networks[0].MarkedForDeletion != nil && !*networks[0].MarkedForDeletion {
//					networksMarkedDeletion <- NetworkToDelete{
//						Network:          networks[0],
//						Path:             path,
//						ExecPath:         *execPath,
//						RoleToAssume:     roleToAssume,
//						BaseDirName:      dirName,
//						TerraformDirName: dir.Name(),
//					}
//					// TODO if it's a destroy, needs to happen in opposite order...
//					// run terraform destroy and delete network
//					//_, err := terraform.DestroyTerraform(ctx, path, *execPath, roleToAssume)
//					//if err != nil {
//					//	errors <- fmt.Errorf("error for %s %s: %v", dirName, dir.Name(), err)
//					//	return
//					//}
//					//o = mm.SoftDelete(&networks[0])
//					//if o.Err != nil {
//					//	errors <- fmt.Errorf("Error deleting network %s: %s", networks[0].ResourceLabel, o.Err.Error())
//					//	return
//					//}
//				} else {
//					// run terraform apply and save outputs
//					out, err := terraform.ApplyTerraform(ctx, path, *execPath, roleToAssume)
//					if err != nil {
//						errors <- fmt.Errorf("error for %s %s: %v", dirName, dir.Name(), err)
//						return
//					}
//					network, err := saveNetworkOutputs(mm, out, group.ID, networks[0])
//					if err != nil {
//						errors <- fmt.Errorf("error saving outputs for %s %s: %v", dirName, dir.Name(), err)
//						return
//					}
//					err = handleWireguardUpdates(mm, *network, cfg)
//					if err != nil {
//						errors <- fmt.Errorf("error updating wireguard for %s %s: %v", dirName, dir.Name(), err)
//						return
//					}
//					// TODO currently handled in terraform
//					//if masterAccount.Observability != nil && masterAccount.Observability.Enabled && network.VpcEndpointId != "" {
//					//	err = handleVpcConnectionsForObservability(ctx, *network, cfg, masterAccount)
//					//	if err != nil {
//					//		errors <- fmt.Errorf("error updating wireguard for %s %s: %v", dirName, dir.Name(), err)
//					//		return
//					//	}
//					//}
//
//				}
//			}
//
//			if dirName == "cluster" {
//				err = saveClusterAlbDnsName(mm, out, group.ID, dir.Name())
//				if err != nil {
//					errors <- fmt.Errorf("error for %s %s: %v", dirName, dir.Name(), err)
//					return
//				}
//			}
//
//			// handle output for grafana credentials
//			if dirName == "cluster_grafana" {
//				err = saveCredsToCluster(mm, out, group.ID, dir.Name())
//				if err != nil {
//					errors <- fmt.Errorf("error for %s %s: %v", dirName, dir.Name(), err)
//					return
//				}
//			}
//			// handle output for alb dns name for environment
//			//if dirName == "environment" {
//			//	err = saveEnvironmentAlbDnsName(mm, out, group.ID, dir.Name())
//			//	if err != nil {
//			//		errors <- fmt.Errorf("error for %s %s: %v", dirName, dir.Name(), err)
//			//		return
//			//	}
//			//}
//			// handle output for rds endpoint for environment
//			if dirName == "rds" {
//				err = saveRdsEndpointAndSecret(mm, out, group.ID, dir.Name(), cfg)
//				if err != nil {
//					errors <- fmt.Errorf("error for %s %s: %v", dirName, dir.Name(), err)
//					return
//				}
//			}
//		}(d)
//	}
//
//	go func() {
//		wg.Wait()
//		close(errors)
//		close(networksMarkedDeletion)
//		close(clustersToDelete)
//		close(envsMarkedDeletion)
//	}()
//
//	errs := make([]error, 0)
//	for err := range errors {
//		errs = append(errs, err)
//	}
//	if len(errs) > 0 {
//		err := fmt.Errorf("errors occurred with applying resources in group %s: %v", group.ResourceLabel, errs)
//		return err
//	}
//
//	networksToDelete := make([]NetworkToDelete, 0)
//	for env := range networksMarkedDeletion {
//		networksToDelete = append(networksToDelete, env)
//	}
//	// destroy stuff
//	for _, network := range networksToDelete {
//		// run terraform destroy and delete network
//		_, err := terraform.DestroyTerraform(ctx, path, *execPath, roleToAssume)
//		if err != nil {
//			errors <- fmt.Errorf("error for %s %s: %v", dirName, dir.Name(), err)
//			return
//		}
//		o = mm.SoftDelete(&networks[0])
//		if o.Err != nil {
//			errors <- fmt.Errorf("Error deleting network %s: %s", networks[0].ResourceLabel, o.Err.Error())
//			return
//		}
//	}
//	return nil
//}

type clusterCredentials struct {
	types.GrafanaCredentials
	types.ArgoCdCredentials
}

type clusterEndpoints struct {
	GrafanaUrl string `json:"grafana_url"`
	ArgoCdUrl  string `json:"argocd_url"`
}

func saveClusterGrafanaOutputs(mm *magicmodel.Operator, outputs map[string]tfexec.OutputMeta, groupID string, clusterResourceLabel string) error {
	var creds clusterCredentials
	var urls clusterEndpoints

	for key, output := range outputs {
		if key == "cluster_credentials" {
			if err := types.UnmarshalWithErrorDetails(output.Value, &creds); err != nil {
				return err
			}
		}
		if key == "cluster_metadata" {
			if err := types.UnmarshalWithErrorDetails(output.Value, &urls); err != nil {
				return err
			}
		}
	}

	var clusters []types.Cluster
	o := mm.Where(&clusters, "Group.ID", groupID)
	if o.Err != nil {
		return o.Err
	}
	for _, cluster := range clusters {
		if cluster.ResourceLabel == clusterResourceLabel {
			//cluster.Metadata.Grafana.GrafanaCredentials = creds.GrafanaCredentials
			//cluster.Metadata.Grafana.EndpointMetadata = types.EndpointMetadata{RootDomain: urls.GrafanaUrl}
			cluster.Metadata.ArgoCd.ArgoCdCredentials = creds.ArgoCdCredentials
			cluster.Metadata.ArgoCd.EndpointMetadata = types.EndpointMetadata{RootDomain: urls.ArgoCdUrl}
			o = mm.Update(&cluster, "Metadata", cluster.Metadata)
			if o.Err != nil {
				return o.Err
			}
			break
		}
	}

	return nil
}

func saveClusterOutputs(mm *magicmodel.Operator, outputs map[string]tfexec.OutputMeta, groupID string, clusterResourceLabel string) error {
	for key, output := range outputs {
		if key == "alb" {
			var alb AlbMap
			if err := types.UnmarshalWithErrorDetails(output.Value, &alb); err != nil {
				return err
			}
			var clusters []types.Cluster
			o := mm.WhereV2(false, &clusters, "Group.ID", groupID)
			if o.Err != nil {
				return o.Err
			}
			for _, e := range clusters {
				if e.ResourceLabel == clusterResourceLabel {
					e.AlbDnsName = alb.DnsName
					o = mm.Update(&e, "AlbDnsName", alb.DnsName)
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

func saveRdsEndpointAndSecret(mm *magicmodel.Operator, outputs map[string]tfexec.OutputMeta, groupID string, rdsResourceLabel string, cfg aws.Config) error {
	for key, output := range outputs {
		if key == "rds_endpoint" {
			var endpoint string
			if err := types.UnmarshalWithErrorDetails(output.Value, &endpoint); err != nil {
				return err
			}
			var rds []types.Rds
			o := mm.Where(&rds, "Group.ID", groupID)
			if o.Err != nil {
				return o.Err
			}
			for _, e := range rds {
				if e.ResourceLabel == rdsResourceLabel {
					e.Endpoint = endpoint
					o = mm.Update(&e, "Endpoint", endpoint)
					if o.Err != nil {
						return o.Err
					}
					break
				}
			}
		}
		if key == "cluster_master_user_secret" {
			var secretArray []map[string]interface{}
			var secretArn string
			if err := types.UnmarshalWithErrorDetails(output.Value, &secretArray); err != nil {
				return err
			}
			for _, secret := range secretArray {
				if secret["secret_status"] == "active" {
					secretArn = secret["secret_arn"].(string)
					break
				}
			}
			// need to get the secret value
			smclient := secretsmanager.NewFromConfig(cfg)
			out, err := smclient.GetSecretValue(context.Background(), &secretsmanager.GetSecretValueInput{SecretId: &secretArn})
			if err != nil {
				return err
			}
			var usernameAndPassword map[string]string
			err = types.UnmarshalWithErrorDetails([]byte(*out.SecretString), &usernameAndPassword)
			var rds []types.Rds
			o := mm.Where(&rds, "Group.ID", groupID)
			if o.Err != nil {
				return o.Err
			}
			for _, e := range rds {
				if e.ResourceLabel == rdsResourceLabel {
					e.MasterUserSecretArn = secretArn
					o = mm.Update(&e, "MasterUserSecretArn", secretArn)
					if o.Err != nil {
						return o.Err
					}
					e.MasterUserPassword = *out.SecretString
					o = mm.Update(&e, "MasterUserPassword", usernameAndPassword["password"])
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

func saveNetworkOutputs(mm *magicmodel.Operator, outputs map[string]tfexec.OutputMeta, groupID string, network types.Network) (*types.Network, error) {
	var wireguardInstanceID string
	if err := types.UnmarshalWithErrorDetails(outputs["wireguard_instance_id"].Value, &wireguardInstanceID); err != nil {
		return nil, fmt.Errorf("Error decoding output value for key %s: %s\n", "wireguard_instance_id", err)
	}

	var wireguardPublicIP string
	if err := types.UnmarshalWithErrorDetails(outputs["wireguard_public_ip"].Value, &wireguardPublicIP); err != nil {
		return nil, fmt.Errorf("Error decoding output value for key %s: %s\n", "wireguard_public_ip", err)
	}

	var vpcMap map[string]interface{}
	if err := types.UnmarshalWithErrorDetails(outputs["vpc"].Value, &vpcMap); err != nil {
		return nil, fmt.Errorf("Error decoding output value for key %s: %s\n", "vpc", err)
	}

	vpcEndpointId := ""
	if value, ok := outputs["vpc_endpoint_id"]; ok {
		if err := types.UnmarshalWithErrorDetails(value.Value, &vpcEndpointId); err != nil {
			return nil, fmt.Errorf("Error decoding output value for key %s: %s", "vpc_endpoint_id", err)
		}
	} else {
		log.Info().Str("GroupID", groupID).Msg("vpc_endpoint_id not found in outputs")
	}

	vpcEndpointDnsName := ""
	if value, ok := outputs["vpc_endpoint_dns_name"]; ok {
		if err := types.UnmarshalWithErrorDetails(value.Value, &vpcEndpointDnsName); err != nil {
			return nil, fmt.Errorf("Error decoding output value for key %s: %s", "vpc_endpoint_dns_name", err)
		}
	} else {
		log.Info().Str("GroupID", groupID).Msg("vpc_endpoint_id not found in outputs")
	}

	//var networks []types.Network
	//o := mm.WhereV2(true, &networks, "Group.ID", groupID).WhereV2(false, &networks, "ResourceLabel", networkResourceLabel)
	//if o.Err != nil {
	//	return nil, o.Err
	//}
	//if len(networks) != 1 {
	//	return nil, fmt.Errorf("network with resource label %s not found or too many returned", networkResourceLabel)
	//}
	network.VpcID = vpcMap["vpc_id"].(string)
	network.WireguardInstanceID = wireguardInstanceID
	network.WireguardPublicIP = wireguardPublicIP
	network.VpcEndpointId = vpcEndpointId
	network.VpcEndpointDnsName = vpcEndpointDnsName

	o := mm.Save(&network)
	if o.Err != nil {
		return nil, o.Err
	}

	return &network, nil
}

// TODO THis is currently in terraform but not sure if we need it here or not currently.
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

func handleWireguardUpdates(mm *magicmodel.Operator, network types.Network, awsCfg aws.Config) error {
	// create ssm client
	client := ssm.NewFromConfig(awsCfg)
	runInitCommands := false
	if network.WireguardPublicKey == "" {
		runInitCommands = true
		privateKey, err := generateWireGuardKey()
		if err != nil {
			return err
		}
		publicKey, err := generateWireGuardPublicKey(privateKey)
		if err != nil {
			return err
		}

		network.WireguardPublicKey = strings.TrimSpace(publicKey)
		network.WireguardPrivateKey = strings.TrimSpace(privateKey)

		o := mm.Save(&network)
		if o.Err != nil {
			return o.Err
		}

		// create parameters in ssm
		err = types.UpdatePublicPrivateKeyParameters(context.Background(), &publicKey, &privateKey, network.ID, awsCfg)
		if err != nil {
			return err
		}
	}

	// update parameter in ssm (in case of port or ip range change
	var clients []types.VpnClient
	o := mm.All(&clients)
	if o.Err != nil {
		return o.Err
	}

	clientIPPublicKeyMap := make(map[string]string)
	for _, c := range clients {
		networkIds := make([]string, 0)
		for _, n := range c.Networks {
			networkIds = append(networkIds, n.ID)
		}
		if slices.Contains(networkIds, network.ID) {
			clientIPPublicKeyMap[c.ClientIP] = c.PublicKey
		}
	}

	configFile := types.CreateServerConfigFile(network.WireguardIP, network.WireguardPort, network.WireguardPrivateKey, clientIPPublicKeyMap)

	err := types.UpdateServerConfigFileParameter(context.Background(), configFile, network.ID, awsCfg)
	if err != nil {
		return err
	}

	commandId, err := types.RunSSMCommands(context.Background(), runInitCommands, network.WireguardInstanceID, awsCfg, network.ID)
	if err != nil {
		return err
	}
	time.Sleep(3 * time.Second) // Have to sleep because there is a delay in the invocation
	err = types.WaitForCommandSuccess(context.Background(), client, *commandId, network.WireguardInstanceID)
	if err != nil {
		return err
	}
	return nil
}

func generateWireGuardKey() (string, error) {
	cmd := exec.Command("wg", "genkey")
	key, err := cmd.Output()
	if err != nil {
		fmt.Println("error when generating private key:  ", string(key))
		return "", err
	}

	return string(key), nil
}

func generateWireGuardPublicKey(privateKey string) (string, error) {
	cmd := exec.Command("wg", "pubkey")
	cmd.Stdin = strings.NewReader(privateKey)
	publicKey, err := cmd.Output()
	if err != nil {
		fmt.Println("error when generating public key:  ", string(publicKey))
		return "", err
	}
	return string(publicKey), nil
}

type AlbMap struct {
	Id               string                 `json:"id"`
	DnsName          string                 `json:"dns_name"`
	ArnSuffix        string                 `json:"arn_suffix"`
	Arn              string                 `json:"arn"`
	ListenerRules    map[string]interface{} `json:"listener_rules"`
	Listeners        map[string]interface{} `json:"listeners"`
	Route53Records   []string               `json:"route_53_records"`
	SecurityGroupArn string                 `json:"security_group_arn"`
	SecurityGroupId  string                 `json:"security_group_id"`
	TargetGroups     map[string]interface{} `json:"target_groups"`
	ZoneId           string                 `json:"zone_id"`
}

func acceptPendingConnections(ctx context.Context, client *ec2.Client, serviceID string, endpointId string) error {
	// Accept VPC endpoint connections
	_, err := client.AcceptVpcEndpointConnections(ctx, &ec2.AcceptVpcEndpointConnectionsInput{
		ServiceId:      aws.String(serviceID),
		VpcEndpointIds: []string{endpointId},
	})
	return err
}
