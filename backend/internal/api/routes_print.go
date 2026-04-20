package api

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/nulzo/trader/internal/cli"
)

// methodColor returns a brand-aware colour for an HTTP method. The
// colour scheme mirrors what most API tools (Postman, Bruno, Swagger)
// use so routes feel familiar at a glance.
func methodColor(method string) cli.Color {
	switch strings.ToUpper(method) {
	case "GET":
		return cli.Green
	case "POST":
		return cli.Yellow
	case "PUT", "PATCH":
		return cli.Blue
	case "DELETE":
		return cli.Red
	default:
		return cli.Cyan
	}
}

// PrintRoutes writes a pretty, method-coloured list of every mounted
// route to w. Pass nil to write to os.Stdout. Call this once at boot
// right after the server is constructed.
//
// Output is deliberately simple text (not a zerolog record) so it
// renders cleanly regardless of LOG_FORMAT — banners and route dumps
// are meant for humans.
func (s *Server) PrintRoutes(w io.Writer) {
	if w == nil {
		w = os.Stdout
	}
	routes := s.Router.Routes()
	sort.SliceStable(routes, func(i, j int) bool {
		if routes[i].Path == routes[j].Path {
			return routes[i].Method < routes[j].Method
		}
		return routes[i].Path < routes[j].Path
	})

	fmt.Fprintln(w, "   "+cli.BoldText(fmt.Sprintf("Routes (%d)", len(routes))))
	fmt.Fprintln(w, "   "+cli.DimText(strings.Repeat("─", 58)))

	// Width for method column so paths line up.
	const methodWidth = 7
	for _, r := range routes {
		method := cli.PadRight(r.Method, methodWidth)
		method = cli.NewStyle().Foreground(methodColor(r.Method)).Bold().Render(method)
		fmt.Fprintf(w, "   %s  %s\n", method, r.Path)
	}
	fmt.Fprintln(w, "   "+cli.DimText(strings.Repeat("─", 58)))
	fmt.Fprintln(w)
}
