//go:build ableton

package daw_test

import (
	"testing"
	"time"

	"tonelab/backend/daw"
	"tonelab/backend/osc"
)

// Against a running Live with AbletonOSC enabled and any set open. Nothing
// about the set is assumed beyond one track; values are put back afterwards.
func TestLiveRoundTrip(t *testing.T) {
	listener, err := osc.Listen("127.0.0.1", 11001)
	if err != nil {
		t.Fatalf("could not listen for Live's replies: %v", err)
	}
	defer listener.Close()
	live := daw.NewAbleton(osc.NewTransport("127.0.0.1", 11000))
	live.Observe(listener.Messages())

	if !live.Probe(time.Second) {
		t.Fatal("Live did not answer /live/test; is AbletonOSC selected as a control surface?")
	}

	tracks, err := live.Tracks(2 * time.Second)
	if err != nil || len(tracks) == 0 {
		t.Fatalf("Tracks: %v (%v)", err, tracks)
	}
	t.Logf("%d tracks, first %q", len(tracks), tracks[0].Name)

	before, err := live.ReadParam(1, "volume", time.Second)
	if err != nil {
		t.Fatalf("ReadParam volume: %v", err)
	}
	if err := live.SetTrackVolume(1, 0.25); err != nil {
		t.Fatalf("SetTrackVolume: %v", err)
	}
	got, err := live.ConfirmParam(1, "volume", time.Second)
	if err != nil {
		t.Fatalf("ConfirmParam: %v", err)
	}
	if v, _ := got.(float64); v < 0.24 || v > 0.26 {
		t.Fatalf("set 0.25, Live reports %v", got)
	}
	t.Logf("volume: was %v, set 0.25, Live reports %v", before, got)
	_ = live.SetTrackVolume(1, before.(float64))

	if err := live.SetTrackPan(1, 0.25); err != nil {
		t.Fatalf("SetTrackPan: %v", err)
	}
	pan, err := live.ConfirmParam(1, "pan", time.Second)
	if err != nil {
		t.Fatalf("ConfirmParam pan: %v", err)
	}
	if v, _ := pan.(float64); v < 0.24 || v > 0.26 {
		t.Fatalf("set pan 0.25, read back %v", pan)
	}
	_ = live.SetTrackPan(1, 0.5)

	if err := live.SetTrackMute(1, true); err != nil {
		t.Fatalf("SetTrackMute: %v", err)
	}
	muted, err := live.ConfirmParam(1, "mute", time.Second)
	if err != nil || muted != true {
		t.Fatalf("expected muted, got %v, %v", muted, err)
	}
	_ = live.SetTrackMute(1, false)

	// Undo: Live's history, not ours. The last change was unmute.
	if err := live.Undo(); err != nil {
		t.Fatalf("Undo: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	after, _ := live.ConfirmParam(1, "mute", time.Second)
	t.Logf("after undo of the unmute, mute reads %v", after)
	_ = live.SetTrackMute(1, false)
}

func TestLiveDevices(t *testing.T) {
	listener, err := osc.Listen("127.0.0.1", 11001)
	if err != nil {
		t.Fatalf("could not listen for Live's replies: %v", err)
	}
	defer listener.Close()
	live := daw.NewAbleton(osc.NewTransport("127.0.0.1", 11000))
	live.Observe(listener.Messages())

	chain, err := live.FXChain(1, 2*time.Second)
	if err != nil {
		t.Fatalf("FXChain: %v", err)
	}
	if len(chain) == 0 {
		t.Skip("track 1 has no devices")
	}
	for _, fx := range chain {
		if fx.Name == "" || len(fx.Params) == 0 {
			t.Errorf("device %d: name %q, %d params", fx.Number, fx.Name, len(fx.Params))
		}
		t.Logf("%d %q: %d params, first %q", fx.Number, fx.Name, len(fx.Params), fx.Params[0].Name)
	}

	// Last device, second parameter (the first is usually Device On).
	target := chain[len(chain)-1]
	param := 2
	if len(target.Params) < 2 {
		param = 1
	}
	before, err := live.ReadFXParam(1, target.Number, param, time.Second)
	if err != nil {
		t.Fatalf("ReadFXParam: %v", err)
	}
	if err := live.SetFXParam(1, target.Number, param, 0.8); err != nil {
		t.Fatalf("SetFXParam: %v", err)
	}
	got, err := live.ConfirmFXParam(1, target.Number, param, time.Second)
	if err != nil {
		t.Fatalf("ConfirmFXParam: %v", err)
	}
	if got < 0.7 || got > 0.9 {
		t.Fatalf("set 0.8 on %q %q, Live reports %v", target.Name, target.Params[param-1].Name, got)
	}
	t.Logf("%q %q: was %v, set 0.8, Live reports %v", target.Name, target.Params[param-1].Name, before, got)
	_ = live.SetFXParam(1, target.Number, param, before)
}
