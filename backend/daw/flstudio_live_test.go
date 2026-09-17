//go:build flstudio

package daw_test

import (
	"testing"
	"time"

	"tonelab/backend/daw"
	"tonelab/backend/midi"
)

// Against a running FL Studio with the Tonelab input assigned to the
// Tonelab controller script. Nothing about the project is assumed beyond
// one mixer insert; values are put back afterwards.
func TestFLStudioLive(t *testing.T) {
	port, err := midi.Open("Tonelab")
	if err != nil {
		t.Fatal(err)
	}
	fl := daw.NewFLStudio(port)
	defer fl.Close()
	if path, err := fl.Install(); err != nil {
		t.Fatal(err)
	} else {
		t.Logf("script at %s", path)
	}
	// The port exists only while this process runs, so the user may need
	// this window to assign it in FL's MIDI settings.
	answered := false
	for waited := 0; waited < 90 && !answered; waited++ {
		answered = fl.Probe(time.Second)
	}
	if !answered {
		t.Fatal("FL did not answer; is the Tonelab input assigned to the Tonelab script, with matching ports?")
	}

	started := time.Now()
	tracks, err := fl.Tracks(time.Second)
	if err != nil || len(tracks) == 0 {
		t.Fatalf("Tracks: %v (%v)", err, tracks)
	}
	t.Logf("%d tracks in %s, first %q", len(tracks), time.Since(started), tracks[0].Name)

	before, err := fl.ReadParam(1, "volume", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	started = time.Now()
	if err := fl.SetTrackVolume(1, 0.25); err != nil {
		t.Fatal(err)
	}
	got, err := fl.ConfirmParam(1, "volume", time.Second)
	if err != nil || got.(float64) < 0.24 || got.(float64) > 0.26 {
		t.Fatalf("set 0.25, FL reports %v, %v", got, err)
	}
	t.Logf("volume: was %v, set 0.25, FL reports %v, set and confirm in %s", before, got, time.Since(started))
	_ = fl.SetTrackVolume(1, before.(float64))

	if err := fl.SetTrackPan(1, 0.25); err != nil {
		t.Fatal(err)
	}
	if pan, err := fl.ConfirmParam(1, "pan", time.Second); err != nil || pan.(float64) < 0.24 || pan.(float64) > 0.26 {
		t.Fatalf("set pan 0.25, read back %v, %v", pan, err)
	}
	_ = fl.SetTrackPan(1, 0.5)

	if err := fl.SetTrackMute(1, true); err != nil {
		t.Fatal(err)
	}
	if muted, err := fl.ConfirmParam(1, "mute", time.Second); err != nil || muted != true {
		t.Fatalf("mute: %v, %v", muted, err)
	}
	_ = fl.SetTrackMute(1, false)

	exercised := false
	for _, track := range tracks {
		started = time.Now()
		chain, err := fl.FXChain(track.Number, 5*time.Second)
		if err != nil {
			t.Fatalf("FXChain %d: %v", track.Number, err)
		}
		if len(chain) == 0 {
			continue
		}
		t.Logf("track %d %q: %d effects in %s; first %q with %d named params", track.Number, track.Name, len(chain), time.Since(started), chain[0].Name, len(chain[0].Params))
		for _, p := range chain[0].Params[:min(4, len(chain[0].Params))] {
			t.Logf("  %d %q kind=%q", p.Number, p.Name, p.Kind)
		}
		if len(chain[0].Params) == 0 || exercised {
			continue
		}
		exercised = true
		param := chain[0].Params[0].Number
		was, err := fl.ReadFXParam(track.Number, 1, param, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if err := fl.SetFXParam(track.Number, 1, param, 0.7); err != nil {
			t.Fatal(err)
		}
		now, err := fl.ConfirmFXParam(track.Number, 1, param, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("  param %d: was %v, set 0.7, FL reports %v", param, was, now)
		_ = fl.SetFXParam(track.Number, 1, param, was)
	}
	if !exercised {
		t.Log("no mixer track with a named effect parameter; effect round trip not exercised")
	}
}
