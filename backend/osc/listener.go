package osc

import (
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"

	goosc "github.com/hypebeast/go-osc/osc"
)

// streamCapacity is how many messages may wait for a consumer. A DAW streams
// position and meter updates continuously, so the stream is a firehose of
// mostly-uninteresting traffic with the occasional message a caller wants.
// Buffering absorbs a caller that pauses briefly; beyond that, dropping is
// the right failure — feedback describes current state, so a message that has
// waited too long has already been superseded.
const streamCapacity = 256

// Listener receives OSC on a local port and hands the messages to a caller.
// It is the read half of the transport: sending is fire-and-forget, but
// reading a value back, or knowing a command landed, requires this.
//
// A DAW must be configured to send here; nothing about sending sets up a
// return path. What arrives, and whether anything arrives at all, is the
// DAW's decision — see backend/daw for what a given DAW actually reports.
type Listener struct {
	conn     *net.UDPConn
	messages chan *goosc.Message
	dropped  atomic.Uint64

	closeOnce sync.Once
}

// Listen binds a UDP port and starts receiving. A port of 0 asks the OS to
// choose one, which Port reports. Binding fails immediately rather than at
// first read, so a port already held by another OSC client is a startup
// error rather than a silence that looks like an unresponsive DAW.
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

// Port reports the port actually bound, which matters when Listen was asked
// for 0 — a DAW has to be told where to send.
func (l *Listener) Port() int {
	return l.conn.LocalAddr().(*net.UDPAddr).Port
}

// Messages is the stream of everything received, with bundles unwrapped into
// the messages they carry. It closes when the Listener does.
func (l *Listener) Messages() <-chan *goosc.Message {
	return l.messages
}

// Dropped counts messages discarded because the caller was not reading fast
// enough. Nonzero is not necessarily a fault — most DAW feedback is position
// updates nobody asked for — but a caller missing an expected message should
// be able to tell "it never arrived" from "I was too slow to take it".
func (l *Listener) Dropped() uint64 {
	return l.dropped.Load()
}

// Close stops receiving and closes the message stream. Safe to call twice,
// since a caller closing on one path and deferring a close on another is the
// normal shape rather than a mistake.
func (l *Listener) Close() error {
	err := error(nil)
	l.closeOnce.Do(func() {
		err = l.conn.Close()
	})
	return err
}

// receive reads until the socket closes. It never lets one bad packet end the
// stream: a UDP port accepts whatever is sent to it, and a DAW is not the
// only thing that might.
func (l *Listener) receive() {
	defer close(l.messages)

	buf := make([]byte, 65535) // one UDP datagram's worth
	for {
		n, _, err := l.conn.ReadFromUDP(buf)
		if err != nil {
			return // the socket is closed; this is how Close ends the loop
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

// Flatten unwraps a packet into the messages it carries. DAWs commonly send
// feedback as OSC bundles rather than bare messages (REAPER does), so a
// receive path that only handles messages silently sees nothing at all.
// Exported because test helpers reading a DAW's feedback need the same rule.
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
