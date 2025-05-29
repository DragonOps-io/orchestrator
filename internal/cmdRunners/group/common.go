package group

import (
	"context"
	"fmt"
	"github.com/DragonOps-io/orchestrator/internal/utils"
	magicmodel "github.com/Ilios-LLC/magicmodel-go/model"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/rs/zerolog/log"
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

type GroupResources struct {
	Networks []types.Network
	Clusters []types.Cluster
	Envs     []types.Environment
	Rds      []types.Rds
}

func getAllResourcesToDeleteByGroupId(mm *magicmodel.Operator, groupID string) (*GroupResources, error) {
	resources := GroupResources{}
	o := mm.WhereV3(true, &resources.Networks, "Group.ID", groupID).WhereV3(false, &resources.Networks, "MarkedForDeletion", true)
	if o.Err != nil {
		return nil, o.Err
	}
	o = mm.WhereV3(true, &resources.Clusters, "Group.ID", groupID).WhereV3(false, &resources.Clusters, "MarkedForDeletion", true)
	if o.Err != nil {
		return nil, o.Err
	}
	o = mm.WhereV3(true, &resources.Envs, "Group.ID", groupID).WhereV3(false, &resources.Envs, "MarkedForDeletion", true)
	if o.Err != nil {
		return nil, o.Err
	}
	o = mm.WhereV3(true, &resources.Rds, "Group.ID", groupID).WhereV3(false, &resources.Rds, "MarkedForDeletion", true)
	if o.Err != nil {
		return nil, o.Err
	}
	return &resources, nil
}

func getAllResourcesForApplyTargetingByGroupId(mm *magicmodel.Operator, groupID string) (*GroupResources, error) {
	resources := GroupResources{}
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

func getAllResourcesByGroupId(mm *magicmodel.Operator, groupID string) (*GroupResources, error) {
	resources := GroupResources{}
	o := mm.WhereV2(false, &resources.Networks, "Group.ID", groupID)
	if o.Err != nil {
		return nil, o.Err
	}

	o = mm.WhereV2(false, &resources.Clusters, "Group.ID", groupID)
	if o.Err != nil {
		return nil, o.Err
	}

	o = mm.WhereV2(false, &resources.Envs, "Group.ID", groupID)
	if o.Err != nil {
		return nil, o.Err
	}

	o = mm.WhereV2(false, &resources.Rds, "Group.ID", groupID)
	if o.Err != nil {
		return nil, o.Err
	}

	return &resources, nil
}

func deleteResourcesFromDynamo(ctx context.Context, resources *GroupResources, mm *magicmodel.Operator, group types.Group, payload Payload, cfg aws.Config) error {
	for _, cluster := range resources.Clusters {
		log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Deleting cluster %s record from DynamoDb.", cluster.Name))
		o := mm.SoftDelete(&cluster)
		if o.Err != nil {
			return o.Err
		}
	}

	for _, network := range resources.Networks {
		client := ssm.NewFromConfig(cfg)
		log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Deleting network parameters for network %s.", network.Name))
		_, err := client.DeleteParameters(ctx, &ssm.DeleteParametersInput{
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
		o := mm.SoftDelete(&network)
		if o.Err != nil {
			return o.Err
		}
	}

	for _, env := range resources.Envs {
		log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Deleting environment %s record from DynamoDb.", env.Name))
		o := mm.SoftDelete(&env)
		if o.Err != nil {
			return o.Err
		}
	}

	for _, db := range resources.Rds {
		log.Debug().Str("GroupID", group.ID).Str("JobId", payload.JobId).Msg(fmt.Sprintf("Deleting database %s record from DynamoDb.", db.Name))
		o := mm.SoftDelete(&db)
		if o.Err != nil {
			return o.Err
		}
	}
	return nil
}

func getExactTerraformResourceNames(allResourcesToDelete *GroupResources, resources Resources) []string {
	var terraformResourcesToDelete []string
	for _, network := range allResourcesToDelete.Networks {
		for _, r := range resources.Data["network"] {
			replacedString := strings.Replace(r, "do_network_dot_resource_label", network.ResourceLabel, -1)
			terraformResourcesToDelete = append(terraformResourcesToDelete, replacedString)
		}
	}
	for _, cluster := range allResourcesToDelete.Clusters {
		for _, r := range resources.Data["cluster"] {
			replacedString := strings.Replace(r, "do_cluster_dot_resource_label", cluster.ResourceLabel, -1)
			terraformResourcesToDelete = append(terraformResourcesToDelete, replacedString)
		}
	}

	for _, environment := range allResourcesToDelete.Envs {
		for _, r := range resources.Data["environment"] {
			replacedString := strings.Replace(r, "do_environment_dot_resource_label", environment.ResourceLabel, -1)
			terraformResourcesToDelete = append(terraformResourcesToDelete, replacedString)
		}
	}
	for _, rds := range allResourcesToDelete.Rds {
		for _, r := range resources.Data["network"] {
			replacedString := strings.Replace(r, "do_rds_dot_resource_label", rds.ResourceLabel, -1)
			terraformResourcesToDelete = append(terraformResourcesToDelete, replacedString)
		}
	}
	return terraformResourcesToDelete
}

func getExactTerraformResourceNamesForTargetApply(allResourcesToApply *GroupResources) []string {
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

func deleteGroupResources(mm *magicmodel.Operator, allResourcesToDelete *GroupResources) error {
	// delete each resources that we deleted above
	for _, network := range allResourcesToDelete.Networks {
		// loop through resources.Network and replace dot_whatever with the actual resource label
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

	for _, environment := range allResourcesToDelete.Envs {
		o := mm.SoftDelete(&environment)
		if o.Err != nil {
			return o.Err
		}
	}
	for _, rds := range allResourcesToDelete.Rds {
		o := mm.SoftDelete(&rds)
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

func runWorkerResourcesList(group types.Group, mm *magicmodel.Operator, jobId string) (*string, error) {
	command := "/app/worker resources list"
	if os.Getenv("IS_LOCAL") == "true" {
		command = "./app/worker resources list"
	}
	msg, err := utils.RunOSCommandOrFail(command)
	if err != nil {
		group.Status = "APPLY_FAILED"
		group.FailedReason = err.Error()
		so := mm.Save(&group)
		if so.Err != nil {
			return nil, so.Err
		}
		if msg != nil {
			return nil, fmt.Errorf("error running `worker group apply` for group with id %s: %s: %s", group.ID, err, *msg)
		} else {
			return nil, fmt.Errorf("error running `worker group apply` for group with id %s: %s", group.ID, err)
		}
	}
	return msg, nil
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

	for _, r := range resources.Rds {
		err = saveRdsOutputs(mm, out, r, cfg)
		if err != nil {
			return err
		}
	}
	return nil
}
