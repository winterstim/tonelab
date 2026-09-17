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
// query. A listened property is pushed on subscription and, a little after
// each change, on the reply address, as the script does; the delay is there
// because a fake pushing before the set returns hides every ordering bug.
type fakeLive struct {
	tracks  []string
	volume  []float32
	pan     []float32
	mute    []int32
	solo    []int32
	devices map[int][]fakeDevice

	mu        sync.Mutex
	observed  []string
	listeners map[string]bool
	feed      chan<- *goosc.Message
	stopped   chan struct{}
	// pushes go out in order, one script tick apart, as Live sends them.
	pushes chan *goosc.Message
}

const fakePushDelay = 20 * time.Millisecond

// listening reports whether a track's property has a listener. Called
// under the lock.
func (f *fakeLive) listening(prop string, track int) bool {
	return f.listeners[fmt.Sprintf("%s/%d", prop, track)]
}

// push sends the property the way a listener fires: current value, on the
// query's address, after the script's tick. Called under the lock.
func (f *fakeLive) push(prop string, track int) {
	if !f.listening(prop, track) {
		return
	}
	f.pushes <- &goosc.Message{Address: "/live/track/get/" + prop, Arguments: []any{int32(track), f.current(prop, track)}}
}

// current is the property as Live would report it. Called under the lock.
func (f *fakeLive) current(prop string, track int) any {
	switch prop {
	case "volume":
		return f.volume[track]
	case "panning":
		return f.pan[track]
	case "mute":
		return f.mute[track] != 0
	case "solo":
		return f.solo[track] != 0
	}
	return nil
}

// change is a hand on a fader in Live: the value moves and listeners hear
// of it, with no query from this side.
func (f *fakeLive) change(prop string, track int, value float32) {
	f.mu.Lock()
	switch prop {
	case "volume":
		f.volume[track] = value
	case "panning":
		f.pan[track] = value
	case "mute":
		f.mute[track] = int32(value)
	case "solo":
		f.solo[track] = int32(value)
	}
	f.push(prop, track)
	f.mu.Unlock()
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

// received reads the fake's state the way a test asserts on it, under the
// lock the answering goroutine holds.
func (f *fakeLive) received(read func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	read()
}

func (f *fakeLive) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.observed...)
}

func (f *fakeLive) run(receiver *osctest.Receiver, feed chan<- *goosc.Message) {
	f.feed = feed
	f.stopped = make(chan struct{})
	f.listeners = map[string]bool{}
	f.pushes = make(chan *goosc.Message, 64)
	go func() {
		for msg := range f.pushes {
			time.Sleep(fakePushDelay)
			feed <- msg
		}
		close(feed)
	}()
	go func() {
		for {
			msg, ok := receiver.Poll(50 * time.Millisecond)
			select {
			case <-f.stopped:
				close(f.pushes)
				return
			default:
			}
			if !ok {
				continue
			}
			// One lock around the whole answer: the test body reads what
			// Live was sent, and a fake racing its own test proves nothing.
			f.mu.Lock()
			f.observed = append(f.observed, msg.Address)
			reply := f.answer(msg)
			f.mu.Unlock()
			if reply != nil {
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
	if strings.HasPrefix(msg.Address, "/live/track/start_listen/") {
		prop := strings.TrimPrefix(msg.Address, "/live/track/start_listen/")
		f.listeners[fmt.Sprintf("%s/%d", prop, id(0))] = true
		defer f.push(prop, id(0))
		return nil
	}
	if strings.HasPrefix(msg.Address, "/live/track/stop_listen/") {
		delete(f.listeners, fmt.Sprintf("%s/%d", strings.TrimPrefix(msg.Address, "/live/track/stop_listen/"), id(0)))
		return nil
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
	case "/live/track/get/solo":
		return echo(1, f.solo[id(0)])
	// Live's listeners fire on change only, measured: a set to the value
	// already there is silent, which is what confirmation has to handle.
	case "/live/track/set/volume":
		if f.volume[id(0)] != num(1) {
			f.volume[id(0)] = num(1)
			defer f.push("volume", id(0))
		}
	case "/live/track/set/panning":
		if f.pan[id(0)] != num(1) {
			f.pan[id(0)] = num(1)
			defer f.push("panning", id(0))
		}
	case "/live/track/set/mute":
		if f.mute[id(0)] != int32(num(1)) {
			f.mute[id(0)] = int32(num(1))
			defer f.push("mute", id(0))
		}
	case "/live/track/set/solo":
		if f.solo[id(0)] != int32(num(1)) {
			f.solo[id(0)] = int32(num(1))
			defer f.push("solo", id(0))
		}
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
		solo:   []int32{0, 0},
		devices: map[int][]fakeDevice{
			1: {{name: "Amp", params: []string{"Gain", "Bass", "Device On", "Cab"}, min: []float32{0, -12, 0, 0}, max: []float32{10, 12, 1, 3}, value: []float32{5, 0, 1, 0}, quantized: []bool{false, false, true, true}}},
		},
	}
	fake.run(receiver, feed)
	t.Cleanup(func() { close(fake.stopped) })
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
	var pan float32
	fake.received(func() { pan = fake.pan[1] })
	if pan != -0.5 {
		t.Fatalf("expected Live to receive -0.5, got %v", pan)
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
	var raw float32
	fake.received(func() { raw = fake.devices[1][0].value[1] })
	if raw != 6 {
		t.Fatalf("expected 0.75 of -12..12 to reach Live as 6, got %v", raw)
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
	var got float32
	fake.received(func() { got = fake.devices[1][0].value[3] })
	if got != 2 {
		t.Fatalf("expected 0.8 of 0..3 rounded to position 2, Live got %v", got)
	}
}

func near(value any, want float64) bool {
	v, ok := value.(float64)
	return ok && v > want-0.001 && v < want+0.001
}

func count(sent []string, address string) int {
	n := 0
	for _, s := range sent {
		if s == address {
			n++
		}
	}
	return n
}

// A change made by hand in Live reaches the backend without a query: the
// track was subscribed by the first read, and Live pushes from then on.
func TestAbletonHearsAHandChangeWithoutAsking(t *testing.T) {
	live, fake := newLive(t)
	if v, err := live.ReadParam(1, "volume", time.Second); err != nil || !near(v, 0.85) {
		t.Fatalf("first read: %v, %v", v, err)
	}
	fake.change("volume", 0, 0.3)
	time.Sleep(3 * fakePushDelay)
	if v, err := live.ReadParam(1, "volume", time.Second); err != nil || !near(v, 0.3) {
		t.Fatalf("expected the hand change to be heard, got %v, %v", v, err)
	}
	if n := count(fake.sent(), "/live/track/get/volume"); n != 0 {
		t.Fatalf("a subscribed track is never queried, but was %d times", n)
	}
	if n := count(fake.sent(), "/live/track/start_listen/volume"); n != 1 {
		t.Fatalf("one subscription per track, got %d", n)
	}
}

// A set is confirmed from the push it causes, and one that changes nothing
// is confirmed by asking, since Live pushes only changes.
func TestAbletonConfirmsFromThePush(t *testing.T) {
	live, fake := newLive(t)
	if err := live.SetTrackVolume(2, 0.4); err != nil {
		t.Fatal(err)
	}
	if v, err := live.ConfirmParam(2, "volume", time.Second); err != nil || !near(v, 0.4) {
		t.Fatalf("expected 0.4 from the push, got %v, %v", v, err)
	}
	if n := count(fake.sent(), "/live/track/get/volume"); n != 0 {
		t.Fatalf("the push should have confirmed it without a query, but %d were made", n)
	}

	if err := live.SetTrackVolume(2, 0.4); err != nil {
		t.Fatal(err)
	}
	if v, err := live.ConfirmParam(2, "volume", 200*time.Millisecond); err != nil || !near(v, 0.4) {
		t.Fatalf("an unchanged value is still confirmed, got %v, %v", v, err)
	}
	if n := count(fake.sent(), "/live/track/get/volume"); n != 1 {
		t.Fatalf("no push comes for an unchanged value, so one query is right, got %d", n)
	}
}

// Live drops every listener when it restarts. A probe is what runs when
// Live has gone quiet, so a probe that succeeds subscribes again.
func TestAbletonProbeRestoresSubscriptions(t *testing.T) {
	live, fake := newLive(t)
	_, _ = live.ReadParam(1, "mute", time.Second)
	before := count(fake.sent(), "/live/track/start_listen/mute")
	if !live.Probe(time.Second) {
		t.Fatal("probe")
	}
	time.Sleep(fakePushDelay)
	if after := count(fake.sent(), "/live/track/start_listen/mute"); after != before+1 {
		t.Fatalf("expected the probe to subscribe again, %d -> %d", before, after)
	}
}

func TestAbletonCloseStopsListening(t *testing.T) {
	live, fake := newLive(t)
	_, _ = live.ReadParam(1, "volume", time.Second)
	if err := live.Close(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(fakePushDelay)
	for _, prop := range []string{"volume", "panning", "mute", "solo"} {
		if count(fake.sent(), "/live/track/stop_listen/"+prop) != 1 {
			t.Fatalf("expected stop_listen for %s", prop)
		}
	}
}
