# LaTex Math Terminal Wrapper

This specification defines a high-performance, zero-delivery-dependency (single binary for user, build-time dependency is acceptable if fully justifiable) terminal wrapper written in **Go**. Its purpose is to intercept LaTeX mathematical expressions from a child process (like Claude Code) and render them as either **Sixel graphics** or **Unicode text** based on terminal capabilities.

---

## 1. Core Architecture: The PTY Proxy
The application must act as a **Pseudo-Terminal (PTY)** master. It spawns the target shell command (e.g., `claude`) and pipes all I/O between the user's physical terminal and the child process.

* **Primary Package:** `github.com/creack/pty`
* **Execution Flow:**
    1.  The wrapper starts and performs a **Capability Check** (Sixel support).
    2.  It spawns the child process and attaches it to a PTY.
    3.  It enters a **Streaming Loop** that reads bytes from the child's `stdout`.
    4.  It maintains a **State Machine** to identify math delimiters ($ and $$).

---

## 2. The State Machine (Delimiter Logic)
To maintain the "live stream" feel of a LLM, the wrapper must process text byte-by-byte.

| State | Action | Transition |
| :--- | :--- | :--- |
| **TEXT** | Pass bytes directly to `os.Stdout`. | On `$`: Transition to **POTENTIAL_MATH**. |
| **POTENTIAL_MATH** | Buffer the `$`. Check next byte. | If `$`: **BLOCK_MATH**. If char: **INLINE_MATH**. |
| **INLINE_MATH** | Buffer all bytes into a memory string. | On `$`: Trigger **RENDER**, return to **TEXT**. |
| **BLOCK_MATH** | Buffer all bytes. | On `$$`: Trigger **RENDER**, return to **TEXT**. |

---

## 3. Rendering Engine (The Dual-Path)

### Path A: High-Fidelity (Sixel)
* **Trigger:** Terminal responds to `\x1b[c` with a string containing `4`.
* **Logic:**
    1.  Parse LaTeX string using `codeberg.org/go-latex/latex`.
    2.  Render the AST to an `image.RGBA` canvas using the library's `drawimg` sub-package.
    3.  Convert the `image.Image` to Sixel format using `github.com/mattn/go-sixel`.
    4.  Write the Sixel escape sequence to `os.Stdout`.

### Path B: Fallback (Unicode/Latex2UTF)
* **Trigger:** Sixel check fails or environment is Zed/Basic Terminal.
* **Logic:**
    1.  Use a lookup table (map) to replace LaTeX macros (e.g., `\sigma`, `\int`) with their **Mathematical Alphanumeric Symbols** in Unicode.
    2.  Handle basic structural elements (e.g., `^2` becomes `²`, `_i` becomes `ᵢ`).
    3.  Output the resulting UTF-8 string.

---

## 4. Technical Requirements & Constraints
* **No CGO:** The binary must be statically linked and pure Go to ensure "Binary and Done" portability.
* **Signal Forwarding:** The wrapper must forward signals (like `SIGINT` / `Ctrl+C`) and window resize events (`SIGWINCH`) to the child process.
* **Latency Goal:** Rendering must occur in a separate goroutine so the text stream doesn't "stutter" while waiting for an image to encode.
* **ANSI Passthrough:** All non-math ANSI escape codes (colors, bolding, cursor movements) from the child process must be passed through untouched.

---

## 5. CLI Usage Pattern
The binary should be named `math-wrap` (or similar) and take the command to wrap as arguments:

```bash
# Example Usage
math-wrap claude code
```

## 6. Recommended Go Dependency Manifest
* `github.com/creack/pty` (Terminal Hijacking)
* `codeberg.org/go-latex/latex` (Pure Go LaTeX Parser/Renderer)
* `github.com/mattn/go-sixel` (Sixel Encoder)
* `golang.org/x/term` (Terminal state/size management)


## 7. Required Features
* Proper conditional support for both "raw" and "cooked" modes
  * check if its Stdin is a Terminal (TTY) 
    * if true, raw mode - things like arrow key navigation of history must work when interactive
    * if false, cooked mode
* TTY resizing must function without issue (see hint below)
```go
// Forward terminal size changes to the PTY
ch := make(chan os.Signal, 1)
signal.Notify(ch, syscall.SIGWINCH)
go func() {
    for range ch {
        if err := pty.InheritSize(os.Stdin, f); err != nil {
            log.Printf("error resizing pty: %s", err)
        }
    }
}()
```

* implement signal propagation regardless of raw/cooked mode (see hint below)
```go
// This should run regardless of Raw/Cooked mode if a PTY is active
go func() {
    for range sigwinchChannel {
        // Update the PTY's internal termios size
        pty.InheritSize(os.Stdin, f)
        
        // Internal: Update your math-renderer's 'max-width' 
        // so the next $$ block is scaled correctly.
        renderer.UpdateWidth(getTermWidth())
    }
}()
```
