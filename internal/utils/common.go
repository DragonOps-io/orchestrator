package utils

import (
	"context"
	"fmt"
	magicmodel "github.com/Ilios-LLC/magicmodel-go/model"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/rs/zerolog/log"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/DragonOps-io/types"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

func RunOSCommandOrFail(command string) (*string, error) {
	msg, err := exec.Command("/bin/sh", "-c", command).CombinedOutput()
	out := string(msg)
	if err != nil {
		return &out, err
	}
	return &out, nil
}

type IsValidResponse struct {
	UserId                     int    `json:"user_id"`
	Team                       int    `json:"team"`
	IsValid                    bool   `json:"is_valid"`
	MasterAccountAccessRoleArn string `json:"master_account_access_role_arn"`
	MasterAccountRegion        string `json:"master_account_region"`
	UserName                   string `json:"user_name"`
}

func IsApiKeyValid(doApiKey string) (*IsValidResponse, error) {
	apiEndpoint, ok := os.LookupEnv("DRAGONOPS_API")
	if !ok {
		apiEndpoint = "https://api.dragonops.io"
	}

	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/valid", apiEndpoint), nil)
	if err != nil {
		return nil, err
	}

	req.Header.Add("do-api-key", doApiKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("error occurred: %s; detail: %s", resp.Status, body)
	}

	response := IsValidResponse{}
	err = types.UnmarshalWithErrorDetails(body, &response)
	if err != nil {
		return nil, err
	}
	return &response, nil
}

func RetrieveBugsnagApiKey(devKey string, repo string, apiKey string) (*string, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/bugsnag", os.Getenv("DRAGONOPS_API")), nil)
	if err != nil {
		return nil, err
	}

	req.Header.Add("do-developer-key", devKey)
	req.Header.Add("do-repo", "bugsnagOrchestratorKey")
	req.Header.Add("do-api-key", apiKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("error occurred: %s; detail: %s", resp.Status, body)
	}

	response := ""
	err = types.UnmarshalWithErrorDetails(body, &response)
	if err != nil {
		return nil, err
	}
	return &response, nil
}

func GetDoApiKeyFromSecretsManager(ctx context.Context, cfg aws.Config, userName string) (*string, error) {
	smClient := secretsmanager.NewFromConfig(cfg)

	secretName := fmt.Sprintf("do-api-key/%s", userName)
	resp, err := smClient.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: &secretName,
	})
	if err != nil {
		return nil, err
	}
	return resp.SecretString, err
}

func UpdateAllEnvironmentStatuses(app types.App, environments []string, status string, mm *magicmodel.Operator, errMsg string) error {
	for _, environment := range environments {
		if env, ok := app.Environments[environment]; ok {
			env.Status = status
			env.FailedReason = errMsg
			app.Environments[environment] = env
		}
	}

	aco := mm.Update(&app, "Environments", app.Environments)
	if aco.Err != nil {
		return aco.Err
	}
	return nil
}

func UpdateSingleEnvironmentStatus(app types.App, envName, status string, mm *magicmodel.Operator, errMsg string) error {
	if appEnv, exists := app.Environments[envName]; exists {
		appEnv.Status = status
		appEnv.FailedReason = errMsg
		app.Environments[envName] = appEnv
		aco := mm.Update(&app, "Environments", app.Environments)
		if aco.Err != nil {
			return aco.Err
		}
		// Only update the deployment status if the status is FAILED
		if status == "APPLY_FAILED" {
			ue := FindAndUpdateDeploymentStatus(app, envName, "FAILED", mm, errMsg)
			if ue != nil {
				return fmt.Errorf("error updating deployment status for env %s: %v", envName, ue)
			}
		}
	}
	return nil
}

func UpdateDeploymentStatus(deploymentId, status string, mm *magicmodel.Operator, errMsg string) error {
	var foundDeployment *types.Deployment
	o := mm.Find(&foundDeployment, deploymentId)
	if o.Err != nil {
		return fmt.Errorf("error retrieving deployment with id %s: %v", deploymentId, o.Err)
	}
	foundDeployment.Status = types.DeploymentStatus(status)
	foundDeployment.FailedReason = errMsg
	foundDeployment.CompletedAt = aws.Time(time.Now())
	aco := mm.Save(&foundDeployment)
	if aco.Err != nil {
		return aco.Err
	}
	return nil
}

func FindAndUpdateDeploymentStatus(app types.App, envName, status string, mm *magicmodel.Operator, errMsg string) error {
	deployedVersion := ""
	if appEnv, exists := app.Environments[envName]; exists {
		deployedVersion = appEnv.DeployedVersion
	}
	if deployedVersion == "" {
		return fmt.Errorf("no deployed version found for app %s in environment %s", app.ID, envName)
	}
	deployments := []types.Deployment{}

	o := mm.WhereV4(true, &deployments, "AppName", app.Name).WhereV4(true, &deployments, "Environment", envName).WhereV4(true, &deployments, "Status", "IN_PROGRESS").WhereV4(true, &deployments, "CurrentVersion", deployedVersion).WhereV4(false, &deployments, "DesiredVersion", deployedVersion)
	if o.Err != nil {
		return fmt.Errorf("error retrieving deployments for app %s in environment %s: %v", app.ID, envName, o.Err)
	}

	if len(deployments) == 0 {
		return fmt.Errorf("no deployments found for app %s in environment %s with deployed version %s", app.ID, envName, deployedVersion)
	}

	var foundDeployment *types.Deployment
	if len(deployments) > 1 {
		sort.Slice(deployments, func(i, j int) bool {
			return deployments[i].StartedAt.After(deployments[j].StartedAt)
		})
		foundDeployment = &deployments[0]
	}
	foundDeployment.Status = types.DeploymentStatus(status)
	foundDeployment.FailedReason = errMsg
	foundDeployment.CompletedAt = aws.Time(time.Now())
	aco := mm.Save(&foundDeployment)
	if aco.Err != nil {
		return aco.Err
	}
	return nil
}

func RunWorkerAppApply(mm *magicmodel.Operator, app types.App, appPath, envName, masterAcctRegion string) error {
	command := fmt.Sprintf("/app/worker app apply --app-id %s --environment-name %s --table-region %s", app.ID, envName, masterAcctRegion)
	os.Setenv("DRAGONOPS_TERRAFORM_DESTINATION", appPath)

	if os.Getenv("IS_LOCAL") == "true" {
		appPath = fmt.Sprintf("./apps/%s/%s", app.ID, envName)
		os.Setenv("DRAGONOPS_TERRAFORM_DESTINATION", appPath)
		command = fmt.Sprintf("./app/worker app apply --app-id %s --environment-name %s --table-region %s", app.ID, envName, masterAcctRegion)
	}

	log.Info().Str("AppID", app.ID).Msg(fmt.Sprintf("Templating terraform application files for environment %s", envName))
	msg, err := RunOSCommandOrFail(command)
	if err != nil {
		ue := UpdateSingleEnvironmentStatus(app, envName, "APPLY_FAILED", mm, fmt.Errorf("Error running `worker app apply` with app with id %s and environment with name %s: %v - %v", app.ID, envName, err, msg).Error())
		if ue != nil {
			return ue
		}
		return fmt.Errorf("error running `worker app apply` with app with id %s and environment with name %s: %v - %v", app.ID, envName, err, msg)
	}
	log.Info().Str("AppID", app.ID).Msg(*msg)
	return nil
}

func CommonStartupTasks(ctx context.Context, mm *magicmodel.Operator, username string) (*types.Account, *aws.Config, error) {

	receiptHandle := os.Getenv("RECEIPT_HANDLE")
	if receiptHandle == "" {
		return nil, nil, fmt.Errorf("error retrieving RECEIPT_HANDLE from queue. Cannot continue")
	}

	var accounts []types.Account
	o := mm.Where(&accounts, "IsMasterAccount", aws.Bool(true))
	if o.Err != nil {
		return nil, nil, fmt.Errorf("an error occurred when trying to find the MasterAccount: %s", o.Err)
	}

	cfg, err := config.LoadDefaultConfig(ctx, func(options *config.LoadOptions) error {
		config.WithRegion(accounts[0].AwsRegion)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	doApiKey, err := GetDoApiKeyFromSecretsManager(ctx, cfg, username)
	if err != nil {
		return nil, nil, err
	}

	authResponse, err := IsApiKeyValid(*doApiKey)
	if err != nil {
		return nil, nil, fmt.Errorf("error verifying validity of DragonOps Api Key: %v", err)
	}

	if !authResponse.IsValid {
		return nil, nil, fmt.Errorf("the DragonOps api key provided is not valid. Please reach out to DragonOps support for help")
	}

	return &accounts[0], &cfg, nil
}

type Resources struct {
	Data map[string][]string
}

func RunWorkerResourcesList(group types.Group, jobId string) (*string, error) {
	command := "/app/worker resources list"
	if os.Getenv("IS_LOCAL") == "true" {
		command = "./app/worker resources list"
	}
	msg, err := RunOSCommandOrFail(command)
	if err != nil {
		if msg != nil {
			return nil, fmt.Errorf("error running `worker group apply` for group with id %s: %s: %s", group.ID, err, *msg)
		} else {
			return nil, fmt.Errorf("error running `worker group apply` for group with id %s: %s", group.ID, err)
		}
	}
	return msg, nil
}

type GroupResources struct {
	Networks []types.Network
	Clusters []types.Cluster
	Database []types.Database
}

func GetAllResourcesToDeleteByGroupId(mm *magicmodel.Operator, groupID string) (*GroupResources, error) {
	resources := GroupResources{}
	o := mm.WhereV4(true, &resources.Networks, "Group.ID", groupID).WhereV4(false, &resources.Networks, "MarkedForDeletion", true)
	if o.Err != nil {
		return nil, o.Err
	}
	o = mm.WhereV4(true, &resources.Clusters, "Group.ID", groupID).WhereV4(false, &resources.Clusters, "MarkedForDeletion", true)
	if o.Err != nil {
		return nil, o.Err
	}
	o = mm.WhereV4(true, &resources.Database, "Group.ID", groupID).WhereV4(false, &resources.Database, "MarkedForDeletion", true)
	if o.Err != nil {
		return nil, o.Err
	}
	return &resources, nil
}

func GetAllClustersByGroupId(mm *magicmodel.Operator, groupID string) ([]types.Cluster, error) {
	var clusters []types.Cluster
	o := mm.WhereV4(false, &clusters, "Group.ID", groupID)
	if o.Err != nil {
		return nil, o.Err
	}
	return clusters, nil
}

func GetExactTerraformResourceNames(allResourcesToDelete *GroupResources, resources Resources) []string {
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

	for _, Database := range allResourcesToDelete.Database {
		for _, r := range resources.Data["network"] {
			replacedString := strings.Replace(r, "do_database_dot_resource_label", Database.ResourceLabel, -1)
			terraformResourcesToDelete = append(terraformResourcesToDelete, replacedString)
		}
	}
	return terraformResourcesToDelete
}
