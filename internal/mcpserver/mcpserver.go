// Package mcpserver offers the agent's tools over the Model Context
// Protocol, to a host whose own model calls them: Claude Desktop, Claude
// Code, Codex, Cursor. The tools are the agent's own, not a second set, so
// Tonelab's chat and a host can never be offered different ones. No model
// and no key of ours is involved: the host brings the model.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/winterstim/tonelab/internal/agent"
)

// turnGap is how long a host must be quiet for its next call to start a new
// turn. The agent reads a page only if a search in the same turn returned
// it; a host has no turns of its own, but its model searches and reads in
// one burst, and a person thinking between requests is well past a minute.
const turnGap = time.Minute

// Status is whether the DAW is answering, and why not when it is not.
type Status struct {
	Connected bool   `json:"connected"`
	Detail    string `json:"detail"`
}

// Options are what the host process knows and this package does not.
type Options struct {
	// Version is the build's, reported to the host in the handshake.
	Version string
	// Status asks the DAW whether it is there. Nil leaves daw_status out.
	Status func() Status
	// Now stands in for the clock in tests.
	Now func() time.Time
}

type server struct {
	tools  *agent.Tools
	status func() Status
	now    func() time.Time

	// mu makes calls one at a time, as the agent's own loop makes them: a
	// host may send several at once, and two sets racing on one fader
	// leave whichever the DAW happened to take last.
	mu   sync.Mutex
	last time.Time
}

// New builds the server over one set of tools. Tools are listed once, from
// what the DAW offered at the time, which is when the host asks.
func New(tools *agent.Tools, opts Options) *mcp.Server {
	s := &server{tools: tools, status: opts.Status, now: opts.Now}
	if s.now == nil {
		s.now = time.Now
	}
	version := opts.Version
	if version == "" {
		version = "dev"
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "tonelab", Title: "Tonelab", Version: version}, &mcp.ServerOptions{
		Instructions: agent.Instructions,
		// Without this the SDK announces logging, which nothing here sends.
		Capabilities: &mcp.ServerCapabilities{},
	})

	offered := map[string]bool{}
	for _, definition := range tools.Definitions() {
		offered[definition.Name] = true
		srv.AddTool(toolOf(definition), s.call(definition.Name))
	}
	if s.status != nil {
		srv.AddTool(&mcp.Tool{
			Name:        "daw_status",
			Description: "Whether the DAW is answering, and what to check when it is not.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(false)},
		}, s.callStatus)
		srv.AddResource(&mcp.Resource{
			URI: "tonelab://status", Name: "status", MIMEType: "application/json",
			Description: "Whether the DAW is answering.",
		}, s.readStatus)
	}
	if offered["list_tracks"] {
		srv.AddResource(&mcp.Resource{
			URI: "tonelab://tracks", Name: "tracks", MIMEType: "application/json",
			Description: "The project's tracks by number and name.",
		}, s.readTool("list_tracks", func(string) json.RawMessage { return json.RawMessage(`{}`) }))
	}
	if offered["list_fx"] {
		srv.AddResourceTemplate(&mcp.ResourceTemplate{
			URITemplate: "tonelab://tracks/{track}/fx", Name: "fx", MIMEType: "application/json",
			Description: "The effects on one track, by number and name.",
		}, s.readTool("list_fx", trackArguments))
	}
	return srv
}

// toolOf states the agent's own account of a tool in the protocol's terms.
// A set is not marked destructive: it replaces a value the DAW's own undo
// brings back, where undo itself takes back whatever the DAW did last.
func toolOf(definition agent.Tool) *mcp.Tool {
	annotations := &mcp.ToolAnnotations{OpenWorldHint: ptr(definition.Web)}
	switch definition.Effect {
	case agent.Reads:
		annotations.ReadOnlyHint = true
	case agent.Changes:
		annotations.DestructiveHint = ptr(false)
		annotations.IdempotentHint = true
	case agent.Reverts:
		annotations.DestructiveHint = ptr(true)
	}
	return &mcp.Tool{
		Name:        definition.Name,
		Description: definition.Description,
		InputSchema: definition.InputSchema,
		Annotations: annotations,
	}
}

// call runs one tool as the agent's loop would, and hands back exactly what
// the agent's own model is shown, so the two cannot be told different things.
// A domain failure is a result the model reads and retries from, not a
// protocol error.
func (s *server) call(name string) mcp.ToolHandler {
	return func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result := s.run(name, request.Params.Arguments)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: encode(result)}},
			IsError: result.Error != nil,
		}, nil
	}
}

func (s *server) run(name string, args json.RawMessage) agent.Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.last.IsZero() || now.Sub(s.last) >= turnGap {
		s.tools.BeginTurn()
	}
	s.last = now
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	return s.tools.Call(name, args)
}

func (s *server) callStatus(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	body, _ := json.Marshal(agent.Result{Value: s.status()})
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(body)}}}, nil
}

func (s *server) readStatus(ctx context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	body, _ := json.Marshal(s.status())
	return contents(request.Params.URI, string(body)), nil
}

// readTool serves a resource from the tool that answers the same question,
// so reading it and calling the tool cannot disagree.
func (s *server) readTool(name string, arguments func(uri string) json.RawMessage) mcp.ResourceHandler {
	return func(ctx context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		uri := request.Params.URI
		args := arguments(uri)
		if args == nil {
			return nil, mcp.ResourceNotFoundError(uri)
		}
		result := s.run(name, args)
		if result.Error != nil {
			return nil, fmt.Errorf("%s: %s", result.Error.Code, result.Error.Message)
		}
		body, err := json.Marshal(result.Value)
		if err != nil {
			return nil, err
		}
		return contents(uri, string(body)), nil
	}
}

// trackArguments reads the track out of tonelab://tracks/{track}/fx; nil
// for anything else, which is then not found rather than guessed at.
func trackArguments(uri string) json.RawMessage {
	rest, ok := strings.CutPrefix(uri, "tonelab://tracks/")
	if !ok {
		return nil
	}
	number, ok := strings.CutSuffix(rest, "/fx")
	if !ok {
		return nil
	}
	n, err := strconv.Atoi(number)
	if err != nil || n < 1 {
		return nil
	}
	return json.RawMessage(fmt.Sprintf(`{"track_id": %d}`, n))
}

func contents(uri, text string) *mcp.ReadResourceResult {
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "application/json", Text: text}}}
}

func encode(result agent.Result) string {
	body, err := json.Marshal(result)
	if err != nil {
		return `{"error":{"code":"internal","message":"the tool result could not be encoded"}}`
	}
	return string(body)
}

func ptr[T any](v T) *T { return &v }
