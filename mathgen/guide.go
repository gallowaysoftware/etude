package mathgen

import (
	"fmt"
	"math/rand"
	"strings"
)

// GuideOptions controls how much practice the rendered math guide carries.
type GuideOptions struct {
	Module         string // restrict to one module ("module_2"); empty = all
	Seed           int64  // makes the rendered guide reproducible
	WorkedExamples int    // worked examples per equation (default 1)
	PracticeShort  int    // short practice problems per equation (default 4)
	Comprehensive  int    // comprehensive multi-part problems per topic (default 1)
}

func (o GuideOptions) withDefaults() GuideOptions {
	if o.WorkedExamples <= 0 {
		o.WorkedExamples = 1
	}
	if o.PracticeShort <= 0 {
		o.PracticeShort = 4
	}
	if o.Comprehensive <= 0 {
		o.Comprehensive = 1
	}
	return o
}

// answerEntry is one item collected for the answer key at the end.
type answerEntry struct {
	label string
	p     Problem
}

// RenderGuide turns a catalog into a self-contained "math textbook":
// every equation with its statement, variable table, when-to-use note, a
// worked example (code-computed), and short practice; then a comprehensive
// multi-part problem per topic; then an answer key with full worked
// solutions. All arithmetic is done in Go, so every answer is correct.
//
// The same numbers are reproducible from Seed, so regenerating the guide
// is stable; the live drill (Equation.Generate with a fresh rng) produces
// the same shape of problem with different numbers.
func RenderGuide(c *Catalog, opt GuideOptions) string {
	opt = opt.withDefaults()
	rng := rand.New(rand.NewSource(opt.Seed))
	eqs := c.ByModule(opt.Module)

	var b strings.Builder
	title := "Math Reference & Practice"
	if c.Program != "" {
		title = c.Program + " — " + title
	}
	if opt.Module != "" {
		title += " (" + opt.Module + ")"
	}
	fmt.Fprintf(&b, "# %s\n\n", title)
	b.WriteString("Every important equation in this material, with what it means, when to use it, a worked example, and practice problems. All answers are computed exactly — work each problem yourself, then check the answer key at the end. The questions use fresh numbers each time the guide is regenerated; the drill tool poses the same kinds of problems with new values so you never memorise a single answer.\n\n")

	var key []answerEntry
	pnum := 0

	// Group by topic, in the catalog's stable order.
	var topics []string
	byTopic := map[string][]Equation{}
	for _, e := range eqs {
		if _, ok := byTopic[e.Topic]; !ok {
			topics = append(topics, e.Topic)
		}
		byTopic[e.Topic] = append(byTopic[e.Topic], e)
	}

	for _, topic := range topics {
		if topic != "" {
			fmt.Fprintf(&b, "## %s\n\n", topic)
		} else {
			b.WriteString("## Equations\n\n")
		}

		for _, e := range byTopic[topic] {
			fmt.Fprintf(&b, "### %s\n\n", e.Name)

			// Statement.
			src := e.Source
			if strings.TrimSpace(src) == "" {
				lhs := e.Result.Symbol
				if lhs == "" {
					lhs = "result"
				}
				src = lhs + " = " + e.Expr
			}
			fmt.Fprintf(&b, "```\n%s\n```\n\n", src)

			if strings.TrimSpace(e.UseNote) != "" {
				fmt.Fprintf(&b, "**When to use:** %s\n\n", strings.TrimSpace(e.UseNote))
			}

			// Variable table.
			b.WriteString("| Symbol | Meaning | Unit |\n|---|---|---|\n")
			for _, v := range e.Vars {
				role := ""
				if v.Role == RoleConstant {
					role = fmt.Sprintf(" (constant = %s)", displayNum(v.Value, v.Decimals))
				}
				fmt.Fprintf(&b, "| %s | %s%s | %s |\n", mdCell(v.symbol()), mdCell(v.Desc), role, mdCell(v.Unit))
			}
			if e.Result.Symbol != "" || e.Result.Desc != "" {
				rsym := e.Result.Symbol
				if rsym == "" {
					rsym = "result"
				}
				fmt.Fprintf(&b, "| **%s** | **%s** | **%s** |\n", mdCell(rsym), mdCell(e.Result.Desc), mdCell(e.Result.Unit))
			}
			b.WriteString("\n")

			// Worked example(s).
			for i := 0; i < opt.WorkedExamples; i++ {
				p, err := e.Generate(rng)
				if err != nil {
					continue
				}
				b.WriteString("**Worked example.** ")
				b.WriteString(p.Question)
				b.WriteString("\n\n")
				b.WriteString(renderSteps(p))
			}

			// Short practice — answers go to the key.
			if opt.PracticeShort > 0 {
				b.WriteString("**Practice.**\n\n")
				for i := 0; i < opt.PracticeShort; i++ {
					p, err := e.Generate(rng)
					if err != nil {
						continue
					}
					pnum++
					label := fmt.Sprintf("P%d", pnum)
					fmt.Fprintf(&b, "%s. %s\n", label, p.Question)
					key = append(key, answerEntry{label: label, p: p})
				}
				b.WriteString("\n")
			}
		}

		// Comprehensive multi-part problem(s) for this topic.
		topicEqs := byTopic[topic]
		for i := 0; i < opt.Comprehensive && len(topicEqs) >= 2; i++ {
			pnum++
			label := fmt.Sprintf("C%d", pnum)
			fmt.Fprintf(&b, "### Comprehensive problem %s\n\n", label)
			b.WriteString("Work each part; answers in the key.\n\n")
			parts := comprehensiveParts(topicEqs, rng)
			for j, p := range parts {
				partLabel := fmt.Sprintf("%s(%c)", label, 'a'+j)
				fmt.Fprintf(&b, "%s %s\n\n", partLabel, p.Question)
				key = append(key, answerEntry{label: partLabel, p: p})
			}
		}
	}

	// Answer key.
	if len(key) > 0 {
		b.WriteString("---\n\n## Answer Key\n\n")
		for _, ae := range key {
			fmt.Fprintf(&b, "**%s.** %s\n\n", ae.label, ae.p.AnswerText)
			b.WriteString(renderSteps(ae.p))
		}
	}

	return b.String()
}

// comprehensiveParts builds one part per equation in the topic (up to a
// sensible cap), each a fresh forward problem — a multi-step exercise that
// exercises the whole topic at once.
func comprehensiveParts(eqs []Equation, rng *rand.Rand) []Problem {
	const maxParts = 5
	var out []Problem
	for _, e := range eqs {
		if len(out) >= maxParts {
			break
		}
		p, err := e.Generate(rng)
		if err != nil {
			continue
		}
		out = append(out, p)
	}
	return out
}

// renderSteps formats a worked solution as an indented code block so the
// arithmetic lines up and pandoc/markdown renders it verbatim.
func renderSteps(p Problem) string {
	var b strings.Builder
	b.WriteString("```\n")
	for _, s := range p.Steps {
		b.WriteString(s)
		b.WriteByte('\n')
	}
	b.WriteString("```\n")
	if p.Citation != "" {
		fmt.Fprintf(&b, "\n*Source: %s*\n", p.Citation)
	}
	b.WriteString("\n")
	return b.String()
}

// mdCell escapes pipe characters so a value never breaks the table.
func mdCell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	if s == "" {
		return "—"
	}
	return s
}
