package daw

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	goosc "github.com/hypebeast/go-osc/osc"
)

// Ableton speaks to Live through the AbletonOSC remote script. It differs
// from REAPER in every way the Client seam was built to absorb: it answers
// queries (a reply carries the query's address and ids), indices start at
// zero, panning runs -1..1, and device parameters come in their own units
// with the range available on request. All of that stays inside this file.
type Ableton struct {
	osc Sender

	// One question at a time: replies are matched by address, and two
	// outstanding queries to one address could not be told apart.
	asking sync.Mutex
	mu     sync.Mutex
	waiter chan *goosc.Message
	want   string

	observed atomic.Uint64
	lastSeen atomic.Int64

	// Last answers, so GetParam can report without asking again.
	cache map[string]any
	// Parameter ranges per device, since a set has to speak the device's
	// own units and a read has to translate back.
	ranges map[string][2][]float64
	// Which parameters step, per device, learned with the ranges.
	steps map[string]map[int]bool
}

var _ Client = (*Ableton)(nil)

func NewAbleton(sender Sender) *Ableton {
	return &Ableton{osc: sender, cache: make(map[string]any), ranges: make(map[string][2][]float64), steps: make(map[string]map[int]bool)}
}

const abletonReplyTimeout = 2 * time.Second

// Every address this backend may send. Setting a track's name or a clip is
// not among them: the agent changes mix and device parameters, nothing else.
var abletonAllowed = []*regexp.Regexp{
	regexp.MustCompile(`^/live/test$`),
	regexp.MustCompile(`^/live/song/(start_playing|stop_playing|undo)$`),
	regexp.MustCompile(`^/live/song/get/(num_tracks|track_names)$`),
	regexp.MustCompile(`^/live/track/get/(volume|panning|mute|solo|send|name|num_devices|devices/name)$`),
	regexp.MustCompile(`^/live/track/set/(volume|panning|mute|solo|send)$`),
	regexp.MustCompile(`^/live/device/get/(name|num_parameters|parameters/(name|min|max|value|is_quantized)|parameter/value)$`),
	regexp.MustCompile(`^/live/device/set/parameter/value$`),
}

func (a *Ableton) send(address string, args ...any) error {
	for _, pattern := range abletonAllowed {
		if pattern.MatchString(address) {
			return a.osc.Send(address, args...)
		}
	}
	return fmt.Errorf("%w: %s", ErrForbidden, address)
}

// Observe routes replies to whoever is waiting for that address. Anything
// else counts as a sign of life and nothing more.
func (a *Ableton) Observe(feedback <-chan *goosc.Message) {
	go func() {
		for msg := range feedback {
			a.observed.Add(1)
			a.lastSeen.Store(time.Now().UnixNano())
			a.mu.Lock()
			if a.waiter != nil && msg.Address == a.want {
				select {
				case a.waiter <- msg:
				default:
				}
			}
			a.mu.Unlock()
		}
	}()
}

func (a *Ableton) LastSeen() time.Time {
	nanos := a.lastSeen.Load()
	if nanos == 0 {
		return time.Time{}
	}
	return time.Unix(0, nanos)
}

func (a *Ableton) Observed() uint64 { return a.observed.Load() }

// ask sends a query and returns the reply's arguments after the ids that
// were sent, which the script echoes first. Silence is an error here, unlike
// REAPER, because a DAW that answers questions and did not is not there.
func (a *Ableton) ask(timeout time.Duration, address string, ids ...any) ([]any, error) {
	a.asking.Lock()
	defer a.asking.Unlock()

	reply := make(chan *goosc.Message, 8)
	a.mu.Lock()
	a.waiter, a.want = reply, address
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.waiter, a.want = nil, ""
		a.mu.Unlock()
	}()

	if err := a.send(address, ids...); err != nil {
		return nil, err
	}
	deadline := time.After(timeout)
	for {
		select {
		case msg := <-reply:
			// The ids must match as well as the address: a reply to an
			// earlier question that timed out can arrive now, and it would
			// answer the wrong track or parameter.
			if !echoes(msg.Arguments, ids) {
				continue
			}
			return msg.Arguments[len(ids):], nil
		case <-deadline:
			return nil, fmt.Errorf("%w: no reply to %s", ErrValueUnknown, address)
		}
	}
}

func echoes(args []any, ids []any) bool {
	if len(args) < len(ids) {
		return false
	}
	for i, id := range ids {
		want, _ := numeric(id)
		got, ok := numeric(args[i])
		if !ok || got != want {
			return false
		}
	}
	return true
}

// Parameters is the same five as REAPER's, which is the point: the names a
// user says are the same, and each backend maps them to its own commands.
var abletonParameters = []Parameter{
	{Name: "volume", Kind: Numeric, Readable: true},
	{Name: "pan", Kind: Numeric, Readable: true},
	{Name: "mute", Kind: Toggle, Readable: true},
	{Name: "solo", Kind: Toggle, Readable: true},
	{Name: "send", Kind: Numeric, Readable: false},
}

func (a *Ableton) Parameters() []Parameter { return abletonParameters }

func (a *Ableton) Play() error { return a.send("/live/song/start_playing") }
func (a *Ableton) Stop() error { return a.send("/live/song/stop_playing") }
func (a *Ableton) Undo() error { return a.send("/live/song/undo") }

func (a *Ableton) SetParam(track int, name string, value any) error {
	param, err := FindParameter(a, name)
	if err != nil {
		return err
	}
	switch param.Kind {
	case Toggle:
		on, ok := value.(bool)
		if !ok {
			return fmt.Errorf("%w: %s takes true or false", ErrParamKind, name)
		}
		if name == "mute" {
			return a.SetTrackMute(track, on)
		}
		return a.SetTrackSolo(track, on)
	default:
		number, ok := numeric(value)
		if !ok {
			return fmt.Errorf("%w: %s takes a number", ErrParamKind, name)
		}
		switch name {
		case "volume":
			return a.SetTrackVolume(track, number)
		case "pan":
			return a.SetTrackPan(track, number)
		}
		return fmt.Errorf("%w: %s needs a send index", ErrParamKind, name)
	}
}

func (a *Ableton) SetTrackVolume(track int, value float64) error {
	if err := validateTrack(track); err != nil {
		return err
	}
	if err := validateNormalized(value); err != nil {
		return err
	}
	return a.send("/live/track/set/volume", int32(track-1), float32(value))
}

// Live's panning is -1..1; the normalized 0..1 this layer promises is
// stretched across it here and folded back on read.
func (a *Ableton) SetTrackPan(track int, value float64) error {
	if err := validateTrack(track); err != nil {
		return err
	}
	if err := validateNormalized(value); err != nil {
		return err
	}
	return a.send("/live/track/set/panning", int32(track-1), float32(value*2-1))
}

func (a *Ableton) SetTrackMute(track int, muted bool) error {
	if err := validateTrack(track); err != nil {
		return err
	}
	return a.send("/live/track/set/mute", int32(track-1), boolInt(muted))
}

func (a *Ableton) SetTrackSolo(track int, soloed bool) error {
	if err := validateTrack(track); err != nil {
		return err
	}
	return a.send("/live/track/set/solo", int32(track-1), boolInt(soloed))
}

func (a *Ableton) SetTrackSendVolume(track, send int, value float64) error {
	if err := validateTrack(track); err != nil {
		return err
	}
	if send < 1 {
		return ErrInvalidSend
	}
	if err := validateNormalized(value); err != nil {
		return err
	}
	return a.send("/live/track/set/send", int32(track-1), int32(send-1), float32(value))
}

func boolInt(on bool) int32 {
	if on {
		return 1
	}
	return 0
}

// ReadParam and ConfirmParam are one operation here: a query is always
// fresh, so there is no cache to be confidently wrong from.
func (a *Ableton) ReadParam(track int, name string, timeout time.Duration) (any, error) {
	return a.ConfirmParam(track, name, timeout)
}

func (a *Ableton) ConfirmParam(track int, name string, timeout time.Duration) (any, error) {
	if err := validateTrack(track); err != nil {
		return nil, err
	}
	param, err := FindParameter(a, name)
	if err != nil {
		return nil, err
	}
	if !param.Readable {
		return nil, fmt.Errorf("%w: %s", ErrNotReadable, name)
	}
	address := map[string]string{"volume": "volume", "pan": "panning", "mute": "mute", "solo": "solo"}[name]
	args, err := a.ask(timeout, "/live/track/get/"+address, int32(track-1))
	if err != nil {
		return nil, err
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("%w: empty reply", ErrValueUnknown)
	}
	// Toggles come back as booleans from a real Live, and as 0/1 in the
	// script's documentation; both are accepted.
	if on, ok := args[0].(bool); ok && param.Kind == Toggle {
		a.mu.Lock()
		a.cache[key(track, name)] = on
		a.mu.Unlock()
		return on, nil
	}
	raw, ok := numeric(args[0])
	if !ok {
		return nil, fmt.Errorf("%w: unreadable reply", ErrValueUnknown)
	}
	var value any
	switch {
	case param.Kind == Toggle:
		value = raw != 0
	case name == "pan":
		value = (raw + 1) / 2
	default:
		value = raw
	}
	a.mu.Lock()
	a.cache[key(track, name)] = value
	a.mu.Unlock()
	return value, nil
}

func (a *Ableton) GetParam(track int, name string) (any, error) {
	if err := validateTrack(track); err != nil {
		return nil, err
	}
	if _, err := FindParameter(a, name); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	value, ok := a.cache[key(track, name)]
	if !ok {
		return nil, fmt.Errorf("%w: track %d %s", ErrValueUnknown, track, name)
	}
	return value, nil
}

func (a *Ableton) Tracks(timeout time.Duration) ([]Track, error) {
	args, err := a.ask(timeout, "/live/song/get/track_names")
	if err != nil {
		return nil, err
	}
	tracks := make([]Track, 0, len(args))
	for i, arg := range args {
		name, _ := arg.(string)
		tracks = append(tracks, Track{Number: i + 1, Name: name})
	}
	if len(tracks) == 0 {
		return nil, errors.New("daw: the DAW reported no tracks")
	}
	return tracks, nil
}

// Probe is a real question here, answered "ok".
func (a *Ableton) Probe(timeout time.Duration) bool {
	_, err := a.ask(timeout, "/live/test")
	return err == nil
}

// FXChain asks rather than provokes: device names, then each device's
// parameter names and ranges. The ranges are kept because every later set
// and read has to translate between the device's units and 0..1.
func (a *Ableton) FXChain(track int, timeout time.Duration) ([]FX, error) {
	if err := validateTrack(track); err != nil {
		return nil, err
	}
	names, err := a.ask(timeout, "/live/track/get/devices/name", int32(track-1))
	if err != nil {
		return nil, err
	}
	var chain []FX
	for i, arg := range names {
		name, _ := arg.(string)
		fx := FX{Number: i + 1, Name: name}
		params, err := a.ask(timeout, "/live/device/get/parameters/name", int32(track-1), int32(i))
		if err != nil {
			return nil, err
		}
		known, err := a.rangeOf(timeout, track, i+1)
		if err != nil {
			return nil, err
		}
		quantized, err := a.ask(timeout, "/live/device/get/parameters/is_quantized", int32(track-1), int32(i))
		if err != nil {
			return nil, err
		}
		for k, p := range params {
			pname, _ := p.(string)
			param := FXParam{Number: k + 1, Name: pname}
			// Live says which parameters step, and the range then counts
			// the steps: a two-step one is a switch.
			if k < len(quantized) && isTrue(quantized[k]) && k < len(known[0]) && k < len(known[1]) {
				a.mu.Lock()
				if a.steps[rangeKey(track, i+1)] == nil {
					a.steps[rangeKey(track, i+1)] = make(map[int]bool)
				}
				a.steps[rangeKey(track, i+1)][k+1] = true
				a.mu.Unlock()
				param.Steps = int(known[1][k]-known[0][k]) + 1
				param.Kind = "list"
				if param.Steps == 2 {
					param.Kind = "switch"
				}
			}
			fx.Params = append(fx.Params, param)
		}
		chain = append(chain, fx)
	}
	return chain, nil
}

func rangeKey(track, fx int) string { return fmt.Sprintf("%d/%d", track, fx) }

func (a *Ableton) rangeOf(timeout time.Duration, track, fx int) ([2][]float64, error) {
	a.mu.Lock()
	known, ok := a.ranges[rangeKey(track, fx)]
	a.mu.Unlock()
	if ok {
		return known, nil
	}
	mins, err := a.ask(timeout, "/live/device/get/parameters/min", int32(track-1), int32(fx-1))
	if err != nil {
		return known, err
	}
	maxs, err := a.ask(timeout, "/live/device/get/parameters/max", int32(track-1), int32(fx-1))
	if err != nil {
		return known, err
	}
	known = [2][]float64{floats(mins), floats(maxs)}
	a.mu.Lock()
	a.ranges[rangeKey(track, fx)] = known
	a.mu.Unlock()
	return known, nil
}

func isTrue(arg any) bool {
	if b, ok := arg.(bool); ok {
		return b
	}
	n, _ := numeric(arg)
	return n != 0
}

func (a *Ableton) quantized(track, fx, param int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.steps[rangeKey(track, fx)][param]
}

func floats(args []any) []float64 {
	out := make([]float64, 0, len(args))
	for _, arg := range args {
		v, _ := numeric(arg)
		out = append(out, v)
	}
	return out
}

func (a *Ableton) bounds(timeout time.Duration, track, fx, param int) (float64, float64, error) {
	known, err := a.rangeOf(timeout, track, fx)
	if err != nil {
		return 0, 0, err
	}
	if param > len(known[0]) || param > len(known[1]) {
		return 0, 0, fmt.Errorf("%w: parameter %d", ErrInvalidFX, param)
	}
	return known[0][param-1], known[1][param-1], nil
}

func (a *Ableton) SetFXParam(track, fx, param int, value float64) error {
	if err := validateFX(track, fx, param); err != nil {
		return err
	}
	if err := validateNormalized(value); err != nil {
		return err
	}
	low, high, err := a.bounds(abletonReplyTimeout, track, fx, param)
	if err != nil {
		return err
	}
	raw := low + value*(high-low)
	if a.quantized(track, fx, param) {
		// A stepped parameter takes whole positions; 0.8 of two is the
		// second, not "mostly on".
		raw = math.Round(raw)
	}
	return a.send("/live/device/set/parameter/value", int32(track-1), int32(fx-1), int32(param-1), float32(raw))
}

func (a *Ableton) ConfirmFXParam(track, fx, param int, timeout time.Duration) (float64, error) {
	if err := validateFX(track, fx, param); err != nil {
		return 0, err
	}
	low, high, err := a.bounds(timeout, track, fx, param)
	if err != nil {
		return 0, err
	}
	args, err := a.ask(timeout, "/live/device/get/parameter/value", int32(track-1), int32(fx-1), int32(param-1))
	if err != nil {
		return 0, err
	}
	if len(args) == 0 {
		return 0, fmt.Errorf("%w: empty reply", ErrValueUnknown)
	}
	raw, _ := numeric(args[0])
	if high == low {
		return 0, nil
	}
	return (raw - low) / (high - low), nil
}

func (a *Ableton) ReadFXParam(track, fx, param int, timeout time.Duration) (float64, error) {
	return a.ConfirmFXParam(track, fx, param, timeout)
}

func (a *Ableton) GetFXParam(track, fx, param int) (float64, error) {
	return a.ConfirmFXParam(track, fx, param, abletonReplyTimeout)
}
