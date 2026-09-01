package osc_test

import (
	"net"
	"strconv"
	"testing"
	"time"

	goosc "github.com/hypebeast/go-osc/osc"
	"tonelab/backend/osc"
)

// Bypasses Transport so these tests cover the receive path alone.
func send(t *testing.T, port int, packet goosc.Packet) {
	t.Helper()

	data, err := packet.MarshalBinary()
	if err != nil {
		t.Fatalf("could not encode the packet under test: %v", err)
	}
	conn, err := net.Dial("udp", net.JoinHostPort("127.0.0.1", portString(port)))
	if err != nil {
		t.Fatalf("could not dial the listener: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write(data); err != nil {
		t.Fatalf("could not write to the listener: %v", err)
	}
}

func portString(port int) string {
	return strconv.Itoa(port)
}

// Without this there is no get_param at all, only commands fired into the
// dark.
func TestListenDeliversMessages(t *testing.T) {
	listener := mustListen(t)

	send(t, listener.Port(), goosc.NewMessage("/track/1/volume", float32(0.5)))

	msg := receive(t, listener, time.Second)
	if msg.Address != "/track/1/volume" {
		t.Fatalf("expected /track/1/volume, got %s", msg.Address)
	}
}

// REAPER wraps feedback in bundles, and a path handling only bare messages
// sees nothing at all. This already cost one wrong implementation.
func TestListenFlattensBundles(t *testing.T) {
	listener := mustListen(t)

	bundle := goosc.NewBundle(time.Now())
	bundle.Append(goosc.NewMessage("/track/1/mute", float32(1)))
	bundle.Append(goosc.NewMessage("/track/1/solo", float32(0)))
	send(t, listener.Port(), bundle)

	first := receive(t, listener, time.Second)
	second := receive(t, listener, time.Second)
	if first.Address != "/track/1/mute" || second.Address != "/track/1/solo" {
		t.Fatalf("expected the bundle's two messages in order, got %s then %s", first.Address, second.Address)
	}
}

// A DAW is not the only thing that can send to a UDP port.
func TestListenSurvivesGarbage(t *testing.T) {
	listener := mustListen(t)

	conn, err := net.Dial("udp", net.JoinHostPort("127.0.0.1", portString(listener.Port())))
	if err != nil {
		t.Fatalf("could not dial the listener: %v", err)
	}
	if _, err := conn.Write([]byte("this is not OSC")); err != nil {
		t.Fatalf("could not write garbage: %v", err)
	}
	conn.Close()

	send(t, listener.Port(), goosc.NewMessage("/play", float32(1)))

	if msg := receive(t, listener, time.Second); msg.Address != "/play" {
		t.Fatalf("expected the stream to continue with /play, got %s", msg.Address)
	}
}

// Otherwise a caller blocks forever on a channel nothing will write to.
func TestCloseEndsTheStream(t *testing.T) {
	listener := mustListen(t)

	if err := listener.Close(); err != nil {
		t.Fatalf("Close returned an error: %v", err)
	}

	select {
	case _, open := <-listener.Messages():
		if open {
			t.Fatal("expected the stream to be closed")
		}
	case <-time.After(time.Second):
		t.Fatal("the stream did not close within a second")
	}
}

// A DAW streams position updates continuously, so a stalled caller must cost
// dropped feedback rather than a stalled receive loop that loses everything.
func TestSlowConsumerDoesNotBlockTheSocket(t *testing.T) {
	listener := mustListen(t)

	for i := 0; i < 500; i++ {
		send(t, listener.Port(), goosc.NewMessage("/time", float32(i)))
	}

	// Nothing has been read yet. The listener must still be alive and
	// delivering, having dropped what it could not hold.
	send(t, listener.Port(), goosc.NewMessage("/play", float32(1)))

	deadline := time.After(2 * time.Second)
	for {
		select {
		case msg := <-listener.Messages():
			if msg.Address == "/play" {
				if listener.Dropped() == 0 {
					t.Error("expected the listener to report dropped messages")
				}
				return
			}
		case <-deadline:
			t.Fatal("the listener stopped delivering after a slow consumer")
		}
	}
}

func mustListen(t *testing.T) *osc.Listener {
	t.Helper()

	listener, err := osc.Listen("127.0.0.1", 0)
	if err != nil {
		t.Fatalf("Listen returned an error: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	return listener
}

func receive(t *testing.T, listener *osc.Listener, timeout time.Duration) *goosc.Message {
	t.Helper()

	select {
	case msg, open := <-listener.Messages():
		if !open {
			t.Fatal("the message stream closed unexpectedly")
		}
		return msg
	case <-time.After(timeout):
		t.Fatalf("no message arrived within %s", timeout)
		return nil
	}
}
