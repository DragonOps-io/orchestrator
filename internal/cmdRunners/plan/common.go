package plan

import (
	"encoding/json"
	"fmt"
	"github.com/rs/zerolog"
	"os"
	"time"
)

func init() {
	zerolog.TimeFieldFormat = time.RFC3339Nano
}

type Payload struct {
	GroupID  string `json:"group_id"`
	JobName  string `json:"job_name"`
	Region   string `json:"region"`
	UserName string `json:"user_name"`
	PlanId   string `json:"plan_id"`
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
