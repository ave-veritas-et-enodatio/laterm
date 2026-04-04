package statemachine

// State represents the current state of the delimiter-detection state machine.
type State int

const (
	// StateText is the default state. Bytes pass through to the terminal.
	StateText State = iota

	// StateEscape indicates an ESC byte (0x1B) was received. The next byte
	// determines which kind of escape sequence follows.
	StateEscape

	// StateEscapeSeq indicates the machine is inside a recognized ANSI
	// escape sequence (CSI, OSC, DCS, APC, PM, or SOS). All bytes pass
	// through until the sequence terminator is reached.
	StateEscapeSeq

	// StatePotentialMath indicates a '$' was seen in text context. The next
	// byte disambiguates between shell variable, inline math, and block math.
	StatePotentialMath

	// StateInlineMath indicates the machine is inside $...$, buffering
	// content for eventual extraction.
	StateInlineMath

	// StateBlockMath indicates the machine is inside $$...$$, buffering
	// content for eventual extraction.
	StateBlockMath

	// StateBlockMathClosing indicates a '$' was seen while in block math.
	// If the next byte is '$', the block expression is complete. Otherwise
	// the '$' is part of the math content.
	StateBlockMathClosing
)

// escapeType identifies which kind of ANSI escape sequence the machine is
// currently tracking.
type escapeType int

const (
	escNone escapeType = iota
	escCSI  // Control Sequence Introducer: ESC [
	escOSC  // Operating System Command:    ESC ]
	escDCS  // Device Control String:       ESC P
	escAPC  // Application Program Command: ESC _
	escPM   // Privacy Message:             ESC ^
	escSOS  // Start of String:             ESC X
)

const (
	esc = 0x1B // ESC byte
	bel = 0x07 // BEL byte — terminates OSC in some terminals
)

// Machine is a pure state machine for detecting LaTeX math delimiters in a
// terminal byte stream. It tracks ANSI escape sequences, applies shell-variable
// heuristics, and enforces byte budgets. It performs no I/O.
//
// The caller feeds bytes via Feed and receives Action values describing what
// to do with the output. The caller is responsible for managing wall-clock
// time budgets and calling TimeBudgetExpired when appropriate.
type Machine struct {
	state     State
	buf       []byte     // accumulated math content (including leading delimiters for flush)
	cfg       Config     // byte budgets
	byteCount int        // content bytes since entering math mode
	escType   escapeType // which ANSI sequence we are inside
	prevEsc   bool       // true if previous byte in escape seq was ESC (for ST detection)
}

// New creates a Machine with the given configuration. Zero-value config fields
// are replaced with defaults.
func New(cfg Config) *Machine {
	return &Machine{
		state: StateText,
		cfg:   cfg.withDefaults(),
	}
}

// State returns the machine's current state. Exported for testing and
// diagnostic logging by callers.
func (m *Machine) State() State {
	return m.state
}

// Reset returns the machine to TEXT state and clears all internal buffers.
// Any accumulated math content is discarded. The caller should use this when
// the stream is reset or on unrecoverable errors.
func (m *Machine) Reset() {
	m.state = StateText
	m.buf = m.buf[:0]
	m.byteCount = 0
	m.escType = escNone
	m.prevEsc = false
}

// TimeBudgetExpired is called by the stream layer when the wall-clock time
// budget for the current math expression has elapsed. If the machine is in a
// math-buffering state, it flushes the buffer as literal text and returns to
// TEXT. Otherwise it returns ActionNone.
func (m *Machine) TimeBudgetExpired() Action {
	switch m.state {
	case StateInlineMath, StateBlockMath, StateBlockMathClosing:
		return m.flushAndReset()
	case StatePotentialMath:
		return m.flushAndReset()
	default:
		return none()
	}
}

// Feed processes a single byte and returns an Action describing the output
// the caller should produce. This is the core of the state machine.
func (m *Machine) Feed(b byte) Action {
	switch m.state {
	case StateText:
		return m.feedText(b)
	case StateEscape:
		return m.feedEscape(b)
	case StateEscapeSeq:
		return m.feedEscapeSeq(b)
	case StatePotentialMath:
		return m.feedPotentialMath(b)
	case StateInlineMath:
		return m.feedInlineMath(b)
	case StateBlockMath:
		return m.feedBlockMath(b)
	case StateBlockMathClosing:
		return m.feedBlockMathClosing(b)
	default:
		// Unknown state — defensive reset. Should never happen in correct code.
		m.Reset()
		return emit(b)
	}
}

// feedText handles bytes in TEXT state.
func (m *Machine) feedText(b byte) Action {
	switch {
	case b == esc:
		m.state = StateEscape
		// Buffer the ESC byte to emit once we know the sequence type.
		return none()
	case b == '$':
		m.state = StatePotentialMath
		// Buffer the '$' — we'll need it if this turns out to be a shell
		// variable or if we need to flush literal.
		m.buf = append(m.buf[:0], '$')
		m.byteCount = 0
		return none()
	default:
		return emit(b)
	}
}

// feedEscape handles the byte immediately after ESC.
func (m *Machine) feedEscape(b byte) Action {
	switch b {
	case '[': // CSI
		m.state = StateEscapeSeq
		m.escType = escCSI
		return emit(esc, '[')
	case ']': // OSC
		m.state = StateEscapeSeq
		m.escType = escOSC
		m.prevEsc = false
		return emit(esc, ']')
	case 'P': // DCS
		m.state = StateEscapeSeq
		m.escType = escDCS
		m.prevEsc = false
		return emit(esc, 'P')
	case '_': // APC
		m.state = StateEscapeSeq
		m.escType = escAPC
		m.prevEsc = false
		return emit(esc, '_')
	case '^': // PM
		m.state = StateEscapeSeq
		m.escType = escPM
		m.prevEsc = false
		return emit(esc, '^')
	case 'X': // SOS
		m.state = StateEscapeSeq
		m.escType = escSOS
		m.prevEsc = false
		return emit(esc, 'X')
	default:
		// Two-byte escape sequence (e.g., ESC M for reverse index).
		m.state = StateText
		m.escType = escNone
		return emit(esc, b)
	}
}

// feedEscapeSeq handles bytes inside an ANSI escape sequence. All bytes,
// including '$', are emitted directly until the appropriate terminator.
func (m *Machine) feedEscapeSeq(b byte) Action {
	switch m.escType {
	case escCSI:
		// CSI terminates on a byte in the range 0x40-0x7E (@ through ~).
		if b >= 0x40 && b <= 0x7E {
			m.state = StateText
			m.escType = escNone
		}
		return emit(b)

	case escOSC:
		// OSC terminates on BEL (0x07) or ST (ESC \).
		if b == bel {
			m.state = StateText
			m.escType = escNone
			m.prevEsc = false
			return emit(b)
		}
		if m.prevEsc && b == '\\' {
			m.state = StateText
			m.escType = escNone
			m.prevEsc = false
			return emit(b)
		}
		m.prevEsc = (b == esc)
		return emit(b)

	case escDCS, escAPC, escPM, escSOS:
		// These all terminate on ST (ESC \).
		if m.prevEsc && b == '\\' {
			m.state = StateText
			m.escType = escNone
			m.prevEsc = false
			return emit(b)
		}
		m.prevEsc = (b == esc)
		return emit(b)

	default:
		// Should not happen — reset defensively.
		m.state = StateText
		m.escType = escNone
		return emit(b)
	}
}

// feedPotentialMath handles the byte after a '$' seen in text context.
// This is where shell-variable heuristics and inline-vs-block disambiguation
// happen.
func (m *Machine) feedPotentialMath(b byte) Action {
	switch {
	// Shell variable: $A-$Z (uppercase letter).
	//
	// Lowercase after $ is ambiguous between shell variables ($var) and
	// single-letter math variables ($x$, $n$). We reject only uppercase
	// because: (1) uppercase shell vars ($PATH, $HOME) are the dominant
	// false positive source, and (2) single-letter lowercase math like
	// $x$ is the primary LaTeX use case. Lowercase shell variables ($path
	// in zsh) will be caught by the time budget (200ms) and flushed as
	// literal.
	case b >= 'A' && b <= 'Z':
		return m.rejectAsShellVar(b)

	// Subshell: $(
	case b == '(':
		return m.rejectAsShellVar(b)

	// Shell expansion: ${
	case b == '{':
		return m.rejectAsShellVar(b)

	// Block math: $$
	case b == '$':
		m.state = StateBlockMath
		m.buf = append(m.buf, '$') // buf is now "$$"
		m.byteCount = 0
		return none()

	// Characters that plausibly start LaTeX inline math content:
	// backslash (\command), lowercase letter (variable), digit,
	// space, caret (^), underscore (_), opening paren in math context,
	// minus/plus for signed expressions, various other math symbols.
	case isInlineMathStart(b):
		m.state = StateInlineMath
		// The leading '$' is already in buf. Now buffer this content byte.
		m.buf = append(m.buf, b)
		m.byteCount = 1
		return bufferForMath()

	default:
		// Not math — flush the '$' as literal and emit this byte.
		return m.rejectAsLiteral(b)
	}
}

// feedInlineMath handles bytes inside an inline math expression ($...$).
func (m *Machine) feedInlineMath(b byte) Action {
	// Closing delimiter.
	if b == '$' {
		// Extract content: everything after the leading '$'.
		content := string(m.buf[1:])
		m.buf = m.buf[:0]
		m.byteCount = 0
		m.state = StateText
		return mathComplete(content, false)
	}

	// Byte budget check.
	m.byteCount++
	if m.byteCount > m.cfg.InlineByteBudget {
		return m.flushAndReset()
	}

	m.buf = append(m.buf, b)
	return bufferForMath()
}

// feedBlockMath handles bytes inside a block math expression ($$...$$).
func (m *Machine) feedBlockMath(b byte) Action {
	if b == '$' {
		m.state = StateBlockMathClosing
		return none()
	}

	// Byte budget check.
	m.byteCount++
	if m.byteCount > m.cfg.BlockByteBudget {
		return m.flushAndReset()
	}

	m.buf = append(m.buf, b)
	return bufferForMath()
}

// feedBlockMathClosing handles the byte after a '$' seen inside block math.
// If this byte is also '$', the expression is complete. Otherwise, the
// first '$' was part of the math content.
func (m *Machine) feedBlockMathClosing(b byte) Action {
	if b == '$' {
		// Closing $$. Extract content: everything after the leading "$$".
		content := string(m.buf[2:])
		m.buf = m.buf[:0]
		m.byteCount = 0
		m.state = StateText
		return mathComplete(content, true)
	}

	// The '$' we saw was not a closing delimiter — it's part of the content.
	// Add both the '$' and this byte to the buffer.
	m.byteCount += 2
	if m.byteCount > m.cfg.BlockByteBudget {
		m.state = StateBlockMath // reset sub-state before flush
		return m.flushAndReset()
	}

	m.buf = append(m.buf, '$', b)
	m.state = StateBlockMath
	return bufferForMath()
}

// rejectAsShellVar flushes the buffered '$' as literal text, emits b, and
// returns to TEXT state. Used when the byte after '$' indicates a shell
// variable pattern.
func (m *Machine) rejectAsShellVar(b byte) Action {
	dollar := m.buf // contains just "$"
	m.buf = m.buf[:0]
	m.byteCount = 0
	m.state = StateText

	out := make([]byte, len(dollar)+1)
	copy(out, dollar)
	out[len(dollar)] = b
	return Action{Kind: ActionFlushLiteral, Data: out}
}

// rejectAsLiteral flushes the buffered '$' as literal text and emits the
// current byte. Returns to TEXT state.
func (m *Machine) rejectAsLiteral(b byte) Action {
	dollar := m.buf
	m.buf = m.buf[:0]
	m.byteCount = 0
	m.state = StateText

	out := make([]byte, len(dollar)+1)
	copy(out, dollar)
	out[len(dollar)] = b
	return Action{Kind: ActionFlushLiteral, Data: out}
}

// flushAndReset flushes all buffered bytes as literal text and returns to
// TEXT state. Used on byte/time budget exhaustion.
func (m *Machine) flushAndReset() Action {
	act := flushLiteral(m.buf)
	m.buf = m.buf[:0]
	m.byteCount = 0
	m.state = StateText
	return act
}

// isInlineMathStart reports whether b is a plausible first byte of inline
// LaTeX math content.
func isInlineMathStart(b byte) bool {
	switch {
	case b == '\\': // \command
		return true
	case b >= 'a' && b <= 'z': // variable name
		return true
	case b >= '0' && b <= '9': // digit
		return true
	case b == ' ': // space (some formatting uses $ x $)
		return true
	case b == '^': // superscript
		return true
	case b == '_': // subscript
		return true
	case b == '-', b == '+': // signed expression
		return true
	case b == '|': // absolute value, norms
		return true
	case b == ')': // closing paren in math context
		return true
	case b == '<', b == '>': // angle brackets in math
		return true
	case b == '~': // tilde spacing
		return true
	case b == '!': // factorial, \! spacing
		return true
	default:
		return false
	}
}
