//go:build windows

package midi

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows has no virtual MIDI port API without a driver, so Open looks for
// an existing input and output pair carrying the name, which a loopback
// driver such as loopMIDI provides. The user creates the port there once;
// its name is what ties the two ends together.
var ErrNoPort = errors.New("midi: no MIDI port with that name; create one named Tonelab in loopMIDI (free) and start Tonelab again")

var (
	winmm              = windows.NewLazySystemDLL("winmm.dll")
	midiInGetNumDevs   = winmm.NewProc("midiInGetNumDevs")
	midiInGetDevCapsW  = winmm.NewProc("midiInGetDevCapsW")
	midiInOpen         = winmm.NewProc("midiInOpen")
	midiInClose        = winmm.NewProc("midiInClose")
	midiInStart        = winmm.NewProc("midiInStart")
	midiInStop         = winmm.NewProc("midiInStop")
	midiInReset        = winmm.NewProc("midiInReset")
	midiInPrepare      = winmm.NewProc("midiInPrepareHeader")
	midiInUnprepare    = winmm.NewProc("midiInUnprepareHeader")
	midiInAddBuffer    = winmm.NewProc("midiInAddBuffer")
	midiOutGetNumDevs  = winmm.NewProc("midiOutGetNumDevs")
	midiOutGetDevCapsW = winmm.NewProc("midiOutGetDevCapsW")
	midiOutOpen        = winmm.NewProc("midiOutOpen")
	midiOutClose       = winmm.NewProc("midiOutClose")
	midiOutPrepare     = winmm.NewProc("midiOutPrepareHeader")
	midiOutUnprepare   = winmm.NewProc("midiOutUnprepareHeader")
	midiOutLongMsg     = winmm.NewProc("midiOutLongMsg")
)

const (
	callbackFunction = 0x30000
	mimLongData      = 0x3C4
	mhdrDone         = 0x1
	inBufferSize     = 4096
	inBufferCount    = 4
)

// midiHdr mirrors MIDIHDR; WinMM fills and returns it whole.
type midiHdr struct {
	data          *byte
	bufferLength  uint32
	bytesRecorded uint32
	user          uintptr
	flags         uint32
	next          uintptr
	reserved      uintptr
	offset        uint32
	reserved2     [8]uintptr
}

// midiInCaps mirrors MIDIINCAPSW; the name is what matters.
type midiInCaps struct {
	mid     uint16
	pid     uint16
	version uint32
	name    [32]uint16
	support uint32
}

type midiOutCaps struct {
	mid         uint16
	pid         uint16
	version     uint32
	name        [32]uint16
	technology  uint16
	voices      uint16
	notes       uint16
	channelMask uint16
	support     uint32
}

type Port struct {
	in  uintptr
	out uintptr

	mu      sync.Mutex
	frames  assembler
	buffers [inBufferCount]*midiHdr
	backing [inBufferCount][]byte
	recycle chan *midiHdr
	inbox   chan []byte
	closed  bool
}

var (
	portsMu sync.Mutex
	ports   = map[uintptr]*Port{}
	// One callback for every port: WinMM calls it on its own thread with
	// the handle, which maps back to the port.
	inProc = windows.NewCallback(func(handle, msg, instance, param1, param2 uintptr) uintptr {
		if msg != mimLongData {
			return 0
		}
		portsMu.Lock()
		p := ports[handle]
		portsMu.Unlock()
		if p == nil {
			return 0
		}
		// param1 is one of our own headers; matched rather than cast, so
		// the memory is always something Go allocated and still holds.
		var header *midiHdr
		var backing []byte
		for i, candidate := range p.buffers {
			if uintptr(unsafe.Pointer(candidate)) == param1 {
				header, backing = candidate, p.backing[i]
			}
		}
		if header == nil {
			return 0
		}
		p.absorb(backing[:header.bytesRecorded])
		// Buffers may not be re-added from the callback; a goroutine does.
		select {
		case p.recycle <- header:
		default:
		}
		return 0
	})
)

func deviceNamed(name string, count func() int, caps func(int) string) (int, bool) {
	for i := 0; i < count(); i++ {
		if strings.EqualFold(strings.TrimSpace(caps(i)), name) {
			return i, true
		}
	}
	return 0, false
}

func Open(name string) (*Port, error) {
	inputs := func() int { n, _, _ := midiInGetNumDevs.Call(); return int(n) }
	inputName := func(i int) string {
		var c midiInCaps
		midiInGetDevCapsW.Call(uintptr(i), uintptr(unsafe.Pointer(&c)), unsafe.Sizeof(c))
		return windows.UTF16ToString(c.name[:])
	}
	outputs := func() int { n, _, _ := midiOutGetNumDevs.Call(); return int(n) }
	outputName := func(i int) string {
		var c midiOutCaps
		midiOutGetDevCapsW.Call(uintptr(i), uintptr(unsafe.Pointer(&c)), unsafe.Sizeof(c))
		return windows.UTF16ToString(c.name[:])
	}
	inIndex, ok := deviceNamed(name, inputs, inputName)
	if !ok {
		return nil, ErrNoPort
	}
	outIndex, ok := deviceNamed(name, outputs, outputName)
	if !ok {
		return nil, ErrNoPort
	}

	p := &Port{recycle: make(chan *midiHdr, inBufferCount), inbox: make(chan []byte, 64)}
	if r, _, _ := midiInOpen.Call(uintptr(unsafe.Pointer(&p.in)), uintptr(inIndex), inProc, 0, callbackFunction); r != 0 {
		return nil, fmt.Errorf("midi: midiInOpen failed: %d", r)
	}
	if r, _, _ := midiOutOpen.Call(uintptr(unsafe.Pointer(&p.out)), uintptr(outIndex), 0, 0, 0); r != 0 {
		midiInClose.Call(p.in)
		return nil, fmt.Errorf("midi: midiOutOpen failed: %d", r)
	}
	portsMu.Lock()
	ports[p.in] = p
	portsMu.Unlock()

	// Sysex arrives only into buffers the application supplied; several
	// are queued so a long message spanning them is not cut off.
	for i := range p.buffers {
		p.backing[i] = make([]byte, inBufferSize)
		p.buffers[i] = &midiHdr{data: &p.backing[i][0], bufferLength: inBufferSize}
		midiInPrepare.Call(p.in, uintptr(unsafe.Pointer(p.buffers[i])), unsafe.Sizeof(*p.buffers[i]))
		midiInAddBuffer.Call(p.in, uintptr(unsafe.Pointer(p.buffers[i])), unsafe.Sizeof(*p.buffers[i]))
	}
	go p.requeue()
	midiInStart.Call(p.in)
	return p, nil
}

func (p *Port) requeue() {
	for header := range p.recycle {
		p.mu.Lock()
		closed := p.closed
		p.mu.Unlock()
		if closed {
			return
		}
		header.bytesRecorded = 0
		header.flags &^= mhdrDone
		midiInAddBuffer.Call(p.in, uintptr(unsafe.Pointer(header)), unsafe.Sizeof(*header))
	}
}

func (p *Port) absorb(bytes []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	for _, message := range p.frames.feed(bytes) {
		select {
		case p.inbox <- message:
		default:
		}
	}
}

func (p *Port) Send(message []byte) error {
	if len(message) == 0 {
		return nil
	}
	header := &midiHdr{data: &message[0], bufferLength: uint32(len(message)), bytesRecorded: uint32(len(message))}
	size := unsafe.Sizeof(*header)
	if r, _, _ := midiOutPrepare.Call(p.out, uintptr(unsafe.Pointer(header)), size); r != 0 {
		return fmt.Errorf("midi: prepare failed: %d", r)
	}
	defer midiOutUnprepare.Call(p.out, uintptr(unsafe.Pointer(header)), size)
	if r, _, _ := midiOutLongMsg.Call(p.out, uintptr(unsafe.Pointer(header)), size); r != 0 {
		return fmt.Errorf("midi: send failed: %d", r)
	}
	// midiOutLongMsg returns before the driver is done; the header must
	// stay prepared until it is.
	for header.flags&mhdrDone == 0 {
		windows.SleepEx(1, false)
	}
	return nil
}

func (p *Port) Messages() <-chan []byte { return p.inbox }

func (p *Port) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.mu.Unlock()
	midiInStop.Call(p.in)
	midiInReset.Call(p.in)
	for _, header := range p.buffers {
		midiInUnprepare.Call(p.in, uintptr(unsafe.Pointer(header)), unsafe.Sizeof(*header))
	}
	midiInClose.Call(p.in)
	midiOutClose.Call(p.out)
	portsMu.Lock()
	delete(ports, p.in)
	portsMu.Unlock()
	close(p.recycle)
	close(p.inbox)
	return nil
}
