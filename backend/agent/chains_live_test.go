//go:build llm && (reaper || ableton || flstudio)

package agent_test

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"tonelab/backend/agent"
	"tonelab/backend/app"
)

// Two runs against the real DAW: the first walks and stores, the second
// answers from the store before its own walk finishes, then writes through
// a chain the DAW confirmed. No model, so the timings are the cache's own.
func TestStoredChainSpeedsUpTheSecondRun(t *testing.T) {
	client := liveDAW(t)
	path := filepath.Join(t.TempDir(), "chains.json")

	first := agent.NewTools(client)
	first.ShareChains(agent.NewChainCache(app.NewChainStore(path, "live")))
	started := time.Now()
	result := call(t, first, "list_fx", `{"track_id": 1}`)
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	walk := time.Since(started)
	list := result.Value.(agent.FXList)
	if len(list.FX) == 0 {
		t.Skip("track 1 of the open project has no effects")
	}
	t.Logf("first run: %d effects walked in %s", len(list.FX), walk)

	second := agent.NewTools(client)
	second.ShareChains(agent.NewChainCache(app.NewChainStore(path, "live")))
	started = time.Now()
	result = call(t, second, "list_fx", `{"track_id": 1}`)
	stored := time.Since(started)
	list = result.Value.(agent.FXList)
	if list.Note == "" || len(list.FX) == 0 {
		t.Fatalf("second run should answer from the store with a note, got %+v", list)
	}
	if stored > walk/5 {
		t.Fatalf("the store should be far faster than a walk: %s against %s", stored, walk)
	}
	t.Logf("second run: answered from the store in %s", stored)

	// A read by index waits for the DAW's own chain, so it cannot land on
	// a parameter that moved since last session.
	started = time.Now()
	result = call(t, second, "get_fx_param", `{"track_id": 1, "fx_id": 1, "param_id": 1}`)
	if result.Error != nil {
		t.Fatalf("read after the store: %+v", result.Error)
	}
	body, _ := json.Marshal(result.Value)
	t.Logf("read waited %s for the walk: %s", time.Since(started), body)
	if result = call(t, second, "list_fx", `{"track_id": 1}`); result.Value.(agent.FXList).Note != "" {
		t.Fatal("once walked this run, the chain is current and unmarked")
	}
}
