// Package osc carries OSC over UDP. It stays free of DAW concepts so that
// backend/daw can own address mapping and a second DAW costs no changes here.
//
// Not built yet: write timeouts (go-osc owns its socket, so a deadline means
// holding our own net.Conn) and delivery confirmation.
package osc

import (
	"fmt"
	"log"

	goosc "github.com/hypebeast/go-osc/osc"
)

// Transport sends to a fixed host:port.
type Transport struct {
	host string
	port int
}

// NewTransport does not dial. UDP has no connection to establish, and an
// unreachable target is indistinguishable from a reachable one until
// something answers, so failing early here would be a false signal.
func NewTransport(host string, port int) *Transport {
	return &Transport{host: host, port: port}
}

// Send reports only that the local write succeeded. UDP gives no delivery
// guarantee, and a fresh client per call keeps no socket alive long enough to
// see an ICMP rejection, so callers needing certainty must confirm through
// the DAW's own feedback (see Listener) rather than trusting a nil error.
func (t *Transport) Send(address string, args ...any) error {
	log.Printf("[osc] -> %s:%d %s %v", t.host, t.port, address, args)

	client := goosc.NewClient(t.host, t.port)
	if err := client.Send(goosc.NewMessage(address, args...)); err != nil {
		log.Printf("[osc] send %s FAILED: %v", address, err)
		return fmt.Errorf("osc: send %s to %s:%d: %w", address, t.host, t.port, err)
	}

	log.Printf("[osc] send %s: local write ok (no delivery confirmation)", address)
	return nil
}
