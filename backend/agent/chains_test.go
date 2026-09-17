package agent_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"tonelab/backend/agent"
	"tonelab/backend/daw"
)

type fakeStore struct {
	mu     sync.Mutex
	known  map[int][]daw.FX
	saved  int
	broken error
}

func (s *fakeStore) Load() (map[int][]daw.FX, error) {
	return s.known, s.broken
}

func (s *fakeStore) Save(known map[int][]daw.FX) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.known = known
	s.saved++
	return nil
}

func (s *fakeStore) saves() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saved
}

// What the last session knew: the same amp, but the reverb has since been
// removed, so fx 3 no longer exists.
func lastSession() map[int][]daw.FX {
	return map[int][]daw.FX{1: {
		{Number: 1, Name: "Amp Sim", Params: []daw.FXParam{{Number: 1, Name: "Input Gain"}, {Number: 2, Name: "Bass"}}},
		{Number: 2, Name: "Cabinet", Params: []daw.FXParam{{Number: 1, Name: "Mic Distance"}}},
		{Number: 3, Name: "Reverb", Params: []daw.FXParam{{Number: 1, Name: "Room Size"}, {Number: 2, Name: "Mix"}}},
	}}
}

func TestStoredChainAnswersReadsBeforeTheWalkFinishes(t *testing.T) {
	backend := newFakeFXDAW()
	backend.delay = 300 * time.Millisecond
	store := &fakeStore{known: lastSession()}
	tools := agent.NewTools(backend)
	tools.ShareChains(agent.NewChainCache(store))

	started := time.Now()
	result := call(t, tools, "find_params", `{"track_id": 1, "query": "reverb mix"}`)
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	if time.Since(started) > 100*time.Millisecond {
		t.Fatal("a stored chain should answer without waiting for the walk")
	}
	found := result.Value.(agent.ParamMatches)
	if found.Note == "" || len(found.Matches) == 0 || found.Matches[0].FXName != "Reverb" {
		t.Fatalf("expected last session's reverb with a note, got %+v", found)
	}
}

func TestWriteWaitsForTheWalkAndUsesTheCurrentChain(t *testing.T) {
	backend := newFakeFXDAW()
	backend.delay = 200 * time.Millisecond
	backend.chains[1] = backend.chains[1][:2]
	store := &fakeStore{known: lastSession()}
	tools := agent.NewTools(backend)
	tools.ShareChains(agent.NewChainCache(store))

	call(t, tools, "list_fx", `{"track_id": 1}`)
	// The reverb the store remembers is gone; the write must find that
	// out from the DAW rather than send fx 3 anything.
	result := call(t, tools, "set_fx_param", `{"track_id": 1, "fx_id": 3, "param_id": 2, "value": 0.5}`)
	if result.Error == nil || result.Error.Code != "unknown_fx" {
		t.Fatalf("a write must resolve against the DAW's current chain, got %+v", result)
	}
	if backend.walks.Load() != 1 {
		t.Fatalf("one walk should serve both calls, got %d", backend.walks.Load())
	}

	result = call(t, tools, "list_fx", `{"track_id": 1}`)
	if list := result.Value.(agent.FXList); list.Note != "" || len(list.FX) != 2 {
		t.Fatalf("after the walk the chain is current and unmarked, got %+v", list)
	}
	if store.saves() != 1 || len(store.known[1]) != 2 {
		t.Fatalf("the walk should have been stored, saves=%d known=%d", store.saves(), len(store.known[1]))
	}
}

func TestStoredChainSurvivesAFailedWalkForReadsOnly(t *testing.T) {
	backend := newFakeFXDAW()
	store := &fakeStore{known: lastSession()}
	tools := agent.NewTools(newRefusingFXDAW(backend))
	tools.ShareChains(agent.NewChainCache(store))

	if result := call(t, tools, "list_fx", `{"track_id": 1}`); result.Error != nil {
		t.Fatalf("a read may still use the store, got %+v", result.Error)
	}
	time.Sleep(50 * time.Millisecond)
	result := call(t, tools, "set_fx_param", `{"track_id": 1, "fx_id": 1, "param_id": 1, "value": 0.5}`)
	if result.Error == nil {
		t.Fatal("a write with no fresh chain must be refused")
	}
	if store.saves() != 0 {
		t.Fatal("a failed walk must not overwrite the store")
	}
}

func TestPreviewAndLiveToolsShareOneWalk(t *testing.T) {
	backend := newFakeFXDAW()
	cache := agent.NewChainCache(nil)
	live := agent.NewTools(backend)
	live.ShareChains(cache)
	preview := agent.NewPreviewTools(backend)
	preview.ShareChains(cache)

	call(t, preview, "list_fx", `{"track_id": 1}`)
	call(t, live, "list_fx", `{"track_id": 1}`)
	if backend.walks.Load() != 1 {
		t.Fatalf("expected one walk across both, got %d", backend.walks.Load())
	}
}

func TestBrokenStoreIsAnEmptyStore(t *testing.T) {
	backend := newFakeFXDAW()
	tools := agent.NewTools(backend)
	tools.ShareChains(agent.NewChainCache(&fakeStore{broken: errors.New("unreadable")}))
	result := call(t, tools, "list_fx", `{"track_id": 1}`)
	if result.Error != nil || !strings.Contains(fmt.Sprint(result.Value), "Amp Sim") {
		t.Fatalf("with no store the DAW is walked, got %+v", result)
	}
}

// refusingFXDAW walks nothing, as a DAW that is not running would.
type refusingFXDAW struct{ *fakeFXDAW }

func newRefusingFXDAW(f *fakeFXDAW) *refusingFXDAW { return &refusingFXDAW{f} }

func (r *refusingFXDAW) FXChain(track int, timeout time.Duration) ([]daw.FX, error) {
	return nil, daw.ErrValueUnknown
}
