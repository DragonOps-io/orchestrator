package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"io"
	"net/http"
	"os"
	"os/exec"
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
	err = json.Unmarshal(body, &response)
	if err != nil {
		return nil, err
	}
	return &response, nil
}

func GetDoApiKeyFromSecretsManager(ctx context.Context, cfg aws.Config, userName string) (*string, error) {
	smClient := secretsmanager.NewFromConfig(cfg)
	resp, err := smClient.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(fmt.Sprintf("do-api-key/%s", userName)),
	})
	if err != nil {
		return nil, err
	}
	return resp.SecretString, err
}
