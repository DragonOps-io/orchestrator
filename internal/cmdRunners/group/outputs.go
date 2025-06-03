package group

import (
	"context"
	"fmt"
	"github.com/DragonOps-io/types"
	magicmodel "github.com/Ilios-LLC/magicmodel-go/model"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/hashicorp/terraform-exec/tfexec"
	"strings"
)

type clusterCredentials struct {
	types.GrafanaCredentials
	types.ArgoCdCredentials
}

type clusterEndpoints struct {
	GrafanaUrl string `json:"grafana_url"`
	ArgoCdUrl  string `json:"argocd_url"`
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

func saveNetworkOutputs(mm *magicmodel.Operator, network types.Network, outputs map[string]tfexec.OutputMeta) (*types.Network, error) {
	suffix := "_" + network.ResourceLabel
	var wireguardInstanceID string
	var wireguardPublicIP string
	var vpcMap map[string]interface{}
	vpcEndpointId := ""
	vpcEndpointDnsName := ""

	for key, output := range outputs {
		// Skip outputs that don't belong to this cluster
		if !strings.HasSuffix(key, suffix) {
			continue
		}
		baseKey := strings.TrimSuffix(key, suffix)
		switch baseKey {
		case "wireguard_instance_id":
			if err := types.UnmarshalWithErrorDetails(output.Value, &wireguardInstanceID); err != nil {
				return nil, fmt.Errorf("error decoding output value for key %s: %s\n", "wireguard_instance_id", err)
			}
		case "wireguard_public_ip":
			if err := types.UnmarshalWithErrorDetails(output.Value, &wireguardPublicIP); err != nil {
				return nil, fmt.Errorf("error decoding output value for key %s: %s\n", "wireguard_public_ip", err)
			}
		case "vpc":
			if err := types.UnmarshalWithErrorDetails(output.Value, &vpcMap); err != nil {
				return nil, fmt.Errorf("error decoding output value for key %s: %s\n", "vpc", err)
			}
		case "vpc_endpoint_id":
			if err := types.UnmarshalWithErrorDetails(output.Value, &vpcEndpointId); err != nil {
				return nil, fmt.Errorf("error decoding output value for key %s: %s", "vpc_endpoint_id", err)
			}
		case "vpc_endpoint_dns_name":
			if err := types.UnmarshalWithErrorDetails(output.Value, &vpcEndpointDnsName); err != nil {
				return nil, fmt.Errorf("error decoding output value for key %s: %s", "vpc_endpoint_dns_name", err)
			}
		}
	}

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

func saveClusterOutputs(mm *magicmodel.Operator, cluster types.Cluster, outputs map[string]tfexec.OutputMeta) error {
	var creds clusterCredentials
	var urls clusterEndpoints
	var alb AlbMap

	suffix := "_" + cluster.ResourceLabel

	for key, output := range outputs {
		// Skip outputs that don't belong to this cluster
		if !strings.HasSuffix(key, suffix) {
			continue
		}

		baseKey := strings.TrimSuffix(key, suffix)

		switch baseKey {
		case "cluster_credentials":
			if err := types.UnmarshalWithErrorDetails(output.Value, &creds); err != nil {
				return fmt.Errorf("unmarshal cluster_credentials: %w", err)
			}
		case "cluster_metadata":
			if err := types.UnmarshalWithErrorDetails(output.Value, &urls); err != nil {
				return fmt.Errorf("unmarshal cluster_metadata: %w", err)
			}
		case "alb":
			if err := types.UnmarshalWithErrorDetails(output.Value, &alb); err != nil {
				return fmt.Errorf("unmarshal alb: %w", err)
			}
		default:
			fmt.Println("Skipping output key:", key, "as it does not match expected keys for cluster outputs.")
		}
	}

	cluster.AlbDnsName = alb.DnsName
	cluster.Metadata.ArgoCd.ArgoCdCredentials = creds.ArgoCdCredentials
	cluster.Metadata.ArgoCd.EndpointMetadata = types.EndpointMetadata{
		RootDomain: urls.ArgoCdUrl,
	}

	if o := mm.Save(&cluster); o.Err != nil {
		return o.Err
	}

	return nil
}

func saveDatabaseOutputs(mm *magicmodel.Operator, outputs map[string]tfexec.OutputMeta, database types.Database, cfg aws.Config) error {
	suffix := "_" + database.ResourceLabel
	var secretArray []map[string]interface{}
	var secretArn string

	for key, output := range outputs {
		// Skip outputs that don't belong to this cluster
		if !strings.HasSuffix(key, suffix) {
			continue
		}

		baseKey := strings.TrimSuffix(key, suffix)

		switch baseKey {
		case "rds_endpoint":
			if err := types.UnmarshalWithErrorDetails(output.Value, &database.Endpoint); err != nil {
				return fmt.Errorf("unmarshal endpoint: %w", err)
			}
		case "cluster_master_user_secret":
			if err := types.UnmarshalWithErrorDetails(output.Value, &secretArray); err != nil {
				return fmt.Errorf("unmarshal clsuter_master_user_secret: %w", err)
			}
			for _, secret := range secretArray {
				if secret["secret_status"] == "active" {
					secretArn = secret["secret_arn"].(string)
					break
				}
			}
			// need to get the secret value
			smClient := secretsmanager.NewFromConfig(cfg)
			out, err := smClient.GetSecretValue(context.Background(), &secretsmanager.GetSecretValueInput{SecretId: &secretArn})
			if err != nil {
				return err
			}
			var usernameAndPassword map[string]string
			err = types.UnmarshalWithErrorDetails([]byte(*out.SecretString), &usernameAndPassword)
			if err != nil {
				return fmt.Errorf("unmarshal username_and_password fail: %w", err)
			}
			database.MasterUserSecretArn = secretArn
			database.MasterUserPassword = *out.SecretString
		}
	}

	o := mm.Save(&database)
	if o.Err != nil {
		return o.Err
	}
	return nil
}
