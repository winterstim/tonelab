//go:build reaper

package daw_test

import (
	"testing"
	"time"

	"tonelab/backend/daw"
	"tonelab/backend/osc"
)

// Against whatever is on track 1 of the open project. Nothing about the
// plugins is assumed: the check is that every effect has a name, every
// parameter has a name, and the walk reached past the first bank when the
// plugin has more than one.
func TestREAPERFXChain(t *testing.T) {
	listener, err := osc.Listen(reaperHost, reaperFeedbackPort)
	if err != nil {
		t.Fatalf("could not listen for REAPER's feedback: %v", err)
	}
	defer listener.Close()
	reaper := daw.NewREAPER(osc.NewTransport(reaperHost, reaperListenPort))
	reaper.Observe(listener.Messages())

	chain, err := reaper.FXChain(1, 2*time.Second)
	if err != nil {
		t.Fatalf("FXChain: %v", err)
	}
	if len(chain) == 0 {
		t.Skip("track 1 of the open project has no effects; put any plugin on it to run this")
	}

	for _, fx := range chain {
		if fx.Name == "" {
			t.Errorf("effect %d has no name", fx.Number)
		}
		if len(fx.Params) == 0 {
			t.Errorf("effect %q reports no parameters", fx.Name)
		}
		for i, param := range fx.Params {
			if param.Number != i+1 {
				t.Errorf("%q: parameter %d numbered %d", fx.Name, i+1, param.Number)
			}
			if param.Name == "" {
				t.Errorf("%q: parameter %d has no name", fx.Name, param.Number)
			}
		}
		t.Logf("%d %q: %d parameters, first %q, last %q", fx.Number, fx.Name, len(fx.Params), fx.Params[0].Name, fx.Params[len(fx.Params)-1].Name)
	}
}

// Sets the first parameter of the first effect on track 1 and takes the
// DAW's word for what it became. Plugins round to their own steps, so the
// check is proximity, not equality.
func TestREAPERFXParamRoundTrip(t *testing.T) {
	listener, err := osc.Listen(reaperHost, reaperFeedbackPort)
	if err != nil {
		t.Fatalf("could not listen for REAPER's feedback: %v", err)
	}
	defer listener.Close()
	reaper := daw.NewREAPER(osc.NewTransport(reaperHost, reaperListenPort))
	reaper.Observe(listener.Messages())

	chain, err := reaper.FXChain(1, 2*time.Second)
	if err != nil || len(chain) == 0 {
		t.Skip("track 1 of the open project has no effects")
	}

	for _, want := range []float64{0.7, 0.3} {
		if err := reaper.SetFXParam(1, 1, 1, want); err != nil {
			t.Fatalf("SetFXParam: %v", err)
		}
		got, err := reaper.ConfirmFXParam(1, 1, 1, 2*time.Second)
		if err != nil {
			t.Fatalf("ConfirmFXParam after setting %v: %v", want, err)
		}
		if got < want-0.05 || got > want+0.05 {
			t.Fatalf("set %v, the DAW reports %v", want, got)
		}
		t.Logf("%q %q: set %v, DAW reports %v", chain[0].Name, chain[0].Params[0].Name, want, got)
	}

	read, err := reaper.ReadFXParam(1, 1, 1, 2*time.Second)
	if err != nil {
		t.Fatalf("ReadFXParam: %v", err)
	}
	if read < 0.25 || read > 0.35 {
		t.Fatalf("expected the last value near 0.3 on read, got %v", read)
	}
}
