package pty

import (
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"
)

func TestStart_EmptyCommand(t *testing.T) {
	t.Parallel()
	_, err := Start(Config{})
	if err == nil {
		t.Fatal("Start with empty command should return error")
	}
	if !strings.Contains(err.Error(), "command must not be empty") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStart_InvalidCommand(t *testing.T) {
	t.Parallel()
	_, err := Start(Config{Command: []string{"/nonexistent/binary/xxxxx"}})
	if err == nil {
		t.Fatal("Start with nonexistent command should return error")
	}
}

func TestSession_EchoRoundtrip(t *testing.T) {
	t.Parallel()

	sess, err := Start(Config{
		Command: []string{"echo", "hello from pty"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer sess.Close()

	// Read output from the child. The PTY may add \r\n line endings.
	buf := make([]byte, 256)
	var output []byte
	for {
		n, readErr := sess.Reader().Read(buf)
		if n > 0 {
			output = append(output, buf[:n]...)
		}
		if readErr != nil {
			break
		}
	}

	exitCode := sess.Wait()
	if exitCode != 0 {
		t.Errorf("exit code = %d, want 0", exitCode)
	}

	got := string(output)
	if !strings.Contains(got, "hello from pty") {
		t.Errorf("output = %q, want substring %q", got, "hello from pty")
	}
}

func TestSession_ExitCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		command  []string
		wantCode int
	}{
		{name: "exit 0", command: []string{"sh", "-c", "exit 0"}, wantCode: 0},
		{name: "exit 1", command: []string{"sh", "-c", "exit 1"}, wantCode: 1},
		{name: "exit 42", command: []string{"sh", "-c", "exit 42"}, wantCode: 42},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sess, err := Start(Config{Command: tt.command})
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			defer sess.Close()

			// Drain output so the child doesn't block.
			go func() { _, _ = io.Copy(io.Discard, sess.Reader()) }()

			code := sess.Wait()
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}
		})
	}
}

func TestSession_ReaderWriter(t *testing.T) {
	t.Parallel()

	sess, err := Start(Config{
		Command: []string{"cat"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer sess.Close()

	// Write to the child's stdin via the PTY writer.
	const msg = "ping\n"
	if _, err := io.WriteString(sess.Writer(), msg); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Read back from the child's stdout via the PTY reader.
	// cat will echo back what it receives. The PTY may also echo the input,
	// so we just check that "ping" appears somewhere in the output.
	buf := make([]byte, 256)
	var output []byte
	for i := 0; i < 10; i++ {
		n, readErr := sess.Reader().Read(buf)
		if n > 0 {
			output = append(output, buf[:n]...)
		}
		if strings.Contains(string(output), "ping") {
			break
		}
		if readErr != nil {
			break
		}
	}

	if !strings.Contains(string(output), "ping") {
		t.Errorf("output = %q, want substring %q", string(output), "ping")
	}

	// Send EOF to cat so it exits.
	sess.Writer().Write([]byte{4}) // Ctrl-D
}

func TestSession_RestoreTerminalIdempotent(t *testing.T) {
	t.Parallel()

	sess, err := Start(Config{
		Command: []string{"true"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer sess.Close()

	go func() { _, _ = io.Copy(io.Discard, sess.Reader()) }()
	sess.Wait()

	// RestoreTerminal should be safe to call multiple times.
	sess.RestoreTerminal()
	sess.RestoreTerminal()
	sess.RestoreTerminal()
}

func TestSession_CloseIdempotent(t *testing.T) {
	t.Parallel()

	sess, err := Start(Config{
		Command: []string{"true"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	go func() { _, _ = io.Copy(io.Discard, sess.Reader()) }()
	sess.Wait()

	// Close should be safe to call; second close may log but should not panic.
	sess.Close()
	sess.Close()
}

func TestSession_ForwardSignals(t *testing.T) {
	t.Parallel()

	sess, err := Start(Config{
		Command: []string{"sleep", "60"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer sess.Close()

	cleanup := sess.ForwardSignals(nil)

	// Stop forwarding, then close the session so the child gets SIGHUP
	// and exits promptly.
	cleanup()

	go func() { _, _ = io.Copy(io.Discard, sess.Reader()) }()

	// Close the PTY master so sleep receives SIGHUP and exits.
	sess.ptmx.Close()
	_ = sess.Wait()
}

func TestWait_NonExitError(t *testing.T) {
	t.Parallel()

	// Construct a Session with a cmd that has already been waited on, to force
	// a non-ExitError from Wait.
	sess, err := Start(Config{
		Command: []string{"true"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer sess.Close()

	go func() { _, _ = io.Copy(io.Discard, sess.Reader()) }()

	// First Wait consumes the process.
	code := sess.Wait()
	if code != 0 {
		t.Errorf("first Wait: exit code = %d, want 0", code)
	}

	// Second Wait should return 1 (non-exit error: "Wait was already called").
	code = sess.Wait()
	if code != 1 {
		t.Errorf("second Wait: exit code = %d, want 1", code)
	}
}

func TestWait_ExitErrorExtraction(t *testing.T) {
	t.Parallel()

	// Verify our ExitError extraction logic directly.
	cmd := exec.Command("sh", "-c", "exit 7")
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatal("expected ExitError from sh -c 'exit 7'")
		}
		if exitErr.ExitCode() != 7 {
			t.Errorf("exit code = %d, want 7", exitErr.ExitCode())
		}
	} else {
		t.Fatal("sh -c 'exit 7' should have returned an error")
	}
}
