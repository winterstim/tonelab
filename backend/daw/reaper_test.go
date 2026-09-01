package daw_test

import (
	"errors"
	"testing"
	"time"

	"tonelab/backend/daw"
	"tonelab/backend/osc"
	"tonelab/backend/osc/osctest"
)

func newREAPER(t *testing.T) (*daw.REAPER, *osctest.Receiver) {
	t.Helper()

	receiver := osctest.NewReceiver(t)
	return daw.NewREAPER(osc.NewTransport("127.0.0.1", receiver.Port)), receiver
}

// TestCommandsMapToREAPERAddresses is the whole point of this layer: turning
// a domain command into the exact OSC address REAPER documents for it. The
// addresses come from REAPER's own Default.ReaperOSC pattern config, and
// getting one wrong is silent — REAPER ignores addresses it doesn't know.
func TestCommandsMapToREAPERAddresses(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command func(*daw.REAPER) error
		address string
		arg     any
	}{
		{"play", (*daw.REAPER).Play, "/play", nil},
		{"stop", (*daw.REAPER).Stop, "/stop", nil},
		{
			"track volume",
			func(r *daw.REAPER) error { return r.SetTrackVolume(2, 0.75) },
			"/track/2/volume", float32(0.75),
		},
		{
			"track pan",
			func(r *daw.REAPER) error { return r.SetTrackPan(3, 0.5) },
			"/track/3/pan", float32(0.5),
		},
		{
			"track mute on",
			func(r *daw.REAPER) error { return r.SetTrackMute(1, true) },
			"/track/1/mute", float32(1),
		},
		{
			"track mute off",
			func(r *daw.REAPER) error { return r.SetTrackMute(1, false) },
			"/track/1/mute", float32(0),
		},
		{
			"track solo",
			func(r *daw.REAPER) error { return r.SetTrackSolo(4, true) },
			"/track/4/solo", float32(1),
		},
		{
			"track send volume",
			func(r *daw.REAPER) error { return r.SetTrackSendVolume(2, 1, 0.25) },
			"/track/2/send/1/volume", float32(0.25),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reaper, receiver := newREAPER(t)

			if err := tc.command(reaper); err != nil {
				t.Fatalf("command returned an error: %v", err)
			}

			msg := receiver.ExpectAddress(time.Second, tc.address)
			if tc.arg == nil {
				if len(msg.Arguments) != 0 {
					t.Fatalf("expected a bare trigger, got arguments %v", msg.Arguments)
				}
				return
			}
			if len(msg.Arguments) != 1 || msg.Arguments[0] != tc.arg {
				t.Fatalf("expected argument %#v, got %#v", tc.arg, msg.Arguments)
			}
		})
	}
}

// TestRejectsInvalidInputSendsNothing covers the half of this layer that is
// not mapping: refusing a command REAPER would silently mishandle. A track
// index REAPER doesn't have, or a value outside the normalized range every
// parameter uses, must fail here rather than travelling down a
// fire-and-forget UDP socket where nothing can report it.
func TestRejectsInvalidInputSendsNothing(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command func(*daw.REAPER) error
		wantErr error
	}{
		{
			"track index below one",
			func(r *daw.REAPER) error { return r.SetTrackVolume(0, 0.5) },
			daw.ErrInvalidTrack,
		},
		{
			"negative track index",
			func(r *daw.REAPER) error { return r.SetTrackMute(-1, true) },
			daw.ErrInvalidTrack,
		},
		{
			"value above one",
			func(r *daw.REAPER) error { return r.SetTrackVolume(1, 1.5) },
			daw.ErrValueOutOfRange,
		},
		{
			"negative value",
			func(r *daw.REAPER) error { return r.SetTrackPan(1, -0.1) },
			daw.ErrValueOutOfRange,
		},
		{
			"send index below one",
			func(r *daw.REAPER) error { return r.SetTrackSendVolume(1, 0, 0.5) },
			daw.ErrInvalidSend,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reaper, receiver := newREAPER(t)

			err := tc.command(reaper)

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected %v, got %v", tc.wantErr, err)
			}
			receiver.ExpectNothing(100 * time.Millisecond)
		})
	}
}

// TestSurfacesTransportFailure keeps the transport's errors flowing upward:
// the agent tools above this layer decide what to tell the user, and they
// can only do that if a failed send isn't reported as a done command.
func TestSurfacesTransportFailure(t *testing.T) {
	reaper := daw.NewREAPER(osc.NewTransport("127.0.0.1", 0))

	if err := reaper.SetTrackVolume(1, 0.5); err == nil {
		t.Fatal("expected an error when the transport cannot send, got nil")
	}
}
