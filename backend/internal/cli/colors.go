// Package cli provides small terminal presentation helpers (ANSI
// colours, text styling, status badges, JSON syntax highlighting).
//
// It is deliberately dependency-free and safe to import from anywhere.
// Colour output is suppressed when NO_COLOR is set (see
// https://no-color.org/) or when LOG_COLOR is explicitly disabled.
package cli

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

// ANSI attribute codes.
const (
	ResetCode     = "\033[0m"
	BoldCode      = "\033[1m"
	DimCode       = "\033[2m"
	ItalicCode    = "\033[3m"
	UnderlineCode = "\033[4m"
)

// Color is a 4-bit ANSI colour escape.
type Color string

const (
	Black   Color = "\033[30m"
	Red     Color = "\033[31m"
	Green   Color = "\033[32m"
	Yellow  Color = "\033[33m"
	Blue    Color = "\033[34m"
	Purple  Color = "\033[35m"
	Cyan    Color = "\033[36m"
	White   Color = "\033[37m"
	Default Color = "\033[39m"
)

// RGB is a 24-bit TrueColor triplet. Integer channels 0..255.
type RGB struct{ R, G, B int }

// Brand palette used for consistent log styling across the app.
var (
	BrandBlue   = RGB{0, 120, 255}
	BrandPurple = RGB{189, 52, 235}
	BrandError  = RGB{255, 87, 87}
	BrandWarn   = RGB{255, 204, 0}
	BrandInfo   = RGB{52, 152, 219}
	BrandGreen  = RGB{52, 219, 158}
)

var (
	noColor     bool
	noColorOnce sync.Once
)

// Enabled reports whether ANSI output should be emitted.
//
// Honoured signals, in order:
//  1. NO_COLOR (standard, see https://no-color.org/) — any value disables.
//  2. LOG_COLOR=false|0 — explicit opt-out.
//  3. Otherwise enabled.
func Enabled() bool {
	noColorOnce.Do(func() {
		if _, ok := os.LookupEnv("NO_COLOR"); ok {
			noColor = true
			return
		}
		if v := os.Getenv("LOG_COLOR"); v != "" {
			noColor = !(v == "true" || v == "1" || v == "yes" || v == "on")
		}
	})
	return !noColor
}

// TextStyler is a fluent builder for styled text.
type TextStyler struct{ codes []string }

// NewStyle returns an empty styler.
func NewStyle() *TextStyler { return &TextStyler{codes: make([]string, 0, 4)} }

// Bold sets the bold attribute.
func (s *TextStyler) Bold() *TextStyler { s.codes = append(s.codes, BoldCode); return s }

// Dim sets the dim attribute.
func (s *TextStyler) Dim() *TextStyler { s.codes = append(s.codes, DimCode); return s }

// Italic sets the italic attribute.
func (s *TextStyler) Italic() *TextStyler { s.codes = append(s.codes, ItalicCode); return s }

// Underline sets the underline attribute.
func (s *TextStyler) Underline() *TextStyler { s.codes = append(s.codes, UnderlineCode); return s }

// Foreground sets a 4-bit ANSI foreground colour.
func (s *TextStyler) Foreground(c Color) *TextStyler { s.codes = append(s.codes, string(c)); return s }

// FgRGB sets a 24-bit TrueColor foreground.
func (s *TextStyler) FgRGB(c RGB) *TextStyler {
	s.codes = append(s.codes, fmt.Sprintf("\033[38;2;%d;%d;%dm", c.R, c.G, c.B))
	return s
}

// BgRGB sets a 24-bit TrueColor background.
func (s *TextStyler) BgRGB(c RGB) *TextStyler {
	s.codes = append(s.codes, fmt.Sprintf("\033[48;2;%d;%d;%dm", c.R, c.G, c.B))
	return s
}

// Render wraps text in the accumulated style and a reset.
// Output is returned unchanged when colour is disabled.
func (s *TextStyler) Render(text string) string {
	if !Enabled() || len(s.codes) == 0 {
		return text
	}
	return strings.Join(s.codes, "") + text + ResetCode
}

// Stylize is a one-shot "colour this text" helper.
func Stylize(text string, color Color) string {
	return NewStyle().Foreground(color).Render(text)
}

// Gradient renders text with a linear interpolation across multiple
// colour stops. progress is clamped to [0, 1] and selects the position
// along the combined gradient. With N stops the gradient is split into
// N-1 equal-length segments.
func Gradient(text string, progress float64, stops ...RGB) string {
	if !Enabled() || len(stops) == 0 {
		return text
	}
	if len(stops) == 1 {
		return NewStyle().FgRGB(stops[0]).Render(text)
	}
	if progress < 0 {
		progress = 0
	} else if progress > 1 {
		progress = 1
	}
	n := float64(len(stops) - 1)
	seg := int(progress * n)
	if seg > len(stops)-2 {
		seg = len(stops) - 2
	}
	local := progress*n - float64(seg)
	a, b := stops[seg], stops[seg+1]
	mix := RGB{
		R: int(float64(a.R) + float64(b.R-a.R)*local),
		G: int(float64(a.G) + float64(b.G-a.G)*local),
		B: int(float64(a.B) + float64(b.B-a.B)*local),
	}
	return NewStyle().FgRGB(mix).Render(text)
}

// BoldText renders text bold.
func BoldText(text string) string { return NewStyle().Bold().Render(text) }

// DimText renders text dim.
func DimText(text string) string { return NewStyle().Dim().Render(text) }

// CheckMark returns a bold green ✔.
func CheckMark() string { return NewStyle().Foreground(Green).Bold().Render("✔") }

// CrossMark returns a bold red ✘.
func CrossMark() string { return NewStyle().Foreground(Red).Bold().Render("✘") }

// WarningSign returns a bold yellow ⚠.
func WarningSign() string { return NewStyle().Foreground(Yellow).Bold().Render("⚠") }
