package llm

import (
	"os"
	"testing"

	"github.com/shopspring/decimal"
)

func TestPriceTable_LookupExact(t *testing.T) {
	tbl := DefaultPriceTable()
	p, ok := tbl.Lookup("openai/gpt-4o-mini")
	if !ok {
		t.Fatalf("expected exact hit for openai/gpt-4o-mini")
	}
	if !p.InputPer1M.Equal(decimal.NewFromFloat(0.15)) {
		t.Errorf("wrong input price: %s", p.InputPer1M)
	}
}

func TestPriceTable_LookupProviderSuffix(t *testing.T) {
	tbl := DefaultPriceTable()
	// "gpt-4o-mini" without the provider prefix should still resolve
	// because the table allows stripping the provider.
	tbl.Set("gpt-4o-mini", ModelPrice{
		InputPer1M:  decimal.NewFromInt(1),
		OutputPer1M: decimal.NewFromInt(2),
	})
	p, ok := tbl.Lookup("openrouter/gpt-4o-mini")
	if !ok {
		t.Fatal("expected suffix match")
	}
	if !p.InputPer1M.Equal(decimal.NewFromInt(1)) {
		t.Errorf("expected suffix price, got %s", p.InputPer1M)
	}
}

func TestPriceTable_LookupReverseProviderPrefix(t *testing.T) {
	// Regression: some local routers / proxies strip the provider
	// prefix from the response's "model" field (e.g. return
	// "gemini-3.1-flash-lite-preview" for a request to
	// "google/gemini-3.1-flash-lite-preview"). Pricing must still
	// resolve via the un-prefixed suffix.
	tbl := NewPriceTable(map[string]ModelPrice{
		"google/gemini-3.1-flash-lite-preview": {
			InputPer1M:  decimal.NewFromFloat(0.075),
			OutputPer1M: decimal.NewFromFloat(0.30),
		},
	})
	p, ok := tbl.Lookup("gemini-3.1-flash-lite-preview")
	if !ok {
		t.Fatal("expected reverse prefix match")
	}
	if !p.InputPer1M.Equal(decimal.NewFromFloat(0.075)) {
		t.Errorf("wrong price: %s", p.InputPer1M)
	}
}

func TestPriceTable_LookupWildcard(t *testing.T) {
	tbl := NewPriceTable(map[string]ModelPrice{
		"google/gemini-*": {InputPer1M: decimal.NewFromFloat(0.5), OutputPer1M: decimal.NewFromFloat(1.0)},
	})
	p, ok := tbl.Lookup("google/gemini-9.0-ultra")
	if !ok {
		t.Fatal("expected wildcard match")
	}
	if !p.InputPer1M.Equal(decimal.NewFromFloat(0.5)) {
		t.Errorf("wrong wildcard price: %s", p.InputPer1M)
	}
}

func TestPriceTable_LookupFallback(t *testing.T) {
	tbl := NewPriceTable(map[string]ModelPrice{
		"default": {InputPer1M: decimal.NewFromFloat(7), OutputPer1M: decimal.NewFromFloat(9)},
	})
	p, ok := tbl.Lookup("some/unknown-model")
	if !ok {
		t.Fatal("expected default")
	}
	if !p.InputPer1M.Equal(decimal.NewFromFloat(7)) {
		t.Errorf("wrong default price: %s", p.InputPer1M)
	}
}

func TestPriceTable_CostMath(t *testing.T) {
	tbl := NewPriceTable(map[string]ModelPrice{
		"m": {InputPer1M: decimal.NewFromFloat(1.00), OutputPer1M: decimal.NewFromFloat(2.00)},
	})
	// $1/1M input * 500k tokens  = $0.50
	// $2/1M output * 250k tokens = $0.50
	// total                     = $1.00
	in, out, total := tbl.Cost("m", 500_000, 250_000)
	if !in.Equal(decimal.NewFromFloat(0.5)) {
		t.Errorf("prompt cost: got %s want 0.5", in)
	}
	if !out.Equal(decimal.NewFromFloat(0.5)) {
		t.Errorf("completion cost: got %s want 0.5", out)
	}
	if !total.Equal(decimal.NewFromFloat(1)) {
		t.Errorf("total cost: got %s want 1", total)
	}
}

func TestPriceTable_CostUnknownIsZero(t *testing.T) {
	tbl := DefaultPriceTable()
	in, out, total := tbl.Cost("no-such-model-anywhere", 1_000_000, 1_000_000)
	if !in.IsZero() || !out.IsZero() || !total.IsZero() {
		t.Errorf("unknown model should cost zero, got in=%s out=%s total=%s", in, out, total)
	}
}

func TestLoadPriceTableFromEnv_Merges(t *testing.T) {
	t.Setenv("LLM_PRICING_JSON", `{"openai/gpt-4o-mini": {"input_per_1m": "0.99", "output_per_1m": "1.99"}}`)
	tbl, err := LoadPriceTableFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	p, ok := tbl.Lookup("openai/gpt-4o-mini")
	if !ok {
		t.Fatal("expected override")
	}
	if !p.InputPer1M.Equal(decimal.NewFromFloat(0.99)) {
		t.Errorf("override not applied: %s", p.InputPer1M)
	}
	// unrelated entries still present
	if _, ok := tbl.Lookup("anthropic/claude-3.5-sonnet"); !ok {
		t.Error("default entries should survive merge")
	}
}

func TestLoadPriceTableFromEnv_MissingIsOK(t *testing.T) {
	os.Unsetenv("LLM_PRICING_JSON")
	tbl, err := LoadPriceTableFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := tbl.Lookup("openai/gpt-4o-mini"); !ok {
		t.Error("defaults should still be present")
	}
}
