package daw_test

import (
	"errors"
	"testing"
	"time"

	goosc "github.com/hypebeast/go-osc/osc"
	"tonelab/backend/daw"
)

// Waits for intake because the backend consumes asynchronously, and asserting
// immediately would race the goroutine rather than test the logic.
func feed(t *testing.T, reaper *daw.REAPER, msgs ...*goosc.Message) {
	t.Helper()

	stream := make(chan *goosc.Message, len(msgs))
	for _, msg := range msgs {
		stream <- msg
	}
	close(stream)

	reaper.Observe(stream)

	deadline := time.Now().Add(time.Second)
	for reaper.Observed() < uint64(len(msgs)) {
		if time.Now().After(deadline) {
			t.Fatalf("backend took in only %d of %d messages", reaper.Observed(), len(msgs))
		}
		time.Sleep(time.Millisecond)
	}
}

// The read path's whole purpose. Our record of a write is not the truth: a
// command can be lost on UDP, and a user can move a fader by hand.
func TestGetParamReturnsWhatTheDAWReported(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message *goosc.Message
		param   string
		want    any
	}{
		{
			"numeric parameter",
			goosc.NewMessage("/track/1/volume", float32(0.716)),
			"volume",
			float64(float32(0.716)),
		},
		{
			"toggle reported as on",
			goosc.NewMessage("/track/1/mute", float32(1)),
			"mute",
			true,
		},
		{
			"toggle reported as off",
			goosc.NewMessage("/track/1/solo", float32(0)),
			"solo",
			false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reaper, _ := newREAPER(t)
			feed(t, reaper, tc.message)

			got, err := reaper.GetParam(1, tc.param)
			if err != nil {
				t.Fatalf("GetParam returned an error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("expected %#v, got %#v", tc.want, got)
			}
		})
	}
}

// Feedback is a stream of state, not a log: an old reading is wrong, not
// history.
func TestGetParamTracksTheLatestValue(t *testing.T) {
	reaper, _ := newREAPER(t)

	feed(t, reaper,
		goosc.NewMessage("/track/2/volume", float32(0.2)),
		goosc.NewMessage("/track/2/volume", float32(0.8)),
	)

	got, err := reaper.GetParam(2, "volume")
	if err != nil {
		t.Fatalf("GetParam returned an error: %v", err)
	}
	if got != float64(float32(0.8)) {
		t.Fatalf("expected the most recent value 0.8, got %#v", got)
	}
}

// Reporting one track's value as another's is a plausible-looking wrong
// answer, which is worse than an error.
func TestGetParamSeparatesTracks(t *testing.T) {
	reaper, _ := newREAPER(t)

	feed(t, reaper, goosc.NewMessage("/track/3/volume", float32(0.5)))

	if _, err := reaper.GetParam(1, "volume"); !errors.Is(err, daw.ErrValueUnknown) {
		t.Fatalf("expected track 1 to be unknown, got %v", err)
	}
	if _, err := reaper.GetParam(3, "volume"); err != nil {
		t.Fatalf("expected track 3 to be known: %v", err)
	}
}

// A name this DAW never had needs a different recovery from a value it simply
// has not mentioned yet.
func TestGetParamDistinguishesItsFailures(t *testing.T) {
	for _, tc := range []struct {
		name    string
		track   int
		param   string
		wantErr error
	}{
		{"unknown parameter", 1, "reverb", daw.ErrUnknownParam},
		{"never reported", 1, "volume", daw.ErrValueUnknown},
		{"invalid track", 0, "volume", daw.ErrInvalidTrack},
		{"parameter the DAW does not report", 1, "send", daw.ErrNotReadable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reaper, _ := newREAPER(t)

			_, err := reaper.GetParam(tc.track, tc.param)

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected %v, got %v", tc.wantErr, err)
			}
		})
	}
}

// A DAW streams far more than parameters, and none of it may be mistaken for
// one.
func TestUnrelatedFeedbackIsIgnored(t *testing.T) {
	reaper, _ := newREAPER(t)

	feed(t, reaper,
		goosc.NewMessage("/time", float32(12.5)),
		goosc.NewMessage("/track/1/volume/str", "-6.0dB"),
		goosc.NewMessage("/track/1/name", "Vocals"),
		goosc.NewMessage("/nonsense"),
		goosc.NewMessage("/track/1/volume", float32(0.4)),
	)

	got, err := reaper.GetParam(1, "volume")
	if err != nil {
		t.Fatalf("GetParam returned an error: %v", err)
	}
	if got != float64(float32(0.4)) {
		t.Fatalf("expected 0.4, got %#v", got)
	}
}

// Liveness counts any feedback, including messages this layer does not
// understand: a DAW streaming position updates is plainly alive.
func TestLastSeenTracksAnyFeedback(t *testing.T) {
	reaper, _ := newREAPER(t)

	if !reaper.LastSeen().IsZero() {
		t.Fatal("a backend that has heard nothing must not claim otherwise")
	}

	before := time.Now()
	feed(t, reaper, goosc.NewMessage("/time", float32(1)))

	if reaper.LastSeen().Before(before) {
		t.Fatalf("expected the arrival to be recorded, got %v", reaper.LastSeen())
	}
}
