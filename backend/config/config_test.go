package config_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"tonelab/backend/config"
)

func write(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("could not write the test config: %v", err)
	}
	return path
}

func TestLoadReadsTheEndpointSettings(t *testing.T) {
	path := write(t, `{
		"llm": {"base_url": "http://localhost:11434/v1", "api_key": "secret", "model": "qwen2.5"},
		"daw": {"backend": "reaper", "host": "127.0.0.1", "port": 8000, "feedback_port": 9000}
	}`)

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load returned an error: %v", err)
	}

	if loaded.LLM.BaseURL != "http://localhost:11434/v1" || loaded.LLM.Model != "qwen2.5" {
		t.Fatalf("unexpected LLM settings: %+v", loaded.LLM)
	}
	if loaded.DAW.Backend != "reaper" || loaded.DAW.Port != 8000 || loaded.DAW.FeedbackPort != 9000 {
		t.Fatalf("unexpected DAW settings: %+v", loaded.DAW)
	}
}

// A missing file is the first run, not a fault, so it has to leave the user
// somewhere they can act rather than with an error about a path they have
// never seen.
func TestMissingConfigIsWrittenAsATemplate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	_, err := config.Load(path)

	if err == nil {
		t.Fatal("expected an error telling the user to fill the file in")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("the error must name the file to edit, got: %v", err)
	}
	written, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("expected a template to have been written: %v", readErr)
	}
	for _, field := range []string{"base_url", "api_key", "model", "backend"} {
		if !strings.Contains(string(written), field) {
			t.Errorf("the template omits %q, so the user cannot tell what to fill in", field)
		}
	}
}

// An API key in a file is a secret, and the surest way to leak one is a log
// line written without thinking about it.
func TestTheAPIKeyIsNeverPrintable(t *testing.T) {
	path := write(t, `{"llm":{"base_url":"http://x/v1","api_key":"super-secret-key","model":"m"},"daw":{"backend":"reaper","host":"h","port":1,"feedback_port":2}}`)

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load returned an error: %v", err)
	}

	printed := loaded.String()
	if strings.Contains(printed, "super-secret-key") {
		t.Fatalf("the API key appears in the printable form: %s", printed)
	}
	if !strings.Contains(printed, "http://x/v1") {
		t.Errorf("the printable form should still be useful for diagnosis, got: %s", printed)
	}
}

// Each failure names what to fix, since the user edits this file by hand with
// no UI to validate it for them.
func TestBrokenConfigsSayWhatIsWrong(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"not json", `{ nope`, "not valid JSON"},
		{"no base url", `{"llm":{"api_key":"k","model":"m"},"daw":{"backend":"reaper","host":"h","port":1,"feedback_port":2}}`, "base_url"},
		{"no model", `{"llm":{"base_url":"u","api_key":"k"},"daw":{"backend":"reaper","host":"h","port":1,"feedback_port":2}}`, "model"},
		{"no daw backend", `{"llm":{"base_url":"u","api_key":"k","model":"m"},"daw":{"host":"h","port":1,"feedback_port":2}}`, "backend"},
		{"unknown daw backend", `{"llm":{"base_url":"u","api_key":"k","model":"m"},"daw":{"backend":"cubase","host":"h","port":1,"feedback_port":2}}`, "cubase"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := write(t, tc.body)

			_, err := config.Load(path)

			if err == nil {
				t.Fatal("expected an error, got none")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected the error to mention %q, got: %v", tc.want, err)
			}
		})
	}
}

// A local runtime needs no key, and demanding one would close the local
// path off.
func TestAnEmptyAPIKeyIsAllowed(t *testing.T) {
	path := write(t, `{"llm":{"base_url":"http://localhost:11434/v1","api_key":"","model":"qwen2.5"},"daw":{"backend":"reaper","host":"127.0.0.1","port":8000,"feedback_port":9000}}`)

	if _, err := config.Load(path); err != nil {
		t.Fatalf("a local endpoint without a key must be valid: %v", err)
	}
}

// A settings screen must not be able to leave the file in a state the next
// startup refuses to read, so saving validates the same way loading does.
func TestSaveRejectsSettingsLoadWouldRefuse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	err := config.Save(path, config.Config{
		LLM: config.LLM{BaseURL: "", Model: "m"},
		DAW: config.DAW{Backend: "reaper", Host: "h", Port: 1, FeedbackPort: 2},
	})

	if err == nil {
		t.Fatal("expected the empty base_url to be refused")
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Error("a refused save must not have written anything")
	}
}

// What was saved is what loads, or a settings screen would be lying about
// having saved.
func TestSavedSettingsLoadBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	want := config.Config{
		LLM: config.LLM{BaseURL: "https://api.example/v1", APIKey: "k", Model: "m"},
		DAW: config.DAW{Backend: "reaper", Host: "127.0.0.1", Port: 8000, FeedbackPort: 9000},
	}

	if err := config.Save(path, want); err != nil {
		t.Fatalf("Save returned an error: %v", err)
	}

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load returned an error: %v", err)
	}
	// The first load names the install; everything else is what was saved.
	if loaded.DeviceID == "" {
		t.Fatal("a device id is made on first load")
	}
	want.DeviceID = loaded.DeviceID
	if loaded != want {
		t.Fatalf("expected %+v, got %+v", want, loaded)
	}
	again, _ := config.Load(path)
	if again.DeviceID != loaded.DeviceID {
		t.Fatal("the device id must survive a reload, a key is bound to it")
	}
}

// The file holds an API key, so it must not be readable by other accounts.
func TestSavedSettingsArePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits are Unix; see TestSavedSettingsArePrivateOnWindows")
	}
	path := filepath.Join(t.TempDir(), "config.json")
	settings := config.Config{
		LLM: config.LLM{BaseURL: "u", Model: "m"},
		DAW: config.DAW{Backend: "reaper", Host: "h", Port: 1, FeedbackPort: 2},
	}

	if err := config.Save(path, settings); err != nil {
		t.Fatalf("Save returned an error: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("could not stat the file: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("expected 0600, got %o", mode)
	}
}

// An interrupted save must leave the previous settings rather than half of
// the new ones, which is why it is written beside and renamed.
func TestSaveLeavesNoStrayFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	settings := config.Config{
		LLM: config.LLM{BaseURL: "u", Model: "m"},
		DAW: config.DAW{Backend: "reaper", Host: "h", Port: 1, FeedbackPort: 2},
	}

	if err := config.Save(path, settings); err != nil {
		t.Fatalf("Save returned an error: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("could not read the directory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected only the config file, got %d entries", len(entries))
	}
}
