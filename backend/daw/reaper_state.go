package daw

import (
	"fmt"
	"strings"
	"sync"

	goosc "github.com/hypebeast/go-osc/osc"
)

// ErrValueUnknown is deliberately not "no such parameter": the value exists
// and we simply have not been told it, so the recovery is a refresh rather
// than a different name.
var ErrValueUnknown = fmt.Errorf("daw: value not reported by the DAW yet")

// state overwrites rather than appends, since DAW feedback is current state
// and not a log: an older reading is wrong, not history.
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

// Observe makes reading a lookup rather than a query, which is a requirement
// and not a shortcut: REAPER never answers questions, only announces changes,
// so a value is known only if we were listening when it was announced. Hence
// a value can be legitimately unknown.
func (r *REAPER) Observe(feedback <-chan *goosc.Message) {
	go func() {
		for msg := range feedback {
			r.observed.Add(1)
			r.absorb(msg)
		}
	}()
}

// Observed distinguishes a silent DAW from one whose messages we are failing
// to interpret.
func (r *REAPER) Observed() uint64 {
	return r.observed.Load()
}

// absorb keeps only the normalized form this layer's contract speaks in
//, discarding the position, meter and string readouts a DAW
// streams alongside it.
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

// parseTrackAddress rejects deeper paths such as /track/1/volume/str on
// purpose: they are a different representation, or a different parameter.
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

// Refresh is separate from GetParam so a network round-trip is not hidden
// inside what looks like a map lookup. It makes REAPER re-announce by
// pointing the control surface elsewhere and back.
//
// It sends only /device/* addresses, which move the surface's own view rather
// than the project, so reading cannot disturb a project someone is working in
//. Enforced by test, not by care.
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

// GetParam answers from the DAW's own account rather than what Tonelab last
// sent, because those differ whenever a command was lost on the way out or a
// user moved a control by hand.
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
