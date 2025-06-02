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
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

func init() {
	zerolog.TimeFieldFormat = time.RFC3339Nano
}

type Payload struct {
	AppID            string   `json:"app_id"`
	EnvironmentNames []string `json:"environment_names"`
	JobId            string   `json:"job_id"`
	JobName          string   `json:"job_name"`
	Region           string   `json:"region"`
	UserName         string   `json:"user_name"`
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

func handleAppEnvironmentOutputs(ctx context.Context, app types.App, envKey string, mm *magicmodel.Operator, out map[string]tfexec.OutputMeta, awsCfg *aws.Config, albZoneMap map[string]string) error {
	envConfig, ok := app.Environments[envKey]
	if ok {
		envConfig.Status = "APPLIED"
		envConfig.FailedReason = ""

		// Extract app URL
		var appUrl AppUrl
		if err := json.Unmarshal(out["app_url"].Value, &appUrl); err != nil {
			fmt.Printf("Error decoding output value for key %s: %s\n", "app_url", err)
		}
		envConfig.Endpoint = string(appUrl)
		fmt.Println(envConfig.Endpoint)
		switch app.SubType {
		case "static":
			var cfDistroID CloudfrontDistroID
			if err := json.Unmarshal(out["cloudfront_distribution_id"].Value, &cfDistroID); err != nil {
				fmt.Printf("Error decoding output value for key %s: %s\n", "cloudfront_distribution_id", err)
			}

			var cfDnsName string
			if err := json.Unmarshal(out["cloudfront_dns_name"].Value, &cfDnsName); err != nil {
				fmt.Printf("Error decoding output value for key %s: %s\n", "cloudfront_dns_name", err)
			}
			envConfig.CloudfrontDistroID = string(cfDistroID)

			err := handleRoute53Domains(envConfig.Route53DomainNames, cfDnsName, awsCfg, ctx, "Z2FDTNDATAQYW2", app.ID)
			if err != nil {
				fmt.Println(err)
				if ue := utils.UpdateSingleEnvironmentStatus(app, envKey, "APPLY_FAILED", mm, err.Error()); ue != nil {
					return ue
				}
				return fmt.Errorf("Error handling route53 domains for app with id %s and environment with name %s: %v", app.ID, envKey, err)
			}
		case "serverless":
			fmt.Println("serverless app")
			var apiGatewayDnsHostedZoneId string
			if err := json.Unmarshal(out["api_gateway_dns_hosted_zone_id"].Value, &apiGatewayDnsHostedZoneId); err != nil {
				fmt.Printf("Error decoding output value for key %s: %s\n", "api_gateway_dns_hosted_zone_id", err)
			}

			var apiGatewayDnsName string
			if err := json.Unmarshal(out["api_gateway_dns_name"].Value, &apiGatewayDnsName); err != nil {
				fmt.Printf("Error decoding output value for key %s: %s\n", "api_gateway_dns_name", err)
			}

			err := handleRoute53Domains(envConfig.Route53DomainNames, apiGatewayDnsName, awsCfg, ctx, apiGatewayDnsHostedZoneId, app.ID)
			if err != nil {
				if ue := utils.UpdateSingleEnvironmentStatus(app, envKey, "APPLY_FAILED", mm, err.Error()); ue != nil {
					return ue
				}
				return fmt.Errorf("Error handling route53 domains for app with id %s and environment with name %s: %v", app.ID, envKey, err)
			}
			fmt.Println("created route53 domains i think")
		default:
			var appDashboardUrl string
			if err := json.Unmarshal(out["app_dashboard_url"].Value, &appDashboardUrl); err != nil {
				fmt.Printf("Error decoding output value for key %s: %s\n", "app_dashboard_url", err)
			}

			app.ObservabilityUrls = &types.ObservabilityUrls{
				UnifiedDashboard: appDashboardUrl,
			}

			var cluster types.Cluster
			var re = regexp.MustCompile(`^[^\s.]+\.([^\s.]+)$`)
			isValidFormat := re.MatchString(envConfig.Cluster)
			if !isValidFormat {
				return fmt.Errorf("error validating networks in application %s in environment %s: networks defined must have a group and cluster resource label, sparaterd by a `.`, ie: group_resource_label.network_resource_label", app.Name, envKey)
			}
			var clusters []types.Cluster
			o := mm.WhereV2(true, &clusters, "Group.ResourceLabel", strings.Split(envConfig.Cluster, ".")[0]).WhereV2(false, &clusters, "ResourceLabel", strings.Split(envConfig.Cluster, ".")[1])
			if o.Err != nil {
				if ue := utils.UpdateSingleEnvironmentStatus(app, envKey, "APPLY_FAILED", mm, o.Err.Error()); ue != nil {
					return ue
				}
				return fmt.Errorf("Error finding cluster %s: %v", envConfig.Cluster, o.Err)
			}
			if len(clusters) == 0 {
				if ue := utils.UpdateSingleEnvironmentStatus(app, envKey, "APPLY_FAILED", mm, fmt.Errorf("No cluster found for resource label %s", envConfig.Cluster).Error()); ue != nil {
					return ue
				}
				return fmt.Errorf("No cluster found for resource label %s", envConfig.Cluster)
			}
			cluster = clusters[0]
			err := handleRoute53Domains(envConfig.Route53DomainNames, cluster.AlbDnsName, awsCfg, ctx, albZoneMap[cluster.Group.Account.Region], app.ID)
			if err != nil {
				if ue := utils.UpdateSingleEnvironmentStatus(app, envKey, "APPLY_FAILED", mm, err.Error()); ue != nil {
					return ue
				}
				return fmt.Errorf("Error handling route53 domains for app with id %s and environment with name %s: %v", app.ID, envKey, err)
			}
		}

		// write it back into the map
		app.Environments[envKey] = envConfig
	}

	return nil
}
