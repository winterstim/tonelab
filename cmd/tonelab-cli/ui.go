package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"tonelab/backend/app"
	"tonelab/backend/config"
)

// model is the interactive screen, shaped like the agent terminals people
// already use: finished turns are printed into the terminal's own
// scrollback, and only the turn in flight, the input box and a status line
// are drawn live. Turns run in the background; esc stops one.
type model struct {
	runtime  *app.Runtime
	settings config.Config

	input   textinput.Model
	spinner spinner.Model
	width   int

	busy    bool
	started time.Time
	verb    string
	steps   []app.JournalStep
	hasPlan bool
	status  app.DAWStatus
	thread  string

	history []string
	recall  int
	draft   string

	menu     []command
	menuAt   int
	picking  []app.ConversationSummary
	pickAt   int
	quitting bool
	ctrlC    time.Time
	notice   string
}

type command struct {
	name, args, help string
}

var commands = []command{
	{"/preview", "<words>", "show the plan, change nothing"},
	{"/apply", "", "carry out the last plan"},
	{"/undo", "", "ask the DAW to take back its last change"},
	{"/new", "", "start a fresh conversation"},
	{"/resume", "", "pick an earlier conversation"},
	{"/rename", "<name>", "rename this conversation"},
	{"/status", "", "is the DAW answering"},
	{"/daw", "[name]", "which DAW, or switch to another"},
	{"/settings", "[name value]", "show settings, or change one"},
	{"/help", "", "this list"},
	{"/quit", "", "leave"},
}

type turnDone struct{ response app.AgentResponse }
type statusTick app.DAWStatus
type clockTick time.Time

func newModel(runtime *app.Runtime, settings config.Config) model {
	input := textinput.New()
	input.Prompt = gradient("› ")
	input.Placeholder = "ask for a change, / for commands"
	input.PlaceholderStyle = theme.Muted
	input.Focus()

	dots := spinner.New()
	dots.Spinner = spinner.MiniDot
	dots.Style = theme.Surface

	m := model{runtime: runtime, settings: settings, input: input, spinner: dots, width: 80}
	if current, err := runtime.Agent.CurrentConversation(); err == nil {
		m.thread = current.Title
		for _, message := range current.Messages {
			if message.From == "you" {
				m.history = append(m.history, message.Text)
			}
		}
	}
	m.recall = len(m.history)
	return m
}

func (m model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, m.probe(), m.spinner.Tick, tea.Println(banner(m.settings)))
}

func banner(settings config.Config) string {
	return strings.Join([]string{
		gradient("tonelab") + theme.Muted.Render("  "+settings.DAW.Backend+"  ·  "+settings.LLM.Model),
		theme.Muted.Render("say what you want changed. / lists commands, esc stops a turn, ctrl+c twice quits."),
		"",
	}, "\n")
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
		m.width = msg.Width
		m.input.Width = max(msg.Width-6, 20)
		return m, nil

	case tea.KeyMsg:
		return m.key(msg)

	case statusTick:
		m.status = app.DAWStatus(msg)
		return m, tea.Tick(5*time.Second, func(time.Time) tea.Msg { return m.probe()() })

	case clockTick:
		if !m.busy {
			return m, nil
		}
		return m, tea.Tick(time.Second, func(t time.Time) tea.Msg { return clockTick(t) })

	case turnDone:
		m.busy, m.notice = false, ""
		m.hasPlan = len(msg.response.Plan) > 0
		if current, err := m.runtime.Agent.CurrentConversation(); err == nil {
			m.thread = current.Title
		}
		return m, tea.Println(renderTurn(msg.response, m.width))

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	var cmd tea.Cmd
	m.input, cmd = typeInto(m.input, msg)
	m.refreshMenu()
	return m, cmd
}

func (m model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if len(m.picking) > 0 {
		return m.pickThread(msg)
	}
	switch msg.Type {
	case tea.KeyCtrlC:
		// Twice within a moment, as the agent terminals do, so a reflex
		// does not throw the session away.
		if time.Since(m.ctrlC) < 2*time.Second {
			m.quitting = true
			return m, tea.Quit
		}
		m.ctrlC = time.Now()
		m.notice = "ctrl+c again to quit"
		return m, nil
	case tea.KeyEsc:
		if m.busy {
			m.runtime.Agent.Stop()
			m.notice = "stopping"
			return m, nil
		}
		if len(m.menu) > 0 {
			m.input.SetValue("")
			m.menu = nil
		}
		return m, nil
	case tea.KeyUp, tea.KeyDown:
		if len(m.menu) > 0 {
			if msg.Type == tea.KeyUp {
				m.menuAt = (m.menuAt + len(m.menu) - 1) % len(m.menu)
			} else {
				m.menuAt = (m.menuAt + 1) % len(m.menu)
			}
			return m, nil
		}
		return m.browseHistory(msg.Type == tea.KeyUp), nil
	case tea.KeyTab:
		if len(m.menu) > 0 {
			m.pick()
		}
		return m, nil
	case tea.KeyEnter:
		if len(m.menu) > 0 && !strings.Contains(strings.TrimSpace(m.input.Value()), " ") {
			chosen := m.menu[m.menuAt]
			m.pick()
			if strings.HasPrefix(chosen.args, "<") {
				return m, nil // needs words after it; optional ones do not wait
			}
		}
		text := strings.TrimSpace(m.input.Value())
		if text == "" || m.busy {
			return m, nil
		}
		m.input.SetValue("")
		m.menu = nil
		// A key typed into a setting is not something to recall with an
		// arrow, or to echo.
		if !secretLine(text) {
			m.history = append(m.history, text)
		}
		m.recall = len(m.history)
		return m.submit(text)
	}
	m.notice = ""
	var cmd tea.Cmd
	m.input, cmd = typeInto(m.input, msg)
	m.refreshMenu()
	return m, cmd
}

func (m *model) pick() {
	chosen := m.menu[m.menuAt]
	if chosen.args != "" {
		m.input.SetValue(chosen.name + " ")
	} else {
		m.input.SetValue(chosen.name)
	}
	m.input.CursorEnd()
	m.menu = nil
}

// refreshMenu offers the commands that start with what was typed, only
// while the line is still a bare command.
func (m *model) refreshMenu() {
	value := m.input.Value()
	m.menu = nil
	if !strings.HasPrefix(value, "/") || strings.Contains(value, " ") {
		return
	}
	for _, c := range commands {
		if strings.HasPrefix(c.name, value) {
			m.menu = append(m.menu, c)
		}
	}
	if m.menuAt >= len(m.menu) {
		m.menuAt = 0
	}
}

func (m model) browseHistory(up bool) model {
	if len(m.history) == 0 {
		return m
	}
	if m.recall == len(m.history) {
		m.draft = m.input.Value()
	}
	if up && m.recall > 0 {
		m.recall--
	} else if !up && m.recall < len(m.history) {
		m.recall++
	}
	if m.recall == len(m.history) {
		m.input.SetValue(m.draft)
	} else {
		m.input.SetValue(m.history[m.recall])
	}
	m.input.CursorEnd()
	return m
}

// submit runs a line: slash commands go straight to the backend, since
// undo after a bad command must not pass through the model that erred.
func (m model) submit(text string) (tea.Model, tea.Cmd) {
	shown := text
	if secretLine(text) {
		shown = strings.Join(strings.Fields(text)[:2], " ") + " ••••••"
	}
	echo := tea.Println(theme.Surface.Render("› ") + theme.Text.Render(shown))
	say := func(lines ...string) (tea.Model, tea.Cmd) {
		// One print, so the echo and its answer cannot land out of order.
		return m, tea.Println(theme.Surface.Render("› ") + theme.Text.Render(shown) + "\n" + indent(strings.Join(lines, "\n")) + "\n")
	}
	run := func(verb string, do func() app.AgentResponse) (tea.Model, tea.Cmd) {
		m.busy, m.started, m.verb, m.notice = true, time.Now(), verb, ""
		return m, tea.Batch(echo, tea.Tick(time.Second, func(t time.Time) tea.Msg { return clockTick(t) }), func() tea.Msg { return turnDone{do()} })
	}
	fields := strings.Fields(text)
	switch fields[0] {
	case "/quit", "/exit":
		m.quitting = true
		return m, tea.Quit
	case "/help":
		return say(helpText())
	case "/new":
		m.runtime.Agent.StartConversation()
		m.thread = ""
		return say(theme.Muted.Render("new conversation"))
	case "/status":
		status, _ := m.runtime.Agent.GetDAWStatus()
		m.status = status
		return say(renderStatus(status))
	case "/daw":
		if len(fields) == 1 {
			return say(renderDAW(m.runtime, m.settings))
		}
		return say(applySetting(m.runtime, "daw", fields[1]))
	case "/settings":
		if len(fields) == 1 {
			return say(renderSettings(m.runtime))
		}
		if len(fields) < 3 {
			return say(theme.Warning.Render("/settings <name> <value>"))
		}
		return say(applySetting(m.runtime, fields[1], strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(text, "/settings"), " "+fields[1]))))
	case "/resume":
		threads, _ := m.runtime.Agent.Conversations()
		if len(threads) == 0 {
			return say(theme.Muted.Render("no conversations yet"))
		}
		m.picking, m.pickAt = threads, 0
		for i, t := range threads {
			if t.Active {
				m.pickAt = i
			}
		}
		return m, echo
	case "/rename":
		if len(fields) == 1 {
			return say(theme.Warning.Render("/rename <name>"))
		}
		current, _ := m.runtime.Agent.CurrentConversation()
		if r, _ := m.runtime.Agent.RenameConversation(current.ID, strings.TrimSpace(strings.TrimPrefix(text, "/rename"))); r.Error != nil {
			return say(theme.Error.Render(r.Error.Message))
		}
		current, _ = m.runtime.Agent.CurrentConversation()
		m.thread = current.Title
		return say(theme.Success.Render("renamed: " + current.Title))
	case "/undo":
		return run("undoing", func() app.AgentResponse { r, _ := m.runtime.Agent.Undo(); return r })
	case "/apply":
		if !m.hasPlan {
			return say(theme.Warning.Render("nothing previewed"))
		}
		return run("applying", func() app.AgentResponse { r, _ := m.runtime.Agent.ApplyPlan(); return r })
	case "/preview":
		if len(fields) == 1 {
			return say(theme.Warning.Render("/preview needs the words of a command"))
		}
		words := strings.TrimSpace(strings.TrimPrefix(text, "/preview"))
		return run("planning", func() app.AgentResponse { r, _ := m.runtime.Agent.PreviewCommand(words); return r })
	}
	if strings.HasPrefix(text, "/") {
		return say(theme.Error.Render("unknown command; / lists them"))
	}
	return run("thinking", func() app.AgentResponse { r, _ := m.runtime.Agent.SendCommand(text); return r })
}

// pickThread is the /resume list: arrows move, enter opens, esc leaves it.
func (m model) pickThread(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyUp:
		m.pickAt = (m.pickAt + len(m.picking) - 1) % len(m.picking)
	case tea.KeyDown:
		m.pickAt = (m.pickAt + 1) % len(m.picking)
	case tea.KeyEsc, tea.KeyCtrlC:
		m.picking = nil
	case tea.KeyEnter:
		chosen := m.picking[m.pickAt]
		m.picking = nil
		opened, err := m.runtime.Agent.OpenConversation(chosen.ID)
		if err != nil {
			return m, tea.Println(indent(theme.Error.Render(err.Error())))
		}
		m.thread = opened.Title
		m.hasPlan = false
		m.history = nil
		var lines []string
		for _, message := range opened.Messages {
			if message.From == "you" {
				m.history = append(m.history, message.Text)
				lines = append(lines, theme.Surface.Render("› ")+theme.Text.Render(message.Text))
			} else {
				lines = append(lines, indent(renderTurnBody(app.AgentResponse{Message: message.Text, Changed: message.Changed, Plan: message.Plan, Error: message.Error}, m.width-4)))
			}
		}
		m.recall = len(m.history)
		return m, tea.Println(theme.Muted.Render("resumed: "+opened.Title) + "\n" + strings.Join(lines, "\n") + "\n")
	}
	return m, nil
}

func (m model) View() string {
	if m.quitting {
		return ""
	}
	var parts []string
	if len(m.picking) > 0 {
		parts = append(parts, theme.Text.Render("  which conversation?")+theme.Muted.Render("  ↑ ↓ enter, esc to stay"))
		for i, t := range m.picking {
			mark := "  "
			if i == m.pickAt {
				mark = theme.Surface.Render("▸ ")
			}
			title := t.Title
			if t.Active {
				title += theme.Muted.Render("  (open)")
			}
			parts = append(parts, "  "+mark+theme.Text.Render(title))
		}
		return strings.Join(parts, "\n")
	}
	if m.busy {
		elapsed := int(time.Since(m.started).Seconds())
		parts = append(parts, m.spinner.View()+" "+theme.Surface.Render(m.verb)+theme.Muted.Render(fmt.Sprintf("  %ds  ·  esc to stop", elapsed)))
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(fireAt(0.6))).Padding(0, 1).Width(max(m.width-2, 24))
	parts = append(parts, box.Render(m.input.View()))
	if len(m.menu) > 0 {
		for i, c := range m.menu {
			line := "  " + c.name
			if c.args != "" {
				line += " " + c.args
			}
			line = fmt.Sprintf("%-22s", line) + theme.Muted.Render(c.help)
			if i == m.menuAt {
				line = theme.Surface.Render("▸") + line[1:]
			} else {
				line = " " + line[1:]
			}
			parts = append(parts, line)
		}
	} else {
		parts = append(parts, statusLine(m))
	}
	return strings.Join(parts, "\n")
}

func statusLine(m model) string {
	daw := theme.Error.Render("○ " + m.settings.DAW.Backend)
	if m.status.Connected {
		daw = theme.Success.Render("● " + m.settings.DAW.Backend)
	}
	right := m.notice
	if right == "" && m.thread != "" {
		right = m.thread
	}
	return "  " + daw + theme.Muted.Render("  ·  "+m.settings.LLM.Model+"  ·  "+right)
}

// renderTurn prints a finished turn: the tools as they were called, one
// line each with a short result, then the answer and what the DAW
// confirmed.
func renderTurn(r app.AgentResponse, width int) string {
	var out []string
	for _, step := range r.Steps {
		call, result := describeStep(step)
		mark := theme.Surface.Render("⏺ ")
		if step.Failed {
			mark = theme.Error.Render("⏺ ")
		}
		out = append(out, mark+theme.Text.Render(call))
		if result != "" {
			out = append(out, theme.Muted.Render("  ⎿ "+result))
		}
	}
	if len(out) > 0 {
		out = append(out, "")
	}
	body := renderTurnBody(r, width-4)
	if body != "" {
		out = append(out, body)
	}
	return indent(strings.Join(out, "\n")) + "\n"
}

// renderTurnBody is renderResponse with the answer wrapped to the width.
func renderTurnBody(r app.AgentResponse, width int) string {
	message := r.Message
	r.Message = ""
	rest := renderResponse(r)
	var out []string
	if message != "" {
		out = append(out, markdown(strings.TrimSpace(message), max(width, 20)))
	}
	if rest != "" {
		out = append(out, rest)
	}
	return strings.Join(out, "\n")
}

const indentation = "  "

func indent(text string) string {
	return indentation + strings.ReplaceAll(text, "\n", "\n"+indentation)
}

func typeInto(input textinput.Model, msg tea.Msg) (textinput.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok || key.Type != tea.KeyRunes || len(key.Runes) < 2 {
		return input.Update(msg)
	}
	// Runes that arrive together, as a paste does, are fed one at a time:
	// the text field matches keys by name, and a batch spelling "up" or
	// "end" would be taken for the key of that name and vanish.
	var cmds []tea.Cmd
	for _, r := range key.Runes {
		var cmd tea.Cmd
		input, cmd = input.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		cmds = append(cmds, cmd)
	}
	return input, tea.Batch(cmds...)
}

func helpText() string {
	var lines []string
	for _, c := range commands {
		name := c.name
		if c.args != "" {
			name += " " + c.args
		}
		lines = append(lines, theme.Surface.Render(fmt.Sprintf("%-18s", name))+theme.Muted.Render(c.help))
	}
	lines = append(lines, "", theme.Muted.Render("↑ ↓ earlier commands  ·  esc stops a turn  ·  ctrl+c twice quits"))
	return strings.Join(lines, "\n")
}

func renderDAW(runtime *app.Runtime, settings config.Config) string {
	current, _ := runtime.Settings.Get()
	var lines []string
	for _, name := range current.DAWAvailable {
		if name == settings.DAW.Backend {
			lines = append(lines, theme.Success.Render("● "+name)+theme.Muted.Render("  in use"))
		} else {
			lines = append(lines, theme.Muted.Render("○ "+name))
		}
	}
	lines = append(lines, theme.Muted.Render("/daw <name> switches; the DAW itself must be set up for Tonelab as the README describes"))
	return strings.Join(lines, "\n")
}

// secretLine is a /settings line carrying a key.
func secretLine(text string) bool {
	fields := strings.Fields(text)
	return len(fields) >= 3 && fields[0] == "/settings" && isSecret(fields[1])
}
