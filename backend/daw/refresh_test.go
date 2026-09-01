package daw_test

import (
	"strings"
	"testing"
	"time"
)

// TestRefreshTouchesOnlyTheControlSurface enforces the guarantee that makes
// reading safe on a project someone is working in: asking a DAW to describe
// itself must never change it. REAPER separates /device/* (what our control
// surface is looking at) from /track/* (the project itself), and this pins
// the read path to the first. A regression here would not fail loudly — it
// would quietly move a user's selection, or dirty a project they then have to
// think about saving.
//
// It needs no DAW running, so it guards every normal test run rather than
// only the ones someone remembers to point at REAPER.
func TestRefreshTouchesOnlyTheControlSurface(t *testing.T) {
	reaper, receiver := newREAPER(t)

	if err := reaper.Refresh(3); err != nil {
		t.Fatalf("Refresh returned an error: %v", err)
	}

	// Drain everything Refresh sent, and hold each address to the rule.
	for i := 0; ; i++ {
		msg := receiver.Expect(time.Second)
		if !strings.HasPrefix(msg.Address, "/device/") {
			t.Fatalf("Refresh sent %s — only /device/* addresses may be sent, since anything else changes the project", msg.Address)
		}
		if i >= 1 {
			break
		}
	}
	receiver.ExpectNothing(100 * time.Millisecond)
}

// TestRefreshRejectsAnInvalidTrack keeps the same validation on the read path
// as the write path, rather than sending a nonsense selection at the DAW.
func TestRefreshRejectsAnInvalidTrack(t *testing.T) {
	reaper, receiver := newREAPER(t)

	if err := reaper.Refresh(0); err == nil {
		t.Fatal("expected an error refreshing track 0, got nil")
	}

	receiver.ExpectNothing(100 * time.Millisecond)
}
