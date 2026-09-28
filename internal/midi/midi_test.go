package midi

import (
	"bytes"
	"testing"
)

func TestAssemblerJoinsSplitSysexAndDropsTheRest(t *testing.T) {
	var a assembler
	if got := a.feed([]byte{0x90, 0x40, 0x7F}); len(got) != 0 {
		t.Fatalf("a note is not sysex, got %v", got)
	}
	if got := a.feed([]byte{SysExStart, 0x7D, 'a', 'b'}); len(got) != 0 {
		t.Fatal("half a message is not a message")
	}
	got := a.feed([]byte{'c', SysExEnd, SysExStart, 0x7D, 'x', SysExEnd})
	if len(got) != 2 || !bytes.Equal(got[0], []byte{SysExStart, 0x7D, 'a', 'b', 'c', SysExEnd}) || !bytes.Equal(got[1], []byte{SysExStart, 0x7D, 'x', SysExEnd}) {
		t.Fatalf("expected two whole messages, got %v", got)
	}
	// A status byte mid-message means the sender gave up; so do we.
	a.feed([]byte{SysExStart, 0x7D, 'a', 0x90, 0x40, 0x7F})
	if got := a.feed([]byte{'b', SysExEnd}); len(got) != 0 {
		t.Fatalf("a cut-off message must not be completed by later bytes, got %v", got)
	}
}
