package daw_test

import (
	"testing"
	"time"
)

// Undo is a project change by definition, so unlike the read path it may send
// outside /device/*. What it must not do is send anything else: an "undo" that
// also touched a parameter would be undoing and doing at once.
func TestUndoSendsOnlyTheUndoAction(t *testing.T) {
	reaper, receiver := newREAPER(t)

	if err := reaper.Undo(); err != nil {
		t.Fatalf("Undo returned an error: %v", err)
	}

	msg := receiver.ExpectAddress(time.Second, "/action")
	if len(msg.Arguments) != 1 {
		t.Fatalf("expected the action id as the only argument, got %v", msg.Arguments)
	}
	if id, ok := msg.Arguments[0].(int32); !ok || id != 40029 {
		t.Fatalf("expected REAPER's Edit: Undo command, got %#v", msg.Arguments[0])
	}
	receiver.ExpectNothing(100 * time.Millisecond)
}
