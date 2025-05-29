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
			break
		}
	}
	return nil
}
