package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The palette: a lagoon, teal at the surface running to deep blue, for
// everything that is chrome. Outcomes get their own colours so a glance
// tells success from refusal without reading.
var (
	lagoon = []string{"#5FF5E0", "#2ED3D6", "#1FA8E0", "#1E78F0", "#2B4FD8"}

	theme = struct {
		Surface, Deep, Muted, Text, Success, Error, Warning lipgloss.Style
	}{
		Surface: lipgloss.NewStyle().Foreground(lipgloss.Color("#5FF5E0")),
		Deep:    lipgloss.NewStyle().Foreground(lipgloss.Color("#2B4FD8")),
		Muted:   lipgloss.NewStyle().Foreground(lipgloss.Color("#6C7A96")),
		Text:    lipgloss.NewStyle().Foreground(lipgloss.Color("#E6F1FF")),
		Success: lipgloss.NewStyle().Foreground(lipgloss.Color("#3DDC97")),
		Error:   lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B")),
		Warning: lipgloss.NewStyle().Foreground(lipgloss.Color("#FFC857")),
	}
)

// gradient paints a string across the lagoon, one colour step per rune,
// which is how the banner and the prompt get their look without an image.
func gradient(text string) string {
	runes := []rune(text)
	if len(runes) == 0 {
		return ""
	}
	var out strings.Builder
	for i, r := range runes {
		out.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(lagoonAt(float64(i) / float64(max(len(runes)-1, 1))))).Render(string(r)))
	}
	return out.String()
}

// lagoonAt interpolates the palette at t in 0..1.
func lagoonAt(t float64) string {
	if t <= 0 {
		return lagoon[0]
	}
	if t >= 1 {
		return lagoon[len(lagoon)-1]
	}
	scaled := t * float64(len(lagoon)-1)
	i := int(scaled)
	f := scaled - float64(i)
	return mix(lagoon[i], lagoon[i+1], f)
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
