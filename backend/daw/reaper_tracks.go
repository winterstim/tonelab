package daw

import (
	"fmt"
	"time"
)

// Track is a track as the DAW describes it, which is what lets a user say
// "the vocals" instead of a number they would have to look up.
type Track struct {
	Number int    `json:"number"`
	Name   string `json:"name"`
}

// parkIndex is the master track. Selecting it is what makes the walk
// deterministic: master exists in every project and is never one of the
// numbered tracks, so the first step of the walk is always a real transition,
// including in a project with a single track.
const parkIndex = 0

// maxTracks bounds the walk. A project with more tracks than this exists, but
// enumerating it one message at a time would cost more than it returns.
const maxTracks = 128

// Tracks enumerates the project's tracks by name.
//
// It is a walk rather than a query because REAPER answers no questions: it
// announces the track the control surface is looking at, so reading N names
// means looking at N tracks. Two of its behaviours make this work at all, and
// both were found by experiment rather than documentation:
//
//   - It announces only transitions, so selecting a track it is already
//     looking at is silent. Silence has to mean one thing before it can end
//     the walk, which is what parking on master accomplishes.
//   - It clamps a selection past the last track, so walking off the end is
//     silent rather than an error.
//
// Only /device/* addresses are sent, so this cannot disturb the project or the
// user's own selection.
func (r *REAPER) Tracks(timeout time.Duration) ([]Track, error) {
	// Parked, then drained: the announcement parking produces would otherwise
	// be read as the first track's name.
	if _, _, err := r.selectAndRead(parkIndex, timeout); err != nil {
		return nil, err
	}

	var tracks []Track
	for number := 1; number <= maxTracks; number++ {
		name, found, err := r.selectAndRead(number, timeout)
		if err != nil {
			return nil, err
		}
		if !found {
			// Silence after a genuine transition: the track does not exist,
			// so the project ends here.
			break
		}
		tracks = append(tracks, Track{Number: number, Name: name})
	}

	if len(tracks) == 0 {
		return nil, fmt.Errorf("daw: the DAW reported no tracks")
	}
	return tracks, nil
}

// selectAndRead points the surface at one track and waits for the name it
// announces. The change signal is taken before sending, so an answer arriving
// immediately is not missed.
func (r *REAPER) selectAndRead(number int, timeout time.Duration) (string, bool, error) {
	changed := r.state.changed()
	_, seenBefore := r.state.currentTrackName()

	if err := r.send("/device/track/select", int32(number)); err != nil {
		return "", false, err
	}

	deadline := time.After(timeout)
	for {
		select {
		case <-changed:
			// Counted rather than compared: two tracks may share a name, and
			// a repeat would then look like silence.
			if name, seenNow := r.state.currentTrackName(); seenNow > seenBefore {
				return name, true, nil
			}
			changed = r.state.changed()
		case <-deadline:
			return "", false, nil
		}
	}
}
