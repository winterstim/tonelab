package daw_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	goosc "github.com/hypebeast/go-osc/osc"
	"tonelab/backend/daw"
	"tonelab/backend/osc"
	"tonelab/backend/osc/osctest"
)

// fakeREAPER answers a track walk the way the real one does, which is the only
// reason this test means anything: it announces the selected track's name but
// only on a transition, and it clamps an out-of-range selection to the last
// track instead of refusing it. Both were measured against REAPER, and a fake
// missing either would make the walk look correct while it deadlocks or
// truncates in the product.
type fakeREAPER struct {
	names    []string
	selected int

	mu       sync.Mutex
	observed []string
}

func (f *fakeREAPER) record(address string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.observed = append(f.observed, address)
}

func (f *fakeREAPER) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.observed...)
}

func (f *fakeREAPER) run(t *testing.T, receiver *osctest.Receiver, feed chan<- *goosc.Message) {
	t.Helper()

	go func() {
		for {
			msg, ok := receiver.Poll(200 * time.Millisecond)
			if !ok {
				close(feed)
				return
			}
			f.record(msg.Address)
			if msg.Address != "/device/track/select" || len(msg.Arguments) == 0 {
				continue
			}
			requested, ok := msg.Arguments[0].(int32)
			if !ok {
				continue
			}

			target := int(requested)
			if target > len(f.names) {
				target = len(f.names) // clamped, as REAPER does
			}
			if target < 0 || target == f.selected {
				continue // no transition, so nothing is announced
			}
			f.selected = target

			// Index 0 is the master track, which is how the walk parks.
			name := "MASTER"
			if target > 0 {
				name = f.names[target-1]
			}
			feed <- goosc.NewMessage("/track/name", name)
		}
	}()
}

func TestTracksEnumeratesTheProject(t *testing.T) {
	receiver := osctest.NewReceiver(t)
	reaper := daw.NewREAPER(osc.NewTransport("127.0.0.1", receiver.Port))

	feed := make(chan *goosc.Message, 32)
	reaper.Observe(feed)
	fake := &fakeREAPER{names: []string{"Vocals", "Drums", "Bass"}, selected: 1}
	fake.run(t, receiver, feed)

	tracks, err := reaper.Tracks(time.Second)
	if err != nil {
		t.Fatalf("Tracks returned an error: %v", err)
	}

	want := []daw.Track{{1, "Vocals"}, {2, "Drums"}, {3, "Bass"}}
	if fmt.Sprint(tracks) != fmt.Sprint(want) {
		t.Fatalf("expected %v, got %v", want, tracks)
	}
}

// The walk parks on the master track first, because a DAW announcing only
// transitions is silent about a track it is already looking at, and silence
// has to mean one thing before it can end the walk.
func TestTracksFindsTheTrackAlreadySelected(t *testing.T) {
	receiver := osctest.NewReceiver(t)
	reaper := daw.NewREAPER(osc.NewTransport("127.0.0.1", receiver.Port))

	feed := make(chan *goosc.Message, 32)
	reaper.Observe(feed)
	// Already looking at track 1, the case a naive walk would drop.
	fake := &fakeREAPER{names: []string{"Vocals", "Drums"}, selected: 1}
	fake.run(t, receiver, feed)

	tracks, err := reaper.Tracks(time.Second)
	if err != nil {
		t.Fatalf("Tracks returned an error: %v", err)
	}
	if len(tracks) != 2 || tracks[0].Name != "Vocals" {
		t.Fatalf("expected both tracks with track 1 first, got %v", tracks)
	}
}

// Tracks sharing a name must not end the walk, since a repeat looks identical
// to silence unless announcements are counted rather than compared.
func TestTracksHandlesDuplicateNames(t *testing.T) {
	receiver := osctest.NewReceiver(t)
	reaper := daw.NewREAPER(osc.NewTransport("127.0.0.1", receiver.Port))

	feed := make(chan *goosc.Message, 32)
	reaper.Observe(feed)
	fake := &fakeREAPER{names: []string{"Audio", "Audio", "Audio"}, selected: 1}
	fake.run(t, receiver, feed)

	tracks, err := reaper.Tracks(time.Second)
	if err != nil {
		t.Fatalf("Tracks returned an error: %v", err)
	}
	if len(tracks) != 3 {
		t.Fatalf("expected 3 tracks despite the shared name, got %v", tracks)
	}
}

// Reading the project must not change it, so the walk is held to the same
// /device/* restriction as Refresh.
func TestTracksTouchesOnlyTheControlSurface(t *testing.T) {
	receiver := osctest.NewReceiver(t)
	reaper := daw.NewREAPER(osc.NewTransport("127.0.0.1", receiver.Port))

	feed := make(chan *goosc.Message, 32)
	reaper.Observe(feed)
	fake := &fakeREAPER{names: []string{"Vocals"}, selected: 1}
	fake.run(t, receiver, feed)

	if _, err := reaper.Tracks(500 * time.Millisecond); err != nil {
		t.Fatalf("Tracks returned an error: %v", err)
	}

	for _, sent := range fake.sent() {
		if len(sent) < 8 || sent[:8] != "/device/" {
			t.Fatalf("the walk sent %s, but only /device/* may be sent while reading", sent)
		}
	}
}

// A project with one track is where parking has to be exactly right: the
// surface is likely already on that track, so without a park there is no
// transition to announce and the walk would report an empty project.
func TestTracksFindsASingleTrack(t *testing.T) {
	receiver := osctest.NewReceiver(t)
	reaper := daw.NewREAPER(osc.NewTransport("127.0.0.1", receiver.Port))

	feed := make(chan *goosc.Message, 32)
	reaper.Observe(feed)
	fake := &fakeREAPER{names: []string{"Vocals"}, selected: 1}
	fake.run(t, receiver, feed)

	tracks, err := reaper.Tracks(time.Second)
	if err != nil {
		t.Fatalf("Tracks returned an error: %v", err)
	}
	if len(tracks) != 1 || tracks[0].Name != "Vocals" {
		t.Fatalf("expected the one track, got %v", tracks)
	}
}
