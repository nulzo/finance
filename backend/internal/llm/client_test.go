package llm_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nulzo/trader/internal/llm"
)

func newStub(respBody string, status int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = b
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(respBody))
	}))
}

func TestClient_Unavailable(t *testing.T) {
	// With no base URL the client is unavailable regardless of key.
	c := llm.NewClient("k", "", "m", nil)
	// Force empty base URL – NewClient normally supplies a default.
	c.BaseURL = ""
	_, _, err := c.Complete(context.Background(), []llm.Message{{Role: "user", Content: "hi"}})
	assert.ErrorIs(t, err, llm.ErrUnavailable)
}

// TestClient_NoAPIKey covers the local-router use case where the endpoint
// does not require authentication. The Authorization header must be
// omitted and the call should still succeed.
func TestClient_NoAPIKey(t *testing.T) {
	var sawAuth string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
	}))
	defer s.Close()
	c := llm.NewClient("", s.URL, "primary", nil)
	require.True(t, c.Available(), "client should be available without api key when base url is set")
	txt, _, err := c.Complete(context.Background(), []llm.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)
	assert.Equal(t, "hi", txt)
	assert.Empty(t, sawAuth, "Authorization header should be omitted when no api key is configured")
}

func TestClient_CompleteSuccess(t *testing.T) {
	body := `{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`
	s := newStub(body, 200)
	defer s.Close()
	c := llm.NewClient("k", s.URL, "primary", []string{"fb"})
	txt, model, err := c.Complete(context.Background(), []llm.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)
	assert.Equal(t, "hello", txt)
	assert.Equal(t, "primary", model)
}

func TestClient_FallbackOnFailure(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, "boom", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer s.Close()
	c := llm.NewClient("k", s.URL, "primary", []string{"fallback-1"})
	txt, model, err := c.Complete(context.Background(), []llm.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)
	assert.Equal(t, "ok", txt)
	assert.Equal(t, "fallback-1", model)
	assert.Equal(t, 2, calls)
}

func TestClient_CompleteJSON_ExtractsFence(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"choices": []map[string]any{{"message": map[string]any{
			"role":    "assistant",
			"content": "```json\n{\"sentiment\": 0.5}\n```",
		}}},
	})
	s := newStub(string(body), 200)
	defer s.Close()
	c := llm.NewClient("k", s.URL, "primary", nil)
	var out struct {
		Sentiment float64 `json:"sentiment"`
	}
	_, err := c.CompleteJSON(context.Background(), []llm.Message{{Role: "user", Content: "hi"}}, &out)
	require.NoError(t, err)
	assert.InEpsilon(t, 0.5, out.Sentiment, 0.0001)
}

func TestClient_AllModelsFail(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", 500)
	}))
	defer s.Close()
	c := llm.NewClient("k", s.URL, "p", []string{"fb"})
	_, _, err := c.Complete(context.Background(), []llm.Message{{Role: "user", Content: "hi"}})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "500") || strings.Contains(err.Error(), "failed"))
}

// TestClient_EmptyContentFailsAndFallsBack reproduces the real-world
// failure signature in the ops logs — the primary model returned a
// valid response envelope but with `message.content: ""`, which used
// to slip through as "ok" and then bubble up to callers as
// "decode json: unexpected end of JSON input (raw=)". The fix turns
// empty content into a retryable failure so the fallback model
// engages and the diagnostic surfaces the finish_reason.
func TestClient_EmptyContentFailsAndFallsBack(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			// Primary: empty content + content_filter finish_reason
			// mirrors what the prism gateway was sending us.
			_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"content_filter","message":{"role":"assistant","content":""}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"ok\":true}"}}]}`))
	}))
	defer s.Close()

	c := llm.NewClient("k", s.URL, "primary", []string{"fallback-1"})
	var out struct {
		OK bool `json:"ok"`
	}
	_, err := c.CompleteJSON(context.Background(), []llm.Message{{Role: "user", Content: "hi"}}, &out)
	require.NoError(t, err, "fallback model should recover from empty primary content")
	assert.True(t, out.OK)
	assert.Equal(t, 2, calls, "fallback must be invoked after empty content on primary")
}

// TestClient_EmptyContentAllModels surfaces an informative error
// (finish reason + diagnostics) rather than the original
// "unexpected end of JSON input" confusion when every model comes
// back empty.
func TestClient_EmptyContentAllModels(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":"","tool_calls":[{"id":"t1"}]}}]}`))
	}))
	defer s.Close()
	c := llm.NewClient("k", s.URL, "primary", []string{"fallback-1"})
	_, _, err := c.Complete(context.Background(), []llm.Message{{Role: "user", Content: "hi"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty content")
	assert.Contains(t, err.Error(), "finish=tool_calls")
	assert.Contains(t, err.Error(), "tool_calls=1")
}

// TestClient_StripsExtensionsOnFallback verifies that when the
// primary attempt returns empty content, the fallback request is
// sent *without* the default extensions. A misconfigured extension
// (unreachable searxng) is the most common cause of empty content;
// stripping the extensions on the second attempt lets the bare
// request succeed.
func TestClient_StripsExtensionsOnFallback(t *testing.T) {
	type seen struct {
		Extensions []llm.Extension `json:"extensions"`
	}
	var requests []seen
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var got seen
		_ = json.Unmarshal(body, &got)
		requests = append(requests, got)
		w.Header().Set("Content-Type", "application/json")
		if len(requests) == 1 {
			_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":""}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer s.Close()

	c := llm.NewClient("k", s.URL, "primary", []string{"fallback-1"},
		llm.WithDefaultExtensions(llm.Extension{
			ID:      "prism:web_search",
			Enabled: true,
			Config:  llm.ExtensionConfig{"searxng_url": "http://unreachable"},
		}),
	)
	txt, model, err := c.Complete(context.Background(), []llm.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)
	assert.Equal(t, "ok", txt)
	assert.Equal(t, "fallback-1", model)
	require.Len(t, requests, 2)
	assert.NotEmpty(t, requests[0].Extensions, "primary attempt should include default extensions")
	assert.Empty(t, requests[1].Extensions, "fallback attempt should strip extensions after empty content")
}

// TestClient_DefaultExtensionsMerge confirms that caller-supplied
// extensions shadow client-level defaults with the same ID, and
// that absent-ID defaults still ride along.
func TestClient_DefaultExtensionsMerge(t *testing.T) {
	type seen struct {
		Extensions []llm.Extension `json:"extensions"`
	}
	var got seen
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer s.Close()

	c := llm.NewClient("k", s.URL, "primary", nil,
		llm.WithDefaultExtensions(
			llm.Extension{ID: "prism:web_search", Enabled: true, Config: llm.ExtensionConfig{"max_results": 3}},
			llm.Extension{ID: "prism:datetime", Enabled: true, Config: llm.ExtensionConfig{"default_timezone": "UTC"}},
		),
	)
	_, _, err := c.Complete(context.Background(), []llm.Message{{Role: "user", Content: "hi"}},
		llm.WithExtensions(llm.Extension{
			ID:      "prism:web_search",
			Enabled: true,
			Config:  llm.ExtensionConfig{"max_results": 10},
		}),
	)
	require.NoError(t, err)
	require.Len(t, got.Extensions, 2)
	ids := []string{got.Extensions[0].ID, got.Extensions[1].ID}
	assert.ElementsMatch(t, []string{"prism:web_search", "prism:datetime"}, ids)
	// The caller's web_search override (max_results=10) must win over
	// the default (max_results=3).
	for _, e := range got.Extensions {
		if e.ID == "prism:web_search" {
			assert.EqualValues(t, 10, e.Config["max_results"])
		}
	}
}
