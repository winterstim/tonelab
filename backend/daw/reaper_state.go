package daw

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	goosc "github.com/hypebeast/go-osc/osc"
)

// ErrValueUnknown is deliberately not "no such parameter": the value exists
// and we simply have not been told it, so the recovery is a refresh rather
// than a different name.
var ErrValueUnknown = errors.New("daw: value not reported by the DAW yet")

// state overwrites rather than appends, since DAW feedback is current state
// and not a log: an older reading is wrong, not history.
type state struct {
	mu     sync.RWMutex
	values map[string]float64

	// How many readings have been recorded, so a caller can tell a value the
	// DAW just reported from one it reported before a question was asked.
	// Presence alone cannot: the stale reading looks identical.
	readings uint64

	// The name of the track the control surface is looking at, with a count
	// of how many times it has been announced. Two tracks may share a name,
	// so a waiter has to watch the count rather than the value.
	trackName       string
	trackNameEvents uint64

	// Closed and replaced on every update, so a waiter can block until
	// something changes instead of polling a map on a timer.
	updated chan struct{}
}

func newState() *state {
	return &state{
		values:  make(map[string]float64),
		updated: make(chan struct{}),
	}
}

func key(track int, param string) string {
	return fmt.Sprintf("%d/%s", track, param)
}

func (s *state) set(track int, param string, value float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.values[key(track, param)] = value
	s.readings++

	close(s.updated)
	s.updated = make(chan struct{})
}

// setTrackName records what the DAW says it is looking at.
func (s *state) setTrackName(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.trackName = name
	s.trackNameEvents++

	close(s.updated)
	s.updated = make(chan struct{})
}

// currentTrackName returns the name and how many announcements have been seen.
func (s *state) currentTrackName() (string, uint64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.trackName, s.trackNameEvents
}

// changed hands back the current generation's channel so a waiter cannot miss
// an update that lands between its read and its wait.
func (s *state) changed() <-chan struct{} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.updated
}

func (s *state) get(track int, param string) (float64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.values[key(track, param)]
	return value, ok
}

// readingsSeen is the count a caller records before asking, to recognise an
// answer that arrived afterwards.
func (s *state) readingsSeen() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readings
}

// Observe makes reading a lookup rather than a query, which is a requirement
// and not a shortcut: REAPER never answers questions, only announces changes,
// so a value is known only if we were listening when it was announced. Hence
// a value can be legitimately unknown.
func (r *REAPER) Observe(feedback <-chan *goosc.Message) {
	go func() {
		for msg := range feedback {
			r.observed.Add(1)
			r.lastSeen.Store(time.Now().UnixNano())
			r.absorb(msg)
			r.tapMu.Lock()
			tap := r.tap
			r.tapMu.Unlock()
			if tap != nil {
				tap(msg)
			}
		}
	}()
}

// LastSeen reports when the DAW last said anything at all, not just something
// we understood. Callers infer connection from it, since nothing about sending
// reveals whether anything is listening.
func (r *REAPER) LastSeen() time.Time {
	nanos := r.lastSeen.Load()
	if nanos == 0 {
		return time.Time{}
	}
	return time.Unix(0, nanos)
}

// Observed distinguishes a silent DAW from one whose messages we are failing
// to interpret.
func (r *REAPER) Observed() uint64 {
	return r.observed.Load()
}

// absorb keeps only the normalized form this layer speaks in,
// discarding the position, meter and string readouts a DAW
// streams alongside it.
func (r *REAPER) absorb(msg *goosc.Message) {
	// The name of the surface's current track, which the track walk waits on.
	if msg.Address == "/track/name" && len(msg.Arguments) > 0 {
		if name, ok := msg.Arguments[0].(string); ok {
			r.state.setTrackName(name)
			return
		}
	}

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
// than the project, so reading cannot disturb a project someone is working
// in. Enforced by test, not by care.
func (r *REAPER) Refresh(track int) error {
	if err := validateTrack(track); err != nil {
		return err
	}

	// Bounce off a different track so the second message is a change; REAPER
	// says nothing when asked to select what is already selected.
	other := track + 1
	if err := r.send("/device/track/select", int32(other)); err != nil {
		return err
	}
	return r.send("/device/track/select", int32(track))
}

// ReadParam answers what the value is, for a caller who asked a question.
// It prefers a fresh reading and falls back to the last one heard, because a
// DAW that reports only changes stays silent about a value that has not moved,
// and answering "I do not know" about something we were told five seconds ago
// would be unhelpful and untrue.
func (r *REAPER) ReadParam(track int, name string, timeout time.Duration) (any, error) {
	value, err := r.ConfirmParam(track, name, timeout)
	if err == nil {
		return value, nil
	}
	if !errors.Is(err, ErrValueUnknown) {
		return nil, err
	}
	// Silence means unchanged far more often than unknown, so the last
	// reading is the better answer where there is one.
	return r.GetParam(track, name)
}

// ConfirmParam answers whether the value is what it should be now, for a
// caller checking its own work. Only a reading that arrives after the call
// counts: right after a change the cache still holds the old value, and
// answering from it reports a number that is confidently wrong, which is worse
// than reporting nothing.
func (r *REAPER) ConfirmParam(track int, name string, timeout time.Duration) (any, error) {
	// Recorded before asking, so an answer already in flight still counts as
	// an answer to this question rather than to the last one.
	seen := r.state.readingsSeen()
	changed := r.state.changed()

	if err := r.Refresh(track); err != nil {
		return nil, err
	}

	deadline := time.After(timeout)
	for {
		if r.state.readingsSeen() > seen {
			value, err := r.GetParam(track, name)
			if err == nil {
				return value, nil
			}
			if !errors.Is(err, ErrValueUnknown) {
				return nil, err // a name or track problem; waiting cannot fix it
			}
		}

		select {
		case <-changed:
			changed = r.state.changed()
		case <-deadline:
			return nil, fmt.Errorf("%w: track %d %s", ErrValueUnknown, track, name)
		}
	}
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
