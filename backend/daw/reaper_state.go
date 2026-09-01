package daw

import (
	"fmt"
	"strings"
	"sync"

	goosc "github.com/hypebeast/go-osc/osc"
)

// ErrValueUnknown means the DAW has not reported this parameter yet. It is
// deliberately not the same as "no such parameter": the value exists, we just
// have not been told it, and the recovery differs — waiting or prompting the
// DAW may help, renaming will not.
var ErrValueUnknown = fmt.Errorf("daw: value not reported by the DAW yet")

// state holds the most recent value the DAW reported per (track, parameter).
// Feedback is a stream of current state rather than a log, so an older
// reading is not history — it is simply wrong, and gets overwritten.
type state struct {
	mu     sync.RWMutex
	values map[string]float64
}

func newState() *state {
	return &state{values: make(map[string]float64)}
}

func key(track int, param string) string {
	return fmt.Sprintf("%d/%s", track, param)
}

func (s *state) set(track int, param string, value float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key(track, param)] = value
}

func (s *state) get(track int, param string) (float64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.values[key(track, param)]
	return value, ok
}

// Observe consumes a DAW's feedback stream in the background, keeping the
// backend's picture of the DAW current. Reading a value is then a lookup
// rather than a query — which is not a shortcut but a requirement: REAPER
// never answers questions, it only announces changes, so the only way to know
// a value is to have been listening when it was announced.
//
// This is also why a value can be legitimately unknown: nothing guarantees
// the DAW mentioned a given parameter since we started listening.
func (r *REAPER) Observe(feedback <-chan *goosc.Message) {
	go func() {
		for msg := range feedback {
			r.observed.Add(1)
			r.absorb(msg)
		}
	}()
}

// Observed counts the feedback messages taken in, whether or not any of them
// were understood. Without it a caller cannot tell a silent DAW from one
// whose messages we are failing to interpret.
func (r *REAPER) Observed() uint64 {
	return r.observed.Load()
}

// absorb records the messages that carry a parameter's current value and
// ignores the rest. A DAW streams far more than parameters — playhead
// position, meters, names, string readouts of the same values — and only the
// normalized form is what this layer's contract speaks in.
func (r *REAPER) absorb(msg *goosc.Message) {
	track, param, ok := parseTrackAddress(msg.Address)
	if !ok {
		return
	}
	if _, err := FindParameter(r, param); err != nil {
		return // an address we can parse but not a parameter we expose
	}
	if len(msg.Arguments) == 0 {
		return
	}
	value, ok := numeric(msg.Arguments[0])
	if !ok {
		return // a string readout such as /volume/str, not the value itself
	}
	r.state.set(track, param, value)
}

// parseTrackAddress splits REAPER's own address form. Anything with a deeper
// path (/track/1/volume/str, /track/1/send/1/volume) is deliberately not a
// match: those are either a different representation of the value or a
// different parameter entirely.
func parseTrackAddress(address string) (track int, param string, ok bool) {
	parts := strings.Split(strings.TrimPrefix(address, "/"), "/")
	if len(parts) != 3 || parts[0] != "track" {
		return 0, "", false
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &track); err != nil || track < 1 {
		return 0, "", false
	}
	return track, parts[2], true
}

// Refresh asks the DAW to report the state of a track, because REAPER never
// answers a question — it only announces changes. Reading a value therefore
// has two steps that must stay separate: make the DAW talk, then read what it
// said. Folding them together would hide a network round-trip inside what
// looks like a map lookup.
//
// It works by pointing the control surface at another track and back, which
// is what makes REAPER re-announce. Crucially it sends only /device/*
// addresses — the surface's own view — and never /track/*, so it does not
// touch the project, mark it dirty, or disturb the user's selection
//. That restriction is a guarantee, and it is tested.
func (r *REAPER) Refresh(track int) error {
	if err := validateTrack(track); err != nil {
		return err
	}

	// Bounce off a different track so the second message is a change; REAPER
	// says nothing when asked to select what is already selected.
	other := track + 1
	if err := r.osc.Send("/device/track/select", int32(other)); err != nil {
		return err
	}
	return r.osc.Send("/device/track/select", int32(track))
}

// GetParam reports a parameter's current value as the DAW last described it —
// never as Tonelab last set it. Those differ whenever a command was lost on
// the way out, or a user moved a control in the DAW itself, and the DAW's
// account is the true one.
func (r *REAPER) GetParam(track int, name string) (any, error) {
	if err := validateTrack(track); err != nil {
		return nil, err
	}
	param, err := FindParameter(r, name)
	if err != nil {
		return nil, err
	}
	if !param.Readable {
		return nil, fmt.Errorf("%w: %s", ErrNotReadable, name)
	}

	value, known := r.state.get(track, name)
	if !known {
		return nil, fmt.Errorf("%w: track %d %s", ErrValueUnknown, track, name)
	}

	if param.Kind == Toggle {
		return value != 0, nil
	}
	return value, nil
}
