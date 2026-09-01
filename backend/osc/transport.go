// Package osc is the OSC transport layer: sending OSC messages over UDP to
// a configured host:port. It knows nothing about REAPER, DAWs, or the agent
// — see backend/daw for the layer that maps domain concepts (tracks, params)
// onto OSC addresses using this transport.
//
// Deliberately not built yet, in rough order of when they will be needed:
// receiving messages (needed to read parameter values back), write timeouts,
// and retry/reconnect for delivery-critical messages. See README.md.
package osc

import (
	"fmt"
	"log"

	goosc "github.com/hypebeast/go-osc/osc"
)

// Transport sends OSC messages to a fixed host:port over UDP.
type Transport struct {
	host string
	port int
}

// NewTransport configures a Transport targeting host:port. It does not dial
// or validate reachability up front — OSC is UDP, so there is no connection
// to establish ahead of time, and an unreachable target is indistinguishable
// from a reachable one until something answers. See Send.
func NewTransport(host string, port int) *Transport {
	return &Transport{host: host, port: port}
}

// Send fires a single OSC message at address, with optional args, and logs
// the attempt and outcome.
//
// UDP delivery is not guaranteed, and a fresh client per call means there is
// no socket alive long enough to see an ICMP port-unreachable either: a nil
// error means the local write succeeded, never that the receiver got it. Any
// command that must not be silently lost needs its own confirmation on top —
// this is the seam that would sit behind.
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
