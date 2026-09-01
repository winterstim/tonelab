// Package osctest stands in for a DAW so every layer above the transport can
// assert on exact OSC traffic with no DAW installed, running, or configured.
// A real socket rather than a mock, so encoding bugs still surface.
package osctest

import (
	"net"
	"testing"
	"time"

	goosc "github.com/hypebeast/go-osc/osc"
)

// Receiver records what arrives, for asserting on what a command sent.
type Receiver struct {
	t    *testing.T
	conn *net.UDPConn
	Port int
}

// NewReceiver takes an OS-assigned port so parallel tests never collide.
func NewReceiver(t *testing.T) *Receiver {
	t.Helper()

	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("osctest: could not start receiver: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	return &Receiver{t: t, conn: conn, Port: conn.LocalAddr().(*net.UDPAddr).Port}
}

// Expect relies on loopback UDP not reordering or dropping in practice, so
// one message sent is one message read.
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

// ExpectAddress returns the message so callers can go on to check arguments.
func (r *Receiver) ExpectAddress(timeout time.Duration, address string) *goosc.Message {
	r.t.Helper()

	msg := r.Expect(timeout)
	if msg.Address != address {
		r.t.Fatalf("osctest: expected address %s, got %s", address, msg.Address)
	}
	return msg
}

// ExpectNothing covers commands that must reach the wire not at all, such as
// one rejected by validation.
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
