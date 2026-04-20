package llm

import (
	"encoding/json"
	"os"
	"strings"
	"sync"

	"github.com/shopspring/decimal"
)

// ModelPrice expresses a provider's per-token cost in USD per million
// tokens (the unit most providers publish). Both InputPer1M and
// OutputPer1M are expected to be non-negative.
type ModelPrice struct {
	InputPer1M  decimal.Decimal `json:"input_per_1m"`
	OutputPer1M decimal.Decimal `json:"output_per_1m"`
}

// PriceTable maps a model name (or a prefix-match pattern) to a price.
//
// Lookup rules when resolving a model:
//  1. Exact match on the full model name (e.g. "openai/gpt-4o-mini").
//  2. Exact match on the suffix after the first "/" (e.g. "gpt-4o-mini").
//  3. Longest-prefix match (e.g. "openai/gpt-4o-*" matches "openai/gpt-4o-2024-08-06").
//  4. Fall back to the "default" entry if present.
//  5. Finally, returns zero price (no cost recorded) — this is the correct
//     behaviour for local/self-hosted models.
type PriceTable struct {
	mu     sync.RWMutex
	prices map[string]ModelPrice
}

// NewPriceTable builds a table. A deep copy is taken so later mutations
// by the caller don't leak into the table.
func NewPriceTable(prices map[string]ModelPrice) *PriceTable {
	t := &PriceTable{prices: make(map[string]ModelPrice, len(prices))}
	for k, v := range prices {
		t.prices[strings.ToLower(k)] = v
	}
	return t
}

// Set overrides a single model's price (used for hot reloading / tests).
func (t *PriceTable) Set(model string, p ModelPrice) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.prices[strings.ToLower(model)] = p
}

// Lookup resolves a model to its price. The returned bool reports
// whether a price was found; callers may choose to still record a
// zero-cost row for unknown/local models.
func (t *PriceTable) Lookup(model string) (ModelPrice, bool) {
	if t == nil {
		return ModelPrice{}, false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	key := strings.ToLower(strings.TrimSpace(model))
	if key == "" {
		if p, ok := t.prices["default"]; ok {
			return p, true
		}
		return ModelPrice{}, false
	}
	if p, ok := t.prices[key]; ok {
		return p, true
	}
	// strip provider prefix: "openai/gpt-4o" -> "gpt-4o"
	if i := strings.Index(key, "/"); i >= 0 && i < len(key)-1 {
		if p, ok := t.prices[key[i+1:]]; ok {
			return p, true
		}
	}
	// reverse: match an un-prefixed query against provider-prefixed
	// entries. Some upstream proxies / local routers strip the
	// "provider/" prefix from the response's "model" field (e.g.
	// "google/gemini-2.0-flash" -> "gemini-2.0-flash"); without this
	// fallback we'd price every such call at $0.
	if !strings.Contains(key, "/") {
		for k, v := range t.prices {
			if i := strings.Index(k, "/"); i >= 0 && i < len(k)-1 && k[i+1:] == key {
				return v, true
			}
		}
	}
	// longest prefix match across wildcard-style entries. We use
	// a manual scan (table is tiny; no index needed).
	var best string
	var bestPrice ModelPrice
	for k, v := range t.prices {
		if strings.HasSuffix(k, "*") {
			pfx := strings.TrimSuffix(k, "*")
			if strings.HasPrefix(key, pfx) && len(pfx) > len(best) {
				best = pfx
				bestPrice = v
			}
		}
	}
	if best != "" {
		return bestPrice, true
	}
	if p, ok := t.prices["default"]; ok {
		return p, true
	}
	return ModelPrice{}, false
}

// Cost returns the prompt, completion and total USD cost for the given
// model + token counts. Zero-cost models (e.g. local Ollama) are
// preserved so rows still persist with explicit zero.
func (t *PriceTable) Cost(model string, promptTokens, completionTokens int) (prompt, completion, total decimal.Decimal) {
	p, _ := t.Lookup(model)
	million := decimal.NewFromInt(1_000_000)
	prompt = p.InputPer1M.Mul(decimal.NewFromInt(int64(promptTokens))).Div(million)
	completion = p.OutputPer1M.Mul(decimal.NewFromInt(int64(completionTokens))).Div(million)
	total = prompt.Add(completion)
	// Round to 8dp so rows are stable when re-aggregated.
	prompt = prompt.Round(8)
	completion = completion.Round(8)
	total = total.Round(8)
	return
}

// DefaultPriceTable is a best-effort built-in table of 2026-era list
// prices for popular providers. Override via LLM_PRICING_JSON in
// production — provider pricing changes over time.
func DefaultPriceTable() *PriceTable {
	d := func(f float64) decimal.Decimal { return decimal.NewFromFloat(f) }
	return NewPriceTable(map[string]ModelPrice{
		"openai/gpt-4o":              {InputPer1M: d(2.50), OutputPer1M: d(10.00)},
		"openai/gpt-4o-mini":         {InputPer1M: d(0.15), OutputPer1M: d(0.60)},
		"openai/gpt-4-turbo":         {InputPer1M: d(10.00), OutputPer1M: d(30.00)},
		"openai/gpt-3.5-turbo":       {InputPer1M: d(0.50), OutputPer1M: d(1.50)},
		"openai/o1-mini":             {InputPer1M: d(1.10), OutputPer1M: d(4.40)},
		"openai/o1":                  {InputPer1M: d(15.00), OutputPer1M: d(60.00)},

		"anthropic/claude-3.5-sonnet":    {InputPer1M: d(3.00), OutputPer1M: d(15.00)},
		"anthropic/claude-3.5-haiku":     {InputPer1M: d(0.80), OutputPer1M: d(4.00)},
		"anthropic/claude-3-opus":        {InputPer1M: d(15.00), OutputPer1M: d(75.00)},
		"anthropic/claude-3-sonnet":      {InputPer1M: d(3.00), OutputPer1M: d(15.00)},
		"anthropic/claude-3-haiku":       {InputPer1M: d(0.25), OutputPer1M: d(1.25)},

		"google/gemini-2.0-flash":               {InputPer1M: d(0.10), OutputPer1M: d(0.40)},
		"google/gemini-2.0-flash-lite":          {InputPer1M: d(0.075), OutputPer1M: d(0.30)},
		"google/gemini-1.5-pro":                 {InputPer1M: d(1.25), OutputPer1M: d(5.00)},
		"google/gemini-1.5-flash":               {InputPer1M: d(0.075), OutputPer1M: d(0.30)},
		"google/gemini-3.1-flash-lite-preview":  {InputPer1M: d(0.075), OutputPer1M: d(0.30)},
		"google/gemini-*":                        {InputPer1M: d(0.10), OutputPer1M: d(0.40)},

		"meta-llama/llama-3.1-405b-instruct": {InputPer1M: d(2.70), OutputPer1M: d(2.70)},
		"meta-llama/llama-3.1-70b-instruct":  {InputPer1M: d(0.60), OutputPer1M: d(0.60)},
		"meta-llama/llama-3.1-8b-instruct":   {InputPer1M: d(0.15), OutputPer1M: d(0.15)},

		"mistralai/mistral-large": {InputPer1M: d(2.00), OutputPer1M: d(6.00)},
		"mistralai/mistral-small": {InputPer1M: d(0.20), OutputPer1M: d(0.60)},

		// Prefix for self-hosted / local routers — zero cost.
		"ollama/*": {InputPer1M: d(0), OutputPer1M: d(0)},
		"local/*":  {InputPer1M: d(0), OutputPer1M: d(0)},
	})
}

// LoadPriceTableFromEnv merges user-supplied pricing over the defaults.
// The environment variable LLM_PRICING_JSON is expected to be a JSON
// object of the shape { "model/name": {"input_per_1m": 1.23, "output_per_1m": 4.56} }.
// A missing env var is not an error; the defaults are returned.
func LoadPriceTableFromEnv() (*PriceTable, error) {
	t := DefaultPriceTable()
	raw := strings.TrimSpace(os.Getenv("LLM_PRICING_JSON"))
	if raw == "" {
		return t, nil
	}
	var custom map[string]ModelPrice
	if err := json.Unmarshal([]byte(raw), &custom); err != nil {
		return t, err
	}
	for k, v := range custom {
		t.Set(k, v)
	}
	return t, nil
}
