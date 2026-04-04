package logging

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := []struct {
		input string
		want  slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},

		// Case-insensitive.
		{"DEBUG", slog.LevelDebug},
		{"Info", slog.LevelInfo},
		{"WARN", slog.LevelWarn},
		{"Error", slog.LevelError},

		// Leading/trailing whitespace.
		{"  debug  ", slog.LevelDebug},
		{"\twarn\n", slog.LevelWarn},

		// Unrecognized or empty -> LevelInfo (the default).
		{"", slog.LevelInfo},
		{"bogus", slog.LevelInfo},
		{"trace", slog.LevelInfo},
		{"fatal", slog.LevelInfo},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("%q", tc.input), func(t *testing.T) {
			got := ParseLevel(tc.input)
			if got != tc.want {
				t.Errorf("ParseLevel(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestInitDefaults(t *testing.T) {
	// With no options and no env vars, Init returns a working (discard) logger.
	t.Setenv("LATERM_LOG_LEVEL", "")
	t.Setenv("LATERM_LOG_FILE", "")

	logger, err := Init()
	if err != nil {
		t.Fatalf("Init() error: %v", err)
	}
	if logger == nil {
		t.Fatal("Init() returned nil logger")
	}

	// Should not panic — the logger works even though it discards.
	logger.Info("test message", "key", "value")
}

func TestDefaultBeforeAndAfterInit(t *testing.T) {
	// Reset to a known state: store a discard logger like package init does.
	defaultLogger.Store(slog.New(discardHandler{}))

	pre := Default()
	if pre == nil {
		t.Fatal("Default() returned nil before Init")
	}
	// The discard handler should report Enabled=false for all levels.
	if pre.Enabled(nil, slog.LevelError) {
		t.Error("Default() before Init should use discardHandler (Enabled=false)")
	}

	// Now Init with a file so we get a real handler.
	logFile := filepath.Join(t.TempDir(), "test.log")
	t.Setenv("LATERM_LOG_LEVEL", "")
	t.Setenv("LATERM_LOG_FILE", "")

	logger, err := Init(WithFile(logFile))
	if err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	post := Default()
	if post == nil {
		t.Fatal("Default() returned nil after Init")
	}
	if post != logger {
		t.Error("Default() after Init should return the same logger Init returned")
	}
	// A real handler should report Enabled=true for at least Info.
	if !post.Enabled(nil, slog.LevelInfo) {
		t.Error("Default() after Init should be enabled for Info level")
	}
}

func TestWithLevel(t *testing.T) {
	cases := []struct {
		name    string
		level   slog.Level
		checkAt slog.Level
		enabled bool
	}{
		{"debug_allows_debug", slog.LevelDebug, slog.LevelDebug, true},
		{"info_blocks_debug", slog.LevelInfo, slog.LevelDebug, false},
		{"warn_blocks_info", slog.LevelWarn, slog.LevelInfo, false},
		{"error_blocks_warn", slog.LevelError, slog.LevelWarn, false},
		{"error_allows_error", slog.LevelError, slog.LevelError, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logFile := filepath.Join(t.TempDir(), "test.log")
			t.Setenv("LATERM_LOG_LEVEL", "")
			t.Setenv("LATERM_LOG_FILE", "")

			logger, err := Init(WithLevel(tc.level), WithFile(logFile))
			if err != nil {
				t.Fatalf("Init() error: %v", err)
			}

			got := logger.Enabled(nil, tc.checkAt)
			if got != tc.enabled {
				t.Errorf("logger.Enabled(_, %v) = %v, want %v (logger level: %v)",
					tc.checkAt, got, tc.enabled, tc.level)
			}
		})
	}
}

func TestWithFile(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "test.log")
	t.Setenv("LATERM_LOG_LEVEL", "")
	t.Setenv("LATERM_LOG_FILE", "")

	logger, err := Init(WithFile(logFile), WithLevel(slog.LevelInfo))
	if err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	logger.Info("hello from test", "k", "v")

	// The closingHandler wraps the file. Read back what was written.
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("reading log file: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("log file is empty after writing a message")
	}

	// Output is JSON (slog.NewJSONHandler). Verify it parses.
	var entry map[string]any
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("log file content is not valid JSON: %v\ncontent: %s", err, data)
	}
	if msg, ok := entry["msg"].(string); !ok || msg != "hello from test" {
		t.Errorf("log entry msg = %q, want %q", entry["msg"], "hello from test")
	}
}

func TestWithFileInvalidPath(t *testing.T) {
	t.Setenv("LATERM_LOG_LEVEL", "")
	t.Setenv("LATERM_LOG_FILE", "")

	_, err := Init(WithFile("/no/such/directory/test.log"))
	if err == nil {
		t.Fatal("Init() with invalid file path should return an error")
	}
	if !strings.Contains(err.Error(), "open log file") {
		t.Errorf("error should mention 'open log file', got: %v", err)
	}
}

func TestFileMode0600(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "restricted.log")
	t.Setenv("LATERM_LOG_LEVEL", "")
	t.Setenv("LATERM_LOG_FILE", "")

	logger, err := Init(WithFile(logFile), WithLevel(slog.LevelInfo))
	if err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	// Write something so the file definitely exists.
	logger.Info("permission test")

	info, err := os.Stat(logFile)
	if err != nil {
		t.Fatalf("stat log file: %v", err)
	}

	mode := info.Mode().Perm()
	if mode != 0600 {
		t.Errorf("log file mode = %04o, want 0600", mode)
	}
}

func TestWithStderr(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "test.log")
	t.Setenv("LATERM_LOG_LEVEL", "")
	t.Setenv("LATERM_LOG_FILE", "")

	// Capture stderr by replacing os.Stderr temporarily.
	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating pipe: %v", err)
	}
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = origStderr })

	logger, initErr := Init(WithFile(logFile), WithStderr(), WithLevel(slog.LevelInfo))
	if initErr != nil {
		w.Close()
		os.Stderr = origStderr
		t.Fatalf("Init() error: %v", initErr)
	}

	logger.Info("tee test message")

	w.Close()
	var buf bytes.Buffer
	io.Copy(&buf, r)
	r.Close()

	stderrOutput := buf.String()
	if !strings.Contains(stderrOutput, "tee test message") {
		t.Errorf("expected stderr to contain log message, got: %q", stderrOutput)
	}

	// Also verify the file got the message.
	fileData, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("reading log file: %v", err)
	}
	if !strings.Contains(string(fileData), "tee test message") {
		t.Errorf("expected log file to contain message, got: %q", string(fileData))
	}
}

func TestEnvVarLevel(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "env.log")
	t.Setenv("LATERM_LOG_LEVEL", "error")
	t.Setenv("LATERM_LOG_FILE", "")

	// No WithLevel option — should pick up LATERM_LOG_LEVEL=error.
	logger, err := Init(WithFile(logFile))
	if err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	if logger.Enabled(nil, slog.LevelWarn) {
		t.Error("logger should NOT be enabled for Warn when env level is error")
	}
	if !logger.Enabled(nil, slog.LevelError) {
		t.Error("logger should be enabled for Error when env level is error")
	}
}

func TestEnvVarFile(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "envfile.log")
	t.Setenv("LATERM_LOG_LEVEL", "info")
	t.Setenv("LATERM_LOG_FILE", logFile)

	// No WithFile option — should pick up LATERM_LOG_FILE.
	logger, err := Init()
	if err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	logger.Info("env file test")

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("reading env log file: %v", err)
	}
	if !strings.Contains(string(data), "env file test") {
		t.Errorf("expected env log file to contain message, got: %q", string(data))
	}
}

func TestOptionOverridesEnvVar(t *testing.T) {
	t.Setenv("LATERM_LOG_LEVEL", "debug")
	t.Setenv("LATERM_LOG_FILE", "")

	logFile := filepath.Join(t.TempDir(), "override.log")

	// WithLevel(Error) should override env LATERM_LOG_LEVEL=debug.
	logger, err := Init(WithFile(logFile), WithLevel(slog.LevelError))
	if err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	if logger.Enabled(nil, slog.LevelDebug) {
		t.Error("option WithLevel(Error) should override env debug level")
	}
	if !logger.Enabled(nil, slog.LevelError) {
		t.Error("logger should be enabled for Error")
	}
}

func TestNeverWritesToStdout(t *testing.T) {
	// Replace stdout with a pipe and verify nothing is written to it.
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating pipe: %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = origStdout })

	logFile := filepath.Join(t.TempDir(), "stdout-test.log")
	t.Setenv("LATERM_LOG_LEVEL", "")
	t.Setenv("LATERM_LOG_FILE", "")

	logger, initErr := Init(WithFile(logFile), WithStderr(), WithLevel(slog.LevelDebug))
	if initErr != nil {
		w.Close()
		os.Stdout = origStdout
		t.Fatalf("Init() error: %v", initErr)
	}

	// Write at every level.
	logger.Debug("debug msg")
	logger.Info("info msg")
	logger.Warn("warn msg")
	logger.Error("error msg")

	w.Close()
	var buf bytes.Buffer
	io.Copy(&buf, r)
	r.Close()

	if buf.Len() > 0 {
		t.Errorf("logging wrote to stdout (critical violation): %q", buf.String())
	}
}
