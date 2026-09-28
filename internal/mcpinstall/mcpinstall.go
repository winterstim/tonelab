// Package mcpinstall adds Tonelab to the MCP hosts on this machine, so a
// person does not have to find each host's config file and write the entry
// by hand. Each host is told to launch one command, over stdio.
//
// A file-based host's config is edited in place, keeping everything else in
// it, with the previous version beside it as .bak. Claude Code owns a large
// file it rewrites itself, so it is asked through its own command instead.
package mcpinstall

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
)

// Name is what Tonelab is called in every host's list of servers.
const Name = "tonelab"

// Server is the command a host launches.
type Server struct {
	Command string
	Args    []string
}

// Outcome is one host's result, in words for the person.
type Outcome struct {
	Host   string
	Done   bool
	Detail string
}

// String is the outcome as a line for a terminal.
func (o Outcome) String() string {
	mark := "-"
	if o.Done {
		mark = "+"
	}
	return fmt.Sprintf("%s %s: %s", mark, o.Host, o.Detail)
}

// Print writes a line per host and reports whether any was done, which is
// what a command's exit status should say.
func Print(w io.Writer, outcomes []Outcome) (done bool) {
	for _, outcome := range outcomes {
		fmt.Fprintln(w, outcome)
		done = done || outcome.Done
	}
	return done
}

// Env is where hosts keep their settings, and how their commands are run.
// Tests point it at a temporary home.
type Env struct {
	Home    string
	AppData string
	// CodexHome is $CODEX_HOME, which moves Codex's directory when set.
	CodexHome string
	GOOS      string
	LookPath  func(string) (string, error)
	Run       func(name string, args ...string) error
}

// System is this machine's.
func System() Env {
	home, _ := os.UserHomeDir()
	return Env{
		Home:      home,
		AppData:   os.Getenv("APPDATA"),
		CodexHome: os.Getenv("CODEX_HOME"),
		GOOS:      runtime.GOOS,
		LookPath:  exec.LookPath,
		Run: func(name string, args ...string) error {
			output, err := exec.Command(name, args...).CombinedOutput()
			if err != nil {
				return fmt.Errorf("%w: %s", err, bytes.TrimSpace(output))
			}
			return nil
		},
	}
}

type host struct {
	id, name string
	present  func(Env) bool
	install  func(Env, Server) (string, error)
	remove   func(Env) (string, error)
}

var hosts = []host{
	{
		id: "claude-desktop", name: "Claude Desktop",
		present: func(e Env) bool { return exists(claudeDesktopDir(e)) },
		install: func(e Env, s Server) (string, error) {
			path := filepath.Join(claudeDesktopDir(e), "claude_desktop_config.json")
			return path + "; restart Claude Desktop to load it", setJSON(path, "mcpServers", &s)
		},
		remove: func(e Env) (string, error) {
			path := filepath.Join(claudeDesktopDir(e), "claude_desktop_config.json")
			return path, setJSON(path, "mcpServers", nil)
		},
	},
	{
		id: "claude-code", name: "Claude Code",
		present: func(e Env) bool { _, err := e.LookPath("claude"); return err == nil },
		install: func(e Env, s Server) (string, error) {
			claude, err := e.LookPath("claude")
			if err != nil {
				return "", errors.New("the claude command is not on PATH")
			}
			// Removed first, so a second install moves the entry to this
			// binary rather than failing on the name.
			_ = e.Run(claude, "mcp", "remove", "--scope", "user", Name)
			args := append([]string{"mcp", "add", "--scope", "user", Name, "--", s.Command}, s.Args...)
			return "added for every project, with claude mcp add", e.Run(claude, args...)
		},
		remove: func(e Env) (string, error) {
			claude, err := e.LookPath("claude")
			if err != nil {
				return "", errors.New("the claude command is not on PATH")
			}
			return "removed with claude mcp remove", e.Run(claude, "mcp", "remove", "--scope", "user", Name)
		},
	},
	{
		id: "codex", name: "Codex",
		present: func(e Env) bool {
			_, err := e.LookPath("codex")
			return err == nil || exists(codexDir(e))
		},
		install: func(e Env, s Server) (string, error) {
			path := filepath.Join(codexDir(e), "config.toml")
			return path, setTOML(path, &s)
		},
		remove: func(e Env) (string, error) {
			path := filepath.Join(codexDir(e), "config.toml")
			return path, setTOML(path, nil)
		},
	},
	{
		id: "cursor", name: "Cursor",
		present: func(e Env) bool { return exists(filepath.Join(e.Home, ".cursor")) },
		install: func(e Env, s Server) (string, error) {
			path := filepath.Join(e.Home, ".cursor", "mcp.json")
			return path, setJSON(path, "mcpServers", &s)
		},
		remove: func(e Env) (string, error) {
			path := filepath.Join(e.Home, ".cursor", "mcp.json")
			return path, setJSON(path, "mcpServers", nil)
		},
	},
}

// Hosts names the hosts this package knows, for a usage line.
func Hosts() []string {
	ids := make([]string, len(hosts))
	for i, h := range hosts {
		ids[i] = h.id
	}
	return ids
}

// Install adds the server to the named hosts, or to every host found on
// this machine when none is named. A host named but not found is set up
// anyway where that means writing a file, so it is there on first launch.
func Install(env Env, server Server, only ...string) []Outcome {
	return each(env, only, func(h host) (string, error) { return h.install(env, server) })
}

// Remove takes the server out of the named hosts, or of every host found.
func Remove(env Env, only ...string) []Outcome {
	return each(env, only, func(h host) (string, error) { return h.remove(env) })
}

func each(env Env, only []string, do func(host) (string, error)) []Outcome {
	var outcomes []Outcome
	for _, id := range only {
		if !slices.Contains(Hosts(), id) {
			outcomes = append(outcomes, Outcome{Host: id, Detail: "not a host this knows; one of " + strings.Join(Hosts(), ", ")})
		}
	}
	for _, h := range hosts {
		named := slices.Contains(only, h.id)
		if len(only) > 0 && !named {
			continue
		}
		if !named && !h.present(env) {
			outcomes = append(outcomes, Outcome{Host: h.name, Detail: "not found on this machine"})
			continue
		}
		detail, err := do(h)
		if err != nil {
			outcomes = append(outcomes, Outcome{Host: h.name, Detail: err.Error()})
			continue
		}
		outcomes = append(outcomes, Outcome{Host: h.name, Done: true, Detail: detail})
	}
	return outcomes
}

func claudeDesktopDir(e Env) string {
	switch e.GOOS {
	case "darwin":
		return filepath.Join(e.Home, "Library", "Application Support", "Claude")
	case "windows":
		return filepath.Join(e.AppData, "Claude")
	default:
		return filepath.Join(e.Home, ".config", "Claude")
	}
}

func codexDir(e Env) string {
	if e.CodexHome != "" {
		return e.CodexHome
	}
	return filepath.Join(e.Home, ".codex")
}

// setJSON sets or, for nil, deletes our entry under key, leaving the rest
// of the file as it was in content. Key order and spacing are the encoder's
// afterwards, which is why the old file is kept beside it.
func setJSON(path, key string, server *Server) error {
	document := map[string]any{}
	old, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if server == nil {
			return nil
		}
	case err != nil:
		return err
	case len(bytes.TrimSpace(old)) > 0:
		decoder := json.NewDecoder(bytes.NewReader(old))
		decoder.UseNumber()
		if err := decoder.Decode(&document); err != nil {
			return fmt.Errorf("%s is not valid JSON, so it was left alone: %w", path, err)
		}
	}
	servers, _ := document[key].(map[string]any)
	if servers == nil {
		if server == nil {
			return nil
		}
		servers = map[string]any{}
	}
	if server == nil {
		if _, ok := servers[Name]; !ok {
			return nil
		}
		delete(servers, Name)
	} else {
		args := server.Args
		if args == nil {
			args = []string{}
		}
		servers[Name] = map[string]any{"command": server.Command, "args": args}
	}
	document[key] = servers
	body, err := encode(document)
	if err != nil {
		return err
	}
	return replace(path, old, body)
}

// codexTable matches a header of our table or of one inside it, such as
// [mcp_servers.tonelab.env], quoted or not.
var codexTable = regexp.MustCompile(`^\s*\[\s*mcp_servers\s*\.\s*("tonelab"|tonelab)\s*(\.|\])`)

// setTOML rewrites only our table in Codex's config, line by line: the
// rest of the file, comments included, is left exactly as it was.
func setTOML(path string, server *Server) error {
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if errors.Is(err, os.ErrNotExist) && server == nil {
		return nil
	}
	var kept []string
	ours := false
	for _, line := range strings.SplitAfter(string(old), "\n") {
		if header := strings.HasPrefix(strings.TrimSpace(line), "["); header {
			ours = codexTable.MatchString(line)
		}
		if !ours && line != "" {
			kept = append(kept, line)
		}
	}
	text := strings.Join(kept, "")
	if server != nil {
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		if text != "" && !strings.HasSuffix(text, "\n\n") {
			text += "\n"
		}
		args := make([]string, len(server.Args))
		for i, arg := range server.Args {
			args[i] = quote(arg)
		}
		text += fmt.Sprintf("[mcp_servers.%s]\ncommand = %s\nargs = [%s]\n", Name, quote(server.Command), strings.Join(args, ", "))
	}
	if text == string(old) {
		return nil
	}
	return replace(path, old, []byte(text))
}

// quote is a TOML basic string. JSON's escapes are a subset of TOML's, and
// a Windows path's backslashes need exactly them.
func quote(s string) string {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.Encode(s)
	return strings.TrimSpace(buffer.String())
}

func encode(document map[string]any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(document); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// replace writes body over path, keeping old beside it, and never leaves a
// half-written config: the new one is written aside and renamed over.
func replace(path string, old, body []byte) error {
	if bytes.Equal(old, body) {
		return nil
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if old != nil {
		if err := os.WriteFile(path+".bak", old, mode); err != nil {
			return err
		}
	}
	temp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(body); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Chmod(mode); err != nil && runtime.GOOS != "windows" {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
