package mcpinstall_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/winterstim/tonelab/internal/mcpinstall"
)

var server = mcpinstall.Server{Command: `C:\Program Files\Tonelab\tonelab-mcp.exe`, Args: []string{"mcp"}}

// env is a machine with nothing installed; tests add the hosts they need.
func env(t *testing.T) (mcpinstall.Env, *[][]string) {
	t.Helper()
	var ran [][]string
	home := t.TempDir()
	return mcpinstall.Env{
		Home:     home,
		AppData:  filepath.Join(home, "AppData"),
		GOOS:     "darwin",
		LookPath: func(string) (string, error) { return "", errors.New("not found") },
		Run: func(name string, args ...string) error {
			ran = append(ran, append([]string{name}, args...))
			return nil
		},
	}, &ran
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func outcome(t *testing.T, outcomes []mcpinstall.Outcome, host string) mcpinstall.Outcome {
	t.Helper()
	for _, o := range outcomes {
		if o.Host == host {
			return o
		}
	}
	t.Fatalf("no outcome for %s in %+v", host, outcomes)
	return mcpinstall.Outcome{}
}

// A person's other servers are theirs: adding ours changes nothing else.
func TestClaudeDesktopKeepsTheOtherServers(t *testing.T) {
	e, _ := env(t)
	path := filepath.Join(e.Home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	original := `{"mcpServers": {"files": {"command": "npx", "args": ["files"]}}, "theme": "dark"}`
	write(t, path, original)

	if o := outcome(t, mcpinstall.Install(e, server), "Claude Desktop"); !o.Done {
		t.Fatalf("install: %+v", o)
	}
	var config struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
		Theme string `json:"theme"`
	}
	if err := json.Unmarshal([]byte(read(t, path)), &config); err != nil {
		t.Fatal(err)
	}
	if config.MCPServers["files"].Command != "npx" || config.Theme != "dark" {
		t.Fatalf("something else changed: %+v", config)
	}
	if got := config.MCPServers["tonelab"]; got.Command != server.Command || len(got.Args) != 1 || got.Args[0] != "mcp" {
		t.Fatalf("entry %+v", got)
	}
	if read(t, path+".bak") != original {
		t.Fatal("the previous file must be kept beside it")
	}

	if o := outcome(t, mcpinstall.Remove(e), "Claude Desktop"); !o.Done {
		t.Fatalf("remove: %+v", o)
	}
	if strings.Contains(read(t, path), "tonelab") || !strings.Contains(read(t, path), "files") {
		t.Fatalf("remove took the wrong thing: %s", read(t, path))
	}
}

func TestAConfigThatIsNotJSONIsLeftAlone(t *testing.T) {
	e, _ := env(t)
	path := filepath.Join(e.Home, ".cursor", "mcp.json")
	write(t, path, "{ this is not json")
	if o := outcome(t, mcpinstall.Install(e, server), "Cursor"); o.Done {
		t.Fatal("a file we cannot read must not be overwritten")
	}
	if read(t, path) != "{ this is not json" {
		t.Fatal("the file changed")
	}
}

// Codex's config is TOML the person edits by hand: only our table changes,
// and comments and other tables stay byte for byte.
func TestCodexChangesOnlyItsOwnTable(t *testing.T) {
	e, _ := env(t)
	path := filepath.Join(e.Home, ".codex", "config.toml")
	write(t, path, `# my settings
model = "gpt-5"

[mcp_servers.tonelab]
command = "/old/tonelab-mcp"
args = []

[mcp_servers.tonelab.env]
X = "1"

[mcp_servers.other]
command = "other"
`)
	if o := outcome(t, mcpinstall.Install(e, server), "Codex"); !o.Done {
		t.Fatalf("install: %+v", o)
	}
	want := `# my settings
model = "gpt-5"

[mcp_servers.other]
command = "other"

[mcp_servers.tonelab]
command = "C:\\Program Files\\Tonelab\\tonelab-mcp.exe"
args = ["mcp"]
`
	if got := read(t, path); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}

	mcpinstall.Remove(e)
	if got := read(t, path); strings.Contains(got, "tonelab") || !strings.Contains(got, "[mcp_servers.other]") || !strings.HasPrefix(got, "# my settings") {
		t.Fatalf("after remove:\n%s", got)
	}
}

func TestCodexHomeIsHonoured(t *testing.T) {
	e, _ := env(t)
	e.CodexHome = filepath.Join(e.Home, "elsewhere")
	if o := outcome(t, mcpinstall.Install(e, server, "codex"), "Codex"); !o.Done {
		t.Fatalf("install: %+v", o)
	}
	if !strings.Contains(read(t, filepath.Join(e.CodexHome, "config.toml")), "[mcp_servers.tonelab]") {
		t.Fatal("CODEX_HOME must be where the entry goes")
	}
}

// Claude Code rewrites its own file while it runs, so it is asked through
// its command, and an old entry is replaced rather than refused.
func TestClaudeCodeIsAskedThroughItsCommand(t *testing.T) {
	e, ran := env(t)
	e.LookPath = func(name string) (string, error) {
		if name == "claude" {
			return "/usr/local/bin/claude", nil
		}
		return "", errors.New("not found")
	}
	if o := outcome(t, mcpinstall.Install(e, server), "Claude Code"); !o.Done {
		t.Fatalf("install: %+v", o)
	}
	got := *ran
	if len(got) != 2 || strings.Join(got[0], " ") != "/usr/local/bin/claude mcp remove --scope user tonelab" {
		t.Fatalf("ran %q", got)
	}
	want := []string{"/usr/local/bin/claude", "mcp", "add", "--scope", "user", "tonelab", "--", server.Command, "mcp"}
	if strings.Join(got[1], "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("ran %q, want %q", got[1], want)
	}
}

// With nothing named, only what is on this machine is touched: a person
// without Cursor does not get a ~/.cursor directory.
func TestOnlyHostsFoundAreTouched(t *testing.T) {
	e, _ := env(t)
	outcomes := mcpinstall.Install(e, server)
	for _, o := range outcomes {
		if o.Done {
			t.Errorf("%s installed on a machine without it", o.Host)
		}
	}
	if _, err := os.Stat(filepath.Join(e.Home, ".cursor")); err == nil {
		t.Fatal("a host that is not installed must not be created")
	}
	if o := outcome(t, mcpinstall.Install(e, server, "cursor"), "Cursor"); !o.Done {
		t.Fatalf("a host named on purpose is set up for its first launch: %+v", o)
	}
}

func TestAnUnknownHostIsSaidToBeOne(t *testing.T) {
	e, _ := env(t)
	o := outcome(t, mcpinstall.Install(e, server, "vscode"), "vscode")
	if o.Done || !strings.Contains(o.Detail, "claude-desktop") {
		t.Fatalf("%+v", o)
	}
}

func TestClaudeDesktopLivesWhereEachSystemKeepsIt(t *testing.T) {
	for goos, dir := range map[string][]string{
		"darwin":  {"Library", "Application Support", "Claude"},
		"windows": {"AppData", "Claude"},
		"linux":   {".config", "Claude"},
	} {
		e, _ := env(t)
		e.GOOS = goos
		mcpinstall.Install(e, server, "claude-desktop")
		path := filepath.Join(append(append([]string{e.Home}, dir...), "claude_desktop_config.json")...)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s: %v", goos, err)
		}
	}
}
