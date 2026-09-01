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
	}
}

func (f *fakeDAW) Parameters() []daw.Parameter { return f.params }

// Validates the name as a real backend does. A fake that accepts anything
// would report success where the product reports param_not_found, which is the
// kind of kindness that makes a test green and useless.
func (f *fakeDAW) SetParam(track int, name string, value any) error {
	if err := f.errs["set"]; err != nil {
		return err
	}
	if !f.knows(name) {
		return fmt.Errorf("%w %q", daw.ErrUnknownParam, name)
	}
	f.setCalls = append(f.setCalls, setCall{track, name, value})
	return nil
}

func (f *fakeDAW) knows(name string) bool {
	for _, param := range f.params {
		if param.Name == name {
			return true
		}
	}
	return false
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

// The two tools are the whole agent-facing surface; one tool per
// control would grow without bound.
func TestDefinitionsAreTheTwoGenericTools(t *testing.T) {
	tools := agent.NewTools(newFakeDAW())

	defs := tools.Definitions()

	if len(defs) != 2 {
		t.Fatalf("expected exactly 2 tools, got %d", len(defs))
	}
	names := map[string]bool{}
	for _, def := range defs {
		names[def.Name] = true
		if def.InputSchema == nil {
			t.Errorf("%s has no input schema", def.Name)
		}
	}
	for _, want := range []string{"get_param", "set_param"} {
		if !names[want] {
			t.Errorf("expected a %s tool, got %v", want, names)
		}
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
