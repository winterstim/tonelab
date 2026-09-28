package mcpserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/winterstim/tonelab/internal/agent"
	"github.com/winterstim/tonelab/internal/daw"
	"github.com/winterstim/tonelab/internal/daw/dawtest"
	"github.com/winterstim/tonelab/internal/mcpserver"
	"github.com/winterstim/tonelab/internal/search"
)

// fakeDAW answers what a real backend answers, and is held to the same
// contract, so a test here cannot pass on a DAW kinder than the real one.
type fakeDAW struct {
	daw.Client

	mu     sync.Mutex
	values map[string]any
	fx     map[string]float64
	undos  int
}

func newFakeDAW() *fakeDAW {
	return &fakeDAW{values: map[string]any{}, fx: map[string]float64{}}
}

func (f *fakeDAW) Parameters() []daw.Parameter {
	return []daw.Parameter{
		{Name: "volume", Kind: daw.Numeric, Readable: true},
		{Name: "mute", Kind: daw.Toggle, Readable: true},
	}
}

func (f *fakeDAW) SetParam(track int, name string, value any) error {
	if track < 1 {
		return daw.ErrInvalidTrack
	}
	var kind daw.Kind
	switch name {
	case "volume":
		kind = daw.Numeric
	case "mute":
		kind = daw.Toggle
	default:
		return fmt.Errorf("%w %q", daw.ErrUnknownParam, name)
	}
	switch v := value.(type) {
	case bool:
		if kind != daw.Toggle {
			return daw.ErrParamKind
		}
	case float64:
		if kind != daw.Numeric {
			return daw.ErrParamKind
		}
		if v < 0 || v > 1 {
			return daw.ErrValueOutOfRange
		}
	default:
		return daw.ErrParamKind
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.values[fmt.Sprintf("%d/%s", track, name)] = value
	return nil
}

func (f *fakeDAW) GetParam(track int, name string) (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	value, ok := f.values[fmt.Sprintf("%d/%s", track, name)]
	if !ok {
		return nil, daw.ErrValueUnknown
	}
	return value, nil
}

func (f *fakeDAW) ReadParam(track int, name string, timeout time.Duration) (any, error) {
	return f.GetParam(track, name)
}

func (f *fakeDAW) ConfirmParam(track int, name string, timeout time.Duration) (any, error) {
	return f.GetParam(track, name)
}

func (f *fakeDAW) Tracks(timeout time.Duration) ([]daw.Track, error) {
	return []daw.Track{{Number: 1, Name: "Guitar"}, {Number: 2, Name: "Vocals"}}, nil
}

func (f *fakeDAW) Undo() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.undos++
	return nil
}

func (f *fakeDAW) FXChain(track int, timeout time.Duration) ([]daw.FX, error) {
	if track != 1 {
		return nil, nil
	}
	return []daw.FX{{Number: 1, Name: "Reverb", Params: []daw.FXParam{{Number: 1, Name: "Mix"}}}}, nil
}

func (f *fakeDAW) SetFXParam(track, fx, param int, value float64) error {
	if track < 1 || fx < 1 || param < 1 {
		return daw.ErrInvalidFX
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fx[fmt.Sprintf("%d/%d/%d", track, fx, param)] = value
	return nil
}

func (f *fakeDAW) ConfirmFXParam(track, fx, param int, timeout time.Duration) (float64, error) {
	return f.ReadFXParam(track, fx, param, timeout)
}

func (f *fakeDAW) ReadFXParam(track, fx, param int, timeout time.Duration) (float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	value, ok := f.fx[fmt.Sprintf("%d/%d/%d", track, fx, param)]
	if !ok {
		return 0, daw.ErrValueUnknown
	}
	return value, nil
}

func TestFakeDAWMeetsTheClientContract(t *testing.T) {
	dawtest.AssertClientContract(t, newFakeDAW())
}

type fakeSearch struct{ hits []search.Hit }

func (f fakeSearch) Search(ctx context.Context, query string, limit int) ([]search.Hit, error) {
	return f.hits, nil
}

// connect runs the server against a real client of the protocol, so what is
// tested is what a host sees on the wire, not this package's own types.
func connect(t *testing.T, tools *agent.Tools, opts mcpserver.Options) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverSide, clientSide := mcp.NewInMemoryTransports()
	server, err := mcpserver.New(tools, opts).Connect(ctx, serverSide, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	client, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, clientSide, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

func callTool(t *testing.T, client *mcp.ClientSession, name string, args any) (agent.Result, bool) {
	t.Helper()
	result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if len(result.Content) != 1 {
		t.Fatalf("%s: expected one content block, got %d", name, len(result.Content))
	}
	var decoded agent.Result
	if err := json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &decoded); err != nil {
		t.Fatalf("%s: result is not the agent's shape: %v", name, err)
	}
	return decoded, result.IsError
}

func TestHostIsOfferedExactlyTheAgentsTools(t *testing.T) {
	tools := agent.NewTools(newFakeDAW())
	tools.EnableSearch(fakeSearch{})
	client := connect(t, tools, mcpserver.Options{})

	listed, err := client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	offered := map[string]*mcp.Tool{}
	for _, tool := range listed.Tools {
		offered[tool.Name] = tool
	}
	definitions := tools.Definitions()
	if len(offered) != len(definitions) {
		t.Fatalf("host sees %d tools, the agent has %d", len(offered), len(definitions))
	}
	for _, definition := range definitions {
		tool, ok := offered[definition.Name]
		if !ok {
			t.Fatalf("%s is not offered to the host", definition.Name)
		}
		if tool.Description != definition.Description {
			t.Errorf("%s: the host is told something else than the agent", definition.Name)
		}
		if tool.Annotations.ReadOnlyHint != (definition.Effect == agent.Reads) {
			t.Errorf("%s: read-only is %v for effect %d", definition.Name, tool.Annotations.ReadOnlyHint, definition.Effect)
		}
		if *tool.Annotations.OpenWorldHint != definition.Web {
			t.Errorf("%s: open world is %v", definition.Name, *tool.Annotations.OpenWorldHint)
		}
	}
	if !*offered["undo"].Annotations.DestructiveHint {
		t.Error("undo takes back whatever the DAW did last, which a host must ask about")
	}
}

func TestHandshakeCarriesTheAgentsRules(t *testing.T) {
	client := connect(t, agent.NewTools(newFakeDAW()), mcpserver.Options{Version: "v1.2.3"})
	init := client.InitializeResult()
	if init.Instructions != agent.Instructions {
		t.Fatal("the host's model must get the same rules as ours")
	}
	if init.ServerInfo.Version != "v1.2.3" {
		t.Fatalf("version %q", init.ServerInfo.Version)
	}
}

func TestSetParamReachesTheDAWAndReportsItsValue(t *testing.T) {
	backend := newFakeDAW()
	client := connect(t, agent.NewTools(backend), mcpserver.Options{})

	result, isError := callTool(t, client, "set_param", map[string]any{"track_id": 2, "param_name": "volume", "value": 0.25})
	if isError || result.Error != nil {
		t.Fatalf("unexpected failure: %+v", result.Error)
	}
	if got, _ := backend.GetParam(2, "volume"); got != 0.25 {
		t.Fatalf("the DAW holds %v", got)
	}
	if !strings.Contains(fmt.Sprint(result.Value), "0.25") {
		t.Fatalf("the host must be told what the DAW reports, got %v", result.Value)
	}
}

// A wrong call is a result the host's model reads and corrects, as ours
// does, not a protocol failure that ends the turn.
func TestDomainFailureIsAToolErrorWithItsCode(t *testing.T) {
	client := connect(t, agent.NewTools(newFakeDAW()), mcpserver.Options{})

	result, isError := callTool(t, client, "set_param", map[string]any{"track_id": 1, "param_name": "loudness", "value": 0.5})
	if !isError {
		t.Fatal("expected the call to be marked as an error")
	}
	if result.Error == nil || result.Error.Code == "" || !strings.Contains(result.Error.Message, "volume") {
		t.Fatalf("the refusal must carry a code and name what the DAW has: %+v", result.Error)
	}
}

func TestToolWithoutArgumentsRuns(t *testing.T) {
	backend := newFakeDAW()
	client := connect(t, agent.NewTools(backend), mcpserver.Options{})
	if _, isError := callTool(t, client, "undo", nil); isError {
		t.Fatal("undo takes no arguments and must run without any")
	}
	if backend.undos != 1 {
		t.Fatalf("undos %d", backend.undos)
	}
}

// The agent reads a page only if a search in the same turn offered it. A
// host has no turns, so a quiet minute stands for one: a read right after
// the search works, the same read after a pause does not.
func TestAQuietMinuteEndsTheTurnForPages(t *testing.T) {
	const address = "https://example.com/amp"
	now := time.Unix(1_000_000, 0)
	tools := agent.NewTools(newFakeDAW())
	tools.EnableSearch(fakeSearch{hits: []search.Hit{{Title: "Amp", URL: address}}})
	client := connect(t, tools, mcpserver.Options{Now: func() time.Time { return now }})

	callTool(t, client, "search", map[string]any{"query": "amp settings"})
	now = now.Add(2 * time.Minute)
	result, _ := callTool(t, client, "fetch_page", map[string]any{"url": address})
	if result.Error == nil || result.Error.Code != "url_not_from_search" {
		t.Fatalf("a page offered in an earlier turn must not be readable, got %+v", result.Error)
	}
}

func TestStatusIsOfferedWhenTheHostCanAsk(t *testing.T) {
	status := func() mcpserver.Status { return mcpserver.Status{Detail: "The DAW did not answer."} }
	client := connect(t, agent.NewTools(newFakeDAW()), mcpserver.Options{Status: status})

	result, isError := callTool(t, client, "daw_status", nil)
	if isError || !strings.Contains(fmt.Sprint(result.Value), "did not answer") {
		t.Fatalf("got %+v", result)
	}
	read, err := client.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "tonelab://status"})
	if err != nil || !strings.Contains(read.Contents[0].Text, "did not answer") {
		t.Fatalf("status resource: %v %+v", err, read)
	}
}

func TestResourcesAnswerAsTheToolsDo(t *testing.T) {
	client := connect(t, agent.NewTools(newFakeDAW()), mcpserver.Options{})
	ctx := context.Background()

	tracks, err := client.ReadResource(ctx, &mcp.ReadResourceParams{URI: "tonelab://tracks"})
	if err != nil || !strings.Contains(tracks.Contents[0].Text, "Vocals") {
		t.Fatalf("tracks: %v %+v", err, tracks)
	}
	fx, err := client.ReadResource(ctx, &mcp.ReadResourceParams{URI: "tonelab://tracks/1/fx"})
	if err != nil || !strings.Contains(fx.Contents[0].Text, "Reverb") {
		t.Fatalf("fx: %v %+v", err, fx)
	}
	for _, uri := range []string{"tonelab://tracks/0/fx", "tonelab://tracks/x/fx"} {
		if _, err := client.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri}); err == nil {
			t.Errorf("%s must not be found", uri)
		}
	}
}
