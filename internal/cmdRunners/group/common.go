package group

import (
	"context"
	"fmt"
	"github.com/DragonOps-io/orchestrator/internal/utils"
	magicmodel "github.com/Ilios-LLC/magicmodel-go/model"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/hashicorp/terraform-exec/tfexec"
	"os"
	"strings"
	"time"

	"github.com/DragonOps-io/types"
	"github.com/rs/zerolog"
)

func init() {
	zerolog.TimeFieldFormat = time.RFC3339Nano
}

type Payload struct {
	GroupID  string `json:"group_id"`
	JobId    string `json:"job_id"`
	JobName  string `json:"job_name"`
	Region   string `json:"region"`
	UserName string `json:"user_name"`
}

var clusterTargetResources = []string{"module.eks_do_cluster_dot_resource_label", "module.vpc_cni_irsa_do_cluster_dot_resource_label", "module.ebs_csi_irsa_do_cluster_dot_resource_label", "module.autoscaler_irsa_do_cluster_dot_resource_label", "module.alb_do_cluster_dot_resource_label", "aws_acm_certificate.main_do_cluster_dot_resource_label", "aws_route53_record.validation_do_cluster_dot_resource_label", "aws_acm_certificate_validation.main_do_cluster_dot_resource_label"}
var networkTargetResources = []string{"module.vpc_do_network_dot_resource_label"}

func GetPayload() (*Payload, error) {
	val, ok := os.LookupEnv("MESSAGE")
	if !ok {
		fmt.Printf("%s not set\n", "MESSAGE")
		return nil, fmt.Errorf("%s not set\n", "MESSAGE")
	}

	payload := Payload{}
	err := types.UnmarshalWithErrorDetails([]byte(val), &payload)
	if err != nil {
		return nil, err
	}

	return &payload, nil
}

func getAllResourcesForApplyTargetingByGroupId(mm *magicmodel.Operator, groupID string) (*utils.GroupResources, error) {
	resources := utils.GroupResources{}
	o := mm.WhereV2(false, &resources.Clusters, "Group.ID", groupID)
	if o.Err != nil {
		return nil, o.Err
	}

	o = mm.WhereV2(false, &resources.Clusters, "Group.ID", groupID)
	if o.Err != nil {
		return nil, o.Err
	}
	return &resources, nil
}

func getAllResourcesByGroupId(mm *magicmodel.Operator, groupID string) (*utils.GroupResources, error) {
	resources := utils.GroupResources{}
	o := mm.WhereV2(false, &resources.Networks, "Group.ID", groupID)
	if o.Err != nil {
		return nil, o.Err
	}

	o = mm.WhereV2(false, &resources.Clusters, "Group.ID", groupID)
	if o.Err != nil {
		return nil, o.Err
	}

	o = mm.WhereV2(false, &resources.Database, "Group.ID", groupID)
	if o.Err != nil {
		return nil, o.Err
	}

	return &resources, nil
}

func deleteResourcesFromDynamo(ctx context.Context, resources *utils.GroupResources, mm *magicmodel.Operator, cfg aws.Config) error {
	for _, cluster := range resources.Clusters {
		o := mm.SoftDelete(&cluster)
		if o.Err != nil {
			return o.Err
		}
	}

	for _, network := range resources.Networks {
		client := ssm.NewFromConfig(cfg)
		_, err := client.DeleteParameters(ctx, &ssm.DeleteParametersInput{
			Names: []string{
				fmt.Sprintf("/%s/wireguard/public_key", network.ID),
				fmt.Sprintf("/%s/wireguard/private_key", network.ID),
				fmt.Sprintf("/%s/wireguard/config_file", network.ID),
			},
		})
		if err != nil {
			// DeleteParameters does not fail with an error if parameters are missing, so no need to check fof NotFound error here
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

		o := mm.SoftDelete(&network)
		if o.Err != nil {
			return o.Err
		}
	}

	for _, db := range resources.Database {
		o := mm.SoftDelete(&db)
		if o.Err != nil {
			return o.Err
		}
	}
	return nil
}

func getExactTerraformResourceNamesForTargetApply(allResourcesToApply *utils.GroupResources) []string {
	var terraformResources []string
	for _, network := range allResourcesToApply.Networks {
		for _, r := range networkTargetResources {
			replacedString := strings.Replace(r, "do_network_dot_resource_label", network.ResourceLabel, -1)
			terraformResources = append(terraformResources, replacedString)
		}
	}

	for _, cluster := range allResourcesToApply.Clusters {
		for _, r := range clusterTargetResources {
			replacedString := strings.Replace(r, "do_cluster_dot_resource_label", cluster.ResourceLabel, -1)
			terraformResources = append(terraformResources, replacedString)
		}
	}

	return terraformResources
}

func deleteGroupResources(mm *magicmodel.Operator, allResourcesToDelete *utils.GroupResources) error {
	for _, network := range allResourcesToDelete.Networks {
		o := mm.SoftDelete(&network)
		if o.Err != nil {
			return o.Err
		}
	}
	for _, cluster := range allResourcesToDelete.Clusters {
		o := mm.SoftDelete(&cluster)
		if o.Err != nil {
			return o.Err
		}
	}

	for _, Database := range allResourcesToDelete.Database {
		o := mm.SoftDelete(&Database)
		if o.Err != nil {
			return o.Err
		}
	}
	return nil
}

func runWorkerGroupApply(mm *magicmodel.Operator, group types.Group, jobId, masterAcctRegion string) error {
	// Re-template now that the resources is deleted -- worker won't even generate the terraform, so we can safely apply
	command := fmt.Sprintf("/app/worker group apply --group-id %s --table-region %s", group.ID, masterAcctRegion)
	if os.Getenv("IS_LOCAL") == "true" {
		command = fmt.Sprintf("./app/worker group apply --group-id %s --table-region %s", group.ID, masterAcctRegion)
	}

	msg, err := utils.RunOSCommandOrFail(command)
	if err != nil {
		group.Status = "APPLY_FAILED"
		group.FailedReason = err.Error()
		so := mm.Save(&group)
		if so.Err != nil {
			return so.Err
		}
		if msg != nil {
			return fmt.Errorf("error running `worker group apply` for group with id %s: %s: %s", group.ID, err, *msg)
		} else {
			return fmt.Errorf("error running `worker group apply` for group with id %s: %s", group.ID, err)
		}
	}
	return nil
}

func handleTerraformOutputs(mm *magicmodel.Operator, group types.Group, out map[string]tfexec.OutputMeta, cfg aws.Config) error {
	resources, err := getAllResourcesByGroupId(mm, group.ID)
	if err != nil {
		return err
	}

	for _, network := range resources.Networks {
		var n *types.Network
		n, err := saveNetworkOutputs(mm, network, out)
		if err != nil {
			return err
		}
		err = handleWireguardUpdates(mm, *n, cfg)
		if err != nil {
			return err
		}
	}

	for _, cluster := range resources.Clusters {
		err = saveClusterOutputs(mm, cluster, out)
		if err != nil {
			return err
		}
	}

	for _, r := range resources.Database {
		err = saveDatabaseOutputs(mm, out, r, cfg)
		if err != nil {
			return err
		}
	}
	return nil
}
