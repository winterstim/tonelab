package daw_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	goosc "github.com/hypebeast/go-osc/osc"
	"tonelab/backend/daw"
	"tonelab/backend/daw/dawtest"
	"tonelab/backend/osc"
	"tonelab/backend/osc/osctest"
)

// fakeLive answers the way AbletonOSC's README says it does: a reply carries
// the query's address, the ids that were sent, then the values; indices are
// zero-based; panning is -1..1; device parameters are in their own units
// with min and max on request. A set is applied and answered on the next
// query, since the script has no push of its own without a listener.
type fakeLive struct {
	tracks  []string
	volume  []float32
	pan     []float32
	mute    []int32
	devices map[int][]fakeDevice

	mu       sync.Mutex
	observed []string
}

type fakeDevice struct {
	name   string
	params []string
	min    []float32
	max    []float32
	value  []float32
	// Which parameters step, as Live reports.
	quantized []bool
}

func (f *fakeLive) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.observed...)
}

func (f *fakeLive) run(receiver *osctest.Receiver, feed chan<- *goosc.Message) {
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
			if reply := f.answer(msg); reply != nil {
				feed <- reply
			}
		}
	}()
}

func (f *fakeLive) answer(msg *goosc.Message) *goosc.Message {
	id := func(i int) int { v, _ := msg.Arguments[i].(int32); return int(v) }
	num := func(i int) float32 {
		switch v := msg.Arguments[i].(type) {
		case float32:
			return v
		case int32:
			return float32(v)
		}
		return 0
	}
	echo := func(n int, values ...any) *goosc.Message {
		reply := goosc.NewMessage(msg.Address)
		reply.Arguments = append(append([]any{}, msg.Arguments[:n]...), values...)
		return reply
	}
	switch msg.Address {
	case "/live/test":
		return echo(0, "ok")
	case "/live/song/get/track_names":
		names := make([]any, 0, len(f.tracks))
		for _, t := range f.tracks {
			names = append(names, t)
		}
		return echo(0, names...)
	case "/live/track/get/volume":
		return echo(1, f.volume[id(0)])
	case "/live/track/get/panning":
		return echo(1, f.pan[id(0)])
	case "/live/track/get/mute":
		return echo(1, f.mute[id(0)])
	case "/live/track/set/volume":
		f.volume[id(0)] = num(1)
	case "/live/track/set/panning":
		f.pan[id(0)] = num(1)
	case "/live/track/set/mute":
		f.mute[id(0)] = int32(num(1))
	case "/live/track/get/devices/name":
		var names []any
		for _, d := range f.devices[id(0)] {
			names = append(names, d.name)
		}
		return echo(1, names...)
	case "/live/device/get/parameters/name", "/live/device/get/parameters/min", "/live/device/get/parameters/max":
		d := f.devices[id(0)][id(1)]
		var out []any
		for i := range d.params {
			switch {
			case strings.HasSuffix(msg.Address, "name"):
				out = append(out, d.params[i])
			case strings.HasSuffix(msg.Address, "min"):
				out = append(out, d.min[i])
			default:
				out = append(out, d.max[i])
			}
		}
		return echo(2, out...)
	case "/live/device/get/parameters/is_quantized":
		d := f.devices[id(0)][id(1)]
		var out []any
		for i := range d.params {
			out = append(out, d.quantized != nil && d.quantized[i])
		}
		return echo(2, out...)
	case "/live/device/get/parameter/value":
		return echo(3, f.devices[id(0)][id(1)].value[id(2)])
	case "/live/device/set/parameter/value":
		f.devices[id(0)][id(1)].value[id(2)] = num(3)
	}
	return nil
}

func newLive(t *testing.T) (*daw.Ableton, *fakeLive) {
	t.Helper()
	receiver := osctest.NewReceiver(t)
	live := daw.NewAbleton(osc.NewTransport("127.0.0.1", receiver.Port))
	feed := make(chan *goosc.Message, 64)
	live.Observe(feed)
	fake := &fakeLive{
		tracks: []string{"Drums", "Vocals"},
		volume: []float32{0.85, 0.85},
		pan:    []float32{0, 0},
		mute:   []int32{0, 0},
		devices: map[int][]fakeDevice{
			1: {{name: "Amp", params: []string{"Gain", "Bass", "Device On", "Cab"}, min: []float32{0, -12, 0, 0}, max: []float32{10, 12, 1, 3}, value: []float32{5, 0, 1, 0}, quantized: []bool{false, false, true, true}}},
		},
	}
	fake.run(receiver, feed)
	return live, fake
}

func TestAbletonMeetsTheClientContract(t *testing.T) {
	live, _ := newLive(t)
	dawtest.AssertClientContract(t, live)
}

// The DAW counts from zero and the user from one; the boundary is here and
// nowhere above.
func TestAbletonTracksAreNumberedFromOne(t *testing.T) {
	live, _ := newLive(t)
	tracks, err := live.Tracks(time.Second)
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if fmt.Sprint(tracks) != fmt.Sprint([]daw.Track{{1, "Drums"}, {2, "Vocals"}}) {
		t.Fatalf("unexpected tracks: %v", tracks)
	}
}

// Panning is -1..1 in Live and 0..1 across this layer, in both directions.
func TestAbletonPanIsTranslated(t *testing.T) {
	live, fake := newLive(t)
	if err := live.SetTrackPan(2, 0.25); err != nil {
		t.Fatalf("SetTrackPan: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if fake.pan[1] != -0.5 {
		t.Fatalf("expected Live to receive -0.5, got %v", fake.pan[1])
	}
	value, err := live.ReadParam(2, "pan", time.Second)
	if err != nil || value != 0.25 {
		t.Fatalf("expected 0.25 back, got %v, %v", value, err)
	}
}

func TestAbletonReadsToggles(t *testing.T) {
	live, _ := newLive(t)
	if err := live.SetParam(1, "mute", true); err != nil {
		t.Fatalf("SetParam: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	value, err := live.ConfirmParam(1, "mute", time.Second)
	if err != nil || value != true {
		t.Fatalf("expected true, got %v, %v", value, err)
	}
}

// Device parameters have their own ranges; 0..1 here means the whole range
// there, and a read translates back so the layer above never sees units.
func TestAbletonFXParamsAreNormalizedAcrossTheRange(t *testing.T) {
	live, fake := newLive(t)
	chain, err := live.FXChain(2, time.Second)
	if err != nil {
		t.Fatalf("FXChain: %v", err)
	}
	if len(chain) != 1 || chain[0].Name != "Amp" || len(chain[0].Params) != 4 || chain[0].Params[1].Name != "Bass" {
		t.Fatalf("unexpected chain: %+v", chain)
	}

	if err := live.SetFXParam(2, 1, 2, 0.75); err != nil {
		t.Fatalf("SetFXParam: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if fake.devices[1][0].value[1] != 6 {
		t.Fatalf("expected 0.75 of -12..12 to reach Live as 6, got %v", fake.devices[1][0].value[1])
	}
	got, err := live.ConfirmFXParam(2, 1, 2, time.Second)
	if err != nil || got != 0.75 {
		t.Fatalf("expected 0.75 back, got %v, %v", got, err)
	}
	if got, _ := live.ReadFXParam(2, 1, 1, time.Second); got != 0.5 {
		t.Fatalf("expected Gain 5 of 0..10 to read as 0.5, got %v", got)
	}
}

// A question a DAW that answers questions did not answer is a missing DAW.
func TestAbletonSilenceIsAnError(t *testing.T) {
	receiver := osctest.NewReceiver(t)
	live := daw.NewAbleton(osc.NewTransport("127.0.0.1", receiver.Port))
	live.Observe(make(chan *goosc.Message))
	if _, err := live.ReadParam(1, "volume", time.Second); !errors.Is(err, daw.ErrValueUnknown) {
		t.Fatalf("expected ErrValueUnknown, got %v", err)
	}
	if live.Probe(time.Second) {
		t.Fatal("a silent DAW must not probe as alive")
	}
}

func TestAbletonProbeAsksAndHears(t *testing.T) {
	live, _ := newLive(t)
	if !live.Probe(time.Second) {
		t.Fatal("expected the fake to answer /live/test")
	}
}

// The script can rename tracks, fire clips and more; none of that is
// reachable from here, and the list is what says so.
func TestAbletonRefusesWhatIsNotListed(t *testing.T) {
	live, fake := newLive(t)
	live.Tracks(time.Second)
	live.SetTrackVolume(1, 0.5)
	for _, address := range fake.sent() {
		if strings.HasPrefix(address, "/live/track/set/name") || strings.HasPrefix(address, "/live/clip") {
			t.Errorf("sent %s", address)
		}
	}
	if err := live.SetParam(1, "name", "x"); err == nil {
		t.Fatal("name is not a parameter here")
	}
}

// A reply to an earlier, timed-out question must not answer a later one to
// the same address: the ids have to match, not only the address.
func TestAbletonIgnoresRepliesToOtherQuestions(t *testing.T) {
	receiver := osctest.NewReceiver(t)
	live := daw.NewAbleton(osc.NewTransport("127.0.0.1", receiver.Port))
	feed := make(chan *goosc.Message, 8)
	live.Observe(feed)

	// Answers every volume query with track 0's value, which is what a
	// stale reply looks like to a caller asking about track 2.
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	go func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			msg, ok := receiver.Poll(200 * time.Millisecond)
			if !ok {
				continue // a quiet spell, not the end
			}
			if msg.Address == "/live/track/get/volume" {
				feed <- goosc.NewMessage(msg.Address, int32(0), float32(0.85))
			}
		}
	}()

	if _, err := live.ReadParam(2, "volume", 300*time.Millisecond); !errors.Is(err, daw.ErrValueUnknown) {
		t.Fatalf("expected the wrong track's reply to be ignored, got %v", err)
	}
	value, err := live.ReadParam(1, "volume", time.Second)
	if v, _ := value.(float64); err != nil || v < 0.84 || v > 0.86 {
		t.Fatalf("expected track 1 answered, got %v, %v", value, err)
	}
}

// Live says which parameters step and the range counts the steps; a set
// to one lands on a whole position, since 0.8 of two states is not a state.
func TestAbletonSteppedParametersAreKnownAndRounded(t *testing.T) {
	live, fake := newLive(t)
	chain, err := live.FXChain(2, time.Second)
	if err != nil {
		t.Fatalf("FXChain: %v", err)
	}
	params := chain[0].Params
	if params[0].Kind != "" || params[2].Kind != "switch" || params[2].Steps != 2 || params[3].Kind != "list" || params[3].Steps != 4 {
		t.Fatalf("unexpected kinds: %+v", params)
	}
	if err := live.SetFXParam(2, 1, 4, 0.8); err != nil {
		t.Fatalf("SetFXParam: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if fake.devices[1][0].value[3] != 2 {
		t.Fatalf("expected 0.8 of 0..3 rounded to position 2, Live got %v", fake.devices[1][0].value[3])
	}
}
