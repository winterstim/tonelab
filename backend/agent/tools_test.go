package agent_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"tonelab/backend/agent"
	"tonelab/backend/agent/llmtest"
	"tonelab/backend/daw"
	"tonelab/backend/daw/dawtest"
)

// A fake backend rather than a real one, so this layer's tests neither depend
// on a DAW nor name one.
type fakeDAW struct {
	daw.Client

	params []daw.Parameter
	values map[string]any
	errs   map[string]error

	setCalls  []setCall
	refreshed []int
	readDelay time.Duration
	tracks    []daw.Track
}

type setCall struct {
	track int
	name  string
	value any
}

func newFakeDAW() *fakeDAW {
	return &fakeDAW{
		params: []daw.Parameter{
			{Name: "volume", Kind: daw.Numeric, Readable: true},
			{Name: "mute", Kind: daw.Toggle, Readable: true},
		},
		values: map[string]any{},
		errs:   map[string]error{},
		tracks: []daw.Track{
			{Number: 1, Name: "Drums"},
			{Number: 2, Name: "Vocals"},
		},
	}
}

func (f *fakeDAW) Parameters() []daw.Parameter { return f.params }

// Validates exactly as a real backend does, which dawtest.AssertClientContract
// holds it to. A fake that accepts what a DAW would refuse reports success
// where the product reports an error, and every test above it goes green
// without proving anything.
func (f *fakeDAW) SetParam(track int, name string, value any) error {
	if err := f.errs["set"]; err != nil {
		return err
	}
	if track < 1 {
		return fmt.Errorf("%w, got %d", daw.ErrInvalidTrack, track)
	}

	param, ok := f.find(name)
	if !ok {
		return fmt.Errorf("%w %q", daw.ErrUnknownParam, name)
	}

	switch param.Kind {
	case daw.Toggle:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%w: %s wants a toggle", daw.ErrParamKind, name)
		}
	default:
		number, ok := value.(float64)
		if !ok {
			return fmt.Errorf("%w: %s wants a number", daw.ErrParamKind, name)
		}
		if number < 0 || number > 1 {
			return fmt.Errorf("%w, got %v", daw.ErrValueOutOfRange, number)
		}
	}

	f.setCalls = append(f.setCalls, setCall{track, name, value})
	return nil
}

// Mirrors the walk's cost and failure shape: enumeration is per track, and a
// backend that cannot answer says so rather than returning an empty project.
func (f *fakeDAW) Tracks(timeout time.Duration) ([]daw.Track, error) {
	if err := f.errs["tracks"]; err != nil {
		return nil, err
	}
	return f.tracks, nil
}

func (f *fakeDAW) find(name string) (daw.Parameter, bool) {
	for _, param := range f.params {
		if param.Name == name {
			return param, true
		}
	}
	return daw.Parameter{}, false
}

// Mirrors the real backend's asynchrony rather than answering instantly, so
// this fake cannot hide the timing the DAW actually imposes.
func (f *fakeDAW) ReadParam(track int, name string, timeout time.Duration) (any, error) {
	f.refreshed = append(f.refreshed, track)
	if err := f.errs["get"]; err != nil {
		return nil, err
	}
	if f.readDelay > timeout {
		return nil, daw.ErrValueUnknown
	}
	time.Sleep(f.readDelay)
	return f.values[name], nil
}

func call(t *testing.T, tools *agent.Tools, name, args string) agent.Result {
	t.Helper()
	return tools.Call(name, json.RawMessage(args))
}

// Parameter access is two generic tools and stays two however many parameters
// exist. list_tracks is not a third of those: it describes the
// project rather than a control, so it grows with nothing.
func TestParameterAccessIsTwoGenericTools(t *testing.T) {
	tools := agent.NewTools(newFakeDAW())

	defs := tools.Definitions()

	names := map[string]bool{}
	for _, def := range defs {
		names[def.Name] = true
		if def.InputSchema == nil {
			t.Errorf("%s has no input schema", def.Name)
		}
	}
	for _, want := range []string{"get_param", "set_param", "list_tracks"} {
		if !names[want] {
			t.Errorf("expected a %s tool, got %v", want, names)
		}
	}
	if len(defs) != 3 {
		t.Fatalf("expected exactly those 3 tools, got %d: %v", len(defs), names)
	}
}

// param_name stays a free string in the schema, so the valid names
// reach the model through the description instead, taken from the backend so
// no second list exists to drift.
func TestDescriptionsListTheBackendsOwnParameters(t *testing.T) {
	backend := newFakeDAW()
	backend.params = append(backend.params, daw.Parameter{Name: "wobble", Kind: daw.Numeric, Readable: true})
	tools := agent.NewTools(backend)

	for _, def := range tools.Definitions() {
		if def.Name == "list_tracks" {
			continue // describes the project, not a parameter
		}
		if !strings.Contains(def.Description, "wobble") {
			t.Errorf("%s description does not mention the backend's own parameter: %q", def.Name, def.Description)
		}
		if strings.Contains(def.InputSchemaJSON(), "wobble") {
			t.Errorf("%s schema hardcodes a parameter name, which the design rules out", def.Name)
		}
	}
}

func TestSetParamDrivesTheDAW(t *testing.T) {
	backend := newFakeDAW()
	tools := agent.NewTools(backend)

	result := call(t, tools, "set_param", `{"track_id":2,"param_name":"volume","value":0.75}`)

	if result.Error != nil {
		t.Fatalf("expected success, got %+v", result.Error)
	}
	if len(backend.setCalls) != 1 {
		t.Fatalf("expected one set, got %v", backend.setCalls)
	}
	got := backend.setCalls[0]
	if got.track != 2 || got.name != "volume" || got.value != 0.75 {
		t.Fatalf("unexpected call %+v", got)
	}
}

// Booleans skip normalization entirely, which is why the schema accepts either
// shape rather than splitting into two tools.
func TestSetParamCarriesBooleans(t *testing.T) {
	backend := newFakeDAW()
	tools := agent.NewTools(backend)

	result := call(t, tools, "set_param", `{"track_id":1,"param_name":"mute","value":true}`)

	if result.Error != nil {
		t.Fatalf("expected success, got %+v", result.Error)
	}
	if backend.setCalls[0].value != true {
		t.Fatalf("expected a boolean to survive as one, got %#v", backend.setCalls[0].value)
	}
}

// Reading refreshes first because a DAW that only announces changes may never
// have mentioned the value, and an agent cannot be expected to know that.
func TestGetParamRefreshesThenReads(t *testing.T) {
	backend := newFakeDAW()
	backend.values["volume"] = 0.5
	tools := agent.NewTools(backend)

	result := call(t, tools, "get_param", `{"track_id":3,"param_name":"volume"}`)

	if result.Error != nil {
		t.Fatalf("expected success, got %+v", result.Error)
	}
	if len(backend.refreshed) == 0 || backend.refreshed[0] != 3 {
		t.Fatalf("expected track 3 to be refreshed first, got %v", backend.refreshed)
	}
	if result.Value != 0.5 {
		t.Fatalf("expected 0.5, got %#v", result.Value)
	}
}

// Every domain failure is a structured code the agent can branch on, never a
// Go error and never prose.
func TestDomainFailuresBecomeCodes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		fail     error
		wantCode string
	}{
		{"unknown parameter", daw.ErrUnknownParam, "param_not_found"},
		{"invalid track", daw.ErrInvalidTrack, "invalid_track"},
		{"out of range", daw.ErrValueOutOfRange, "value_out_of_range"},
		{"wrong value type", daw.ErrParamKind, "invalid_value_type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := newFakeDAW()
			backend.errs["set"] = tc.fail
			tools := agent.NewTools(backend)

			result := call(t, tools, "set_param", `{"track_id":1,"param_name":"volume","value":0.5}`)

			if result.Error == nil {
				t.Fatal("expected a structured error, got success")
			}
			if result.Error.Code != tc.wantCode {
				t.Fatalf("expected code %q, got %q", tc.wantCode, result.Error.Code)
			}
			if result.Error.Message == "" {
				t.Error("expected a message safe to show the user")
			}
		})
	}
}

// A value the DAW has not reported is not a missing parameter, and the agent's
// recovery differs, so the codes differ too.
func TestUnknownValueIsItsOwnCode(t *testing.T) {
	backend := newFakeDAW()
	backend.errs["get"] = daw.ErrValueUnknown
	tools := agent.NewTools(backend)

	result := call(t, tools, "get_param", `{"track_id":1,"param_name":"volume"}`)

	if result.Error == nil || result.Error.Code != "value_unknown" {
		t.Fatalf("expected value_unknown, got %+v", result.Error)
	}
}

// Malformed arguments and unknown tools come back in the same structured shape
// as everything else, since a model that guessed wrong needs to read the reply.
func TestMalformedCallsStayStructured(t *testing.T) {
	tools := agent.NewTools(newFakeDAW())

	for _, tc := range []struct {
		name     string
		tool     string
		args     string
		wantCode string
	}{
		{"unknown tool", "set_everything", `{}`, "unknown_tool"},
		{"broken json", "set_param", `{`, "invalid_arguments"},
		{"missing field", "set_param", `{"track_id":1,"value":0.5}`, "invalid_arguments"},
		{"wrong field type", "set_param", `{"track_id":"two","param_name":"volume","value":0.5}`, "invalid_arguments"},
		{"value neither number nor boolean", "set_param", `{"track_id":1,"param_name":"volume","value":"loud"}`, "invalid_arguments"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := call(t, tools, tc.tool, tc.args)

			if result.Error == nil {
				t.Fatal("expected a structured error, got success")
			}
			if result.Error.Code != tc.wantCode {
				t.Fatalf("expected %q, got %q (%s)", tc.wantCode, result.Error.Code, result.Error.Message)
			}
		})
	}
}

// The schema is what the model is told; the Go checks are what actually runs.
// They are two statements of one rule, so a test holds them together.
func TestSchemaAndValidationAgreeOnRequiredFields(t *testing.T) {
	tools := agent.NewTools(newFakeDAW())

	for _, def := range tools.Definitions() {
		var schema struct {
			Required []string `json:"required"`
		}
		if err := json.Unmarshal([]byte(def.InputSchemaJSON()), &schema); err != nil {
			t.Fatalf("%s: schema is not valid JSON: %v", def.Name, err)
		}
		for _, field := range schema.Required {
			args := map[string]any{"track_id": 1, "param_name": "volume", "value": 0.5}
			delete(args, field)
			encoded, _ := json.Marshal(args)

			if result := tools.Call(def.Name, encoded); result.Error == nil {
				t.Errorf("%s: schema requires %q but the call succeeded without it", def.Name, field)
			}
		}
	}
}

// A DAW slower than the timeout must report the value as unknown rather than
// hang an agent turn, which is the failure the caller can actually act on.
func TestSlowDAWTimesOutAsUnknown(t *testing.T) {
	backend := newFakeDAW()
	backend.readDelay = time.Hour
	tools := agent.NewTools(backend)

	result := call(t, tools, "get_param", `{"track_id":1,"param_name":"volume"}`)

	if result.Error == nil || result.Error.Code != "value_unknown" {
		t.Fatalf("expected value_unknown, got %+v", result.Error)
	}
}

// The fake is held to the same contract as the real backend, so it cannot
// quietly accept what a DAW would refuse. Both fake tests that went green and
// useless in this project failed exactly that way.
func TestFakeDAWMeetsTheClientContract(t *testing.T) {
	dawtest.AssertClientContract(t, newFakeDAW())
}

// A live model emitted {"value":"0.5"} against a schema saying number, and it
// is the smaller models, the ones a user is likeliest to run locally, that do
// it most. Rejecting an unambiguous number because of its quotes would fail a
// command the model got completely right.
func TestNumbersArrivingAsStringsAreAccepted(t *testing.T) {
	for _, tc := range []struct {
		name string
		args string
		want any
	}{
		{"quoted float", `{"track_id":2,"param_name":"volume","value":"0.5"}`, 0.5},
		{"quoted integer", `{"track_id":2,"param_name":"volume","value":"1"}`, 1.0},
		{"quoted track id", `{"track_id":"2","param_name":"volume","value":0.5}`, 0.5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := newFakeDAW()
			tools := agent.NewTools(backend)

			result := call(t, tools, "set_param", tc.args)

			if result.Error != nil {
				t.Fatalf("expected the call to be understood, got %+v", result.Error)
			}
			if len(backend.setCalls) != 1 {
				t.Fatalf("expected one command, got %v", backend.setCalls)
			}
			if backend.setCalls[0].track != 2 || backend.setCalls[0].value != tc.want {
				t.Fatalf("expected track 2 and %v, got %+v", tc.want, backend.setCalls[0])
			}
		})
	}
}

// Leniency stops at ambiguity: text that is not a number, and a boolean
// spelled as a word, are the model getting it wrong rather than formatting it
// oddly, and it needs to be told.
func TestOnlyUnambiguousStringsAreAccepted(t *testing.T) {
	for _, tc := range []struct {
		name string
		args string
	}{
		{"words", `{"track_id":1,"param_name":"volume","value":"loud"}`},
		{"units the contract does not use", `{"track_id":1,"param_name":"volume","value":"-6dB"}`},
		{"empty", `{"track_id":1,"param_name":"volume","value":""}`},
		{"track id that is not a number", `{"track_id":"the vocals","param_name":"volume","value":0.5}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := newFakeDAW()
			tools := agent.NewTools(backend)

			result := call(t, tools, "set_param", tc.args)

			if result.Error == nil || result.Error.Code != "invalid_arguments" {
				t.Fatalf("expected invalid_arguments, got %+v", result.Error)
			}
			if len(backend.setCalls) != 0 {
				t.Fatalf("nothing should have reached the DAW, got %v", backend.setCalls)
			}
		})
	}
}

// Without this the other tools are unusable by name: they address tracks by
// number, and a user saying "the vocals" has no number to give.
func TestListTracksReportsTheProject(t *testing.T) {
	tools := agent.NewTools(newFakeDAW())

	result := call(t, tools, "list_tracks", `{}`)

	if result.Error != nil {
		t.Fatalf("expected success, got %+v", result.Error)
	}
	tracks, ok := result.Value.([]daw.Track)
	if !ok || len(tracks) != 2 {
		t.Fatalf("expected two tracks, got %#v", result.Value)
	}
	if tracks[1].Name != "Vocals" || tracks[1].Number != 2 {
		t.Fatalf("expected Vocals as track 2, got %+v", tracks[1])
	}
}

// The model is told the tool exists only when the backend can answer it, since
// a tool that always fails is worse than one that is absent.
func TestListTracksIsOfferedOnlyWhenSupported(t *testing.T) {
	withNames := agent.NewTools(newFakeDAW())
	if !offers(withNames.Definitions(), "list_tracks") {
		t.Error("a backend that can list tracks should offer the tool")
	}

	withoutNames := agent.NewTools(&numbersOnlyDAW{fakeDAW: newFakeDAW()})
	if offers(withoutNames.Definitions(), "list_tracks") {
		t.Error("a backend that cannot list tracks must not offer the tool")
	}
}

// Embeds the fake but hides Tracks, standing for a DAW whose surface cannot
// name what it contains.
type numbersOnlyDAW struct{ *fakeDAW }

func (numbersOnlyDAW) Tracks() {}

func offers(definitions []agent.Tool, name string) bool {
	for _, definition := range definitions {
		if definition.Name == name {
			return true
		}
	}
	return false
}

// A live model asked to mute a track sent "true", then 1, and was refused
// each time, spending a slow round trip per guess. None of these readings is
// ambiguous for a parameter that is on or off.
func TestTogglesAcceptWhatModelsActuallySend(t *testing.T) {
	for _, tc := range []struct {
		name string
		args string
		want bool
	}{
		{"boolean", `{"track_id":1,"param_name":"mute","value":true}`, true},
		{"quoted boolean", `{"track_id":1,"param_name":"mute","value":"true"}`, true},
		{"quoted false", `{"track_id":1,"param_name":"mute","value":"false"}`, false},
		{"one", `{"track_id":1,"param_name":"mute","value":1}`, true},
		{"zero", `{"track_id":1,"param_name":"mute","value":0}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := newFakeDAW()
			tools := agent.NewTools(backend)

			result := call(t, tools, "set_param", tc.args)

			if result.Error != nil {
				t.Fatalf("expected the call to be understood, got %+v", result.Error)
			}
			if backend.setCalls[0].value != tc.want {
				t.Fatalf("expected %v, got %#v", tc.want, backend.setCalls[0].value)
			}
		})
	}
}

// A refusal has to say what this parameter wanted. "A number or true/false"
// leaves the model to guess which, and every guess is another slow round trip
// while the user waits.
func TestRefusalsNameWhatTheParameterWants(t *testing.T) {
	tools := agent.NewTools(newFakeDAW())

	toggle := call(t, tools, "set_param", `{"track_id":1,"param_name":"mute","value":0.5}`)
	if toggle.Error == nil || !strings.Contains(toggle.Error.Message, "true or false") {
		t.Errorf("expected the toggle's refusal to ask for true or false, got %+v", toggle.Error)
	}

	numeric := call(t, tools, "set_param", `{"track_id":1,"param_name":"volume","value":"loud"}`)
	if numeric.Error == nil || !strings.Contains(numeric.Error.Message, "0.0 and 1.0") {
		t.Errorf("expected the numeric refusal to state the range, got %+v", numeric.Error)
	}
}

// A slow local model is reachable, and saying otherwise sends the user to
// check a URL that is fine.
func TestSlowEndpointIsReportedAsATimeout(t *testing.T) {
	server := llmtest.New(t, llmtest.Turn{Delay: 300 * time.Millisecond, Content: "too late"})
	orchestrator := agent.NewOrchestrator(agent.Config{
		BaseURL: server.BaseURL(), Model: "m", Timeout: 50 * time.Millisecond,
	}, agent.NewTools(newFakeDAW()))

	response := orchestrator.Send("anything")

	if response.Error == nil || response.Error.Code != "llm_timeout" {
		t.Fatalf("expected llm_timeout, got %+v", response.Error)
	}
}

// The project's whole policy on how a model may write a value, as a table.
// Two of these rows were production failures found by running a real model,
// which is why the list is written down rather than reasoned about: a new one
// belongs here first, not in a user's session.
func TestValueRepresentationPolicy(t *testing.T) {
	for _, tc := range []struct {
		param    string
		value    string
		accepted bool
		want     any
	}{
		// Numbers, however they are spelled.
		{"volume", `0.5`, true, 0.5},
		{"volume", `"0.5"`, true, 0.5}, // seen live
		{"volume", `".5"`, true, 0.5},
		{"volume", `" 0.5 "`, true, 0.5}, // whitespace a model may pad with
		{"volume", `1`, true, 1.0},
		{"volume", `"50%"`, true, 0.5}, // unambiguous against a 0-1 contract
		{"volume", `0`, true, 0.0},

		// Units need the DAW's own curve to convert, so guessing is worse
		// than refusing.
		{"volume", `"-6dB"`, false, nil},
		{"volume", `"440Hz"`, false, nil},
		{"volume", `"loud"`, false, nil},
		{"volume", `"half"`, false, nil},
		{"volume", `true`, false, nil},
		{"volume", `""`, false, nil},
		{"volume", `null`, false, nil},

		// Switches, however they are spelled.
		{"mute", `true`, true, true},
		{"mute", `"true"`, true, true}, // seen live
		{"mute", `"True"`, true, true},
		{"mute", `1`, true, true}, // seen live
		{"mute", `"yes"`, true, true},
		{"mute", `"on"`, true, true},
		{"mute", `false`, true, false},
		{"mute", `"off"`, true, false},
		{"mute", `0`, true, false},

		// A switch is not a dial, and 0.5 of muted means nothing.
		{"mute", `0.5`, false, nil},
		{"mute", `"maybe"`, false, nil},
		{"mute", `"muted"`, false, nil},
	} {
		name := tc.param + " " + tc.value
		t.Run(name, func(t *testing.T) {
			backend := newFakeDAW()
			tools := agent.NewTools(backend)
			args := `{"track_id":1,"param_name":"` + tc.param + `","value":` + tc.value + `}`

			result := call(t, tools, "set_param", args)

			if !tc.accepted {
				if result.Error == nil {
					t.Fatalf("expected a refusal, but the DAW received %v", backend.setCalls)
				}
				if len(backend.setCalls) != 0 {
					t.Fatalf("a refused value still reached the DAW: %v", backend.setCalls)
				}
				return
			}
			if result.Error != nil {
				t.Fatalf("expected acceptance, got %+v", result.Error)
			}
			if backend.setCalls[0].value != tc.want {
				t.Fatalf("expected %#v, got %#v", tc.want, backend.setCalls[0].value)
			}
		})
	}
}

// A command left over a socket that guarantees nothing, so "sent" and "done"
// must not be the same word to a model reporting to a user.
func TestSetParamConfirmsFromTheDAW(t *testing.T) {
	backend := newFakeDAW()
	backend.values["volume"] = 0.25
	tools := agent.NewTools(backend)

	result := call(t, tools, "set_param", `{"track_id":1,"param_name":"volume","value":0.25}`)

	if result.Error != nil {
		t.Fatalf("expected success, got %+v", result.Error)
	}
	applied, ok := result.Value.(agent.Applied)
	if !ok {
		t.Fatalf("expected the set to report what was applied, got %#v", result.Value)
	}
	if applied.Confirmed != 0.25 {
		t.Fatalf("expected the DAW's own reading, got %#v", applied.Confirmed)
	}
	if applied.Note != "" {
		t.Errorf("a confirmed change should carry no caveat, got %q", applied.Note)
	}
}

// A DAW that never reports the parameter has still accepted the command, so
// this is neither a failure nor a confirmation, and the model must be able to
// tell the difference.
func TestUnverifiableChangeSaysSo(t *testing.T) {
	backend := newFakeDAW()
	backend.errs["get"] = daw.ErrValueUnknown
	tools := agent.NewTools(backend)

	result := call(t, tools, "set_param", `{"track_id":1,"param_name":"volume","value":0.25}`)

	if result.Error != nil {
		t.Fatalf("an unverified change is not a failed one, got %+v", result.Error)
	}
	applied := result.Value.(agent.Applied)
	if applied.Confirmed != nil {
		t.Errorf("nothing should be presented as confirmed, got %#v", applied.Confirmed)
	}
	if !strings.Contains(applied.Note, "unverified") {
		t.Errorf("expected the caveat to be explicit, got %q", applied.Note)
	}
}

// A refusal that lists what does exist turns a lost turn into a corrected one.
// Observed live: a model wrote "muted" for "mute" and had nothing to correct
// against.
func TestUnknownParameterNamesTheAlternatives(t *testing.T) {
	tools := agent.NewTools(newFakeDAW())

	result := call(t, tools, "set_param", `{"track_id":1,"param_name":"muted","value":true}`)

	if result.Error == nil || result.Error.Code != "param_not_found" {
		t.Fatalf("expected param_not_found, got %+v", result.Error)
	}
	for _, expected := range []string{"muted", "mute", "volume"} {
		if !strings.Contains(result.Error.Message, expected) {
			t.Errorf("expected the refusal to mention %q, got %q", expected, result.Error.Message)
		}
	}
}
