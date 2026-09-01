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
	client, err := daw.New("ableton", nil)

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
		if _, err := daw.New(name, nil); err != nil {
			t.Errorf("Backends listed %q but New rejected it: %v", name, err)
		}
	}
}
