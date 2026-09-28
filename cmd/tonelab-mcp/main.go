// Command tonelab-mcp is Tonelab for a host that brings its own model:
// Claude Desktop, Claude Code, Codex, Cursor. It is the agent's tools and
// the DAW connection and nothing else, with no window and no model of its
// own, so it needs neither a subscription nor a key.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/winterstim/tonelab/internal/config"
	"github.com/winterstim/tonelab/internal/mcpstdio"
	"github.com/winterstim/tonelab/internal/version"
)

func main() {
	dawName := flag.String("daw", "", "use this DAW backend for this run instead of the configured one")
	flag.Usage = usage
	flag.Parse()

	switch flag.Arg(0) {
	case "", "serve":
	case "version":
		fmt.Println(version.Version)
		return
	default:
		usage()
		os.Exit(2)
	}

	configPath, err := config.Path()
	if err != nil {
		fail(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := mcpstdio.Serve(ctx, configPath, *dawName); err != nil && ctx.Err() == nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "tonelab-mcp:", err)
	os.Exit(1)
}

func usage() {
	fmt.Fprintln(os.Stderr, `tonelab-mcp: Tonelab's DAW tools for Claude, Codex, Cursor and other MCP hosts

  tonelab-mcp            serve over stdin and stdout; this is what a host runs
  tonelab-mcp version    print the build

  --daw <name>   use another DAW backend for this run

Settings come from the same config file the desktop app uses; TONELAB_CONFIG
points at another one.`)
}
