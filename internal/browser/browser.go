// Package browser hands a URL to the desktop, the way a terminal tool
// signing into a service does. On a headless machine there is nothing to
// hand it to; the caller prints the address as well, so failing quietly is
// enough.
package browser

import (
	"os/exec"
	"runtime"
)

// Open starts the system's handler for url and does not wait for it.
func Open(url string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", url)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		command = exec.Command("xdg-open", url)
	}
	return command.Start()
}
