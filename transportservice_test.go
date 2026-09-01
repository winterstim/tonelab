package main

import (
	"strings"
	"testing"
	"time"

	"tonelab/backend/daw"
	"tonelab/backend/osc"
	"tonelab/backend/osc/osctest"
)

// TestTransportService_PlayAndStop covers what clicking the two buttons in
// the running app does, minus the click: the frontend calls straight into
// these methods through the generated Wails bindings, so everything below
// this point is the same code path. Verifying it here is what keeps the UI
// buttons from being the only way to exercise the OSC leg.
func TestTransportService_PlayAndStop(t *testing.T) {
	reaper := osctest.NewReceiver(t)
	service := NewTransportService(daw.NewREAPER(osc.NewTransport("127.0.0.1", reaper.Port)))

	for _, tc := range []struct {
		name    string
		call    func() string
		command string
		address string
	}{
		{"Play", service.Play, "play", "/play"},
		{"Stop", service.Stop, "stop", "/stop"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.call()

			reaper.ExpectAddress(time.Second, tc.address)
			if !strings.Contains(result, tc.command) {
				t.Errorf("expected the UI text to name %s, got %q", tc.command, result)
			}
			if strings.Contains(result, "Could not") {
				t.Errorf("expected a success message, got %q", result)
			}
		})
	}
}

// TestTransportService_ReportsSendFailure pins the other branch: when the
// transport fails, the string the toast shows must say so rather than
// reporting a send that never happened. Port 0 is not a routable
// destination, so the write fails locally without needing a DAW.
func TestTransportService_ReportsSendFailure(t *testing.T) {
	service := NewTransportService(daw.NewREAPER(osc.NewTransport("127.0.0.1", 0)))

	result := service.Play()

	if !strings.Contains(result, "Could not") {
		t.Errorf("expected a failure message, got %q", result)
	}
}
