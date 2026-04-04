package unicode

// Lookup tables for LaTeX command → Unicode string mapping.
//
// These are intentionally flat maps. Perfect fidelity is not the goal;
// this is a best-effort rendering for terminal output.

// commands maps LaTeX command names (without leading backslash) to their
// Unicode approximations. Multi-character results (like "lim") are
// represented as plain strings — they have no special Unicode form and
// are emitted literally as a readability aid.
var commands = map[string]string{
	// Greek lowercase
	"alpha":      "α",
	"beta":       "β",
	"gamma":      "γ",
	"delta":      "δ",
	"epsilon":    "ε",
	"varepsilon": "ɛ",
	"zeta":       "ζ",
	"eta":        "η",
	"theta":      "θ",
	"vartheta":   "ϑ",
	"iota":       "ι",
	"kappa":      "κ",
	"lambda":     "λ",
	"mu":         "μ",
	"nu":         "ν",
	"xi":         "ξ",
	"pi":         "π",
	"varpi":      "ϖ",
	"rho":        "ρ",
	"varrho":     "ϱ",
	"sigma":      "σ",
	"varsigma":   "ς",
	"tau":        "τ",
	"upsilon":    "υ",
	"phi":        "φ",
	"varphi":     "ϕ",
	"chi":        "χ",
	"psi":        "ψ",
	"omega":      "ω",

	// Greek uppercase
	"Gamma":   "Γ",
	"Delta":   "Δ",
	"Theta":   "Θ",
	"Lambda":  "Λ",
	"Xi":      "Ξ",
	"Pi":      "Π",
	"Sigma":   "Σ",
	"Upsilon": "Υ",
	"Phi":     "Φ",
	"Psi":     "Ψ",
	"Omega":   "Ω",

	// Operators
	"int":     "∫",
	"iint":    "∬",
	"iiint":   "∭",
	"oint":    "∮",
	"sum":     "∑",
	"prod":    "∏",
	"partial": "∂",
	"nabla":   "∇",
	"infty":   "∞",
	"pm":      "±",
	"mp":      "∓",
	"times":   "×",
	"div":     "÷",
	"cdot":    "·",
	"ast":     "∗",
	"star":    "⋆",
	"circ":    "∘",
	"bullet": "•",
	// sqrt is handled structurally in handleBackslash (not via table lookup)
	// because it takes an argument: \sqrt{x} → √x.
	"lim": "lim",
	"log":     "log",
	"ln":      "ln",
	"exp":     "exp",
	"sin":     "sin",
	"cos":     "cos",
	"tan":     "tan",
	"det":     "det",
	"dim":     "dim",
	"min":     "min",
	"max":     "max",
	"sup":     "sup",
	"inf":     "inf",
	"gcd":     "gcd",

	// Relations
	"leq":      "≤",
	"geq":      "≥",
	"neq":      "≠",
	"approx":   "≈",
	"equiv":    "≡",
	"sim":      "∼",
	"simeq":    "≃",
	"cong":     "≅",
	"propto":   "∝",
	"ll":       "≪",
	"gg":       "≫",
	"subset":   "⊂",
	"supset":   "⊃",
	"subseteq": "⊆",
	"supseteq": "⊇",
	"in":       "∈",
	"notin":    "∉",
	"ni":       "∋",
	"forall":   "∀",
	"exists":   "∃",

	// Arrows
	"rightarrow":      "→",
	"leftarrow":       "←",
	"leftrightarrow":  "↔",
	"Rightarrow":      "⇒",
	"Leftarrow":       "⇐",
	"Leftrightarrow":  "⇔",
	"uparrow":         "↑",
	"downarrow":       "↓",
	"mapsto":          "↦",
	"implies":         "⟹",
	"iff":             "⟺",
	"to":              "→",

	// Binary ops
	"oplus":    "⊕",
	"otimes":   "⊗",
	"odot":     "⊙",
	"cap":      "∩",
	"cup":      "∪",
	"wedge":    "∧",
	"vee":      "∨",
	"setminus": "∖",

	// Delimiters
	"langle": "⟨",
	"rangle": "⟩",
	"lfloor": "⌊",
	"rfloor": "⌋",
	"lceil":  "⌈",
	"rceil":  "⌉",
	"lvert":  "|",
	"rvert":  "|",
	"lVert":  "‖",
	"rVert":  "‖",

	// Misc
	"hbar":       "ℏ",
	"ell":        "ℓ",
	"Re":         "ℜ",
	"Im":         "ℑ",
	"wp":         "℘",
	"aleph":      "ℵ",
	"emptyset":   "∅",
	"varnothing": "∅",
	"cdots":      "⋯",
	"ldots":      "…",
	"vdots":      "⋮",
	"ddots":      "⋱",
	"prime":      "′",
	"angle":      "∠",
	"triangle":   "△",
	"square":     "□",
	"diamond":    "◇",

	// Spacing
	"quad":      "    ",
	"qquad":     "        ",
	"enspace":   "\u2002",
	"thinspace": "\u2009",
}

// accentCommands maps LaTeX accent command names to their Unicode combining
// characters. These are applied after the content character: e.g. \hat{x}
// renders as x followed by the combining circumflex.
var accentCommands = map[string]string{
	"hat":  "\u0302", // combining circumflex
	"bar":  "\u0304", // combining overline
	"tilde": "\u0303", // combining tilde
	"vec":  "\u20D7", // combining right arrow
	"dot":  "\u0307", // combining dot above
	"ddot": "\u0308", // combining diaeresis
}

// superscripts maps ASCII characters to their Unicode superscript equivalents.
// Characters without a Unicode superscript form are absent from the map.
var superscripts = map[rune]rune{
	'0': '⁰', '1': '¹', '2': '²', '3': '³', '4': '⁴',
	'5': '⁵', '6': '⁶', '7': '⁷', '8': '⁸', '9': '⁹',
	'+': '⁺', '-': '⁻', '=': '⁼', '(': '⁽', ')': '⁾',
	'a': 'ᵃ', 'b': 'ᵇ', 'c': 'ᶜ', 'd': 'ᵈ', 'e': 'ᵉ',
	'f': 'ᶠ', 'g': 'ᵍ', 'h': 'ʰ', 'i': 'ⁱ', 'j': 'ʲ',
	'k': 'ᵏ', 'l': 'ˡ', 'm': 'ᵐ', 'n': 'ⁿ', 'o': 'ᵒ',
	'p': 'ᵖ', 'r': 'ʳ', 's': 'ˢ', 't': 'ᵗ', 'u': 'ᵘ',
	'v': 'ᵛ', 'w': 'ʷ', 'x': 'ˣ', 'y': 'ʸ', 'z': 'ᶻ',
}

// subscripts maps ASCII characters to their Unicode subscript equivalents.
// The available set is more limited than superscripts.
var subscripts = map[rune]rune{
	'0': '₀', '1': '₁', '2': '₂', '3': '₃', '4': '₄',
	'5': '₅', '6': '₆', '7': '₇', '8': '₈', '9': '₉',
	'+': '₊', '-': '₋', '=': '₌', '(': '₍', ')': '₎',
	'a': 'ₐ', 'e': 'ₑ', 'h': 'ₕ', 'i': 'ᵢ', 'j': 'ⱼ',
	'k': 'ₖ', 'l': 'ₗ', 'm': 'ₘ', 'n': 'ₙ', 'o': 'ₒ',
	'p': 'ₚ', 'r': 'ᵣ', 's': 'ₛ', 't': 'ₜ', 'u': 'ᵤ',
	'v': 'ᵥ', 'x': 'ₓ',
}

// fontStyleCommands are LaTeX commands that apply a font variant to
// their argument. Unicode terminal output cannot represent these styles,
// so we strip the command and render the argument content.
var fontStyleCommands = map[string]bool{
	"mathcal":      true,
	"mathrm":       true,
	"mathbf":       true,
	"mathit":       true,
	"mathbb":       true,
	"mathfrak":     true,
	"mathsf":       true,
	"mathtt":       true,
	"text":         true,
	"textrm":       true,
	"textbf":       true,
	"textit":       true,
	"texttt":       true,
	"textsf":       true,
	"operatorname": true,
	"boldsymbol":   true,
	"bm":           true,
}

// stripCommands are LaTeX commands that should be removed entirely from
// the output — they are layout hints with no visible content.
var stripCommands = map[string]bool{
	"left":  true,
	"right": true,
	"big":   true,
	"Big":   true,
	"bigg":  true,
	"Bigg":  true,
}
