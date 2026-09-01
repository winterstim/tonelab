package main

import (
	"fmt"

	"tonelab/backend/osc"
)

// TransportService sends hardcoded REAPER transport commands over OSC.
// Still hardcoded to two commands — the typed DAW command layer
// (backend/daw) that will replace it does not exist yet — but it no longer
// owns the wire: it delegates to backend/osc.Transport, which is the seam
// every OSC message in the app goes through from here on.
type TransportService struct {
	osc *osc.Transport
}

func NewTransportService(transport *osc.Transport) *TransportService {
	return &TransportService{osc: transport}
}

func (t *TransportService) Play() string {
	return t.send("/play")
}

func (t *TransportService) Stop() string {
	return t.send("/stop")
}

// send fires a bare trigger message (no args) and turns the result into a
// line for the UI toast. A nil error means the local write succeeded, not
// that REAPER received it — UDP has no delivery confirmation — so the
// success text points at REAPER itself as the real check.
func (t *TransportService) send(address string) string {
	if err := t.osc.Send(address); err != nil {
		return fmt.Sprintf("OSC send failed: %v", err)
	}
	return fmt.Sprintf("Sent %s — check REAPER", address)
}
