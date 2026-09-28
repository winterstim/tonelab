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
	"github.com/winterstim/tonelab/internal/mcpinstall"
	"github.com/winterstim/tonelab/internal/mcpstdio"
	"github.com/winterstim/tonelab/internal/version"
)

func main() {
	dawName := flag.String("daw", "", "use this DAW backend for this run instead of the configured one")
	flag.Usage = usage
	flag.Parse()

	configPath, err := config.Path()
	if err != nil {
		fail(err)
	}
	args := flag.Args()
	if len(args) == 0 {
		args = []string{"serve"}
	}
	switch args[0] {
	case "serve":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := mcpstdio.Serve(ctx, configPath, *dawName); err != nil && ctx.Err() == nil {
			fail(err)
		}
	case "install", "remove":
		executable, err := os.Executable()
		if err != nil {
			fail(err)
		}
		os.Exit(report(args[0], mcpinstall.Server{Command: executable}, args[1:]))
	case "settings":
		showSettings(configPath)
	case "set":
		if len(args) < 2 {
			usage()
			os.Exit(2)
		}
		setSetting(configPath, args[1], args[2:])
	case "login":
		login(configPath)
	case "logout":
		logout(configPath)
	case "version":
		fmt.Println(version.Version)
	default:
		usage()
		os.Exit(2)
	}
}

// report installs or removes, prints one line per host, and exits non-zero
// only when nothing was done, which is what a script can act on.
func report(action string, server mcpinstall.Server, only []string) int {
	var outcomes []mcpinstall.Outcome
	if action == "install" {
		outcomes = mcpinstall.Install(mcpinstall.System(), server, only...)
	} else {
		outcomes = mcpinstall.Remove(mcpinstall.System(), only...)
	}
	if !mcpinstall.Print(os.Stdout, outcomes) {
		return 1
	}
	return 0
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "tonelab-mcp:", err)
	os.Exit(1)
}

func usage() {
	fmt.Fprintf(os.Stderr, `tonelab-mcp: Tonelab's DAW tools for Claude, Codex, Cursor and other MCP hosts

  tonelab-mcp install [host...]   add Tonelab to the hosts on this machine
  tonelab-mcp remove [host...]    take it out again
  tonelab-mcp settings            which DAW and web search it uses
  tonelab-mcp set <name> <value>  change one: %s
  tonelab-mcp login               web search on a Tonelab plan (optional)
  tonelab-mcp logout
  tonelab-mcp                     serve over stdin and stdout; what a host runs
  tonelab-mcp version

  Hosts: %s. With none named, every one found is set up.
  --daw <name>   use another DAW backend for this run

Settings come from the same file the desktop app uses; TONELAB_CONFIG points
at another one. A host picks up a change the next time it starts the server.
`, settingNames(), joined(mcpinstall.Hosts()))
}
