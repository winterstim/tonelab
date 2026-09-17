package daw_test

import (
	"testing"
	"time"

	"tonelab/backend/daw"
	"tonelab/backend/osc"
	"tonelab/backend/osc/osctest"
)

// Keeps DAW choice out of application startup. Drives a command through the
// result, since a non-nil Client proves nothing about its wiring.
func TestNewBuildsARegisteredBackend(t *testing.T) {
	receiver := osctest.NewReceiver(t)

	client, err := daw.New("reaper", osc.NewTransport("127.0.0.1", receiver.Port))
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}
	if err := client.Play(); err != nil {
		t.Fatalf("Play returned an error: %v", err)
	}

	receiver.ExpectAddress(time.Second, "/play")
}

// A mistyped config value must not produce a nil client that fails later,
// somewhere unrelated.
func TestNewRejectsUnknownBackend(t *testing.T) {
	client, err := daw.New("protools", nil)

	if err == nil {
		t.Fatal("expected an error naming an unregistered backend, got nil")
	}
	if client != nil {
		t.Fatalf("expected no client alongside the error, got %#v", client)
	}
}

// A settings UI reads this to offer choices, so it must not drift from what
// New actually accepts.
func TestBackendsListsWhatCanBeSelected(t *testing.T) {
	names := daw.Backends()

	if len(names) == 0 {
		t.Fatal("expected at least one registered backend")
	}
	for _, name := range names {
		transport, err := daw.TransportOf(name)
		if err != nil {
			t.Errorf("Backends listed %q but it has no transport: %v", name, err)
			continue
		}
		switch transport {
		case daw.OSC:
			_, err = daw.New(name, nil)
		case daw.MIDI:
			_, err = daw.NewMIDI(name, nil)
		}
		if err != nil {
			t.Errorf("Backends listed %q but its constructor rejected it: %v", name, err)
		}
	}
}

// A backend says which transport it needs, so startup can open a MIDI port
// or an OSC socket without naming the DAW; asking for the wrong one is an
// error rather than a nil client.
func TestRegistryKnowsEachBackendsTransport(t *testing.T) {
	if transport, err := daw.TransportOf("flstudio"); err != nil || transport != daw.MIDI {
		t.Fatalf("flstudio speaks MIDI, got %v, %v", transport, err)
	}
	if transport, err := daw.TransportOf("reaper"); err != nil || transport != daw.OSC {
		t.Fatalf("reaper speaks OSC, got %v, %v", transport, err)
	}
	if _, err := daw.New("flstudio", nil); err == nil {
		t.Fatal("an OSC sender cannot drive a MIDI backend")
	}
	if _, err := daw.NewMIDI("reaper", nil); err == nil {
		t.Fatal("a MIDI link cannot drive an OSC backend")
	}
	if _, err := daw.TransportOf("protools"); err == nil {
		t.Fatal("unknown backends have no transport")
	}
}
