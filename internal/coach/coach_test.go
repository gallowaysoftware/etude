package coach

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gallowaysoftware/etude/internal/course"
	"github.com/gallowaysoftware/etude/internal/qbank"
	"github.com/gallowaysoftware/etude/internal/study"
)

var t0 = time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC)

// fixtureCourse writes a two-unit saq course: unit A has two questions,
// unit B has one, so coverage-spreading has something to spread over.
func fixtureCourse(t *testing.T) string {
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

func newCoach(t *testing.T) *Coach {
	t.Helper()
	root := fixtureCourse(t)
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
	return &Coach{Store: s, Bank: b}
}

func TestSweepServesFreshQuestionsFirst(t *testing.T) {
	c := newCoach(t)
	// A non-blindspot due review exists (unsure-correct requeues fast),
	// but unseen questions must still win: breadth before re-drilling.
	if _, err := c.Record("freeform shaky", "", 4, 1, "", t0); err != nil {
		t.Fatal(err)
	}
	n := c.Next("", t0.Add(10*time.Minute))
	if n.Action != QuizNew {
		t.Fatalf("sweep should prefer a fresh question, got %q", n.Action)
	}
	if n.Question == nil || n.Question.Prompt == "" {
		t.Fatalf("quiz_new must carry the bank question: %+v", n)
	}
}

func TestBlindspotJumpsQueue(t *testing.T) {
	c := newCoach(t)
	n := c.Next("", t0)
	if n.Action != QuizNew {
		t.Fatalf("fresh bank should quiz_new, got %q", n.Action)
	}
	// Answer it confidently wrong → blindspot, due in 7 minutes.
	if _, err := c.Record(n.Question.ID, "", 1, 3, "confident miss", t0); err != nil {
		t.Fatal(err)
	}
	n = c.Next("", t0.Add(8*time.Minute))
	if n.Action != Review {
		t.Fatalf("due blindspot must jump the queue over the sweep, got %q", n.Action)
	}
	if n.Question == nil || n.Question.Answer == "" {
		t.Fatal("a blindspot review must re-attach the official answer key")
	}
}

func TestSweepSpreadsAcrossUnits(t *testing.T) {
	c := newCoach(t)
	// First pick: any unit; after answering, the sweep must prefer the
	// unit with nothing attempted yet.
	first := c.Next("", t0)
	if first.Action != QuizNew {
		t.Fatalf("got %q", first.Action)
	}
	c.Record(first.Question.ID, "", 5, 3, "", t0)
	second := c.Next("", t0.Add(time.Minute))
	if second.Action != QuizNew {
		t.Fatalf("sweep should continue while questions are unseen, got %q", second.Action)
	}
	if second.Question.UnitKey() == first.Question.UnitKey() {
		t.Fatalf("sweep should spread to the least-covered unit, stayed on %q", first.Question.UnitKey())
	}
}

func TestSweepCompleteThenGapDrillThenReverify(t *testing.T) {
	c := newCoach(t)
	// Drain the sweep, mastering everything.
	for {
		n := c.Next("", t0)
		if n.Action != QuizNew {
			t.Fatalf("unexpected %q mid-sweep", n.Action)
		}
		c.Record(n.Question.ID, "", 5, 3, "", t0)
		c.Record(n.Question.ID, "", 5, 3, "", t0.Add(time.Hour)) // criterion: mastered
		if c.Report("", t0).Mastered == 3 {
			break
		}
	}
	// Same session: nothing due, mastered items reverify tomorrow —
	// the bank is exhausted, so introduce_new.
	n := c.Next("", t0.Add(2*time.Hour))
	if n.Action != IntroduceNew {
		t.Fatalf("exhausted scope should introduce_new, got %q", n.Action)
	}
	// Next day: mastered items come up for re-verification.
	n = c.Next("", t0.Add(26*time.Hour))
	if n.Action != Reverify {
		t.Fatalf("mastered items should reverify next day, got %q", n.Action)
	}
	if n.Question == nil {
		t.Fatal("reverify should carry the official question")
	}
}

func TestGapDrillAfterSweepWithMiss(t *testing.T) {
	c := newCoach(t)
	// Answer every question once, missing one (unsure) so the sweep
	// completes with a due review outstanding.
	var missed string
	for c.Report("", t0).Tracked < 3 {
		n := c.Next("", t0)
		if n.Action != QuizNew {
			t.Fatalf("unexpected %q mid-sweep", n.Action)
		}
		if missed == "" {
			missed = n.Question.ID
			c.Record(n.Question.ID, "", 1, 0, "", t0) // lapse, requeue 7 min
		} else {
			c.Record(n.Question.ID, "", 5, 3, "", t0)
		}
	}
	n := c.Next("", t0.Add(8*time.Minute))
	if n.Action != Review {
		t.Fatalf("post-sweep due item should be a review, got %q", n.Action)
	}
	if n.Item.Topic != missed {
		t.Fatalf("gap drill should resurface the missed item, got %q", n.Item.Topic)
	}
}

func TestCoverageAndReport(t *testing.T) {
	c := newCoach(t)
	n := c.Next("", t0)
	c.Record(n.Question.ID, "", 5, 3, "", t0)

	units, overall := c.Coverage("")
	if overall.Questions != 3 || overall.Attempted != 1 || overall.Mastered != 0 {
		t.Fatalf("overall coverage wrong: %+v", overall)
	}
	if len(units) != 2 {
		t.Fatalf("expected 2 units, got %+v", units)
	}
	// Least-mastered unit first: both at 0%, order stable; after
	// mastering one unit's questions it must sort last.
	for _, q := range c.Bank.Questions {
		if q.UnitTopic == n.Question.UnitTopic {
			c.Record(q.ID, "", 5, 3, "", t0)
			c.Record(q.ID, "", 5, 3, "", t0.Add(time.Hour))
		}
	}
	units, _ = c.Coverage("")
	if units[0].MasteredPct != 0 {
		t.Fatalf("least-mastered unit should sort first: %+v", units)
	}

	rep := c.Report("", t0)
	if rep.BankSize != 3 || rep.Tracked == 0 {
		t.Fatalf("report wrong: %+v", rep)
	}
}

func TestRecordEnrichesOfficialMeta(t *testing.T) {
	c := newCoach(t)
	n := c.Next("", t0)
	it, err := c.Record(n.Question.ID, "", 4, 2, "close", t0)
	if err != nil {
		t.Fatal(err)
	}
	if it.Kind != "official" || it.Module != "module_1" || it.Question == "" {
		t.Fatalf("bank meta not attached: %+v", it)
	}
	// A freeform topic stays freeform.
	it, err = c.Record("some concept", "2", 3, 1, "", t0)
	if err != nil {
		t.Fatal(err)
	}
	if it.Kind != "freeform" || it.Module != "module_2" {
		t.Fatalf("freeform meta wrong: %+v", it)
	}
}

func TestMigrateAliases(t *testing.T) {
	c := newCoach(t)
	n := c.Next("", t0)
	legacy := n.Question.Aliases[0]
	// State lands under the legacy ID (as a pre-stable-ID store would
	// hold it), then the alias migration folds it forward.
	if _, err := c.Store.Record(legacy, study.Meta{Module: "module_1"}, 1, 3, "blindspot", t0); err != nil {
		t.Fatal(err)
	}
	if err := c.MigrateAliases(); err != nil {
		t.Fatalf("MigrateAliases: %v", err)
	}
	if c.Store.Seen(legacy) {
		t.Fatal("legacy key should be gone")
	}
	if !c.Store.Seen(n.Question.ID) {
		t.Fatal("state should live under the stable ID")
	}
	// And the migrated blindspot must steer scheduling immediately.
	next := c.Next("", t0.Add(8*time.Minute))
	if next.Action != Review || next.Item.Topic != n.Question.ID {
		t.Fatalf("migrated blindspot should drive the next round, got %+v", next)
	}
}
