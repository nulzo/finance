// Package logger provides the application's structured logger.
//
// It wraps zerolog with a small facade that mirrors the ergonomics of
// the sibling model-router-api project:
//
//   - A global logger initialised lazily from environment variables.
//   - A pretty, colour-aware console encoder for humans (LOG_FORMAT=console).
//   - A newline-delimited JSON encoder for log shippers (LOG_FORMAT=json).
//   - Package-level shortcuts (Info, Warn, Error, ...) that write to
//     the global logger, plus Get() for callers that want a value they
//     can pass around.
//
// Usage:
//
//	logger.SetGlobal(logger.New(logger.DefaultConfig()))
//	logger.Info().Str("mode", "mock").Msg("starting")
//
// Or, for dependency-injected code paths, accept `zerolog.Logger` and
// call it directly — no facade required.
package logger

import (
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// Format identifies the on-wire encoding for a logger.
type Format string

const (
	// FormatConsole is the human-readable, optionally-colourised encoder.
	FormatConsole Format = "console"
	// FormatJSON is newline-delimited JSON, one record per line.
	FormatJSON Format = "json"
)

// Config controls a logger instance. Zero values resolve to sane
// defaults in New().
type Config struct {
	// Level is one of debug|info|warn|error|fatal (case-insensitive).
	Level string
	// Format is "console" or "json". Empty defaults to console.
	Format Format
	// EnableColor adds ANSI colours to the console encoder. Ignored
	// when Format is JSON. When Enabled() returns false (NO_COLOR)
	// colours are suppressed regardless.
	EnableColor bool
	// ServiceName, when non-empty, is attached to every record under
	// the "service" key. Helps with multi-service log backends.
	ServiceName string
	// Output overrides the sink (defaults to os.Stdout). Primarily
	// useful in tests.
	Output io.Writer
}

// DefaultConfig returns the config materialised from environment
// variables. Consult the package docs for the full set of knobs.
func DefaultConfig() Config {
	return Config{
		Level:       getEnv("LOG_LEVEL", "info"),
		Format:      Format(getEnv("LOG_FORMAT", "console")),
		EnableColor: shouldEnableColor(),
		ServiceName: getEnv("LOG_SERVICE_NAME", "trader"),
	}
}

// New returns a fresh zerolog.Logger wired up according to cfg.
//
// The returned logger always carries a timestamp field; when
// cfg.ServiceName is non-empty a "service" field is also attached.
func New(cfg Config) zerolog.Logger {
	zerolog.TimeFieldFormat = time.RFC3339Nano

	lvl, err := zerolog.ParseLevel(strings.ToLower(strings.TrimSpace(cfg.Level)))
	if err != nil || lvl == zerolog.NoLevel {
		lvl = zerolog.InfoLevel
	}

	out := cfg.Output
	if out == nil {
		out = os.Stdout
	}

	var sink io.Writer
	switch cfg.Format {
	case FormatJSON:
		sink = out
	default: // console or empty
		sink = newConsoleWriter(out, cfg.EnableColor)
	}

	b := zerolog.New(sink).Level(lvl).With().Timestamp()
	if cfg.ServiceName != "" {
		b = b.Str("service", cfg.ServiceName)
	}
	return b.Logger()
}

// Global logger management.
var (
	globalMu      sync.RWMutex
	globalLogger  zerolog.Logger
	globalInitted bool
)

// SetGlobal overrides the process-wide logger. Safe for concurrent use.
func SetGlobal(l zerolog.Logger) {
	globalMu.Lock()
	defer globalMu.Unlock()
	globalLogger = l
	globalInitted = true
}

// Get returns the process-wide logger. If none has been installed
// via SetGlobal the default config is materialised on first call.
func Get() zerolog.Logger {
	globalMu.RLock()
	if globalInitted {
		defer globalMu.RUnlock()
		return globalLogger
	}
	globalMu.RUnlock()

	globalMu.Lock()
	defer globalMu.Unlock()
	if !globalInitted {
		globalLogger = New(DefaultConfig())
		globalInitted = true
	}
	return globalLogger
}

// With returns a zerolog.Context bound to the global logger — the
// familiar pattern for adding fields to a child logger:
//
//	log := logger.With().Str("portfolio", id).Logger()
func With() zerolog.Context { return Get().With() }

// Event shortcuts. These return *zerolog.Event so callers can chain
// field setters followed by Msg / Msgf in the usual way:
//
//	logger.Info().Str("addr", ":8080").Msg("http listening")

// Debug returns a debug-level event bound to the global logger.
func Debug() *zerolog.Event { l := Get(); return l.Debug() }

// Info returns an info-level event bound to the global logger.
func Info() *zerolog.Event { l := Get(); return l.Info() }

// Warn returns a warn-level event bound to the global logger.
func Warn() *zerolog.Event { l := Get(); return l.Warn() }

// Error returns an error-level event bound to the global logger.
func Error() *zerolog.Event { l := Get(); return l.Error() }

// Fatal returns a fatal-level event bound to the global logger. Note
// that calling .Msg on a Fatal event exits the process (via os.Exit).
func Fatal() *zerolog.Event { l := Get(); return l.Fatal() }

// Sync flushes buffered log entries. zerolog writes synchronously so
// this is a no-op today, but it matches the model-router-api surface
// and gives us a place to hook flushing if we ever swap the sink.
func Sync() {}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func shouldEnableColor() bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	if v := os.Getenv("LOG_COLOR"); v != "" {
		return v == "true" || v == "1" || v == "yes" || v == "on"
	}
	return true
}
