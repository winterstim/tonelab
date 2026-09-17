//go:build darwin && cgo

package midi

/*
#cgo LDFLAGS: -framework CoreMIDI -framework CoreFoundation
#include <CoreMIDI/CoreMIDI.h>
#include <stdlib.h>
#include <stdint.h>

extern void tonelabMIDIRead(MIDIPacketList *list, void *ref, void *src);

static OSStatus tonelabOpen(const char *name, MIDIClientRef *client, MIDIEndpointRef *source, MIDIEndpointRef *dest, uintptr_t ref) {
	CFStringRef cfname = CFStringCreateWithCString(NULL, name, kCFStringEncodingUTF8);
	OSStatus status = MIDIClientCreate(cfname, NULL, NULL, client);
	if (status == noErr) status = MIDISourceCreate(*client, cfname, source);
	if (status == noErr) status = MIDIDestinationCreate(*client, cfname, (MIDIReadProc)tonelabMIDIRead, (void *)ref, dest);
	CFRelease(cfname);
	return status;
}

static OSStatus tonelabSend(MIDIEndpointRef source, const unsigned char *bytes, int count) {
	// A packet list on the stack holds one packet of up to a few hundred
	// bytes; a sysex is split across as many as it needs.
	unsigned char buffer[1024];
	MIDIPacketList *list = (MIDIPacketList *)buffer;
	int offset = 0;
	while (offset < count) {
		MIDIPacket *packet = MIDIPacketListInit(list);
		int chunk = count - offset;
		if (chunk > 256) chunk = 256;
		packet = MIDIPacketListAdd(list, sizeof(buffer), packet, 0, chunk, bytes + offset);
		if (packet == NULL) return -1;
		OSStatus status = MIDIReceived(source, list);
		if (status != noErr) return status;
		offset += chunk;
	}
	return noErr;
}

extern void tonelabMIDIBytes(uintptr_t ref, unsigned char *bytes, int count);

// The packet walk stays in C: MIDIPacket is a packed, variable-length
// struct that cgo cannot index, and MIDIPacketNext is the one way to step it.
static void tonelabWalk(const MIDIPacketList *list, void *ref) {
	const MIDIPacket *packet = &list->packet[0];
	for (UInt32 i = 0; i < list->numPackets; i++) {
		tonelabMIDIBytes((uintptr_t)ref, (unsigned char *)packet->data, packet->length);
		packet = MIDIPacketNext(packet);
	}
}

// For tests: a second client sends to our destination and listens to our
// source, the way a DAW would.
static OSStatus tonelabLoopSend(MIDIEndpointRef dest, const unsigned char *bytes, int count) {
	MIDIClientRef client; MIDIPortRef out;
	OSStatus status = MIDIClientCreate(CFSTR("Tonelab test"), NULL, NULL, &client);
	if (status != noErr) return status;
	status = MIDIOutputPortCreate(client, CFSTR("out"), &out);
	if (status != noErr) return status;
	unsigned char buffer[1024];
	MIDIPacketList *list = (MIDIPacketList *)buffer;
	int offset = 0;
	while (offset < count && status == noErr) {
		MIDIPacket *packet = MIDIPacketListInit(list);
		int chunk = count - offset; if (chunk > 200) chunk = 200;
		MIDIPacketListAdd(list, sizeof(buffer), packet, 0, chunk, bytes + offset);
		status = MIDISend(out, dest, list);
		offset += chunk;
	}
	MIDIPortDispose(out);
	MIDIClientDispose(client);
	return status;
}

// For tests: listen to our source from a second client and hand what
// arrives to Go, the way a DAW's input would.
extern void tonelabMIDILoopBytes(uintptr_t ref, unsigned char *bytes, int count);
static void tonelabLoopRead(const MIDIPacketList *list, void *ref, void *src) {
	const MIDIPacket *packet = &list->packet[0];
	for (UInt32 i = 0; i < list->numPackets; i++) {
		tonelabMIDILoopBytes((uintptr_t)ref, (unsigned char *)packet->data, packet->length);
		packet = MIDIPacketNext(packet);
	}
}
static OSStatus tonelabLoopListen(MIDIEndpointRef source, uintptr_t ref, MIDIClientRef *client, MIDIPortRef *in) {
	OSStatus status = MIDIClientCreate(CFSTR("Tonelab test in"), NULL, NULL, client);
	if (status != noErr) return status;
	status = MIDIInputPortCreate(*client, CFSTR("in"), tonelabLoopRead, (void *)ref, in);
	if (status != noErr) return status;
	return MIDIPortConnectSource(*in, source, NULL);
}

static void tonelabClose(MIDIClientRef client, MIDIEndpointRef source, MIDIEndpointRef dest) {
	MIDIEndpointDispose(dest);
	MIDIEndpointDispose(source);
	MIDIClientDispose(client);
}
*/
import "C"

import (
	"fmt"
	"runtime/cgo"
	"sync"
	"unsafe"
)

// Port is a pair of virtual endpoints with one name: the DAW sees an input
// it can assign a controller script to and an output for the script to
// answer on.
type Port struct {
	client C.MIDIClientRef
	source C.MIDIEndpointRef
	dest   C.MIDIEndpointRef
	handle cgo.Handle

	mu     sync.Mutex
	frames assembler
	in     chan []byte
	closed bool
}

func Open(name string) (*Port, error) {
	p := &Port{in: make(chan []byte, 64)}
	p.handle = cgo.NewHandle(p)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	if status := C.tonelabOpen(cname, &p.client, &p.source, &p.dest, C.uintptr_t(p.handle)); status != C.noErr {
		p.handle.Delete()
		return nil, fmt.Errorf("midi: could not create virtual port %q: status %d", name, int(status))
	}
	return p, nil
}

func (p *Port) Send(message []byte) error {
	if len(message) == 0 {
		return nil
	}
	if status := C.tonelabSend(p.source, (*C.uchar)(unsafe.Pointer(&message[0])), C.int(len(message))); status != C.noErr {
		return fmt.Errorf("midi: send failed: status %d", int(status))
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
	C.tonelabClose(p.client, p.source, p.dest)
	p.handle.Delete()
	close(p.in)
	return nil
}

func (p *Port) absorb(bytes []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	for _, message := range p.frames.feed(bytes) {
		select {
		case p.in <- message:
		default:
		}
	}
}

//export tonelabMIDIRead
func tonelabMIDIRead(list *C.MIDIPacketList, ref unsafe.Pointer, _ unsafe.Pointer) {
	C.tonelabWalk(list, ref)
}

//export tonelabMIDIBytes
func tonelabMIDIBytes(ref C.uintptr_t, bytes *C.uchar, count C.int) {
	p := cgo.Handle(ref).Value().(*Port)
	p.absorb(C.GoBytes(unsafe.Pointer(bytes), count))
}

// loopSend delivers bytes to this port's destination from a separate MIDI
// client, so a test exercises the real CoreMIDI path.
func (p *Port) loopSend(bytes []byte) error {
	if status := C.tonelabLoopSend(p.dest, (*C.uchar)(unsafe.Pointer(&bytes[0])), C.int(len(bytes))); status != C.noErr {
		return fmt.Errorf("status %d", int(status))
	}
	return nil
}

// loopListener receives what a DAW would from this port's source.
type loopListener struct {
	frames assembler
	got    chan []byte
	client C.MIDIClientRef
	in     C.MIDIPortRef
	handle cgo.Handle
}

func (p *Port) loopListen() (*loopListener, error) {
	l := &loopListener{got: make(chan []byte, 8)}
	l.handle = cgo.NewHandle(l)
	if status := C.tonelabLoopListen(p.source, C.uintptr_t(l.handle), &l.client, &l.in); status != C.noErr {
		return nil, fmt.Errorf("status %d", int(status))
	}
	return l, nil
}

//export tonelabMIDILoopBytes
func tonelabMIDILoopBytes(ref C.uintptr_t, bytes *C.uchar, count C.int) {
	l := cgo.Handle(ref).Value().(*loopListener)
	for _, m := range l.frames.feed(C.GoBytes(unsafe.Pointer(bytes), count)) {
		select {
		case l.got <- m:
		default:
		}
	}
}
