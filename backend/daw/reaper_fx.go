package daw

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	goosc "github.com/hypebeast/go-osc/osc"
)

// FX is one effect on a track as the DAW names it, with the parameters it
// reports. Nothing here is known ahead of time: a plugin Tonelab has never
// seen arrives the same way as one it has.
type FX struct {
	Number int       `json:"number"`
	Name   string    `json:"name"`
	Params []FXParam `json:"params"`
}

type FXParam struct {
	Number int    `json:"number"`
	Name   string `json:"name"`
	// Kind is "" for a continuous control, "switch" for two states, "list"
	// for a known number of states (in Steps), "discrete" for stepped
	// with the count unknown. A model sending 0.8 to a switch gets
	// something it did not ask for, so it is told which is which.
	Kind  string `json:"kind,omitempty"`
	Steps int    `json:"steps,omitempty"`
}

var switchWords = map[string]bool{"on": true, "off": true, "normal": true, "bypassed": true, "bypass": true,
	"yes": true, "no": true, "true": true, "false": true, "enabled": true, "disabled": true}

// kindOfText infers a kind from the DAW's readout, the only hint REAPER
// gives: a number means a control, a word means a position.
func kindOfText(readout string) string {
	text := strings.TrimSpace(readout)
	if text == "" {
		return ""
	}
	if numericPrefix(strings.Fields(text)[0]) {
		return ""
	}
	if switchWords[strings.ToLower(text)] {
		return "switch"
	}
	return "discrete"
}

// numericPrefix reports whether a readout token starts with a number. Units
// are not always spaced off: FL writes "75Hz" and "1.5sec", REAPER "-6.0 dB".
func numericPrefix(token string) bool {
	end := 0
	for end < len(token) && strings.ContainsRune("+-0123456789.", rune(token[end])) {
		end++
	}
	if end == 0 {
		return false
	}
	_, err := strconv.ParseFloat(token[:end], 64)
	return err == nil
}

// bankSize is how many parameters the surface shows at once, its setting and
// not the plugin's. Parameters beyond it come by selecting further banks.
const bankSize = 16

// maxBanks bounds the walk. A plugin with more parameters than this exists,
// but a model would not be helped by all of them.
const maxBanks = 64

// quiet is how long the feedback has to be silent for a dump to count as
// finished. The DAW sends a chain as one burst, so a gap means the end.
const quiet = 150 * time.Millisecond

var (
	chainName  = regexp.MustCompile(`^/fx/([0-9]+)/name$`)
	chainParam = regexp.MustCompile(`^/fx/([0-9]+)/fxparam/([0-9]+)/name$`)
	bankParam  = regexp.MustCompile(`^/fxparam/([0-9]+)/name$`)
	chainText  = regexp.MustCompile(`^/fx/([0-9]+)/fxparam/([0-9]+)/value/str$`)
	bankText   = regexp.MustCompile(`^/fxparam/([0-9]+)/value/str$`)
)

// FXChain enumerates the effects on one track and their parameters. Measured,
// not documented: pointing the surface at a track dumps every effect and the
// first bank of each in one burst; parameters past that come one bank at a
// time, numbered from one within the bank, and an empty name ends the list.
// Only /device/* is sent, so the project and the user's selection are
// untouched.
func (r *REAPER) FXChain(track int, timeout time.Duration) ([]FX, error) {
	if track < 1 {
		return nil, ErrInvalidTrack
	}
	r.surface.Lock()
	defer r.surface.Unlock()

	collector := newFXCollector()
	r.setTap(collector.absorb)
	defer r.setTap(nil)

	// Parked first: the surface may already sit on the track, and a DAW
	// announcing only transitions would then say nothing.
	if err := r.send("/device/track/select", int32(parkIndex)); err != nil {
		return nil, err
	}
	collector.awaitQuiet(timeout)
	collector.reset()
	if err := r.send("/device/track/select", int32(track)); err != nil {
		return nil, err
	}
	collector.awaitQuiet(timeout)

	chain := collector.chain()
	for i := range chain {
		if len(chain[i].Params) < bankSize {
			continue
		}
		more, err := r.walkBanks(collector, i+1, timeout)
		if err != nil {
			return nil, err
		}
		chain[i].Params = append(chain[i].Params, more...)
	}

	// Left where it was found, so the next dump reads the same way.
	if err := r.send("/device/fxparam/bank/select", int32(1)); err != nil {
		return nil, err
	}
	if err := r.send("/device/fx/select", int32(1)); err != nil {
		return nil, err
	}
	return chain, nil
}

// walkBanks reads the parameters of one effect beyond the first bank. It
// stops at the first empty name and never on silence, since a bank past the
// end is silent and silence is also what a slow DAW looks like.
func (r *REAPER) walkBanks(collector *fxCollector, fx int, timeout time.Duration) ([]FXParam, error) {
	if err := r.send("/device/fx/select", int32(fx)); err != nil {
		return nil, err
	}
	collector.awaitQuiet(timeout)

	var params []FXParam
	for bank := 2; bank <= maxBanks; bank++ {
		collector.reset()
		if err := r.send("/device/fxparam/bank/select", int32(bank)); err != nil {
			return nil, err
		}
		if !collector.awaitBank(bank, timeout) {
			break
		}
		inBank, ended := collector.bank()
		for _, param := range inBank {
			param.Number += (bank - 1) * bankSize
			params = append(params, param)
		}
		if ended || len(inBank) < bankSize {
			break
		}
	}
	return params, nil
}

func (r *REAPER) setTap(tap func(*goosc.Message)) {
	r.tapMu.Lock()
	defer r.tapMu.Unlock()
	r.tap = tap
}

// fxCollector gathers one dump. Names are kept by position rather than
// appended, because the DAW repeats itself and order across a burst is not
// guaranteed.
type fxCollector struct {
	mu       sync.Mutex
	names    map[int]string
	params   map[int]map[int]string
	kinds    map[int]map[int]string
	inBank   map[int]string
	bankKind map[int]string
	bankSeen int
	ended    bool
	last     time.Time
	changed  chan struct{}
}

func newFXCollector() *fxCollector {
	c := &fxCollector{changed: make(chan struct{})}
	c.reset()
	return c
}

func (c *fxCollector) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.names = make(map[int]string)
	c.params = make(map[int]map[int]string)
	c.kinds = make(map[int]map[int]string)
	c.inBank = make(map[int]string)
	c.bankKind = make(map[int]string)
	c.bankSeen = 0
	c.ended = false
	c.last = time.Now()
}

// absorb counts only what it understands towards the burst: a playing DAW
// streams its position many times a second, and quiet measured against
// that never arrives.
func (c *fxCollector) absorb(msg *goosc.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if m := chainName.FindStringSubmatch(msg.Address); m != nil {
		if name, ok := stringArg(msg); ok && name != "" {
			c.names[atoi(m[1])] = name
		}
	} else if m := chainParam.FindStringSubmatch(msg.Address); m != nil {
		if name, ok := stringArg(msg); ok && name != "" {
			fx := atoi(m[1])
			if c.params[fx] == nil {
				c.params[fx] = make(map[int]string)
			}
			c.params[fx][atoi(m[2])] = name
		}
	} else if m := chainText.FindStringSubmatch(msg.Address); m != nil {
		if text, ok := stringArg(msg); ok {
			fx := atoi(m[1])
			if c.kinds[fx] == nil {
				c.kinds[fx] = make(map[int]string)
			}
			c.kinds[fx][atoi(m[2])] = kindOfText(text)
		}
	} else if m := bankText.FindStringSubmatch(msg.Address); m != nil {
		if text, ok := stringArg(msg); ok {
			c.bankKind[atoi(m[1])] = kindOfText(text)
		}
	} else if m := bankParam.FindStringSubmatch(msg.Address); m != nil {
		if name, ok := stringArg(msg); ok {
			if name == "" {
				c.ended = true
			} else {
				c.inBank[atoi(m[1])] = name
			}
		}
	} else if msg.Address == "/device/fxparam/bank/str" {
		if text, ok := stringArg(msg); ok {
			c.bankSeen = atoi(text)
		}
	} else {
		return
	}

	c.last = time.Now()
	close(c.changed)
	c.changed = make(chan struct{})
}

// awaitQuiet returns once nothing has arrived for a while, or at the
// deadline. A burst has no terminator of its own.
func (c *fxCollector) awaitQuiet(timeout time.Duration) {
	deadline := time.After(timeout)
	for {
		c.mu.Lock()
		since := time.Since(c.last)
		changed := c.changed
		c.mu.Unlock()
		if since >= quiet {
			return
		}
		select {
		case <-changed:
		case <-time.After(quiet - since):
		case <-deadline:
			return
		}
	}
}

// awaitBank waits for the DAW to confirm it switched, then for the names to
// settle. Without the confirmation a bank past the end, which is silent,
// would be read as an empty bank that exists.
func (c *fxCollector) awaitBank(bank int, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		c.mu.Lock()
		seen := c.bankSeen
		changed := c.changed
		c.mu.Unlock()
		if seen == bank {
			c.awaitQuiet(timeout)
			return true
		}
		select {
		case <-changed:
		case <-deadline:
			return false
		}
	}
}

func (c *fxCollector) chain() []FX {
	c.mu.Lock()
	defer c.mu.Unlock()
	var chain []FX
	for number := 1; ; number++ {
		name, ok := c.names[number]
		if !ok {
			break
		}
		fx := FX{Number: number, Name: name}
		for k := 1; ; k++ {
			param, ok := c.params[number][k]
			if !ok {
				break
			}
			fx.Params = append(fx.Params, FXParam{Number: k, Name: param, Kind: c.kinds[number][k]})
		}
		chain = append(chain, fx)
	}
	return chain
}

func (c *fxCollector) bank() ([]FXParam, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var params []FXParam
	for k := 1; k <= bankSize; k++ {
		name, ok := c.inBank[k]
		if !ok {
			break
		}
		params = append(params, FXParam{Number: k, Name: name, Kind: c.bankKind[k]})
	}
	return params, c.ended
}

func stringArg(msg *goosc.Message) (string, bool) {
	if len(msg.Arguments) == 0 {
		return "", false
	}
	text, ok := msg.Arguments[0].(string)
	return text, ok
}

func atoi(text string) int {
	n, _ := strconv.Atoi(text)
	return n
}

// ErrInvalidFX covers an effect or parameter index below one. Past the end is
// not checked here: the DAW is silent about it, and silence reaches the
// caller as an unconfirmed set rather than a guess.
var ErrInvalidFX = errors.New("daw: effect and parameter indices start at 1")

var (
	absoluteFXValue = regexp.MustCompile(`^/track/([0-9]+)/fx/([0-9]+)/fxparam/([0-9]+)/value$`)
	surfaceFXValue  = regexp.MustCompile(`^/fx/([0-9]+)/fxparam/([0-9]+)/value$`)
	bankFXValue     = regexp.MustCompile(`^/fxparam/([0-9]+)/value$`)
)

func fxKey(fx, param int) string {
	return fmt.Sprintf("fx/%d/param/%d", fx, param)
}

func validateFX(track, fx, param int) error {
	if err := validateTrack(track); err != nil {
		return err
	}
	if fx < 1 || param < 1 {
		return ErrInvalidFX
	}
	return nil
}

// SetFXParam addresses a parameter by position, since that is the only
// handle the DAW offers; the name to position mapping is FXChain's to give.
func (r *REAPER) SetFXParam(track, fx, param int, value float64) error {
	if err := validateFX(track, fx, param); err != nil {
		return err
	}
	if err := validateNormalized(value); err != nil {
		return err
	}
	return r.send(fmt.Sprintf("/track/%d/fx/%d/fxparam/%d/value", track, fx, param), float32(value))
}

// GetFXParam answers from the DAW's account. An effect parameter differs from
// a track parameter in one useful way, measured: the DAW echoes a set back,
// as the plugin quantized it, whichever track the surface looks at.
func (r *REAPER) GetFXParam(track, fx, param int) (float64, error) {
	if err := validateFX(track, fx, param); err != nil {
		return 0, err
	}
	value, ok := r.state.get(track, fxKey(fx, param))
	if !ok {
		return 0, fmt.Errorf("%w: track %d fx %d param %d", ErrValueUnknown, track, fx, param)
	}
	return value, nil
}

// ConfirmFXParam accepts only a reading made after the call, for the same
// reason ConfirmParam does: the cache right after a change holds the value
// being disproved.
func (r *REAPER) ConfirmFXParam(track, fx, param int, timeout time.Duration) (float64, error) {
	if err := validateFX(track, fx, param); err != nil {
		return 0, err
	}
	r.surface.Lock()
	defer r.surface.Unlock()
	if param > bankSize {
		defer r.leaveSurface()
	}

	seen := r.state.readingsSeen()
	changed := r.state.changed()

	if err := r.refreshFX(track, fx, param); err != nil {
		return 0, err
	}

	deadline := time.After(timeout)
	for {
		// This parameter's own reading, made after the question: any other
		// reading arriving is not an answer, and the cache holds the value
		// being disproved.
		if value, ok := r.state.getSince(track, fxKey(fx, param), seen); ok {
			return value, nil
		}
		select {
		case <-changed:
			changed = r.state.changed()
		case <-deadline:
			return 0, fmt.Errorf("%w: track %d fx %d param %d", ErrValueUnknown, track, fx, param)
		}
	}
}

// ReadFXParam asks, and falls back to the last reading, as ReadParam does.
func (r *REAPER) ReadFXParam(track, fx, param int, timeout time.Duration) (float64, error) {
	value, err := r.ConfirmFXParam(track, fx, param, timeout)
	if err == nil {
		return value, nil
	}
	if !errors.Is(err, ErrValueUnknown) {
		return 0, err
	}
	return r.GetFXParam(track, fx, param)
}

// absorbFX files effect values. The surface-relative form names no track, so
// it is filed under the track the surface was last pointed at, which this
// backend knows because it is the only thing pointing it.
func (r *REAPER) absorbFX(msg *goosc.Message) bool {
	if len(msg.Arguments) == 0 {
		return false
	}
	if m := absoluteFXValue.FindStringSubmatch(msg.Address); m != nil {
		if value, ok := numeric(msg.Arguments[0]); ok {
			r.state.set(atoi(m[1]), fxKey(atoi(m[2]), atoi(m[3])), value)
		}
		return true
	}
	if m := surfaceFXValue.FindStringSubmatch(msg.Address); m != nil {
		track := int(r.surfaceTrack.Load())
		if value, ok := numeric(msg.Arguments[0]); ok && track > 0 {
			r.state.set(track, fxKey(atoi(m[1]), atoi(m[2])), value)
		}
		return true
	}
	// Bank-relative: names neither track, effect nor bank, all of which are
	// where this backend last pointed the surface.
	if m := bankFXValue.FindStringSubmatch(msg.Address); m != nil {
		track, fx, bank := int(r.surfaceTrack.Load()), int(r.surfaceFX.Load()), int(r.surfaceBank.Load())
		if fx < 1 {
			fx = 1
		}
		if bank < 1 {
			bank = 1
		}
		if value, ok := numeric(msg.Arguments[0]); ok && track > 0 {
			r.state.set(track, fxKey(fx, (bank-1)*bankSize+atoi(m[1])), value)
		}
		return true
	}
	return false
}

// refreshFX makes the DAW announce one parameter's value. The first bank
// of every effect comes with the track dump; anything beyond it means
// pointing the surface at that effect and bank, which is why the surface
// is locked here and left on bank 1 afterwards.
// Each step waits for the DAW's burst to end before the next, because the
// backend files bank feedback under where it last pointed the surface, and
// a burst still arriving when the pointer moves would be filed wrongly.
func (r *REAPER) refreshFX(track, fx, param int) error {
	if err := r.send("/device/track/select", int32(parkIndex)); err != nil {
		return err
	}
	r.awaitQuiet(time.Second)
	if err := r.send("/device/track/select", int32(track)); err != nil {
		return err
	}
	bank := (param-1)/bankSize + 1
	if bank == 1 {
		return nil
	}
	r.awaitQuiet(time.Second)
	if err := r.send("/device/fx/select", int32(fx)); err != nil {
		return err
	}
	r.awaitQuiet(time.Second)
	return r.send("/device/fxparam/bank/select", int32(bank))
}

// leaveSurface waits for the bank's burst to end before pointing the
// surface back: a value arriving after the bank was reset would be filed
// under the wrong parameter, since the message itself names no bank.
func (r *REAPER) leaveSurface() {
	r.awaitQuiet(2 * time.Second)
	_ = r.send("/device/fxparam/bank/select", int32(1))
	_ = r.send("/device/fx/select", int32(1))
}

func (r *REAPER) awaitQuiet(timeout time.Duration) {
	deadline := time.After(timeout)
	for {
		changed := r.state.changed()
		select {
		case <-changed:
		case <-time.After(quiet):
			return
		case <-deadline:
			return
		}
	}
}
