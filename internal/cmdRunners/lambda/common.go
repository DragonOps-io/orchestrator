package lambda

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/rs/zerolog"
)

func init() {
	zerolog.TimeFieldFormat = time.RFC3339Nano
}

type Payload struct {
	LambdaID       string   `json:"app_id"`
	EnvironmentIDs []string `json:"environment_ids"`
	JobId          string   `json:"job_id"`
	JobName        string   `json:"job_name"`
	Region         string   `json:"region"`
	UserName       string   `json:"user_name"`
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
