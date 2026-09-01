//go:build reaper

// Against a real REAPER, behind a build tag. The fake backend in the unit
// tests answers synchronously, which hides every timing question; this is
// where those surface.
package agent_test

import (
	"encoding/json"
	"testing"

	"tonelab/backend/agent"
	"tonelab/backend/daw"
	"tonelab/backend/osc"
)

const (
	reaperHost         = "127.0.0.1"
	reaperListenPort   = 8000
	reaperFeedbackPort = 9000
)

// A tool call has to survive the round-trip delay a real DAW imposes. Reading
// is not a lookup here: the refresh travels over UDP and the answer comes back
// asynchronously, so a get that does not wait reads an empty cache.
func TestGetParamAgainstREAPER(t *testing.T) {
	listener, err := osc.Listen(reaperHost, reaperFeedbackPort)
	if err != nil {
		t.Fatalf("could not listen for REAPER's feedback: %v", err)
	}
	defer listener.Close()

	backend := daw.NewREAPER(osc.NewTransport(reaperHost, reaperListenPort))
	backend.Observe(listener.Messages())
	tools := agent.NewTools(backend)

	set := tools.Call("set_param", json.RawMessage(`{"track_id":1,"param_name":"volume","value":0.25}`))
	if set.Error != nil {
		t.Fatalf("set_param failed: %+v", set.Error)
	}

	got := tools.Call("get_param", json.RawMessage(`{"track_id":1,"param_name":"volume"}`))
	if got.Error != nil {
		t.Fatalf("get_param failed: %+v (this is the timing bug if the code is value_unknown)", got.Error)
	}
	value, ok := got.Value.(float64)
	if !ok || value < 0.2 || value > 0.3 {
		t.Fatalf("expected roughly 0.25, got %#v", got.Value)
	}
}
