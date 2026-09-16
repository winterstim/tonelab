// Package config reads the settings a user supplies by hand. There is no
// settings UI yet, so the file is the interface: every error names the file
// and the field, because nothing else will tell them what to fix.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tonelab/backend/daw"
	"tonelab/backend/search"
)

// LLM points at any OpenAI-compatible endpoint, which is what lets a cloud key
// and a local runtime share one code path.
type LLM struct {
	BaseURL string `json:"base_url"`
	// Empty is valid: a local runtime usually wants no key at all.
	APIKey string `json:"api_key"`
	Model  string `json:"model"`
}

// DAW is where to reach the DAW, and which backend speaks to it. Held here
// rather than compiled in, so choosing a DAW stays configuration.
type DAW struct {
	Backend      string `json:"backend"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
	FeedbackPort int    `json:"feedback_port"`
}

// UI holds what the person prefers rather than what the machine requires.
// Stored with the rest so a preference survives a restart, which is the whole
// point of one.
type UI struct {
	// Theme is light, dark, or system. Empty means system, so a config
	// written before this existed still loads.
	Theme string `json:"theme,omitempty"`

	// PreviewByDefault decides whether commands are proposed before they run.
	// A safety choice that belongs to the user: some want to see every change
	// first, some want the tool to get on with it.
	PreviewByDefault bool `json:"preview_by_default"`
}

// Search is optional. Empty provider means the agent has no web access,
// which is a valid state rather than a misconfiguration.
type Search struct {
	Provider string `json:"provider,omitempty"`
	APIKey   string `json:"api_key,omitempty"`
	BaseURL  string `json:"base_url,omitempty"`
}

type Config struct {
	LLM    LLM    `json:"llm"`
	DAW    DAW    `json:"daw"`
	UI     UI     `json:"ui"`
	Search Search `json:"search"`
}

// String masks the key. The likeliest way to leak a secret is a log line
// written without thinking, so the printable form simply cannot carry it.
func (c Config) String() string {
	key := "not set"
	if c.LLM.APIKey != "" {
		key = "set"
	}
	searchKey := "not set"
	if c.Search.APIKey != "" {
		searchKey = "set"
	}
	return fmt.Sprintf("llm{base_url:%s model:%s api_key:%s} daw{backend:%s %s:%d feedback:%d} search{provider:%q api_key:%s}",
		c.LLM.BaseURL, c.LLM.Model, key,
		c.DAW.Backend, c.DAW.Host, c.DAW.Port, c.DAW.FeedbackPort,
		c.Search.Provider, searchKey)
}

// Path is where the file lives when the user has not said otherwise.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config: no user config directory: %w", err)
	}
	return filepath.Join(dir, "tonelab", "config.json"), nil
}

// Load reads and validates the file. A missing file is a first run rather than
// a fault, so a template is written and the user is pointed at it: an error
// about a path they have never seen is a dead end.
func Load(path string) (Config, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if writeErr := writeTemplate(path); writeErr != nil {
			return Config{}, fmt.Errorf("config: %s does not exist and could not be created: %w", path, writeErr)
		}
		return Config{}, fmt.Errorf("config: wrote a template to %s, fill it in and start again", path)
	}
	if err != nil {
		return Config{}, fmt.Errorf("config: could not read %s: %w", path, err)
	}

	var loaded Config
	if err := json.Unmarshal(body, &loaded); err != nil {
		return Config{}, fmt.Errorf("config: %s is not valid JSON: %w", path, err)
	}
	if err := loaded.validate(); err != nil {
		return Config{}, fmt.Errorf("config: %s: %w", path, err)
	}
	return loaded, nil
}

// validate reports the first thing to fix, naming the JSON field rather than
// the Go one, since the field name is what the user sees.
func (c Config) validate() error {
	switch {
	case c.LLM.BaseURL == "":
		return errors.New("llm.base_url is required, for example http://localhost:11434/v1")
	case c.LLM.Model == "":
		return errors.New("llm.model is required, the model name the endpoint expects")
	case c.DAW.Backend == "":
		return fmt.Errorf("daw.backend is required, one of: %s", strings.Join(daw.Backends(), ", "))
	case c.DAW.Host == "":
		return errors.New("daw.host is required, usually 127.0.0.1")
	case c.DAW.Port == 0:
		return errors.New("daw.port is required, the port the DAW listens for OSC on")
	case c.DAW.FeedbackPort == 0:
		return errors.New("daw.feedback_port is required, the port the DAW sends feedback to")
	case c.UI.Theme != "" && c.UI.Theme != "light" && c.UI.Theme != "dark" && c.UI.Theme != "system":
		return fmt.Errorf("ui.theme %q is not one of: light, dark, system", c.UI.Theme)
	}
	if c.Search.Provider != "" {
		if _, err := search.New(search.Config{Provider: c.Search.Provider, APIKey: c.Search.APIKey, BaseURL: c.Search.BaseURL}); err != nil {
			return err
		}
	}

	// Checked against the registry rather than a list here, so a new backend
	// becomes selectable without touching this file.
	for _, name := range daw.Backends() {
		if name == c.DAW.Backend {
			return nil
		}
	}
	return fmt.Errorf("daw.backend %q is not one of: %s", c.DAW.Backend, strings.Join(daw.Backends(), ", "))
}

// Save writes settings back, validating first so a settings screen cannot
// leave the file in a state the next startup refuses to read.
//
// Written whole rather than merged: the file is small, and a partial write is
// how a config ends up in a state nobody wrote on purpose.
func Save(path string, settings Config) error {
	if err := settings.validate(); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("config: could not create %s: %w", filepath.Dir(path), err)
	}

	body, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("config: could not encode settings: %w", err)
	}

	// Written beside the target and renamed, so an interrupted save leaves
	// the previous settings rather than half of the new ones.
	temporary := path + ".new"
	if err := os.WriteFile(temporary, append(body, '\n'), 0o600); err != nil {
		return fmt.Errorf("config: could not write %s: %w", temporary, err)
	}
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("config: could not replace %s: %w", path, err)
	}
	return nil
}

// writeTemplate leaves a file the user can edit rather than one they must
// invent, with the local-runtime case filled in since it needs no account.
func writeTemplate(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	template := Config{
		LLM: LLM{BaseURL: "http://localhost:11434/v1", APIKey: "", Model: "qwen2.5"},
		DAW: DAW{Backend: "reaper", Host: "127.0.0.1", Port: 8000, FeedbackPort: 9000},
		UI:  UI{Theme: "system"},
	}
	body, err := json.MarshalIndent(template, "", "  ")
	if err != nil {
		return err
	}

	// 0600: the file holds an API key.
	return os.WriteFile(path, append(body, '\n'), 0o600)
}
