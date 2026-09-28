package mcpstdio

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/winterstim/tonelab/internal/agent"
	"github.com/winterstim/tonelab/internal/config"
	"github.com/winterstim/tonelab/internal/daw/dawtest"
	"github.com/winterstim/tonelab/internal/mcplocal"
	"github.com/winterstim/tonelab/internal/mcpserver"
	"github.com/winterstim/tonelab/internal/version"
)

// holdFake stands in for the window: a holder that keeps a fake DAW.
func holdFake(t *testing.T, dir string) (*dawtest.Fake, func()) {
	t.Helper()
	fake := dawtest.NewFake()
	stop, err := mcplocal.Hold(mcpserver.New(agent.NewTools(fake), mcpserver.Options{}), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	return fake, stop
}

func newBridge(dir string) *bridge {
	return &bridge{configPath: filepath.Join(dir, "config.json"), dir: dir,
		client: mcp.NewClient(&mcp.Implementation{Name: "tonelab-bridge", Version: version.Version}, nil)}
}

// host connects a client to what the bridge offers, as Claude would over
// stdio, with memory in its place.
func host(t *testing.T, b *bridge) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	session, err := b.backend(ctx)
	if err != nil {
		t.Fatal(err)
	}
	server, err := b.mirror(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	serverSide, clientSide := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverSide, nil); err != nil {
		t.Fatal(err)
	}
	client, err := mcp.NewClient(&mcp.Implementation{Name: "host", Version: "1"}, nil).Connect(ctx, clientSide, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close(); b.close() })
	return client
}

func call(t *testing.T, client *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return result
}

func text(result *mcp.CallToolResult) string {
	return result.Content[0].(*mcp.TextContent).Text
}

func TestTheHostIsServedThroughTheWindow(t *testing.T) {
	dir := t.TempDir()
	fake, stop := holdFake(t, dir)
	defer stop()
	client := host(t, newBridge(dir))

	if init := client.InitializeResult(); init.Instructions != agent.Instructions {
		t.Fatal("the host must get the agent's rules through the bridge too")
	}
	result := call(t, client, "set_param", map[string]any{"track_id": 1, "param_name": "volume", "value": 0.5})
	if result.IsError {
		t.Fatalf("set_param: %s", text(result))
	}
	if got, _ := fake.GetParam(1, "volume"); got != 0.5 {
		t.Fatalf("the window's DAW holds %v", got)
	}
}

// The window closes and opens again while the host keeps its session: the
// next call finds the new holder, and the host never sees the change.
func TestAChangedHolderIsFoundAgain(t *testing.T) {
	dir := t.TempDir()
	_, stop := holdFake(t, dir)
	client := host(t, newBridge(dir))
	call(t, client, "list_tracks", nil)

	stop()
	second, stopSecond := holdFake(t, dir)
	defer stopSecond()
	result := call(t, client, "set_param", map[string]any{"track_id": 2, "param_name": "mute", "value": true})
	if result.IsError {
		t.Fatalf("after the holder changed: %s", text(result))
	}
	if got, _ := second.GetParam(2, "mute"); got != true {
		t.Fatalf("the new holder's DAW holds %v", got)
	}
}

// Undo is not repeatable, but a call the old holder never received is safe
// to send to the new one: it runs exactly once.
func TestUndoNeverSentIsSentToTheNewHolderOnce(t *testing.T) {
	dir := t.TempDir()
	_, stop := holdFake(t, dir)
	client := host(t, newBridge(dir))
	call(t, client, "list_tracks", nil)

	stop()
	second, stopSecond := holdFake(t, dir)
	defer stopSecond()
	if result := call(t, client, "undo", nil); result.IsError {
		t.Fatalf("undo after the holder changed: %s", text(result))
	}
	if second.Undos() != 1 {
		t.Fatalf("undo ran %d times on the new holder", second.Undos())
	}
}

// A call that reached the holder before the way broke may have run; undo
// is then reported as unknown rather than run a second time.
func TestAnUndoThatMayHaveRunIsNotRepeated(t *testing.T) {
	for _, err := range []error{errors.New("unexpected EOF"), mcp.ErrConnectionClosed, &jsonrpc.Error{Code: codeServerClosing}} {
		if answered(err) || notSent(err) {
			t.Errorf("%v: the call may have run, so undo must not be repeated", err)
		}
	}
	if !notSent(fmt.Errorf("sending: %w", &jsonrpc.Error{Code: codeNotSent})) {
		t.Error("a rejected call was never sent")
	}
	if !answered(&jsonrpc.Error{Code: -32602, Message: "invalid params"}) {
		t.Error("an error the server sent is its answer, not a broken way")
	}
}

func freeUDPPort(t *testing.T) int {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).Port
}

// With nobody holding the DAW the bridge holds it, and lets it go for the
// window: the feedback port is free when Release returns, and the next call
// goes to the window rather than taking the port back.
func TestTheBridgeLetsTheDAWGoToTheWindow(t *testing.T) {
	dir := t.TempDir()
	feedback := freeUDPPort(t)
	settings := config.Config{
		LLM: config.LocalLLM(),
		DAW: config.DAW{Backend: "reaper", Host: "127.0.0.1", Port: freeUDPPort(t), FeedbackPort: feedback},
	}
	if err := config.Save(filepath.Join(dir, "config.json"), settings); err != nil {
		t.Fatal(err)
	}
	b := newBridge(dir)
	client := host(t, b)
	if b.held == nil {
		t.Fatal("with no holder the bridge must hold the DAW itself")
	}
	holder, err := mcplocal.Find(dir)
	if err != nil || holder.Keeps {
		t.Fatalf("a host's server must offer the DAW and let it go: %+v %v", holder, err)
	}

	if err := mcplocal.Release(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	port, err := net.ListenPacket("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(feedback)))
	if err != nil {
		t.Fatalf("the feedback port must be free once Release returns: %v", err)
	}
	port.Close()

	window, stop := holdFake(t, dir)
	defer stop()
	started := time.Now()
	result := call(t, client, "set_param", map[string]any{"track_id": 1, "param_name": "volume", "value": 0.75})
	if result.IsError {
		t.Fatalf("after letting go: %s", text(result))
	}
	if got, _ := window.GetParam(1, "volume"); got != 0.75 {
		t.Fatalf("the call must reach the window, which holds %v", got)
	}
	if time.Since(started) > releaseGrace {
		t.Fatal("the call waited out the grace instead of finding the window")
	}
	if b.held != nil {
		t.Fatal("the bridge took the DAW back from the window")
	}
}
