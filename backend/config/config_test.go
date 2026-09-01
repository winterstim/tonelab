package config_test

import (
	"os"
	"path/filepath"
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

// A local runtime needs no key, and demanding one would block the path
// this exists to keep open.
func TestAnEmptyAPIKeyIsAllowed(t *testing.T) {
	path := write(t, `{"llm":{"base_url":"http://localhost:11434/v1","api_key":"","model":"qwen2.5"},"daw":{"backend":"reaper","host":"127.0.0.1","port":8000,"feedback_port":9000}}`)

	if _, err := config.Load(path); err != nil {
		t.Fatalf("a local endpoint without a key must be valid: %v", err)
	}
}
