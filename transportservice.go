package main

import (
	"fmt"

	"tonelab/backend/daw"
)

// TransportService is deliberately thin: it holds no OSC address, no argument
// encoding and no knowledge of which DAW is connected, so a second backend
// changes nothing here.
//
// Two hardcoded commands until the agent exists, since these buttons are
// currently the only way to drive the chain by hand.
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

// report points the user at the DAW as the real check, because a nil error
// only means the command reached the socket.
func (t *TransportService) report(command string, err error) string {
	if err != nil {
		return fmt.Sprintf("Could not %s: %v", command, err)
	}
	return fmt.Sprintf("Sent %s, check your DAW", command)
}
