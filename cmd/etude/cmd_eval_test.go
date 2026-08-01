package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gallowaysoftware/etude/grade"
)

// fakeGrader returns scripted verdicts keyed by the question prompt.
type fakeGrader struct {
	byPrompt map[string]grade.Verdict
}

func (f *fakeGrader) Grade(_ context.Context, req grade.Request) (grade.Verdict, error) {
	v, ok := f.byPrompt[req.Question]
	if !ok {
		return grade.Verdict{}, fmt.Errorf("no scripted verdict for prompt %q", req.Question)
	}
	return v, nil
}

// evalFixtureCourse writes a one-module saq course with three questions
// so golden entries have real bank IDs to resolve against.
func evalFixtureCourse(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	manifest := `version: 1
slug: evalfixture
title: Eval Fixture
subject: fixture subject
program: the fixture program
persona: a fixture lecturer
assessment: end-of-unit self-check questions
source: .
modules:
  - num: 1
    topic: only module
assessment_markers:
  preset: saq
`
	if err := os.WriteFile(filepath.Join(root, "course.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "Module_1", "Lesson_01_Alpha_SAQ")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lesson := `# Alpha SAQ

## Self-Assessment Questions

1. \* Alpha question one?
2. \* Alpha question two?
3. \* Alpha question three?

## Suggested Response - Q1

Response:

Alpha answer one.

## Suggested Response - Q2

Response:

Alpha answer two.

## Suggested Response - Q3

Response:

Alpha answer three.
`
	if err := os.WriteFile(filepath.Join(dir, "lesson.md"), []byte(lesson), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// evalFixture loads the fixture course's bank and writes a golden set
// covering all three questions with the given expected qualities,
// returning the entries and the bank prompts keyed by question ID.
func evalFixture(t *testing.T, expected []int) ([]evalEntry, map[string]string) {
	t.Helper()
	if len(expected) != 3 {
		t.Fatalf("fixture has exactly 3 questions, got %d expectations", len(expected))
	}
	root := evalFixtureCourse(t)
	deps, err := loadCoach(root)
	if err != nil {
		t.Fatalf("loadCoach: %v", err)
	}
	t.Cleanup(func() { deps.Close() })
	if deps.Bank.Len() != 3 {
		t.Fatalf("fixture bank should hold 3 questions, got %d", deps.Bank.Len())
	}

	goldenPath := filepath.Join(t.TempDir(), "golden.jsonl")
	var sb strings.Builder
	for i, q := range deps.Bank.Questions {
		line, err := json.Marshal(goldenEntry{
			QuestionID:      q.ID,
			LearnerAnswer:   "a learner attempt",
			ExpectedQuality: expected[i],
			Rationale:       "scripted",
		})
		if err != nil {
			t.Fatal(err)
		}
		sb.Write(line)
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(goldenPath, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := loadGolden(goldenPath, deps.Bank)
	if err != nil {
		t.Fatalf("loadGolden: %v", err)
	}
	prompts := map[string]string{}
	for _, q := range deps.Bank.Questions {
		prompts[q.ID] = q.Prompt
	}
	return entries, prompts
}

func TestEvalSummaryMath(t *testing.T) {
	entries, prompts := evalFixture(t, []int{4, 3, 5})
	// Over-score q1 by one, match q2, under-score q3 by one: exact 1/3,
	// within-1 3/3, signed errors +1, 0, -1 -> mean 0.
	g := &fakeGrader{byPrompt: map[string]grade.Verdict{}}
	for i, e := range entries {
		g.byPrompt[prompts[e.QuestionID]] = grade.Verdict{
			Quality:     []int{5, 3, 4}[i],
			Explanation: fmt.Sprintf("scripted verdict %d", i),
		}
	}

	var buf bytes.Buffer
	if err := runGradingEval(context.Background(), &buf, g, entries, true); err != nil {
		t.Fatalf("runGradingEval: %v", err)
	}
	var rep evalReport
	if err := json.Unmarshal(buf.Bytes(), &rep); err != nil {
		t.Fatalf("--json output must parse: %v\n%s", err, buf.String())
	}
	s := rep.Summary
	if s.Count != 3 || s.ExactMatches != 1 || s.WithinOne != 3 {
		t.Errorf("counts = %+v, want count 3, exact 1, within-1 3", s)
	}
	if s.ExactRate != 1.0/3.0 {
		t.Errorf("exact rate = %v, want 1/3", s.ExactRate)
	}
	if s.WithinOneRate != 1.0 {
		t.Errorf("within-1 rate = %v, want 1.0", s.WithinOneRate)
	}
	if s.MeanSignedError != 0 {
		t.Errorf("mean signed error = %v, want 0", s.MeanSignedError)
	}
	if len(rep.Entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(rep.Entries))
	}
	for _, e := range rep.Entries {
		if e.Delta != e.Actual-e.Expected {
			t.Errorf("entry %s delta %d != actual-expected", e.QuestionID, e.Delta)
		}
	}
	// Starting thresholds ride along so a consumer can recompute the
	// verdict after tightening them.
	if rep.Thresholds.ExactMin != evalExactMin || rep.Thresholds.Within1Min != evalWithin1Min || rep.Thresholds.MSEMax != evalMSEMax {
		t.Errorf("thresholds = %+v, want the package constants", rep.Thresholds)
	}
}

func TestEvalLeniencySignConvention(t *testing.T) {
	entries, prompts := evalFixture(t, []int{2, 2, 2})
	// A grader scoring everything 4 against expected 2s is lenient:
	// mean signed error must be POSITIVE and the verdict must fail
	// (mse > +0.5 ceiling) despite perfect within-1 calibration.
	g := &fakeGrader{byPrompt: map[string]grade.Verdict{}}
	for _, e := range entries {
		g.byPrompt[prompts[e.QuestionID]] = grade.Verdict{Quality: 4, Explanation: "fine"}
	}
	sum := summarize(runForSummary(t, g, entries))
	if sum.MeanSignedError != 2.0 {
		t.Errorf("lenient grader: mean signed error = %v, want +2.0 (positive = lenient)", sum.MeanSignedError)
	}
	if sum.Passed {
		t.Error("a systematically lenient grader must fail the verdict")
	}

	// And the mirror: under-scoring is negative (strict).
	strict := &fakeGrader{byPrompt: map[string]grade.Verdict{}}
	for _, e := range entries {
		strict.byPrompt[prompts[e.QuestionID]] = grade.Verdict{Quality: 1, Explanation: "harsh"}
	}
	sum = summarize(runForSummary(t, strict, entries))
	if sum.MeanSignedError != -1.0 {
		t.Errorf("strict grader: mean signed error = %v, want -1.0 (negative = strict)", sum.MeanSignedError)
	}

	// The human output must spell the convention out — a user should
	// never have to guess which sign is the dangerous one.
	var buf bytes.Buffer
	if err := runGradingEval(context.Background(), &buf, g, entries, false); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "LENIENT") || !strings.Contains(out, "+2.00") {
		t.Errorf("human output must state the sign convention and signed value:\n%s", out)
	}
	if !strings.Contains(out, "verdict: FAIL") {
		t.Errorf("lenient grader should print verdict FAIL:\n%s", out)
	}
}

func runForSummary(t *testing.T, g grade.Grader, entries []evalEntry) []evalResult {
	t.Helper()
	var buf bytes.Buffer
	if err := runGradingEval(context.Background(), &buf, g, entries, true); err != nil {
		t.Fatalf("runGradingEval: %v", err)
	}
	var rep evalReport
	if err := json.Unmarshal(buf.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	return rep.Entries
}

func TestEvalUnknownQuestionIDNamesLine(t *testing.T) {
	root := evalFixtureCourse(t)
	deps, err := loadCoach(root)
	if err != nil {
		t.Fatalf("loadCoach: %v", err)
	}
	defer deps.Close()

	goldenPath := filepath.Join(t.TempDir(), "golden.jsonl")
	valid, _ := json.Marshal(goldenEntry{
		QuestionID:      deps.Bank.Questions[0].ID,
		LearnerAnswer:   "ok",
		ExpectedQuality: 4,
		Rationale:       "fine",
	})
	body := string(valid) + "\n" +
		`{"question_id": "module_1.nope.q99", "learner_answer": "x", "expected_quality": 3, "rationale": "stale"}` + "\n"
	if err := os.WriteFile(goldenPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = loadGolden(goldenPath, deps.Bank)
	if err == nil {
		t.Fatal("unknown question_id must be an error")
	}
	if !strings.Contains(err.Error(), "line 2") || !strings.Contains(err.Error(), "module_1.nope.q99") {
		t.Errorf("error must name the line and the bad id, got: %v", err)
	}
}

func TestEvalGoldenValidation(t *testing.T) {
	root := evalFixtureCourse(t)
	deps, err := loadCoach(root)
	if err != nil {
		t.Fatalf("loadCoach: %v", err)
	}
	defer deps.Close()

	write := func(body string) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "golden.jsonl")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	if _, err := loadGolden(write("not json\n"), deps.Bank); err == nil || !strings.Contains(err.Error(), "line 1") {
		t.Errorf("malformed JSON must name its line, got: %v", err)
	}
	if _, err := loadGolden(write(`{"question_id": "x", "learner_answer": "a", "expected_quality": 9}`+"\n"), deps.Bank); err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Errorf("out-of-range expected_quality must be rejected, got: %v", err)
	}
	if _, err := loadGolden(write("\n\n"), deps.Bank); err == nil || !strings.Contains(err.Error(), "no golden entries") {
		t.Errorf("empty golden set must be rejected, got: %v", err)
	}
}
