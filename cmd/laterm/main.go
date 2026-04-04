// Command laterm wraps a child process and renders LaTeX math expressions from
// its output as Sixel graphics or Unicode text.
//
// Usage:
//
//	laterm <command> [args...]
//
// SIGKILL and OOM are unrecoverable: the terminal may be left in raw mode.
// Run "reset" to recover.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"

	"github.com/ave-veritas-et-enodatio/laterm/internal/logging"
	"github.com/ave-veritas-et-enodatio/laterm/internal/pty"
	"github.com/ave-veritas-et-enodatio/laterm/internal/render"
	"github.com/ave-veritas-et-enodatio/laterm/internal/render/sixel"
	"github.com/ave-veritas-et-enodatio/laterm/internal/render/unicode"
	"github.com/ave-veritas-et-enodatio/laterm/internal/sanitize"
	"github.com/ave-veritas-et-enodatio/laterm/internal/statemachine"
	"github.com/ave-veritas-et-enodatio/laterm/internal/stream"
	"github.com/ave-veritas-et-enodatio/laterm/internal/termcap"
)

const usage = `Usage: laterm <command> [args...]

Wraps a command and renders LaTeX math expressions as Sixel graphics
or Unicode text.

Environment variables:
  LATERM_LOG_LEVEL  Log level: debug, info, warn, error (default: info)
  LATERM_LOG_FILE   Log file path (default: none)
`

func main() {
	os.Exit(run())
}

func run() (exitCode int) {
	// 1. Parse args.
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		return 1
	}
	command := os.Args[1:]

	// 2. Initialize logging.
	logger, cleanup, err := logging.Init()
	if err != nil {
		fmt.Fprintf(os.Stderr, "laterm: init logging: %v\n", err)
		return 1
	}
	defer cleanup()
	logger.Info("laterm starting", slog.String("command", command[0]))

	// 3. Save original terminal state before anything touches the terminal.
	//    This is the ground-truth state for restoration, independent of what
	//    termcap.Probe or pty.Start do with raw mode.
	stdinFd := int(os.Stdin.Fd())
	if term.IsTerminal(stdinFd) {
		origState, err := term.GetState(stdinFd)
		if err == nil {
			defer func() {
				if restoreErr := term.Restore(stdinFd, origState); restoreErr != nil {
					logger.Warn("laterm: final terminal restore failed",
						slog.String("error", restoreErr.Error()))
				}
			}()
		}
	}

	// 4. Probe terminal capabilities.
	caps, err := termcap.Probe(os.Stdin.Fd(), os.Stdin, os.Stdout)
	if err != nil {
		logger.Warn("laterm: termcap probe failed, continuing with defaults",
			slog.String("error", err.Error()))
	}

	renderCaps := render.Capabilities{
		SixelSupported: caps.SixelSupported,
		WidthCells:     caps.WidthCells,
		HeightCells:    caps.HeightCells,
		WidthPixels:    caps.WidthPixels,
		HeightPixels:   caps.HeightPixels,
	}

	// 5. Create renderers and select based on capabilities.
	unicodeRenderer := unicode.New()
	sixelRenderer := sixel.New(sixel.Config{
		MaxPixelWidth:  caps.WidthPixels,
		MaxPixelHeight: caps.HeightPixels,
		Logger:         logger,
	})
	renderer := render.Select(renderCaps, sixelRenderer, unicodeRenderer, logger)

	// 6. Start PTY session.
	session, err := pty.Start(pty.Config{
		Command: command,
		Logger:  logger,
	})
	if err != nil {
		logger.Error("laterm: start pty", slog.String("error", err.Error()))
		fmt.Fprintf(os.Stderr, "laterm: %v\n", err)
		return 1
	}
	defer session.Close()

	// 7. Panic recovery. Registered after session.Close so it runs before
	//    Close (LIFO order). RestoreTerminal is idempotent, so calling it
	//    here ensures the terminal is restored even if Close hasn't run yet.
	//    Uses named return to set exit code from recovered panic.
	defer func() {
		if r := recover(); r != nil {
			session.RestoreTerminal()
			logger.Error("laterm: panic", slog.Any("panic", r))
			exitCode = 1
		}
	}()

	// 8. Signal handling.
	//    ForwardSignals handles SIGINT/SIGTERM forwarding to child and
	//    SIGWINCH resize. We capture the onResize callback below once the
	//    stream loop exists.
	//
	//    Separate handler for SIGHUP/SIGQUIT: restore terminal and exit.
	//    These signals mean the controlling terminal is gone (SIGHUP) or
	//    the user wants a core dump (SIGQUIT); in both cases we restore
	//    terminal state and exit promptly.
	exitSigs := make(chan os.Signal, 1)
	signal.Notify(exitSigs, syscall.SIGHUP, syscall.SIGQUIT)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				session.RestoreTerminal()
				logger.Error("panic in goroutine", slog.Any("error", r))
			}
		}()
		sig := <-exitSigs
		session.RestoreTerminal()
		logger.Info("laterm: caught signal, exiting", slog.String("signal", sig.String()))
		// Re-raise the signal with default handler for correct exit status.
		signal.Reset(sig)
		syscall.Kill(syscall.Getpid(), sig.(syscall.Signal))
	}()

	// 9. Copy stdin to child PTY.
	go func() {
		defer func() {
			if r := recover(); r != nil {
				session.RestoreTerminal()
				logger.Error("panic in goroutine", slog.Any("error", r))
			}
		}()
		_, _ = io.Copy(session.Writer(), os.Stdin)
	}()

	// 10. Create and run the stream loop.
	machine := statemachine.New(statemachine.Config{})
	san := sanitize.New(sanitize.Config{})
	loop, err := stream.NewLoop(stream.Config{
		Reader:    session.Reader(),
		Writer:    os.Stdout,
		Machine:   machine,
		Sanitizer: san,
		Renderer:  renderer,
		Logger:    logger,
		MaxWidth:  caps.WidthPixels,
	})
	if err != nil {
		session.RestoreTerminal()
		logger.Error("laterm: create stream loop", slog.String("error", err.Error()))
		return 1
	}

	// Now wire up signal forwarding with the resize callback.
	stopSignals := session.ForwardSignals(func() {
		_, _, pxWidth, _, sizeErr := termcap.GetSize(os.Stdin.Fd())
		if sizeErr != nil {
			logger.Warn("laterm: get size on resize", slog.String("error", sizeErr.Error()))
			return
		}
		loop.UpdateMaxWidth(pxWidth)
	})
	defer stopSignals()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := loop.Run(ctx); err != nil {
		logger.Debug("laterm: stream loop ended", slog.String("error", err.Error()))
	}

	// 11. Wait for child and exit with its code.
	return session.Wait()
}
