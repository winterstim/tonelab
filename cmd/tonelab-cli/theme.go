package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The palette: fire. Acid red with a touch of pink at one end, pure acid
// yellow at the other, for everything that is chrome. Outcomes get their
// own colours so a glance tells success from refusal without reading, and
// none of them is a fire colour, so they read against it.
var (
	fire = []string{"#FF1F5A", "#FF3D3D", "#FF7A1A", "#FFC400", "#FFFF00"}

	theme = struct {
		Surface, Deep, Muted, Text, Success, Error, Warning lipgloss.Style
	}{
		Surface: lipgloss.NewStyle().Foreground(lipgloss.Color("#FF1F5A")),
		Deep:    lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFF00")),
		Muted:   lipgloss.NewStyle().Foreground(lipgloss.Color("#8A8593")),
		Text:    lipgloss.NewStyle().Foreground(lipgloss.Color("#F3F1F5")),
		Success: lipgloss.NewStyle().Foreground(lipgloss.Color("#3DDC97")),
		Error:   lipgloss.NewStyle().Foreground(lipgloss.Color("#FF4DFF")),
		Warning: lipgloss.NewStyle().Foreground(lipgloss.Color("#4DD2FF")),
	}
)

// gradient paints a string across the fire, one colour step per rune,
// which is how the banner and the prompt get their look without an image.
func gradient(text string) string {
	runes := []rune(text)
	if len(runes) == 0 {
		return ""
	}
	var out strings.Builder
	for i, r := range runes {
		out.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(fireAt(float64(i) / float64(max(len(runes)-1, 1))))).Render(string(r)))
	}
	return out.String()
}

// fireAt interpolates the palette at t in 0..1.
func fireAt(t float64) string {
	if t <= 0 {
		return fire[0]
	}
	if t >= 1 {
		return fire[len(fire)-1]
	}
	scaled := t * float64(len(fire)-1)
	i := int(scaled)
	f := scaled - float64(i)
	return mix(fire[i], fire[i+1], f)
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
