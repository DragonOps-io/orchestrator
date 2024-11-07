package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/DragonOps-io/orchestrator/cmd"
	"github.com/DragonOps-io/orchestrator/internal/utils"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/bugsnag/bugsnag-go/v2"
)

func main() {
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion("us-east-1"))
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	username, err := GetPayloadUsername()
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	apiKey, err := utils.GetDoApiKeyFromSecretsManager(ctx, cfg, *username)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	devKey := os.Getenv("BUGSNAG_DEV_KEY")
	if devKey == "" {
		fmt.Println("BUGSNAG_DEV_KEY not set")
		os.Exit(1)
	}

	repo := "bugsnagOrchestratorKey"

	bugsnagApiKey, err := utils.RetrieveBugsnagApiKey(devKey, repo, *apiKey)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	bugsnag.Configure(bugsnag.Configuration{
		APIKey:          *bugsnagApiKey,
		ReleaseStage:    os.Getenv("DRAGONOPS_ENVIRONMENT"),
		ProjectPackages: []string{"main", "github.com/DragonOps-io/orchestrator"},
	})

	if err = cmd.NewRootCommand().ExecuteContext(ctx); err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}
}

func GetPayloadUsername() (*string, error) {
	val, ok := os.LookupEnv("MESSAGE")
	if !ok {
		fmt.Printf("%s not set\n", "MESSAGE")
		return nil, fmt.Errorf("%s not set\n", "MESSAGE")
	}

	payload := map[string]string{}
	err := json.Unmarshal([]byte(val), &payload)
	if err != nil {
		return nil, err
	}

	username := payload["user_name"]

	return &username, nil
}
