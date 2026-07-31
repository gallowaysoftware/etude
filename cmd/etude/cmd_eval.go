package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/gallowaysoftware/etude/internal/grade"
	"github.com/gallowaysoftware/etude/internal/qbank"
)

// Pass thresholds for the grader qualification verdict. These are
// starting points, not truth: they exist to catch a grader that is
// grossly miscalibrated (especially systematically lenient, which
// silently marks blindspots mastered — the failure the whole drill
// exists to prevent). Tighten them as the golden set grows.
const (
	evalExactMin   = 0.50 // exact-match rate floor
	evalWithin1Min = 0.90 // within-±1 rate floor
	evalMSEMax     = 0.50 // mean signed error ceiling; positive = lenient

	// A golden set of ~50 against a local model is minutes of serial
	// waiting; a small pool keeps that bearable without hammering the
	// endpoint.
	evalWorkers = 4
)

// goldenEntry is one line of the golden set (JSONL). question_id
// resolves against the extracted bank, which supplies the prompt,
// official answer, and rubric points for the grading request.
type goldenEntry struct {
	QuestionID      string `json:"question_id"`
	LearnerAnswer   string `json:"learner_answer"`
	ExpectedQuality int    `json:"expected_quality"`
	Rationale       string `json:"rationale"`
}

// evalEntry pairs a golden line with the bank question it resolves to.
type evalEntry struct {
	goldenEntry
	Question *qbank.Question
}

// evalResult is one graded comparison.
type evalResult struct {
	QuestionID string `json:"question_id"`
	Expected   int    `json:"expected"`
	Actual     int    `json:"actual"`
	Delta      int    `json:"delta"` // actual - expected; positive = grader more lenient
	Note       string `json:"note"`
}

// evalSummary aggregates a completed run.
type evalSummary struct {
	Count           int     `json:"count"`
	ExactMatches    int     `json:"exact_matches"`
	ExactRate       float64 `json:"exact_rate"`
	WithinOne       int     `json:"within_one"`
	WithinOneRate   float64 `json:"within_one_rate"`
	MeanSignedError float64 `json:"mean_signed_error"` // positive = LENIENT, negative = STRICT
	Passed          bool    `json:"passed"`
}

// evalReport is the machine-readable (--json) output shape.
type evalReport struct {
	Entries    []evalResult `json:"entries"`
	Summary    evalSummary  `json:"summary"`
	Thresholds struct {
		ExactMin   float64 `json:"exact_min"`
		Within1Min float64 `json:"within_one_min"`
		MSEMax     float64 `json:"mean_signed_error_max"`
	} `json:"thresholds"`
}

func evalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Evaluation harnesses (grading).",
		Long: `eval qualifies components of the drill against known-good fixtures
before they are trusted with weeks of study.`,
	}
	cmd.AddCommand(evalGradingCmd())
	return cmd
}

func evalGradingCmd() *cobra.Command {
	var (
		coursePath string
		goldenPath string
		llmURL     string
		llmAPIKey  string
		llmModel   string
		jsonOut    bool
	)
	cmd := &cobra.Command{
		Use:   "grading",
		Short: "Score a candidate grader model against golden answer/grade pairs.",
		Long: `grading runs each golden answer/grade pair through the candidate
grader and reports calibration: per-entry deltas, exact-match and
within-±1 rates, and the mean signed error (actual minus expected).
Positive mean signed error means the grader is systematically LENIENT —
it marks shakier recall as better than it is, silently recording
blindspots as mastered. That is the failure the whole drill exists to
prevent, so leniency fails the verdict faster than strictness does.

A completed run always exits 0: a failing grader is a result, not an
error. Non-zero exits are for usage, IO, or endpoint failures.

Verdict thresholds are starting points, not truth (exact >= 50%,
within-1 >= 90%, mean signed error <= +0.5) — tighten them as the
golden set grows.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			deps, err := loadCoach(coursePath)
			if err != nil {
				return err
			}
			defer deps.Close()
			if deps.Bank == nil || deps.Bank.Len() == 0 {
				return fmt.Errorf("course %s yields an empty question bank: golden question_ids have nothing to resolve against", coursePath)
			}
			entries, err := loadGolden(goldenPath, deps.Bank)
			if err != nil {
				return err
			}
			g, err := resolveEndpoint(llmURL, llmAPIKey, llmModel).grader()
			if err != nil {
				return err
			}
			return runGradingEval(cmd.Context(), cmd.OutOrStdout(), g, entries, jsonOut)
		},
	}
	cmd.Flags().StringVar(&coursePath, "course", "", "Course directory or course.yaml (required).")
	cmd.Flags().StringVar(&goldenPath, "golden", "", "Golden set JSONL: question_id, learner_answer, expected_quality, rationale (required).")
	cmd.Flags().StringVar(&llmURL, "llm-url", "", "OpenAI-compatible endpoint for the candidate grader (or ETUDE_LLM_URL).")
	cmd.Flags().StringVar(&llmAPIKey, "llm-api-key", "", "API key for the grader endpoint (or ETUDE_LLM_API_KEY).")
	cmd.Flags().StringVar(&llmModel, "llm-model", "", "Model to qualify as grader (or ETUDE_LLM_MODEL).")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON instead of the table.")
	_ = cmd.MarkFlagRequired("course")
	_ = cmd.MarkFlagRequired("golden")
	return cmd
}

// loadGolden parses the JSONL golden set and resolves each question_id
// against the bank. Every malformed line is fatal and names its line
// number: a silently skipped golden entry biases the calibration the
// user is about to trust.
func loadGolden(path string, bank *qbank.Bank) ([]evalEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var entries []evalEntry
	lineNo := 0
	for line := range strings.Lines(string(data)) {
		lineNo++
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var g goldenEntry
		if err := json.Unmarshal([]byte(line), &g); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", path, lineNo, err)
		}
		if g.QuestionID == "" {
			return nil, fmt.Errorf("%s line %d: question_id is required", path, lineNo)
		}
		if g.ExpectedQuality < 0 || g.ExpectedQuality > 5 {
			return nil, fmt.Errorf("%s line %d: expected_quality %d out of range 0-5", path, lineNo, g.ExpectedQuality)
		}
		q := bank.Get(g.QuestionID)
		if q == nil {
			return nil, fmt.Errorf("%s line %d: question_id %q not found in the course's question bank", path, lineNo, g.QuestionID)
		}
		entries = append(entries, evalEntry{goldenEntry: g, Question: q})
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%s: no golden entries", path)
	}
	return entries, nil
}

// runGradingEval grades every entry with the candidate grader and
// renders the comparison. A completed run returns nil regardless of the
// verdict — a failing grader is a result, not an error.
func runGradingEval(ctx context.Context, w io.Writer, g grade.Grader, entries []evalEntry, jsonOut bool) error {
	results := make([]evalResult, len(entries))
	errs := make([]error, len(entries))

	jobs := make(chan int)
	var wg sync.WaitGroup
	wg.Add(evalWorkers)
	for range evalWorkers {
		go func() {
			defer wg.Done()
			for i := range jobs {
				e := entries[i]
				v, err := g.Grade(ctx, grade.Request{
					Question:   e.Question.Prompt,
					Answer:     e.Question.Answer,
					Points:     e.Question.Points,
					Learner:    e.LearnerAnswer,
					Difficulty: string(e.Question.Difficulty),
				})
				if err != nil {
					errs[i] = err
					continue
				}
				results[i] = evalResult{
					QuestionID: e.QuestionID,
					Expected:   e.ExpectedQuality,
					Actual:     v.Quality,
					Delta:      v.Quality - e.ExpectedQuality,
					Note:       oneLine(v.Explanation, 100),
				}
			}
		}()
	}
	for i := range entries {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			return fmt.Errorf("grade %s: %w", entries[i].QuestionID, err)
		}
	}

	sum := summarize(results)
	if jsonOut {
		return writeEvalJSON(w, results, sum)
	}
	writeEvalTable(w, results, sum)
	return nil
}

// summarize aggregates the per-entry deltas. The signed error keeps its
// sign: positive means the grader over-scores (lenient), negative means
// it under-scores (strict).
func summarize(results []evalResult) evalSummary {
	var s evalSummary
	s.Count = len(results)
	signedSum := 0
	for _, r := range results {
		switch d := r.Delta; d {
		case 0:
			s.ExactMatches++
			s.WithinOne++
		case 1, -1:
			s.WithinOne++
		}
		signedSum += r.Delta
	}
	s.ExactRate = float64(s.ExactMatches) / float64(s.Count)
	s.WithinOneRate = float64(s.WithinOne) / float64(s.Count)
	s.MeanSignedError = float64(signedSum) / float64(s.Count)
	s.Passed = s.ExactRate >= evalExactMin && s.WithinOneRate >= evalWithin1Min && s.MeanSignedError <= evalMSEMax
	return s
}

func writeEvalJSON(w io.Writer, results []evalResult, sum evalSummary) error {
	var rep evalReport
	rep.Entries = results
	rep.Summary = sum
	rep.Thresholds.ExactMin = evalExactMin
	rep.Thresholds.Within1Min = evalWithin1Min
	rep.Thresholds.MSEMax = evalMSEMax
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}

func writeEvalTable(w io.Writer, results []evalResult, sum evalSummary) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "QUESTION ID\tEXP\tACT\t|D|\tNOTE")
	for _, r := range results {
		d := r.Delta
		if d < 0 {
			d = -d
		}
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%s\n", r.QuestionID, r.Expected, r.Actual, d, r.Note)
	}
	tw.Flush()

	fmt.Fprintf(w, "\nexact match:       %d/%d (%.1f%%)\n", sum.ExactMatches, sum.Count, sum.ExactRate*100)
	fmt.Fprintf(w, "within +/-1:       %d/%d (%.1f%%)\n", sum.WithinOne, sum.Count, sum.WithinOneRate*100)
	fmt.Fprintf(w, "mean signed error: %+.2f  (positive = LENIENT, negative = STRICT)\n", sum.MeanSignedError)
	if sum.MeanSignedError > 0 {
		fmt.Fprintln(w, "  leniency over-scores shaky recall and silently marks blindspots mastered —\n  the failure the drill exists to prevent; treat any positive drift with suspicion.")
	}

	verdict := "FAIL"
	if sum.Passed {
		verdict = "PASS"
	}
	fmt.Fprintf(w, "\nverdict: %s (starting thresholds: exact >= %.0f%%, within-1 >= %.0f%%, mean signed error <= %+.1f)\n",
		verdict, evalExactMin*100, evalWithin1Min*100, evalMSEMax)
	if !sum.Passed {
		fmt.Fprintln(w, "a completed run is exit 0 either way — a failing grader is a result, not an error")
	}
}

// oneLine flattens whitespace and caps a note for table display.
func oneLine(s string, maxRunes int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > maxRunes {
		return string(r[:maxRunes-3]) + "..."
	}
	return s
}
