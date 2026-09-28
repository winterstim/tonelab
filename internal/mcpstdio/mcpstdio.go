// Package mcpstdio is how a host starts Tonelab: as a process it talks to
// over stdin and stdout, which is what Claude Desktop, Claude Code, Codex
// and Cursor all launch. The command line and the standalone MCP binary
// both enter here, so the two cannot serve different things.
//
// The process reaches the DAW through whoever holds it: the window, the
// command line, or another host's server. When nobody does, it holds the
// DAW itself, and lets go when the window opens. The host sees one server
// throughout, whichever way its calls go.
package mcpstdio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/winterstim/tonelab/internal/app"
	"github.com/winterstim/tonelab/internal/config"
	"github.com/winterstim/tonelab/internal/mcplocal"
	"github.com/winterstim/tonelab/internal/version"
)

// releaseGrace is how long, after letting the DAW go, this process looks
// for the new holder before taking the DAW back. The one that asked binds
// the port within milliseconds; without the wait a call arriving in between
// would take the port straight back and the window would fail to open.
const releaseGrace = 10 * time.Second

// Serve answers the host until it hangs up, with the DAW the settings name,
// or dawName for this run. Stdout carries the protocol and nothing else;
// the log stays on stderr, which hosts keep as the server's log.
func Serve(ctx context.Context, configPath, dawName string) error {
	b := &bridge{configPath: configPath, dir: filepath.Dir(configPath), dawName: dawName,
		client: mcp.NewClient(&mcp.Implementation{Name: "tonelab-bridge", Version: version.Version}, nil)}
	defer b.close()

	// What the host is offered is read from the first way to the DAW found:
	// the same settings stand behind every way, so the list does not change.
	session, err := b.backend(ctx)
	if err != nil {
		return err
	}
	server, err := b.mirror(ctx, session)
	if err != nil {
		return err
	}
	return server.Run(ctx, &mcp.StdioTransport{})
}

type bridge struct {
	configPath, dir, dawName string
	client                   *mcp.Client

	mu sync.Mutex
	// session is the current way to the DAW: the holder's server, or our
	// own through memory while we hold it. Nil means find one.
	session *mcp.ClientSession
	// held is set while this process holds the DAW.
	held *held
	// released is when this process last let the DAW go.
	released time.Time
}

type held struct {
	runtime *app.Runtime
	session *mcp.ClientSession
	stop    func()
}

// backend is the way to the DAW, found again whenever the last one went.
func (b *bridge) backend(ctx context.Context) (*mcp.ClientSession, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.session != nil {
		return b.session, nil
	}
	for {
		if session, holder, err := mcplocal.Dial(ctx, b.dir, b.client); err == nil {
			log.Printf("[tonelab] reaching the DAW through process %d", holder.PID)
			b.session = session
			return session, nil
		}
		if time.Since(b.released) >= releaseGrace {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	if err := b.hold(ctx); err != nil {
		return nil, err
	}
	b.session = b.held.session
	return b.session, nil
}

// hold opens the DAW here and offers it to other processes, which is what
// lets a second host, or the window, find it. Called with mu held.
func (b *bridge) hold(ctx context.Context) error {
	settings, err := load(b.configPath)
	if err != nil {
		return err
	}
	if b.dawName != "" {
		settings.DAW.Backend = b.dawName
	}
	runtime, err := app.Assemble(settings, b.configPath, nil)
	if err != nil {
		return err
	}
	server := runtime.MCPServer()
	serverSide, clientSide := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverSide, nil); err != nil {
		runtime.Close()
		return err
	}
	session, err := b.client.Connect(ctx, clientSide, nil)
	if err != nil {
		runtime.Close()
		return err
	}
	h := &held{runtime: runtime, session: session}
	stop, err := mcplocal.Hold(server, b.dir, func() { b.release(h) })
	if err != nil {
		// Unshared is still working: this host has the DAW, and only
		// another one looking for it would miss out.
		log.Printf("[tonelab] the DAW is not offered to other processes: %v", err)
		stop = func() {}
	}
	h.stop = stop
	b.held = h
	log.Printf("[tonelab] holding the DAW")
	return nil
}

// release gives the DAW up for a process that needs it, and has done so by
// the time it returns, since that process binds the port next. The next
// call here finds the new holder.
func (b *bridge) release(h *held) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.held != h {
		return
	}
	h.session.Close()
	h.runtime.Close()
	b.held, b.session = nil, nil
	b.released = time.Now()
	// The endpoint that asked is still being answered; it stops after.
	go h.stop()
	log.Printf("[tonelab] let the DAW go to another Tonelab")
}

// drop forgets a way to the DAW that failed, unless it was replaced since.
func (b *bridge) drop(session *mcp.ClientSession) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.session == session {
		if b.held == nil || b.held.session != session {
			session.Close()
		}
		b.session = nil
	}
}

func (b *bridge) close() {
	b.mu.Lock()
	h, session := b.held, b.session
	b.held, b.session = nil, nil
	b.mu.Unlock()
	if session != nil && (h == nil || h.session != session) {
		session.Close()
	}
	if h != nil {
		h.stop()
		h.session.Close()
		h.runtime.Close()
	}
}

// mirror offers the host what the first way to the DAW offers, each call
// forwarded to whichever way is current when it is made.
func (b *bridge) mirror(ctx context.Context, session *mcp.ClientSession) (*mcp.Server, error) {
	init := session.InitializeResult()
	server := mcp.NewServer(&mcp.Implementation{Name: "tonelab", Title: "Tonelab", Version: version.Version}, &mcp.ServerOptions{
		Instructions: init.Instructions,
		Capabilities: &mcp.ServerCapabilities{},
	})
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		return nil, err
	}
	for _, tool := range tools.Tools {
		server.AddTool(tool, b.forwardTool(tool))
	}
	if init.Capabilities.Resources != nil {
		resources, err := session.ListResources(ctx, nil)
		if err != nil {
			return nil, err
		}
		for _, resource := range resources.Resources {
			server.AddResource(resource, b.forwardRead)
		}
		templates, err := session.ListResourceTemplates(ctx, nil)
		if err != nil {
			return nil, err
		}
		for _, template := range templates.ResourceTemplates {
			server.AddResourceTemplate(template, b.forwardRead)
		}
	}
	return server, nil
}

// forwardTool calls the tool wherever the DAW is now. A way that went, the
// window closed or a holder let go, is replaced and the call made once more.
// A call that may have arrived before the way broke is made again only if a
// second run changes nothing more: a second undo takes back another change.
func (b *bridge) forwardTool(tool *mcp.Tool) mcp.ToolHandler {
	repeatable := tool.Annotations == nil || tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint
	return func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		params := &mcp.CallToolParams{Name: tool.Name, Arguments: request.Params.Arguments}
		for attempt := 0; ; attempt++ {
			session, err := b.backend(ctx)
			if err != nil {
				return failure("daw_unavailable", err.Error()), nil
			}
			result, err := session.CallTool(ctx, params)
			if err == nil || answered(err) {
				return result, err
			}
			b.drop(session)
			if attempt > 0 || !(repeatable || notSent(err)) || ctx.Err() != nil {
				return failure("daw_moved", fmt.Sprintf("The connection to the DAW changed during the call, so whether %s ran is not known. Check the value before calling again.", tool.Name)), nil
			}
		}
	}
}

func (b *bridge) forwardRead(ctx context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	for attempt := 0; ; attempt++ {
		session, err := b.backend(ctx)
		if err != nil {
			return nil, err
		}
		result, err := session.ReadResource(ctx, request.Params)
		if err == nil || answered(err) || attempt > 0 || ctx.Err() != nil {
			return result, err
		}
		b.drop(session)
	}
}

// The SDK reports a broken way in JSON-RPC's own shape, under codes of its
// own beside the ones a server sends.
const (
	codeClientClosing = -32003
	codeServerClosing = -32004
	codeNotSent       = -32005
)

// answered is an error the other server sent, as opposed to one on the way
// there: the way still works, and the answer is the host's to read.
func answered(err error) bool {
	var wire *jsonrpc.Error
	if errors.Is(err, mcp.ErrConnectionClosed) || !errors.As(err, &wire) {
		return false
	}
	switch wire.Code {
	case codeClientClosing, codeServerClosing, codeNotSent:
		return false
	}
	return true
}

// notSent is a call the transport never delivered, which is safe to make
// again whatever it does: the holder it was meant for had already gone.
func notSent(err error) bool {
	var wire *jsonrpc.Error
	return errors.As(err, &wire) && wire.Code == codeNotSent
}

func failure(code, message string) *mcp.CallToolResult {
	body, _ := json.Marshal(map[string]any{"error": map[string]string{"code": code, "message": message}})
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(body)}}, IsError: true}
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
