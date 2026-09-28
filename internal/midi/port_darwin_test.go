//go:build darwin && cgo

package midi

import (
	"bytes"
	"testing"
	"time"
)

// Through CoreMIDI itself: a second client sends a sysex longer than one
// packet to the port, and it comes out whole.
func TestPortReceivesLongSysexWhole(t *testing.T) {
	port, err := Open("Tonelab test port")
	if err != nil {
		t.Fatal(err)
	}
	defer port.Close()

	body := bytes.Repeat([]byte("0123456789"), 60)
	message := append(append([]byte{SysExStart, 0x7D}, body...), SysExEnd)
	if err := port.loopSend(message); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-port.Messages():
		if !bytes.Equal(got, message) {
			t.Fatalf("got %d bytes, want %d, equal=%v", len(got), len(message), bytes.Equal(got, message))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("nothing arrived through CoreMIDI")
	}
}

// What the port sends reaches a client listening to it, long sysex whole.
func TestPortSendReachesAListener(t *testing.T) {
	port, err := Open("Tonelab test port 2")
	if err != nil {
		t.Fatal(err)
	}
	defer port.Close()
	listener, err := port.loopListen()
	if err != nil {
		t.Fatal(err)
	}
	message := append(append([]byte{SysExStart, 0x7D}, bytes.Repeat([]byte("abcdefghij"), 50)...), SysExEnd)
	if err := port.Send(message); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-listener.got:
		if !bytes.Equal(got, message) {
			t.Fatalf("got %d bytes, want %d", len(got), len(message))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("nothing reached the listener")
	}
}
