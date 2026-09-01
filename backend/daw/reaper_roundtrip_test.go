//go:build reaper

// Round-trip tests against a real running REAPER, behind a build tag because
// they need one. See backend/osc/README.md for the configuration; REAPER's
// feedback leg is off by default.
//
//	go test -tags reaper -count=1 ./backend/daw/...
package daw_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	goosc "github.com/hypebeast/go-osc/osc"
	"tonelab/backend/daw"
	"tonelab/backend/osc"
	"tonelab/backend/osc/osctest"
)

const (
	reaperHost         = "127.0.0.1"
	reaperListenPort   = 8000
	reaperFeedbackPort = 9000

	// So the test creates the track it operates on rather than assuming the
	// open project has one.
	actionInsertTrack = 40001

	await = 3 * time.Second
)

// The check the fake receiver cannot make: asserting on what we send only
// proves we sent it. Argument encoding is the real subject, since mute and
// solo go out as floats on a reading of REAPER's `b/` prefix.
//
// REAPER never echoes back a value the device just set, only derived
// readouts, so the assertions use those. That is stronger anyway: a dB figure
// proves REAPER interpreted the value rather than merely stored it.
func TestREAPERAcceptsCommands(t *testing.T) {
	feedback := osctest.NewFeedback(t, reaperFeedbackPort)
	transport := osc.NewTransport(reaperHost, reaperListenPort)
	reaper := daw.NewREAPER(transport)

	// Work on a track this test created, so it neither depends on nor
	// disturbs whatever project happens to be open.
	if err := transport.Send("/action", int32(actionInsertTrack)); err != nil {
		t.Fatalf("could not insert a track to test against: %v", err)
	}
	track := awaitNewTrack(t, feedback)
	t.Logf("operating on track %d", track)

	// First because it needs no track, so a REAPER that is not acting on our
	// commands fails here rather than somewhere more confusing.
	t.Run("transport", func(t *testing.T) {
		// REAPER reports transitions, so stop first to make play a change.
		if err := reaper.Stop(); err != nil {
			t.Fatalf("Stop: %v", err)
		}
		feedback.AwaitValue("/stop", 1, 0, await)

		if err := reaper.Play(); err != nil {
			t.Fatalf("Play: %v", err)
		}
		feedback.AwaitValue("/play", 1, 0, await)

		// Leave it stopped so a rerun starts clean.
		if err := reaper.Stop(); err != nil {
			t.Fatalf("Stop: %v", err)
		}
		feedback.AwaitValue("/stop", 1, 0, await)
	})

	t.Run("volume", func(t *testing.T) {
		if err := reaper.SetTrackVolume(track, 0.25); err != nil {
			t.Fatalf("SetTrackVolume: %v", err)
		}
		// Pinning the dB figure tests the normalized-value contract
		//, not just message delivery.
		feedback.AwaitValue(address(track, "volume/db"), -30, 0.1, await)
	})

	t.Run("pan", func(t *testing.T) {
		if err := reaper.SetTrackPan(track, 0.75); err != nil {
			t.Fatalf("SetTrackPan: %v", err)
		}
		// Pan comes back only as a formatted string.
		awaitString(t, feedback, address(track, "pan/str"), "50%R")
	})

	// Each toggle starts from a known position, since REAPER reports
	// transitions rather than states.
	t.Run("mute", func(t *testing.T) {
		mustToggle(t, reaper.SetTrackMute, track, false)
		mustToggle(t, reaper.SetTrackMute, track, true)
		feedback.AwaitValue(address(track, "mute"), 1, 0, await)

		mustToggle(t, reaper.SetTrackMute, track, false)
		feedback.AwaitValue(address(track, "mute"), 0, 0, await)
	})

	t.Run("solo", func(t *testing.T) {
		mustToggle(t, reaper.SetTrackSolo, track, false)
		mustToggle(t, reaper.SetTrackSolo, track, true)
		feedback.AwaitValue(address(track, "solo"), 1, 0, await)

		// A stray solo silences everything else, so never leave one behind.
		mustToggle(t, reaper.SetTrackSolo, track, false)
		feedback.AwaitValue(address(track, "solo"), 0, 0, await)
	})
}

// Checks the listener the application actually uses, not the helper standing
// in for it elsewhere here. One that works against a fake receiver but not
// REAPER would pass every normal test and return nothing in the product.
func TestREAPERFeedbackReachesTheProductionListener(t *testing.T) {
	listener, err := osc.Listen(reaperHost, reaperFeedbackPort)
	if err != nil {
		t.Fatalf("could not listen for REAPER's feedback: %v", err)
	}
	defer listener.Close()

	reaper := daw.NewREAPER(osc.NewTransport(reaperHost, reaperListenPort))

	// Stop first so the play that follows is a transition REAPER reports.
	if err := reaper.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := reaper.Play(); err != nil {
		t.Fatalf("Play: %v", err)
	}

	deadline := time.After(await)
	for {
		select {
		case msg, open := <-listener.Messages():
			if !open {
				t.Fatal("the listener's stream closed while waiting for REAPER")
			}
			if msg.Address == "/play" {
				_ = reaper.Stop() // do not leave REAPER rolling
				return
			}
		case <-deadline:
			t.Fatalf("REAPER's feedback never reached the listener within %s (dropped %d)", await, listener.Dropped())
		}
	}
}

// Pins the awkward truth this design was built around: REAPER does not echo
// back a value the device itself set, so writing then reading returns
// nothing. A value becomes known when REAPER volunteers it, and /device/*
// makes it volunteer without touching the project.
func TestREAPERReadPath(t *testing.T) {
	listener, err := osc.Listen(reaperHost, reaperFeedbackPort)
	if err != nil {
		t.Fatalf("could not listen for REAPER's feedback: %v", err)
	}
	defer listener.Close()

	transport := osc.NewTransport(reaperHost, reaperListenPort)
	reaper := daw.NewREAPER(transport)
	reaper.Observe(listener.Messages())

	// Setting alone must not make it readable.
	if err := reaper.SetTrackVolume(1, 0.25); err != nil {
		t.Fatalf("SetTrackVolume: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := reaper.GetParam(1, "volume"); !errors.Is(err, daw.ErrValueUnknown) {
		t.Fatalf("expected the value to still be unknown after setting it, got %v", err)
	}

	// Make REAPER volunteer, without touching the project.
	transport.Send("/device/track/select", int32(2))
	time.Sleep(200 * time.Millisecond)
	transport.Send("/device/track/select", int32(1))

	deadline := time.Now().Add(await)
	for {
		value, err := reaper.GetParam(1, "volume")
		if err == nil {
			// Allow for REAPER's own rounding.
			got, ok := value.(float64)
			if !ok {
				t.Fatalf("expected a float64, got %#v", value)
			}
			if got < 0.2 || got > 0.3 {
				t.Fatalf("expected roughly the 0.25 we set, got %v", got)
			}
			t.Logf("read back %v after %d feedback messages", got, reaper.Observed())
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("value never became known within %s (%d messages observed): %v", await, reaper.Observed(), err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func address(track int, param string) string {
	return fmt.Sprintf("/track/%d/%s", track, param)
}

func mustToggle(t *testing.T, set func(int, bool) error, track int, on bool) {
	t.Helper()

	if err := set(track, on); err != nil {
		t.Fatalf("setting %v on track %d: %v", on, track, err)
	}
}

func awaitString(t *testing.T, feedback *osctest.Feedback, address, want string) {
	t.Helper()

	feedback.Await(address, await, func(msg *goosc.Message) bool {
		return len(msg.Arguments) > 0 && msg.Arguments[0] == want
	})
}

// REAPER has no "here is the new track" message and its track count travels
// device to REAPER, not back, so the new track is identified by the selection
// REAPER announces.
func awaitNewTrack(t *testing.T, feedback *osctest.Feedback) int {
	t.Helper()

	var track int
	feedback.AwaitAny("the newly inserted track to be selected", await, func(m *goosc.Message) bool {
		var index int
		if _, err := fmt.Sscanf(m.Address, "/track/%d/select", &index); err != nil {
			return false
		}
		if len(m.Arguments) == 0 {
			return false
		}
		selected, ok := m.Arguments[0].(float32)
		if !ok || selected != 1 {
			return false
		}
		track = index
		return true
	})
	return track
}
