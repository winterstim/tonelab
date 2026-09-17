//go:build linux && cgo

package midi

/*
#cgo LDFLAGS: -lasound
#include <alsa/asoundlib.h>
#include <stdlib.h>

static int tonelabOpen(const char *name, snd_seq_t **seq, int *port) {
	int err = snd_seq_open(seq, "default", SND_SEQ_OPEN_DUPLEX, 0);
	if (err < 0) return err;
	snd_seq_set_client_name(*seq, name);
	*port = snd_seq_create_simple_port(*seq, name,
		SND_SEQ_PORT_CAP_READ | SND_SEQ_PORT_CAP_SUBS_READ | SND_SEQ_PORT_CAP_WRITE | SND_SEQ_PORT_CAP_SUBS_WRITE,
		SND_SEQ_PORT_TYPE_MIDI_GENERIC | SND_SEQ_PORT_TYPE_APPLICATION);
	return *port < 0 ? *port : 0;
}

// For tests: subscribe port b to port a, as a DAW's MIDI settings would.
static int tonelabConnect(snd_seq_t *from, int fromPort, snd_seq_t *to, int toPort) {
	return snd_seq_connect_to(from, fromPort, snd_seq_client_id(to), toPort);
}

static int tonelabSend(snd_seq_t *seq, int port, const unsigned char *bytes, int count) {
	snd_seq_event_t ev;
	snd_seq_ev_clear(&ev);
	snd_seq_ev_set_source(&ev, port);
	snd_seq_ev_set_subs(&ev);
	snd_seq_ev_set_direct(&ev);
	snd_seq_ev_set_sysex(&ev, count, (void *)bytes);
	int err = snd_seq_event_output(seq, &ev);
	if (err < 0) return err;
	return snd_seq_drain_output(seq);
}

// Blocks until an event arrives; sysex is copied into buf and its length
// returned, anything else returns 0.
static int tonelabReceive(snd_seq_t *seq, unsigned char *buf, int cap) {
	snd_seq_event_t *ev = NULL;
	int err = snd_seq_event_input(seq, &ev);
	if (err < 0) return err;
	if (ev->type != SND_SEQ_EVENT_SYSEX) return 0;
	int n = ev->data.ext.len;
	if (n > cap) n = cap;
	memcpy(buf, ev->data.ext.ptr, n);
	return n;
}
*/
import "C"

import (
	"fmt"
	"sync"
	"unsafe"
)

// Port is an ALSA sequencer client with one duplex port, which any DAW's
// MIDI settings can connect to by name.
type Port struct {
	seq  *C.snd_seq_t
	port C.int

	mu     sync.Mutex
	frames assembler
	in     chan []byte
	closed bool
}

func Open(name string) (*Port, error) {
	p := &Port{in: make(chan []byte, 64)}
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	if err := C.tonelabOpen(cname, &p.seq, &p.port); err < 0 {
		return nil, fmt.Errorf("midi: could not create ALSA port %q: %s", name, C.GoString(C.snd_strerror(err)))
	}
	go p.receive()
	return p, nil
}

func (p *Port) receive() {
	buf := make([]byte, 1<<16)
	for {
		n := C.tonelabReceive(p.seq, (*C.uchar)(unsafe.Pointer(&buf[0])), C.int(len(buf)))
		if n < 0 {
			return
		}
		if n == 0 {
			continue
		}
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return
		}
		// ALSA delivers a long sysex as several events, each a slice of
		// it; the assembler joins them like packets anywhere else.
		for _, message := range p.frames.feed(buf[:n]) {
			select {
			case p.in <- message:
			default:
			}
		}
		p.mu.Unlock()
	}
}

func (p *Port) Send(message []byte) error {
	if len(message) == 0 {
		return nil
	}
	if err := C.tonelabSend(p.seq, p.port, (*C.uchar)(unsafe.Pointer(&message[0])), C.int(len(message))); err < 0 {
		return fmt.Errorf("midi: send failed: %s", C.GoString(C.snd_strerror(err)))
	}
	return nil
}

func (p *Port) Messages() <-chan []byte { return p.in }

func (p *Port) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.mu.Unlock()
	C.snd_seq_close(p.seq)
	close(p.in)
	return nil
}

func connect(from, to *Port) error {
	if err := C.tonelabConnect(from.seq, from.port, to.seq, to.port); err < 0 {
		return fmt.Errorf("connect: %s", C.GoString(C.snd_strerror(err)))
	}
	return nil
}
