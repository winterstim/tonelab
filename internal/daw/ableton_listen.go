package daw

import (
	"fmt"
	"strings"
	"time"
)

// Live pushes a property once subscribed: the current value at once, then
// every change, on the same address a query is answered on. Measured: a
// query costs 64 to 100 ms, a push lands about 100 ms after the change, and
// a track touched by hand shows up without anyone asking. So the mix
// properties of every track this backend has touched are subscribed, and
// reads come from what Live pushed.
var abletonListened = map[string]string{"volume": "volume", "pan": "panning", "mute": "mute", "solo": "solo"}

// primeTimeout bounds the wait for the values Live pushes on subscription.
// Measured about 100 ms; a Live that sends nothing is handled by the query
// path, not by waiting longer here.
const primeTimeout = 500 * time.Millisecond

// listen subscribes a track's mix properties, once, and waits for the
// values Live pushes on subscription. The wait matters: those carry the
// state before any set made in the same breath, and a confirmation that
// counted one as the answer to its set would confirm the old value.
// Re-sent by Probe: a probe runs when Live has gone quiet, which is when
// it may have restarted and dropped every listener with the script.
func (a *Ableton) listen(track int) {
	a.mu.Lock()
	already := a.listened[track]
	a.listened[track] = true
	a.mu.Unlock()
	if already {
		return
	}
	a.subscribe(track)
	deadline := time.Now().Add(primeTimeout)
	for name := range abletonListened {
		_, _ = a.awaitArrival(key(track, name), 0, time.Until(deadline))
	}
}

func (a *Ableton) subscribe(track int) {
	for _, property := range abletonListened {
		_ = a.send("/live/track/start_listen/"+property, int32(track-1))
	}
}

func (a *Ableton) relisten() {
	a.mu.Lock()
	tracks := make([]int, 0, len(a.listened))
	for track := range a.listened {
		tracks = append(tracks, track)
	}
	a.mu.Unlock()
	for _, track := range tracks {
		a.subscribe(track)
	}
}

// Close drops the subscriptions, so a Live left running is not pushing to
// a port nobody reads.
func (a *Ableton) Close() error {
	a.mu.Lock()
	tracks := make([]int, 0, len(a.listened))
	for track := range a.listened {
		tracks = append(tracks, track)
	}
	a.listened = map[int]bool{}
	a.mu.Unlock()
	for _, track := range tracks {
		for _, property := range abletonListened {
			_ = a.send("/live/track/stop_listen/"+property, int32(track-1))
		}
	}
	return nil
}

// absorb files a pushed or replied mix value under the track and name the
// rest of this layer uses, and wakes whoever waits on it. Called under mu.
func (a *Ableton) absorb(address string, args []any) {
	property := strings.TrimPrefix(address, "/live/track/get/")
	name := ""
	for n, p := range abletonListened {
		if p == property {
			name = n
		}
	}
	if name == "" || len(args) < 2 {
		return
	}
	index, ok := numeric(args[0])
	if !ok {
		return
	}
	value, ok := translate(name, args[1])
	if !ok {
		return
	}
	k := key(int(index)+1, name)
	a.cache[k] = value
	a.arrived[k]++
	close(a.changed)
	a.changed = make(chan struct{})
}

// translate turns Live's value into this layer's: toggles to bool, panning
// from -1..1 to 0..1. Toggles come as booleans from a real Live and as 0/1
// in the script's documentation; both are accepted.
func translate(name string, raw any) (any, bool) {
	if on, ok := raw.(bool); ok {
		return on, name == "mute" || name == "solo"
	}
	number, ok := numeric(raw)
	if !ok {
		return nil, false
	}
	switch name {
	case "mute", "solo":
		return number != 0, true
	case "pan":
		return (number + 1) / 2, true
	}
	return number, true
}

// awaitArrival waits for a reading of k newer than seen. Nil error with
// the value when one came, ErrValueUnknown when the timeout passed.
func (a *Ableton) awaitArrival(k string, seen uint64, timeout time.Duration) (any, error) {
	deadline := time.After(timeout)
	for {
		a.mu.Lock()
		if a.arrived[k] > seen {
			value := a.cache[k]
			a.mu.Unlock()
			return value, nil
		}
		changed := a.changed
		a.mu.Unlock()
		select {
		case <-changed:
		case <-deadline:
			return nil, fmt.Errorf("%w: %s", ErrValueUnknown, k)
		}
	}
}
