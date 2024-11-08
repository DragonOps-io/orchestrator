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
	"github.com/rs/zerolog/log"
)

func main() {
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion("us-east-1"))
	if err != nil {
		fmt.Println("%s", err)
		os.Exit(1)
	}

	username, err := GetPayloadUsername()
	if err != nil {
		fmt.Println("%s", err)
		os.Exit(1)
	}

	apiKey, err := utils.GetDoApiKeyFromSecretsManager(ctx, cfg, *username)
	if err != nil {
		fmt.Println("%s", err)
		os.Exit(1)
	}

	devKey := "8707cf2b-d77e-4e3e-b227-790024844919"
	repo := "bugsnagOrchestratorKey"

	bugsnagApiKey, err := utils.RetrieveBugsnagApiKey(devKey, repo, *apiKey)
	if err != nil {
		fmt.Println("%s", err)
		os.Exit(1)
	}

	bugsnag.Configure(bugsnag.Configuration{
		APIKey:          *bugsnagApiKey,
		ReleaseStage:    os.Getenv("DRAGONOPS_ENVIRONMENT"),
		ProjectPackages: []string{"main", "github.com/DragonOps-io/orchestrator"},
	})

	if err := cmd.NewRootCommand().ExecuteContext(ctx); err != nil {
		log.Err(err)
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

	username, ok := payload["user_name"]
	if !ok {
		return nil, fmt.Errorf("user_name not found in payload\n")
	}

	return &username, nil
}
