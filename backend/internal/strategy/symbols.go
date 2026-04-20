package strategy

import (
	"regexp"
	"strings"
)

// tickerRE matches US-style tickers that the broker can route.
//
// Accepted:
//   - 1–5 uppercase letters   (AAPL, MSFT, F)
//   - optional class suffix   (BRK.B, BF.A)
//
// Rejected:
//   - CUSIPs / ISINs          (571903BM4)
//   - foreign exchange codes  (LM09.SG, 0QZI.IL, 3V64.TI)
//   - empty or placeholder    ("", "N/A")
//
// The check is intentionally strict: we'd rather drop an obscure US
// micro-cap than let a non-tradeable instrument flow into decisions and
// get a 400 back from the broker at order time.
var tickerRE = regexp.MustCompile(`^[A-Z]{1,5}(\.[A-Z])?$`)

// ValidTicker reports whether s looks like a tradeable US ticker. It
// normalises whitespace + case before testing so callers don't have to.
func ValidTicker(s string) bool {
	s = strings.ToUpper(strings.TrimSpace(s))
	if s == "" || s == "N/A" {
		return false
	}
	return tickerRE.MatchString(s)
}
