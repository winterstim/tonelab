package daw_test

import (
	"errors"
	"testing"
	"time"

	"tonelab/backend/daw"
)

// Keeps a parameter list out of every layer above, where a copy would drift
// and would be wrong for any DAW with a different set.
func TestParametersAreReportedByTheBackend(t *testing.T) {
	client, _ := newREAPER(t)

	params, err := daw.ParametersOf(client)
	if err != nil {
		t.Fatalf("ParametersOf returned an error: %v", err)
	}

	// The MVP set, each with the shape its value takes.
	want := map[string]daw.Kind{
		"volume": daw.Numeric,
		"pan":    daw.Numeric,
		"mute":   daw.Toggle,
		"solo":   daw.Toggle,
		"send":   daw.Numeric,
	}
	if len(params) != len(want) {
		t.Fatalf("expected %d parameters, got %d: %v", len(want), len(params), params)
	}
	for _, param := range params {
		kind, known := want[param.Name]
		if !known {
			t.Errorf("backend reported an unexpected parameter %q", param.Name)
			continue
		}
		if param.Kind != kind {
			t.Errorf("%s: expected kind %v, got %v", param.Name, kind, param.Kind)
		}
	}
}

// A caller must not edit the backend's description of itself through the
// slice it was handed.
func TestParametersCannotBeMutatedByCallers(t *testing.T) {
	client, _ := newREAPER(t)

	first, _ := daw.ParametersOf(client)
	first[0].Name = "tampered"

	second, _ := daw.ParametersOf(client)
	if second[0].Name == "tampered" {
		t.Fatal("a caller mutated the backend's parameter list")
	}
}

// The generic entry point the tools layer calls. Resolution stays inside the
// backend so layers above pass names through rather than translating them.
func TestSetParamRoutesByName(t *testing.T) {
	for _, tc := range []struct {
		name    string
		param   string
		value   any
		address string
		arg     any
	}{
		{"numeric parameter", "volume", 0.75, "/track/2/volume", float32(0.75)},
		{"toggle on", "mute", true, "/track/2/mute", float32(1)},
		{"toggle off", "solo", false, "/track/2/solo", float32(0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, receiver := newREAPER(t)

			if err := client.SetParam(2, tc.param, tc.value); err != nil {
				t.Fatalf("SetParam returned an error: %v", err)
			}

			msg := receiver.ExpectAddress(time.Second, tc.address)
			if len(msg.Arguments) != 1 || msg.Arguments[0] != tc.arg {
				t.Fatalf("expected argument %#v, got %#v", tc.arg, msg.Arguments)
			}
		})
	}
}

// The agent needs "no such parameter" and "wrong kind of value" kept apart,
// since the recoveries differ.
func TestSetParamRejectsBadRequests(t *testing.T) {
	for _, tc := range []struct {
		name    string
		param   string
		value   any
		wantErr error
	}{
		{"unknown parameter", "reverb", 0.5, daw.ErrUnknownParam},
		{"number for a toggle", "mute", 0.5, daw.ErrParamKind},
		{"boolean for a number", "volume", true, daw.ErrParamKind},
		{"value out of range", "volume", 1.5, daw.ErrValueOutOfRange},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, receiver := newREAPER(t)

			err := client.SetParam(1, tc.param, tc.value)

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected %v, got %v", tc.wantErr, err)
			}
			receiver.ExpectNothing(100 * time.Millisecond)
		})
	}
}
