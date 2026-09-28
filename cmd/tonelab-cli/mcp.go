package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/winterstim/tonelab/internal/mcpinstall"
	"github.com/winterstim/tonelab/internal/mcpstdio"
)

// runMCP is the same server tonelab-mcp runs, for someone who already has
// the command line and wants no second binary: "mcp" serves, and
// "mcp install" or "mcp remove" set up the hosts to launch it.
func runMCP(configPath, dawName string, args []string) {
	if len(args) > 0 && (args[0] == "install" || args[0] == "remove") {
		executable, err := os.Executable()
		if err != nil {
			fail(err)
		}
		var outcomes []mcpinstall.Outcome
		if args[0] == "install" {
			outcomes = mcpinstall.Install(mcpinstall.System(), mcpinstall.Server{Command: executable, Args: []string{"mcp"}}, args[1:]...)
		} else {
			outcomes = mcpinstall.Remove(mcpinstall.System(), args[1:]...)
		}
		if !mcpinstall.Print(os.Stdout, outcomes) {
			os.Exit(1)
		}
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := mcpstdio.Serve(ctx, configPath, dawName); err != nil && ctx.Err() == nil {
		fail(err)
	}
}
