package daw_test

import (
	"testing"
	"time"

	"tonelab/backend/daw"
	"tonelab/backend/osc"
	"tonelab/backend/osc/osctest"
)

// TestNewBuildsARegisteredBackend covers the seam that keeps DAW choice out
// of application startup: the caller names a backend, it does not construct
// one. Driving a command through the result is what proves the returned
// Client is wired to the transport, not merely non-nil.
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

// TestNewRejectsUnknownBackend keeps a mistyped configuration value from
// producing a nil client that fails much later, somewhere unrelated.
func TestNewRejectsUnknownBackend(t *testing.T) {
	client, err := daw.New("ableton", nil)

	if err == nil {
		t.Fatal("expected an error naming an unregistered backend, got nil")
	}
	if client != nil {
		t.Fatalf("expected no client alongside the error, got %#v", client)
	}
}

// TestBackendsListsWhatCanBeSelected is what configuration and any future
// settings UI read to offer a choice, rather than repeating a list that
// would drift from the registry.
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
