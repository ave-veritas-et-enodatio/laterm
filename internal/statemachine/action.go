// Package statemachine implements a pure byte-level state machine for detecting
// LaTeX math delimiters ($..$ and $$..$$) in a terminal byte stream. It tracks
// ANSI/VT escape sequences to avoid false triggers, applies shell-variable
// heuristics, and enforces byte/time budgets. The machine performs no I/O;
// callers feed bytes and receive actions describing what to do with the output.
package statemachine

// ActionKind identifies the type of action returned by the state machine.
type ActionKind int

const (
	// ActionNone means no output is needed for this byte. This occurs when
	// the machine is accumulating state (e.g., buffering math content or
	// waiting to disambiguate a delimiter).
	ActionNone ActionKind = iota

	// ActionEmit means the caller should write the returned bytes directly
	// to the terminal. Used for normal text passthrough and ANSI escape
	// sequence bytes.
	ActionEmit

	// ActionBufferForMath means the byte has been added to the internal math
	// buffer. The caller should produce no output for this byte.
	ActionBufferForMath

	// ActionMathComplete means a complete math expression has been detected.
	// Content holds the extracted LaTeX, and IsBlock indicates whether this
	// was a block ($$...$$) or inline ($...$) expression.
	ActionMathComplete

	// ActionFlushLiteral means the machine has abandoned a potential math
	// expression and is flushing accumulated bytes as literal text. This
	// happens on shell-variable rejection, byte budget exhaustion, or time
	// budget expiry. The caller should write the returned bytes directly.
	ActionFlushLiteral
)

// Action is the output of the state machine for a single Feed call or a
// budget-expiry notification.
type Action struct {
	Kind    ActionKind
	Data    []byte // for Emit and FlushLiteral
	Content string // for MathComplete: the extracted LaTeX content
	IsBlock bool   // for MathComplete: true if $$..$$, false if $..$
}

// emit returns an Action that tells the caller to write b directly to the terminal.
func emit(b ...byte) Action {
	return Action{Kind: ActionEmit, Data: b}
}

// bufferForMath returns an Action indicating a byte was added to the math buffer.
func bufferForMath() Action {
	return Action{Kind: ActionBufferForMath}
}

// mathComplete returns an Action indicating a math expression is ready.
func mathComplete(content string, isBlock bool) Action {
	return Action{Kind: ActionMathComplete, Content: content, IsBlock: isBlock}
}

// flushLiteral returns an Action that tells the caller to write accumulated
// bytes directly to the terminal as literal text.
func flushLiteral(data []byte) Action {
	// Return a copy so callers cannot observe mutations to the machine's buffer.
	out := make([]byte, len(data))
	copy(out, data)
	return Action{Kind: ActionFlushLiteral, Data: out}
}

// none returns an Action indicating no output is needed.
func none() Action {
	return Action{Kind: ActionNone}
}
