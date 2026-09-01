package main

import (
	"fmt"
	"log"

	"github.com/hypebeast/go-osc/osc"
)

// TransportService sends hardcoded REAPER transport commands over OSC.
// Deliberately minimal and disposable — walking-skeleton proof
// that the Go -> OSC -> REAPER leg of the chain works end to end, before
// any real DAW command layer (backend/daw) or agent is built on top of it.
//
// oscHost/oscPort must match REAPER's Preferences > Control/OSC/web ->
// Add -> OSC, "Local listen port" for an enabled OSC control surface.
type TransportService struct{}

const (
	oscHost = "127.0.0.1"
	oscPort = 8000
)

func (t *TransportService) Play() string {
	return t.send("/play")
}

func (t *TransportService) Stop() string {
	return t.send("/stop")
}

// send fires a bare trigger message (no args) at oscHost:oscPort and logs
// every step to stdout — visible in the terminal running `wails3 dev` — so
// a silent failure (REAPER not listening, wrong port, dropped packet) is
// distinguishable from "the UDP write itself errored", which almost never
// happens since UDP is fire-and-forget and has no delivery confirmation.
func (t *TransportService) send(address string) string {
	log.Printf("[osc] dialing %s:%d to send %s", oscHost, oscPort, address)
	client := osc.NewClient(oscHost, oscPort)
	err := client.Send(osc.NewMessage(address))
	if err != nil {
		log.Printf("[osc] send %s FAILED: %v", address, err)
		return fmt.Sprintf("OSC send failed: %v", err)
	}
	log.Printf("[osc] send %s: no local error (UDP has no delivery confirmation — this does NOT mean REAPER received it)", address)
	return fmt.Sprintf("Sent %s to %s:%d (check terminal + REAPER)", address, oscHost, oscPort)
}
