package main

import (
	"fmt"

	"tonelab/backend/daw"
)

// TransportService exposes transport control to the frontend. It is a thin
// Wails boundary over the DAW command layer: no OSC addresses, no argument
// encoding, and no knowledge of which DAW is connected — those belong to
// backend/daw, and this type only turns the result into text a button can
// display.
//
// Still two hardcoded commands. The agent (which will drive backend/daw
// through the parameter tools instead) does not exist yet, and until it does
// these buttons are how the chain gets exercised by hand.
type TransportService struct {
	daw daw.Client
}

func NewTransportService(client daw.Client) *TransportService {
	return &TransportService{daw: client}
}

func (t *TransportService) Play() string {
	return t.report("play", t.daw.Play())
}

func (t *TransportService) Stop() string {
	return t.report("stop", t.daw.Stop())
}

// report turns a command's outcome into a line for the UI toast. A nil error
// means the command was written to the socket, not that the DAW received it
// — the transport is fire-and-forget — so the success text points at the DAW
// itself as the real check.
func (t *TransportService) report(command string, err error) string {
	if err != nil {
		return fmt.Sprintf("Could not %s: %v", command, err)
	}
	return fmt.Sprintf("Sent %s — check your DAW", command)
}
