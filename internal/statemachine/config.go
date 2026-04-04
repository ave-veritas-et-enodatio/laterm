package statemachine

const (
	// DefaultInlineByteBudget is the maximum number of bytes allowed in an
	// inline math expression before the machine gives up and flushes literal.
	DefaultInlineByteBudget = 512

	// DefaultBlockByteBudget is the maximum number of bytes allowed in a
	// block math expression before the machine gives up and flushes literal.
	DefaultBlockByteBudget = 4096
)

// Config controls byte budgets for math expression detection. Zero values
// are replaced with defaults.
type Config struct {
	// InlineByteBudget is the maximum number of content bytes in an inline
	// math expression ($...$). If exceeded, the accumulated buffer is flushed
	// as literal text and the machine returns to TEXT state.
	InlineByteBudget int

	// BlockByteBudget is the maximum number of content bytes in a block
	// math expression ($$...$$). If exceeded, the accumulated buffer is
	// flushed as literal text and the machine returns to TEXT state.
	BlockByteBudget int
}

// withDefaults returns a copy of c with zero fields replaced by defaults.
func (c Config) withDefaults() Config {
	if c.InlineByteBudget <= 0 {
		c.InlineByteBudget = DefaultInlineByteBudget
	}
	if c.BlockByteBudget <= 0 {
		c.BlockByteBudget = DefaultBlockByteBudget
	}
	return c
}
