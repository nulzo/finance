package logger

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/nulzo/trader/internal/cli"
)

// newConsoleWriter builds a zerolog.ConsoleWriter with our colour
// scheme and JSON-aware value formatter. When enableColor is false the
// output is plain text, which is what we want for non-TTY sinks.
func newConsoleWriter(out io.Writer, enableColor bool) zerolog.ConsoleWriter {
	w := zerolog.ConsoleWriter{
		Out:        out,
		TimeFormat: time.RFC3339,
		NoColor:    !enableColor || !cli.Enabled(),
	}
	if w.NoColor {
		return w
	}

	w.FormatLevel = func(i any) string {
		raw := strings.ToUpper(fmt.Sprintf("%s", i))
		switch raw {
		case "TRACE":
			return cli.NewStyle().Foreground(cli.Purple).Dim().Render(pad(raw))
		case "DEBUG":
			return cli.NewStyle().Foreground(cli.Purple).Bold().Render(pad(raw))
		case "INFO":
			return cli.NewStyle().Foreground(cli.Cyan).Bold().Render(pad(raw))
		case "WARN":
			return cli.NewStyle().FgRGB(cli.BrandWarn).Bold().Render(pad(raw))
		case "ERROR":
			return cli.NewStyle().FgRGB(cli.BrandError).Bold().Render(pad(raw))
		case "FATAL", "PANIC":
			return cli.NewStyle().BgRGB(cli.BrandError).Foreground(cli.White).Bold().Render(" " + raw + " ")
		}
		return pad(raw)
	}

	w.FormatMessage = func(i any) string {
		if i == nil {
			return ""
		}
		return cli.BoldText(fmt.Sprintf("%s", i))
	}

	w.FormatFieldName = func(i any) string {
		return cli.Stylize(fmt.Sprintf("%s=", i), cli.Blue)
	}

	w.FormatFieldValue = func(i any) string {
		s := toString(i)
		// Detect JSON-ish values (arrays / objects) and pretty-highlight
		// them inline. This is the signature "look" of the sibling
		// project's console logger.
		trimmed := strings.TrimSpace(s)
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			return cli.HighlightJSON(s)
		}
		// Literal tokens.
		switch trimmed {
		case "true", "false":
			return cli.Stylize(s, cli.Yellow)
		case "null":
			return cli.NewStyle().Dim().Render(s)
		}
		// Bare numbers in purple for easy scanning.
		if _, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return cli.Stylize(s, cli.Purple)
		}
		// Everything else is a plain value; leave the colour to the
		// level/name scheme so values don't dominate the line.
		return s
	}

	w.FormatErrFieldName = func(i any) string {
		return cli.NewStyle().FgRGB(cli.BrandError).Bold().Render(fmt.Sprintf("%s=", i))
	}
	w.FormatErrFieldValue = func(i any) string {
		return cli.NewStyle().FgRGB(cli.BrandError).Render(toString(i))
	}

	return w
}

func pad(s string) string {
	// zerolog emits variable-width level tokens (INFO/WARN vs DEBUG/
	// ERROR). A tiny pad keeps columns aligned without pulling in a
	// full tabwriter.
	const width = 5
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

func toString(i any) string {
	if i == nil {
		return ""
	}
	switch v := i.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return fmt.Sprintf("%v", v)
	}
}
