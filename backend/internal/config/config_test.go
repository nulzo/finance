package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nulzo/trader/internal/config"
)

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("TRADER_MODE", "mock")
	t.Setenv("LLM_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("BROKER_PROVIDER", "mock")
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, config.ModeMock, cfg.Mode)
	assert.Greater(t, cfg.InitialWalletCents, int64(0))
	assert.NotNil(t, cfg.LLMFallbacks)
	assert.NotEmpty(t, cfg.LLMBaseURL, "a default LLM base URL should always be configured")
}

// Legacy OPENROUTER_* variables must still work for backwards compatibility.
func TestLoad_LegacyOpenRouterEnv(t *testing.T) {
	t.Setenv("TRADER_MODE", "mock")
	t.Setenv("LLM_API_KEY", "")
	t.Setenv("LLM_BASE_URL", "")
	t.Setenv("OPENROUTER_API_KEY", "legacy-key")
	t.Setenv("OPENROUTER_BASE_URL", "https://openrouter.ai/api/v1")
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, "legacy-key", cfg.LLMAPIKey)
	assert.Equal(t, "https://openrouter.ai/api/v1", cfg.LLMBaseURL)
}

// Newer LLM_* variables take precedence over the legacy ones.
func TestLoad_NewVarsBeatLegacy(t *testing.T) {
	t.Setenv("TRADER_MODE", "mock")
	t.Setenv("LLM_API_KEY", "new-key")
	t.Setenv("LLM_BASE_URL", "http://router.local/v1")
	t.Setenv("OPENROUTER_API_KEY", "legacy-key")
	t.Setenv("OPENROUTER_BASE_URL", "https://openrouter.ai/api/v1")
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, "new-key", cfg.LLMAPIKey)
	assert.Equal(t, "http://router.local/v1", cfg.LLMBaseURL)
}

func TestLoad_LiveRequiresAlpacaCreds(t *testing.T) {
	t.Setenv("TRADER_MODE", "live")
	t.Setenv("ALPACA_API_KEY", "")
	t.Setenv("ALPACA_API_SECRET", "")
	_, err := config.Load()
	require.Error(t, err)
}

func TestLoad_InvalidMode(t *testing.T) {
	t.Setenv("TRADER_MODE", "fake")
	_, err := config.Load()
	require.Error(t, err)
}

// A bare API_TOKEN= with a trailing `# comment` on the same line trips
// godotenv into storing the comment as the value. The loader must treat
// that as "auth disabled" instead of silently enabling auth with a
// garbage token (which 401s every /v1 call).
func TestLoad_APIToken_StripsLeakedComment(t *testing.T) {
	t.Setenv("TRADER_MODE", "mock")
	t.Setenv("API_TOKEN", "# optional: require Bearer token")
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Empty(t, cfg.APIToken, "a comment-looking token should disable auth")
}

// Whitespace-only API_TOKEN values should also disable auth.
func TestLoad_APIToken_WhitespaceOnly(t *testing.T) {
	t.Setenv("TRADER_MODE", "mock")
	t.Setenv("API_TOKEN", "     ")
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Empty(t, cfg.APIToken)
}

// A real opaque token must pass through untouched.
func TestLoad_APIToken_ValidTokenPreserved(t *testing.T) {
	t.Setenv("TRADER_MODE", "mock")
	t.Setenv("API_TOKEN", "  s3cret-abc123  ")
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, "s3cret-abc123", cfg.APIToken, "trimmed but preserved")
}
