// Package termcap queries terminal capabilities: Sixel support (via DA1)
// and terminal dimensions in cells and pixels.
//
// It does NOT make rendering decisions — it only reports what the terminal
// supports. The caller uses the result to select a renderer.
package termcap

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"time"

	"github.com/benn-herrera/laterm/internal/logging"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// Capabilities describes what the terminal supports.
type Capabilities struct {
	SixelSupported bool
	WidthCells     int
	HeightCells    int
	WidthPixels    int
	HeightPixels   int
}

// da1Query is the Device Attributes (DA1) escape sequence.
var da1Query = []byte("\x1b[c")

// da1Timeout is how long to wait for a DA1 response before giving up.
const da1Timeout = 500 * time.Millisecond

// defaultCellWidth and defaultCellHeight are used to estimate pixel dimensions
// when the terminal reports zero for pixel size.
const (
	defaultCellWidth  = 8
	defaultCellHeight = 16
)

// Probe queries the terminal for its capabilities.
//
// It sends a DA1 query (\x1b[c) and parses the response for Sixel support
// (attribute 4 in the parameter list). It also queries terminal size in cells
// and pixels.
//
// If ttyFd is not a terminal, returns default capabilities (no Sixel, zero
// dimensions). The ttyFd parameter should be the file descriptor of the
// terminal (typically os.Stdin.Fd()). ttyWriter is where to send the DA1
// query (typically os.Stdout). ttyReader is where to read the DA1 response
// (typically os.Stdin).
func Probe(ttyFd uintptr, ttyReader io.Reader, ttyWriter io.Writer) (Capabilities, error) {
	log := logging.Default()

	if !term.IsTerminal(int(ttyFd)) {
		log.Debug("termcap: fd is not a terminal, returning defaults")
		return Capabilities{}, nil
	}

	cols, rows, pxW, pxH, err := GetSize(ttyFd)
	if err != nil {
		return Capabilities{}, fmt.Errorf("termcap: get terminal size: %w", err)
	}

	caps := Capabilities{
		WidthCells:  cols,
		HeightCells: rows,
		WidthPixels: pxW,
		HeightPixels: pxH,
	}

	// Estimate pixel dimensions from cells if the terminal doesn't report them.
	if caps.WidthPixels == 0 && caps.WidthCells > 0 {
		caps.WidthPixels = caps.WidthCells * defaultCellWidth
		log.Debug("termcap: estimated pixel width from cells",
			slog.Int("widthPixels", caps.WidthPixels))
	}
	if caps.HeightPixels == 0 && caps.HeightCells > 0 {
		caps.HeightPixels = caps.HeightCells * defaultCellHeight
		log.Debug("termcap: estimated pixel height from cells",
			slog.Int("heightPixels", caps.HeightPixels))
	}

	sixel, err := querySixel(ttyFd, ttyReader, ttyWriter, log)
	if err != nil {
		// Non-fatal: log and continue without Sixel.
		log.Warn("termcap: DA1 query failed, assuming no Sixel",
			slog.String("error", err.Error()))
	}
	caps.SixelSupported = sixel

	log.Info("termcap: probed capabilities",
		slog.Bool("sixel", caps.SixelSupported),
		slog.Int("cols", caps.WidthCells),
		slog.Int("rows", caps.HeightCells),
		slog.Int("pxWidth", caps.WidthPixels),
		slog.Int("pxHeight", caps.HeightPixels),
	)

	return caps, nil
}

// GetSize returns the current terminal size in cells and pixels.
// Uses TIOCGWINSZ ioctl via golang.org/x/sys/unix.
func GetSize(fd uintptr) (cols, rows, pixelWidth, pixelHeight int, err error) {
	ws, err := unix.IoctlGetWinsize(int(fd), unix.TIOCGWINSZ)
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("termcap: TIOCGWINSZ: %w", err)
	}
	return int(ws.Col), int(ws.Row), int(ws.Xpixel), int(ws.Ypixel), nil
}

// querySixel sends a DA1 query and parses the response for Sixel support.
// It puts the terminal into raw mode to read the response, then restores
// the original state.
func querySixel(ttyFd uintptr, ttyReader io.Reader, ttyWriter io.Writer, log *slog.Logger) (bool, error) {
	fd := int(ttyFd)

	// Save terminal state and enter raw mode so we can read the response.
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return false, fmt.Errorf("enter raw mode: %w", err)
	}
	defer func() {
		if restoreErr := term.Restore(fd, oldState); restoreErr != nil {
			log.Warn("termcap: failed to restore terminal state",
				slog.String("error", restoreErr.Error()))
		}
	}()

	// Send the DA1 query.
	if _, err := ttyWriter.Write(da1Query); err != nil {
		return false, fmt.Errorf("write DA1 query: %w", err)
	}

	// Read the response with a timeout.
	resp, err := readDA1Response(ttyReader, da1Timeout)
	if err != nil {
		return false, fmt.Errorf("read DA1 response: %w", err)
	}

	log.Debug("termcap: DA1 response received", slog.Int("len", len(resp)))

	return parseDA1(resp), nil
}

// readDA1Response reads bytes from r until it sees a complete DA1 response
// (terminated by 'c') or the timeout expires.
//
// A DA1 response looks like: \x1b[?<params>c
// where <params> is a semicolon-separated list of numbers.
func readDA1Response(r io.Reader, timeout time.Duration) ([]byte, error) {
	type result struct {
		data []byte
		err  error
	}

	ch := make(chan result, 1)

	// NOTE: on timeout, this goroutine will be blocked in Read and will leak
	// until the reader is closed or returns. This is acceptable for a one-shot
	// probe at startup.
	go func() {
		var buf bytes.Buffer
		b := make([]byte, 1)
		inResponse := false

		for {
			n, err := r.Read(b)
			if err != nil {
				ch <- result{buf.Bytes(), fmt.Errorf("read: %w", err)}
				return
			}
			if n == 0 {
				continue
			}

			c := b[0]

			// Look for ESC to start capturing.
			if c == 0x1b {
				inResponse = true
				buf.Reset()
				buf.WriteByte(c)
				continue
			}

			if inResponse {
				buf.WriteByte(c)
				// DA1 response ends with 'c'.
				if c == 'c' {
					ch <- result{buf.Bytes(), nil}
					return
				}
				// Safety: if the response is unreasonably long, bail out.
				if buf.Len() > 256 {
					ch <- result{nil, fmt.Errorf("response too long (%d bytes)", buf.Len())}
					return
				}
			}
			// Discard bytes before the first ESC (could be echoed input, etc.)
		}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case res := <-ch:
		return res.data, res.err
	case <-timer.C:
		return nil, fmt.Errorf("timeout after %v", timeout)
	}
}

// parseDA1 examines a DA1 response for Sixel support.
// The response format is: \x1b[?<p1>;<p2>;...;<pN>c
// Sixel is indicated by attribute 4 in the parameter list.
func parseDA1(resp []byte) bool {
	// Minimum valid response: ESC [ ? <digit> c = 5 bytes
	if len(resp) < 4 {
		return false
	}

	// Strip the ESC prefix if present. We stored starting from ESC.
	// Expected: \x1b [ ? <params> c
	// In buf we have: [? <params> c  (ESC was the trigger but is first byte)
	// Actually we stored ESC too, so resp = ESC [ ? <params> c

	// Find the start of the parameter area.
	// Look for "?", then parse until "c".
	qIdx := bytes.IndexByte(resp, '?')
	if qIdx < 0 {
		return false
	}

	// End marker is the final 'c'.
	cIdx := bytes.LastIndexByte(resp, 'c')
	if cIdx < 0 || cIdx <= qIdx {
		return false
	}

	// Extract the parameter string between '?' and 'c'.
	params := resp[qIdx+1 : cIdx]

	// Split by ';' and check for '4'.
	for _, part := range bytes.Split(params, []byte{';'}) {
		part = bytes.TrimSpace(part)
		if len(part) == 0 {
			continue
		}
		n, err := strconv.Atoi(string(part))
		if err != nil {
			continue
		}
		if n == 4 {
			return true
		}
	}

	return false
}
