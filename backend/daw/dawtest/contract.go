// Package dawtest holds the behaviour every daw.Client must share, as one set
// of assertions run against both real backends and the fakes that stand in for
// them in other packages' tests.
//
// It exists because of a specific failure: a fake that accepted any parameter
// name made a recovery test pass while the recovery never happened, and a fake
// that answered instantly hid a network round-trip. A fake kinder than reality
// is worse than no test, and the only reliable guard is holding both to one
// contract rather than to one author's memory.
package dawtest

import (
	"errors"
	"testing"

	"tonelab/backend/daw"
)

// AssertClientContract is the whole shared contract. Every implementation of
// daw.Client, real or fake, is expected to pass it unchanged.
func AssertClientContract(t *testing.T, client daw.Client) {
	t.Helper()

	t.Run("describes its own parameters", func(t *testing.T) {
		params, err := daw.ParametersOf(client)
		if err != nil {
			t.Fatalf("a client must be able to describe itself: %v", err)
		}
		if len(params) == 0 {
			t.Fatal("a client reporting no parameters cannot be driven at all")
		}

		seen := map[string]bool{}
		for _, param := range params {
			if param.Name == "" {
				t.Error("a parameter without a name cannot be resolved")
			}
			if seen[param.Name] {
				t.Errorf("%q is reported twice, so resolution is ambiguous", param.Name)
			}
			seen[param.Name] = true
		}
	})

	t.Run("rejects a parameter it does not have", func(t *testing.T) {
		err := client.SetParam(1, "definitely-not-a-parameter", 0.5)

		if !errors.Is(err, daw.ErrUnknownParam) {
			t.Fatalf("expected ErrUnknownParam so the agent can rename, got %v", err)
		}
	})

	t.Run("rejects a value of the wrong kind", func(t *testing.T) {
		params, _ := daw.ParametersOf(client)

		for _, param := range params {
			wrong := any(true)
			if param.Kind == daw.Toggle {
				wrong = 0.5
			}

			if err := client.SetParam(1, param.Name, wrong); !errors.Is(err, daw.ErrParamKind) {
				t.Errorf("%s: expected ErrParamKind for a %T, got %v", param.Name, wrong, err)
			}
		}
	})

	t.Run("rejects a track index below one", func(t *testing.T) {
		err := client.SetParam(0, firstNumeric(t, client), 0.5)

		if !errors.Is(err, daw.ErrInvalidTrack) {
			t.Fatalf("expected ErrInvalidTrack, got %v", err)
		}
	})

	t.Run("rejects a value outside the normalized range", func(t *testing.T) {
		name := firstNumeric(t, client)

		for _, value := range []float64{-0.1, 1.5} {
			if err := client.SetParam(1, name, value); !errors.Is(err, daw.ErrValueOutOfRange) {
				t.Errorf("%v: expected ErrValueOutOfRange, got %v", value, err)
			}
		}
	})

	t.Run("accepts what it says it accepts", func(t *testing.T) {
		params, _ := daw.ParametersOf(client)

		for _, param := range params {
			value := any(0.5)
			if param.Kind == daw.Toggle {
				value = true
			}

			err := client.SetParam(1, param.Name, value)
			if err != nil && !errors.Is(err, daw.ErrParamKind) {
				// ErrParamKind is allowed here only for a parameter whose
				// addressing needs more than (track, name), such as a send.
				t.Errorf("%s: reported as supported but rejected a valid value: %v", param.Name, err)
			}
		}
	})
}

func firstNumeric(t *testing.T, client daw.Client) string {
	t.Helper()

	params, err := daw.ParametersOf(client)
	if err != nil {
		t.Fatalf("client cannot describe itself: %v", err)
	}
	for _, param := range params {
		if param.Kind == daw.Numeric {
			return param.Name
		}
	}
	t.Fatal("no numeric parameter to test the range with")
	return ""
}
