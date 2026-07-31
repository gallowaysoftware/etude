package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/gallowaysoftware/etude/internal/coach"
	"github.com/gallowaysoftware/etude/internal/grade"
	"github.com/gallowaysoftware/etude/internal/qbank"
)

// errDone ends the session cleanly: the learner typed 'quit' or stdin
// closed. Whatever is in flight is abandoned UNRECORDED — nothing is
// written until coach.Record runs, so an interrupted item simply stays
// due and the store's scheduling resumes it next session. That is the
// whole resume story; there is no session state of the REPL's own.
var errDone = errors.New("drill session ended")

// drillCmd is the human terminal loop over the same coach policy the
// other frontends (drill api, skill, MCP) drive. It owns no scheduling
// or grading logic of its own: the coach decides what comes next, the
// configured grader judges, and this loop relays between the two.
func drillCmd() *cobra.Command {
	var (
		courseDir string
		module    string
		llmURL    string
		llmKey    string
		llmModel  string
	)
	cmd := &cobra.Command{
		Use:   "drill",
		Short: "Interactive drill loop in the terminal.",
		Long: `drill runs the human drill session: the coach picks the next item
(due blindspots first, then the diagnostic sweep of unseen questions, then
spaced gap-drilling), the question is posed VERBATIM from the course's own
assessment material, and your answer is graded point-by-point against the
official answer by the configured OpenAI-compatible endpoint.

Each round asks for a stated confidence 0-3 BEFORE anything is revealed —
that is what separates "wrong" from "confidently wrong", and the blindspot
queue depends on it. End a multi-line answer with a line containing only
'.'. Type 'quit' (or close stdin) at any prompt to stop; anything not yet
graded is simply not recorded, so the item stays due for next time.

The grader endpoint comes from --llm-url/--llm-model or ETUDE_LLM_URL /
ETUDE_LLM_API_KEY / ETUDE_LLM_MODEL. No vibe daemon is required.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			deps, err := loadCoach(courseDir)
			if err != nil {
				return err
			}
			defer deps.Close()
			// Resolve the grader up front: a missing endpoint is a
			// startup error with setup guidance, not a mid-loop surprise
			// after the learner has already typed an answer.
			g, err := resolveEndpoint(llmURL, llmKey, llmModel).grader()
			if err != nil {
				return err
			}
			return runDrill(cmd.Context(), deps, g, os.Stdin, cmd.OutOrStdout(), module)
		},
	}
	f := cmd.Flags()
	f.StringVar(&courseDir, "course", "", "Course directory (or course.yaml) to drill.")
	f.StringVar(&module, "module", "", "Scope to one module (e.g. 2, M2, module_2); empty drills everything.")
	f.StringVar(&llmURL, "llm-url", "", "OpenAI-compatible grader base URL (or ETUDE_LLM_URL).")
	f.StringVar(&llmKey, "llm-api-key", "", "Grader API key, if the endpoint wants one (or ETUDE_LLM_API_KEY).")
	f.StringVar(&llmModel, "llm-model", "", "Grader model name (or ETUDE_LLM_MODEL).")
	_ = cmd.MarkFlagRequired("course")
	return cmd
}

// reportCmd prints the standing briefing: coverage against the bank,
// mastery, and the outstanding blindspots a learner should attack next.
func reportCmd() *cobra.Command {
	var (
		courseDir string
		module    string
	)
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Coverage, mastery, and blindspots for a course.",
		Long: `report summarizes the study store against the question bank:
overall progress, a per-unit coverage table (questions / attempted /
mastered / %), and the outstanding blindspots — confident-but-wrong
items, the most dangerous quadrant — with the note from their last attempt.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			deps, err := loadCoach(courseDir)
			if err != nil {
				return err
			}
			defer deps.Close()
			return runReport(deps, cmd.OutOrStdout(), module)
		},
	}
	f := cmd.Flags()
	f.StringVar(&courseDir, "course", "", "Course directory (or course.yaml) to report on.")
	f.StringVar(&module, "module", "", "Scope to one module (e.g. 2, M2, module_2); empty reports everything.")
	_ = cmd.MarkFlagRequired("course")
	return cmd
}

// runDrill is the REPL proper, decoupled from cobra so tests can drive
// it with scripted stdin and capture stdout.
func runDrill(ctx context.Context, deps *drillDeps, g grade.Grader, in io.Reader, out io.Writer, module string) error {
	sc := bufio.NewScanner(in)
	// Answers are multi-line prose; the default 64 KiB token cap would
	// silently truncate a long one.
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)

	scope := "all modules"
	if l := coach.ModuleLabel(module); l != "" {
		scope = l
	}
	fmt.Fprintf(out, "etude drill — %s (%s). End an answer with '.'; 'quit' stops.\n",
		deps.Manifest.Title, scope)

	for {
		n := deps.Coach.Next(module, time.Now())
		if n.Question == nil {
			// introduce_new (bank exhausted in scope), or a stored
			// freeform item with no bank question to pose verbatim.
			// Either way there is nothing this loop may put on screen.
			printExhausted(out, n)
			return nil
		}
		q := n.Question

		fmt.Fprintf(out, "\n── %s · %s", q.Module, q.Unit)
		switch n.Action {
		case coach.Review:
			fmt.Fprint(out, " (review)")
		case coach.Reverify:
			fmt.Fprint(out, " (re-verify)")
		}
		fmt.Fprintf(out, " ──\n\n%s\n", q.Prompt)
		if len(q.Figures) > 0 {
			// A terminal cannot show the image; the path is the pointer.
			fmt.Fprintf(out, "\nFigures: %s\n", strings.Join(q.Figures, ", "))
		}

		answer, err := readAnswer(sc, out)
		if err != nil {
			return drillExit(err)
		}
		conf, err := readConfidence(sc, out)
		if err != nil {
			// EOF before confidence: nothing recorded, item stays due.
			return drillExit(err)
		}

		v, err := g.Grade(ctx, grade.Request{
			Question:   q.Prompt,
			Answer:     q.Answer,
			Points:     q.Points,
			Learner:    answer,
			Difficulty: string(q.Difficulty),
		})
		if err != nil {
			// A grading failure is not a learner failure: the attempt is
			// dropped (item stays due), not recorded as wrong.
			fmt.Fprintf(out, "\ngrading failed: %v\n(this attempt was not recorded; the item stays due)\n", err)
			continue
		}
		printVerdict(out, q, v)

		// The note is what 'report' and 'gaps' surface later, so lead
		// with the concrete gap, not the grade.
		note := v.Explanation
		if len(v.Misses) > 0 {
			note = "missed: " + strings.Join(v.Misses, "; ")
		}
		if _, err := deps.Coach.Record(q.ID, module, v.Quality, conf, note, time.Now()); err != nil {
			return fmt.Errorf("record attempt: %w", err)
		}
		fmt.Fprintf(out, "\n(recorded: quality %d, confidence %d)\n", v.Quality, conf)
	}
}

// drillExit maps the clean-exit sentinel to nil and passes real I/O
// errors through.
func drillExit(err error) error {
	if errors.Is(err, errDone) {
		return nil
	}
	return err
}

// readAnswer collects the multi-line recall attempt. A line holding only
// '.' ends it; 'quit' or EOF abandons the item with nothing recorded.
func readAnswer(sc *bufio.Scanner, out io.Writer) (string, error) {
	fmt.Fprintln(out, "\nYour answer (end with a line containing only '.', or 'quit' to stop):")
	var lines []string
	terminated := false
	for sc.Scan() {
		line := sc.Text()
		t := strings.TrimSpace(line)
		if t == "." {
			terminated = true
			break
		}
		if strings.EqualFold(t, "quit") {
			return "", errDone
		}
		lines = append(lines, line)
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	if !terminated {
		return "", errDone
	}
	return strings.TrimSpace(strings.Join(lines, "\n")), nil
}

// readConfidence collects the stated confidence BEFORE anything is
// revealed — confidence-before-reveal is what separates 'wrong' from
// 'confidently wrong', and the blindspot queue depends on it. Invalid
// input is reprompted, never guessed.
func readConfidence(sc *bufio.Scanner, out io.Writer) (int, error) {
	for {
		fmt.Fprint(out, "\nConfidence 0-3 (0 = wild guess, 1 = unsure, 2 = fairly sure, 3 = certain): ")
		if !sc.Scan() {
			if err := sc.Err(); err != nil {
				return 0, err
			}
			return 0, errDone
		}
		t := strings.TrimSpace(sc.Text())
		if strings.EqualFold(t, "quit") {
			return 0, errDone
		}
		if c, err := strconv.Atoi(t); err == nil && c >= 0 && c <= 3 {
			return c, nil
		}
		fmt.Fprintln(out, "Please enter 0, 1, 2, or 3 (or 'quit').")
	}
}

// printVerdict renders the reveal: the grade, the point-by-point
// hits/misses, then the official answer and citation to review.
func printVerdict(out io.Writer, q *qbank.Question, v grade.Verdict) {
	fmt.Fprintf(out, "\n── Quality %d/5 ──\n", v.Quality)
	if len(v.Hits) > 0 {
		fmt.Fprintln(out, "Hit:")
		for _, h := range v.Hits {
			fmt.Fprintf(out, "  + %s\n", h)
		}
	}
	if len(v.Misses) > 0 {
		fmt.Fprintln(out, "Missed:")
		for _, m := range v.Misses {
			fmt.Fprintf(out, "  - %s\n", m)
		}
	}
	if v.Explanation != "" {
		fmt.Fprintf(out, "\n%s\n", v.Explanation)
	}
	fmt.Fprintf(out, "\nOfficial answer:\n%s\n", q.Answer)
	if q.Citation != "" {
		fmt.Fprintf(out, "\nReview: %s\n", q.Citation)
	}
}

// printExhausted is the introduce_new view: the bank has nothing left in
// scope, so say so and steer toward the weak spots instead of inventing
// questions.
func printExhausted(out io.Writer, n coach.Next) {
	fmt.Fprintln(out, "\nNothing left to drill in scope — the bank is exhausted.")
	if len(n.WeakTopics) > 0 {
		fmt.Fprintln(out, "Weakest spots:")
		for _, g := range n.WeakTopics {
			fmt.Fprintf(out, "  - %s (%s, last quality %d)\n",
				trunc(g.Topic, 60), g.Calibration, g.LastQuality)
		}
	}
	fmt.Fprintln(out, "Try another --module, or wrap up for today.")
}

// runReport renders coach.Report + coach.Coverage as the standing
// briefing.
func runReport(deps *drillDeps, out io.Writer, module string) error {
	now := time.Now()
	rep := deps.Coach.Report(module, now)
	units, overall := deps.Coach.Coverage(module)

	scope := "all modules"
	if l := coach.ModuleLabel(module); l != "" {
		scope = l
	}
	fmt.Fprintf(out, "Report — %s (%s)\n\n", deps.Manifest.Title, scope)
	fmt.Fprintf(out, "Tracked %d · Mastered %d · Due now %d · Blindspots %d · Bank %d questions\n",
		rep.Tracked, rep.Mastered, rep.DueNow, rep.Blindspots, rep.BankSize)
	if rep.NextDue != "" {
		fmt.Fprintf(out, "Next due: %s\n", rep.NextDue)
	}

	fmt.Fprintln(out, "\nCoverage by unit (least mastered first):")
	fmt.Fprintf(out, "  %-44s %3s %4s %4s %4s\n", "UNIT", "Q", "ATT", "MST", "%")
	for _, u := range units {
		fmt.Fprintf(out, "  %-44s %3d %4d %4d %3d%%\n",
			trunc(u.Module+" · "+u.Unit, 44), u.Questions, u.Attempted, u.Mastered, u.MasteredPct)
	}
	fmt.Fprintf(out, "  %-44s %3d %4d %4d %3d%%\n",
		"TOTAL", overall.Questions, overall.Attempted, overall.Mastered, overall.MasteredPct)

	// Blindspots are the quadrant that matters: confident but wrong.
	// Report.Weak is already risk-ranked by the coach.
	var blind []coach.Gap
	for _, g := range rep.Weak {
		if g.Calibration == "blindspot" {
			blind = append(blind, g)
		}
	}
	if len(blind) > 0 {
		fmt.Fprintln(out, "\nBlindspots (confident but wrong):")
		for _, g := range blind {
			// Gap.Topic is the question ID for official items — swap it
			// for the prompt, which is what a human recognizes.
			label := g.Topic
			if deps.Bank != nil {
				if q := deps.Bank.Get(g.Topic); q != nil {
					label = q.Prompt
				}
			}
			fmt.Fprintf(out, "  - %s (last quality %d)", trunc(label, 60), g.LastQuality)
			if g.Unit != "" {
				fmt.Fprintf(out, " [%s]", g.Unit)
			}
			if g.Note != "" {
				fmt.Fprintf(out, " — %s", g.Note)
			}
			fmt.Fprintln(out)
		}
	}
	return nil
}

// trunc shortens a label for table columns, on rune boundaries.
func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
