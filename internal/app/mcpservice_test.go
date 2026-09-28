package app_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/winterstim/tonelab/internal/app"
	"github.com/winterstim/tonelab/internal/mcpinstall"
)

// The window points hosts at its own binary with "mcp", which is how the
// app serves them without a second download.
func TestTheWindowSetsHostsToStartTheApp(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o700); err != nil {
		t.Fatal(err)
	}
	env := mcpinstall.Env{Home: home, GOOS: "linux",
		LookPath: func(string) (string, error) { return "", errors.New("not found") },
		Run:      func(string, ...string) error { return nil }}
	service := app.NewMCPService("/Applications/Tonelab.app/Contents/MacOS/tonelab", env)

	setups, err := service.Install()
	if err != nil {
		t.Fatal(err)
	}
	var cursor app.HostSetup
	for _, s := range setups {
		if s.Host == "Cursor" {
			cursor = s
		}
	}
	if !cursor.Done {
		t.Fatalf("Cursor was found but not set up: %+v", setups)
	}
	body, _ := os.ReadFile(filepath.Join(home, ".cursor", "mcp.json"))
	if !strings.Contains(string(body), `"/Applications/Tonelab.app/Contents/MacOS/tonelab"`) || !strings.Contains(string(body), `"mcp"`) {
		t.Fatalf("entry %s", body)
	}
}
