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

	"github.com/benn-herrera/laterm/internal/logging"
	"github.com/benn-herrera/laterm/internal/pty"
	"github.com/benn-herrera/laterm/internal/render"
	"github.com/benn-herrera/laterm/internal/render/sixel"
	"github.com/benn-herrera/laterm/internal/render/unicode"
	"github.com/benn-herrera/laterm/internal/sanitize"
	"github.com/benn-herrera/laterm/internal/statemachine"
	"github.com/benn-herrera/laterm/internal/stream"
	"github.com/benn-herrera/laterm/internal/termcap"
)

const usage = `Usage: laterm <command> [args...]

Wraps a command and renders LaTeX math expressions as Sixel graphics
or Unicode text.

Environment variables:
  LATERM_LOG_LEVEL  Log level: debug, info, warn, error (default: info)
  LATERM_LOG_FILE   Log file path (default: none)
`

func main() {
	// 1. Parse args.
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}
	command := os.Args[1:]

	// 2. Initialize logging.
	logger, err := logging.Init()
	if err != nil {
		fmt.Fprintf(os.Stderr, "laterm: init logging: %v\n", err)
		os.Exit(1)
	}
	logger.Info("laterm starting", slog.String("command", command[0]))

	// 3. Probe terminal capabilities.
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

	// 4. Create renderers and select based on capabilities.
	unicodeRenderer := unicode.New()
	sixelRenderer := sixel.New(sixel.Config{
		MaxPixelWidth:  caps.WidthPixels,
		MaxPixelHeight: caps.HeightPixels,
		Logger:         logger,
	})
	renderer := render.Select(renderCaps, sixelRenderer, unicodeRenderer)

	// 5. Start PTY session.
	session, err := pty.Start(pty.Config{
		Command: command,
		Logger:  logger,
	})
	if err != nil {
		logger.Error("laterm: start pty", slog.String("error", err.Error()))
		fmt.Fprintf(os.Stderr, "laterm: %v\n", err)
		os.Exit(1)
	}
	defer session.Close()

	// 6. Panic recovery. Registered after session.Close so it runs after
	//    Close (LIFO order). RestoreTerminal is idempotent, so calling it
	//    here ensures the terminal is restored even if Close hasn't run yet.
	defer func() {
		if r := recover(); r != nil {
			session.RestoreTerminal()
			logger.Error("laterm: panic", slog.Any("panic", r))
			os.Exit(1)
		}
	}()

	// 7. Signal handling.
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
		sig := <-exitSigs
		session.RestoreTerminal()
		logger.Info("laterm: caught signal, exiting", slog.String("signal", sig.String()))
		// Re-raise the signal with default handler for correct exit status.
		signal.Reset(sig)
		syscall.Kill(syscall.Getpid(), sig.(syscall.Signal))
	}()

	// 8. Copy stdin to child PTY.
	go func() {
		_, _ = io.Copy(session.Writer(), os.Stdin)
	}()

	// 9. Create and run the stream loop.
	machine := statemachine.New(statemachine.Config{})
	san := sanitize.New(sanitize.Config{})
	loop := stream.NewLoop(stream.Config{
		Reader:    session.Reader(),
		Writer:    os.Stdout,
		Machine:   machine,
		Sanitizer: san,
		Renderer:  renderer,
		Logger:    logger,
		MaxWidth:  caps.WidthCells,
	})

	// Now wire up signal forwarding with the resize callback.
	stopSignals := session.ForwardSignals(func() {
		cols, _, _, _, sizeErr := termcap.GetSize(os.Stdin.Fd())
		if sizeErr != nil {
			logger.Warn("laterm: get size on resize", slog.String("error", sizeErr.Error()))
			return
		}
		loop.UpdateMaxWidth(cols)
	})
	defer stopSignals()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := loop.Run(ctx); err != nil {
		logger.Debug("laterm: stream loop ended", slog.String("error", err.Error()))
	}

	// 10. Wait for child and exit with its code.
	exitCode := session.Wait()
	os.Exit(exitCode)
}
