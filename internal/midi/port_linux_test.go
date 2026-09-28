//go:build linux && cgo

package midi

import (
	"bytes"
	"testing"
	"time"
)

// Through the ALSA sequencer itself: two ports in this process connected to
// each other, a sysex longer than one event crossing whole. Skips where
// there is no sequencer, such as a container without snd-seq loaded.
func TestPortLoopsThroughALSA(t *testing.T) {
	a, err := Open("Tonelab test a")
	if err != nil {
		t.Skipf("no ALSA sequencer: %v", err)
	}
	defer a.Close()
	b, err := Open("Tonelab test b")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := connect(a, b); err != nil {
		t.Fatal(err)
	}
	message := append(append([]byte{SysExStart, 0x7D}, bytes.Repeat([]byte("0123456789"), 60)...), SysExEnd)
	if err := a.Send(message); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-b.Messages():
		if !bytes.Equal(got, message) {
			t.Fatalf("got %d bytes, want %d", len(got), len(message))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("nothing arrived through ALSA")
	}
}
