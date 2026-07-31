// Package mathextract auto-proposes a mathgen Catalog from a textbook
// run's processed_lessons.json using the LLM, then keeps only the entries
// whose encoded formula reproduces a worked example transcribed from the
// source. The LLM is good at recognising "this is a computable formula"
// and transcribing it; it is NOT trusted to do arithmetic — the
// validation harness (mathgen.Validate) is the correctness gate, so a
// wrong formula is dropped rather than shipped.
package mathextract

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/gallowaysoftware/etude/internal/rag"
	"github.com/gallowaysoftware/etude/mathgen"
)

// Config drives one extraction run.
type Config struct {
	LessonsFile string // processed_lessons.json from a prior run
	ExtraFile   string // optional extra markdown of source equations
	OutFile     string // catalog YAML to write
	Module      string // tag entries with this module id
	Program     string // course descriptor woven into the prompt
	LLMURL      string
	LLMModel    string
	KeepInvalid bool // also write rejected entries to <out>.rejected.yaml
}

// corpusBudget caps how much source text we hand the model in one pass.
// Module corpora fit comfortably; very large modules should be split by
// the caller (one pass per few lessons) and the catalogs merged.
const corpusBudget = 220_000

// Run executes the extraction: read source, ask the LLM for a catalog,
// validate, and write only the entries that compute correctly.
func Run(ctx context.Context, cfg Config) error {
	data, err := os.ReadFile(cfg.LessonsFile)
	if err != nil {
		return fmt.Errorf("read lessons %s: %w", cfg.LessonsFile, err)
	}
	var pl rag.ProcessedLessons
	if err := json.Unmarshal(data, &pl); err != nil {
		return fmt.Errorf("parse lessons %s: %w", cfg.LessonsFile, err)
	}

	corpus := buildCorpus(&pl, cfg.ExtraFile)
	if strings.TrimSpace(corpus) == "" {
		return fmt.Errorf("no source content found in %s", cfg.LessonsFile)
	}

	client := rag.NewLLMClient(cfg.LLMURL, cfg.LLMModel, cfg.Program)
	raw, err := client.Chat(ctx, systemPrompt(cfg.Program), corpus, rag.ChatOpts{
		Temperature: 0.2,
		MaxTokens:   16384,
	})
	if err != nil {
		return fmt.Errorf("llm: %w", err)
	}

	cat, err := parseCatalog(raw)
	if err != nil {
		return fmt.Errorf("parse llm catalog: %w", err)
	}
	cat.Program = cfg.Program
	for i := range cat.Equations {
		if cat.Equations[i].Module == "" {
			cat.Equations[i].Module = cfg.Module
		}
	}

	clean, issues := cat.Filter()

	// Report.
	verified, unverified := 0, 0
	for _, e := range clean.Equations {
		if e.Verified() {
			verified++
		} else {
			unverified++
		}
	}
	fmt.Fprintf(os.Stderr, "extracted %d equation(s); kept %d (%d verified against a source example, %d unverified — spot-check these), dropped %d\n",
		len(cat.Equations), len(clean.Equations), verified, unverified, len(cat.Equations)-len(clean.Equations))
	if len(issues) > 0 {
		fmt.Fprintln(os.Stderr, "dropped entries and why:")
		for _, is := range issues {
			fmt.Fprintf(os.Stderr, "  - %s\n", is)
		}
	}

	if err := clean.Save(cfg.OutFile); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s\n", cfg.OutFile)

	if cfg.KeepInvalid && len(issues) > 0 {
		bad := badCatalog(cat, issues)
		rej := cfg.OutFile + ".rejected.yaml"
		if err := bad.Save(rej); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "wrote %s (failed entries, for manual repair)\n", rej)
	}
	return nil
}

// buildCorpus concatenates the equation-bearing parts of each lesson
// (lecture prose, key numbers, processes) plus any extra reference file,
// capped to the budget.
func buildCorpus(pl *rag.ProcessedLessons, extraFile string) string {
	var b strings.Builder
	for _, l := range pl.Items {
		fmt.Fprintf(&b, "## %s\n\n", l.Lesson)
		b.WriteString(strings.TrimSpace(l.LectureContent))
		b.WriteString("\n")
		if len(l.KeyNumbers) > 0 {
			b.WriteString("\nKey numbers:\n")
			for k, v := range l.KeyNumbers {
				fmt.Fprintf(&b, "- %s: %s\n", k, v)
			}
		}
		for _, p := range l.Processes {
			fmt.Fprintf(&b, "- %s\n", p)
		}
		b.WriteString("\n")
		if b.Len() > corpusBudget {
			break
		}
	}
	if extraFile != "" {
		if data, err := os.ReadFile(extraFile); err == nil {
			b.WriteString("\n## Additional equation reference\n\n")
			b.Write(data)
		}
	}
	s := b.String()
	if len(s) > corpusBudget {
		s = s[:corpusBudget]
	}
	return s
}

// parseCatalog extracts the JSON object the model emits and unmarshals it.
// Tolerates code fences and a leading "thinking" preamble by scanning for
// the first balanced {…} block.
func parseCatalog(raw string) (*mathgen.Catalog, error) {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "```"); i >= 0 {
		// Drop everything up to the first fence's newline, and any closing fence.
		if nl := strings.IndexByte(s[i:], '\n'); nl >= 0 {
			s = s[i+nl+1:]
		}
		if j := strings.LastIndex(s, "```"); j >= 0 {
			s = s[:j]
		}
		s = strings.TrimSpace(s)
	}
	obj, ok := firstJSONObject(s)
	if !ok {
		return nil, fmt.Errorf("no JSON object found in model output")
	}
	var cat mathgen.Catalog
	if err := json.Unmarshal([]byte(obj), &cat); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return &cat, nil
}

// firstJSONObject returns the first balanced {…} substring (brace-counted,
// string-aware), or false.
func firstJSONObject(s string) (string, bool) {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return "", false
	}
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
}

// badCatalog returns the equations that failed validation, for inspection.
func badCatalog(cat *mathgen.Catalog, issues []mathgen.ValidationIssue) *mathgen.Catalog {
	bad := map[string]bool{}
	for _, is := range issues {
		bad[is.EquationID] = true
	}
	out := &mathgen.Catalog{Program: cat.Program}
	for _, e := range cat.Equations {
		if bad[e.ID] {
			out.Equations = append(out.Equations, e)
		}
	}
	return out
}
