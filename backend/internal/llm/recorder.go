package llm

import "context"

// Recorder is invoked once per LLM attempt (including each fallback try
// within a single Complete() call). Implementations should NOT block the
// caller — persist asynchronously or ensure the write path is fast.
//
// A nil Recorder is valid and simply disables persistence; the DB-backed
// implementation lives in cmd/trader/main.go to avoid a storage import
// cycle here.
type Recorder interface {
	RecordCall(ctx context.Context, rec CallRecord)
}

// CallRecord is the payload passed to a Recorder. Field semantics mirror
// domain.LLMCall; this intermediate type keeps the llm package free of
// a storage dependency.
type CallRecord struct {
	Operation         string
	AttemptIndex      int
	ModelRequested    string
	ModelUsed         string
	Outcome           string
	PromptTokens      int
	CompletionTokens  int
	TotalTokens       int
	// PromptCostUSD / CompletionCostUSD / TotalCostUSD are set by the
	// client using its configured PriceTable. Callers normally only
	// read these in tests.
	PromptCostUSD     string // decimal string, 8dp
	CompletionCostUSD string
	TotalCostUSD      string
	LatencyMS         int64
	RequestBytes      int
	ResponseBytes     int
	RequestMessages   string // JSON-encoded []Message
	ResponseText      string
	ErrorMessage      string
	TraceID           string
	SpanID            string
	Temperature       float64
	MaxTokens         int
	JSONMode          bool
}

// operationKey is an unexported context key so no external package can
// collide with it.
type operationKey struct{}

// WithOperation tags ctx with a caller-chosen operation label (e.g.
// "news.analyse"). The LLM client writes this label into the persisted
// call record so you can slice cost by subsystem.
func WithOperation(ctx context.Context, op string) context.Context {
	if op == "" {
		return ctx
	}
	return context.WithValue(ctx, operationKey{}, op)
}

// OperationFrom reads the operation label previously set via
// WithOperation. Returns "" when none was set.
func OperationFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(operationKey{}).(string); ok {
		return v
	}
	return ""
}
