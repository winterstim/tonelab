package osc_test

import (
	"net"
	"testing"
	"time"

	goosc "github.com/hypebeast/go-osc/osc"
	"tonelab/backend/osc"
)

// TestSend_DeliversAddressedMessage is the one seam this package exists to
// guarantee: Transport.Send puts a correctly addressed OSC message on the
// wire to the configured host:port. Tested against a real local UDP
// listener, not against go-osc internals, so the suite needs no DAW
// running to pass.
func TestSend_DeliversAddressedMessage(t *testing.T) {
	listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("failed to start mock OSC listener: %v", err)
	}
	defer listener.Close()

	port := listener.LocalAddr().(*net.UDPAddr).Port
	transport := osc.NewTransport("127.0.0.1", port)

	if err := transport.Send("/play"); err != nil {
		t.Fatalf("Send returned an error: %v", err)
	}

	if err := listener.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("failed to set read deadline: %v", err)
	}
	buf := make([]byte, 1024)
	n, _, err := listener.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("did not receive a packet within 1s: %v", err)
	}

	packet, err := goosc.ParsePacket(string(buf[:n]))
	if err != nil {
		t.Fatalf("received bytes did not parse as an OSC packet: %v", err)
	}
	msg, ok := packet.(*goosc.Message)
	if !ok {
		t.Fatalf("expected an OSC message, got %T", packet)
	}
	if msg.Address != "/play" {
		t.Fatalf("expected address /play, got %s", msg.Address)
	}
}

// TestSend_UnsupportedArgumentReturnsError verifies Send surfaces a failure
// to the caller rather than swallowing it silently — the caller
// (TransportService) needs this to report a real error back through the UI
// instead of a false "sent" toast. Deliberately network-independent: an
// unsupported argument type fails at message encoding, before any socket
// I/O, so the test can't flake on platform-specific UDP send behavior.
func TestSend_UnsupportedArgumentReturnsError(t *testing.T) {
	transport := osc.NewTransport("127.0.0.1", 9999)

	// go-osc's Message.MarshalBinary only supports bool, nil, int32, int64,
	// float32, float64, string, []byte, and osc.Timetag — a bare struct
	// isn't one of them.
	if err := transport.Send("/play", struct{}{}); err == nil {
		t.Fatal("expected an error sending an unsupported argument type, got nil")
	}
}
