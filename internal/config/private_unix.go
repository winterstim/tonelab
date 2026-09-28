//go:build !windows

package config

import "os"

// writePrivate writes a file only its owner can read. The file holds API
// keys, so this is not a courtesy: on Unix the mode bits say it.
func writePrivate(path string, body []byte) error {
	return os.WriteFile(path, body, 0o600)
}
