//go:build reaper

// Round-trip tests against a real running REAPER. Excluded from the normal
// suite by the `reaper` build tag — see backend/osc/README.md for what REAPER
// has to be configured to do (the feedback leg is not on by default).
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

	// REAPER's "Track: Insert new track" command, so the test can create the
	// track it operates on instead of assuming the open project has one.
	actionInsertTrack = 40001

	await = 3 * time.Second
)

// TestREAPERAcceptsCommands is the check the fake receiver cannot make.
// Asserting on the address we send only proves we sent what we meant to; only
// REAPER's own feedback proves REAPER understood it — including the argument
// encoding, since mute and solo go out as 1.0/0.0 floats on a reading of
// REAPER's `b/` pattern prefix that nothing else verifies.
//
// Note what REAPER does NOT send: it never echoes back the normalized value a
// device just set (no /track/N/volume, no /track/N/pan), only the derived
// readouts. Asserting on those is stronger anyway — a dB figure proves REAPER
// interpreted the normalized value, not merely that it stored it.
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

	// Transport first: it needs no track, and if REAPER is not actually
	// acting on what we send, failing here says so before anything else.
	t.Run("transport", func(t *testing.T) {
		// REAPER reports transitions, not states, so stop first to make the
		// play that follows an actual change.
		if err := reaper.Stop(); err != nil {
			t.Fatalf("Stop: %v", err)
		}
		feedback.AwaitValue("/stop", 1, 0, await)

		if err := reaper.Play(); err != nil {
			t.Fatalf("Play: %v", err)
		}
		feedback.AwaitValue("/play", 1, 0, await)

		// Leave the transport stopped, so a rerun starts clean and REAPER is
		// not left rolling after the suite exits.
		if err := reaper.Stop(); err != nil {
			t.Fatalf("Stop: %v", err)
		}
		feedback.AwaitValue("/stop", 1, 0, await)
	})

	t.Run("volume", func(t *testing.T) {
		if err := reaper.SetTrackVolume(track, 0.25); err != nil {
			t.Fatalf("SetTrackVolume: %v", err)
		}
		// 0.25 of REAPER's own volume taper is -30 dB. Pinning the number
		// makes this a test of the whole normalized-value contract
		//, not just of message delivery.
		feedback.AwaitValue(address(track, "volume/db"), -30, 0.1, await)
	})

	t.Run("pan", func(t *testing.T) {
		if err := reaper.SetTrackPan(track, 0.75); err != nil {
			t.Fatalf("SetTrackPan: %v", err)
		}
		// Pan comes back only as a formatted string; 0.75 is half right.
		awaitString(t, feedback, address(track, "pan/str"), "50%R")
	})

	// REAPER reports transitions, not states, so each toggle starts from a
	// known position before the assertion it cares about.
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

		// Leave the project unsoloed; a stray solo silences everything else
		// and would be a nasty thing to hand back to whoever is using REAPER.
		mustToggle(t, reaper.SetTrackSolo, track, false)
		feedback.AwaitValue(address(track, "solo"), 0, 0, await)
	})
}

// TestREAPERFeedbackReachesTheProductionListener checks the read path the
// application will actually use, not the test helper that stands in for it
// elsewhere in this file. get_param depends on this: a listener that works
// against a fake receiver but not against REAPER would pass every test in
// the normal suite and return nothing in the product.
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

// TestREAPERReadPath is what get_param rests on, checked against the real
// thing. It also pins the awkward truth this design had to be built around:
// REAPER does not echo back a value the device itself set, so writing then
// reading returns nothing. A value becomes known when REAPER volunteers it,
// and the safe way to make it volunteer is to ask the control surface to look
// at the track — a /device/* message, which touches the surface's own view
// and not the project.
func TestREAPERReadPath(t *testing.T) {
	listener, err := osc.Listen(reaperHost, reaperFeedbackPort)
	if err != nil {
		t.Fatalf("could not listen for REAPER's feedback: %v", err)
	}
	defer listener.Close()

	transport := osc.NewTransport(reaperHost, reaperListenPort)
	reaper := daw.NewREAPER(transport)
	reaper.Observe(listener.Messages())

	// Set a value, then confirm that alone does NOT make it readable.
	if err := reaper.SetTrackVolume(1, 0.25); err != nil {
		t.Fatalf("SetTrackVolume: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := reaper.GetParam(1, "volume"); !errors.Is(err, daw.ErrValueUnknown) {
		t.Fatalf("expected the value to still be unknown after setting it, got %v", err)
	}

	// Now make REAPER volunteer its state, without touching the project.
	transport.Send("/device/track/select", int32(2))
	time.Sleep(200 * time.Millisecond)
	transport.Send("/device/track/select", int32(1))

	deadline := time.Now().Add(await)
	for {
		value, err := reaper.GetParam(1, "volume")
		if err == nil {
			// REAPER's taper puts our 0.25 back at roughly 0.25.
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

// awaitNewTrack returns the 1-based index of the track REAPER just created,
// so the test operates on its own track rather than on whatever the open
// project happens to contain. REAPER announces the new track by selecting
// it — there is no "here is the new track" message, and the track count it
// accepts on /device/track/count travels the other way, device to REAPER.
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
