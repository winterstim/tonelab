//go:build reaper

// This test needs a real, running REAPER and is excluded from the normal
// suite by the `reaper` build tag. Run it with:
//
//	go test -tags reaper -count=1 ./backend/osc/...
//
// REAPER must have an OSC control surface enabled (Preferences >
// Control/OSC/web > Add > OSC) listening on 8000 and, critically, sending
// feedback to 127.0.0.1:9000 — without the feedback leg REAPER never says
// whether a command landed, and this test cannot exist.
package osc_test

import (
	"testing"
	"time"

	"tonelab/backend/osc"
	"tonelab/backend/osc/osctest"
)

const (
	reaperHost         = "127.0.0.1"
	reaperListenPort   = 8000
	reaperFeedbackPort = 9000
)

// TestReaperRoundTrip_PlayAndStop is the check that no mock can make: that
// REAPER itself acts on what this package sends. It asserts on REAPER's own
// feedback (/play 1, /stop 1) rather than on anything Tonelab believes about
// the message it wrote, which is the only way to tell a delivered command
// from a lost one over UDP.
func TestReaperRoundTrip_PlayAndStop(t *testing.T) {
	feedback := osctest.NewFeedback(t, reaperFeedbackPort)
	transport := osc.NewTransport(reaperHost, reaperListenPort)

	// Start from a known state: REAPER reports a transition, so a project
	// already playing would never announce /play again.
	if err := transport.Send("/stop"); err != nil {
		t.Fatalf("Send(/stop) failed: %v", err)
	}
	feedback.AwaitValue("/stop", 1, 0, 3*time.Second)

	if err := transport.Send("/play"); err != nil {
		t.Fatalf("Send(/play) failed: %v", err)
	}
	feedback.AwaitValue("/play", 1, 0, 3*time.Second)

	// Leave the transport stopped so a rerun starts clean and REAPER isn't
	// left rolling after the suite exits.
	if err := transport.Send("/stop"); err != nil {
		t.Fatalf("Send(/stop) failed: %v", err)
	}
	feedback.AwaitValue("/stop", 1, 0, 3*time.Second)
}
