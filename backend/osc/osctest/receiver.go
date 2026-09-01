// Package osctest provides a fake OSC receiver for tests: a real UDP socket
// on localhost that records the messages sent to it. It stands in for a DAW
// so tests covering anything above the transport (backend/daw, and the agent
// tools above that) can assert on the exact OSC traffic a command produces
// without a DAW installed, running, or configured.
package osctest

import (
	"net"
	"testing"
	"time"

	goosc "github.com/hypebeast/go-osc/osc"
)

// Receiver is a UDP listener that parses whatever arrives as OSC.
type Receiver struct {
	t    *testing.T
	conn *net.UDPConn
	Port int
}

// NewReceiver starts a receiver on an OS-assigned localhost port and closes
// it when the test ends.
func NewReceiver(t *testing.T) *Receiver {
	t.Helper()

	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("osctest: could not start receiver: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	return &Receiver{t: t, conn: conn, Port: conn.LocalAddr().(*net.UDPAddr).Port}
}

// Expect waits up to timeout for one message and returns it, failing the test
// if nothing arrives or what arrives is not a single OSC message. UDP on
// loopback does not reorder or drop in practice, so one message in means one
// message out here.
func (r *Receiver) Expect(timeout time.Duration) *goosc.Message {
	r.t.Helper()

	if err := r.conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		r.t.Fatalf("osctest: could not set read deadline: %v", err)
	}

	buf := make([]byte, 4096)
	n, _, err := r.conn.ReadFromUDP(buf)
	if err != nil {
		r.t.Fatalf("osctest: no OSC message arrived within %s: %v", timeout, err)
	}

	packet, err := goosc.ParsePacket(string(buf[:n]))
	if err != nil {
		r.t.Fatalf("osctest: received %d bytes that are not valid OSC: %v", n, err)
	}
	msg, ok := packet.(*goosc.Message)
	if !ok {
		r.t.Fatalf("osctest: expected an OSC message, got %T", packet)
	}
	return msg
}

// ExpectAddress waits for one message and asserts its address, returning it
// for any further assertions on its arguments.
func (r *Receiver) ExpectAddress(timeout time.Duration, address string) *goosc.Message {
	r.t.Helper()

	msg := r.Expect(timeout)
	if msg.Address != address {
		r.t.Fatalf("osctest: expected address %s, got %s", address, msg.Address)
	}
	return msg
}

// ExpectNothing asserts no message arrives within timeout — for commands that
// must not touch the wire at all (a validation failure, say).
func (r *Receiver) ExpectNothing(timeout time.Duration) {
	r.t.Helper()

	if err := r.conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		r.t.Fatalf("osctest: could not set read deadline: %v", err)
	}
	buf := make([]byte, 4096)
	if n, _, err := r.conn.ReadFromUDP(buf); err == nil {
		r.t.Fatalf("osctest: expected no OSC traffic, got %d bytes", n)
	}
}
