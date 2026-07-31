package mathgen

import (
	"fmt"
	"maps"
	"math"
	"math/rand"
	"sort"
	"strconv"
	"strings"
)

// Problem is one generated, fully-solved numeric problem. The Answer and
// every line in Steps are computed in Go, so they are correct by
// construction. Question is the narrative posed to the student.
type Problem struct {
	EquationID string
	Question   string             // narrative with sampled numbers substituted in
	Inputs     map[string]float64 // the sampled input values
	Answer     float64            // computed result
	AnswerText string             // formatted answer with unit, e.g. "0.375 g/L"
	Steps      []string           // worked-solution lines (formula, substitution, result)
	Citation   string
}

// formatNum renders v rounded to exactly `decimals` places (presentation
// rounding for a final answer), trimming trailing zeros so "12.50" reads
// as "12.5" but "0.375" stays exact.
func formatNum(v float64, decimals int) string {
	if decimals < 0 {
		decimals = 0
	}
	s := strconv.FormatFloat(v, 'f', decimals, 64)
	return trimZeros(s)
}

// displayNum renders a KNOWN exact value (a sampled input or a constant)
// faithfully: it starts at `decimals` but bumps precision until the text
// round-trips to the value, so a constant like 0.1 declared with decimals
// 0 still shows as "0.1" rather than the misleading "0". Used everywhere a
// given value is shown to the student; the final answer uses formatNum
// (which intentionally rounds for presentation).
func displayNum(v float64, decimals int) string {
	if decimals < 0 {
		decimals = 0
	}
	for d := decimals; d <= decimals+8; d++ {
		s := strconv.FormatFloat(v, 'f', d, 64)
		if back, err := strconv.ParseFloat(s, 64); err == nil {
			tol := math.Abs(v) * 1e-9
			if v == 0 || math.Abs(back-v) <= tol {
				return trimZeros(s)
			}
		}
	}
	return trimZeros(strconv.FormatFloat(v, 'f', decimals+8, 64))
}

func trimZeros(s string) string {
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimSuffix(s, ".")
	}
	return s
}

// withUnit appends a unit when present.
func withUnit(s, unit string) string {
	if unit == "" {
		return s
	}
	return s + " " + unit
}

// Sample draws one set of input values. Continuous variables are rounded
// to their Decimals (default 2); a Step>0 quantises to that grid (so e.g.
// a normality samples to 0.1 N, not 0.0734 N). rng lets the caller make
// the guide reproducible (seed it) and the drill fresh (new seed each
// call).
func (e Equation) Sample(rng *rand.Rand) map[string]float64 {
	inputs := map[string]float64{}
	for _, v := range e.inputs() {
		x := v.Min + rng.Float64()*(v.Max-v.Min)
		if v.Step > 0 {
			x = math.Round(x/v.Step) * v.Step
			// keep inside the range after snapping
			if x < v.Min {
				x += v.Step
			}
			if x > v.Max {
				x -= v.Step
			}
		} else {
			dec := v.Decimals
			if dec == 0 {
				dec = 2
			}
			p := math.Pow(10, float64(dec))
			x = math.Round(x*p) / p
		}
		inputs[v.Name] = x
	}
	return inputs
}

// env merges sampled inputs with the equation's constants.
func (e Equation) env(inputs map[string]float64) map[string]float64 {
	env := e.constEnv()
	maps.Copy(env, inputs)
	return env
}

// Question renders the Prompt template, substituting each {var} with its
// sampled value (formatted to the variable's Decimals). When no Prompt is
// authored it falls back to a generic "given …, compute …" sentence.
func (e Equation) Question(inputs map[string]float64) string {
	if strings.TrimSpace(e.Prompt) != "" {
		return placeholderRe.ReplaceAllStringFunc(e.Prompt, func(m string) string {
			name := m[1 : len(m)-1]
			v, ok := e.varByName(name)
			if !ok {
				return m
			}
			return displayNum(inputs[name], v.Decimals)
		})
	}
	// Fallback narrative.
	var given []string
	for _, v := range e.inputs() {
		given = append(given, fmt.Sprintf("%s = %s", v.symbol(), withUnit(displayNum(inputs[v.Name], v.Decimals), v.Unit)))
	}
	sort.Strings(given)
	res := e.Result.Desc
	if res == "" {
		res = e.Name
	}
	return fmt.Sprintf("Given %s, calculate %s.", strings.Join(given, ", "), res)
}

// substituted renders the display equation with each variable name
// replaced by its value — the middle line of a worked solution. Names are
// replaced longest-first so "V_sample" isn't clobbered by "V". Uses the
// source form when available (its × / ÷ read better) else the Expr.
func (e Equation) substituted(inputs map[string]float64) string {
	display := e.Source
	if strings.TrimSpace(display) == "" {
		display = e.Expr
	}
	// Replace the right-hand side only when source is "LHS = RHS".
	if i := strings.Index(display, "="); i >= 0 && strings.TrimSpace(e.Source) != "" {
		display = display[i+1:]
	}
	type kv struct {
		name string
		val  float64
		dec  int
	}
	var vars []kv
	for _, v := range e.Vars {
		val := v.Value
		if v.Role != RoleConstant {
			val = inputs[v.Name]
		}
		vars = append(vars, kv{v.symbol(), val, v.Decimals})
		if v.symbol() != v.Name {
			vars = append(vars, kv{v.Name, val, v.Decimals})
		}
	}
	sort.Slice(vars, func(i, j int) bool { return len(vars[i].name) > len(vars[j].name) })
	out := display
	for _, kv := range vars {
		out = replaceIdent(out, kv.name, displayNum(kv.val, kv.dec))
	}
	out = strings.ReplaceAll(out, "*", "×")
	return strings.TrimSpace(out)
}

// replaceIdent replaces whole-identifier occurrences of name in s, so
// "N" doesn't match inside "V_NaOH". Boundaries are non-identifier runes.
func replaceIdent(s, name, repl string) string {
	if name == "" {
		return s
	}
	var b strings.Builder
	isIdent := func(r byte) bool {
		return r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
	}
	for i := 0; i < len(s); {
		if i+len(name) <= len(s) && s[i:i+len(name)] == name {
			beforeOK := i == 0 || !isIdent(s[i-1])
			afterOK := i+len(name) == len(s) || !isIdent(s[i+len(name)])
			if beforeOK && afterOK {
				b.WriteString(repl)
				i += len(name)
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// Solve computes the answer and a worked solution for one sampled input
// set. The arithmetic is done by Eval (correct by construction); the Steps
// show the formula, the substitution, and the result.
func (e Equation) Solve(inputs map[string]float64) (Problem, error) {
	ans, err := Eval(e.Expr, e.env(inputs))
	if err != nil {
		return Problem{}, fmt.Errorf("equation %s: %w", e.ID, err)
	}
	ansText := withUnit(formatNum(ans, e.Result.Decimals), e.Result.Unit)
	var steps []string
	lhs := e.Result.Symbol
	if lhs == "" {
		lhs = "answer"
	}
	if strings.TrimSpace(e.Source) != "" {
		steps = append(steps, e.Source)
	} else {
		steps = append(steps, fmt.Sprintf("%s = %s", lhs, e.Expr))
	}
	steps = append(steps, fmt.Sprintf("%s = %s", lhs, e.substituted(inputs)))
	steps = append(steps, fmt.Sprintf("%s = %s", lhs, ansText))
	return Problem{
		EquationID: e.ID,
		Question:   e.Question(inputs),
		Inputs:     inputs,
		Answer:     ans,
		AnswerText: ansText,
		Steps:      steps,
		Citation:   e.Citation,
	}, nil
}

// Generate samples one fresh problem and solves it. This is the single
// entry point both the guide (seeded rng) and the live drill (fresh rng)
// use — same shape, different numbers.
func (e Equation) Generate(rng *rand.Rand) (Problem, error) {
	return e.Solve(e.Sample(rng))
}
