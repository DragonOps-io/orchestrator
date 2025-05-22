package app

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/DragonOps-io/orchestrator/internal/utils"
	"github.com/DragonOps-io/types"
	magicmodel "github.com/Ilios-LLC/magicmodel-go/model"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/rs/zerolog/log"
	"os"
	"time"

	"github.com/rs/zerolog"
)

func init() {
	zerolog.TimeFieldFormat = time.RFC3339Nano
}

type Payload struct {
	AppID          string   `json:"app_id"`
	EnvironmentIDs []string `json:"environment_ids"`
	JobId          string   `json:"job_id"`
	JobName        string   `json:"job_name"`
	Region         string   `json:"region"`
	UserName       string   `json:"user_name"`
}

func GetPayload() (*Payload, error) {
	val, ok := os.LookupEnv("MESSAGE")
	if !ok {
		fmt.Printf("%s not set\n", "MESSAGE")
		return nil, fmt.Errorf("%s not set\n", "MESSAGE")
	}

	payload := Payload{}
	err := json.Unmarshal([]byte(val), &payload)
	if err != nil {
		return nil, err
	}

	return &payload, nil
}

func runWorkerAppApply(mm *magicmodel.Operator, app types.App, appPath string, env types.Environment, masterAcctRegion string) error {
	command := fmt.Sprintf("/app/worker app apply --app-id %s --environment-id %s --table-region %s", app.ID, env.ID, masterAcctRegion)
	os.Setenv("DRAGONOPS_TERRAFORM_DESTINATION", appPath)

	if os.Getenv("IS_LOCAL") == "true" {
		appPath = fmt.Sprintf("./apps/%s/%s", app.ID, env.ID)
		os.Setenv("DRAGONOPS_TERRAFORM_DESTINATION", appPath)
		command = fmt.Sprintf("./app/worker app apply --app-id %s --environment-id %s --table-region %s", app.ID, env.ID, masterAcctRegion)
	}

	log.Debug().Str("AppID", app.ID).Msg(fmt.Sprintf("Templating terraform application files for environment %s", env.Name))
	msg, err := utils.RunOSCommandOrFail(command)
	if err != nil {
		ue := updateSingleEnvironmentStatusToApplyFailed(app, env, mm, fmt.Errorf("Error running `worker app apply` with app with id %s and environment with id %s: %v - %v", app.ID, env.ID, err, msg))
		if ue != nil {
			return ue
		}
		return fmt.Errorf("error running `worker app apply` with app with id %s and environment with id %s: %v - %v", app.ID, env.ID, err, msg)
	}
	log.Debug().Str("AppID", app.ID).Msg(*msg)
	return nil
}

func handleAppEnvironmentOutputs(ctx context.Context, app types.App, env types.Environment, mm *magicmodel.Operator, out map[string]tfexec.OutputMeta, awsCfg *aws.Config, albZoneMap map[string]string) error {
	for idx := range app.Environments {
		if app.Environments[idx].Environment == env.ResourceLabel && app.Environments[idx].Group == env.Group.ResourceLabel {
			app.Environments[idx].Status = "APPLIED"
			app.Environments[idx].FailedReason = ""
			var appUrl AppUrl
			if err := json.Unmarshal(out["app_url"].Value, &appUrl); err != nil {
				fmt.Printf("Error decoding output value for key %s: %s\n", "app_url", err)
			}
			app.Environments[idx].Endpoint = string(appUrl)
			// TODO lambda / serverless will be in here too!
			if app.SubType == "static" {
				var cfDistroID CloudfrontDistroID
				if err := json.Unmarshal(out["cloudfront_distribution_id"].Value, &cfDistroID); err != nil {
					fmt.Printf("Error decoding output value for key %s: %s\n", "cloudfront_distribution_id", err)
				}

				var cfDnsName string
				if err := json.Unmarshal(out["cloudfront_dns_name"].Value, &cfDnsName); err != nil {
					fmt.Printf("Error decoding output value for key %s: %s\n", "cloudfront_dns_name", err)
				}
				app.Environments[idx].CloudfrontDistroID = string(cfDistroID)

				err := handleRoute53Domains(app.Environments[idx].Route53DomainNames, cfDnsName, awsCfg, ctx, "Z2FDTNDATAQYW2", app.ID)
				if err != nil {
					ue := updateSingleEnvironmentStatusToApplyFailed(app, env, mm, err)
					if ue != nil {
						return ue
					}
				}
			} else {
				// handle the dashboard outputs
				var appDashboardUrl string
				if err := json.Unmarshal(out["app_dashboard_url"].Value, &appDashboardUrl); err != nil {
					fmt.Printf("Error decoding output value for key %s: %s\n", "app_dashboard_url", err)
				}
				// need to get the unique id for loki datasource from cluster
				app.Environments[idx].ObservabilityUrls = &types.ObservabilityUrls{
					UnifiedDashboard: appDashboardUrl,
				}

				app.ObservabilityUrls = &types.ObservabilityUrls{
					UnifiedDashboard: appDashboardUrl,
				}

				if app.SubType == "server" {
					// get the cluster the environment belongs to and that's the alb name
					var cluster types.Cluster
					o := mm.Find(&cluster, env.Cluster.ID)
					if o.Err != nil {
						ue := updateSingleEnvironmentStatusToApplyFailed(app, env, mm, o.Err)
						if ue != nil {
							return ue
						}
						return fmt.Errorf("Error finding cluster with id %s: %v", env.Cluster.ID, o.Err)
					}
					err := handleRoute53Domains(app.Environments[idx].Route53DomainNames, cluster.AlbDnsName, awsCfg, ctx, albZoneMap[env.Group.Account.Region], app.ID)
					if err != nil {
						ue := updateSingleEnvironmentStatusToApplyFailed(app, env, mm, err)
						if ue != nil {
							return ue
						}
						return fmt.Errorf("Error handling route53 domains for app with id %s and environment with id %s: %v", app.ID, env.ID, err)
					}
				}
			}
			break
		}
	}
	return nil
}

// TODO Example of go routines
//func destroy(ctx context.Context, mm *magicmodel.Operator, group types.Group, execPath *string, roleToAssume *string, dirName string, cfg aws.Config, payload Payload) error {
//	directoryPath := filepath.Join(os.Getenv("DRAGONOPS_TERRAFORM_DESTINATION"), dirName)
//	directories, _ := os.ReadDir(directoryPath)
//	log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Destroying all %ss", dirName))
//
//	// go routine setup stuff
//	wg := &sync.WaitGroup{}
//	errors := make(chan error, 0)
//	cancelCtx, cancel := context.WithCancel(context.Background())
//	defer cancel()
//	for _, d := range directories {
//		wg.Add(1)
//		log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Destroying %s %s", dirName, d.Name()))
//		path, _ := filepath.Abs(filepath.Join(directoryPath, d.Name()))
//
//		go func(dir os.DirEntry) {
//			defer wg.Done()
//			if strings.Contains(path, "cluster") && !strings.Contains(path, "cluster_grafana") {
//				fmt.Println(dir.Name())
//				clusterResourceLabel := strings.Split(strings.Split(path, "/groups/")[1], "/")[2]
//				// setup the client before the go routine?
//				ec2Client := ec2.NewFromConfig(cfg, func(o *ec2.Options) { o.Region = cfg.Region })
//				tagClient := resourcegroupstaggingapi.NewFromConfig(cfg, func(o *resourcegroupstaggingapi.Options) { o.Region = cfg.Region })
//				// get the cluster so we have the name
//				var clusters []types.Cluster
//				var cluster *types.Cluster
//				o := mm.Where(&clusters, "ResourceLabel", clusterResourceLabel)
//				if o.Err != nil {
//					log.Warn().Msg(fmt.Sprintf("error getting clusters: %s", o.Err.Error()))
//				}
//				for idx := range clusters {
//					if clusters[idx].Group.ResourceLabel == group.ResourceLabel {
//						cluster = &clusters[idx]
//					}
//				}
//				if cluster == nil {
//					// cluster not found what then?
//					log.Warn().Str("GroupID", group.ID).Msg("cluster not found and cannot delete spot instances")
//				} else {
//					go func() {
//						select {
//						case <-cancelCtx.Done():
//							return
//						default:
//						}
//						time.Sleep(30 * time.Second)
//						deleteSpotInstances(cancelCtx, group.Name, group.ID, cluster.Name, ec2Client, tagClient, payload)
//					}()
//				}
//			}
//			// destroy terraform or return an error
//			_, err := terraform.DestroyTerraform(ctx, path, *execPath, roleToAssume)
//			if err != nil {
//				errors <- fmt.Errorf("error for %s %s: %v", dirName, dir.Name(), err)
//				return
//			}
//			return
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
//		err := fmt.Errorf("errors occurred with destroying resources in group %s: %v", group.ResourceLabel, errs)
//		return err
//	}
//	return nil
//}
