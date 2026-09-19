package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// A palette is a gradient for everything that is chrome, plus the colours
// for outcomes. The outcome colours are the same in every palette so a
// glance tells success from refusal whichever look is on, and none of them
// is a chrome colour, so they read against it.
type palette struct {
	Name  string
	Stops []string

	Surface, Deep, Muted, Text, Success, Error, Warning lipgloss.Style
}

var (
	fire    = []string{"#FF1F5A", "#FF3D3D", "#FF7A1A", "#FFC400", "#FFFF00"}
	lagoon  = []string{"#5CF0E6", "#2FD0D8", "#1FA6D9", "#1F6FD9", "#1F3FBF"}
	emerald = []string{"#B6FFD6", "#5CF0A0", "#2ED07A", "#1AA860", "#0E7A48"}
	// One stop: no gradient, plain chrome for a terminal that should not
	// shout. White is for a dark terminal, black for a light one.
	white = []string{"#FFFFFF"}
	black = []string{"#000000"}

	// Order is the order /theme lists them in; the first is the default.
	// "adaptive" is white or black by the terminal's background, decided
	// each time the theme is applied.
	palettes = []palette{
		newPalette("fire", fire),
		newPalette("lagoon", lagoon),
		newPalette("emerald", emerald),
		newPalette("white", white),
		newPalette("black", black),
		{Name: "adaptive"},
	}

	theme = palettes[0]

	// What the terminal's background was found to be when the program
	// started, or what the person said it is. The query works only before
	// the prompt owns stdin, so a terminal that changes its look mid-run,
	// or one that answers wrongly, needs the override.
	detectedDark = true
	background   = ""
)

// Body text is not part of any palette: it follows the terminal's own
// background so a light terminal gets dark text under every theme. The
// background is read once at startup, before the program owns stdin,
// since the query needs to hear the terminal's answer.
func newPalette(name string, stops []string) palette {
	fg := func(hex string) lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color(hex)) }
	return palette{
		Name:    name,
		Stops:   stops,
		Surface: fg(stops[0]),
		Deep:    fg(stops[len(stops)-1]),
		Muted:   lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#6E6A75", Dark: "#8A8593"}),
		Text:    lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#161616", Dark: "#F3F1F5"}),
		Success: fg("#3DDC97"),
		Error:   fg("#FF4DFF"),
		Warning: fg("#4DD2FF"),
	}
}

// resolve turns the adaptive entry into the flat palette that reads on
// this terminal, keeping the name so the listing marks the right row.
func resolve(p palette) palette {
	if p.Name != "adaptive" {
		return p
	}
	if lipgloss.HasDarkBackground() {
		return newPalette("adaptive", white)
	}
	return newPalette("adaptive", black)
}

// useTheme switches the look for everything drawn from now on. The name
// is matched loosely so "Lagoon" works too. A flat theme says which
// terminal it is for: white is for a dark one and black for a light one,
// so choosing it settles the body text without a detection to trust.
func useTheme(name string) error {
	for _, p := range palettes {
		if !strings.EqualFold(p.Name, name) {
			continue
		}
		dark := detectedDark
		switch {
		case background != "":
			dark = background == "dark"
		case p.Name == "white":
			dark = true
		case p.Name == "black":
			dark = false
		}
		lipgloss.SetHasDarkBackground(dark)
		theme = resolve(p)
		return nil
	}
	return errors.New("no theme called " + name + "; themes: " + strings.Join(themeNames(), ", "))
}

// setBackground records the person's word on the terminal, "light",
// "dark" or "" to go back to what was detected, and reapplies the theme.
func setBackground(word string) error {
	switch word {
	case "", "light", "dark":
		background = word
		return useTheme(theme.Name)
	}
	return errors.New("the background is light or dark, not " + word)
}

func themeNames() []string {
	names := make([]string, len(palettes))
	for i, p := range palettes {
		names[i] = p.Name
	}
	return names
}

// The theme is the CLI's own preference, kept beside the shared config
// rather than in it: the window has its own look, and the settings
// service should not learn about a field only one surface reads.
type cliPrefs struct {
	Theme      string `json:"theme,omitempty"`
	Background string `json:"background,omitempty"`
}

func prefsPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "cli.json")
}

// loadTheme applies the saved theme, if any. A missing or broken file
// means the default, since a look is not worth refusing to start over.
// The default is applied either way, so the body text follows what was
// detected.
func loadTheme(configPath string) {
	var prefs cliPrefs
	if raw, err := os.ReadFile(prefsPath(configPath)); err == nil {
		_ = json.Unmarshal(raw, &prefs)
	}
	if prefs.Background == "light" || prefs.Background == "dark" {
		background = prefs.Background
	}
	name := prefs.Theme
	if name == "" {
		name = theme.Name
	}
	if useTheme(name) != nil {
		_ = useTheme(palettes[0].Name)
	}
}

// saveTheme switches and remembers; the switch comes first so a disk
// error still leaves the look the person asked for on screen. An empty
// name keeps the theme and changes only the background word.
func saveTheme(configPath, name, bg string) error {
	if name != "" {
		if err := useTheme(name); err != nil {
			return err
		}
	}
	if err := setBackground(bg); err != nil {
		return err
	}
	raw, _ := json.Marshal(cliPrefs{Theme: theme.Name, Background: background})
	return os.WriteFile(prefsPath(configPath), append(raw, '\n'), 0o600)
}

// gradient paints a string across the palette, one colour step per rune,
// which is how the banner and the prompt get their look without an image.
func gradient(text string) string {
	runes := []rune(text)
	if len(runes) == 0 {
		return ""
	}
	var out strings.Builder
	for i, r := range runes {
		out.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(paletteAt(float64(i) / float64(max(len(runes)-1, 1))))).Render(string(r)))
	}
	return out.String()
}

// paletteAt interpolates the current palette at t in 0..1.
func paletteAt(t float64) string {
	stops := theme.Stops
	if t <= 0 || len(stops) == 1 {
		return stops[0]
	}
	if t >= 1 {
		return stops[len(stops)-1]
	}
	scaled := t * float64(len(stops)-1)
	i := int(scaled)
	f := scaled - float64(i)
	return mix(stops[i], stops[i+1], f)
}

func mix(a, b string, f float64) string {
	var ar, ag, ab, br, bg, bb int
	fmt.Sscanf(a, "#%02x%02x%02x", &ar, &ag, &ab)
	fmt.Sscanf(b, "#%02x%02x%02x", &br, &bg, &bb)
	lerp := func(x, y int) int { return int(float64(x) + (float64(y)-float64(x))*f) }
	return fmt.Sprintf("#%02x%02x%02x", lerp(ar, br), lerp(ag, bg), lerp(ab, bb))
}

// rule is a horizontal line in the gradient, sized to the width.
func rule(width int) string {
	return gradient(strings.Repeat("─", max(width, 1)))
}

// renderThemes is the /theme listing: each name painted in its own look,
// the current one marked.
func renderThemes() string {
	current := theme
	defer func() { theme = current }()
	var lines []string
	for _, p := range palettes {
		theme = resolve(p)
		mark := "  "
		if p.Name == current.Name {
			mark = "● "
		}
		lines = append(lines, current.Muted.Render(mark)+gradient(fmt.Sprintf("%-10s", p.Name))+current.Muted.Render(sampleFor(p)))
	}
	bg := "detected " + map[bool]string{true: "dark", false: "light"}[detectedDark]
	if background != "" {
		bg = background + ", as you said"
	}
	lines = append(lines, "", current.Muted.Render("/theme <name> switches and remembers it; add light or dark if the text reads wrong (background: "+bg+")"))
	return strings.Join(lines, "\n")
}

func sampleFor(p palette) string {
	switch {
	case p.Name == "adaptive":
		return "white or black, by the terminal"
	case len(p.Stops) == 1:
		return "plain"
	}
	return "gradient"
}
