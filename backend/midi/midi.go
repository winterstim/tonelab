// Package midi is a MIDI port for DAWs whose scripting reaches the outside
// only through MIDI. FL Studio's Python cannot open a socket or a file
// (measured: both return NULL), so its bridge speaks system exclusive on a
// port the user assigns once as a controller. On macOS (CoreMIDI) and
// Linux (ALSA sequencer) the port is created here; Windows has no virtual
// port API without a driver, so there the port is one the user made in a
// loopback driver such as loopMIDI, found by name.
package midi

import "errors"

// Link is what a DAW backend needs from a port: bytes out, sysex bodies in.
// A fake in tests stands on the same interface as the real port.
type Link interface {
	// Send writes one complete sysex, framing included.
	Send(message []byte) error
	// Messages yields each complete sysex received, framing included.
	Messages() <-chan []byte
	Close() error
}

var ErrUnsupported = errors.New("midi: virtual ports are not available on this platform yet")

// Start and end of a system exclusive message. Everything between is
// seven-bit, which is why the bridge speaks ASCII.
const (
	SysExStart = 0xF0
	SysExEnd   = 0xF7
)

// assembler reassembles sysex from packets: a long message arrives split,
// and only the whole of it means anything. Anything that is not sysex is
// dropped, since the bridge speaks nothing else.
type assembler struct {
	partial []byte
}

func (a *assembler) feed(bytes []byte) (complete [][]byte) {
	for _, b := range bytes {
		switch {
		case b == SysExStart:
			a.partial = []byte{b}
		case a.partial == nil:
			continue
		case b == SysExEnd:
			complete = append(complete, append(a.partial, b))
			a.partial = nil
		case b >= 0x80:
			// A status byte inside sysex means it was cut off; start over.
			a.partial = nil
		default:
			a.partial = append(a.partial, b)
		}
	}
	return complete
}
