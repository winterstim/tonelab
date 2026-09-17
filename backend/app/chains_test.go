package app

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"tonelab/backend/daw"
)

func TestChainStoreRoundTripsPerDAW(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chains.json")
	store := NewChainStore(path, "reaper")

	if _, err := store.Load(); err == nil {
		t.Fatal("a missing file is an error, not an empty chain set")
	}
	chains := map[int][]daw.FX{2: {{Number: 1, Name: "Amp", Params: []daw.FXParam{{Number: 1, Name: "Gain", Kind: "discrete"}}}}}
	if err := store.Save(chains); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
			t.Fatalf("chains are project contents, want 0600, got %o", info.Mode().Perm())
		}
	}

	loaded, err := store.Load()
	if err != nil || len(loaded[2]) != 1 || loaded[2][0].Params[0].Kind != "discrete" {
		t.Fatalf("round trip lost something: %+v, %v", loaded, err)
	}

	if _, err := NewChainStore(path, "ableton").Load(); err == nil {
		t.Fatal("another DAW must not inherit these chains")
	}
}
