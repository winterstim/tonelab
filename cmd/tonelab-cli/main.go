// Command tonelab-cli is Tonelab in a terminal: the same backend the
// desktop window stands on, with a prompt instead of a window. One-shot
// for scripts, interactive otherwise.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	goruntime "runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"golang.org/x/term"

	"tonelab/backend/app"
	"tonelab/backend/config"
)

func main() {
	dawName := flag.String("daw", "", "use this DAW backend for this run instead of the configured one")
	preview := flag.Bool("preview", false, "show the plan instead of applying a one-shot command")
	asJSON := flag.Bool("json", false, "print the one-shot result as JSON")
	quiet := flag.Bool("quiet", false, "suppress the backend log")
	flag.Usage = usage
	flag.Parse()

	if *quiet || !*asJSON && flag.NArg() == 0 {
		// The backend logs OSC and tool traffic to stderr, which would
		// scribble over an interactive screen.
		log.SetOutput(io.Discard)
	}

	configPath, err := config.Path()
	if err != nil {
		fail(err)
	}
	settings, err := config.Load(configPath)
	if err != nil {
		fail(err)
	}
	// Asked now, while stdin is still ours: once the prompt owns it the
	// terminal's answer would be read as keystrokes and the background
	// would default to dark, which paints a light terminal unreadable.
	detectedDark = lipgloss.HasDarkBackground()
	loadTheme(configPath)
	if *dawName != "" {
		settings.DAW.Backend = *dawName
	}
	if !isTerminal() || os.Getenv("NO_COLOR") != "" {
		// Piped or asked for plain: no colour codes in what a script reads.
		lipgloss.SetColorProfile(termenv.Ascii)
		plain = true
	}
	runtime, err := app.Assemble(settings, configPath, openBrowser)
	if err != nil {
		fail(err)
	}
	defer runtime.Close()

	switch {
	case flag.NArg() == 0:
		if !isTerminal() {
			fail(errors.New("interactive mode needs a terminal; pass the command as arguments"))
		}
		program := tea.NewProgram(newModel(runtime, settings, configPath))
		if _, err := program.Run(); err != nil {
			fail(err)
		}
	case flag.Arg(0) == "status":
		status, _ := runtime.Agent.GetDAWStatus()
		emit(*asJSON, status, func() string { return renderStatus(status) })
	case flag.Arg(0) == "undo":
		response, _ := runtime.Agent.Undo()
		emit(*asJSON, response, func() string { return renderResponse(response) })
	default:
		text := strings.Join(flag.Args(), " ")
		var response app.AgentResponse
		if *preview {
			response, _ = runtime.Agent.PreviewCommand(text)
		} else {
			response, _ = runtime.Agent.SendCommand(text)
		}
		emit(*asJSON, response, func() string { return renderResponse(response) })
		os.Exit(exitCode(response))
	}
}

// exitCode is what a script can branch on: 1 when the turn failed, 2 when
// it ran but the DAW did not confirm a change, which is a different
// question from whether the command was understood.
func exitCode(response app.AgentResponse) int {
	if response.Error != nil {
		return 1
	}
	for _, change := range response.Changed {
		if change.NewValue == nil {
			return 2
		}
	}
	return 0
}

func isTerminal() bool { return term.IsTerminal(int(os.Stdout.Fd())) }

func emit(asJSON bool, value any, render func() string) {
	if asJSON {
		body, _ := json.MarshalIndent(value, "", "  ")
		fmt.Println(string(body))
		return
	}
	fmt.Println(render())
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, theme.Error.Render(err.Error()))
	os.Exit(1)
}

func usage() {
	fmt.Fprintln(os.Stderr, gradient("tonelab-cli"))
	fmt.Fprintln(os.Stderr, `
  tonelab-cli                         talk to the DAW interactively
  tonelab-cli <command in words>      run one command and exit
  tonelab-cli --preview <command>     show what it would do, change nothing
  tonelab-cli status                  is the DAW answering
  tonelab-cli undo                    ask the DAW to take back its last change

  --daw <name>   use another DAW backend for this run
  --json         machine-readable output for one-shot commands
  --quiet        hide the backend log

  /theme in the interactive prompt picks the look: fire, lagoon, emerald, white, black, adaptive; add light or dark for the background.

Settings come from the same config file the desktop app uses; TONELAB_CONFIG
points at another one.`)
}

// openBrowser hands a URL to the desktop, the way a terminal tool signing
// into a service does; on a headless machine the address is printed and
// this simply fails quietly.
func openBrowser(url string) error {
	var command *exec.Cmd
	switch goruntime.GOOS {
	case "darwin":
		command = exec.Command("open", url)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		command = exec.Command("xdg-open", url)
	}
	return command.Start()
}
