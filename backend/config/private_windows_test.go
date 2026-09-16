//go:build windows

package config_test

import (
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
	"tonelab/backend/config"
)

// The saved file must grant its owner and nobody else, whatever the
// directory would have handed down.
func TestSavedSettingsArePrivateOnWindows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	settings := config.Config{
		LLM: config.LLM{BaseURL: "u", Model: "m"},
		DAW: config.DAW{Backend: "reaper", Host: "h", Port: 1, FeedbackPort: 2},
	}
	if err := config.Save(path, settings); err != nil {
		t.Fatalf("Save returned an error: %v", err)
	}

	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("could not read the file's permissions: %v", err)
	}
	sddl := descriptor.String()
	if !strings.Contains(sddl, "D:P") {
		t.Fatalf("expected a protected DACL, got %s", sddl)
	}
	if !strings.Contains(sddl, ";;;OW)") {
		t.Fatalf("expected the owner to be the grantee, got %s", sddl)
	}
	if strings.Count(sddl, "(A;") != 1 {
		t.Fatalf("expected exactly one grant, got %s", sddl)
	}
}
