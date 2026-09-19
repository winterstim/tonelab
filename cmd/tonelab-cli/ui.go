package main

import (
	"fmt"
	"os"
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
	path     string

	input   textinput.Model
	spinner spinner.Model
	width   int
	sized   bool

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

	// Everything printed above the prompt this session, as a way to draw
	// it again at another width: a resize starts the terminal over.
	log []func(width int) string

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
	{"/theme", "[name] [light|dark]", "the look, and which background it sits on"},
	{"/login", "", "sign in with a Tonelab subscription"},
	{"/account", "", "the subscription and what is used"},
	{"/logout", "", "sign out of the subscription"},
	{"/help", "", "this list"},
	{"/quit", "", "leave"},
}

type turnDone struct{ response app.AgentResponse }
type loginDone struct{ state app.SignInState }
type statusTick app.DAWStatus
type clockTick time.Time

func newModel(runtime *app.Runtime, settings config.Config, path string) model {
	input := textinput.New()
	input.Placeholder = "ask for a change, / for commands"
	input.Focus()

	dots := spinner.New()
	dots.Spinner = spinner.MiniDot

	m := model{runtime: runtime, settings: settings, path: path, input: input, spinner: dots, width: 80}
	m.repaint()
	m.log = append(m.log, func(int) string { return banner(settings) })
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

// repaint applies the theme to the widgets that keep a style of their
// own, which is what a /theme switch has to reach beyond the next print.
func (m *model) repaint() {
	m.input.Prompt = gradient("› ")
	m.input.PlaceholderStyle = theme.Muted
	// Typed text is styled too: a terminal's default foreground can be
	// grey on a light background, which left the command being typed
	// fainter than its own help line.
	m.input.TextStyle = theme.Text
	m.spinner.Style = theme.Surface
}

// fitPlaceholder keeps the hint inside the box: the text field draws
// its placeholder whole, and a box narrower than it would wrap the row.
func (m *model) fitPlaceholder() {
	const hint = "ask for a change, / for commands"
	if m.input.Width >= len(hint) {
		m.input.Placeholder = hint
		return
	}
	m.input.Placeholder = "ask for a change"
	if m.input.Width < len(m.input.Placeholder) {
		m.input.Placeholder = strings.TrimSpace(hint[:max(m.input.Width-1, 0)]) + "…"
	}
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
		// The prompt is drawn inline, and a terminal that changes width
		// (a zoom, a resize) rewraps the rows under it, so the repaint
		// lands on the wrong rows and old frames pile up, on screen and
		// in the scrollback the overflow was pushed into. The one clean
		// cure is to start the terminal over: wipe it, scrollback
		// included, and print the conversation again at the new width
		// from the backend's copy, which is the source anyway.
		changed := m.sized && msg.Width != m.width
		m.sized = true
		m.width = msg.Width
		m.input.Width = max(msg.Width-6, 4)
		m.fitPlaceholder()
		if changed {
			return m, m.redraw()
		}
		return m, nil

	case tea.KeyMsg:
		return m.key(msg)

	case loginDone:
		// Signing in changed the endpoint; the status line reads it.
		m.settings, _ = config.Load(m.path)
		if msg.state.Error != "" {
			return m, m.print(func(int) string { return indent(theme.Error.Render(msg.state.Error)) + "\n" })
		}
		if !msg.state.Done {
			return m, m.print(func(int) string { return indent(theme.Muted.Render("sign-in cancelled")) + "\n" })
		}
		account := renderAccount(m.runtime)
		return m, m.print(func(int) string { return indent(theme.Success.Render("signed in")+"\n"+account) + "\n" })

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
		response := msg.response
		return m, m.print(func(width int) string { return renderTurn(response, width) })

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
	echoLine := func() string { return theme.Surface.Render("› ") + theme.Text.Render(shown) }
	say := func(lines ...string) (tea.Model, tea.Cmd) {
		// One print, so the echo and its answer cannot land out of order.
		answer := indent(strings.Join(lines, "\n"))
		cmd := m.print(func(int) string { return echoLine() + "\n" + answer + "\n" })
		return m, cmd
	}
	run := func(verb string, do func() app.AgentResponse) (tea.Model, tea.Cmd) {
		m.busy, m.started, m.verb, m.notice = true, time.Now(), verb, ""
		echo := m.print(func(int) string { return echoLine() })
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
	case "/theme":
		if len(fields) == 1 {
			return say(renderThemes())
		}
		name, bg := fields[1], background
		if name == "light" || name == "dark" {
			name, bg = "", name
		} else if len(fields) > 2 {
			bg = fields[2]
		}
		if err := saveTheme(m.path, name, bg); err != nil {
			return say(theme.Error.Render(err.Error()))
		}
		m.repaint()
		return say(gradient("tonelab") + theme.Muted.Render("  now in "+theme.Name))
	case "/login":
		state, _ := m.runtime.Hosted.SignIn("")
		if state.Error != "" {
			return say(theme.Error.Render(state.Error))
		}
		// The browser leg takes as long as the person does; the answer
		// arrives as a message so the prompt stays usable meanwhile.
		wait := func() tea.Msg {
			for {
				current, _ := m.runtime.Hosted.State()
				if !current.Running {
					return loginDone{current}
				}
				time.Sleep(time.Second)
			}
		}
		_, cmd := say(theme.Text.Render("Approve this device in the browser. The code shown there must be"), "", theme.Deep.Render(state.UserCode), "", theme.Muted.Render(state.VerifyURL))
		return m, tea.Batch(cmd, wait)
	case "/account":
		return say(renderAccount(m.runtime))
	case "/logout":
		if _, err := m.runtime.Hosted.SignOut(); err != nil {
			return say(theme.Error.Render(err.Error()))
		}
		m.settings, _ = config.Load(m.path)
		return say(theme.Muted.Render("signed out; the model is the local runtime again"))
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
		echo := m.print(func(int) string { return echoLine() })
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
			return m, m.print(func(int) string { return indent(theme.Error.Render(err.Error())) })
		}
		m.thread = opened.Title
		m.hasPlan = false
		m.history = nil
		for _, message := range opened.Messages {
			if message.From == "you" {
				m.history = append(m.history, message.Text)
			}
		}
		m.recall = len(m.history)
		return m, m.print(func(width int) string {
			return theme.Muted.Render("resumed: "+opened.Title) + "\n" + transcript(opened, width) + "\n"
		})
	}
	return m, nil
}

// transcript is the conversation as the prompt would have printed it,
// laid out for the given width.
func transcript(thread app.Conversation, width int) string {
	var lines []string
	for _, message := range thread.Messages {
		if message.From == "you" {
			lines = append(lines, theme.Surface.Render("› ")+theme.Text.Render(message.Text))
		} else {
			lines = append(lines, indent(renderTurnBody(app.AgentResponse{Message: message.Text, Changed: message.Changed, Plan: message.Plan, Error: message.Error}, width-4)))
		}
	}
	return strings.Join(lines, "\n")
}

// print shows text above the prompt and keeps how to draw it again.
// Kept bounded: a session that ran for days should not redraw all of it.
func (m *model) print(render func(width int) string) tea.Cmd {
	m.log = append(m.log, render)
	if len(m.log) > 200 {
		m.log = m.log[len(m.log)-200:]
	}
	return tea.Println(render(m.width))
}

// redraw wipes the terminal, scrollback included, and prints this
// session's output again at the new width. Only what was on screen comes
// back, laid out afresh, so the terminal reads as it did before the
// resize rather than as a replay of the whole thread.
func (m model) redraw() tea.Cmd {
	var body strings.Builder
	for _, render := range m.log {
		body.WriteString(render(m.width))
		body.WriteString("\n")
	}
	// The wipe is written directly, screen then scrollback then home,
	// because the rows a resize rewrapped have already scrolled off by
	// the time the renderer would act. Not tea.ClearScreen after it:
	// Terminal.app treats a screen clear as a push into the scrollback,
	// so a second one would leave a screenful of blank rows above. The
	// print that follows makes the renderer draw the prompt again.
	return tea.Sequence(
		func() tea.Msg { os.Stdout.WriteString("\x1b[2J\x1b[3J\x1b[H"); return nil },
		tea.Println(strings.TrimRight(body.String(), "\n")),
	)
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
		return m.fit(parts)
	}
	if m.busy {
		elapsed := int(time.Since(m.started).Seconds())
		parts = append(parts, m.spinner.View()+" "+theme.Surface.Render(m.verb)+theme.Muted.Render(fmt.Sprintf("  %ds  ·  esc to stop", elapsed)))
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(paletteAt(0.6))).Padding(0, 1).Width(max(m.width-2, 8))
	parts = append(parts, box.Render(m.input.View()))
	if len(m.menu) > 0 {
		for i, c := range m.menu {
			name := " " + c.name
			if c.args != "" {
				name += " " + c.args
			}
			mark := " "
			if i == m.menuAt {
				mark = theme.Surface.Render("▸")
			}
			parts = append(parts, mark+theme.Text.Render(fmt.Sprintf("%-21s", name))+theme.Muted.Render(c.help))
		}
	} else {
		parts = append(parts, statusLine(m))
	}
	return m.fit(parts)
}

// fit truncates every row to the terminal's width. The prompt is drawn
// inline, and a row the terminal has to wrap makes the frame one row
// taller than the renderer believes, so each repaint drifts down and
// leaves a copy of the frame behind.
func (m model) fit(rows []string) string {
	cut := lipgloss.NewStyle().MaxWidth(max(m.width, 1))
	for i, row := range rows {
		rows[i] = cut.Render(row)
	}
	return strings.Join(rows, "\n")
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
