// Copyright ©2020 The go-latex Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mtex

import (
	"strings"

	"github.com/ave-veritas-et-enodatio/laterm/internal/golatex/ast"
	"github.com/ave-veritas-et-enodatio/laterm/internal/golatex/tex"
)

type handlerFunc func(p *parser, node ast.Node, state tex.State, math bool) tex.Node

func (h handlerFunc) Handle(p *parser, node ast.Node, state tex.State, math bool) tex.Node {
	return h(p, node, state, math)
}

type handler interface {
	Handle(p *parser, node ast.Node, state tex.State, math bool) tex.Node
}

var (
	builtinMacros = map[string]handler{
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
		`(`:       builtinMacro(""),
		`\langle`: builtinMacro(""),
		`\lceil`:  builtinMacro(""),
		`\lfloor`: builtinMacro(""),

		// right delim
		`\}`:      builtinMacro(""),
		`\)`:      builtinMacro(""),
		`)`:       builtinMacro(""),
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

		// text and font commands
		`\text`:       builtinMacro("A"),
		`\mathrm`:     builtinMacro("A"),
		`\boldsymbol`: builtinMacro("A"),

		// invisible box
		`\phantom`: builtinMacro("A"),

		// style switches (no args)
		`\displaystyle`: builtinMacro(""),
		`\textstyle`:    builtinMacro(""),

		// over/under braces
		`\underbrace`: builtinMacro("A"),
		`\overbrace`:  builtinMacro("A"),

		// negation modifier
		`\not`: builtinMacro("A"),

		// color (consume the arg, ignore color)
		`\color`: builtinMacro("A"),

		// accent commands
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

		// catch-all
		//
		`\overline`:     builtinMacro("A"),
		`\operatorname`: builtinMacro("A"),
	}
)

type builtinMacro string

func (m builtinMacro) Handle(p *parser, n ast.Node, state tex.State, math bool) tex.Node {
	node := n.(*ast.Macro)
	if m == "" {
		return tex.NewChar(node.Name.Name, state, math)
	}

	name := node.Name.Name

	// Two-arg macros like \stackrel — handle before the general loop.
	if string(m) == "AA" {
		return handleBuiltinTwoArg(p, node, name, state, math)
	}

	// Dispatch based on signature characters.
	// 'a' = required arg, 'o' = optional arg.
	// argIndex tracks which argument in node.Args we consume next.
	argIndex := 0
	var lastResult tex.Node
	for _, typ := range strings.ToLower(string(m)) {
		switch typ {
		case 'o':
			// Optional argument — consumed but not used by builtinMacro
			// (only sqrt uses it, and sqrt has its own handler).
			argIndex++
		case 'a':
			lastResult = handleBuiltinArg(p, node, name, argIndex, state, math)
			argIndex++
		}
	}

	if lastResult != nil {
		return lastResult
	}
	// Fallback: render as a plain character.
	return tex.NewChar(name, state, math)
}

// handleBuiltinArg handles a single required-argument macro dispatched through
// the builtinMacro table. It covers \math*, \text*, and \operatorname.
func handleBuiltinArg(p *parser, node *ast.Macro, name string, argIndex int, state tex.State, math bool) tex.Node {
	if argIndex >= len(node.Args) {
		// Missing argument — degrade gracefully to plain text.
		return tex.NewChar(name, state, math)
	}
	arg, ok := node.Args[argIndex].(*ast.Arg)
	if !ok {
		return tex.NewChar(name, state, math)
	}

	switch {
	// Math font commands: \mathcal, \mathbb, \mathbf, \mathrm, etc.
	case strings.HasPrefix(name, `\math`):
		fontType := name[5:] // strip `\math` prefix
		state.Font.Type = fontType
		return p.handleNode(ast.List(arg.List), state, math)

	// \text{...}: render in roman font, non-math mode.
	case name == `\text`:
		state.Font.Type = "rm"
		return p.handleNode(ast.List(arg.List), state, false)

	// Text font commands: \textbf, \textit, etc.
	case strings.HasPrefix(name, `\text`):
		fontType := name[5:] // strip `\text` prefix
		state.Font.Type = fontType
		return p.handleNode(ast.List(arg.List), state, false)

	// \boldsymbol{...}: render in bold font.
	case name == `\boldsymbol`:
		state.Font.Type = "bf"
		return p.handleNode(ast.List(arg.List), state, math)

	// \operatorname{...}: render argument content in roman font,
	// one character at a time, like handleFunction.
	case name == `\operatorname`:
		state.Font.Type = "rm"
		var nodes []tex.Node
		for _, child := range arg.List {
			nodes = append(nodes, p.handleNode(child, state, math))
		}
		return tex.HListOf(nodes, true)

	// \phantom{...}: render content (true phantom is invisible, but visible
	// is better than panicking).
	case name == `\phantom`:
		return p.handleNode(ast.List(arg.List), state, math)

	// \underbrace{...}, \overbrace{...}: render the content without the brace
	// decoration. Brace layout is complex; just render the body.
	case name == `\underbrace`, name == `\overbrace`:
		return p.handleNode(ast.List(arg.List), state, math)

	// \not{...}: negation modifier. Render the argument as-is; the
	// combining solidus overlay is cosmetic.
	case name == `\not`:
		return p.handleNode(ast.List(arg.List), state, math)

	// \color{...}: ignore color specification, render arg content.
	case name == `\color`:
		return p.handleNode(ast.List(arg.List), state, math)

	// Accent commands: \hat, \vec, \bar, \dot, etc. Render the argument
	// content without the accent decoration for now. Better than panicking.
	case name == `\hat`, name == `\tilde`, name == `\vec`,
		name == `\bar`, name == `\dot`, name == `\ddot`,
		name == `\breve`, name == `\acute`, name == `\grave`,
		name == `\check`:
		return p.handleNode(ast.List(arg.List), state, math)

	default:
		// Unknown single-arg macro — process the argument with current state.
		return p.handleNode(ast.List(arg.List), state, math)
	}
}

// handleBuiltinTwoArg handles two-argument macros dispatched through the
// builtinMacro table. Currently this covers \stackrel{A}{B} only; \frac,
// \binom, etc. have dedicated handlers that take priority in handler().
func handleBuiltinTwoArg(p *parser, node *ast.Macro, name string, state tex.State, math bool) tex.Node {
	if len(node.Args) < 2 {
		return tex.NewChar(name, state, math)
	}
	arg0, ok0 := node.Args[0].(*ast.Arg)
	arg1, ok1 := node.Args[1].(*ast.Arg)
	if !ok0 || !ok1 {
		return tex.NewChar(name, state, math)
	}

	switch name {
	case `\stackrel`:
		// \stackrel{A}{B}: place A (shrunk) centered above B, no fraction rule.
		top := p.handleNode(ast.List(arg0.List), state, math)
		bot := p.handleNode(ast.List(arg1.List), state, math)
		return p.genfrac("", "", 0, textStyle, top, bot, state)

	default:
		// Generic two-arg: just render both arguments sequentially.
		n0 := p.handleNode(ast.List(arg0.List), state, math)
		n1 := p.handleNode(ast.List(arg1.List), state, math)
		return tex.HListOf([]tex.Node{n0, n1}, true)
	}
}
