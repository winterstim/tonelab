package osctest

import (
	"fmt"
	"net"
	"testing"
	"time"

	goosc "github.com/hypebeast/go-osc/osc"

	"tonelab/backend/osc"
)

// Feedback listens on a fixed port for the messages a DAW sends back, which
// is what separates "we wrote to a socket" from "the DAW acted on it". Unlike
// Receiver it takes the port rather than choosing one, because the DAW has to
// be configured to send there.
type Feedback struct {
	t    *testing.T
	conn *net.UDPConn
	port int
}

// NewFeedback starts listening on port, failing the test if the port is
// already held — usually another OSC client, or a leftover test process.
func NewFeedback(t *testing.T, port int) *Feedback {
	t.Helper()

	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port})
	if err != nil {
		t.Fatalf("osctest: could not listen for feedback on port %d (is something else holding it?): %v", port, err)
	}
	t.Cleanup(func() { conn.Close() })

	return &Feedback{t: t, conn: conn, port: port}
}

// Await waits for the first message at address whose arguments satisfy match,
// ignoring the unrelated feedback a DAW streams continuously (time, playhead
// position, other tracks). A nil match accepts any message at that address.
func (f *Feedback) Await(address string, timeout time.Duration, match func(*goosc.Message) bool) *goosc.Message {
	f.t.Helper()

	return f.AwaitAny(address, timeout, func(msg *goosc.Message) bool {
		return msg.Address == address && (match == nil || match(msg))
	})
}

// AwaitAny waits for the first message satisfying match, whatever its
// address — for feedback whose address is not known in advance, such as a
// DAW announcing which track it just created. want describes what is being
// waited for, and appears in the failure message.
func (f *Feedback) AwaitAny(want string, timeout time.Duration, match func(*goosc.Message) bool) *goosc.Message {
	f.t.Helper()

	deadline := time.Now().Add(timeout)
	buf := make([]byte, 8192)
	for {
		if err := f.conn.SetReadDeadline(deadline); err != nil {
			f.t.Fatalf("osctest: could not set read deadline: %v", err)
		}
		n, _, err := f.conn.ReadFromUDP(buf)
		if err != nil {
			f.t.Fatalf("osctest: nothing reported %s within %s — the DAW is either not running, not listening, or not configured to send feedback to port %d: %v",
				want, timeout, f.port, err)
		}

		packet, err := goosc.ParsePacket(string(buf[:n]))
		if err != nil {
			continue // not OSC we understand; keep waiting for what we want
		}
		for _, msg := range Flatten(packet) {
			if match(msg) {
				return msg
			}
		}
	}
}

// AwaitValue waits for address to report the given numeric value. DAWs report
// their own idea of a parameter after setting it, and that value has been
// through their internal representation and back, so exact float equality is
// the wrong test — tolerance is.
func (f *Feedback) AwaitValue(address string, want float64, tolerance float64, timeout time.Duration) {
	f.t.Helper()

	f.Await(address, timeout, func(msg *goosc.Message) bool {
		got, err := numericArgument(msg)
		return err == nil && got >= want-tolerance && got <= want+tolerance
	})
}

func numericArgument(msg *goosc.Message) (float64, error) {
	if len(msg.Arguments) == 0 {
		return 0, fmt.Errorf("osctest: %s carried no arguments", msg.Address)
	}
	switch v := msg.Arguments[0].(type) {
	case float32:
		return float64(v), nil
	case float64:
		return v, nil
	case int32:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case bool:
		if v {
			return 1, nil
		}
		return 0, nil
	}
	return 0, fmt.Errorf("osctest: %s carried a non-numeric argument %#v", msg.Address, msg.Arguments[0])
}

// Flatten unwraps a packet into the messages it carries, by the same rule the
// real receive path uses — tests that accepted bundles differently from
// production would be testing the wrong thing.
func Flatten(packet goosc.Packet) []*goosc.Message {
	return osc.Flatten(packet)
}
