//go:build reaper

// This test needs a real, running REAPER and is excluded from the normal
// suite by the `reaper` build tag. Run it with:
//
//	go test -tags reaper -count=1 ./backend/osc/...
//
// REAPER must have an OSC control surface enabled (Preferences >
// Control/OSC/web > Add > OSC) listening on 8000 and, critically, sending
// feedback to 127.0.0.1:9000 — without the feedback leg REAPER never says
// whether a command landed, and this test cannot exist.
package osc_test

import (
	"net"
	"testing"
	"time"

	goosc "github.com/hypebeast/go-osc/osc"
	"tonelab/backend/osc"
)

const (
	reaperHost         = "127.0.0.1"
	reaperListenPort   = 8000
	reaperFeedbackPort = 9000
)

// TestReaperRoundTrip_PlayAndStop is the check that no mock can make: that
// REAPER itself acts on what this package sends. It asserts on REAPER's own
// feedback (/play 1, /stop 1) rather than on anything Tonelab believes about
// the message it wrote, which is the only way to tell a delivered command
// from a lost one over UDP.
func TestReaperRoundTrip_PlayAndStop(t *testing.T) {
	feedback := listenForFeedback(t)
	transport := osc.NewTransport(reaperHost, reaperListenPort)

	// Start from a known state: REAPER only reports a transition, so a
	// project already playing would never send /play 1 again.
	if err := transport.Send("/stop"); err != nil {
		t.Fatalf("Send(/stop) failed: %v", err)
	}
	awaitFeedback(t, feedback, "/stop", 3*time.Second)

	if err := transport.Send("/play"); err != nil {
		t.Fatalf("Send(/play) failed: %v", err)
	}
	awaitFeedback(t, feedback, "/play", 3*time.Second)

	// Leave the transport stopped so a rerun starts clean and REAPER isn't
	// left rolling after the suite exits.
	if err := transport.Send("/stop"); err != nil {
		t.Fatalf("Send(/stop) failed: %v", err)
	}
	awaitFeedback(t, feedback, "/stop", 3*time.Second)
}

// flatten unwraps a packet into the messages it carries. REAPER sends its
// feedback as OSC bundles, not bare messages — a receive path that only
// understands messages sees nothing at all, which is exactly how this test
// failed the first time it ran.
func flatten(packet goosc.Packet) []*goosc.Message {
	switch p := packet.(type) {
	case *goosc.Message:
		return []*goosc.Message{p}
	case *goosc.Bundle:
		var msgs []*goosc.Message
		msgs = append(msgs, p.Messages...)
		for _, nested := range p.Bundles {
			msgs = append(msgs, flatten(nested)...)
		}
		return msgs
	}
	return nil
}

func listenForFeedback(t *testing.T) *net.UDPConn {
	t.Helper()

	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP(reaperHost), Port: reaperFeedbackPort})
	if err != nil {
		t.Fatalf("could not listen on REAPER's feedback port %d (is another OSC client holding it?): %v", reaperFeedbackPort, err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// awaitFeedback waits for REAPER to report address with a true value,
// ignoring the unrelated feedback (time, beat, track state) it streams
// continuously.
func awaitFeedback(t *testing.T, conn *net.UDPConn, address string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	buf := make([]byte, 4096)
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("could not set read deadline: %v", err)
		}
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			t.Fatalf("REAPER never reported %s within %s — it is either not running, not listening on %d, or not sending feedback to %d: %v",
				address, timeout, reaperListenPort, reaperFeedbackPort, err)
		}

		packet, err := goosc.ParsePacket(string(buf[:n]))
		if err != nil {
			continue // not OSC we understand; keep waiting for what we want
		}
		for _, msg := range flatten(packet) {
			if msg.Address != address {
				continue
			}
			if len(msg.Arguments) > 0 {
				if on, ok := msg.Arguments[0].(float32); ok && on == 0 {
					continue // REAPER reporting the state turning off, not on
				}
			}
			return
		}
	}
}
