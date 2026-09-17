package daw

import (
	"embed"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"tonelab/backend/midi"
)

// FLStudio speaks to FL Studio through a controller script Tonelab ships,
// over system exclusive on a virtual MIDI port. FL's Python has no socket
// and no file (measured), so MIDI is the only way out of it; a request is
// one ASCII JSON object, the reply another, matched by id. Measured round
// trip about 1 ms. FL's API answers synchronously, so a read after a set
// is FL's own account and nothing here waits for an announcement.
type FLStudio struct {
	link midi.Link

	mu      sync.Mutex
	next    int
	waiting map[int]chan map[string]any
	// slots remembers which mixer slot each fx number of a track's chain
	// stood for when it was enumerated, since FL numbers by slot and
	// leaves gaps where nothing is loaded.
	slots map[int][]int
	// expected is what each set asked for, so a confirmation can tell a
	// reading from before the set apart from one after it.
	expected map[string]any

	observed atomic.Uint64
	lastSeen atomic.Int64
}

var _ Client = (*FLStudio)(nil)

func NewFLStudio(link midi.Link) *FLStudio {
	f := &FLStudio{link: link, waiting: map[int]chan map[string]any{}, slots: map[int][]int{}, expected: map[string]any{}}
	// The registry builds a backend before anything is connected; with no
	// link every request fails as unanswered, which is the truth.
	if link != nil {
		go f.receive()
	}
	return f
}

const flReplyTimeout = 2 * time.Second

// flTag is the sysex header: start byte and the non-commercial
// manufacturer id, which no real device claims.
var flTag = []byte{midi.SysExStart, 0x7D}

func (f *FLStudio) receive() {
	for message := range f.link.Messages() {
		f.observed.Add(1)
		f.lastSeen.Store(time.Now().UnixNano())
		if len(message) < len(flTag)+1 || string(message[:len(flTag)]) != string(flTag) {
			continue
		}
		var reply map[string]any
		if err := json.Unmarshal(message[len(flTag):len(message)-1], &reply); err != nil {
			continue
		}
		id, ok := numeric(reply["id"])
		if !ok {
			continue
		}
		f.mu.Lock()
		waiter := f.waiting[int(id)]
		f.mu.Unlock()
		if waiter != nil {
			select {
			case waiter <- reply:
			default:
			}
		}
	}
}

// ask sends one request and waits for its reply. Ids make concurrent
// requests safe, and a reply to a request that timed out is dropped.
func (f *FLStudio) ask(timeout time.Duration, request map[string]any) (map[string]any, error) {
	f.mu.Lock()
	f.next++
	id := f.next
	waiter := make(chan map[string]any, 1)
	f.waiting[id] = waiter
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		delete(f.waiting, id)
		f.mu.Unlock()
	}()

	if f.link == nil {
		return nil, fmt.Errorf("%w: no MIDI port", ErrValueUnknown)
	}
	request["id"] = id
	body, err := asciiJSON(request)
	if err != nil {
		return nil, err
	}
	message := append(append(append([]byte{}, flTag...), body...), midi.SysExEnd)
	if err := f.link.Send(message); err != nil {
		return nil, err
	}
	select {
	case reply := <-waiter:
		if text, ok := reply["error"].(string); ok {
			return nil, fmt.Errorf("%w: %s", ErrValueUnknown, text)
		}
		return reply, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("%w: no reply to %v", ErrValueUnknown, request["op"])
	}
}

// asciiJSON escapes every rune above 127, since sysex bytes are seven-bit
// and a plugin named in another alphabet must still cross.
func asciiJSON(value any) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var out strings.Builder
	for _, r := range string(body) {
		if r > 127 {
			fmt.Fprintf(&out, "\\u%04x", r)
		} else {
			out.WriteRune(r)
		}
	}
	return []byte(out.String()), nil
}

var flParameters = []Parameter{
	{Name: "volume", Kind: Numeric, Readable: true},
	{Name: "pan", Kind: Numeric, Readable: true},
	{Name: "mute", Kind: Toggle, Readable: true},
	{Name: "solo", Kind: Toggle, Readable: true},
	{Name: "send", Kind: Numeric, Readable: false},
}

func (f *FLStudio) Parameters() []Parameter { return flParameters }

func (f *FLStudio) Play() error { return f.command("play") }
func (f *FLStudio) Stop() error { return f.command("stop") }
func (f *FLStudio) Undo() error { return f.command("undo") }

func (f *FLStudio) command(op string) error {
	_, err := f.ask(flReplyTimeout, map[string]any{"op": op})
	return err
}

func (f *FLStudio) SetParam(track int, name string, value any) error {
	param, err := FindParameter(f, name)
	if err != nil {
		return err
	}
	switch param.Kind {
	case Numeric:
		number, ok := value.(float64)
		if !ok {
			return fmt.Errorf("%w: %s wants a number", ErrParamKind, name)
		}
		switch name {
		case "volume":
			return f.SetTrackVolume(track, number)
		case "pan":
			return f.SetTrackPan(track, number)
		}
		return fmt.Errorf("%w: %s needs a destination track", ErrParamKind, name)
	case Toggle:
		on, ok := value.(bool)
		if !ok {
			return fmt.Errorf("%w: %s wants true or false", ErrParamKind, name)
		}
		switch name {
		case "mute":
			return f.SetTrackMute(track, on)
		case "solo":
			return f.SetTrackSolo(track, on)
		}
	}
	return fmt.Errorf("%w: %s", ErrUnknownParam, name)
}

func (f *FLStudio) set(track int, name string, value any, normalized any) error {
	f.mu.Lock()
	f.expected[key(track, name)] = normalized
	f.mu.Unlock()
	_, err := f.ask(flReplyTimeout, map[string]any{"op": "set", "track": track, "name": name, "value": value})
	return err
}

func (f *FLStudio) SetTrackVolume(track int, value float64) error {
	if err := validateTrack(track); err != nil {
		return err
	}
	if err := validateNormalized(value); err != nil {
		return err
	}
	return f.set(track, "volume", value, value)
}

// FL pans -1..1; the 0..1 promised here is stretched across it and folded
// back on read.
func (f *FLStudio) SetTrackPan(track int, value float64) error {
	if err := validateTrack(track); err != nil {
		return err
	}
	if err := validateNormalized(value); err != nil {
		return err
	}
	return f.set(track, "pan", value*2-1, value)
}

func (f *FLStudio) SetTrackMute(track int, muted bool) error {
	if err := validateTrack(track); err != nil {
		return err
	}
	return f.set(track, "mute", muted, muted)
}

func (f *FLStudio) SetTrackSolo(track int, soloed bool) error {
	if err := validateTrack(track); err != nil {
		return err
	}
	return f.set(track, "solo", soloed, soloed)
}

// A send in FL is a route to another mixer track, so the send index is the
// destination track's number.
func (f *FLStudio) SetTrackSendVolume(track, send int, value float64) error {
	if err := validateTrack(track); err != nil {
		return err
	}
	if send < 1 {
		return ErrInvalidSend
	}
	if err := validateNormalized(value); err != nil {
		return err
	}
	_, err := f.ask(flReplyTimeout, map[string]any{"op": "route", "track": track, "to": send, "value": value})
	return err
}

// ReadParam asks FL, which answers from its own state, so there is no
// cache to be confidently wrong from.
func (f *FLStudio) ReadParam(track int, name string, timeout time.Duration) (any, error) {
	if err := validateTrack(track); err != nil {
		return nil, err
	}
	param, err := FindParameter(f, name)
	if err != nil {
		return nil, err
	}
	if !param.Readable {
		return nil, fmt.Errorf("%w: %s", ErrNotReadable, name)
	}
	return f.read(param, track, name, timeout)
}

// ConfirmParam reads again until FL reports what the last set asked for,
// or the timeout passes, and returns FL's reading either way. Measured: a
// volume set is visible on the next read, a mute or solo lands on FL's
// own tick, up to a millisecond later, and a single read in between
// reported the value from before the set as if the set had failed.
func (f *FLStudio) ConfirmParam(track int, name string, timeout time.Duration) (any, error) {
	if err := validateTrack(track); err != nil {
		return nil, err
	}
	param, err := FindParameter(f, name)
	if err != nil {
		return nil, err
	}
	if !param.Readable {
		return nil, fmt.Errorf("%w: %s", ErrNotReadable, name)
	}
	f.mu.Lock()
	want, set := f.expected[key(track, name)]
	f.mu.Unlock()
	deadline := time.Now().Add(timeout)
	for {
		value, err := f.read(param, track, name, flReplyTimeout)
		if err != nil {
			return nil, err
		}
		if !set || same(value, want) || time.Now().After(deadline) {
			return value, nil
		}
	}
}

// same compares a reading with what was asked, within float noise.
func same(got, want any) bool {
	a, aok := got.(float64)
	b, bok := want.(float64)
	if aok && bok {
		return math.Abs(a-b) < 0.001
	}
	return got == want
}

func (f *FLStudio) read(param Parameter, track int, name string, timeout time.Duration) (any, error) {
	reply, err := f.ask(timeout, map[string]any{"op": "get", "track": track, "name": name})
	if err != nil {
		return nil, err
	}
	raw := reply["value"]
	if param.Kind == Toggle {
		if on, ok := raw.(bool); ok {
			return on, nil
		}
		number, ok := numeric(raw)
		if !ok {
			return nil, fmt.Errorf("%w: unreadable reply", ErrValueUnknown)
		}
		return number != 0, nil
	}
	number, ok := numeric(raw)
	if !ok {
		return nil, fmt.Errorf("%w: unreadable reply", ErrValueUnknown)
	}
	if name == "pan" {
		return (number + 1) / 2, nil
	}
	return number, nil
}

// GetParam asks too: a synchronous DAW has no "last reading" worth
// preferring to the current one.
func (f *FLStudio) GetParam(track int, name string) (any, error) {
	return f.ConfirmParam(track, name, flReplyTimeout)
}

func (f *FLStudio) Tracks(timeout time.Duration) ([]Track, error) {
	reply, err := f.ask(timeout, map[string]any{"op": "tracks"})
	if err != nil {
		return nil, err
	}
	list, _ := reply["tracks"].([]any)
	tracks := make([]Track, 0, len(list))
	for _, item := range list {
		entry, _ := item.(map[string]any)
		number, _ := numeric(entry["n"])
		name, _ := entry["name"].(string)
		tracks = append(tracks, Track{Number: int(number), Name: name})
	}
	return tracks, nil
}

func (f *FLStudio) LastSeen() time.Time {
	nanos := f.lastSeen.Load()
	if nanos == 0 {
		return time.Time{}
	}
	return time.Unix(0, nanos)
}

func (f *FLStudio) Observed() uint64 { return f.observed.Load() }

func (f *FLStudio) Probe(timeout time.Duration) bool {
	_, err := f.ask(timeout, map[string]any{"op": "ping"})
	return err == nil
}

// flParamPage is how many parameter names one request fetches. A page is
// a few kilobytes; a wrapped plugin's four thousand take a handful.
const flParamPage = 512

// flMaxParams bounds a plugin's parameter list, as REAPER's walk is
// bounded: a model is not helped by thousands.
const flMaxParams = 1024

// FXChain lists the loaded slots of a mixer track with their named
// parameters. FL numbers effects by slot and leaves gaps; the chain is
// numbered without them, and the slot each number stood for is kept for
// the reads and writes that follow. Parameter numbers are FL's own
// indices plus one, since the list is sparse: FL's wrapper for a
// third-party plugin reports every parameter nameless (measured on an
// Audio Unit reverb: values present, names empty) and appends a MIDI
// controller map, "MIDI CC #n" then "MIDI Channel n", to every wrapped
// plugin. A
// nameless parameter cannot be found by name, so it is left out, and the
// map is not the plugin's.
func (f *FLStudio) FXChain(track int, timeout time.Duration) ([]FX, error) {
	if err := validateTrack(track); err != nil {
		return nil, err
	}
	reply, err := f.ask(timeout, map[string]any{"op": "fx", "track": track})
	if err != nil {
		return nil, err
	}
	list, _ := reply["fx"].([]any)
	chain := make([]FX, 0, len(list))
	slots := make([]int, 0, len(list))
	for _, item := range list {
		entry, _ := item.(map[string]any)
		slot, _ := numeric(entry["slot"])
		name, _ := entry["name"].(string)
		count, _ := numeric(entry["count"])
		params, err := f.namedParams(track, int(slot), int(count), timeout)
		if err != nil {
			return nil, err
		}
		slots = append(slots, int(slot))
		chain = append(chain, FX{Number: len(chain) + 1, Name: name, Params: params})
	}
	f.mu.Lock()
	f.slots[track] = slots
	f.mu.Unlock()
	return chain, nil
}

func (f *FLStudio) namedParams(track, slot, count int, timeout time.Duration) ([]FXParam, error) {
	var params []FXParam
	for from := 0; from < count && len(params) < flMaxParams; from += flParamPage {
		reply, err := f.ask(timeout, map[string]any{"op": "params", "track": track, "slot": slot, "from": from, "n": flParamPage})
		if err != nil {
			return nil, err
		}
		page, _ := reply["params"].([]any)
		for _, raw := range page {
			entry, _ := raw.([]any)
			if len(entry) < 3 {
				continue
			}
			index, _ := numeric(entry[0])
			name, _ := entry[1].(string)
			readout, _ := entry[2].(string)
			if strings.TrimSpace(name) == "" {
				continue
			}
			if strings.HasPrefix(name, "MIDI CC #") || strings.HasPrefix(name, "MIDI Channel ") {
				return params, nil
			}
			kind := kindOfText(readout)
			steps := 0
			if kind == "switch" {
				steps = 2
			}
			params = append(params, FXParam{Number: int(index) + 1, Name: name, Kind: kind, Steps: steps})
			if len(params) == flMaxParams {
				break
			}
		}
	}
	return params, nil
}

func (f *FLStudio) slotOf(track, fx int) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	slots := f.slots[track]
	if fx < 1 || fx > len(slots) {
		return 0, fmt.Errorf("%w: effect %d on track %d has not been enumerated", ErrInvalidFX, fx, track)
	}
	return slots[fx-1], nil
}

func (f *FLStudio) SetFXParam(track, fx, param int, value float64) error {
	if err := validateTrack(track); err != nil {
		return err
	}
	if fx < 1 || param < 1 {
		return ErrInvalidFX
	}
	if err := validateNormalized(value); err != nil {
		return err
	}
	slot, err := f.slotOf(track, fx)
	if err != nil {
		return err
	}
	_, err = f.ask(flReplyTimeout, map[string]any{"op": "fxset", "track": track, "slot": slot, "param": param - 1, "value": value})
	return err
}

func (f *FLStudio) ConfirmFXParam(track, fx, param int, timeout time.Duration) (float64, error) {
	if err := validateTrack(track); err != nil {
		return 0, err
	}
	if fx < 1 || param < 1 {
		return 0, ErrInvalidFX
	}
	slot, err := f.slotOf(track, fx)
	if err != nil {
		return 0, err
	}
	reply, err := f.ask(timeout, map[string]any{"op": "fxget", "track": track, "slot": slot, "param": param - 1})
	if err != nil {
		return 0, err
	}
	value, ok := numeric(reply["value"])
	if !ok || math.IsNaN(value) {
		return 0, fmt.Errorf("%w: unreadable reply", ErrValueUnknown)
	}
	return value, nil
}

func (f *FLStudio) ReadFXParam(track, fx, param int, timeout time.Duration) (float64, error) {
	return f.ConfirmFXParam(track, fx, param, timeout)
}

func (f *FLStudio) GetFXParam(track, fx, param int) (float64, error) {
	return f.ConfirmFXParam(track, fx, param, flReplyTimeout)
}

func (f *FLStudio) Close() error {
	if f.link == nil {
		return nil
	}
	return f.link.Close()
}

//go:embed flstudio/device_Tonelab.py
var flScript embed.FS

// Install writes the controller script where FL Studio looks for one, and
// reports the path. Idempotent: an unchanged script is left alone, so FL is
// not asked to reload for nothing. The user still assigns the "Tonelab"
// input to it once in FL's MIDI settings; a script cannot do that itself.
func (f *FLStudio) Install() (string, error) {
	body, err := flScript.ReadFile("flstudio/device_Tonelab.py")
	if err != nil {
		return "", err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, "Documents", "Image-Line", "FL Studio", "Settings", "Hardware", "Tonelab")
	path := filepath.Join(dir, "device_Tonelab.py")
	if current, err := os.ReadFile(path); err == nil && string(current) == string(body) {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return "", err
	}
	return path, nil
}
