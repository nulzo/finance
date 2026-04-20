// Package llm wraps an OpenAI-compatible chat completions endpoint with
// retries, a structured-output helper, and fallback-model support.
//
// The client is deliberately provider-agnostic: it works against OpenAI
// directly, OpenRouter, a self-hosted model-router (e.g. the sibling
// model-router-api project), or any other endpoint that speaks the
// OpenAI /v1/chat/completions wire format. Authentication is optional –
// when no API key is configured the Authorization header is omitted, which
// is what local routers typically expect.
//
// When the primary model fails (timeout, 5xx, non-JSON response where
// JSON is required) the client retries with each fallback model in order
// before returning the last error.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/nulzo/trader/internal/domain"
	"github.com/nulzo/trader/internal/telemetry"
)

// llmTracer scopes LLM spans under the package's import path so they
// group cleanly in trace backends (e.g. Jaeger / Tempo).
var llmTracer = otel.Tracer("github.com/nulzo/trader/internal/llm")

// Message is a single chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ExtensionConfig map[string]any

// Extension represents a model extension (e.g., prism:web_search).
type Extension struct {
	ID      string          `json:"id"`
	Enabled bool            `json:"enabled"`
	Config  ExtensionConfig `json:"config,omitempty"`
}

// Request is a simplified chat completion request.
type Request struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	// ResponseFormat requests structured JSON output.
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
	// Extensions enables provider-specific extensions like web search.
	Extensions []Extension `json:"extensions,omitempty"`
}

// ResponseFormat instructs the model to emit JSON.
type ResponseFormat struct {
	Type string `json:"type"` // "json_object"
}

// Response captures the chat completion response we care about.
// ResponseMessage intentionally carries `Refusal` and `ToolCalls` in
// addition to the plain content because some providers — notably
// when a web-search / tool-use extension is enabled — respond with
// an empty `content` alongside populated `tool_calls` or a safety
// `refusal`. We keep those so the caller can generate a useful
// diagnostic instead of the opaque "empty content" error.
type ResponseMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	Refusal   string           `json:"refusal,omitempty"`
	ToolCalls []map[string]any `json:"tool_calls,omitempty"`
}

type Response struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message ResponseMessage `json:"message"`
		Finish  string          `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// Client talks to an OpenAI-compatible chat completion endpoint.
// It is safe to use without an API key against a router/proxy that does
// not require one (e.g. a local model-router with auth disabled).
type Client struct {
	APIKey    string
	BaseURL   string
	Primary   string
	Fallbacks []string
	HTTP      *http.Client
	// Referrer/Title are forwarded via HTTP headers (used by OpenRouter
	// for ranking and attribution). Both are optional and safely ignored
	// by other providers/routers.
	Referrer string
	Title    string
	// Pricing resolves per-model cost; when nil all calls are recorded
	// with zero cost. DefaultPriceTable covers common providers.
	Pricing *PriceTable
	// Recorder persists each attempt for auditing and cost tracking.
	// A nil Recorder disables persistence silently.
	Recorder Recorder
	// DefaultExtensions are appended to every outbound request
	// before any caller-supplied extensions. Useful for wiring
	// provider-specific extensions (e.g. `prism:web_search`,
	// `prism:datetime`) in one place instead of duplicating the
	// config across `AnalyseNews`, `Decide`, etc. Safe to leave
	// nil — the gateway ignores the key when absent.
	DefaultExtensions []Extension
}

// Option tunes client construction.
type Option func(*Client)

// WithHTTPClient replaces the default HTTP client.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.HTTP = h } }

// WithReferrer sets the HTTP-Referer header.
func WithReferrer(s string) Option { return func(c *Client) { c.Referrer = s } }

// WithTitle sets the X-Title header.
func WithTitle(s string) Option { return func(c *Client) { c.Title = s } }

// WithPricing installs a price table used to compute per-call cost.
func WithPricing(p *PriceTable) Option { return func(c *Client) { c.Pricing = p } }

// WithRecorder installs a persistence hook invoked once per attempt.
func WithRecorder(r Recorder) Option { return func(c *Client) { c.Recorder = r } }

// WithDefaultExtensions installs a slice of extensions that are
// merged into every outgoing request. Only extensions with
// `Enabled=true` are meaningful; disabled ones are still sent so
// the gateway can explicitly acknowledge them.
func WithDefaultExtensions(exts ...Extension) Option {
	return func(c *Client) { c.DefaultExtensions = append(c.DefaultExtensions, exts...) }
}

// NewClient constructs a client. The API key is optional – when empty
// the Authorization header is omitted so the client can be pointed at a
// local router with auth disabled. If the baseURL is also empty the
// client is considered unavailable and calls will return ErrUnavailable;
// callers should use a deterministic fallback path in that case.
func NewClient(apiKey, baseURL, primary string, fallbacks []string, opts ...Option) *Client {
	if baseURL == "" {
		baseURL = "https://openrouter.ai/api/v1"
	}
	c := &Client{
		APIKey:    apiKey,
		BaseURL:   strings.TrimRight(baseURL, "/"),
		Primary:   primary,
		Fallbacks: fallbacks,
		HTTP:      &http.Client{Timeout: 45 * time.Second},
		Referrer:  "https://github.com/nulzo/trader",
		Title:     "trader",
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// ErrUnavailable indicates the client has no endpoint configured.
var ErrUnavailable = errors.New("llm: unavailable (no base url)")

// Available reports whether the client has an endpoint to call. The API
// key is intentionally optional – local routers frequently run without
// auth and will reject a bogus bearer token, so we only require a
// base URL to consider the client usable.
func (c *Client) Available() bool { return c != nil && c.BaseURL != "" }

// hasWebSearch reports whether the client has an enabled web-search
// extension configured. Prompts use this to decide whether to tell
// the model it can browse the web — an unconditional "you have
// internet access" claim hallucinated against a vanilla OpenAI
// endpoint produces fabricated citations.
func (c *Client) hasWebSearch() bool {
	if c == nil {
		return false
	}
	for _, e := range c.DefaultExtensions {
		if e.Enabled && strings.Contains(e.ID, "web_search") {
			return true
		}
	}
	return false
}

// Complete sends a chat completion, retrying across fallback models.
// The returned ModelUsed is the model that produced the final response.
func (c *Client) Complete(ctx context.Context, messages []Message, opts ...CompleteOpt) (string, string, error) {
	if !c.Available() {
		return "", "", ErrUnavailable
	}
	o := completeOpts{Temperature: 0.2, MaxTokens: 600}
	for _, op := range opts {
		op(&o)
	}
	models := append([]string{c.Primary}, c.Fallbacks...)
	// Merge client-level default extensions with call-level ones. Call-
	// level extensions win on ID collision because they're the more
	// specific override.
	exts := mergeExtensions(c.DefaultExtensions, o.Extensions)
	// attemptErr tracks every per-model failure so the final error can
	// surface the full chain instead of just the last model's message.
	// Without this, operators see "model not found: ollama/llama3.1"
	// and assume ollama is being used primarily, when in fact it's the
	// last fallback and the real failures are upstream in the chain.
	type attemptErr struct {
		model string
		err   error
	}
	var attempts []attemptErr
	for i, m := range models {
		req := Request{
			Model:       m,
			Messages:    messages,
			Temperature: o.Temperature,
			MaxTokens:   o.MaxTokens,
			Extensions:  exts,
		}
		if o.JSON {
			req.ResponseFormat = &ResponseFormat{Type: "json_object"}
		}
		text, err := c.callOnce(ctx, req, i, o.JSON)
		if err == nil {
			return text, m, nil
		}
		attempts = append(attempts, attemptErr{model: m, err: err})
		if ctx.Err() != nil {
			return "", "", ctx.Err()
		}
		// On empty-content / safety-refusal failures, strip extensions
		// for the next attempt. A misconfigured gateway extension is
		// the single most common cause of empty content; the fallback
		// model is unlikely to do better with the same extensions
		// applied. The plain request usually succeeds.
		if errors.Is(err, errEmptyContent) {
			exts = nil
		}
	}
	if len(attempts) == 0 {
		return "", "", fmt.Errorf("%w: all models failed", domain.ErrProviderFailure)
	}
	// Build a chain summary like:
	//   provider failure: all 3 models failed;
	//     [1] google/gemini-3.1-flash-lite-preview: 400 bad request: ...;
	//     [2] anthropic/claude-3.5-haiku: empty content (finish=length);
	//     [3] ollama/llama3.1: 400 bad request: model not found
	// so operators can tell immediately which models were tried and why
	// each one failed, instead of seeing only the last fallback's error
	// and assuming that model was being used primarily.
	var b strings.Builder
	fmt.Fprintf(&b, "all %d models failed", len(attempts))
	for i, a := range attempts {
		fmt.Fprintf(&b, "; [%d] %s: %v", i+1, a.model, a.err)
	}
	return "", "", fmt.Errorf("%w: %s", domain.ErrProviderFailure, b.String())
}

// CompleteJSON calls Complete in JSON mode and unmarshals the body into out.
func (c *Client) CompleteJSON(ctx context.Context, messages []Message, out any, opts ...CompleteOpt) (string, error) {
	opts = append(opts, WithJSON())
	raw, model, err := c.Complete(ctx, messages, opts...)
	if err != nil {
		return model, err
	}
	cleaned := extractJSON(raw)
	if err := json.Unmarshal([]byte(cleaned), out); err != nil {
		return model, fmt.Errorf("%w: decode json: %v (raw=%s)", domain.ErrProviderFailure, err, truncate(raw, 200))
	}
	return model, nil
}

type completeOpts struct {
	Temperature float64
	MaxTokens   int
	JSON        bool
	Extensions  []Extension
}

// CompleteOpt tunes a single call.
type CompleteOpt func(*completeOpts)

// WithTemperature overrides the temperature.
func WithTemperature(t float64) CompleteOpt { return func(o *completeOpts) { o.Temperature = t } }

// WithMaxTokens overrides the response cap.
func WithMaxTokens(n int) CompleteOpt { return func(o *completeOpts) { o.MaxTokens = n } }

// WithJSON asks the model to respond in JSON.
func WithJSON() CompleteOpt { return func(o *completeOpts) { o.JSON = true } }

// WithExtensions enables provider-specific extensions.
func WithExtensions(exts ...Extension) CompleteOpt {
	return func(o *completeOpts) { o.Extensions = append(o.Extensions, exts...) }
}

func (c *Client) callOnce(ctx context.Context, req Request, attempt int, jsonMode bool) (string, error) {
	ctx, span := llmTracer.Start(ctx, "llm.chat.completions",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("gen_ai.system", "openai"),
			attribute.String("gen_ai.request.model", req.Model),
			attribute.String("server.address", c.BaseURL),
			attribute.Float64("gen_ai.request.temperature", req.Temperature),
			attribute.Int("gen_ai.request.max_tokens", req.MaxTokens),
		),
	)
	defer span.End()
	start := time.Now()

	// rec accumulates the audit record; finalize() is called from every
	// exit path (including error paths) so every attempt lands in the DB.
	rec := CallRecord{
		Operation:      OperationFrom(ctx),
		AttemptIndex:   attempt,
		ModelRequested: req.Model,
		ModelUsed:      req.Model,
		Outcome:        "ok",
		Temperature:    req.Temperature,
		MaxTokens:      req.MaxTokens,
		JSONMode:       jsonMode,
	}
	if msgBytes, mErr := json.Marshal(req.Messages); mErr == nil {
		rec.RequestMessages = string(msgBytes)
		rec.RequestBytes = len(msgBytes)
	}
	if sc := span.SpanContext(); sc.IsValid() {
		rec.TraceID = sc.TraceID().String()
		rec.SpanID = sc.SpanID().String()
	}
	var finalText string
	defer func() {
		rec.LatencyMS = time.Since(start).Milliseconds()
		if c.Pricing != nil {
			// Price on ModelUsed first (authoritative — that's what the
			// provider actually ran). If that lookup yields $0 and the
			// requested name differs, fall back to ModelRequested. This
			// catches proxies that rewrite the response's model id
			// (e.g. stripping the "google/" prefix) so we don't silently
			// bill every call at zero.
			p, cc, total := c.Pricing.Cost(rec.ModelUsed, rec.PromptTokens, rec.CompletionTokens)
			if total.IsZero() && rec.ModelRequested != "" && rec.ModelRequested != rec.ModelUsed {
				if _, ok := c.Pricing.Lookup(rec.ModelRequested); ok {
					p, cc, total = c.Pricing.Cost(rec.ModelRequested, rec.PromptTokens, rec.CompletionTokens)
				}
			}
			rec.PromptCostUSD = p.String()
			rec.CompletionCostUSD = cc.String()
			rec.TotalCostUSD = total.String()
		} else {
			rec.PromptCostUSD, rec.CompletionCostUSD, rec.TotalCostUSD = "0", "0", "0"
		}
		rec.ResponseText = finalText
		rec.ResponseBytes = len(finalText)
		if c.Recorder != nil {
			c.Recorder.RecordCall(ctx, rec)
		}
	}()

	body, err := json.Marshal(req)
	if err != nil {
		rec.Outcome = "marshal_error"
		rec.ErrorMessage = err.Error()
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		telemetry.App.RecordLLMCall(ctx, req.Model, rec.Outcome, time.Since(start), 0, 0)
		return "", err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		rec.Outcome = "request_error"
		rec.ErrorMessage = err.Error()
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		telemetry.App.RecordLLMCall(ctx, req.Model, rec.Outcome, time.Since(start), 0, 0)
		return "", err
	}
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.Referrer != "" {
		httpReq.Header.Set("HTTP-Referer", c.Referrer)
	}
	if c.Title != "" {
		httpReq.Header.Set("X-Title", c.Title)
	}
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		rec.Outcome = "transport_error"
		rec.ErrorMessage = err.Error()
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		telemetry.App.RecordLLMCall(ctx, req.Model, rec.Outcome, time.Since(start), 0, 0)
		return "", fmt.Errorf("%w: %v", domain.ErrProviderFailure, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	span.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))
	if resp.StatusCode >= 400 {
		outErr := fmt.Errorf("%w: %s: %s", domain.ErrProviderFailure, resp.Status, truncate(string(raw), 240))
		rec.Outcome = fmt.Sprintf("http_%d", resp.StatusCode)
		rec.ErrorMessage = truncate(string(raw), 2000)
		finalText = string(raw)
		span.RecordError(outErr)
		span.SetStatus(codes.Error, resp.Status)
		telemetry.App.RecordLLMCall(ctx, req.Model, rec.Outcome, time.Since(start), 0, 0)
		return "", outErr
	}
	var r Response
	if err := json.Unmarshal(raw, &r); err != nil {
		rec.Outcome = "decode_error"
		rec.ErrorMessage = err.Error()
		finalText = string(raw)
		span.RecordError(err)
		span.SetStatus(codes.Error, "decode")
		telemetry.App.RecordLLMCall(ctx, req.Model, rec.Outcome, time.Since(start), 0, 0)
		return "", fmt.Errorf("%w: decode: %v", domain.ErrProviderFailure, err)
	}
	if r.Model != "" {
		rec.ModelUsed = r.Model
	}
	rec.PromptTokens = r.Usage.PromptTokens
	rec.CompletionTokens = r.Usage.CompletionTokens
	rec.TotalTokens = r.Usage.TotalTokens
	if rec.TotalTokens == 0 {
		rec.TotalTokens = rec.PromptTokens + rec.CompletionTokens
	}
	if len(r.Choices) == 0 {
		outErr := fmt.Errorf("%w: empty choices", domain.ErrProviderFailure)
		rec.Outcome = "empty_choices"
		rec.ErrorMessage = "provider returned no choices"
		span.RecordError(outErr)
		span.SetStatus(codes.Error, "empty")
		telemetry.App.RecordLLMCall(ctx, req.Model, rec.Outcome, time.Since(start), r.Usage.PromptTokens, r.Usage.CompletionTokens)
		return "", outErr
	}
	span.SetAttributes(
		attribute.String("gen_ai.response.model", r.Model),
		attribute.String("gen_ai.response.finish_reason", r.Choices[0].Finish),
		attribute.Int("gen_ai.usage.input_tokens", r.Usage.PromptTokens),
		attribute.Int("gen_ai.usage.output_tokens", r.Usage.CompletionTokens),
	)
	finalText = strings.TrimSpace(r.Choices[0].Message.Content)
	// Empty content is its own failure mode — it used to slip through
	// as "ok" and then CompleteJSON would fail to decode `""` with
	// the opaque "unexpected end of JSON input (raw=)" error. Treat
	// it as a retryable provider failure so the fallback model (or
	// a retry without extensions) gets a chance. We also capture the
	// finish_reason, refusal, tool_calls presence, and a prefix of
	// the raw body so the audit row can explain *why*.
	if finalText == "" {
		msg := r.Choices[0].Message
		reason := strings.TrimSpace(r.Choices[0].Finish)
		detail := summariseEmptyContent(reason, msg, raw)
		outErr := fmt.Errorf("%w: %w: %s", domain.ErrProviderFailure, errEmptyContent, detail)
		rec.Outcome = "empty_content"
		if reason != "" {
			rec.Outcome = "empty_content_" + reason
		}
		rec.ErrorMessage = truncate(detail, 2000)
		// Persist a short prefix of the raw body so we can forensically
		// inspect the tool_calls / refusal / reasoning payloads the
		// provider shipped without logging whole-turn transcripts.
		finalText = truncate(string(raw), 2000)
		span.RecordError(outErr)
		span.SetStatus(codes.Error, "empty_content")
		telemetry.App.RecordLLMCall(ctx, req.Model, rec.Outcome, time.Since(start), r.Usage.PromptTokens, r.Usage.CompletionTokens)
		return "", outErr
	}
	telemetry.App.RecordLLMCall(ctx, req.Model, "ok", time.Since(start), r.Usage.PromptTokens, r.Usage.CompletionTokens)
	return finalText, nil
}

// errEmptyContent is a sentinel the dispatcher matches on so it can
// react (e.g. strip extensions before the next fallback attempt). It
// is wrapped inside domain.ErrProviderFailure at the call site so
// callers that only care about the coarse-grained provider failure
// still see it.
var errEmptyContent = errors.New("empty content")

// summariseEmptyContent produces a compact diagnostic string that
// actually tells us why the provider returned empty content: finish
// reason, presence of a refusal, number of tool calls, and a short
// prefix of the raw body. This is what lands in `rec.ErrorMessage`
// so operators opening the LLM Calls page can see something better
// than "empty content".
func summariseEmptyContent(finish string, msg ResponseMessage, raw []byte) string {
	parts := []string{}
	if finish != "" {
		parts = append(parts, fmt.Sprintf("finish=%s", finish))
	}
	if s := strings.TrimSpace(msg.Refusal); s != "" {
		parts = append(parts, fmt.Sprintf("refusal=%q", truncate(s, 160)))
	}
	if n := len(msg.ToolCalls); n > 0 {
		parts = append(parts, fmt.Sprintf("tool_calls=%d", n))
	}
	if len(raw) > 0 {
		parts = append(parts, fmt.Sprintf("raw=%s", truncate(string(raw), 240)))
	}
	if len(parts) == 0 {
		return "provider returned empty content"
	}
	return strings.Join(parts, " ")
}

// mergeExtensions layers caller-supplied extensions on top of
// client-level defaults. Extensions share a primary key (ID); a
// caller override shadows a default with the same ID. Order is
// preserved: defaults first, then the (possibly overridden)
// caller-supplied list.
func mergeExtensions(defaults, overrides []Extension) []Extension {
	if len(defaults) == 0 && len(overrides) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(overrides))
	for _, e := range overrides {
		seen[e.ID] = struct{}{}
	}
	out := make([]Extension, 0, len(defaults)+len(overrides))
	for _, e := range defaults {
		if _, shadowed := seen[e.ID]; shadowed {
			continue
		}
		out = append(out, e)
	}
	out = append(out, overrides...)
	return out
}

// extractJSON finds the first JSON object/array in the text. Models
// occasionally wrap JSON in Markdown code fences; this handles that.
func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[") {
		return s
	}
	re := regexp.MustCompile(`(?s)(\{.*\}|\[.*\])`)
	if m := re.FindString(s); m != "" {
		return m
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
