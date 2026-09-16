//go:build llm && (reaper || ableton)

package app

import "os"

// envConfig lets one run point at a different endpoint's settings, which is
// how the same pass is run against a local runtime and a hosted one.
func envConfig() string {
	return os.Getenv("TONELAB_CONFIG")
}
