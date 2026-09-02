package daw_test

import (
	"errors"
	"testing"
	"time"

	"tonelab/backend/daw"
	"tonelab/backend/osc"
	"tonelab/backend/osc/osctest"
)

// A transport that reports what a guarded backend would have sent, so a
// refusal can be told apart from a delivery.
type recordingSender struct{ sent []string }

func (r *recordingSender) Send(address string, args ...any) error {
	r.sent = append(r.sent, address)
	return nil
}

// REAPER's /action reaches every menu item it has, including quitting and
// closing without saving, which are outside its undo history. The permitted
// ids are listed rather than the forbidden ones: there are thousands of
// actions and the list grows with REAPER, so only naming what we mean stays
// correct.
func TestOnlyListedActionsAreSent(t *testing.T) {
	for _, id := range []int32{40004, 40026, 40860, 41929, 1, 0, -1, 999999} {
		sender := &recordingSender{}
		reaper := daw.NewREAPER(sender)

		err := daw.SendActionForTest(reaper, id)

		if !errors.Is(err, daw.ErrForbidden) {
			t.Errorf("action %d: expected it to be refused, got %v", id, err)
		}
		if len(sender.sent) != 0 {
			t.Errorf("action %d: reached the DAW anyway", id)
		}
	}
}

// Undo is the one action this backend runs, and it must still work.
func TestUndoIsPermitted(t *testing.T) {
	reaper, receiver := newREAPER(t)

	if err := reaper.Undo(); err != nil {
		t.Fatalf("Undo was refused: %v", err)
	}

	receiver.ExpectAddress(time.Second, "/action")
}

// The guard fails closed, so an address nobody wrote down on purpose does not
// reach the DAW even if a future method sends it.
func TestUnlistedAddressesAreRefused(t *testing.T) {
	for _, address := range []string{
		"/track/1/name",    // renaming is a project edit nothing here needs
		"/track/1/recarm",  // arming a track for recording
		"/marker/1/delete", // destroying project structure
		"/time",            // meaningless to send, and not ours to send
		"/action/40004",    // the id smuggled into the address instead
		"/anything",
	} {
		sender := &recordingSender{}
		reaper := daw.NewREAPER(sender)

		if err := daw.SendForTest(reaper, address); !errors.Is(err, daw.ErrForbidden) {
			t.Errorf("%s: expected it to be refused, got %v", address, err)
		}
		if len(sender.sent) != 0 {
			t.Errorf("%s: reached the DAW anyway", address)
		}
	}
}

// Everything the backend actually does still gets through, or the guard would
// be protecting the user from the product.
func TestPermittedCommandsStillReachTheDAW(t *testing.T) {
	receiver := osctest.NewReceiver(t)
	reaper := daw.NewREAPER(osc.NewTransport("127.0.0.1", receiver.Port))

	for _, command := range []struct {
		name string
		run  func() error
	}{
		{"play", reaper.Play},
		{"stop", reaper.Stop},
		{"volume", func() error { return reaper.SetTrackVolume(1, 0.5) }},
		{"pan", func() error { return reaper.SetTrackPan(1, 0.5) }},
		{"mute", func() error { return reaper.SetTrackMute(1, true) }},
		{"solo", func() error { return reaper.SetTrackSolo(1, true) }},
		{"send volume", func() error { return reaper.SetTrackSendVolume(1, 1, 0.5) }},
		{"refresh", func() error { return reaper.Refresh(1) }},
	} {
		t.Run(command.name, func(t *testing.T) {
			if err := command.run(); err != nil {
				t.Fatalf("%s was refused: %v", command.name, err)
			}
			receiver.Expect(time.Second)
		})
	}
}
