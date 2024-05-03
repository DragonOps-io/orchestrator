package main

import (
	"context"
	"github.com/DragonOps-io/orchestrator/cmd"
	"github.com/rs/zerolog/log"
	"os"
	"sync"
)

var WG sync.WaitGroup

func main() {
	ctx := context.Background()
	if err := cmd.NewRootCommand(&WG).ExecuteContext(ctx); err != nil {
		log.Err(err)
		os.Exit(1)
	}
	WG.Wait()
}
