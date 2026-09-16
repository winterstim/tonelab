package daw_test

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	goosc "github.com/hypebeast/go-osc/osc"
	"tonelab/backend/daw"
	"tonelab/backend/osc"
	"tonelab/backend/osc/osctest"
)

// fakeFXSurface answers the way a measured REAPER does, which is what makes
// the walk's tests worth anything: selecting a track dumps every FX and the
// first sixteen parameters of each under /fx/N/...; selecting a parameter
// bank announces the bank number and sixteen names under /fxparam/K/name,
// empty past the end; a bank past the last one is silent. Sizes are the
// device's, not the plugin's.
type fakeFXSurface struct {
	chains   map[int][]fakeFX // per track
	selected int
	fx       int
	bank     int

	// What a set comes back as, since plugins round to their own steps.
	quantize func(float32) float32
	// Values by "track/fx/param", dumped with the chain.
	values map[string]float32

	mu       sync.Mutex
	observed []string
}

type fakeFX struct {
	name   string
	params []string
}

const fakeBankSize = 16

var setPattern = regexp.MustCompile(`^/track/([0-9]+)/fx/([0-9]+)/fxparam/([0-9]+)/value$`)

func (f *fakeFXSurface) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.observed...)
}

func (f *fakeFXSurface) run(receiver *osctest.Receiver, feed chan<- *goosc.Message) {
	f.fx, f.bank = 1, 1
	go func() {
		for {
			msg, ok := receiver.Poll(200 * time.Millisecond)
			if !ok {
				close(feed)
				return
			}
			f.mu.Lock()
			f.observed = append(f.observed, msg.Address)
			f.mu.Unlock()
			if len(msg.Arguments) == 0 {
				continue
			}
			if m := setPattern.FindStringSubmatch(msg.Address); m != nil {
				// Echoed whatever the surface looks at, measured; through
				// the plugin's own rounding.
				v, _ := msg.Arguments[0].(float32)
				if f.quantize != nil {
					v = f.quantize(v)
				}
				if f.values == nil {
					f.values = make(map[string]float32)
				}
				f.values[fmt.Sprintf("%s/%s/%s", m[1], m[2], m[3])] = v
				feed <- goosc.NewMessage(msg.Address, v)
				continue
			}
			requested, ok := msg.Arguments[0].(int32)
			if !ok {
				continue
			}
			target := int(requested)

			switch msg.Address {
			case "/device/track/select":
				if target == f.selected {
					continue
				}
				f.selected, f.fx, f.bank = target, 1, 1
				feed <- goosc.NewMessage("/track/name", fmt.Sprintf("Track %d", target))
				for i, fx := range f.chains[target] {
					feed <- goosc.NewMessage(fmt.Sprintf("/fx/%d/name", i+1), fx.name)
					for k, name := range fx.params {
						if k >= fakeBankSize {
							break
						}
						feed <- goosc.NewMessage(fmt.Sprintf("/fx/%d/fxparam/%d/name", i+1, k+1), name)
						value, ok := f.values[fmt.Sprintf("%d/%d/%d", target, i+1, k+1)]
						if !ok {
							value = 0.5
						}
						feed <- goosc.NewMessage(fmt.Sprintf("/fx/%d/fxparam/%d/value", i+1, k+1), value)
					}
				}
				f.announceBank(feed)
			case "/device/fx/select":
				if target == f.fx || target > len(f.chains[f.selected]) {
					continue
				}
				f.fx = target
				f.announceBank(feed)
			case "/device/fxparam/bank/select":
				chain := f.chains[f.selected]
				if target == f.bank || f.fx > len(chain) || (target-1)*fakeBankSize >= len(chain[f.fx-1].params) {
					continue // a bank past the end is silent, measured
				}
				f.bank = target
				feed <- goosc.NewMessage("/device/fxparam/bank/str", fmt.Sprint(target))
				f.announceBank(feed)
			}
		}
	}()
}

func (f *fakeFXSurface) announceBank(feed chan<- *goosc.Message) {
	chain := f.chains[f.selected]
	if f.fx > len(chain) {
		return
	}
	fx := chain[f.fx-1]
	feed <- goosc.NewMessage("/fx/name", fx.name)
	for k := 1; k <= fakeBankSize; k++ {
		index := (f.bank-1)*fakeBankSize + k - 1
		name := ""
		if index < len(fx.params) {
			name = fx.params[index]
		}
		feed <- goosc.NewMessage(fmt.Sprintf("/fxparam/%d/name", k), name)
	}
}

func knobs(n int) []string {
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("Knob %d", i+1)
	}
	return names
}

func newFXReaper(t *testing.T, chains map[int][]fakeFX) (*daw.REAPER, *fakeFXSurface) {
	t.Helper()
	receiver := osctest.NewReceiver(t)
	reaper := daw.NewREAPER(osc.NewTransport("127.0.0.1", receiver.Port))
	feed := make(chan *goosc.Message, 1024)
	reaper.Observe(feed)
	fake := &fakeFXSurface{chains: chains, selected: 1}
	fake.run(receiver, feed)
	return reaper, fake
}

// A plugin's parameters arrive sixteen at a time, so a walk that stopped at
// the first bank would hand the model a third of an amp simulator.
func TestFXChainWalksEveryBank(t *testing.T) {
	reaper, _ := newFXReaper(t, map[int][]fakeFX{
		1: {{name: "Amp", params: knobs(40)}, {name: "EQ", params: []string{"Low", "Mid", "High"}}},
	})

	chain, err := reaper.FXChain(1, time.Second)
	if err != nil {
		t.Fatalf("FXChain returned an error: %v", err)
	}

	if len(chain) != 2 {
		t.Fatalf("expected two effects, got %v", chain)
	}
	if chain[0].Name != "Amp" || len(chain[0].Params) != 40 {
		t.Fatalf("expected Amp with 40 params, got %q with %d", chain[0].Name, len(chain[0].Params))
	}
	if chain[0].Params[39].Number != 40 || chain[0].Params[39].Name != "Knob 40" {
		t.Fatalf("expected the last param numbered 40 and named, got %+v", chain[0].Params[39])
	}
	if chain[1].Name != "EQ" || len(chain[1].Params) != 3 {
		t.Fatalf("expected EQ with 3 params, got %+v", chain[1])
	}
}

// A track with nothing on it is an answer, not a failure.
func TestFXChainOfAnEmptyTrack(t *testing.T) {
	reaper, _ := newFXReaper(t, map[int][]fakeFX{1: {{name: "EQ", params: []string{"Low"}}}, 2: nil})

	chain, err := reaper.FXChain(2, time.Second)
	if err != nil {
		t.Fatalf("FXChain returned an error: %v", err)
	}
	if len(chain) != 0 {
		t.Fatalf("expected no effects, got %v", chain)
	}
}

// The walk starts on the track the surface is already looking at, where a
// DAW announcing only transitions would otherwise say nothing.
func TestFXChainOfTheTrackAlreadySelected(t *testing.T) {
	reaper, _ := newFXReaper(t, map[int][]fakeFX{1: {{name: "Comp", params: []string{"Threshold", "Ratio"}}}})

	chain, err := reaper.FXChain(1, time.Second)
	if err != nil {
		t.Fatalf("FXChain returned an error: %v", err)
	}
	if len(chain) != 1 || chain[0].Name != "Comp" || len(chain[0].Params) != 2 {
		t.Fatalf("expected Comp with two params, got %v", chain)
	}
}

// Asking what is on a track must not change the project: only the surface's
// own view may move. Held here, without a DAW, so it guards every run.
func TestFXChainTouchesOnlyTheControlSurface(t *testing.T) {
	reaper, fake := newFXReaper(t, map[int][]fakeFX{1: {{name: "Amp", params: knobs(20)}}})

	if _, err := reaper.FXChain(1, time.Second); err != nil {
		t.Fatalf("FXChain returned an error: %v", err)
	}
	for _, address := range fake.sent() {
		if !strings.HasPrefix(address, "/device/") {
			t.Errorf("FXChain sent %s, which reaches the project", address)
		}
	}
	if len(fake.sent()) == 0 {
		t.Fatal("nothing was sent")
	}
}

// A set is confirmed by what the DAW echoes, which for an effect parameter is
// the value as the plugin quantized it rather than the one sent, measured as
// 0.7 coming back 0.708. The confirmation therefore reports the echo.
func TestFXParamSetIsConfirmedFromTheEcho(t *testing.T) {
	reaper, fake := newFXReaper(t, map[int][]fakeFX{1: {{name: "Amp", params: knobs(3)}}})
	fake.quantize = func(v float32) float32 { return float32(int(v*12)) / 12 }

	seen := reaper.Observed()
	if err := reaper.SetFXParam(1, 1, 2, 0.7); err != nil {
		t.Fatalf("SetFXParam: %v", err)
	}
	value, err := reaper.ConfirmFXParam(1, 1, 2, time.Second)
	if err != nil {
		t.Fatalf("ConfirmFXParam: %v", err)
	}
	if value < 0.66 || value > 0.67 {
		t.Fatalf("expected the quantized echo near 0.667, got %v", value)
	}
	if reaper.Observed() == seen {
		t.Fatal("nothing was observed, so the value cannot have come from the DAW")
	}
}

// Reading a value nobody has set means making the DAW dump the track, which
// names values relative to the surface's view; the backend has to know which
// track that view is on to file them under the right number.
func TestFXParamReadComesFromTheTrackDump(t *testing.T) {
	reaper, fake := newFXReaper(t, map[int][]fakeFX{
		1: {{name: "Amp", params: knobs(3)}},
		2: {{name: "Verb", params: []string{"Size", "Mix"}}},
	})
	fake.values = map[string]float32{"2/1/2": 0.25}

	value, err := reaper.ReadFXParam(2, 1, 2, time.Second)
	if err != nil {
		t.Fatalf("ReadFXParam: %v", err)
	}
	if value != 0.25 {
		t.Fatalf("expected 0.25 from the dump, got %v", value)
	}
	for _, address := range fake.sent() {
		if !strings.HasPrefix(address, "/device/") {
			t.Errorf("reading sent %s, which reaches the project", address)
		}
	}
}

func TestFXParamRefusesWhatTheDAWWouldClamp(t *testing.T) {
	reaper, _ := newFXReaper(t, map[int][]fakeFX{1: {{name: "Amp", params: knobs(3)}}})
	for _, tc := range []struct {
		name           string
		track, fx, prm int
		value          float64
	}{
		{"track 0", 0, 1, 1, 0.5},
		{"fx 0", 1, 0, 1, 0.5},
		{"param 0", 1, 1, 0, 0.5},
		{"above one", 1, 1, 1, 1.5},
		{"NaN", 1, 1, 1, math.NaN()},
	} {
		if err := reaper.SetFXParam(tc.track, tc.fx, tc.prm, tc.value); err == nil {
			t.Errorf("%s: expected a refusal", tc.name)
		}
	}
}
