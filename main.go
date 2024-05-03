package main

import (
	"context"
	"github.com/DragonOps-io/orchestrator/cmd"
	"github.com/rs/zerolog/log"
	"os"
)

func main() {
	ctx := context.Background()
	if err := cmd.NewRootCommand().ExecuteContext(ctx); err != nil {
		log.Err(err)
		os.Exit(1)
	}
}
