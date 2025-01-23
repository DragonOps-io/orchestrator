package plan

import (
	"fmt"
	"os"
	"time"

	"github.com/DragonOps-io/types"
	"github.com/rs/zerolog"
)

func init() {
	zerolog.TimeFieldFormat = time.RFC3339Nano
}

type Payload struct {
	GroupID        string   `json:"group_id"`
	JobId          string   `json:"job_id"`
	JobName        string   `json:"job_name"`
	Region         string   `json:"region"`
	UserName       string   `json:"user_name"`
	PlanId         string   `json:"plan_id"`
	AppID          string   `json:"app_id"`
	EnvironmentIDs []string `json:"environment_ids"`
}

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
