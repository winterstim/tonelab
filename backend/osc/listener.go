package osc

import (
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"

	goosc "github.com/hypebeast/go-osc/osc"
)

// Buffered so a caller that pauses briefly loses nothing.
const streamCapacity = 256

// Listener is the read half of the transport. Sending is fire-and-forget, so
// reading a value back or confirming a command landed is only possible here.
type Listener struct {
	conn     *net.UDPConn
	messages chan *goosc.Message
	dropped  atomic.Uint64

	closeOnce sync.Once
}

// Listen binds immediately so a port held by another OSC client fails at
// startup rather than looking like an unresponsive DAW later. Port 0 lets the
// OS choose, which Port reports.
func Listen(host string, port int) (*Listener, error) {
	addr := &net.UDPAddr{IP: net.ParseIP(host), Port: port}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("osc: listen on %s:%d: %w", host, port, err)
	}

	listener := &Listener{
		conn:     conn,
		messages: make(chan *goosc.Message, streamCapacity),
	}
	go listener.receive()

	log.Printf("[osc] listening on %s", conn.LocalAddr())
	return listener, nil
}

// Port matters when Listen was asked for 0, since the DAW must be told where
// to send.
func (l *Listener) Port() int {
	return l.conn.LocalAddr().(*net.UDPAddr).Port
}

// Messages closes when the Listener does, so callers block on a live stream
// or learn it ended, never both.
func (l *Listener) Messages() <-chan *goosc.Message {
	return l.messages
}

// Dropped lets a caller missing an expected message tell "never arrived" from
// "I was too slow to take it".
func (l *Listener) Dropped() uint64 {
	return l.dropped.Load()
}

// Close is idempotent because closing on one path while deferring a close on
// another is normal rather than a mistake.
func (l *Listener) Close() error {
	err := error(nil)
	l.closeOnce.Do(func() {
		err = l.conn.Close()
	})
	return err
}

// receive drops rather than blocks when the caller stalls. DAW feedback is
// mostly position updates, a stale one is already superseded, and a blocked
// read loop would lose everything instead of the stale part. One bad packet
// never ends the stream either, since a UDP port accepts whatever is sent.
func (l *Listener) receive() {
	defer close(l.messages)

	buf := make([]byte, 65535)
	for {
		n, _, err := l.conn.ReadFromUDP(buf)
		if err != nil {
			return // socket closed; this is how Close ends the loop
		}

		packet, err := goosc.ParsePacket(string(buf[:n]))
		if err != nil {
			log.Printf("[osc] <- discarded %d bytes that are not OSC: %v", n, err)
			continue
		}

		for _, msg := range Flatten(packet) {
			select {
			case l.messages <- msg:
			default:
				l.dropped.Add(1)
			}
		}
	}
}

// Flatten exists because DAWs commonly wrap feedback in bundles (REAPER
// does), so a path handling only messages sees nothing at all. Exported so
// test helpers apply the same rule as production.
func Flatten(packet goosc.Packet) []*goosc.Message {
	switch p := packet.(type) {
	case *goosc.Message:
		return []*goosc.Message{p}
	case *goosc.Bundle:
		msgs := append([]*goosc.Message{}, p.Messages...)
		for _, nested := range p.Bundles {
			msgs = append(msgs, Flatten(nested)...)
		}
		return msgs
	}
	return nil
}
