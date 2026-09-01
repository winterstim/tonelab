package main

import (
	"errors"
	"strings"
	"testing"

	"tonelab/backend/daw"
)

// stubDAW records what it was asked to do. TransportService takes a
// daw.Client, so its tests need no DAW backend, no OSC socket and no
// addresses — using a real backend here would test that backend twice and
// tie this layer to whichever DAW happens to be first.
type stubDAW struct {
	daw.Client // unimplemented methods panic if this layer ever calls them

	calls []string
	err   error
}

func (s *stubDAW) Play() error { return s.record("play") }
func (s *stubDAW) Stop() error { return s.record("stop") }

func (s *stubDAW) record(command string) error {
	s.calls = append(s.calls, command)
	return s.err
}

// TestTransportService_CallsTheDAW covers what clicking the two buttons in
// the running app does, minus the click: the frontend calls straight into
// these methods through the generated Wails bindings, so everything below
// this point is the same code path.
func TestTransportService_CallsTheDAW(t *testing.T) {
	for _, tc := range []struct {
		name    string
		call    func(*TransportService) string
		command string
	}{
		{"Play", (*TransportService).Play, "play"},
		{"Stop", (*TransportService).Stop, "stop"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &stubDAW{}
			service := NewTransportService(client)

			result := tc.call(service)

			if len(client.calls) != 1 || client.calls[0] != tc.command {
				t.Fatalf("expected one %s command, got %v", tc.command, client.calls)
			}
			if !strings.Contains(result, tc.command) {
				t.Errorf("expected the UI text to name %s, got %q", tc.command, result)
			}
			if strings.Contains(result, "Could not") {
				t.Errorf("expected a success message, got %q", result)
			}
		})
	}
}

// TestTransportService_ReportsFailure pins the other branch: when the DAW
// layer fails, the string the toast shows must say so rather than reporting
// a command that never landed.
func TestTransportService_ReportsFailure(t *testing.T) {
	service := NewTransportService(&stubDAW{err: errors.New("nothing is listening")})

	result := service.Play()

	if !strings.Contains(result, "Could not") {
		t.Errorf("expected a failure message, got %q", result)
	}
	if !strings.Contains(result, "nothing is listening") {
		t.Errorf("expected the underlying reason to reach the user, got %q", result)
	}
}
