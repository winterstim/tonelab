package daw

import "time"

// Probe asks the DAW to say something, and reports whether it did. A
// project with no tracks cannot be probed: master is already selected and
// track 1 does not exist, and every other surface command was measured
// silent there too (bank sizes announce a slot once per DAW session only).
// Callers word silence with that in mind. Silence
// proves nothing: an idle REAPER sends zero messages in ten seconds, so a
// status read from quiet alone shows disconnected whenever the musician is
// thinking. The answer is provoked by moving the control surface's own view,
// which is a transition it announces; only /device/* is sent.
func (r *REAPER) Probe(timeout time.Duration) bool {
	// Serialized against the track walk, which also drives the surface: two
	// of them at once would each see the other's answers.
	r.surface.Lock()
	defer r.surface.Unlock()

	// Any message at all counts, which is what liveness means. Watching a
	// narrower signal was the first version's bug: the probe provokes a track
	// name, while the counter it waited on tracks parameter values, so a
	// perfectly alive DAW read as absent.
	before := r.Observed()

	// Two targets rather than one, because the surface may already be sitting
	// on either, and selecting where it already is announces nothing. Master
	// and the first track both exist in any project worth connecting to, so
	// one of the two is always a change.
	for _, target := range []int32{0, 1} {
		if err := r.send("/device/track/select", target); err != nil {
			return false
		}
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if r.Observed() > before {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}
