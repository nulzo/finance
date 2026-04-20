package cli

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
)

// rawBanner is the ASCII art for the trader startup message. Rendered
// once at boot with a brand-colour gradient applied line-by-line. Kept
// as a raw string so you can swap fonts without touching any Go code.
const rawBanner = `
 ███████████ ███████████     █████████   ██████████   ██████████ ███████████
░█░░░███░░░█░░███░░░░░███   ███░░░░░███ ░░███░░░░███ ░░███░░░░░█░░███░░░░░███
░   ░███  ░  ░███    ░███  ░███    ░███  ░███   ░░███ ░███  █ ░  ░███    ░███
    ░███     ░██████████   ░███████████  ░███    ░███ ░██████    ░██████████
    ░███     ░███░░░░░███  ░███░░░░░███  ░███    ░███ ░███░░█    ░███░░░░░███
    ░███     ░███    ░███  ░███    ░███  ░███    ███  ░███ ░   █ ░███    ░███
    █████    █████   █████ █████   █████ ██████████   ██████████ █████   █████
   ░░░░░    ░░░░░   ░░░░░ ░░░░░   ░░░░░ ░░░░░░░░░░   ░░░░░░░░░░ ░░░░░   ░░░░░
`

// BannerInfo holds the fields rendered under the ASCII banner. All are
// optional — empty values simply skip the line.
type BannerInfo struct {
	Tagline     string
	Version     string
	Commit      string
	Environment string
	Mode        string
	Addr        string
	PortfolioID string
	GithubURL   string
}

// PrintBanner writes the ASCII banner followed by a metadata block to
// w. Colours are automatically suppressed when NO_COLOR is set. Call
// this once from main() right before starting the HTTP server.
//
// Pass nil to write to os.Stdout.
func PrintBanner(w io.Writer, info BannerInfo) {
	if w == nil {
		w = os.Stdout
	}
	lines := strings.Split(rawBanner, "\n")
	if len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			fmt.Fprintln(w)
			continue
		}
		ratio := 0.0
		if len(lines) > 1 {
			ratio = float64(i) / float64(len(lines)-1)
		}
		fmt.Fprintln(w, Gradient(line, ratio, BrandGreen, BrandBlue, BrandPurple))
	}

	if info.Tagline != "" {
		fmt.Fprintln(w, BoldText("   "+info.Tagline))
	}
	fmt.Fprintln(w)

	row := func(k, v string) {
		if v == "" {
			return
		}
		fmt.Fprintf(w, "   %s  %s\n", DimText(PadRight(k+":", 13)), BoldText(v))
	}
	row("Version", info.Version)
	row("Commit", info.Commit)
	row("Go", runtime.Version())
	row("Environment", info.Environment)
	row("Mode", info.Mode)
	row("Addr", info.Addr)
	row("Portfolio", info.PortfolioID)
	row("Github", info.GithubURL)

	fmt.Fprintln(w, "   "+DimText(strings.Repeat("─", 58)))
	fmt.Fprintln(w)
}

// PadRight right-pads s with spaces so the result has length at least
// n. Shorter than n is left untouched when s is already wide enough.
func PadRight(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}
