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

// Kind lets a caller validate a value without knowing the parameter, which
// the tools layer needs since its schema accepts either shape.
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

// Parameter is how a backend describes itself, so no layer above carries a
// parameter list of its own. Where the description comes from is the
// backend's business: asked of the DAW, or known statically.
type Parameter struct {
	Name string
	Kind Kind

	// Readable exists because a DAW may accept a parameter it never reports
	// back, and a caller must know that before promising to answer "what is
	// it now?".
	Readable bool
}

// Describer is separate from Client so a backend unable to answer says so,
// rather than returning a silently empty list that reads as "nothing here".
type Describer interface {
	Parameters() []Parameter
}

// ParametersOf is what the tools layer resolves names against, so a second
// copy of the parameter list never exists to drift.
func ParametersOf(client Client) ([]Parameter, error) {
	describer, ok := client.(Describer)
	if !ok {
		return nil, fmt.Errorf("daw: backend %T cannot describe its parameters", client)
	}
	return describer.Parameters(), nil
}

// FindParameter resolves against the backend's own description, never a
// hardcoded set.
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
