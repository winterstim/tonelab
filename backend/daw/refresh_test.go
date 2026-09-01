package daw_test

import (
	"strings"
	"testing"
	"time"
)

// Asking a DAW to describe itself must never change it. REAPER separates
// /device/* (our surface's view) from /track/* (the project), and this pins
// reading to the first. A regression would not fail loudly: it would quietly
// move a user's selection or dirty their project. Needs no DAW, so it guards
// every run rather than the ones someone points at REAPER.
func TestRefreshTouchesOnlyTheControlSurface(t *testing.T) {
	reaper, receiver := newREAPER(t)

	if err := reaper.Refresh(3); err != nil {
		t.Fatalf("Refresh returned an error: %v", err)
	}

	// Drain everything Refresh sent, and hold each address to the rule.
	for i := 0; ; i++ {
		msg := receiver.Expect(time.Second)
		if !strings.HasPrefix(msg.Address, "/device/") {
			t.Fatalf("Refresh sent %s, but only /device/* addresses may be sent, since anything else changes the project", msg.Address)
		}
		if i >= 1 {
			break
		}
	}
	receiver.ExpectNothing(100 * time.Millisecond)
}

// The read path validates as the write path does, rather than sending a
// nonsense selection at the DAW.
func TestRefreshRejectsAnInvalidTrack(t *testing.T) {
	reaper, receiver := newREAPER(t)

	if err := reaper.Refresh(0); err == nil {
		t.Fatal("expected an error refreshing track 0, got nil")
	}

	receiver.ExpectNothing(100 * time.Millisecond)
}
