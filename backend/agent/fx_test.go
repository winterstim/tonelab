package agent_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"tonelab/backend/agent"
	"tonelab/backend/daw"
)

// fakeFXDAW stands for a backend that can enumerate effects. It refuses what
// the real one refuses (indices below one), echoes a set as a plugin would
// (quantized), and is silent about an effect that does not exist, since the
// real DAW is silent about it too.
type fakeFXDAW struct {
	*fakeDAW
	chains   map[int][]daw.FX
	fxValues map[string]float64
	walks    int
}

func newFakeFXDAW() *fakeFXDAW {
	return &fakeFXDAW{
		fakeDAW: newFakeDAW(),
		chains: map[int][]daw.FX{
			1: {
				{Number: 1, Name: "Amp Sim", Params: []daw.FXParam{
					{Number: 1, Name: "Input Gain"}, {Number: 2, Name: "Bass"}, {Number: 3, Name: "Middle"}, {Number: 4, Name: "Treble"}, {Number: 5, Name: "Presence"}, {Number: 6, Name: "Master"},
				}},
				{Number: 2, Name: "Cabinet", Params: []daw.FXParam{{Number: 1, Name: "Mic Distance"}, {Number: 2, Name: "Low Cut"}, {Number: 3, Name: "High Cut"}}},
				{Number: 3, Name: "Reverb", Params: []daw.FXParam{{Number: 1, Name: "Room Size"}, {Number: 2, Name: "Mix"}, {Number: 3, Name: "Bypass", Kind: "switch", Steps: 2}}},
			},
			2: nil,
		},
		fxValues: map[string]float64{},
	}
}

func (f *fakeFXDAW) FXChain(track int, timeout time.Duration) ([]daw.FX, error) {
	if track < 1 {
		return nil, daw.ErrInvalidTrack
	}
	f.walks++
	return f.chains[track], nil
}

func fxKey(track, fx, param int) string { return fmt.Sprintf("%d/%d/%d", track, fx, param) }

func (f *fakeFXDAW) exists(track, fx, param int) bool {
	chain := f.chains[track]
	return fx >= 1 && fx <= len(chain) && param >= 1 && param <= len(chain[fx-1].Params)
}

func (f *fakeFXDAW) SetFXParam(track, fx, param int, value float64) error {
	if track < 1 || fx < 1 || param < 1 {
		return daw.ErrInvalidFX
	}
	if value < 0 || value > 1 {
		return daw.ErrValueOutOfRange
	}
	if f.exists(track, fx, param) {
		f.fxValues[fxKey(track, fx, param)] = float64(int(value*100)) / 100
	}
	return nil
}

func (f *fakeFXDAW) ConfirmFXParam(track, fx, param int, timeout time.Duration) (float64, error) {
	value, ok := f.fxValues[fxKey(track, fx, param)]
	if !ok {
		return 0, daw.ErrValueUnknown
	}
	return value, nil
}

func (f *fakeFXDAW) ReadFXParam(track, fx, param int, timeout time.Duration) (float64, error) {
	return f.ConfirmFXParam(track, fx, param, timeout)
}

// Effect tools exist only for a backend that can enumerate effects, as
// list_tracks does; a tool that always fails is worse than one that is absent.
func TestFXToolsAreOfferedOnlyWhenSupported(t *testing.T) {
	with := agent.NewTools(newFakeFXDAW())
	without := agent.NewTools(newFakeDAW())
	for _, name := range []string{"list_fx", "find_params", "get_fx_param", "set_fx_param"} {
		if !offers(with.Definitions(), name) {
			t.Errorf("%s should be offered by a backend that enumerates effects", name)
		}
		if offers(without.Definitions(), name) {
			t.Errorf("%s must not be offered by a backend that cannot", name)
		}
	}
}

// list_fx names the effects and how many parameters each has, and nothing
// more: the parameters themselves are what find_params is for.
func TestListFXNamesWithoutDumping(t *testing.T) {
	tools := agent.NewTools(newFakeFXDAW())

	result := call(t, tools, "list_fx", `{"track_id": 1}`)

	if result.Error != nil {
		t.Fatalf("expected success, got %+v", result.Error)
	}
	text, _ := json.Marshal(result.Value)
	if !strings.Contains(string(text), `"Amp Sim"`) || !strings.Contains(string(text), `"Reverb"`) {
		t.Fatalf("expected the effect names, got %s", text)
	}
	if strings.Contains(string(text), "Presence") {
		t.Fatalf("list_fx must not dump parameters, got %s", text)
	}
}

// find_params is how a model reaches a parameter without being handed every
// parameter on the track. Words match against parameter and effect names,
// case-insensitively, and the best matches come first.
func TestFindParamsMatchesWords(t *testing.T) {
	tools := agent.NewTools(newFakeFXDAW())

	result := call(t, tools, "find_params", `{"track_id": 1, "query": "reverb mix"}`)

	if result.Error != nil {
		t.Fatalf("expected success, got %+v", result.Error)
	}
	matches, ok := result.Value.([]agent.ParamMatch)
	if !ok || len(matches) == 0 {
		t.Fatalf("expected matches, got %#v", result.Value)
	}
	if matches[0].FX != 3 || matches[0].Param != 2 {
		t.Fatalf("expected Reverb Mix (fx 3 param 2) first, got %+v", matches[0])
	}
	for _, match := range matches {
		if match.FXName == "" || match.Name == "" {
			t.Errorf("a match must carry both names, got %+v", match)
		}
	}
}

func TestFindParamsReportsNoMatchAsSuch(t *testing.T) {
	tools := agent.NewTools(newFakeFXDAW())

	result := call(t, tools, "find_params", `{"track_id": 1, "query": "wobble"}`)

	if result.Error == nil || result.Error.Code != "no_match" {
		t.Fatalf("expected no_match, got %+v", result)
	}
	if !strings.Contains(result.Error.Message, "Amp Sim") {
		t.Fatalf("a refusal should name the effects that exist, got %q", result.Error.Message)
	}
}

func TestFindParamsOnAnEmptyTrack(t *testing.T) {
	tools := agent.NewTools(newFakeFXDAW())

	result := call(t, tools, "find_params", `{"track_id": 2, "query": "gain"}`)

	if result.Error == nil || result.Error.Code != "no_fx" {
		t.Fatalf("expected no_fx, got %+v", result)
	}
}

// A set is reported from the DAW's echo, as the plugin rounded it.
func TestSetFXParamReportsWhatTheDAWSays(t *testing.T) {
	backend := newFakeFXDAW()
	tools := agent.NewTools(backend)

	result := call(t, tools, "set_fx_param", `{"track_id": 1, "fx_id": 1, "param_id": 2, "value": 0.777}`)

	if result.Error != nil {
		t.Fatalf("expected success, got %+v", result.Error)
	}
	applied, ok := result.Value.(agent.AppliedFX)
	if !ok {
		t.Fatalf("expected AppliedFX, got %#v", result.Value)
	}
	if applied.Confirmed == nil || *applied.Confirmed != 0.77 {
		t.Fatalf("expected the quantized 0.77 back, got %+v", applied)
	}
	if applied.Name != "Bass" || applied.FXName != "Amp Sim" {
		t.Fatalf("the report should carry names a user recognises, got %+v", applied)
	}
}

// The DAW is silent about an effect that does not exist, so the refusal has
// to come from what the chain says rather than from waiting.
func TestSetFXParamRefusesAnEffectThatIsNotThere(t *testing.T) {
	tools := agent.NewTools(newFakeFXDAW())

	result := call(t, tools, "set_fx_param", `{"track_id": 1, "fx_id": 9, "param_id": 1, "value": 0.5}`)

	if result.Error == nil || result.Error.Code != "unknown_fx" {
		t.Fatalf("expected unknown_fx, got %+v", result)
	}
}

func TestGetFXParamReadsBack(t *testing.T) {
	backend := newFakeFXDAW()
	backend.fxValues[fxKey(1, 2, 1)] = 0.4
	tools := agent.NewTools(backend)

	result := call(t, tools, "get_fx_param", `{"track_id": 1, "fx_id": 2, "param_id": 1}`)

	if result.Error != nil {
		t.Fatalf("expected success, got %+v", result.Error)
	}
	read, ok := result.Value.(agent.ReadFX)
	if !ok || read.Value != 0.4 || read.Name != "Mic Distance" {
		t.Fatalf("expected 0.4 for Mic Distance, got %#v", result.Value)
	}
}

// The chain is walked once per track within a turn's span, not once per
// tool call: each walk moves the surface and costs most of a second.
func TestFXChainIsNotWalkedPerCall(t *testing.T) {
	backend := newFakeFXDAW()
	tools := agent.NewTools(backend)

	call(t, tools, "list_fx", `{"track_id": 1}`)
	call(t, tools, "find_params", `{"track_id": 1, "query": "gain"}`)
	call(t, tools, "set_fx_param", `{"track_id": 1, "fx_id": 1, "param_id": 1, "value": 0.5}`)

	if backend.walks != 1 {
		t.Fatalf("expected one walk, got %d", backend.walks)
	}
}

// Every effect tool is a read, or a write to one addressed parameter: none
// changes the project when previewing.
func TestPreviewDisarmsSetFXParam(t *testing.T) {
	backend := newFakeFXDAW()
	tools := agent.NewPreviewTools(backend)

	result := call(t, tools, "set_fx_param", `{"track_id": 1, "fx_id": 1, "param_id": 1, "value": 0.5}`)

	if result.Error != nil {
		t.Fatalf("expected a plan, got %+v", result.Error)
	}
	if _, ok := result.Value.(agent.Planned); !ok {
		t.Fatalf("expected Planned, got %#v", result.Value)
	}
	if len(backend.fxValues) != 0 {
		t.Fatal("a preview must not reach the DAW")
	}
}

// What a value means travels with the match, so a model is not left to send
// 0.8 to a switch and read "unverified" back.
func TestFindParamsCarriesTheKind(t *testing.T) {
	tools := agent.NewTools(newFakeFXDAW())
	result := call(t, tools, "find_params", `{"track_id": 1, "query": "bypass"}`)
	matches, ok := result.Value.([]agent.ParamMatch)
	if !ok || len(matches) == 0 {
		t.Fatalf("expected matches, got %+v", result)
	}
	if matches[0].Name != "Bypass" || matches[0].Kind != "switch" || matches[0].Steps != 2 {
		t.Fatalf("expected Bypass as a two-step switch, got %+v", matches[0])
	}
	text, _ := json.Marshal(matches[0])
	if !strings.Contains(string(text), `"kind":"switch"`) {
		t.Fatalf("the kind must reach the model: %s", text)
	}
}
