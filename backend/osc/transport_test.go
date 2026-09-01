package osc_test

import (
	"testing"
	"time"

	"tonelab/backend/osc"
	"tonelab/backend/osc/osctest"
)

// TestSend_DeliversAddressedMessage is the one seam this package exists to
// guarantee: Transport.Send puts a correctly addressed OSC message on the
// wire to the configured host:port. Tested against a real local UDP
// listener, not against go-osc internals, so the suite needs no DAW
// running to pass.
func TestSend_DeliversAddressedMessage(t *testing.T) {
	listener := osctest.NewReceiver(t)
	transport := osc.NewTransport("127.0.0.1", listener.Port)

	if err := transport.Send("/play"); err != nil {
		t.Fatalf("Send returned an error: %v", err)
	}

	listener.ExpectAddress(time.Second, "/play")
}

// TestSend_DeliversArguments covers the other half of the wire format: an
// address alone is enough for a trigger like /play, but every parameter
// command above this layer carries a value, and a value that silently fails
// to arrive would look identical to one that arrived wrong.
func TestSend_DeliversArguments(t *testing.T) {
	listener := osctest.NewReceiver(t)
	transport := osc.NewTransport("127.0.0.1", listener.Port)

	if err := transport.Send("/track/1/volume", float32(0.5)); err != nil {
		t.Fatalf("Send returned an error: %v", err)
	}

	msg := listener.ExpectAddress(time.Second, "/track/1/volume")
	if len(msg.Arguments) != 1 {
		t.Fatalf("expected 1 argument, got %d: %v", len(msg.Arguments), msg.Arguments)
	}
	if got, ok := msg.Arguments[0].(float32); !ok || got != 0.5 {
		t.Fatalf("expected float32 0.5, got %#v", msg.Arguments[0])
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

// TestSend_EncodingFailureSendsNothing pins the failure mode down further:
// a rejected message must not put a partial or malformed packet on the wire,
// since a DAW receiving half a command is worse than receiving none.
func TestSend_EncodingFailureSendsNothing(t *testing.T) {
	listener := osctest.NewReceiver(t)
	transport := osc.NewTransport("127.0.0.1", listener.Port)

	if err := transport.Send("/play", struct{}{}); err == nil {
		t.Fatal("expected an error sending an unsupported argument type, got nil")
	}

	listener.ExpectNothing(100 * time.Millisecond)
}
