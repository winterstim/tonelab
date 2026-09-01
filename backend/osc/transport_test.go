package osc_test

import (
	"testing"
	"time"

	"tonelab/backend/osc"
	"tonelab/backend/osc/osctest"
)

// The one guarantee this package makes. Asserted against a real UDP socket
// rather than go-osc internals, so no DAW is needed to run it.
func TestSend_DeliversAddressedMessage(t *testing.T) {
	listener := osctest.NewReceiver(t)
	transport := osc.NewTransport("127.0.0.1", listener.Port)

	if err := transport.Send("/play"); err != nil {
		t.Fatalf("Send returned an error: %v", err)
	}

	listener.ExpectAddress(time.Second, "/play")
}

// A value that silently fails to arrive looks identical to one that arrived
// wrong, and every parameter command above this layer carries one.
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

// A swallowed failure would surface as a false "sent" in the UI. Uses an
// encoding failure so the test cannot flake on platform UDP behaviour.
func TestSend_UnsupportedArgumentReturnsError(t *testing.T) {
	transport := osc.NewTransport("127.0.0.1", 9999)

	// go-osc's Message.MarshalBinary only supports bool, nil, int32, int64,
	// float32, float64, string, []byte, and osc.Timetag, and a bare struct
	// isn't one of them.
	if err := transport.Send("/play", struct{}{}); err == nil {
		t.Fatal("expected an error sending an unsupported argument type, got nil")
	}
}

// A DAW receiving half a command is worse than receiving none.
func TestSend_EncodingFailureSendsNothing(t *testing.T) {
	listener := osctest.NewReceiver(t)
	transport := osc.NewTransport("127.0.0.1", listener.Port)

	if err := transport.Send("/play", struct{}{}); err == nil {
		t.Fatal("expected an error sending an unsupported argument type, got nil")
	}

	listener.ExpectNothing(100 * time.Millisecond)
}
