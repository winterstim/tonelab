package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/winterstim/tonelab/internal/mcpstdio"
)

// serveMCP is the same server tonelab-mcp runs, for someone who already
// has the command line and wants no second binary.
func serveMCP(configPath, dawName string) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := mcpstdio.Serve(ctx, configPath, dawName); err != nil && ctx.Err() == nil {
		fail(err)
	}
}
