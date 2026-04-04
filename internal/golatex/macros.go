// Copyright ©2020 The go-latex Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package latex

import (
	"strings"

	"github.com/ave-veritas-et-enodatio/laterm/internal/golatex/ast"
	"github.com/ave-veritas-et-enodatio/laterm/internal/golatex/internal/tex2unicode"
	"github.com/ave-veritas-et-enodatio/laterm/internal/golatex/token"
)

type macroParser interface {
	parseMacro(p *parser) ast.Node
}

func (p *parser) addBuiltinMacros() {
	p.macros = map[string]macroParser{
		// binary operators
		`\amalg`:           builtinMacro(""),
		`\ast`:             builtinMacro(""),
		`\bigcirc`:         builtinMacro(""),
		`\bigtriangledown`: builtinMacro(""),
		`\bigtriangleup`:   builtinMacro(""),
		`\bullet`:          builtinMacro(""),
		`\cdot`:            builtinMacro(""),
		`\circ`:            builtinMacro(""),
		`\cap`:             builtinMacro(""),
		`\cup`:             builtinMacro(""),
		`\dagger`:          builtinMacro(""),
		`\ddagger`:         builtinMacro(""),
		`\diamond`:         builtinMacro(""),
		`\div`:             builtinMacro(""),
		`\lhd`:             builtinMacro(""),
		`\mp`:              builtinMacro(""),
		`\odot`:            builtinMacro(""),
		`\ominus`:          builtinMacro(""),
		`\oplus`:           builtinMacro(""),
		`\oslash`:          builtinMacro(""),
		`\otimes`:          builtinMacro(""),
		`\pm`:              builtinMacro(""),
		`\rhd`:             builtinMacro(""),
		`\setminus`:        builtinMacro(""),
		`\sqcap`:           builtinMacro(""),
		`\sqcup`:           builtinMacro(""),
		`\star`:            builtinMacro(""),
		`\times`:           builtinMacro(""),
		`\triangleleft`:    builtinMacro(""),
		`\triangleright`:   builtinMacro(""),
		`\uplus`:           builtinMacro(""),
		`\unlhd`:           builtinMacro(""),
		`\unrhd`:           builtinMacro(""),
		`\vee`:             builtinMacro(""),
		`\wedge`:           builtinMacro(""),
		`\wr`:              builtinMacro(""),

		// arithmetic operators
		`\binom`:    builtinMacro("AA"),
		`\dfrac`:    builtinMacro("AA"),
		`\frac`:     builtinMacro("AA"),
		`\stackrel`: builtinMacro("AA"),
		`\tfrac`:    builtinMacro("AA"),

		// relation symbols
		`\approx`:     builtinMacro(""),
		`\asymp`:      builtinMacro(""),
		`\bowtie`:     builtinMacro(""),
		`\cong`:       builtinMacro(""),
		`\dashv`:      builtinMacro(""),
		`\doteq`:      builtinMacro(""),
		`\doteqdot`:   builtinMacro(""),
		`\dotplus`:    builtinMacro(""),
		`\dots`:       builtinMacro(""),
		`\equiv`:      builtinMacro(""),
		`\frown`:      builtinMacro(""),
		`\geq`:        builtinMacro(""),
		`\gg`:         builtinMacro(""),
		`\in`:         builtinMacro(""),
		`\leq`:        builtinMacro(""),
		`\ll`:         builtinMacro(""),
		`\mid`:        builtinMacro(""),
		`\models`:     builtinMacro(""),
		`\neq`:        builtinMacro(""),
		`\ni`:         builtinMacro(""),
		`\parallel`:   builtinMacro(""),
		`\perp`:       builtinMacro(""),
		`\prec`:       builtinMacro(""),
		`\preceq`:     builtinMacro(""),
		`\propto`:     builtinMacro(""),
		`\sim`:        builtinMacro(""),
		`\simeq`:      builtinMacro(""),
		`\smile`:      builtinMacro(""),
		`\sqsubset`:   builtinMacro(""),
		`\sqsubseteq`: builtinMacro(""),
		`\sqsupset`:   builtinMacro(""),
		`\sqsupseteq`: builtinMacro(""),
		`\subset`:     builtinMacro(""),
		`\subseteq`:   builtinMacro(""),
		`\succ`:       builtinMacro(""),
		`\succeq`:     builtinMacro(""),
		`\supset`:     builtinMacro(""),
		`\supseteq`:   builtinMacro(""),
		`\vdash`:      builtinMacro(""),
		`\Join`:       builtinMacro(""),

		// arrow symbols
		`\downarrow`:          builtinMacro(""),
		`\hookleftarrow`:      builtinMacro(""),
		`\hookrightarrow`:     builtinMacro(""),
		`\leadsto`:            builtinMacro(""),
		`\leftarrow`:          builtinMacro(""),
		`\leftharpoondown`:    builtinMacro(""),
		`\leftharpoonup`:      builtinMacro(""),
		`\leftrightarrow`:     builtinMacro(""),
		`\longleftarrow`:      builtinMacro(""),
		`\longleftrightarrow`: builtinMacro(""),
		`\longmapsto`:         builtinMacro(""),
		`\longrightarrow`:     builtinMacro(""),
		`\rightarrow`:         builtinMacro(""),
		`\mapsto`:             builtinMacro(""),
		`\nearrow`:            builtinMacro(""),
		`\nwarrow`:            builtinMacro(""),
		`\rightharpoondown`:   builtinMacro(""),
		`\rightharpoonup`:     builtinMacro(""),
		`\rightleftharpoons`:  builtinMacro(""),
		`\searrow`:            builtinMacro(""),
		`\swarrow`:            builtinMacro(""),
		`\uparrow`:            builtinMacro(""),
		`\updownarrow`:        builtinMacro(""),
		`\Downarrow`:          builtinMacro(""),
		`\Leftarrow`:          builtinMacro(""),
		`\Leftrightarrow`:     builtinMacro(""),
		`\Longleftarrow`:      builtinMacro(""),
		`\Longleftrightarrow`: builtinMacro(""),
		`\Longrightarrow`:     builtinMacro(""),
		`\Rightarrow`:         builtinMacro(""),
		`\Uparrow`:            builtinMacro(""),
		`\Updownarrow`:        builtinMacro(""),

		// punctuation symbols
		`\ldotp`: builtinMacro(""),
		`\cdotp`: builtinMacro(""),

		// over-under symbols
		`\bigcap`:    builtinMacro(""),
		`\bigcup`:    builtinMacro(""),
		`\bigodot`:   builtinMacro(""),
		`\bigoplus`:  builtinMacro(""),
		`\bigotimes`: builtinMacro(""),
		`\bigsqcup`:  builtinMacro(""),
		`\biguplus`:  builtinMacro(""),
		`\bigvee`:    builtinMacro(""),
		`\bigwedge`:  builtinMacro(""),
		`\coprod`:    builtinMacro(""),
		`\prod`:      builtinMacro(""),
		`\sum`:       builtinMacro(""),

		// over-under functions
		`\lim`:    builtinMacro(""),
		`\liminf`: builtinMacro(""),
		`\limsup`: builtinMacro(""),
		`\max`:    builtinMacro(""),
		`\min`:    builtinMacro(""),
		`\sup`:    builtinMacro(""),

		// dropsub symbols
		`\int`:  builtinMacro(""),
		`\oint`: builtinMacro(""),

		// font names
		`\rm`:      builtinMacro(""),
		`\cal`:     builtinMacro(""),
		`\it`:      builtinMacro(""),
		`\tt`:      builtinMacro(""),
		`\sf`:      builtinMacro(""),
		`\bf`:      builtinMacro(""),
		`\default`: builtinMacro(""),
		`\bb`:      builtinMacro(""),
		`\frak`:    builtinMacro(""),
		`\scr`:     builtinMacro(""),
		`\regular`: builtinMacro(""),

		// function names
		`\arccos`: builtinMacro(""),
		`\arcsin`: builtinMacro(""),
		`\arctan`: builtinMacro(""),
		`\arg`:    builtinMacro(""),
		`\cos`:    builtinMacro(""),
		`\cosh`:   builtinMacro(""),
		`\cot`:    builtinMacro(""),
		`\coth`:   builtinMacro(""),
		`\csc`:    builtinMacro(""),
		`\deg`:    builtinMacro(""),
		`\det`:    builtinMacro(""),
		`\dim`:    builtinMacro(""),
		`\exp`:    builtinMacro(""),
		`\gcd`:    builtinMacro(""),
		`\hom`:    builtinMacro(""),
		`\inf`:    builtinMacro(""),
		`\ker`:    builtinMacro(""),
		`\lg`:     builtinMacro(""),
		`\ln`:     builtinMacro(""),
		`\log`:    builtinMacro(""),
		`\sec`:    builtinMacro(""),
		`\sin`:    builtinMacro(""),
		`\sinh`:   builtinMacro(""),
		`\sqrt`:   builtinMacro("OA"),
		`\tan`:    builtinMacro(""),
		`\tanh`:   builtinMacro(""),
		`\Pr`:     builtinMacro(""),

		// ambi delim
		`\backslash`: builtinMacro(""),
		`\vert`:      builtinMacro(""),
		`\Vert`:      builtinMacro(""),

		// left delim
		`\{`:      builtinMacro(""),
		`\(`:      builtinMacro(""),
		`\langle`: builtinMacro(""),
		`\lceil`:  builtinMacro(""),
		`\lfloor`: builtinMacro(""),

		// right delim
		`\}`:      builtinMacro(""),
		`\)`:      builtinMacro(""),
		`\rangle`: builtinMacro(""),
		`\rceil`:  builtinMacro(""),
		`\rfloor`: builtinMacro(""),

		// symbols
		`\alpha`:   builtinMacro(""),
		`\beta`:    builtinMacro(""),
		`\gamma`:   builtinMacro(""),
		`\delta`:   builtinMacro(""),
		`\iota`:    builtinMacro(""),
		`\epsilon`: builtinMacro(""),
		`\eta`:     builtinMacro(""),
		`\kappa`:   builtinMacro(""),
		`\lambda`:  builtinMacro(""),
		`\mu`:      builtinMacro(""),
		`\nu`:      builtinMacro(""),
		`\omicron`: builtinMacro(""),
		`\pi`:      builtinMacro(""),
		`\theta`:   builtinMacro(""),
		`\xi`:      builtinMacro(""),
		`\rho`:     builtinMacro(""),
		`\sigma`:   builtinMacro(""),
		`\tau`:     builtinMacro(""),
		`\upsilon`: builtinMacro(""),
		`\phi`:     builtinMacro(""),
		`\chi`:     builtinMacro(""),
		`\psi`:     builtinMacro(""),
		`\omega`:   builtinMacro(""),
		`\zeta`:    builtinMacro(""),
		`\Alpha`:   builtinMacro(""),
		`\Beta`:    builtinMacro(""),
		`\Gamma`:   builtinMacro(""),
		`\Delta`:   builtinMacro(""),
		`\Epsilon`: builtinMacro(""),
		`\Zeta`:    builtinMacro(""),
		`\Eta`:     builtinMacro(""),
		`\Theta`:   builtinMacro(""),
		`\Iota`:    builtinMacro(""),
		`\Kappa`:   builtinMacro(""),
		`\Lambda`:  builtinMacro(""),
		`\Mu`:      builtinMacro(""),
		`\Nu`:      builtinMacro(""),
		`\Xi`:      builtinMacro(""),
		`\Omicron`: builtinMacro(""),
		`\Pi`:      builtinMacro(""),
		`\Rho`:     builtinMacro(""),
		`\Sigma`:   builtinMacro(""),
		`\Tau`:     builtinMacro(""),
		`\Upsilon`: builtinMacro(""),
		`\Phi`:     builtinMacro(""),
		`\Chi`:     builtinMacro(""),
		`\Psi`:     builtinMacro(""),
		`\Omega`:   builtinMacro(""),
		`\hbar`:    builtinMacro(""),
		`\nabla`:   builtinMacro(""),

		// math font
		`\mathbf`:      builtinMacro("A"),
		`\mathit`:      builtinMacro("A"),
		`\mathsf`:      builtinMacro("A"),
		`\mathtt`:      builtinMacro("A"),
		`\mathcal`:     builtinMacro("A"),
		`\mathdefault`: builtinMacro("A"),
		`\mathbb`:      builtinMacro("A"),
		`\mathfrak`:    builtinMacro("A"),
		`\mathscr`:     builtinMacro("A"),
		`\mathregular`: builtinMacro("A"),

		// text
		`\textbf`:      builtinMacro("A"),
		`\textit`:      builtinMacro("A"),
		`\textsf`:      builtinMacro("A"),
		`\texttt`:      builtinMacro("A"),
		`\textcal`:     builtinMacro("A"),
		`\textdefault`: builtinMacro("A"),
		`\textbb`:      builtinMacro("A"),
		`\textfrak`:    builtinMacro("A"),
		`\textscr`:     builtinMacro("A"),
		`\textregular`: builtinMacro("A"),

		// space, symbols
		`\ `:      builtinMacro(""),
		`\,`:      builtinMacro(""),
		`\;`:      builtinMacro(""),
		`\!`:      builtinMacro(""),
		`\quad`:   builtinMacro(""),
		`\qquad`:  builtinMacro(""),
		`\:`:      builtinMacro(""),
		`\cdots`:  builtinMacro(""),
		`\ddots`:  builtinMacro(""),
		`\ldots`:  builtinMacro(""),
		`\vdots`:  builtinMacro(""),
		`\hspace`: builtinMacro("A"),

		// accents (one required arg each)
		`\hat`:   builtinMacro("A"),
		`\tilde`: builtinMacro("A"),
		`\vec`:   builtinMacro("A"),
		`\bar`:   builtinMacro("A"),
		`\dot`:   builtinMacro("A"),
		`\ddot`:  builtinMacro("A"),
		`\breve`: builtinMacro("A"),
		`\acute`: builtinMacro("A"),
		`\grave`: builtinMacro("A"),
		`\check`: builtinMacro("A"),

		// text and font commands
		`\text`:       builtinMacro("A"),
		`\mathrm`:     builtinMacro("A"),
		`\boldsymbol`: builtinMacro("A"),

		// invisible box / spacing
		`\phantom`: builtinMacro("A"),

		// style switches (no args -- apply to rest of group)
		`\displaystyle`: builtinMacro(""),
		`\textstyle`:    builtinMacro(""),

		// over/under braces
		`\underbrace`: builtinMacro("A"),
		`\overbrace`:  builtinMacro("A"),

		// negation modifier (one required arg)
		`\not`: builtinMacro("A"),

		// color (consume color name arg; ignore)
		`\color`: builtinMacro("A"),

		// catch-all
		//
		`\overline`:     builtinMacro("A"),
		`\operatorname`: builtinMacro("A"),
	}

	// \left ... \right requires custom parsing: read a delimiter token,
	// collect content until \right, then read the closing delimiter.
	p.macros[`\left`] = funcMacro(parseLeftRight)

	// add all known UTF-8 symbols
	for _, k := range tex2unicode.Symbols() {
		_, ok := p.macros[`\`+k]
		if ok {
			continue
		}
		p.macros[`\`+k] = builtinMacro("")
	}
}

// funcMacro adapts a plain function to the macroParser interface.
type funcMacro func(p *parser) ast.Node

func (f funcMacro) parseMacro(p *parser) ast.Node { return f(p) }

type builtinMacro string

func (m builtinMacro) parseMacro(p *parser) ast.Node {
	node := &ast.Macro{
		Name: &ast.Ident{
			NamePos: p.s.tok.Pos,
			Name:    p.s.tok.Text,
		},
	}

	for _, typ := range strings.ToLower(string(m)) {
		switch typ {
		case 'a':
			p.parseMacroArg(node)
		case 'o':
			p.parseOptMacroArg(node)
		case 'v':
			p.parseVerbatimMacroArg(node)
		}
	}

	return node
}

// parseLeftRight handles \left<delim> ... \right<delim>.
//
// It reads the next token as the left delimiter, collects all nodes until a
// \right macro is encountered, then reads the token after \right as the right
// delimiter. The result is an *ast.Macro with Name="\left" and three Args:
//
//	Args[0]: left delimiter  (Symbol text)
//	Args[1]: content between \left and \right
//	Args[2]: right delimiter (Symbol text)
func parseLeftRight(p *parser) ast.Node {
	pos := p.s.tok.Pos

	ldelim := p.readDelimToken()

	var content ast.List
	for p.s.Next() {
		if p.s.tok.Kind == token.Macro && p.s.tok.Text == `\right` {
			break
		}
		node := p.parseNode(p.s.tok)
		if node != nil {
			content = append(content, node)
		}
	}

	rdelim := p.readDelimToken()

	return &ast.Macro{
		Name: &ast.Ident{NamePos: pos, Name: `\left`},
		Args: ast.List{
			&ast.Arg{List: ast.List{&ast.Symbol{Text: ldelim}}},
			&ast.Arg{List: content},
			&ast.Arg{List: ast.List{&ast.Symbol{Text: rdelim}}},
		},
	}
}

// readDelimToken reads the next token and returns the delimiter string.
// The delimiter may be a symbol token (e.g. "(", ")", "[", "]", ".", "|")
// or a macro token (e.g. \{, \}, \langle, \rangle, \vert, \Vert, \|).
func (p *parser) readDelimToken() string {
	tok := p.next()
	switch tok.Kind {
	case token.Symbol, token.Lparen, token.Rparen,
		token.Lbrack, token.Rbrack, token.Lbrace, token.Rbrace:
		return tok.Text
	case token.Macro:
		return tok.Text
	default:
		// Unrecognised delimiter -- use invisible delimiter as fallback so
		// the parser does not panic. The mtex layer can decide what to do.
		return "."
	}
}
