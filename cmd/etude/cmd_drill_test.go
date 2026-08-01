package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gallowaysoftware/etude/coach"
	"github.com/gallowaysoftware/etude/course"
	"github.com/gallowaysoftware/etude/grade"
	"github.com/gallowaysoftware/etude/qbank"
	"github.com/gallowaysoftware/etude/study"
)

// fixtureDrillCourse mirrors internal/coach's saq fixture (unit Alpha
// with two questions, unit Beta with one) so the sweep order is
// predictable: Alpha Q1 first, then Beta Q1 (least-covered unit wins).
func fixtureDrillCourse(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	manifest := `version: 1
slug: fixture
title: Fixture Course
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
	lessons := map[string]string{
		"Module_1/Lesson_01_Alpha_SAQ": `# Alpha SAQ

## Self-Assessment Questions

1. \* Alpha question one?
2. \*\* Alpha question two?

## Suggested Response - Q1

Response:

Alpha answer one.

## Suggested Response - Q2

Response:

Alpha answer two.
`,
		"Module_1/Lesson_02_Beta_SAQ": `# Beta SAQ

## Self-Assessment Questions

1. \* Beta question one?

## Suggested Response - Q1

Response:

Beta answer one.
`,
	}
	for rel, content := range lessons {
		dir := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "lesson.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// newDrillDeps builds drillDeps directly rather than via loadCoach: the
// store takes a single-writer lock on its file, and a second loadCoach
// against the same course would deadlock the test.
func newDrillDeps(t *testing.T) *drillDeps {
	t.Helper()
	root := fixtureDrillCourse(t)
	m, err := course.Load(root)
	if err != nil {
		t.Fatalf("course.Load: %v", err)
	}
	b, err := qbank.Extract(m)
	if err != nil {
		t.Fatalf("qbank.Extract: %v", err)
	}
	if b.Len() != 3 {
		t.Fatalf("fixture bank should hold 3 questions, got %d", b.Len())
	}
	s, err := study.NewStore(filepath.Join(t.TempDir(), "study.json"))
	if err != nil {
		t.Fatalf("study.NewStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return &drillDeps{Manifest: m, Bank: b, Coach: &coach.Coach{Store: s, Bank: b}}
}

// bankQuestion finds the fixture question by its prompt text.
func bankQuestion(t *testing.T, b *qbank.Bank, prompt string) *qbank.Question {
	t.Helper()
	for _, q := range b.Questions {
		if q.Prompt == prompt {
			return q
		}
	}
	t.Fatalf("bank has no question %q", prompt)
	return nil
}

// recordingGrader records the requests it was asked to grade and returns
// a canned verdict, so the test asserts exactly what the REPL sent.
type recordingGrader struct {
	requests []grade.Request
	verdict  grade.Verdict
	err      error
}

func (f *recordingGrader) Grade(_ context.Context, req grade.Request) (grade.Verdict, error) {
	f.requests = append(f.requests, req)
	return f.verdict, f.err
}

// storeItems decodes the on-disk study store, the ground truth for what
// a session recorded.
func storeItems(t *testing.T, s *study.Store) map[string]study.Item {
	t.Helper()
	data, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatalf("read study store: %v", err)
	}
	var f struct {
		Items map[string]study.Item `json:"items"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("decode study store: %v", err)
	}
	return f.Items
}

func TestDrillSession(t *testing.T) {
	deps := newDrillDeps(t)
	fg := &recordingGrader{verdict: grade.Verdict{
		Quality:     4,
		Hits:        []string{"alpha point"},
		Misses:      []string{"other point"},
		Explanation: "close; missed other point",
	}}
	// One full round: answer, terminator, two invalid confidences, a
	// valid one, verdict — then the next question is posed and 'quit'
	// ends the session.
	in := strings.NewReader("alpha answer one attempt\n.\n9\nnope\n2\nquit\n")
	var out bytes.Buffer
	if err := runDrill(context.Background(), deps, fg, in, &out, ""); err != nil {
		t.Fatalf("runDrill: %v", err)
	}
	s := out.String()

	q1 := bankQuestion(t, deps.Bank, "Alpha question one?")

	// The prompt is posed verbatim, with its module/unit line.
	if !strings.Contains(s, "Alpha question one?") {
		t.Fatalf("verbatim prompt missing from output:\n%s", s)
	}
	if !strings.Contains(s, q1.Module) || !strings.Contains(s, q1.Unit) {
		t.Fatalf("module/unit line missing from output:\n%s", s)
	}

	// Confidence is collected BEFORE anything is revealed.
	iPrompt := strings.Index(s, "Alpha question one?")
	iConf := strings.Index(s, "Confidence 0-3")
	iReveal := strings.Index(s, "Quality 4/5")
	if iPrompt < 0 || iConf < 0 || iReveal < 0 || iPrompt >= iConf || iConf >= iReveal {
		t.Fatalf("expected prompt < confidence < reveal order (got %d, %d, %d):\n%s",
			iPrompt, iConf, iReveal, s)
	}
	// Invalid confidence is reprompted, not guessed.
	if n := strings.Count(s, "Please enter 0, 1, 2, or 3"); n != 2 {
		t.Fatalf("expected 2 confidence reprompts, got %d:\n%s", n, s)
	}

	// The verdict is rendered: hits, misses, explanation, official
	// answer, citation.
	for _, want := range []string{
		"+ alpha point",
		"- other point",
		"close; missed other point",
		"Official answer:",
		"Alpha answer one.",
		q1.Citation,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("verdict output missing %q:\n%s", want, s)
		}
	}

	// The grader saw the verbatim prompt, the official answer and
	// rubric points, and the learner's typed answer.
	if len(fg.requests) != 1 {
		t.Fatalf("expected 1 grading request, got %d", len(fg.requests))
	}
	req := fg.requests[0]
	if req.Question != q1.Prompt || req.Answer != q1.Answer {
		t.Fatalf("grading request not built from the bank item: %+v", req)
	}
	if req.Learner != "alpha answer one attempt" {
		t.Fatalf("learner answer mismatch: %q", req.Learner)
	}
	if strings.Join(req.Points, "|") != strings.Join(q1.Points, "|") {
		t.Fatalf("rubric points mismatch: %v vs %v", req.Points, q1.Points)
	}

	// Record was applied: the store holds exactly one item with the
	// graded quality and the (final, valid) stated confidence.
	items := storeItems(t, deps.Coach.Store)
	if len(items) != 1 {
		t.Fatalf("expected 1 stored item, got %d: %v", len(items), items)
	}
	for _, it := range items {
		if it.LastQuality != 4 || it.LastConfidence != 2 {
			t.Fatalf("stored quality/confidence = %d/%d, want 4/2", it.LastQuality, it.LastConfidence)
		}
		if it.Topic != q1.ID {
			t.Fatalf("stored under topic %q, want bank ID %q", it.Topic, q1.ID)
		}
	}

	// The second Next reflects the record: the sweep moved to the
	// least-covered unit (Beta), whose prompt was posed before quit.
	if !strings.Contains(s, "Beta question one?") {
		t.Fatalf("second round should pose Beta's fresh question:\n%s", s)
	}
	if !deps.Coach.Store.Seen(q1.ID) {
		t.Fatalf("coach store should have seen %q", q1.ID)
	}

	// quit exits 0 — runDrill returning nil above is the assertion.
}

func TestDrillEOFBeforeConfidenceRecordsNothing(t *testing.T) {
	deps := newDrillDeps(t)
	fg := &recordingGrader{verdict: grade.Verdict{Quality: 5}}
	// Answer and terminator arrive, then stdin closes before the
	// confidence prompt is answered: the item must NOT be recorded.
	in := strings.NewReader("half an answer\n.\n")
	var out bytes.Buffer
	if err := runDrill(context.Background(), deps, fg, in, &out, ""); err != nil {
		t.Fatalf("runDrill: %v", err)
	}
	if len(fg.requests) != 0 {
		t.Fatalf("grader must not run before confidence is stated, got %d requests", len(fg.requests))
	}
	if rep := deps.Coach.Report("", time.Now()); rep.Tracked != 0 {
		t.Fatalf("mid-item EOF recorded %d items, want 0", rep.Tracked)
	}
	// The item simply stays due: the next session poses it again.
	n := deps.Coach.Next("", time.Now())
	if n.Question == nil || n.Question.Prompt != "Alpha question one?" {
		t.Fatalf("interrupted item should still be served, got %+v", n)
	}
}

func TestDrillEOFDuringAnswerRecordsNothing(t *testing.T) {
	deps := newDrillDeps(t)
	fg := &recordingGrader{verdict: grade.Verdict{Quality: 5}}
	// Stdin closes mid-answer, before even the '.' terminator.
	in := strings.NewReader("half an answer\n")
	var out bytes.Buffer
	if err := runDrill(context.Background(), deps, fg, in, &out, ""); err != nil {
		t.Fatalf("runDrill: %v", err)
	}
	if rep := deps.Coach.Report("", time.Now()); rep.Tracked != 0 {
		t.Fatalf("mid-answer EOF recorded %d items, want 0", rep.Tracked)
	}
}

func TestReportRenders(t *testing.T) {
	deps := newDrillDeps(t)
	q1 := bankQuestion(t, deps.Bank, "Alpha question one?")
	// A confident-but-wrong attempt is the blindspot the report exists
	// to surface.
	if _, err := deps.Coach.Record(q1.ID, "", 1, 3, "missed: everything", time.Now()); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runReport(deps, &out, ""); err != nil {
		t.Fatalf("runReport: %v", err)
	}
	s := out.String()
	for _, want := range []string{
		"Fixture Course",
		"Tracked 1",
		"Blindspots 1",
		"Bank 3 questions",
		"Alpha", // coverage table row
		"TOTAL",
		"Blindspots (confident but wrong):",
		"Alpha question one?",
		"missed: everything",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("report missing %q:\n%s", want, s)
		}
	}
}
