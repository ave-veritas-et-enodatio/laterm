// Package pty manages PTY lifecycle: creation, child process spawning,
// raw/cooked mode, signal forwarding, and terminal state restoration.
//
// It exposes the PTY as io.Reader/io.Writer so callers never depend on
// creack/pty types directly.
package pty

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"

	creackpty "github.com/creack/pty"
	"golang.org/x/term"

	"github.com/benn-herrera/laterm/internal/logging"
)

// Session represents an active PTY session with a child process.
type Session struct {
	ptmx     *os.File   // PTY master
	cmd      *exec.Cmd  // child process
	oldState *term.State // saved terminal state (nil if not in raw mode)
	isRaw    bool        // whether we entered raw mode

	restoreOnce sync.Once
	logger      *slog.Logger
}

// Config holds parameters for creating a Session.
type Config struct {
	Command []string     // command and args to run
	Logger  *slog.Logger // logger instance; if nil, uses logging.Default()
}

// Start creates a PTY, spawns the child process, and optionally enters raw mode.
//
// If stdin is a TTY, Start enters raw mode and saves the original terminal
// state for later restoration. If stdin is not a TTY (pipe), it stays in
// cooked mode.
//
// The caller must call Session.Close when done. Typical usage:
//
//	sess, err := pty.Start(cfg)
//	if err != nil { ... }
//	defer sess.Close()
//	exitCode := sess.Wait()
func Start(cfg Config) (*Session, error) {
	if len(cfg.Command) == 0 {
		return nil, errors.New("pty: command must not be empty")
	}

	logger := cfg.Logger
	if logger == nil {
		logger = logging.Default()
	}

	cmd := exec.Command(cfg.Command[0], cfg.Command[1:]...)

	ptmx, err := creackpty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("pty: start command %q: %w", cfg.Command[0], err)
	}

	s := &Session{
		ptmx:   ptmx,
		cmd:    cmd,
		logger: logger,
	}

	// Sync initial terminal size from stdin to the PTY, if stdin is a terminal.
	stdinFd := int(os.Stdin.Fd())
	if term.IsTerminal(stdinFd) {
		if err := creackpty.InheritSize(os.Stdin, ptmx); err != nil {
			logger.Warn("pty: initial size sync failed", slog.String("err", err.Error()))
		}

		oldState, err := term.MakeRaw(stdinFd)
		if err != nil {
			// Clean up on failure: close PTY. The child will get SIGHUP.
			ptmx.Close()
			return nil, fmt.Errorf("pty: enter raw mode: %w", err)
		}
		s.oldState = oldState
		s.isRaw = true
		logger.Debug("pty: entered raw mode")
	} else {
		logger.Debug("pty: stdin is not a terminal, staying in cooked mode")
	}

	return s, nil
}

// Reader returns an io.Reader for the child's PTY output.
func (s *Session) Reader() io.Reader {
	return s.ptmx
}

// Writer returns an io.Writer for writing to the child's PTY input.
func (s *Session) Writer() io.Writer {
	return s.ptmx
}

// RestoreTerminal restores the original terminal state if raw mode was entered.
// Safe to call multiple times; only the first call has effect.
func (s *Session) RestoreTerminal() {
	s.restoreOnce.Do(func() {
		if s.oldState == nil {
			return
		}
		if err := term.Restore(int(os.Stdin.Fd()), s.oldState); err != nil {
			s.logger.Warn("pty: restore terminal state failed", slog.String("err", err.Error()))
			return
		}
		s.logger.Debug("pty: terminal state restored")
	})
}

// Wait waits for the child process to exit and returns its exit code.
// Returns 0 on clean exit, the process exit code on ExitError, or 1 on
// other errors.
func (s *Session) Wait() int {
	err := s.cmd.Wait()
	if err == nil {
		return 0
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}

	s.logger.Warn("pty: wait returned non-exit error", slog.String("err", err.Error()))
	return 1
}

// Close cleans up the session: restores terminal state and closes the PTY master.
// Does NOT kill the child process — call Wait first.
func (s *Session) Close() {
	s.RestoreTerminal()
	if err := s.ptmx.Close(); err != nil {
		s.logger.Debug("pty: close ptmx", slog.String("err", err.Error()))
	}
}

// ForwardSignals starts goroutines that forward signals to the child process.
//
//   - SIGINT, SIGTERM: forwarded to the child via cmd.Process.Signal.
//   - SIGWINCH: resizes the PTY via InheritSize(os.Stdin, ptmx), then calls
//     onResize if non-nil.
//
// Returns a cleanup function that stops signal forwarding. The caller must
// call it when signal forwarding is no longer needed.
func (s *Session) ForwardSignals(onResize func()) func() {
	// Channel for SIGINT, SIGTERM.
	termSigs := make(chan os.Signal, 1)
	signal.Notify(termSigs, syscall.SIGINT, syscall.SIGTERM)

	// Channel for SIGWINCH.
	winchSig := make(chan os.Signal, 1)
	signal.Notify(winchSig, syscall.SIGWINCH)

	var wg sync.WaitGroup

	// Forward SIGINT/SIGTERM to the child.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for sig := range termSigs {
			if s.cmd.Process == nil {
				continue
			}
			if err := s.cmd.Process.Signal(sig); err != nil {
				s.logger.Debug("pty: forward signal failed",
					slog.String("signal", sig.String()),
					slog.String("err", err.Error()),
				)
			}
		}
	}()

	// Handle SIGWINCH: resize PTY, then call onResize callback.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range winchSig {
			if err := creackpty.InheritSize(os.Stdin, s.ptmx); err != nil {
				s.logger.Warn("pty: resize failed", slog.String("err", err.Error()))
				continue
			}
			s.logger.Debug("pty: resized PTY")
			if onResize != nil {
				onResize()
			}
		}
	}()

	return func() {
		signal.Stop(termSigs)
		signal.Stop(winchSig)
		close(termSigs)
		close(winchSig)
		wg.Wait()
	}
}
