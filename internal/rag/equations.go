package rag

import (
	"context"
	"fmt"
	"strings"
)

// equationsSystemPrompt asks the LLM to scan a textbook corpus and
// extract every equation / load-bearing numerical relationship with
// its context. Output is markdown grouped by topic. The subject
// descriptor is injected at run time so the open pipeline ships no
// curriculum-specific identity.
func equationsSystemPrompt(subject string) string {
	return "You are a study-aid generator for " + subject + ".\n\n" + `Given the full text of a textbook module, identify EVERY equation,
formula, or load-bearing numerical relationship a student must
memorise for the exam. Group them by topic. For each one, write:

  1. The equation as plain text (e.g. "v = d / t") — no
     LaTeX, no Unicode math symbols beyond × ÷ ≤ ≥ ± °.
  2. Each variable's meaning with units.
  3. A one-paragraph note on when this equation applies and what its
     edge cases are (boundary values, temperature corrections,
     unit-conversion gotchas).

Strict rules:
  - Dedup. If "yield = output / input × 100%" appears in three
    lessons with slightly different framings, emit ONE entry that
    consolidates the framings.
  - Skip standalone numbers without a relationship (those go on the
    key-numbers reference, not the equations cheat sheet).
  - Skip worked examples with specific numbers ("a 2.0 kg mass at
    3 m/s"). Equations only.
  - If a relationship is conditional (e.g. only valid above a
    threshold temperature), state the condition explicitly.

Output format: markdown. First byte: '#'. Use '## Topic' headings
to group, '### Name of equation' as the entry header, then the
plain-text equation in a fenced code block, then the variable
table, then the application note. No commentary, no preamble.`
}

// ExtractEquations runs a single LLM pass over the combined
// lecture_content of every lesson in the module and returns the
// equations.md markdown. Designed for module-sized inputs — the
// full Module 1 corpus is well under the 131k context of qwen3.6-27b.
// For larger modules the caller should chunk + run multiple passes
// and merge.
func ExtractEquations(ctx context.Context, c *LLMClient, lessons *ProcessedLessons) (string, error) {
	var b strings.Builder
	for _, l := range lessons.Items {
		fmt.Fprintf(&b, "## %s\n\n", l.Lesson)
		b.WriteString(strings.TrimSpace(l.LectureContent))
		b.WriteString("\n\n")
	}
	corpus := b.String()
	// Aggressive budget — equation extraction is one of the most
	// reasoning-heavy passes in the pipeline; give the model room
	// to think before it commits.
	return c.Chat(ctx, equationsSystemPrompt(c.Subject), corpus, ChatOpts{
		Temperature: 0.2,
		MaxTokens:   16384,
	})
}
