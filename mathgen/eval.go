// Package mathgen turns a catalog of computable equations into study
// material: a written "math textbook" (worked examples + practice
// problems with code-computed answers) and a live drill that generates
// fresh, never-repeating numeric problems. The arithmetic is done in Go,
// never by an LLM, so every answer is correct by construction.
//
// The package is curriculum-agnostic: equations, variable ranges, units,
// and question wording all come from a data-file Catalog (see catalog.go).
// The same Catalog drives the static guide (guide.go) and the live drill
// (problem.go), so a question a student sees in the drill is the same
// shape as one in the guide, only with different sampled numbers.
package mathgen

import (
	"fmt"
	"math"
	"strings"
	"unicode"
)

// Eval parses and evaluates an arithmetic expression over the given
// variable environment. It is a deliberately small, side-effect-free
// calculator — no assignment, no I/O, no unbounded loops — safe to run on
// expressions that originate from a data file. Supported syntax:
//
//   - - * / ^        binary operators (^ is right-associative power)
//   - +              unary sign
//     ( )              grouping
//     pi e             constants
//     f(a, b, ...)     function calls (see funcs below)
//     identifiers      resolved from env (case-sensitive)
//
// A missing variable, unknown function, malformed expression, or a result
// that is NaN/±Inf returns an error rather than a bogus number — callers
// rely on that to reject bad catalog entries.
func Eval(expr string, env map[string]float64) (float64, error) {
	p := &parser{toks: tokenize(expr)}
	v, err := p.parseExpr()
	if err != nil {
		return 0, err
	}
	if p.pos != len(p.toks) {
		return 0, fmt.Errorf("unexpected trailing input near %q", p.peek().text)
	}
	got, err := v.eval(env)
	if err != nil {
		return 0, err
	}
	if math.IsNaN(got) || math.IsInf(got, 0) {
		return 0, fmt.Errorf("expression %q evaluated to non-finite %v", expr, got)
	}
	return got, nil
}

// Variables returns the set of identifiers referenced by expr that are
// not built-in constants (pi, e) or function names. Used by the validator
// to confirm every symbol an equation uses is actually declared.
func Variables(expr string) ([]string, error) {
	p := &parser{toks: tokenize(expr)}
	node, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.pos != len(p.toks) {
		return nil, fmt.Errorf("unexpected trailing input near %q", p.peek().text)
	}
	seen := map[string]bool{}
	var out []string
	var walk func(n exprNode)
	walk = func(n exprNode) {
		switch t := n.(type) {
		case varNode:
			name := string(t)
			if _, isConst := constants[name]; isConst {
				return
			}
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		case unaryNode:
			walk(t.x)
		case binNode:
			walk(t.l)
			walk(t.r)
		case callNode:
			for _, a := range t.args {
				walk(a)
			}
		}
	}
	walk(node)
	return out, nil
}

// ---- constants & functions ------------------------------------------

var constants = map[string]float64{
	"pi": math.Pi,
	"e":  math.E,
}

// funcs are the whitelisted function calls. Each takes a fixed arity;
// the parser checks argument count at eval time.
var funcs = map[string]struct {
	arity int
	fn    func(a []float64) (float64, error)
}{
	"sqrt":  {1, func(a []float64) (float64, error) { return math.Sqrt(a[0]), nil }},
	"abs":   {1, func(a []float64) (float64, error) { return math.Abs(a[0]), nil }},
	"exp":   {1, func(a []float64) (float64, error) { return math.Exp(a[0]), nil }},
	"ln":    {1, func(a []float64) (float64, error) { return math.Log(a[0]), nil }},
	"log":   {1, func(a []float64) (float64, error) { return math.Log10(a[0]), nil }},
	"log2":  {1, func(a []float64) (float64, error) { return math.Log2(a[0]), nil }},
	"log10": {1, func(a []float64) (float64, error) { return math.Log10(a[0]), nil }},
	"sin":   {1, func(a []float64) (float64, error) { return math.Sin(a[0]), nil }},
	"cos":   {1, func(a []float64) (float64, error) { return math.Cos(a[0]), nil }},
	"tan":   {1, func(a []float64) (float64, error) { return math.Tan(a[0]), nil }},
	"floor": {1, func(a []float64) (float64, error) { return math.Floor(a[0]), nil }},
	"ceil":  {1, func(a []float64) (float64, error) { return math.Ceil(a[0]), nil }},
	"round": {1, func(a []float64) (float64, error) { return math.Round(a[0]), nil }},
	"pow":   {2, func(a []float64) (float64, error) { return math.Pow(a[0], a[1]), nil }},
	"min":   {2, func(a []float64) (float64, error) { return math.Min(a[0], a[1]), nil }},
	"max":   {2, func(a []float64) (float64, error) { return math.Max(a[0], a[1]), nil }},
}

// ---- AST -------------------------------------------------------------

type exprNode interface {
	eval(env map[string]float64) (float64, error)
}

type numNode float64

func (n numNode) eval(map[string]float64) (float64, error) { return float64(n), nil }

type varNode string

func (n varNode) eval(env map[string]float64) (float64, error) {
	name := string(n)
	if c, ok := constants[name]; ok {
		return c, nil
	}
	v, ok := env[name]
	if !ok {
		return 0, fmt.Errorf("undefined variable %q", name)
	}
	return v, nil
}

type unaryNode struct {
	op byte
	x  exprNode
}

func (n unaryNode) eval(env map[string]float64) (float64, error) {
	v, err := n.x.eval(env)
	if err != nil {
		return 0, err
	}
	if n.op == '-' {
		return -v, nil
	}
	return v, nil
}

type binNode struct {
	op   byte
	l, r exprNode
}

func (n binNode) eval(env map[string]float64) (float64, error) {
	l, err := n.l.eval(env)
	if err != nil {
		return 0, err
	}
	r, err := n.r.eval(env)
	if err != nil {
		return 0, err
	}
	switch n.op {
	case '+':
		return l + r, nil
	case '-':
		return l - r, nil
	case '*':
		return l * r, nil
	case '/':
		if r == 0 {
			return 0, fmt.Errorf("division by zero")
		}
		return l / r, nil
	case '^':
		return math.Pow(l, r), nil
	}
	return 0, fmt.Errorf("unknown operator %q", string(n.op))
}

type callNode struct {
	name string
	args []exprNode
}

func (n callNode) eval(env map[string]float64) (float64, error) {
	f, ok := funcs[n.name]
	if !ok {
		return 0, fmt.Errorf("unknown function %q", n.name)
	}
	if len(n.args) != f.arity {
		return 0, fmt.Errorf("function %q takes %d argument(s), got %d", n.name, f.arity, len(n.args))
	}
	vals := make([]float64, len(n.args))
	for i, a := range n.args {
		v, err := a.eval(env)
		if err != nil {
			return 0, err
		}
		vals[i] = v
	}
	return f.fn(vals)
}

// ---- tokenizer -------------------------------------------------------

type tokKind int

const (
	tokNum tokKind = iota
	tokIdent
	tokOp
	tokLParen
	tokRParen
	tokComma
)

type token struct {
	kind tokKind
	text string
	num  float64
}

func tokenize(s string) []token {
	var toks []token
	rs := []rune(s)
	i := 0
	for i < len(rs) {
		c := rs[i]
		switch {
		case unicode.IsSpace(c):
			i++
		case c >= '0' && c <= '9' || c == '.':
			j := i
			for j < len(rs) && (rs[j] >= '0' && rs[j] <= '9' || rs[j] == '.') {
				j++
			}
			// optional scientific notation: 1e3, 2.5e-4
			if j < len(rs) && (rs[j] == 'e' || rs[j] == 'E') {
				k := j + 1
				if k < len(rs) && (rs[k] == '+' || rs[k] == '-') {
					k++
				}
				if k < len(rs) && rs[k] >= '0' && rs[k] <= '9' {
					for k < len(rs) && rs[k] >= '0' && rs[k] <= '9' {
						k++
					}
					j = k
				}
			}
			text := string(rs[i:j])
			var f float64
			fmt.Sscanf(text, "%g", &f)
			toks = append(toks, token{kind: tokNum, text: text, num: f})
			i = j
		case c == '_' || unicode.IsLetter(c):
			j := i
			for j < len(rs) && (rs[j] == '_' || unicode.IsLetter(rs[j]) || rs[j] >= '0' && rs[j] <= '9') {
				j++
			}
			toks = append(toks, token{kind: tokIdent, text: string(rs[i:j])})
			i = j
		case c == '(':
			toks = append(toks, token{kind: tokLParen, text: "("})
			i++
		case c == ')':
			toks = append(toks, token{kind: tokRParen, text: ")"})
			i++
		case c == ',':
			toks = append(toks, token{kind: tokComma, text: ","})
			i++
		case strings.ContainsRune("+-*/^", c):
			toks = append(toks, token{kind: tokOp, text: string(c)})
			i++
		default:
			// Unknown rune becomes an op token so the parser errors clearly.
			toks = append(toks, token{kind: tokOp, text: string(c)})
			i++
		}
	}
	return toks
}

// ---- parser (recursive descent) -------------------------------------

type parser struct {
	toks []token
	pos  int
}

func (p *parser) peek() token {
	if p.pos < len(p.toks) {
		return p.toks[p.pos]
	}
	return token{kind: tokOp, text: ""} // EOF sentinel
}

func (p *parser) next() token {
	t := p.peek()
	p.pos++
	return t
}

// expr := term (('+'|'-') term)*
func (p *parser) parseExpr() (exprNode, error) {
	left, err := p.parseTerm()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t.kind == tokOp && (t.text == "+" || t.text == "-") {
			p.next()
			right, err := p.parseTerm()
			if err != nil {
				return nil, err
			}
			left = binNode{op: t.text[0], l: left, r: right}
			continue
		}
		return left, nil
	}
}

// term := unary (('*'|'/') unary)*
func (p *parser) parseTerm() (exprNode, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t.kind == tokOp && (t.text == "*" || t.text == "/") {
			p.next()
			right, err := p.parseUnary()
			if err != nil {
				return nil, err
			}
			left = binNode{op: t.text[0], l: left, r: right}
			continue
		}
		return left, nil
	}
}

// unary := ('+'|'-') unary | power
//
// Unary sign binds LOOSER than '^', matching the usual maths convention
// that -2^2 means -(2^2) = -4, not (-2)^2 = 4. Write (-2)^2 to negate the
// base.
func (p *parser) parseUnary() (exprNode, error) {
	t := p.peek()
	if t.kind == tokOp && (t.text == "+" || t.text == "-") {
		p.next()
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return unaryNode{op: t.text[0], x: x}, nil
	}
	return p.parsePower()
}

// power := primary ('^' unary)?   (right-associative; exponent may be signed)
func (p *parser) parsePower() (exprNode, error) {
	base, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	t := p.peek()
	if t.kind == tokOp && t.text == "^" {
		p.next()
		exp, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return binNode{op: '^', l: base, r: exp}, nil
	}
	return base, nil
}

// primary := number | ident | ident '(' args ')' | '(' expr ')'
func (p *parser) parsePrimary() (exprNode, error) {
	t := p.next()
	switch t.kind {
	case tokNum:
		return numNode(t.num), nil
	case tokIdent:
		if p.peek().kind == tokLParen {
			p.next() // consume '('
			var args []exprNode
			if p.peek().kind != tokRParen {
				for {
					a, err := p.parseExpr()
					if err != nil {
						return nil, err
					}
					args = append(args, a)
					if p.peek().kind == tokComma {
						p.next()
						continue
					}
					break
				}
			}
			if p.next().kind != tokRParen {
				return nil, fmt.Errorf("missing ) in call to %q", t.text)
			}
			return callNode{name: t.text, args: args}, nil
		}
		return varNode(t.text), nil
	case tokLParen:
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if p.next().kind != tokRParen {
			return nil, fmt.Errorf("missing closing )")
		}
		return e, nil
	}
	if t.text == "" {
		return nil, fmt.Errorf("unexpected end of expression")
	}
	return nil, fmt.Errorf("unexpected token %q", t.text)
}
