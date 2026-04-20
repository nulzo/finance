package cli

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// jsonTokenRegex tokenises a JSON-ish string into keys, string values,
// literals (true/false/null) and numbers. The expression is
// intentionally forgiving because we also apply it to values emitted by
// our logger that may only loosely resemble JSON.
var jsonTokenRegex = regexp.MustCompile(
	`("(\\u[a-zA-Z0-9]{4}|\\[^u]|[^\\"])*"(\s*:)?|\b(true|false|null)\b|-?\d+(?:\.\d*)?(?:[eE][+\-]?\d+)?)`,
)

// HighlightJSON returns s with ANSI colour escapes applied to keys,
// string values, booleans, null and numbers. When colour is disabled
// (NO_COLOR) the input is returned verbatim.
//
//   - Keys  -> blue
//   - Strings -> green
//   - Bool  -> yellow
//   - null  -> dim
//   - Numbers -> purple
func HighlightJSON(s string) string {
	if !Enabled() {
		return s
	}
	return jsonTokenRegex.ReplaceAllStringFunc(s, func(tok string) string {
		switch {
		case strings.HasSuffix(tok, ":"):
			key := tok[:len(tok)-1]
			return fmt.Sprintf("%s%s%s:", Blue, key, ResetCode)
		case strings.HasPrefix(tok, "\""):
			return fmt.Sprintf("%s%s%s", Green, tok, ResetCode)
		case tok == "true" || tok == "false":
			return fmt.Sprintf("%s%s%s", Yellow, tok, ResetCode)
		case tok == "null":
			return fmt.Sprintf("%s%s%s", DimCode, tok, ResetCode)
		default:
			return fmt.Sprintf("%s%s%s", Purple, tok, ResetCode)
		}
	})
}

// PrettyFormat marshals v as indented JSON and colourises it. If v is
// already a []byte / string it is assumed to be JSON-ish and passed
// through the highlighter directly.
func PrettyFormat(v any) string {
	var s string
	switch t := v.(type) {
	case []byte:
		s = string(t)
	case string:
		s = t
	default:
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return fmt.Sprintf("%+v", v)
		}
		s = string(b)
	}
	return HighlightJSON(s)
}

// PrettyPrint is PrettyFormat + fmt.Println — handy in debug scripts.
func PrettyPrint(v any) { fmt.Println(PrettyFormat(v)) }
