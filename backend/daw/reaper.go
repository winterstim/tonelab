// Package daw is the DAW command layer: a typed Go API for what can be done
// to a DAW, mapping domain commands onto one DAW's OSC addresses. It knows
// nothing about the agent — this is deliberately the layer you can drive by
// hand, and test, with no LLM anywhere near it.
//
// REAPER is the only backend. A second one would implement the
// same Client interface and own its normalize/denormalize work, which is why
// values crossing this API are always normalized 0.0-1.0 rather
// than dB, Hz, or whatever a given DAW happens to speak.
package daw

import (
	"errors"
	"fmt"
)

// Failures a caller can act on differently, rather than one opaque error.
// The agent tools above this layer turn these into the structured codes the
// UI and the agent's own loop branch on.
var (
	ErrInvalidTrack    = errors.New("daw: track index must be 1 or greater")
	ErrInvalidSend     = errors.New("daw: send index must be 1 or greater")
	ErrValueOutOfRange = errors.New("daw: value must be between 0.0 and 1.0")
)

// Client is what any DAW backend must be able to do. Track-level parameters
// only, which is the whole of MVP scope — FX parameters need a name-to-index
// bridge that does not exist yet.
type Client interface {
	Play() error
	Stop() error

	// SetParam sets a parameter by the name the backend itself reports
	// (see Describer). Dispatch lives in the backend because the mapping
	// from a name to a command is DAW-specific — the layers above resolve
	// names, they do not translate them.
	SetParam(track int, name string, value any) error

	SetTrackVolume(track int, value float64) error
	SetTrackPan(track int, value float64) error
	SetTrackMute(track int, muted bool) error
	SetTrackSolo(track int, soloed bool) error
	SetTrackSendVolume(track, send int, value float64) error
}

// Sender is the transport this layer writes through, narrowed to the one
// method it uses so the DAW layer can be tested without a socket and doesn't
// depend on the OSC package's concrete type.
type Sender interface {
	Send(address string, args ...any) error
}

// REAPER maps commands onto REAPER's native OSC addresses, as documented in
// REAPER's own Default.ReaperOSC pattern config. Track and send indices are
// 1-based there, matching what REAPER shows in its own UI.
type REAPER struct {
	osc Sender
}

var _ Client = (*REAPER)(nil)

func NewREAPER(sender Sender) *REAPER {
	return &REAPER{osc: sender}
}

// parameters is what this backend can control. REAPER's OSC surface cannot
// enumerate itself — there is no discovery in OSC — so this list is what its
// documented pattern config supports. A backend whose DAW can be asked builds
// the same list by asking it; callers above cannot tell the difference, which
// is the point.
var parameters = []Parameter{
	{Name: "volume", Kind: Numeric, Readable: true},
	{Name: "pan", Kind: Numeric, Readable: true},
	{Name: "mute", Kind: Toggle, Readable: true},
	{Name: "solo", Kind: Toggle, Readable: true},
	// Send volume is addressed by two indices rather than one, so it is not
	// reachable through SetParam's (track, name) shape and is marked
	// unreadable because no feedback for it has been observed. Reaching it
	// needs a richer target than a track number — see README.
	{Name: "send", Kind: Numeric, Readable: false},
}

func (r *REAPER) Parameters() []Parameter {
	return append([]Parameter(nil), parameters...)
}

// SetParam routes a named parameter to the typed command that implements it.
// The type of value must match the parameter's Kind: the contract above this
// layer carries either a number or a boolean, and sending the wrong one is a
// caller error worth naming rather than a value to coerce.
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

// numeric accepts the float shapes a JSON-decoded value can arrive as, since
// the agent tools above this layer hand over whatever their schema produced.
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
	return r.osc.Send("/play")
}

func (r *REAPER) Stop() error {
	return r.osc.Send("/stop")
}

// SetTrackVolume sets track volume from a normalized 0.0-1.0 value. REAPER's
// `n/track/@/volume` is normalized already, so there is nothing to convert —
// a DAW whose OSC speaks real units would do that conversion right here.
func (r *REAPER) SetTrackVolume(track int, value float64) error {
	return r.setTrackValue(track, "volume", value)
}

// SetTrackPan sets track pan from a normalized 0.0-1.0 value, where 0.5 is
// centre — REAPER's own convention for `n/track/@/pan`, not a Tonelab one.
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
	return r.osc.Send(fmt.Sprintf("/track/%d/send/%d/volume", track, send), float32(value))
}

func (r *REAPER) setTrackValue(track int, param string, value float64) error {
	if err := validateTrack(track); err != nil {
		return err
	}
	if err := validateNormalized(value); err != nil {
		return err
	}
	return r.osc.Send(fmt.Sprintf("/track/%d/%s", track, param), float32(value))
}

// setTrackToggle sends REAPER's binary track parameters. They go out as
// 1.0/0.0 floats rather than OSC booleans: REAPER's pattern config marks
// these `b/track/@/mute`, and a float is what its own surfaces send.
func (r *REAPER) setTrackToggle(track int, param string, on bool) error {
	if err := validateTrack(track); err != nil {
		return err
	}
	value := float32(0)
	if on {
		value = 1
	}
	return r.osc.Send(fmt.Sprintf("/track/%d/%s", track, param), value)
}

func validateTrack(track int) error {
	if track < 1 {
		return fmt.Errorf("%w, got %d", ErrInvalidTrack, track)
	}
	return nil
}

// validateNormalized enforces the contract every numeric parameter crossing
// this API shares, so a caller that skipped its own validation
// can't push a value REAPER would clamp silently.
func validateNormalized(value float64) error {
	if value < 0 || value > 1 {
		return fmt.Errorf("%w, got %v", ErrValueOutOfRange, value)
	}
	return nil
}
