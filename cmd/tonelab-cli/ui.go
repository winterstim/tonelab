package main

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"tonelab/backend/app"
	"tonelab/backend/config"
)

// model is the interactive screen: a banner with the DAW's state, the
// conversation so far, and a prompt. Turns run in the background so the
// screen keeps drawing; the backend already guards against two at once.
type model struct {
	runtime  *app.Runtime
	settings config.Config

	input    textinput.Model
	spinner  spinner.Model
	viewport viewport.Model
	width    int
	height   int

	lines    []string
	busy     bool
	status   app.DAWStatus
	hasPlan  bool
	quitting bool
}

type turnDone struct {
	response app.AgentResponse
	preview  bool
}

type statusTick app.DAWStatus

func newModel(runtime *app.Runtime, settings config.Config) model {
	input := textinput.New()
	input.Prompt = gradient("› ")
	input.Placeholder = "ask for a change, or /help"
	input.PlaceholderStyle = theme.Muted
	input.Focus()

	dots := spinner.New()
	dots.Spinner = spinner.Points
	dots.Style = theme.Surface

	m := model{runtime: runtime, settings: settings, input: input, spinner: dots, viewport: viewport.New(80, 20)}
	if current, err := runtime.Agent.CurrentConversation(); err == nil {
		for _, message := range current.Messages {
			m.lines = append(m.lines, renderMessage(message)...)
		}
	}
	return m
}

func (m model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, m.probe(), m.spinner.Tick)
}

// probe asks the backend, not the DAW directly: the same liveness rules
// the window uses, including recent feedback counting as proof.
func (m model) probe() tea.Cmd {
	return func() tea.Msg {
		status, _ := m.runtime.Agent.GetDAWStatus()
		return statusTick(status)
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.viewport.Width = msg.Width
		m.viewport.Height = max(msg.Height-6, 3)
		m.input.Width = msg.Width - 4
		m.refresh()
		return m, nil

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			m.quitting = true
			return m, tea.Quit
		case tea.KeyEnter:
			text := strings.TrimSpace(m.input.Value())
			if text == "" || m.busy {
				return m, nil
			}
			m.input.SetValue("")
			return m.submit(text)
		}

	case statusTick:
		m.status = app.DAWStatus(msg)
		return m, tea.Tick(5*time.Second, func(time.Time) tea.Msg { return m.probe()() })

	case turnDone:
		m.busy = false
		m.hasPlan = len(msg.response.Plan) > 0
		m.lines = append(m.lines, indent(renderResponse(msg.response)), "")
		m.refresh()
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	// Runes that arrive together, as a paste does, are fed one at a time:
	// the text field matches keys by name, and a batch spelling "up" or
	// "end" would be taken for the key of that name and vanish.
	var cmd tea.Cmd
	m.input, cmd = typeInto(m.input, msg)
	return m, cmd
}

func typeInto(input textinput.Model, msg tea.Msg) (textinput.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok || key.Type != tea.KeyRunes || len(key.Runes) < 2 {
		return input.Update(msg)
	}
	var cmds []tea.Cmd
	for _, r := range key.Runes {
		var cmd tea.Cmd
		input, cmd = input.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		cmds = append(cmds, cmd)
	}
	return input, tea.Batch(cmds...)
}

// submit runs a line: slash commands go straight to the backend, since
// undo after a bad command must not pass through the model that erred.
func (m model) submit(text string) (tea.Model, tea.Cmd) {
	m.lines = append(m.lines, theme.Surface.Render("you ")+theme.Text.Render(text))
	switch {
	case text == "/quit" || text == "/exit":
		m.quitting = true
		return m, tea.Quit
	case text == "/help":
		m.lines = append(m.lines, indent(helpText()), "")
		m.refresh()
		return m, nil
	case text == "/new":
		m.runtime.Agent.StartConversation()
		m.lines = []string{theme.Muted.Render("new conversation")}
		m.refresh()
		return m, nil
	case text == "/status":
		status, _ := m.runtime.Agent.GetDAWStatus()
		m.status = status
		m.lines = append(m.lines, indent(renderStatus(status)), "")
		m.refresh()
		return m, nil
	case text == "/undo":
		m.busy = true
		return m, func() tea.Msg { r, _ := m.runtime.Agent.Undo(); return turnDone{response: r} }
	case text == "/apply":
		if !m.hasPlan {
			m.lines = append(m.lines, indent(theme.Warning.Render("nothing previewed")), "")
			m.refresh()
			return m, nil
		}
		m.busy = true
		return m, func() tea.Msg { r, _ := m.runtime.Agent.ApplyPlan(); return turnDone{response: r} }
	case strings.HasPrefix(text, "/preview "):
		m.busy = true
		command := strings.TrimSpace(strings.TrimPrefix(text, "/preview "))
		return m, func() tea.Msg {
			r, _ := m.runtime.Agent.PreviewCommand(command)
			return turnDone{response: r, preview: true}
		}
	case strings.HasPrefix(text, "/"):
		m.lines = append(m.lines, indent(theme.Error.Render("unknown command; /help lists them")), "")
		m.refresh()
		return m, nil
	}
	m.busy = true
	m.refresh()
	return m, func() tea.Msg { r, _ := m.runtime.Agent.SendCommand(text); return turnDone{response: r} }
}

// refresh redraws the thread, wrapped to the width: a viewport clips, and
// a clipped answer is an answer the user did not get.
func (m *model) refresh() {
	// Answers are indented; wrapping first and indenting after keeps the
	// continuation lines under the first one.
	width := max(m.viewport.Width-4, 20)
	wrapped := make([]string, 0, len(m.lines))
	for _, line := range m.lines {
		if strings.HasPrefix(line, indentation) {
			wrapped = append(wrapped, indent(lipgloss.NewStyle().Width(width).Render(strings.TrimPrefix(line, indentation))))
		} else {
			wrapped = append(wrapped, lipgloss.NewStyle().Width(width+4).Render(line))
		}
	}
	m.viewport.SetContent(strings.Join(wrapped, "\n"))
	m.viewport.GotoBottom()
}

func (m model) View() string {
	if m.quitting {
		return ""
	}
	var status string
	if m.status.Connected {
		status = theme.Success.Render("● " + m.settings.DAW.Backend)
	} else {
		status = theme.Error.Render("○ " + m.settings.DAW.Backend)
	}
	header := lipgloss.JoinHorizontal(lipgloss.Top, gradient("tonelab"), "  ", status, "  ", theme.Muted.Render(m.settings.LLM.Model))

	prompt := m.input.View()
	if m.busy {
		prompt = m.spinner.View() + theme.Muted.Render(" thinking")
	}
	footer := theme.Muted.Render("enter send  ·  /preview  /apply  /undo  /new  /status  ·  esc quit")

	return strings.Join([]string{header, rule(m.width), m.viewport.View(), rule(m.width), prompt, footer}, "\n")
}

func renderMessage(message app.ChatMessage) []string {
	if message.From == "you" {
		return []string{theme.Surface.Render("you ") + theme.Text.Render(message.Text)}
	}
	return []string{indent(renderResponse(app.AgentResponse{Message: message.Text, Changed: message.Changed, Plan: message.Plan, Error: message.Error})), ""}
}

const indentation = "    "

func indent(text string) string {
	return indentation + strings.ReplaceAll(text, "\n", "\n"+indentation)
}

func helpText() string {
	return strings.Join([]string{
		theme.Text.Render("Say what you want changed, in words. Then:"),
		theme.Surface.Render("/preview <words>") + theme.Muted.Render("  show the plan without touching the project"),
		theme.Surface.Render("/apply") + theme.Muted.Render("            carry out the last plan"),
		theme.Surface.Render("/undo") + theme.Muted.Render("             ask the DAW to take back its last change"),
		theme.Surface.Render("/new") + theme.Muted.Render("              start a fresh conversation"),
		theme.Surface.Render("/status") + theme.Muted.Render("           is the DAW answering"),
		theme.Surface.Render("/quit") + theme.Muted.Render("             leave"),
	}, "\n")
}
