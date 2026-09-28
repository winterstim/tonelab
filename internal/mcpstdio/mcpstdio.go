// Package mcpstdio is how a host starts Tonelab: as a process it talks to
// over stdin and stdout, which is what Claude Desktop, Claude Code, Codex
// and Cursor all launch. The command line and the standalone MCP binary
// both enter here, so the two cannot serve different things.
package mcpstdio

import (
	"context"
	"errors"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/winterstim/tonelab/internal/app"
	"github.com/winterstim/tonelab/internal/config"
	"github.com/winterstim/tonelab/internal/mcpserver"
	"github.com/winterstim/tonelab/internal/version"
)

// Serve opens the DAW the settings name, or dawName for this run, and
// answers the host until it hangs up. Stdout carries the protocol and
// nothing else; the log stays on stderr, which hosts keep as the server's.
func Serve(ctx context.Context, configPath, dawName string) error {
	settings, err := load(configPath)
	if err != nil {
		return err
	}
	if dawName != "" {
		settings.DAW.Backend = dawName
	}
	runtime, err := app.Assemble(settings, configPath, nil)
	if err != nil {
		return err
	}
	defer runtime.Close()

	server := mcpserver.New(runtime.Tools, mcpserver.Options{
		Version: version.Version,
		Status: func() mcpserver.Status {
			status, _ := runtime.Agent.GetDAWStatus()
			return mcpserver.Status{Connected: status.Connected, Detail: status.Detail}
		},
	})
	return server.Run(ctx, &mcp.StdioTransport{})
}

// load runs on the template when there is no config yet: the host brings
// the model, so the template's REAPER on its default ports is a working
// start, and a person who added Tonelab to Claude should not first be sent
// to edit a file.
func load(path string) (config.Config, error) {
	settings, err := config.Load(path)
	var first *config.FirstRunError
	if errors.As(err, &first) {
		log.Printf("[tonelab] no settings yet; wrote %s and starting with them", path)
		return config.Load(path)
	}
	return settings, err
}
