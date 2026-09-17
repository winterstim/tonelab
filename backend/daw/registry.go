package daw

import (
	"fmt"
	"sort"

	"tonelab/backend/midi"
)

// Transport is how a backend reaches its DAW. Application startup opens
// the matching one without knowing which DAW asked for it.
type Transport string

const (
	OSC  Transport = "osc"
	MIDI Transport = "midi"
)

type backend struct {
	transport Transport
	osc       func(Sender) Client
	midi      func(midi.Link) Client
}

// backends keeps DAW names out of application startup: registering one here
// is all it takes to make a backend selectable.
var backends = map[string]backend{
	"reaper":   {transport: OSC, osc: func(sender Sender) Client { return NewREAPER(sender) }},
	"ableton":  {transport: OSC, osc: func(sender Sender) Client { return NewAbleton(sender) }},
	"flstudio": {transport: MIDI, midi: func(link midi.Link) Client { return NewFLStudio(link) }},
}

// TransportOf says what New needs for a name, so the caller can open it.
func TransportOf(name string) (Transport, error) {
	entry, ok := backends[name]
	if !ok {
		return "", fmt.Errorf("daw: no backend named %q (available: %v)", name, Backends())
	}
	return entry.transport, nil
}

// New takes a name so the choice of DAW is configuration rather than a
// decision compiled into the caller.
func New(name string, sender Sender) (Client, error) {
	entry, ok := backends[name]
	if !ok {
		return nil, fmt.Errorf("daw: no backend named %q (available: %v)", name, Backends())
	}
	if entry.transport != OSC {
		return nil, fmt.Errorf("daw: %s speaks %s, not OSC", name, entry.transport)
	}
	return entry.osc(sender), nil
}

// NewMIDI is New for the backends that reach their DAW over a MIDI port.
func NewMIDI(name string, link midi.Link) (Client, error) {
	entry, ok := backends[name]
	if !ok {
		return nil, fmt.Errorf("daw: no backend named %q (available: %v)", name, Backends())
	}
	if entry.transport != MIDI {
		return nil, fmt.Errorf("daw: %s speaks %s, not MIDI", name, entry.transport)
	}
	return entry.midi(link), nil
}

// Backends is what a settings screen offers, so the list cannot drift from
// what is actually registered.
func Backends() []string {
	names := make([]string, 0, len(backends))
	for name := range backends {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
