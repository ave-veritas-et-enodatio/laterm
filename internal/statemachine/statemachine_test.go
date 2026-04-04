package statemachine

import (
	"bytes"
	"strings"
	"testing"
)

// feedAll feeds every byte of input into the machine and collects all returned
// actions. This is the standard driver for table-driven tests.
func feedAll(m *Machine, input []byte) []Action {
	var actions []Action
	for _, b := range input {
		a := m.Feed(b)
		if a.Kind != ActionNone {
			actions = append(actions, a)
		}
	}
	return actions
}

// collectEmitted feeds input and concatenates all ActionEmit data into a single
// byte slice, for tests that only care about passthrough output.
func collectEmitted(m *Machine, input []byte) []byte {
	var out []byte
	for _, b := range input {
		a := m.Feed(b)
		if a.Kind == ActionEmit {
			out = append(out, a.Data...)
		}
	}
	return out
}

// assertState is a test helper that fails if the machine is not in the expected state.
func assertState(t *testing.T, m *Machine, want State) {
	t.Helper()
	if got := m.State(); got != want {
		t.Errorf("state = %d, want %d", got, want)
	}
}

// --- Acceptance criteria ---

func TestInlineMathDetection(t *testing.T) {
	// AC-1: $\sigma$ is detected as inline math and content \sigma is extracted.
	m := New(Config{})
	actions := feedAll(m, []byte(`$\sigma$`))

	var found bool
	for _, a := range actions {
		if a.Kind == ActionMathComplete {
			found = true
			if a.IsBlock {
				t.Error("expected inline math, got block")
			}
			if a.Content != `\sigma` {
				t.Errorf("content = %q, want %q", a.Content, `\sigma`)
			}
		}
	}
	if !found {
		t.Error("no ActionMathComplete returned")
	}
	assertState(t, m, StateText)
}

func TestBlockMathDetection(t *testing.T) {
	// AC-2: $$\int_0^1 f(x) dx$$ is detected as block math.
	m := New(Config{})
	input := []byte(`$$\int_0^1 f(x) dx$$`)
	actions := feedAll(m, input)

	var found bool
	for _, a := range actions {
		if a.Kind == ActionMathComplete {
			found = true
			if !a.IsBlock {
				t.Error("expected block math, got inline")
			}
			want := `\int_0^1 f(x) dx`
			if a.Content != want {
				t.Errorf("content = %q, want %q", a.Content, want)
			}
		}
	}
	if !found {
		t.Error("no ActionMathComplete returned")
	}
	assertState(t, m, StateText)
}

func TestBlockMathMultiline(t *testing.T) {
	// AC-2 (multiline variant).
	m := New(Config{})
	input := []byte("$$\n\\int_0^1\nf(x) dx\n$$")
	actions := feedAll(m, input)

	var found bool
	for _, a := range actions {
		if a.Kind == ActionMathComplete {
			found = true
			if !a.IsBlock {
				t.Error("expected block math, got inline")
			}
			want := "\n\\int_0^1\nf(x) dx\n"
			if a.Content != want {
				t.Errorf("content = %q, want %q", a.Content, want)
			}
		}
	}
	if !found {
		t.Error("no ActionMathComplete returned for multiline block math")
	}
}

func TestShellVariableRejection(t *testing.T) {
	// AC-3: $PATH, $HOME, $(cmd), ${var} are flushed as literal text.
	tests := []struct {
		name  string
		input string
	}{
		{"PATH", "$PATH"},
		{"HOME", "$HOME"},
		{"subshell", "$(cmd)"},
		{"expansion", "${var}"},
		{"single_uppercase", "$X"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(Config{})
			actions := feedAll(m, []byte(tt.input))

			// The first non-ActionNone action after '$' + shell char should be
			// ActionFlushLiteral for the '$' and the trigger byte.
			var gotFlush bool
			for _, a := range actions {
				if a.Kind == ActionFlushLiteral {
					gotFlush = true
					// Data should start with '$'.
					if len(a.Data) == 0 || a.Data[0] != '$' {
						t.Errorf("flush data = %q, expected to start with '$'", a.Data)
					}
				}
				if a.Kind == ActionMathComplete {
					t.Error("shell variable should not produce ActionMathComplete")
				}
			}
			if !gotFlush {
				t.Error("expected ActionFlushLiteral for shell variable pattern")
			}
			assertState(t, m, StateText)
		})
	}
}

func TestInlineByteBudgetExceeded(t *testing.T) {
	// AC-4: inline byte budget (512 bytes) exceeded → flush as literal.
	budget := 16 // use a small budget so the test is fast
	m := New(Config{InlineByteBudget: budget})

	// Start inline math with '$a', then feed budget+1 more bytes to exceed.
	var input bytes.Buffer
	input.WriteByte('$')
	input.WriteByte('a')
	for i := 0; i < budget+1; i++ {
		input.WriteByte('x')
	}

	actions := feedAll(m, input.Bytes())

	var gotFlush bool
	for _, a := range actions {
		if a.Kind == ActionFlushLiteral {
			gotFlush = true
		}
		if a.Kind == ActionMathComplete {
			t.Error("should not complete math when budget exceeded")
		}
	}
	if !gotFlush {
		t.Error("expected ActionFlushLiteral when inline byte budget exceeded")
	}
	assertState(t, m, StateText)
}

func TestBlockByteBudgetExceeded(t *testing.T) {
	// AC-5: block byte budget (4096 bytes) exceeded → flush as literal.
	budget := 32
	m := New(Config{BlockByteBudget: budget})

	var input bytes.Buffer
	input.WriteString("$$")
	for i := 0; i < budget+1; i++ {
		input.WriteByte('x')
	}

	actions := feedAll(m, input.Bytes())

	var gotFlush bool
	for _, a := range actions {
		if a.Kind == ActionFlushLiteral {
			gotFlush = true
		}
		if a.Kind == ActionMathComplete {
			t.Error("should not complete math when budget exceeded")
		}
	}
	if !gotFlush {
		t.Error("expected ActionFlushLiteral when block byte budget exceeded")
	}
	assertState(t, m, StateText)
}

func TestInlineTimeBudgetExpired(t *testing.T) {
	// AC-6: inline time budget exceeded → flush as literal.
	m := New(Config{})
	// Enter inline math.
	m.Feed('$')
	m.Feed('x')
	if m.State() != StateInlineMath {
		t.Fatalf("expected StateInlineMath, got %d", m.State())
	}

	a := m.TimeBudgetExpired()
	if a.Kind != ActionFlushLiteral {
		t.Errorf("Kind = %d, want ActionFlushLiteral (%d)", a.Kind, ActionFlushLiteral)
	}
	assertState(t, m, StateText)
}

func TestConsecutiveMathExpressions(t *testing.T) {
	// AC-7: consecutive math expressions ($a$ and $b$) detected independently.
	m := New(Config{})
	input := []byte("$a$ and $b$")
	actions := feedAll(m, input)

	var completions []Action
	for _, a := range actions {
		if a.Kind == ActionMathComplete {
			completions = append(completions, a)
		}
	}
	if len(completions) != 2 {
		t.Fatalf("got %d MathComplete actions, want 2", len(completions))
	}
	if completions[0].Content != "a" {
		t.Errorf("first content = %q, want %q", completions[0].Content, "a")
	}
	if completions[1].Content != "b" {
		t.Errorf("second content = %q, want %q", completions[1].Content, "b")
	}
	for i, c := range completions {
		if c.IsBlock {
			t.Errorf("completion[%d]: expected inline, got block", i)
		}
	}
}

func TestDollarInCSISequence(t *testing.T) {
	// AC-8: $ byte inside CSI sequence does NOT trigger math transition.
	m := New(Config{})
	// CSI sequence: ESC [ <params> <final>
	// Embed '$' as a parameter byte (valid CSI parameter range 0x30-0x3F includes '$'=0x24, which is below range.
	// Actually $ is 0x24, outside CSI parameter range — but the machine emits all CSI bytes until final.
	// Test: ESC [ 3 $ m  — the $ is in the sequence and should be emitted, not trigger math.
	input := []byte{0x1B, '[', '3', '$', 'm'}
	actions := feedAll(m, input)

	for _, a := range actions {
		if a.Kind == ActionMathComplete || a.Kind == ActionBufferForMath {
			t.Error("$ inside CSI sequence should not trigger math detection")
		}
	}
	assertState(t, m, StateText)
}

func TestDollarInOSCSequence(t *testing.T) {
	// AC-9: $ byte inside OSC sequence does NOT trigger math transition.
	m := New(Config{})
	// OSC: ESC ] <content> BEL
	input := []byte{0x1B, ']', '0', ';', '$', 'H', 'O', 'M', 'E', 0x07}
	actions := feedAll(m, input)

	for _, a := range actions {
		if a.Kind == ActionMathComplete || a.Kind == ActionBufferForMath {
			t.Error("$ inside OSC sequence should not trigger math detection")
		}
	}
	assertState(t, m, StateText)
}

func TestDollarInDCSAPCPMSOSSequences(t *testing.T) {
	// AC-10: $ byte inside DCS, APC, PM, SOS sequences does NOT trigger math.
	tests := []struct {
		name    string
		intro   byte
	}{
		{"DCS", 'P'},
		{"APC", '_'},
		{"PM", '^'},
		{"SOS", 'X'},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(Config{})
			// Sequence: ESC <intro> ... $ ... ESC \
			input := []byte{0x1B, tt.intro, 'x', '$', 'y', 0x1B, '\\'}
			actions := feedAll(m, input)

			for _, a := range actions {
				if a.Kind == ActionMathComplete || a.Kind == ActionBufferForMath {
					t.Errorf("$ inside %s sequence should not trigger math detection", tt.name)
				}
			}
			assertState(t, m, StateText)
		})
	}
}

func TestANSIPassthrough(t *testing.T) {
	// AC-11: ANSI escape sequences pass through unmodified.
	tests := []struct {
		name  string
		input []byte
	}{
		{
			"CSI_color",
			// ESC [ 31 m  (set foreground to red)
			[]byte{0x1B, '[', '3', '1', 'm'},
		},
		{
			"CSI_cursor",
			// ESC [ 10 ; 20 H  (move cursor)
			[]byte{0x1B, '[', '1', '0', ';', '2', '0', 'H'},
		},
		{
			"OSC_title_BEL",
			// ESC ] 0 ; title BEL
			[]byte{0x1B, ']', '0', ';', 't', 'i', 't', 'l', 'e', 0x07},
		},
		{
			"OSC_title_ST",
			// ESC ] 0 ; title ESC \
			[]byte{0x1B, ']', '0', ';', 't', 'i', 't', 'l', 'e', 0x1B, '\\'},
		},
		{
			"two_byte_escape",
			// ESC M (reverse line feed)
			[]byte{0x1B, 'M'},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(Config{})
			emitted := collectEmitted(m, tt.input)

			if !bytes.Equal(emitted, tt.input) {
				t.Errorf("emitted = %q, want %q", emitted, tt.input)
			}
			assertState(t, m, StateText)
		})
	}
}

func TestNoIODependencies(t *testing.T) {
	// AC-12: No I/O dependencies — tested with pure byte sequences.
	// This entire test file operates on []byte without any I/O. This test
	// exists as an explicit assertion that Machine has no I/O fields.
	m := New(Config{})
	_ = m.Feed('a')
	_ = m.Feed('$')
	_ = m.Feed('x')
	_ = m.Feed('$')
	_ = m.TimeBudgetExpired()
	m.Reset()
	// If this compiles and runs, the machine has no I/O dependencies.
	assertState(t, m, StateText)
}

// --- Additional edge cases ---

func TestBlockMathOpening(t *testing.T) {
	// $$ is block math opening, not empty inline math.
	m := New(Config{})
	m.Feed('$')
	m.Feed('$')
	if m.State() != StateBlockMath {
		t.Errorf("state = %d, want StateBlockMath (%d)", m.State(), StateBlockMath)
	}
}

func TestBlockMathWithInteriorDollar(t *testing.T) {
	// $$content with $ inside$$ — the interior $ is part of content.
	m := New(Config{})
	input := []byte("$$a$b$$")
	actions := feedAll(m, input)

	var found bool
	for _, a := range actions {
		if a.Kind == ActionMathComplete {
			found = true
			if !a.IsBlock {
				t.Error("expected block math")
			}
			if a.Content != "a$b" {
				t.Errorf("content = %q, want %q", a.Content, "a$b")
			}
		}
	}
	if !found {
		t.Error("no ActionMathComplete returned")
	}
}

func TestTripleDollar(t *testing.T) {
	// $$$ → block math opening ($$) then the third $ starts block-math-closing.
	m := New(Config{})
	m.Feed('$') // TEXT → POTENTIAL_MATH
	m.Feed('$') // POTENTIAL_MATH → BLOCK_MATH
	a := m.Feed('$') // BLOCK_MATH → BLOCK_MATH_CLOSING
	// Third $ transitions to BlockMathClosing; returns ActionNone since we're
	// waiting for the next byte to confirm closing.
	if a.Kind != ActionNone {
		t.Errorf("third $: Kind = %d, want ActionNone (%d)", a.Kind, ActionNone)
	}
	if m.State() != StateBlockMathClosing {
		t.Errorf("state = %d, want StateBlockMathClosing (%d)", m.State(), StateBlockMathClosing)
	}
}

func TestQuadrupleDollar(t *testing.T) {
	// $$$$ → block math opening ($$), then closing ($$), empty content.
	m := New(Config{})
	actions := feedAll(m, []byte("$$$$"))

	var found bool
	for _, a := range actions {
		if a.Kind == ActionMathComplete {
			found = true
			if !a.IsBlock {
				t.Error("expected block math")
			}
			if a.Content != "" {
				t.Errorf("content = %q, want empty", a.Content)
			}
		}
	}
	if !found {
		t.Error("no ActionMathComplete for $$$$")
	}
}

func TestTwoBytEscapeSequence(t *testing.T) {
	// Simple two-byte ESC sequence (ESC M for reverse line feed).
	m := New(Config{})
	a1 := m.Feed(0x1B)
	if a1.Kind != ActionNone {
		t.Errorf("ESC byte: Kind = %d, want ActionNone", a1.Kind)
	}
	a2 := m.Feed('M')
	if a2.Kind != ActionEmit {
		t.Fatalf("second byte: Kind = %d, want ActionEmit", a2.Kind)
	}
	if !bytes.Equal(a2.Data, []byte{0x1B, 'M'}) {
		t.Errorf("data = %q, want ESC M", a2.Data)
	}
	assertState(t, m, StateText)
}

func TestReset(t *testing.T) {
	// Reset returns machine to TEXT state from any state.
	tests := []struct {
		name  string
		setup func(m *Machine)
	}{
		{"from_inline_math", func(m *Machine) { m.Feed('$'); m.Feed('x') }},
		{"from_block_math", func(m *Machine) { m.Feed('$'); m.Feed('$'); m.Feed('x') }},
		{"from_potential_math", func(m *Machine) { m.Feed('$') }},
		{"from_escape", func(m *Machine) { m.Feed(0x1B) }},
		{"from_escape_seq", func(m *Machine) { m.Feed(0x1B); m.Feed('[') }},
		{"from_block_math_closing", func(m *Machine) { m.Feed('$'); m.Feed('$'); m.Feed('x'); m.Feed('$') }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(Config{})
			tt.setup(m)
			if m.State() == StateText {
				t.Fatal("setup did not move machine out of TEXT state")
			}
			m.Reset()
			assertState(t, m, StateText)
		})
	}
}

func TestFeedBufferForMath(t *testing.T) {
	// Feed returns ActionBufferForMath for content bytes in math mode.
	m := New(Config{})
	m.Feed('$') // → POTENTIAL_MATH

	a := m.Feed('x') // → INLINE_MATH, first content byte
	if a.Kind != ActionBufferForMath {
		t.Errorf("first content byte: Kind = %d, want ActionBufferForMath (%d)", a.Kind, ActionBufferForMath)
	}

	a = m.Feed('y') // still in INLINE_MATH
	if a.Kind != ActionBufferForMath {
		t.Errorf("second content byte: Kind = %d, want ActionBufferForMath (%d)", a.Kind, ActionBufferForMath)
	}
}

func TestTextToPotentialToInline(t *testing.T) {
	// Transition from TEXT → POTENTIAL_MATH → INLINE_MATH via lowercase letter after $.
	m := New(Config{})

	assertState(t, m, StateText)
	m.Feed('$')
	assertState(t, m, StatePotentialMath)
	m.Feed('a')
	assertState(t, m, StateInlineMath)
}

func TestDollarFollowedByNewline(t *testing.T) {
	// $ followed by newline → flushed as literal.
	m := New(Config{})
	actions := feedAll(m, []byte("$\n"))

	var gotFlush bool
	for _, a := range actions {
		if a.Kind == ActionFlushLiteral {
			gotFlush = true
			if !bytes.Equal(a.Data, []byte("$\n")) {
				t.Errorf("flush data = %q, want %q", a.Data, "$\n")
			}
		}
		if a.Kind == ActionMathComplete {
			t.Error("$ followed by newline should not produce math")
		}
	}
	if !gotFlush {
		t.Error("expected ActionFlushLiteral for $ followed by newline")
	}
}

func TestTimeBudgetExpiredInPotentialMath(t *testing.T) {
	// TimeBudgetExpired while in POTENTIAL_MATH.
	m := New(Config{})
	m.Feed('$')
	assertState(t, m, StatePotentialMath)

	a := m.TimeBudgetExpired()
	if a.Kind != ActionFlushLiteral {
		t.Errorf("Kind = %d, want ActionFlushLiteral", a.Kind)
	}
	if !bytes.Equal(a.Data, []byte("$")) {
		t.Errorf("data = %q, want %q", a.Data, "$")
	}
	assertState(t, m, StateText)
}

func TestTimeBudgetExpiredInBlockMath(t *testing.T) {
	m := New(Config{})
	m.Feed('$')
	m.Feed('$')
	m.Feed('x')
	assertState(t, m, StateBlockMath)

	a := m.TimeBudgetExpired()
	if a.Kind != ActionFlushLiteral {
		t.Errorf("Kind = %d, want ActionFlushLiteral", a.Kind)
	}
	// Buffer should contain "$$x".
	if !bytes.Equal(a.Data, []byte("$$x")) {
		t.Errorf("data = %q, want %q", a.Data, "$$x")
	}
	assertState(t, m, StateText)
}

func TestTimeBudgetExpiredInBlockMathClosing(t *testing.T) {
	m := New(Config{})
	m.Feed('$')
	m.Feed('$')
	m.Feed('x')
	m.Feed('$') // → BLOCK_MATH_CLOSING
	assertState(t, m, StateBlockMathClosing)

	a := m.TimeBudgetExpired()
	if a.Kind != ActionFlushLiteral {
		t.Errorf("Kind = %d, want ActionFlushLiteral", a.Kind)
	}
	assertState(t, m, StateText)
}

func TestTimeBudgetExpiredInTextState(t *testing.T) {
	// TimeBudgetExpired in TEXT state → ActionNone.
	m := New(Config{})
	a := m.TimeBudgetExpired()
	if a.Kind != ActionNone {
		t.Errorf("Kind = %d, want ActionNone", a.Kind)
	}
}

func TestTimeBudgetExpiredInEscapeState(t *testing.T) {
	// TimeBudgetExpired in ESCAPE state → ActionNone.
	m := New(Config{})
	m.Feed(0x1B)
	assertState(t, m, StateEscape)

	a := m.TimeBudgetExpired()
	if a.Kind != ActionNone {
		t.Errorf("Kind = %d, want ActionNone", a.Kind)
	}
}

func TestCSIWithColorCodes(t *testing.T) {
	// Various CSI sequences pass through fully.
	tests := []struct {
		name  string
		input []byte
	}{
		{"bold", []byte{0x1B, '[', '1', 'm'}},
		{"fg_256", []byte{0x1B, '[', '3', '8', ';', '5', ';', '1', '9', '6', 'm'}},
		{"reset", []byte{0x1B, '[', '0', 'm'}},
		{"erase_line", []byte{0x1B, '[', '2', 'K'}},
		{"scroll_region", []byte{0x1B, '[', '1', ';', '2', '4', 'r'}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(Config{})
			emitted := collectEmitted(m, tt.input)
			if !bytes.Equal(emitted, tt.input) {
				t.Errorf("emitted = %q, want %q", emitted, tt.input)
			}
			assertState(t, m, StateText)
		})
	}
}

func TestOSCWithSTTerminator(t *testing.T) {
	// OSC terminated by ST (ESC \).
	m := New(Config{})
	input := []byte{0x1B, ']', '2', ';', 'h', 'i', 0x1B, '\\'}
	emitted := collectEmitted(m, input)
	if !bytes.Equal(emitted, input) {
		t.Errorf("emitted = %q, want %q", emitted, input)
	}
	assertState(t, m, StateText)
}

func TestInlineMathStartCharacters(t *testing.T) {
	// Various characters that should trigger inline math detection.
	triggers := []struct {
		name string
		char byte
	}{
		{"backslash", '\\'},
		{"lowercase", 'a'},
		{"digit", '0'},
		{"space", ' '},
		{"caret", '^'},
		{"underscore", '_'},
		{"minus", '-'},
		{"plus", '+'},
		{"pipe", '|'},
		{"close_paren", ')'},
		{"less_than", '<'},
		{"greater_than", '>'},
		{"tilde", '~'},
		{"bang", '!'},
	}

	for _, tt := range triggers {
		t.Run(tt.name, func(t *testing.T) {
			m := New(Config{})
			m.Feed('$')
			a := m.Feed(tt.char)
			if a.Kind != ActionBufferForMath {
				t.Errorf("$ + %q: Kind = %d, want ActionBufferForMath (%d)", string(tt.char), a.Kind, ActionBufferForMath)
			}
			assertState(t, m, StateInlineMath)
		})
	}
}

func TestNonMathNonShellAfterDollar(t *testing.T) {
	// Characters that are neither shell-variable patterns nor math-start characters
	// should flush as literal.
	rejects := []byte{'\t', '#', '@', '%', '&', '*', '=', ',', '.', '/', '?', '"', '\'', ';', ':'}

	for _, b := range rejects {
		t.Run(string([]byte{b}), func(t *testing.T) {
			m := New(Config{})
			actions := feedAll(m, []byte{'$', b})

			var gotFlush bool
			for _, a := range actions {
				if a.Kind == ActionFlushLiteral {
					gotFlush = true
				}
			}
			if !gotFlush {
				t.Errorf("$ + 0x%02x: expected ActionFlushLiteral", b)
			}
			assertState(t, m, StateText)
		})
	}
}

func TestDefaultByteBudgets(t *testing.T) {
	// Zero-value config gets defaults.
	cfg := Config{}.withDefaults()
	if cfg.InlineByteBudget != DefaultInlineByteBudget {
		t.Errorf("InlineByteBudget = %d, want %d", cfg.InlineByteBudget, DefaultInlineByteBudget)
	}
	if cfg.BlockByteBudget != DefaultBlockByteBudget {
		t.Errorf("BlockByteBudget = %d, want %d", cfg.BlockByteBudget, DefaultBlockByteBudget)
	}
}

func TestCustomByteBudgets(t *testing.T) {
	// Non-zero config values are preserved.
	cfg := Config{InlineByteBudget: 100, BlockByteBudget: 200}.withDefaults()
	if cfg.InlineByteBudget != 100 {
		t.Errorf("InlineByteBudget = %d, want 100", cfg.InlineByteBudget)
	}
	if cfg.BlockByteBudget != 200 {
		t.Errorf("BlockByteBudget = %d, want 200", cfg.BlockByteBudget)
	}
}

func TestInlineByteBudgetBoundary(t *testing.T) {
	// Exactly at budget → still valid. One over → flush.
	budget := 4
	m := New(Config{InlineByteBudget: budget})

	// Feed '$' + 'a' (enters inline math) + budget bytes of content.
	m.Feed('$')
	m.Feed('a') // 1 content byte
	for i := 1; i < budget; i++ {
		a := m.Feed('x') // content bytes 2..budget
		if a.Kind != ActionBufferForMath {
			t.Fatalf("byte %d: Kind = %d, want ActionBufferForMath", i+1, a.Kind)
		}
	}
	// At this point byteCount == budget. Next byte exceeds.
	a := m.Feed('x')
	if a.Kind != ActionFlushLiteral {
		t.Errorf("budget+1 byte: Kind = %d, want ActionFlushLiteral", a.Kind)
	}
	assertState(t, m, StateText)
}

func TestBlockByteBudgetBoundary(t *testing.T) {
	budget := 4
	m := New(Config{BlockByteBudget: budget})

	m.Feed('$')
	m.Feed('$')
	for i := 0; i < budget; i++ {
		a := m.Feed('x')
		if a.Kind != ActionBufferForMath {
			t.Fatalf("byte %d: Kind = %d, want ActionBufferForMath", i+1, a.Kind)
		}
	}
	// Exceeds budget.
	a := m.Feed('x')
	if a.Kind != ActionFlushLiteral {
		t.Errorf("budget+1 byte: Kind = %d, want ActionFlushLiteral", a.Kind)
	}
	assertState(t, m, StateText)
}

func TestBlockByteBudgetExceededInClosingState(t *testing.T) {
	// Budget exceeded during BLOCK_MATH_CLOSING (interior $ + next byte push over).
	budget := 3
	m := New(Config{BlockByteBudget: budget})

	m.Feed('$')
	m.Feed('$')
	// Feed budget-1 content bytes so byteCount is budget-1.
	for i := 0; i < budget-1; i++ {
		m.Feed('x')
	}
	// Now byteCount == budget-1. Feed '$' → BLOCK_MATH_CLOSING.
	m.Feed('$')
	assertState(t, m, StateBlockMathClosing)

	// Feed a non-$ byte. This adds 2 to byteCount (the $ and this byte).
	// byteCount becomes budget-1+2 = budget+1. Should flush.
	a := m.Feed('y')
	if a.Kind != ActionFlushLiteral {
		t.Errorf("Kind = %d, want ActionFlushLiteral", a.Kind)
	}
	assertState(t, m, StateText)
}

func TestMixedTextAndMath(t *testing.T) {
	// Normal text, then math, then more text.
	m := New(Config{})
	input := []byte("hello $x$ world")
	actions := feedAll(m, input)

	var emitted []byte
	var mathContent string
	for _, a := range actions {
		switch a.Kind {
		case ActionEmit:
			emitted = append(emitted, a.Data...)
		case ActionFlushLiteral:
			emitted = append(emitted, a.Data...)
		case ActionMathComplete:
			mathContent = a.Content
		}
	}

	if mathContent != "x" {
		t.Errorf("math content = %q, want %q", mathContent, "x")
	}
	if string(emitted) != "hello  world" {
		t.Errorf("emitted = %q, want %q", string(emitted), "hello  world")
	}
}

func TestMathFollowedByANSI(t *testing.T) {
	// Math expression immediately followed by ANSI sequence.
	m := New(Config{})
	// $x$ then ESC [ 31 m
	input := []byte("$x$\x1b[31m")
	actions := feedAll(m, input)

	var gotMath, gotANSI bool
	for _, a := range actions {
		if a.Kind == ActionMathComplete {
			gotMath = true
		}
		if a.Kind == ActionEmit {
			gotANSI = true
		}
	}
	if !gotMath {
		t.Error("expected math completion before ANSI")
	}
	if !gotANSI {
		t.Error("expected ANSI passthrough after math")
	}
	assertState(t, m, StateText)
}

func TestANSIFollowedByMath(t *testing.T) {
	// ANSI sequence immediately followed by math.
	m := New(Config{})
	input := []byte("\x1b[31m$x$")
	actions := feedAll(m, input)

	var gotMath bool
	for _, a := range actions {
		if a.Kind == ActionMathComplete {
			gotMath = true
			if a.Content != "x" {
				t.Errorf("content = %q, want %q", a.Content, "x")
			}
		}
	}
	if !gotMath {
		t.Error("expected math completion after ANSI sequence")
	}
}

func TestResetClearsBuffer(t *testing.T) {
	// After reset, previously buffered content does not leak into next expression.
	m := New(Config{})
	m.Feed('$')
	m.Feed('a')
	m.Feed('b')
	m.Feed('c')
	m.Reset()

	// Now detect a new expression.
	actions := feedAll(m, []byte("$x$"))
	var found bool
	for _, a := range actions {
		if a.Kind == ActionMathComplete {
			found = true
			if a.Content != "x" {
				t.Errorf("content = %q, want %q (buffer leaked from before reset?)", a.Content, "x")
			}
		}
	}
	if !found {
		t.Error("no ActionMathComplete after reset")
	}
}

func TestLongInlineMathWithinBudget(t *testing.T) {
	// An inline expression exactly at the budget boundary succeeds.
	budget := 8
	m := New(Config{InlineByteBudget: budget})

	var input bytes.Buffer
	input.WriteByte('$')
	content := strings.Repeat("x", budget) // exactly budget bytes of content
	input.WriteString(content)
	input.WriteByte('$')

	actions := feedAll(m, input.Bytes())

	var found bool
	for _, a := range actions {
		if a.Kind == ActionMathComplete {
			found = true
			if a.Content != content {
				t.Errorf("content length = %d, want %d", len(a.Content), len(content))
			}
		}
		if a.Kind == ActionFlushLiteral {
			t.Error("should not flush when exactly at budget")
		}
	}
	if !found {
		t.Error("expected math completion at exact budget boundary")
	}
}

// --- Table-driven Feed tests ---

func TestFeedTableDriven(t *testing.T) {
	tests := []struct {
		name      string
		input     []byte
		wantKinds []ActionKind // expected kinds for non-ActionNone actions in order
		wantState State
	}{
		{
			name:      "plain_text",
			input:     []byte("hello"),
			wantKinds: []ActionKind{ActionEmit, ActionEmit, ActionEmit, ActionEmit, ActionEmit},
			wantState: StateText,
		},
		{
			name:      "just_dollar",
			input:     []byte("$"),
			wantKinds: nil, // ActionNone only
			wantState: StatePotentialMath,
		},
		{
			name:      "inline_complete",
			input:     []byte("$a$"),
			wantKinds: []ActionKind{ActionBufferForMath, ActionMathComplete},
			wantState: StateText,
		},
		{
			name:      "block_complete",
			input:     []byte("$$ab$$"),
			wantKinds: []ActionKind{ActionBufferForMath, ActionBufferForMath, ActionMathComplete},
			wantState: StateText,
		},
		{
			name:      "shell_var",
			input:     []byte("$HOME"),
			wantKinds: []ActionKind{ActionFlushLiteral, ActionEmit, ActionEmit, ActionEmit},
			wantState: StateText,
		},
		{
			name:      "esc_then_text",
			input:     []byte{0x1B, 'M', 'h', 'i'},
			wantKinds: []ActionKind{ActionEmit, ActionEmit, ActionEmit},
			wantState: StateText,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(Config{})
			actions := feedAll(m, tt.input)

			if len(actions) != len(tt.wantKinds) {
				var gotKinds []ActionKind
				for _, a := range actions {
					gotKinds = append(gotKinds, a.Kind)
				}
				t.Fatalf("got %d actions %v, want %d %v", len(actions), gotKinds, len(tt.wantKinds), tt.wantKinds)
			}
			for i, a := range actions {
				if a.Kind != tt.wantKinds[i] {
					t.Errorf("action[%d].Kind = %d, want %d", i, a.Kind, tt.wantKinds[i])
				}
			}
			assertState(t, m, tt.wantState)
		})
	}
}

// --- Comprehensive content extraction tests ---

func TestMathContentExtraction(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		content string
		isBlock bool
	}{
		{"simple_variable", `$x$`, "x", false},
		{"greek_letter", `$\alpha$`, `\alpha`, false},
		{"fraction", `$\frac{1}{2}$`, `\frac{1}{2}`, false},
		{"subscript", `$x_i$`, "x_i", false},
		{"superscript", `$x^2$`, "x^2", false},
		{"block_integral", `$$\int_0^1 x dx$$`, `\int_0^1 x dx`, true},
		{"block_sum", `$$\sum_{i=0}^n i$$`, `\sum_{i=0}^n i`, true},
		{"spaces", `$ x + y $`, " x + y ", false},
		{"backslash_commands", `$\sqrt{\pi}$`, `\sqrt{\pi}`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(Config{})
			actions := feedAll(m, []byte(tt.input))

			var found bool
			for _, a := range actions {
				if a.Kind == ActionMathComplete {
					found = true
					if a.Content != tt.content {
						t.Errorf("content = %q, want %q", a.Content, tt.content)
					}
					if a.IsBlock != tt.isBlock {
						t.Errorf("isBlock = %v, want %v", a.IsBlock, tt.isBlock)
					}
				}
			}
			if !found {
				t.Errorf("no ActionMathComplete for input %q", tt.input)
			}
		})
	}
}

func TestFlushLiteralDataIntegrity(t *testing.T) {
	// When the machine flushes literal, the data should be a faithful copy of
	// what was buffered — not a reference to the internal buffer.
	m := New(Config{})
	m.Feed('$')
	m.Feed('x') // inline math
	a := m.TimeBudgetExpired()
	if a.Kind != ActionFlushLiteral {
		t.Fatalf("Kind = %d, want ActionFlushLiteral", a.Kind)
	}

	flushed := a.Data
	// Feed more data to mutate the internal buffer.
	m.Feed('$')
	m.Feed('y')

	// The previously returned data should be unmodified.
	if !bytes.Equal(flushed, []byte("$x")) {
		t.Errorf("flushed data mutated: %q, want %q", flushed, "$x")
	}
}

func TestDollarInCSIParameterBytes(t *testing.T) {
	// CSI with $ in various positions — all should be emitted.
	m := New(Config{})
	// DECSCA: ESC [ 1 $ r
	input := []byte{0x1B, '[', '1', '$', 'r'}
	emitted := collectEmitted(m, input)
	if !bytes.Equal(emitted, input) {
		t.Errorf("emitted = %q, want %q", emitted, input)
	}
	assertState(t, m, StateText)
}

func TestMultipleMathExpressionsInterspersed(t *testing.T) {
	// Multiple math expressions with text between them.
	m := New(Config{})
	input := []byte("the $a$ and $$b$$ then $c$ end")
	actions := feedAll(m, input)

	var completions []Action
	for _, a := range actions {
		if a.Kind == ActionMathComplete {
			completions = append(completions, a)
		}
	}

	if len(completions) != 3 {
		t.Fatalf("got %d completions, want 3", len(completions))
	}

	want := []struct {
		content string
		isBlock bool
	}{
		{"a", false},
		{"b", true},
		{"c", false},
	}

	for i, w := range want {
		if completions[i].Content != w.content {
			t.Errorf("completion[%d].Content = %q, want %q", i, completions[i].Content, w.content)
		}
		if completions[i].IsBlock != w.isBlock {
			t.Errorf("completion[%d].IsBlock = %v, want %v", i, completions[i].IsBlock, w.isBlock)
		}
	}
}

// --- Fuzz test ---

func FuzzFeed(f *testing.F) {
	// Seed corpus with representative inputs.
	f.Add([]byte(`$\sigma$`))
	f.Add([]byte(`$$\int_0^1 f(x) dx$$`))
	f.Add([]byte("$PATH"))
	f.Add([]byte("$(cmd)"))
	f.Add([]byte("${var}"))
	f.Add([]byte("$$$"))
	f.Add([]byte("$$$$"))
	f.Add([]byte("hello $x$ world"))
	f.Add([]byte("\x1b[31m$x$\x1b[0m"))
	f.Add([]byte("\x1b]0;title\x07"))
	f.Add([]byte("\x1bM"))
	f.Add([]byte("$\n"))
	f.Add([]byte("$$a$b$$"))
	f.Add([]byte("normal text with no specials"))
	f.Add([]byte{0x1B, 'P', 'x', '$', 'y', 0x1B, '\\'})
	f.Add([]byte("$a$ $b$ $c$"))
	f.Add([]byte("$$"))
	f.Add([]byte("$"))
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		m := New(Config{InlineByteBudget: 64, BlockByteBudget: 256})

		for _, b := range data {
			a := m.Feed(b)

			// Invariant: Kind is always a known value.
			switch a.Kind {
			case ActionNone, ActionEmit, ActionBufferForMath, ActionMathComplete, ActionFlushLiteral:
				// OK.
			default:
				t.Fatalf("unknown ActionKind: %d", a.Kind)
			}

			// Invariant: ActionEmit and ActionFlushLiteral always have non-empty Data.
			if (a.Kind == ActionEmit || a.Kind == ActionFlushLiteral) && len(a.Data) == 0 {
				t.Fatalf("Kind=%d with empty Data", a.Kind)
			}

			// Invariant: state is always a known value.
			switch m.State() {
			case StateText, StateEscape, StateEscapeSeq, StatePotentialMath,
				StateInlineMath, StateBlockMath, StateBlockMathClosing:
				// OK.
			default:
				t.Fatalf("unknown State: %d", m.State())
			}
		}

		// After processing all input, TimeBudgetExpired should always leave
		// the machine in a non-math state or return ActionNone.
		a := m.TimeBudgetExpired()
		switch a.Kind {
		case ActionNone, ActionFlushLiteral:
			// OK.
		default:
			t.Fatalf("TimeBudgetExpired returned unexpected Kind: %d", a.Kind)
		}

		// After TimeBudgetExpired, machine should not be in a math-buffering state.
		switch m.State() {
		case StateText, StateEscape, StateEscapeSeq:
			// OK — these are non-math states.
		default:
			t.Fatalf("after TimeBudgetExpired, state = %d (expected non-math)", m.State())
		}

		// Reset should always return to TEXT.
		m.Reset()
		if m.State() != StateText {
			t.Fatalf("after Reset, state = %d, want StateText", m.State())
		}
	})
}
