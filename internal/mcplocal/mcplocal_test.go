package mcplocal_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/winterstim/tonelab/internal/agent"
	"github.com/winterstim/tonelab/internal/daw/dawtest"
	"github.com/winterstim/tonelab/internal/mcplocal"
	"github.com/winterstim/tonelab/internal/mcpserver"
)

func newServer() *mcp.Server {
	return mcpserver.New(agent.NewTools(dawtest.NewFake()), mcpserver.Options{})
}

func newClient() *mcp.Client {
	return mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
}

func TestAnotherProcessReachesTheDAWThroughTheHolder(t *testing.T) {
	dir := t.TempDir()
	stop, err := mcplocal.Hold(newServer(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	session, holder, err := mcplocal.Dial(context.Background(), dir, newClient())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if holder.PID != os.Getpid() || !holder.Keeps {
		t.Fatalf("holder %+v", holder)
	}
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_tracks", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("list_tracks through the holder: %v %+v", err, result)
	}
}

// Any local program can reach a port on 127.0.0.1, and a web page can try.
// Only one that can read our settings directory has the token.
func TestTheEndpointRefusesAnyoneWithoutTheToken(t *testing.T) {
	dir := t.TempDir()
	stop, err := mcplocal.Hold(newServer(), dir, func() { t.Error("released without the token") })
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	holder, err := mcplocal.Find(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/mcp", "/release"} {
		request, _ := http.NewRequest(http.MethodPost, holder.URL+path, strings.NewReader(`{}`))
		request.Header.Set("Authorization", "Bearer wrong")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s with a wrong token: %s", path, response.Status)
		}
	}
}

func TestTheTokenFileIsPrivate(t *testing.T) {
	dir := t.TempDir()
	stop, err := mcplocal.Hold(newServer(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	info, err := os.Stat(filepath.Join(dir, "daw-holder.json"))
	if err != nil {
		t.Fatal(err)
	}
	if os.PathSeparator == '/' && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("the file holding the token is readable by others: %v", info.Mode())
	}
}

// Opening the window while a host's server holds the DAW: the server lets
// go, and has by the time Release returns, since the window binds next.
func TestAServerLetsGoWhenAsked(t *testing.T) {
	dir := t.TempDir()
	var released atomic.Bool
	stop, err := mcplocal.Hold(newServer(), dir, func() { released.Store(true) })
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	if err := mcplocal.Release(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if !released.Load() {
		t.Fatal("Release returned before the holder let go")
	}
	if _, err := mcplocal.Find(dir); !errors.Is(err, mcplocal.ErrNoHolder) {
		t.Fatalf("the file must be gone so nobody is sent to the old holder: %v", err)
	}
}

func TestAPersonsSessionKeepsTheDAW(t *testing.T) {
	dir := t.TempDir()
	stop, err := mcplocal.Hold(newServer(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if err := mcplocal.Release(context.Background(), dir); err == nil {
		t.Fatal("the window must not be asked to let go")
	}
	if _, err := mcplocal.Find(dir); err != nil {
		t.Fatalf("the window's file must stay: %v", err)
	}
}

// A holder that crashed leaves its file. Finding it must not strand a host:
// the file is dropped, so the caller takes the DAW itself.
func TestAFileFromAProcessThatDiedIsCleared(t *testing.T) {
	dir := t.TempDir()
	body, _ := json.Marshal(mcplocal.Holder{URL: "http://127.0.0.1:1", Token: "t", PID: 1})
	if err := os.WriteFile(filepath.Join(dir, "daw-holder.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mcplocal.Dial(context.Background(), dir, newClient()); !errors.Is(err, mcplocal.ErrNoHolder) {
		t.Fatalf("dial: %v", err)
	}
	if _, err := mcplocal.Find(dir); !errors.Is(err, mcplocal.ErrNoHolder) {
		t.Fatalf("the dead holder's file must be gone: %v", err)
	}
	if err := mcplocal.Release(context.Background(), dir); err != nil {
		t.Fatalf("nothing to release is not an error: %v", err)
	}
}

func TestStoppingLeavesNoFile(t *testing.T) {
	dir := t.TempDir()
	stop, err := mcplocal.Hold(newServer(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	stop()
	if _, err := mcplocal.Find(dir); !errors.Is(err, mcplocal.ErrNoHolder) {
		t.Fatalf("a stopped holder must not be found: %v", err)
	}
}
