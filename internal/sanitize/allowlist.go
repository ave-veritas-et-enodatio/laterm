package sanitize

// allowedCommands is the set of LaTeX commands permitted in expressions.
// Looked up without the leading backslash (e.g. "frac", not "\frac").
//
// This list must match what the go-latex parser and mtex renderer actually
// handle. A command belongs here only if it appears in at least one of:
//   - internal/golatex/macros.go          (parser-level macros)
//   - internal/golatex/mtex/macros.go     (mtex-level handlers)
//   - internal/golatex/mtex/symbols/symbols_gen.go (FunctionNames, etc.)
//   - internal/golatex/internal/tex2unicode/utf8.go (tex2uni symbol table)
//
// Commands that were previously allowed but have no handler are listed in
// comments at the bottom so they can be re-enabled when support is added.
var allowedCommands = map[string]bool{
	// Greek letters (lowercase)
	// Sources: parser macros + mtex macros (explicit), tex2unicode
	"alpha":      true,
	"beta":       true,
	"gamma":      true,
	"delta":      true,
	"epsilon":    true,
	"varepsilon": true, // tex2unicode
	"zeta":       true,
	"eta":        true,
	"theta":      true,
	"vartheta":   true, // tex2unicode
	"iota":       true,
	"kappa":      true,
	"lambda":     true,
	"mu":         true,
	"nu":         true,
	"xi":         true,
	"omicron":    true, // parser macros (explicit)
	"pi":         true,
	"varpi":      true, // tex2unicode
	"rho":        true,
	"varrho":     true, // tex2unicode
	"sigma":      true,
	"varsigma":   true, // tex2unicode
	"tau":        true,
	"upsilon":    true,
	"phi":        true,
	"varphi":     true, // tex2unicode
	"chi":        true,
	"psi":        true,
	"omega":      true,

	// Greek letters (uppercase)
	// Sources: parser macros + mtex macros (explicit)
	"Alpha":   true,
	"Beta":    true,
	"Gamma":   true,
	"Delta":   true,
	"Epsilon": true,
	"Zeta":    true,
	"Eta":     true,
	"Theta":   true,
	"Iota":    true,
	"Kappa":   true,
	"Lambda":  true,
	"Mu":      true,
	"Nu":      true,
	"Xi":      true,
	"Omicron": true,
	"Pi":      true,
	"Rho":     true,
	"Sigma":   true,
	"Tau":     true,
	"Upsilon": true,
	"Phi":     true,
	"Chi":     true,
	"Psi":     true,
	"Omega":   true,

	// Arithmetic / fraction operators
	// Sources: parser macros + mtex macros
	"frac":     true,
	"dfrac":    true,
	"tfrac":    true,
	"binom":    true,
	"stackrel": true,
	"sqrt":     true,

	// Over-under symbols
	// Sources: parser macros + mtex macros, symbols_gen OverUnderSymbols
	"sum":       true,
	"prod":      true,
	"coprod":    true,
	"bigcap":    true,
	"bigcup":    true,
	"bigodot":   true,
	"bigoplus":  true,
	"bigotimes": true,
	"bigsqcup":  true,
	"biguplus":  true,
	"bigvee":    true,
	"bigwedge":  true,

	// Over-under functions / function names
	// Sources: parser macros + mtex macros, symbols_gen FunctionNames
	"lim":    true,
	"limsup": true,
	"liminf": true,
	"sup":    true,
	"inf":    true,
	"max":    true,
	"min":    true,
	"log":    true,
	"ln":     true,
	"lg":     true,
	"exp":    true,
	"sin":    true,
	"cos":    true,
	"tan":    true,
	"cot":    true,
	"sec":    true,
	"csc":    true,
	"arcsin": true,
	"arccos": true,
	"arctan": true,
	"sinh":   true,
	"cosh":   true,
	"tanh":   true,
	"coth":   true,
	"det":    true,
	"dim":    true,
	"ker":    true,
	"hom":    true,
	"arg":    true,
	"deg":    true,
	"gcd":    true,
	"Pr":     true,

	// Dropsub symbols
	// Sources: parser macros + mtex macros, symbols_gen DropSubSymbols
	"int":  true,
	"oint": true,

	// Integrals in tex2unicode only (no parser macro, but tex2uni auto-registers)
	"iint":  true, // tex2unicode
	"iiint": true, // tex2unicode

	// Relation symbols
	// Sources: parser macros + mtex macros, symbols_gen RelationSymbols, tex2unicode
	"leq":        true,
	"geq":        true,
	"neq":        true,
	"approx":     true,
	"equiv":      true,
	"sim":        true,
	"simeq":      true,
	"cong":       true,
	"propto":     true,
	"ll":         true,
	"gg":         true,
	"subset":     true,
	"supset":     true,
	"subseteq":   true,
	"supseteq":   true,
	"in":         true,
	"ni":         true,
	"notin":      true, // tex2unicode
	"forall":     true, // tex2unicode
	"exists":     true, // tex2unicode
	"nexists":    true, // tex2unicode
	"asymp":      true,
	"bowtie":     true,
	"dashv":      true,
	"doteq":      true,
	"doteqdot":   true,
	"dotplus":    true,
	"dots":       true,
	"frown":      true,
	"mid":        true,
	"models":     true,
	"parallel":   true,
	"perp":       true,
	"prec":       true,
	"preceq":     true,
	"smile":      true,
	"sqsubset":   true,
	"sqsubseteq": true,
	"sqsupset":   true,
	"sqsupseteq": true,
	"succ":       true,
	"succeq":     true,
	"vdash":      true,
	"Join":       true,

	// Arrow symbols
	// Sources: parser macros + mtex macros, symbols_gen ArrowSymbols, tex2unicode
	"rightarrow":          true,
	"leftarrow":           true,
	"leftrightarrow":      true,
	"Rightarrow":          true,
	"Leftarrow":           true,
	"Leftrightarrow":      true,
	"uparrow":             true,
	"downarrow":           true,
	"mapsto":              true,
	"to":                  true, // tex2unicode (maps to rightarrow)
	"longleftarrow":       true,
	"longrightarrow":      true,
	"longleftrightarrow":  true,
	"longmapsto":          true,
	"Longleftarrow":       true,
	"Longrightarrow":      true,
	"Longleftrightarrow":  true,
	"hookleftarrow":       true,
	"hookrightarrow":      true,
	"leftharpoondown":     true,
	"leftharpoonup":       true,
	"rightharpoondown":    true,
	"rightharpoonup":      true,
	"rightleftharpoons":   true,
	"nearrow":             true,
	"nwarrow":             true,
	"searrow":             true,
	"swarrow":             true,
	"updownarrow":         true,
	"Downarrow":           true,
	"Uparrow":             true,
	"Updownarrow":         true,
	"leadsto":             true,

	// Binary operators
	// Sources: parser macros + mtex macros, symbols_gen BinaryOperators
	"pm":              true,
	"mp":              true,
	"times":           true,
	"div":             true,
	"cdot":            true,
	"ast":             true,
	"star":            true,
	"circ":            true,
	"bullet":          true,
	"oplus":           true,
	"otimes":          true,
	"odot":            true,
	"ominus":          true,
	"oslash":          true,
	"cap":             true,
	"cup":             true,
	"wedge":           true,
	"vee":             true,
	"setminus":        true,
	"amalg":           true,
	"bigcirc":         true,
	"bigtriangledown": true,
	"bigtriangleup":   true,
	"dagger":          true,
	"ddagger":         true,
	"diamond":         true,
	"lhd":             true,
	"rhd":             true,
	"sqcap":           true,
	"sqcup":           true,
	"triangleleft":    true,
	"triangleright":   true,
	"uplus":           true,
	"unlhd":           true,
	"unrhd":           true,
	"wr":              true,

	// Delimiters
	// Sources: parser macros + mtex macros, symbols_gen AmbiDelim/LeftDelim/RightDelim
	"left":      true, // \left...\right parsed specially
	"right":     true,
	"langle":    true,
	"rangle":    true,
	"lfloor":    true,
	"rfloor":    true,
	"lceil":     true,
	"rceil":     true,
	"backslash": true,
	"vert":      true,
	"Vert":      true,

	// Punctuation symbols
	// Sources: parser macros + mtex macros, symbols_gen PunctuationSymbols
	"ldotp": true,
	"cdotp": true,

	// Accents (one required arg each)
	// Sources: parser macros + mtex macros
	"hat":   true,
	"bar":   true,
	"tilde": true,
	"vec":   true,
	"dot":   true,
	"ddot":  true,
	"breve": true,
	"acute": true,
	"grave": true,
	"check": true,

	// Wide accents in tex2unicode
	"widehat":   true, // tex2unicode
	"widetilde": true, // tex2unicode

	// Overline (parser macros + mtex macros)
	"overline": true,

	// Over/under arrows in tex2unicode
	"overleftarrow": true, // tex2unicode

	// Math font commands
	// Sources: parser macros + mtex macros
	"mathrm":      true,
	"mathbf":      true,
	"mathit":      true,
	"mathsf":      true,
	"mathtt":      true,
	"mathcal":     true,
	"mathbb":      true,
	"mathfrak":    true,
	"mathscr":     true,
	"mathdefault": true,
	"mathregular": true,

	// Text font commands
	// Sources: parser macros + mtex macros
	"text":        true,
	"textbf":      true,
	"textit":      true,
	"textsf":      true,
	"texttt":      true,
	"textcal":     true,
	"textdefault": true,
	"textbb":      true,
	"textfrak":    true,
	"textscr":     true,
	"textregular": true,

	// Font style switches (no args)
	// Sources: parser macros + mtex macros
	"boldsymbol":  true,
	"operatorname": true,

	// Short font names (apply to rest of group)
	// Sources: parser macros + mtex macros
	"rm":      true,
	"cal":     true,
	"it":      true,
	"tt":      true,
	"sf":      true,
	"bf":      true,
	"default": true,
	"bb":      true,
	"frak":    true,
	"scr":     true,
	"regular": true,

	// Style switches
	// Sources: parser macros + mtex macros
	"displaystyle": true,
	"textstyle":    true,

	// Spacing
	// Sources: parser macros + mtex macros
	"quad":   true,
	"qquad":  true,
	"hspace": true,

	// Dots
	// Sources: parser macros + mtex macros, tex2unicode
	"cdots": true,
	"ddots": true,
	"ldots": true,
	"vdots": true,

	// Over/under braces
	// Sources: parser macros + mtex macros
	"underbrace": true,
	"overbrace":  true,

	// Negation modifier
	// Sources: parser macros + mtex macros
	"not": true,

	// Invisible box
	// Sources: parser macros + mtex macros
	"phantom": true,

	// Color (consumes arg, ignored)
	// Sources: parser macros + mtex macros
	"color": true,

	// Misc symbols (all via tex2unicode auto-registration)
	"infty":       true,
	"partial":     true,
	"nabla":       true,
	"hbar":        true,
	"ell":         true,
	"Re":          true,
	"Im":          true,
	"wp":          true,
	"aleph":       true,
	"beth":        true,
	"emptyset":    true,
	"varnothing":  true,
	"prime":       true,
	"backprime":   true,
	"angle":       true,
	"clubsuit":    true,
	"diamondsuit": true,
	"heartsuit":   true,
	"spadesuit":   true,
	"flat":        true,
	"natural":     true,
	"sharp":       true,

	// --- Previously allowed commands removed: no parser/handler support yet ---
	//
	// "implies":         // no handler in parser, mtex, or tex2unicode
	// "iff":             // no handler in parser, mtex, or tex2unicode
	// "big":             // no handler (delimiter sizing not implemented)
	// "Big":             // no handler (delimiter sizing not implemented)
	// "bigg":            // no handler (delimiter sizing not implemented)
	// "Bigg":            // no handler (delimiter sizing not implemented)
	// "lvert":           // no handler in parser, mtex, or tex2unicode
	// "rvert":           // no handler in parser, mtex, or tex2unicode
	// "lVert":           // no handler in parser, mtex, or tex2unicode
	// "rVert":           // no handler in parser, mtex, or tex2unicode
	// "underline":       // no handler (only \overline is implemented)
	// "overrightarrow":  // no handler in parser, mtex, or tex2unicode
	// "enspace":         // mtex spaceWidth table only; parser doesn't register it
	// "thinspace":       // mtex spaceWidth table only; parser doesn't register it
	// "triangle":        // no handler; \triangleleft/\triangleright exist but not bare \triangle
	// "square":          // no handler; only \blacksquare in tex2unicode
	// "tbinom":          // no handler in parser, mtex, or tex2unicode
	// "dbinom":          // no handler in parser, mtex, or tex2unicode
	// "overset":         // no handler in parser, mtex, or tex2unicode
	// "underset":        // no handler in parser, mtex, or tex2unicode
	// "boxed":           // no handler in parser, mtex, or tex2unicode
	// "hphantom":        // no handler (only \phantom is implemented)
	// "vphantom":        // no handler (only \phantom is implemented)
	// "smash":           // no handler in parser, mtex, or tex2unicode
	// "mod":             // no handler in parser, mtex, or tex2unicode
	// "pmod":            // no handler in parser, mtex, or tex2unicode
	// "bmod":            // no handler in parser, mtex, or tex2unicode
	// "cancel":          // no handler in parser, mtex, or tex2unicode
	// "xcancel":         // no handler in parser, mtex, or tex2unicode
	// "sout":            // no handler in parser, mtex, or tex2unicode
}

// NOTE: Environments (\begin{env}...\end{env}) are not yet supported by
// the go-latex parser — it panics on \begin at parser.go:143. The
// allowedEnvironments map and \begin/\end special-case handling have been
// removed. Re-enable when the parser gains environment support.
//
// Previously allowed environments:
//   matrix, pmatrix, bmatrix, Bmatrix, vmatrix, Vmatrix,
//   cases, aligned, gathered, array, subarray, split
