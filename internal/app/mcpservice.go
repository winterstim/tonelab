package app

import "github.com/winterstim/tonelab/internal/mcpinstall"

// HostSetup is one MCP host's result, as the settings screen lists it.
type HostSetup struct {
	Host   string
	Done   bool
	Detail string
}

// MCPService adds Tonelab to the MCP hosts on this machine from the window:
// Claude Desktop, Claude Code, Codex, Cursor. Each is pointed at the app's
// own binary, which serves MCP when started with "mcp", so having the app
// is enough and nothing else needs downloading.
type MCPService struct {
	server mcpinstall.Server
	env    mcpinstall.Env
}

func NewMCPService(command string, env mcpinstall.Env) *MCPService {
	return &MCPService{server: mcpinstall.Server{Command: command, Args: []string{"mcp"}}, env: env}
}

// Install sets up every host found on this machine.
func (s *MCPService) Install() ([]HostSetup, error) {
	if s.server.Command == "" {
		return []HostSetup{{Host: "Tonelab", Detail: "The app cannot tell where it is installed, so no host was set up."}}, nil
	}
	return setups(mcpinstall.Install(s.env, s.server)), nil
}

// Remove takes Tonelab out of every host found.
func (s *MCPService) Remove() ([]HostSetup, error) {
	return setups(mcpinstall.Remove(s.env)), nil
}

func setups(outcomes []mcpinstall.Outcome) []HostSetup {
	out := make([]HostSetup, len(outcomes))
	for i, o := range outcomes {
		out[i] = HostSetup{Host: o.Host, Done: o.Done, Detail: o.Detail}
	}
	return out
}
