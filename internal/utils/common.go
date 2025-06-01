package utils

import (
	"context"
	"fmt"
	magicmodel "github.com/Ilios-LLC/magicmodel-go/model"
	"github.com/rs/zerolog/log"
	"io"
	"net/http"
	"os"
	"os/exec"

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

func UpdateEnvironmentStatusesToApplyFailed(app types.App, environmentsToApply []string, mm *magicmodel.Operator, err error) error {
	for _, envName := range environmentsToApply {
		if env, ok := app.Environments[envName]; ok && env.Status == "APPLYING" {
			env.Status = "APPLY_FAILED"
			env.FailedReason = err.Error()
			app.Environments[envName] = env
		}
	}

	aco := mm.Update(&app, "Environments", app.Environments)
	if aco.Err != nil {
		return aco.Err
	}
	return nil
}

func UpdateSingleEnvironmentStatusToApplyFailed(app types.App, envName string, mm *magicmodel.Operator, err error) error {
	env, exists := app.Environments[envName]
	if exists && env.Status == "APPLYING" {
		env.Status = "APPLY_FAILED"
		env.FailedReason = err.Error()
		app.Environments[envName] = env
	}

	aco := mm.Update(&app, "Environments", app.Environments)
	if aco.Err != nil {
		return aco.Err
	}
	return nil
}

func UpdateSingleEnvironmentStatusToDestroyed(app types.App, envName string, mm *magicmodel.Operator) error {
	appEnv, exists := app.Environments[envName]
	if exists && appEnv.Status == "DESTROYING" {
		appEnv.Status = "DESTROYED"
		appEnv.FailedReason = ""
		app.Environments[envName] = appEnv
	}

	aco := mm.Update(&app, "Environments", app.Environments)
	if aco.Err != nil {
		return aco.Err
	}
	return nil
}

func UpdateSingleEnvironmentStatusToDestroyFailed(app types.App, envName string, mm *magicmodel.Operator, err error) error {
	env, exists := app.Environments[envName]
	if exists && env.Status == "DESTROYING" {
		env.Status = "DESTROY_FAILED"
		env.FailedReason = err.Error()
		app.Environments[envName] = env
	}

	aco := mm.Update(&app, "Environments", app.Environments)
	if aco.Err != nil {
		return aco.Err
	}
	return nil
}

func UpdateSingleEnvironmentStatusToApplied(app types.App, envName string, mm *magicmodel.Operator) error {
	appEnv, exists := app.Environments[envName]
	if exists && appEnv.Status == "APPLYING" {
		appEnv.Status = "APPLIED"
		appEnv.FailedReason = ""
		app.Environments[envName] = appEnv
	}

	aco := mm.Update(&app, "Environments", app.Environments)
	if aco.Err != nil {
		return aco.Err
	}
	return nil
}

func UpdateEnvironmentStatusesToDestroyFailed(app types.App, environmentsToApply []string, mm *magicmodel.Operator, errMsg string) error {
	for _, envName := range environmentsToApply {
		if env, ok := app.Environments[envName]; ok && env.Status == "DESTROYING" {
			env.Status = "DESTROY_FAILED"
			env.FailedReason = errMsg
			app.Environments[envName] = env
		}
	}

	aco := mm.Update(&app, "Environments", app.Environments)
	if aco.Err != nil {
		return aco.Err
	}
	return nil
}

func UpdateEnvironmentStatusesToDestroyed(app types.App, environmentsToApply []string, mm *magicmodel.Operator) error {
	for _, envName := range environmentsToApply {
		if env, ok := app.Environments[envName]; ok && env.Status == "DESTROYING" {
			env.Status = "DESTROYED"
			env.FailedReason = ""
			app.Environments[envName] = env
		}
	}

	aco := mm.Update(&app, "Environments", app.Environments)
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

	log.Debug().Str("AppID", app.ID).Msg(fmt.Sprintf("Templating terraform application files for environment %s", envName))
	msg, err := RunOSCommandOrFail(command)
	if err != nil {
		ue := UpdateSingleEnvironmentStatusToApplyFailed(app, envName, mm, fmt.Errorf("Error running `worker app apply` with app with id %s and environment with name %s: %v - %v", app.ID, envName, err, msg))
		if ue != nil {
			return ue
		}
		return fmt.Errorf("error running `worker app apply` with app with id %s and environment with name %s: %v - %v", app.ID, envName, err, msg)
	}
	log.Debug().Str("AppID", app.ID).Msg(*msg)
	return nil
}
