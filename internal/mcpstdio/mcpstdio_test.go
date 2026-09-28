package mcpstdio

import (
	"os"
	"path/filepath"
	"testing"
)

// Someone who added Tonelab to Claude and never opened the window has no
// settings file. They must get a working server on the template, and the
// file for later, rather than an instruction to edit it first.
func TestFirstRunStartsOnTheTemplate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tonelab", "config.json")

	settings, err := load(path)
	if err != nil {
		t.Fatalf("first run must start: %v", err)
	}
	if settings.DAW.Backend == "" || settings.DeviceID == "" {
		t.Fatalf("started without a DAW or a device id: %+v", settings.DAW)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the settings must be left for later: %v", err)
	}
}
