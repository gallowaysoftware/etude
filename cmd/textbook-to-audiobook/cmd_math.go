package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/gallowaysoftware/textbook-to-audiobook/internal/mathextract"
	"github.com/gallowaysoftware/textbook-to-audiobook/mathgen"
)

// mathCmd is the umbrella for the "math textbook" family. A catalog of
// computable equations (a YAML data file, hand-authored or auto-extracted
// from the curriculum) drives two outputs:
//
//	math guide    — a written math reference + practice with code-computed
//	                answers (the "all the math in this unit" study artifact)
//	math drill    — a single fresh, never-repeating numeric problem (the
//	                same mechanism the live RAG drill uses)
//	math validate — check a catalog: every formula parses, computes, and
//	                reproduces its source worked example
//	math extract  — LLM-propose a catalog from processed_lessons.json, then
//	                keep only the entries that validate
//
// The arithmetic is always done in Go (mathgen), never by an LLM, so every
// answer in the guide and the drill is correct by construction.
func mathCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "math",
		Short: "Math textbook + drill from a computable-equation catalog.",
		Long: `math turns a YAML catalog of computable equations into study material:

  math guide     — render the math reference + practice (answers computed exactly)
  math drill     — pose one fresh numeric problem with a worked solution
  math validate  — verify every catalog formula parses, computes, and matches its source example
  math extract   — LLM-propose a catalog from a run's processed_lessons.json, keep only what validates

The catalog is curriculum data (formula, variable ranges, units, question
wording, and a source worked example per equation). The same catalog feeds
the live drill in the RAG environment, so a drilled question is the same
shape with fresh numbers.
`,
	}
	cmd.AddCommand(mathGuideCmd())
	cmd.AddCommand(mathValidateCmd())
	cmd.AddCommand(mathDrillCmd())
	cmd.AddCommand(mathExtractCmd())
	return cmd
}

// loadAndFilter loads a catalog and drops entries that fail validation,
// reporting what was dropped to stderr (so a guide/drill never ships a
// wrong formula, but one bad entry doesn't sink the whole run).
func loadAndFilter(path string) (*mathgen.Catalog, error) {
	cat, err := mathgen.LoadCatalog(path)
	if err != nil {
		return nil, err
	}
	clean, issues := cat.Filter()
	if len(issues) > 0 {
		fmt.Fprintf(os.Stderr, "warning: dropped %d catalog entr(ies) that failed validation:\n", len(issues))
		for _, is := range issues {
			fmt.Fprintf(os.Stderr, "  - %s\n", is)
		}
	}
	return clean, nil
}

func mathGuideCmd() *cobra.Command {
	var (
		catalogPath   string
		outFile       string
		module        string
		seed          int64
		worked        int
		practice      int
		comprehensive int
	)
	cmd := &cobra.Command{
		Use:   "guide",
		Short: "Render the math reference + practice guide (markdown).",
		Long: `guide renders a self-contained "math textbook" from the catalog: every
equation with its statement, variable table, when-to-use note, a worked
example, and practice problems — followed by comprehensive multi-part
problems and an answer key with full worked solutions. All arithmetic is
computed in Go, so every answer is exact.

The numbers are reproducible from --seed; regenerate with a new seed for a
fresh problem set.
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cat, err := loadAndFilter(catalogPath)
			if err != nil {
				return err
			}
			if len(cat.Equations) == 0 {
				return fmt.Errorf("no valid equations in catalog %s", catalogPath)
			}
			md := mathgen.RenderGuide(cat, mathgen.GuideOptions{
				Module:         module,
				Seed:           seed,
				WorkedExamples: worked,
				PracticeShort:  practice,
				Comprehensive:  comprehensive,
			})
			if outFile == "" || outFile == "-" {
				fmt.Print(md)
				return nil
			}
			if err := os.WriteFile(outFile, []byte(md), 0o644); err != nil {
				return fmt.Errorf("write %s: %w", outFile, err)
			}
			fmt.Fprintf(os.Stderr, "wrote %s (%d equations)\n", outFile, len(cat.ByModule(module)))
			return nil
		},
	}
	cmd.Flags().StringVar(&catalogPath, "catalog", "", "Path to the equation catalog YAML (required).")
	cmd.Flags().StringVar(&outFile, "out", "", "Output markdown path ('-' or empty = stdout).")
	cmd.Flags().StringVar(&module, "module", "", "Restrict to one module (e.g. 'module_2'); empty = all.")
	cmd.Flags().Int64Var(&seed, "seed", 1, "Random seed for problem numbers (reproducible).")
	cmd.Flags().IntVar(&worked, "worked", 1, "Worked examples per equation.")
	cmd.Flags().IntVar(&practice, "practice", 4, "Short practice problems per equation.")
	cmd.Flags().IntVar(&comprehensive, "comprehensive", 1, "Comprehensive multi-part problems per topic.")
	_ = cmd.MarkFlagRequired("catalog")
	return cmd
}

func mathValidateCmd() *cobra.Command {
	var catalogPath string
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Check that every catalog formula parses, computes, and matches its source example.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cat, err := mathgen.LoadCatalog(catalogPath)
			if err != nil {
				return err
			}
			issues := mathgen.Validate(cat)
			fmt.Printf("%d equation(s) in catalog\n", len(cat.Equations))
			if len(issues) == 0 {
				fmt.Println("all equations valid ✓")
				return nil
			}
			fmt.Printf("%d issue(s):\n", len(issues))
			for _, is := range issues {
				fmt.Printf("  - %s\n", is)
			}
			return fmt.Errorf("%d catalog issue(s)", len(issues))
		},
	}
	cmd.Flags().StringVar(&catalogPath, "catalog", "", "Path to the equation catalog YAML (required).")
	_ = cmd.MarkFlagRequired("catalog")
	return cmd
}

func mathDrillCmd() *cobra.Command {
	var (
		catalogPath string
		module      string
		id          string
		seed        int64
		showAnswer  bool
		asJSON      bool
	)
	cmd := &cobra.Command{
		Use:   "drill",
		Short: "Pose one fresh numeric problem (the live-drill mechanism).",
		Long: `drill samples one fresh problem from the catalog and prints it, then the
worked solution. This is the same Sample+Solve path the RAG drill calls,
so it's a quick way to see the kind of question the drill generates. With
--seed 0 (default) it uses a time-seeded RNG so each invocation differs.
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cat, err := loadAndFilter(catalogPath)
			if err != nil {
				return err
			}
			eqs := cat.ByModule(module)
			if len(eqs) == 0 {
				return fmt.Errorf("no valid equations in scope")
			}
			var rng *rand.Rand
			if seed == 0 {
				rng = rand.New(rand.NewSource(time.Now().UnixNano()))
			} else {
				rng = rand.New(rand.NewSource(seed))
			}
			var eq mathgen.Equation
			if id != "" {
				e, ok := cat.Get(id)
				if !ok {
					return fmt.Errorf("no equation with id %q", id)
				}
				eq = e
			} else {
				eq = eqs[rng.Intn(len(eqs))]
			}
			p, err := eq.Generate(rng)
			if err != nil {
				return err
			}
			if asJSON {
				// Machine-readable shape for the live drill: question to pose,
				// plus the grading key (answer + tolerance + worked steps) the
				// caller keeps private until the student has answered.
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{
					"equation_id": p.EquationID,
					"name":        eq.Name,
					"topic":       eq.Topic,
					"question":    p.Question,
					"inputs":      p.Inputs,
					"answer":      p.Answer,
					"answer_text": p.AnswerText,
					"answer_unit": eq.Result.Unit,
					"steps":       p.Steps,
					"citation":    p.Citation,
				})
			}
			fmt.Printf("[%s] %s\n\n", eq.Name, p.Question)
			if showAnswer {
				fmt.Printf("Answer: %s\n\n", p.AnswerText)
				for _, s := range p.Steps {
					fmt.Println("  " + s)
				}
				if p.Citation != "" {
					fmt.Printf("\nSource: %s\n", p.Citation)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&catalogPath, "catalog", "", "Path to the equation catalog YAML (required).")
	cmd.Flags().StringVar(&module, "module", "", "Restrict to one module; empty = all.")
	cmd.Flags().StringVar(&id, "id", "", "Drill a specific equation by id (default: random).")
	cmd.Flags().Int64Var(&seed, "seed", 0, "RNG seed (0 = time-seeded, fresh each run).")
	cmd.Flags().BoolVar(&showAnswer, "answer", true, "Print the answer and worked solution after the question.")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Emit the problem + grading key as JSON (for the live drill / scripting).")
	_ = cmd.MarkFlagRequired("catalog")
	return cmd
}

func mathExtractCmd() *cobra.Command {
	var (
		lessonsFile string
		extraFile   string
		outFile     string
		module      string
		llmURL      string
		llmModel    string
		keepInvalid bool
	)
	cmd := &cobra.Command{
		Use:   "extract",
		Short: "LLM-propose a catalog from processed_lessons.json; keep only what validates.",
		Long: `extract reads a prior run's processed_lessons.json (and optionally an
extra equation-reference markdown), asks the LLM to encode every
computable equation it finds as a catalog entry (formula, variable ranges,
units, question wording, and a worked example transcribed from the
source), then runs the validation harness and keeps ONLY the entries whose
encoded formula reproduces their source example. Wrong formulas are
dropped, not shipped — "auto-extract everything computable, gated by a
correctness check."

Requires the LLM proxy running (vibe start <long_form profile>, :9000).
Review the emitted catalog before using it; extraction is a starting
point, not gospel.
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return mathextract.Run(ctx, mathextract.Config{
				LessonsFile: lessonsFile,
				ExtraFile:   extraFile,
				OutFile:     outFile,
				Module:      module,
				Program:     cfg.ProgramLabel,
				LLMURL:      llmURL,
				LLMModel:    llmModel,
				KeepInvalid: keepInvalid,
			})
		},
	}
	cmd.Flags().StringVar(&lessonsFile, "lessons", "", "Path to processed_lessons.json (required).")
	cmd.Flags().StringVar(&extraFile, "equations-ref", "", "Optional extra markdown of source equations to mine (e.g. an equation_reference.md).")
	cmd.Flags().StringVar(&outFile, "out", "", "Output catalog YAML path (required).")
	cmd.Flags().StringVar(&module, "module", "", "Module identifier to tag entries with (e.g. 'module_1').")
	cmd.Flags().StringVar(&llmURL, "llm-url", "http://127.0.0.1:9000", "LLM proxy base URL (vibe proxy).")
	cmd.Flags().StringVar(&llmModel, "llm-model", "qwen3.6-27b-mtp-q6_k", "LLM model alias.")
	cmd.Flags().BoolVar(&keepInvalid, "keep-invalid", false, "Also write entries that failed validation (into <out>.rejected.yaml) for manual repair.")
	_ = cmd.MarkFlagRequired("lessons")
	_ = cmd.MarkFlagRequired("out")
	return cmd
}
