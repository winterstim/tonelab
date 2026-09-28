package dawtest

import (
	"fmt"
	"sync"
	"time"

	"github.com/winterstim/tonelab/internal/daw"
)

// Fake is a DAW for tests above this layer: two tracks, a volume and a mute,
// one reverb on track 1, and undo. It refuses what a real backend refuses
// and is held to the same contract, so a test cannot pass on a DAW kinder
// than the real one. Safe for concurrent use, as a DAW behind a server is.
type Fake struct {
	daw.Client

	mu     sync.Mutex
	values map[string]any
	fx     map[string]float64
	undos  int
}

// NewFake returns a Fake with nothing set yet.
func NewFake() *Fake {
	return &Fake{values: map[string]any{}, fx: map[string]float64{}}
}

func (f *Fake) Parameters() []daw.Parameter {
	return []daw.Parameter{
		{Name: "volume", Kind: daw.Numeric, Readable: true},
		{Name: "mute", Kind: daw.Toggle, Readable: true},
	}
}

func (f *Fake) SetParam(track int, name string, value any) error {
	if track < 1 {
		return daw.ErrInvalidTrack
	}
	var kind daw.Kind
	switch name {
	case "volume":
		kind = daw.Numeric
	case "mute":
		kind = daw.Toggle
	default:
		return fmt.Errorf("%w %q", daw.ErrUnknownParam, name)
	}
	switch v := value.(type) {
	case bool:
		if kind != daw.Toggle {
			return daw.ErrParamKind
		}
	case float64:
		if kind != daw.Numeric {
			return daw.ErrParamKind
		}
		if v < 0 || v > 1 {
			return daw.ErrValueOutOfRange
		}
	default:
		return daw.ErrParamKind
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.values[fmt.Sprintf("%d/%s", track, name)] = value
	return nil
}

func (f *Fake) GetParam(track int, name string) (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	value, ok := f.values[fmt.Sprintf("%d/%s", track, name)]
	if !ok {
		return nil, daw.ErrValueUnknown
	}
	return value, nil
}

func (f *Fake) ReadParam(track int, name string, timeout time.Duration) (any, error) {
	return f.GetParam(track, name)
}

func (f *Fake) ConfirmParam(track int, name string, timeout time.Duration) (any, error) {
	return f.GetParam(track, name)
}

func (f *Fake) Tracks(timeout time.Duration) ([]daw.Track, error) {
	return []daw.Track{{Number: 1, Name: "Guitar"}, {Number: 2, Name: "Vocals"}}, nil
}

func (f *Fake) Undo() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.undos++
	return nil
}

func (f *Fake) FXChain(track int, timeout time.Duration) ([]daw.FX, error) {
	if track != 1 {
		return nil, nil
	}
	return []daw.FX{{Number: 1, Name: "Reverb", Params: []daw.FXParam{{Number: 1, Name: "Mix"}}}}, nil
}

func (f *Fake) SetFXParam(track, fx, param int, value float64) error {
	if track < 1 || fx < 1 || param < 1 {
		return daw.ErrInvalidFX
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fx[fmt.Sprintf("%d/%d/%d", track, fx, param)] = value
	return nil
}

func (f *Fake) ConfirmFXParam(track, fx, param int, timeout time.Duration) (float64, error) {
	return f.ReadFXParam(track, fx, param, timeout)
}

func (f *Fake) ReadFXParam(track, fx, param int, timeout time.Duration) (float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	value, ok := f.fx[fmt.Sprintf("%d/%d/%d", track, fx, param)]
	if !ok {
		return 0, daw.ErrValueUnknown
	}
	return value, nil
}

// Undos is how many times undo was asked for.
func (f *Fake) Undos() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.undos
}
