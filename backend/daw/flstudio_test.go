package daw_test

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"tonelab/backend/daw"
	"tonelab/backend/daw/dawtest"
	"tonelab/backend/midi"
)

// fakeFL stands for the bridge script inside FL Studio, on the same Link
// the real port implements: one JSON object per sysex, ids echoed, ASCII
// only. It applies mutes and solos one tick late, as FL does (measured:
// a read straight after the set can still show the old value), and
// answers volume at once. Effects sit in numbered slots with gaps.
type fakeFL struct {
	mu      sync.Mutex
	in      chan []byte
	tracks  []string
	volume  []float64
	pan     []float64
	mute    []bool
	solo    []bool
	slots   map[int]map[int]fakeFLPlugin
	sent    []string
	tick    time.Duration
	closed  bool
	pending []func()
}

type fakeFLPlugin struct {
	name   string
	params []string
	values []float64
	strs   []string
}

func newFakeFL() *fakeFL {
	return &fakeFL{
		in:     make(chan []byte, 64),
		tracks: []string{"Master", "Insert 1", "Insert 2", "Insert 3"},
		volume: []float64{0.8, 0.8, 0.8, 0.8},
		pan:    []float64{0, 0, 0, 0},
		mute:   make([]bool, 4),
		solo:   make([]bool, 4),
		slots: map[int]map[int]fakeFLPlugin{
			1: {
				2: {name: "Fruity Reeverb 2", params: []string{"Room size", "Wet", "Bypass"}, values: []float64{0.5, 0.3, 0}, strs: []string{"50%", "-10.5 dB", "Off"}},
				7: {name: "Emphasizer", params: []string{"Emphasis", "Clipping"}, values: []float64{0.5, 0}, strs: []string{"50%", "Hard"}},
				// A wrapped plugin as FL's wrapper reports it: one named
				// control among nameless ones, then the wrapper's MIDI map.
				9: {name: "AUReverb2", params: []string{"", "", "legacy mode", "", "MIDI CC #0 (Bank select MSB)", "MIDI Channel 1 Aftertouch"}, values: []float64{0.5, 1, 1, 0, 0, 0}, strs: []string{"0.50", "1.00", "1.00", "", "", ""}},
			},
		},
		tick: 5 * time.Millisecond,
	}
}

func (f *fakeFL) Messages() <-chan []byte { return f.in }
func (f *fakeFL) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		close(f.in)
	}
	return nil
}

func (f *fakeFL) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sent...)
}

func (f *fakeFL) Send(message []byte) error {
	if message[0] != midi.SysExStart || message[1] != 0x7D || message[len(message)-1] != midi.SysExEnd {
		return nil
	}
	for _, b := range message {
		if b >= 0x80 && b != midi.SysExStart && b != midi.SysExEnd {
			panic("eight-bit byte inside sysex")
		}
	}
	var request map[string]any
	if err := json.Unmarshal(message[2:len(message)-1], &request); err != nil {
		return nil
	}
	f.mu.Lock()
	f.sent = append(f.sent, request["op"].(string))
	body := f.handle(request)
	f.mu.Unlock()
	body["id"] = request["id"]
	out := daw.MustASCII(nil, body)
	go func() {
		time.Sleep(time.Millisecond)
		f.mu.Lock()
		defer f.mu.Unlock()
		if !f.closed {
			f.in <- append(append([]byte{midi.SysExStart, 0x7D}, out...), midi.SysExEnd)
		}
	}()
	return nil
}

func (f *fakeFL) later(apply func()) {
	go func() {
		time.Sleep(f.tick)
		f.mu.Lock()
		apply()
		f.mu.Unlock()
	}()
}

func (f *fakeFL) handle(r map[string]any) map[string]any {
	num := func(k string) int { v, _ := r[k].(float64); return int(v) }
	switch r["op"] {
	case "ping":
		return map[string]any{"ok": true, "version": 45}
	case "tracks":
		var list []map[string]any
		for i := 1; i < len(f.tracks); i++ {
			list = append(list, map[string]any{"n": i, "name": f.tracks[i]})
		}
		return map[string]any{"tracks": list}
	case "undo", "play", "stop":
		return map[string]any{"ok": true}
	}
	track := num("track")
	if track < 0 || track >= len(f.tracks) {
		return map[string]any{"error": "no such track"}
	}
	switch r["op"] {
	case "get":
		switch r["name"] {
		case "volume":
			return map[string]any{"value": f.volume[track]}
		case "pan":
			return map[string]any{"value": f.pan[track]}
		case "mute":
			return map[string]any{"value": boolNum(f.mute[track])}
		case "solo":
			return map[string]any{"value": boolNum(f.solo[track])}
		}
		return map[string]any{"error": "no such parameter"}
	case "set":
		v, _ := r["value"].(float64)
		on, _ := r["value"].(bool)
		switch r["name"] {
		case "volume":
			f.volume[track] = v
		case "pan":
			f.pan[track] = v
		case "mute":
			f.later(func() { f.mute[track] = on })
		case "solo":
			f.later(func() { f.solo[track] = on })
		default:
			return map[string]any{"error": "no such parameter"}
		}
		return map[string]any{"ok": true}
	case "route":
		return map[string]any{"ok": true}
	case "fx":
		var chain []map[string]any
		for slot := 0; slot < 10; slot++ {
			p, ok := f.slots[track][slot]
			if !ok {
				continue
			}
			chain = append(chain, map[string]any{"slot": slot, "name": p.name, "count": len(p.params)})
		}
		return map[string]any{"fx": chain}
	case "params":
		p, ok := f.slots[track][num("slot")]
		if !ok {
			return map[string]any{"error": "no such effect"}
		}
		from, n := num("from"), num("n")
		var page [][]any
		for i := from; i < len(p.params) && i < from+n; i++ {
			page = append(page, []any{i, p.params[i], p.strs[i]})
		}
		return map[string]any{"params": page}
	}
	p, ok := f.slots[track][num("slot")]
	param := num("param")
	if !ok || param < 0 || param >= len(p.params) {
		return map[string]any{"error": "no such effect parameter"}
	}
	switch r["op"] {
	case "fxget":
		return map[string]any{"value": p.values[param], "str": p.strs[param]}
	case "fxset":
		v, _ := r["value"].(float64)
		p.values[param] = v
		return map[string]any{"ok": true}
	}
	return map[string]any{"error": "unknown op"}
}

func boolNum(on bool) int {
	if on {
		return 1
	}
	return 0
}

func newFL(t *testing.T) (*daw.FLStudio, *fakeFL) {
	t.Helper()
	fake := newFakeFL()
	fl := daw.NewFLStudio(fake)
	t.Cleanup(func() { fl.Close() })
	return fl, fake
}

func TestFLStudioMeetsTheClientContract(t *testing.T) {
	fl, _ := newFL(t)
	dawtest.AssertClientContract(t, fl)
}

func TestFLStudioTracksSkipTheMaster(t *testing.T) {
	fl, _ := newFL(t)
	tracks, err := fl.Tracks(time.Second)
	if err != nil || len(tracks) != 3 || tracks[0].Number != 1 || tracks[0].Name != "Insert 1" {
		t.Fatalf("got %v, %v", tracks, err)
	}
}

func TestFLStudioPanIsTranslated(t *testing.T) {
	fl, fake := newFL(t)
	if err := fl.SetTrackPan(2, 0.25); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	fake.mu.Lock()
	raw := fake.pan[2]
	fake.mu.Unlock()
	if raw != -0.5 {
		t.Fatalf("FL should receive -0.5, got %v", raw)
	}
	if v, err := fl.ConfirmParam(2, "pan", time.Second); err != nil || v != 0.25 {
		t.Fatalf("expected 0.25 back, got %v, %v", v, err)
	}
}

// A mute lands on FL's tick, so a confirmation that read once would report
// the value from before the set. It reads until FL agrees.
func TestFLStudioConfirmsAToggleAfterFLsTick(t *testing.T) {
	fl, _ := newFL(t)
	if err := fl.SetTrackMute(1, true); err != nil {
		t.Fatal(err)
	}
	if v, err := fl.ConfirmParam(1, "mute", time.Second); err != nil || v != true {
		t.Fatalf("expected true once FL applied it, got %v, %v", v, err)
	}
	// A plain read reports whatever FL has now, without waiting on anything.
	if v, err := fl.ReadParam(1, "mute", time.Second); err != nil || v != true {
		t.Fatalf("read: %v, %v", v, err)
	}
}

func TestFLStudioConfirmReportsFLsAccountWhenTheSetDidNotLand(t *testing.T) {
	fl, fake := newFL(t)
	fake.tick = time.Second
	if err := fl.SetTrackSolo(1, true); err != nil {
		t.Fatal(err)
	}
	v, err := fl.ConfirmParam(1, "solo", 50*time.Millisecond)
	if err != nil || v != false {
		t.Fatalf("a set FL has not applied is reported as FL sees it, got %v, %v", v, err)
	}
}

// FL numbers effects by slot and leaves gaps; the chain is numbered
// without them, and a write by chain number reaches the right slot.
func TestFLStudioChainHidesSlotGaps(t *testing.T) {
	fl, fake := newFL(t)
	chain, err := fl.FXChain(1, time.Second)
	if err != nil || len(chain) != 3 {
		t.Fatalf("got %v, %v", chain, err)
	}
	if wrapped := chain[2]; len(wrapped.Params) != 1 || wrapped.Params[0].Number != 3 || wrapped.Params[0].Name != "legacy mode" {
		t.Fatalf("a wrapped plugin keeps its one named control under FL's own number, got %+v", wrapped.Params)
	}
	if err := fl.SetFXParam(1, 3, 3, 0.7); err != nil {
		t.Fatalf("writing by FL's number: %v", err)
	}
	if v, err := fl.ConfirmFXParam(1, 3, 3, time.Second); err != nil || v != 0.7 {
		t.Fatalf("expected 0.7 on the named control, got %v, %v", v, err)
	}
	if chain[0].Number != 1 || chain[0].Name != "Fruity Reeverb 2" || chain[1].Number != 2 || chain[1].Name != "Emphasizer" {
		t.Fatalf("chain numbered by position, got %+v", chain)
	}
	if chain[0].Params[2].Kind != "switch" || chain[0].Params[1].Kind != "" || chain[1].Params[1].Kind != "discrete" {
		t.Fatalf("kinds from readouts: %+v %+v", chain[0].Params, chain[1].Params)
	}
	if err := fl.SetFXParam(1, 2, 1, 0.9); err != nil {
		t.Fatal(err)
	}
	if v, err := fl.ConfirmFXParam(1, 2, 1, time.Second); err != nil || v != 0.9 {
		t.Fatalf("expected 0.9 from Emphasizer, got %v, %v", v, err)
	}
	fake.mu.Lock()
	landed := fake.slots[1][7].values[0]
	fake.mu.Unlock()
	if landed != 0.9 {
		t.Fatalf("fx 2 must map to slot 7, slot 7 holds %v", landed)
	}
	if err := fl.SetFXParam(1, 4, 1, 0.5); err == nil {
		t.Fatal("an effect beyond the chain must be refused, not sent to a slot")
	}
	if _, err := fl.FXChain(2, time.Second); err != nil {
		t.Fatalf("an empty track is an empty chain, not an error: %v", err)
	}
}

func TestFLStudioSilenceIsAnError(t *testing.T) {
	fake := newFakeFL()
	fake.closed = true
	close(fake.in)
	fl := daw.NewFLStudio(fake)
	if _, err := fl.ReadParam(1, "volume", 30*time.Millisecond); err == nil {
		t.Fatal("no reply must be an error, not a stale number")
	}
	if fl.Probe(30 * time.Millisecond) {
		t.Fatal("a silent FL is not alive")
	}
}

func TestFLStudioSpeaksASCIIOnly(t *testing.T) {
	fl, fake := newFL(t)
	fake.tracks[1] = "Größe"
	// The fake panics on an eight-bit byte; the request carrying a name
	// would be the set, which never carries one, so check the encoder itself.
	tracks, err := fl.Tracks(time.Second)
	if err != nil || tracks[0].Name != "Größe" {
		t.Fatalf("a non-ASCII name must survive the seven-bit channel, got %v, %v", tracks, err)
	}
	if !strings.Contains(string(daw.MustASCII(t, map[string]any{"name": "Größe"})), "\\u00f6") {
		t.Fatal("runes above 127 must be escaped")
	}
}
