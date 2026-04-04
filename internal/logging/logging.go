// Package logging configures structured, leveled logging for laterm.
//
// It wraps log/slog with file and stderr output, configured via functional
// options or environment variables (LATERM_LOG_LEVEL, LATERM_LOG_FILE).
// Logging NEVER writes to stdout — stdout is the terminal data stream.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
)

// defaultLogger holds the most recently initialized logger.
// Starts as a no-op logger (discard handler).
var defaultLogger atomic.Pointer[slog.Logger]

func init() {
	defaultLogger.Store(slog.New(discardHandler{}))
}

// Default returns the most recently initialized logger.
// If Init has not been called, returns a no-op logger.
func Default() *slog.Logger {
	return defaultLogger.Load()
}

// config holds the resolved logging configuration.
type config struct {
	level    slog.Level
	filePath string
	stderr   bool
}

// Option configures the logger returned by Init.
type Option func(*config)

// WithLevel sets the log level.
func WithLevel(level slog.Level) Option {
	return func(c *config) {
		c.level = level
	}
}

// WithFile enables file logging at the given path (mode 0600).
func WithFile(path string) Option {
	return func(c *config) {
		c.filePath = path
	}
}

// WithStderr enables teeing log output to stderr.
func WithStderr() Option {
	return func(c *config) {
		c.stderr = true
	}
}

// Init creates and returns a configured slog.Logger and a cleanup function.
//
// The cleanup function closes any log file handles opened during Init.
// Callers must invoke cleanup when the logger is no longer needed (typically
// via defer). When no file is opened, cleanup is a no-op.
//
// With no options, Init reads defaults from environment variables:
//   - LATERM_LOG_LEVEL: "debug", "info", "warn", or "error" (case-insensitive). Default: "info".
//   - LATERM_LOG_FILE: file path for log output (mode 0600).
//
// Options override environment variable defaults.
// The returned logger is also set as the package default (accessible via Default).
func Init(opts ...Option) (*slog.Logger, func(), error) {
	noop := func() {}

	cfg := config{
		level:    envLevel(),
		filePath: os.Getenv("LATERM_LOG_FILE"),
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	writers, cleanup, err := buildWriters(cfg)
	if err != nil {
		return nil, noop, err
	}

	if len(writers) == 0 {
		logger := slog.New(discardHandler{})
		defaultLogger.Store(logger)
		return logger, cleanup, nil
	}

	var w io.Writer
	if len(writers) == 1 {
		w = writers[0]
	} else {
		w = io.MultiWriter(writers...)
	}

	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: cfg.level,
	})

	logger := slog.New(&closingHandler{
		Handler: handler,
		cleanup: cleanup,
	})
	defaultLogger.Store(logger)
	return logger, cleanup, nil
}

// envLevel parses LATERM_LOG_LEVEL into an slog.Level.
// Returns slog.LevelInfo if unset or unrecognized.
func envLevel() slog.Level {
	return ParseLevel(os.Getenv("LATERM_LOG_LEVEL"))
}

// ParseLevel converts a level string to an slog.Level.
// Recognized values (case-insensitive): "debug", "info", "warn", "error".
// Returns slog.LevelInfo for unrecognized or empty input.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// buildWriters opens the configured output destinations.
// Returns the writers and a cleanup function that closes any opened files.
func buildWriters(cfg config) (writers []io.Writer, cleanup func(), err error) {
	var closers []io.Closer

	cleanup = func() {
		for _, c := range closers {
			c.Close()
		}
	}

	if cfg.filePath != "" {
		f, ferr := os.OpenFile(cfg.filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if ferr != nil {
			cleanup()
			return nil, nil, fmt.Errorf("logging: open log file %q: %w", cfg.filePath, ferr)
		}
		closers = append(closers, f)
		writers = append(writers, f)
	}

	if cfg.stderr {
		writers = append(writers, os.Stderr)
	}

	return writers, cleanup, nil
}

// closingHandler wraps an slog.Handler and holds a cleanup function
// for closing file handles when the logger is no longer needed.
type closingHandler struct {
	slog.Handler
	cleanup   func()
	closeOnce sync.Once
}

// Close releases resources held by this handler (e.g., open log files).
func (h *closingHandler) Close() error {
	h.closeOnce.Do(func() {
		if h.cleanup != nil {
			h.cleanup()
		}
	})
	return nil
}

// discardHandler is a no-op slog.Handler that discards all records.
type discardHandler struct{}

func (discardHandler) Enabled(_ context.Context, _ slog.Level) bool  { return false }
func (discardHandler) Handle(_ context.Context, _ slog.Record) error  { return nil }
func (d discardHandler) WithAttrs(_ []slog.Attr) slog.Handler        { return d }
func (d discardHandler) WithGroup(_ string) slog.Handler              { return d }
