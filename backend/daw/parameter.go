package daw

import (
	"errors"
	"fmt"
)

var (
	ErrUnknownParam = errors.New("daw: unknown parameter")
	ErrParamKind    = errors.New("daw: wrong value type for parameter")
	ErrNotReadable  = errors.New("daw: parameter cannot be read back")
)

// Kind is what shape a parameter's value takes. It exists so callers can
// validate a value without knowing the parameter — the agent tools accept
// either a number or a boolean and need to
// know which one a given name wants.
type Kind int

const (
	Numeric Kind = iota // normalized 0.0-1.0
	Toggle              // on/off
)

func (k Kind) String() string {
	if k == Toggle {
		return "toggle"
	}
	return "numeric"
}

// Parameter describes one thing a backend can control, as that backend
// reports it. Backends differ in where this comes from and that is the point:
// a DAW whose OSC surface can enumerate its parameters builds this list by
// asking the DAW, one that cannot returns what it knows statically. Callers
// above see the same shape either way and never hardcode a parameter list.
type Parameter struct {
	Name string
	Kind Kind

	// Readable reports whether the current value can be read back from the
	// DAW. Not every surface can: a DAW may accept a parameter without ever
	// reporting it, and a caller needs to know that before promising a user
	// it can answer "what is it now?".
	Readable bool
}

// Describer is implemented by backends that can report their own parameters.
// Kept separate from Client so a backend is not forced to answer before it
// can, and so callers must handle "this backend cannot say" explicitly rather
// than receiving a silently empty list.
type Describer interface {
	Parameters() []Parameter
}

// ParametersOf returns what a backend says it can control, or an error if the
// backend cannot describe itself. This is the call the agent tools resolve a
// free-text parameter name against, instead of a list compiled into them.
func ParametersOf(client Client) ([]Parameter, error) {
	describer, ok := client.(Describer)
	if !ok {
		return nil, fmt.Errorf("daw: backend %T cannot describe its parameters", client)
	}
	return describer.Parameters(), nil
}

// FindParameter looks a parameter up by name among what a backend reports.
func FindParameter(client Client, name string) (Parameter, error) {
	params, err := ParametersOf(client)
	if err != nil {
		return Parameter{}, err
	}
	for _, param := range params {
		if param.Name == name {
			return param, nil
		}
	}
	return Parameter{}, fmt.Errorf("%w %q", ErrUnknownParam, name)
}
