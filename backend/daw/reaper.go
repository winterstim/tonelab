// Package daw turns domain commands into one DAW's OSC addresses. It knows
// nothing about the agent, so it can be driven and tested with no LLM
// involved, which is what makes DAW bugs findable.
//
// Values crossing this API are always normalized 0.0-1.0, never dB
// or Hz, so the contract above stays identical when a DAW that speaks real
// units is added. Each backend owns that conversion.
package daw

import (
	goosc "github.com/hypebeast/go-osc/osc"

	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
)

// Separate sentinels because the agent recovers from each differently, and
// the tools layer maps them onto structured codes.
var (
	ErrInvalidTrack    = errors.New("daw: track index must be 1 or greater")
	ErrInvalidSend     = errors.New("daw: send index must be 1 or greater")
	ErrValueOutOfRange = errors.New("daw: value must be between 0.0 and 1.0")
)

// Client is the seam a second DAW implements. Track-level parameters only,
// which is the whole of MVP scope.
type Client interface {
	Play() error
	Stop() error

	// SetParam dispatches inside the backend because a name maps to a
	// command differently per DAW. Layers above resolve names, never
	// translate them.
	SetParam(track int, name string, value any) error

	SetTrackVolume(track int, value float64) error
	SetTrackPan(track int, value float64) error
	SetTrackMute(track int, muted bool) error
	SetTrackSolo(track int, soloed bool) error
	SetTrackSendVolume(track, send int, value float64) error
}

// Sender is narrowed to the one method used, so this layer neither depends on
// the OSC package's concrete type nor needs a socket to test.
type Sender interface {
	Send(address string, args ...any) error
}

// REAPER maps commands onto the addresses in REAPER's own Default.ReaperOSC
// pattern config. Indices are 1-based there, matching REAPER's UI.
type REAPER struct {
	osc Sender

	// The read path's picture of the DAW, kept current by Observe.
	state    *state
	observed atomic.Uint64

	// Unix nanoseconds of the last feedback of any kind.
	lastSeen atomic.Int64

	// Guards the control surface's view, which both the track walk and the
	// liveness probe move. Concurrent users would read each other's answers.
	surface sync.Mutex

	// A walk in progress reads raw feedback through here, since what it
	// needs (names in banks) is transient and not state worth keeping.
	tapMu sync.Mutex
	tap   func(*goosc.Message)

	// Where the surface was last pointed, since its feedback names no
	// track. Zero until this backend has pointed it somewhere.
	surfaceTrack atomic.Int64
	// Likewise which effect and parameter bank, since bank feedback names
	// neither. Both are left at 1 between operations, so selecting a
	// higher one is always a transition the DAW announces.
	surfaceFX   atomic.Int64
	surfaceBank atomic.Int64
}

var _ Client = (*REAPER)(nil)

func NewREAPER(sender Sender) *REAPER {
	return &REAPER{osc: sender, state: newState()}
}

// Static because OSC has no discovery mechanism to ask REAPER with. A DAW
// that can be asked builds the same list at runtime, and callers above cannot
// tell which happened.
var parameters = []Parameter{
	{Name: "volume", Kind: Numeric, Readable: true},
	{Name: "pan", Kind: Numeric, Readable: true},
	{Name: "mute", Kind: Toggle, Readable: true},
	{Name: "solo", Kind: Toggle, Readable: true},
	// Send volume is addressed by two indices rather than one, so it is not
	// reachable through SetParam's (track, name) shape and is marked
	// unreadable because no feedback for it has been observed. Reaching it
	// needs a richer target than a track number.
	{Name: "send", Kind: Numeric, Readable: false},
}

func (r *REAPER) Parameters() []Parameter {
	return append([]Parameter(nil), parameters...)
}

// SetParam rejects a value whose type contradicts the parameter's Kind rather
// than coercing it, since the contract above carries either a number or a
// boolean and confusing them is a caller bug worth naming.
func (r *REAPER) SetParam(track int, name string, value any) error {
	param, err := FindParameter(r, name)
	if err != nil {
		return err
	}

	switch param.Kind {
	case Toggle:
		on, ok := value.(bool)
		if !ok {
			return fmt.Errorf("%w: %s wants a %s, got %T", ErrParamKind, name, param.Kind, value)
		}
		return r.setTrackToggle(track, name, on)
	default:
		number, ok := numeric(value)
		if !ok {
			return fmt.Errorf("%w: %s wants a %s, got %T", ErrParamKind, name, param.Kind, value)
		}
		if name == "send" {
			return fmt.Errorf("%w: %s needs a send index, set it with SetTrackSendVolume", ErrParamKind, name)
		}
		return r.setTrackValue(track, name, number)
	}
}

// numeric accepts every float shape a JSON-decoded value can arrive as, since
// the tools layer passes through whatever its schema produced.
func numeric(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	}
	return 0, false
}

func (r *REAPER) Play() error {
	return r.send("/play")
}

func (r *REAPER) Stop() error {
	return r.send("/stop")
}

// SetTrackVolume passes the value through because REAPER's OSC is normalized
// already. A DAW speaking real units would convert here.
func (r *REAPER) SetTrackVolume(track int, value float64) error {
	return r.setTrackValue(track, "volume", value)
}

// SetTrackPan takes 0.5 as centre, which is REAPER's convention rather than
// one Tonelab imposes.
func (r *REAPER) SetTrackPan(track int, value float64) error {
	return r.setTrackValue(track, "pan", value)
}

func (r *REAPER) SetTrackMute(track int, muted bool) error {
	return r.setTrackToggle(track, "mute", muted)
}

func (r *REAPER) SetTrackSolo(track int, soloed bool) error {
	return r.setTrackToggle(track, "solo", soloed)
}

func (r *REAPER) SetTrackSendVolume(track, send int, value float64) error {
	if err := validateTrack(track); err != nil {
		return err
	}
	if send < 1 {
		return fmt.Errorf("%w, got %d", ErrInvalidSend, send)
	}
	if err := validateNormalized(value); err != nil {
		return err
	}
	return r.send(fmt.Sprintf("/track/%d/send/%d/volume", track, send), float32(value))
}

func (r *REAPER) setTrackValue(track int, param string, value float64) error {
	if err := validateTrack(track); err != nil {
		return err
	}
	if err := validateNormalized(value); err != nil {
		return err
	}
	return r.send(fmt.Sprintf("/track/%d/%s", track, param), float32(value))
}

// setTrackToggle sends 1.0/0.0 floats rather than OSC booleans, matching what
// REAPER's own surfaces send for its `b/` patterns. Verified against a live
// REAPER, since the config alone is ambiguous.
func (r *REAPER) setTrackToggle(track int, param string, on bool) error {
	if err := validateTrack(track); err != nil {
		return err
	}
	value := float32(0)
	if on {
		value = 1
	}
	return r.send(fmt.Sprintf("/track/%d/%s", track, param), value)
}

func validateTrack(track int) error {
	if track < 1 {
		return fmt.Errorf("%w, got %d", ErrInvalidTrack, track)
	}
	return nil
}

// validateNormalized stops a caller pushing a value the DAW would silently
// clamp, which would leave the agent believing a command it can't verify.
//
// NaN is checked separately because every comparison against it is false, so
// a range check alone lets it through to the DAW untouched.
func validateNormalized(value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf("%w, got %v", ErrValueOutOfRange, value)
	}
	if value < 0 || value > 1 {
		return fmt.Errorf("%w, got %v", ErrValueOutOfRange, value)
	}
	return nil
}
