package daw_test

import (
	"errors"
	"testing"
	"time"

	goosc "github.com/hypebeast/go-osc/osc"
	"tonelab/backend/daw"
	"tonelab/backend/osc"
	"tonelab/backend/osc/osctest"
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

// Answering from what was last heard reports a number that is confidently
// wrong right after a change, which is worse than reporting nothing. Observed
// live: a confirmed set came back with the value from before the set.
func TestReadParamWaitsForAnAnswerToThisQuestion(t *testing.T) {
	receiver := osctest.NewReceiver(t)
	reaper := daw.NewREAPER(osc.NewTransport("127.0.0.1", receiver.Port))

	feed := make(chan *goosc.Message, 8)
	reaper.Observe(feed)

	// A stale reading, as an earlier command would have left behind.
	feed <- goosc.NewMessage("/track/1/volume", float32(0.5))
	deadline := time.Now().Add(time.Second)
	for reaper.Observed() < 1 {
		if time.Now().After(deadline) {
			t.Fatal("the backend never took in the first reading")
		}
		time.Sleep(time.Millisecond)
	}

	// The DAW answers the refresh as REAPER does, measured: the selected
	// track's number, then its values with no index at all.
	go func() {
		receiver.Expect(time.Second) // the refresh's first select
		receiver.Expect(time.Second) // and its second
		feed <- goosc.NewMessage("/device/track/select/1", int32(1))
		feed <- goosc.NewMessage("/track/volume", float32(0.9))
	}()

	value, err := reaper.ReadParam(1, "volume", 2*time.Second)
	if err != nil {
		t.Fatalf("ReadParam returned an error: %v", err)
	}
	if value != float64(float32(0.9)) {
		t.Fatalf("expected the DAW's fresh answer 0.9, got %v", value)
	}
}

// Confirming is not asking. A DAW that says nothing has confirmed nothing, and
// reporting the reading from before the change as proof of it would be a claim
// the DAW never made. Asking the same thing through ReadParam is allowed to
// answer from what was last heard, since silence usually means unchanged.
func TestConfirmParamReportsSilenceRatherThanStaleness(t *testing.T) {
	receiver := osctest.NewReceiver(t)
	reaper := daw.NewREAPER(osc.NewTransport("127.0.0.1", receiver.Port))

	feed := make(chan *goosc.Message, 8)
	reaper.Observe(feed)
	feed <- goosc.NewMessage("/track/1/volume", float32(0.5))
	deadline := time.Now().Add(time.Second)
	for reaper.Observed() < 1 {
		if time.Now().After(deadline) {
			t.Fatal("the backend never took in the reading")
		}
		time.Sleep(time.Millisecond)
	}

	if _, err := reaper.ConfirmParam(1, "volume", 200*time.Millisecond); !errors.Is(err, daw.ErrValueUnknown) {
		t.Fatalf("confirming must not accept a reading from before the change, got %v", err)
	}

	value, err := reaper.ReadParam(1, "volume", 200*time.Millisecond)
	if err != nil {
		t.Fatalf("asking should still answer from what was last heard: %v", err)
	}
	if value != float64(float32(0.5)) {
		t.Fatalf("expected the last known 0.5, got %v", value)
	}
}

// A value announced without an index belongs to the track the surface is
// on, which the DAW names first; before it has named one, or on the
// master, the value has no track and is dropped.
func TestUnindexedValuesFollowTheSurface(t *testing.T) {
	reaper := daw.NewREAPER(osc.NewTransport("127.0.0.1", 1))
	feed := make(chan *goosc.Message, 8)
	reaper.Observe(feed)

	feed <- goosc.NewMessage("/track/volume", float32(0.3))
	feed <- goosc.NewMessage("/device/track/select/1", int32(0))
	feed <- goosc.NewMessage("/track/volume", float32(0.716))
	feed <- goosc.NewMessage("/device/track/select/1", int32(2))
	feed <- goosc.NewMessage("/track/volume", float32(0.5))
	feed <- goosc.NewMessage("/track/pan", float32(0.25))
	deadline := time.Now().Add(time.Second)
	for reaper.Observed() < 6 {
		if time.Now().After(deadline) {
			t.Fatal("feedback was not taken in")
		}
		time.Sleep(time.Millisecond)
	}
	if v, err := reaper.GetParam(2, "volume"); err != nil || v != float64(float32(0.5)) {
		t.Fatalf("track 2 volume from the surface: %v, %v", v, err)
	}
	if v, err := reaper.GetParam(2, "pan"); err != nil || v != float64(float32(0.25)) {
		t.Fatalf("track 2 pan from the surface: %v, %v", v, err)
	}
	if _, err := reaper.GetParam(1, "volume"); err == nil {
		t.Fatal("nothing was ever said about track 1")
	}
}
