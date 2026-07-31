package study

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(filepath.Join(t.TempDir(), "progress.json"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// rec is a test helper: record a confident-graded attempt with module meta.
func rec(t *testing.T, s *Store, topic, module string, quality, confidence int, note string, now time.Time) *Item {
	t.Helper()
	it, err := s.Record(topic, Meta{Module: module}, quality, confidence, note, now)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	return it
}

func TestMasteryAfterCriterionConfidentReps(t *testing.T) {
	s := newTestStore(t)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	var it *Item
	for range criterion {
		it = rec(t, s, "distillation cuts", "2", 5, 3, "", now)
		now = now.Add(time.Hour) // simulate later in the session / next day
	}
	if !it.Mastered {
		t.Fatalf("expected mastery after %d confident-correct reps, got reps=%d streak=%d", criterion, it.Reps, it.ConsecutiveCorrect)
	}
	if it.Module != "2" {
		t.Fatalf("module not retained: %q", it.Module)
	}
}

func TestCorrectButNotConfidentDoesNotMaster(t *testing.T) {
	s := newTestStore(t)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	var it *Item
	for range criterion + 1 {
		it = rec(t, s, "reflux ratio", "2", 5, 1, "", now) // correct but low confidence
		now = now.Add(time.Hour)
	}
	if it.Mastered {
		t.Fatal("low-confidence correct answers should not graduate a topic")
	}
}

func TestLapseResetsStreakAndMastery(t *testing.T) {
	s := newTestStore(t)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	for range criterion {
		rec(t, s, "steam latent heat", "3", 5, 3, "", now)
		now = now.Add(time.Hour)
	}
	it := rec(t, s, "steam latent heat", "3", 1, 0, "blanked on units", now)
	if it.Mastered {
		t.Fatal("lapse should clear mastery")
	}
	if it.ConsecutiveCorrect != 0 {
		t.Fatalf("lapse should reset streak to 0, got %d", it.ConsecutiveCorrect)
	}
	if it.Lapses != 1 {
		t.Fatalf("lapse count should be 1, got %d", it.Lapses)
	}
	if it.Note != "blanked on units" {
		t.Fatalf("note not stored: %q", it.Note)
	}
}

func TestBlindspotIsHighestRisk(t *testing.T) {
	s := newTestStore(t)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// A confident-wrong blindspot and a merely-shaky topic.
	rec(t, s, "confident wrong", "3", 1, 3, "thought I knew it", now) // blindspot
	rec(t, s, "just unsure", "3", 3, 0, "needed a nudge", now)        // shaky

	gaps := s.Gaps(5, "")
	if len(gaps) < 2 || gaps[0].Topic != "confident wrong" {
		t.Fatalf("expected blindspot ranked first, got %+v", topicsOf(gaps))
	}
	if gaps[0].Calibration() != "blindspot" {
		t.Fatalf("expected blindspot calibration, got %q", gaps[0].Calibration())
	}
}

func TestNextItemPrefersBlindspotThenWeakestDue(t *testing.T) {
	s := newTestStore(t)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	rec(t, s, "easy topic", "1", 5, 3, "", now)
	rec(t, s, "blindspot topic", "1", 1, 3, "", now)
	later := now.Add(48 * time.Hour) // everything overdue

	it, action := s.NextItem("", later)
	if action != "review" {
		t.Fatalf("expected review, got %q", action)
	}
	if it.Topic != "blindspot topic" {
		t.Fatalf("expected blindspot first, got %q", it.Topic)
	}
}

func TestWithinSessionRequeue(t *testing.T) {
	s := newTestStore(t)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	it := rec(t, s, "missed topic", "1", 1, 2, "", now)
	// A miss requeues this session but with a gap, so other items interleave
	// before it returns — spaced relearning, not immediate massed repetition.
	if it.Due.Sub(now) != 7*time.Minute {
		t.Fatalf("missed item should requeue at 7 minutes, got %s", it.Due.Sub(now))
	}
	// Right-but-unsure requeues at 20, then 40 as the streak grows.
	it = rec(t, s, "missed topic", "1", 4, 1, "", now.Add(10*time.Minute))
	if got := it.Due.Sub(now.Add(10 * time.Minute)); got != 20*time.Minute {
		t.Fatalf("streak 1 should requeue at 20 minutes, got %s", got)
	}
	it = rec(t, s, "missed topic", "1", 4, 1, "", now.Add(40*time.Minute))
	if got := it.Due.Sub(now.Add(40 * time.Minute)); got != 40*time.Minute {
		t.Fatalf("streak 2 should requeue at 40 minutes, got %s", got)
	}
}

func TestNextItemIntroduceNewWhenNothingDue(t *testing.T) {
	s := newTestStore(t)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	rec(t, s, "only topic", "1", 5, 3, "", now) // due ~24h out (confident-correct)

	_, action := s.NextItem("", now) // same instant: not due yet
	if action != "introduce_new" {
		t.Fatalf("expected introduce_new when nothing due, got %q", action)
	}
}

func TestMasteredItemResurfacesNextDayForReverification(t *testing.T) {
	s := newTestStore(t)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	for range criterion {
		rec(t, s, "ester formation", "1", 5, 3, "", now)
		now = now.Add(time.Hour)
	}
	// Mastered: it must NOT be due again within the same session...
	if it := s.NextMasteredDue("", now); it != nil {
		t.Fatalf("mastered item should not be due for re-verification same-session, got %q", it.Topic)
	}
	// ...and it must NOT appear in active rotation either.
	if _, action := s.NextItem("", now); action != "introduce_new" {
		t.Fatalf("mastered item should leave active rotation, got %q", action)
	}
	// ...but it IS due for re-verification the next day.
	next := now.Add(25 * time.Hour)
	it := s.NextMasteredDue("", next)
	if it == nil || it.Topic != "ester formation" {
		t.Fatalf("expected mastered item due for re-verification next day, got %+v", it)
	}
}

func TestMasteredReverificationIntervalsExpand(t *testing.T) {
	s := newTestStore(t)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// Master the item, then keep passing re-verifications: 1, 2, 4 days.
	rec(t, s, "mash ph", "2", 5, 3, "", now)
	it := rec(t, s, "mash ph", "2", 5, 3, "", now.Add(time.Hour))
	if got := it.Due.Sub(now.Add(time.Hour)); got != 24*time.Hour {
		t.Fatalf("first re-verification should be 1 day out, got %s", got)
	}
	it = rec(t, s, "mash ph", "2", 5, 3, "", now.Add(26*time.Hour))
	if got := it.Due.Sub(now.Add(26 * time.Hour)); got != 48*time.Hour {
		t.Fatalf("second re-verification should be 2 days out, got %s", got)
	}
	it = rec(t, s, "mash ph", "2", 5, 3, "", now.Add(76*time.Hour))
	if got := it.Due.Sub(now.Add(76 * time.Hour)); got != 96*time.Hour {
		t.Fatalf("third re-verification should be 4 days out, got %s", got)
	}
}

func TestMissedReverificationUnmasters(t *testing.T) {
	s := newTestStore(t)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	for range criterion {
		rec(t, s, "mash conversion", "2", 5, 3, "", now)
		now = now.Add(time.Hour)
	}
	// Next day, re-verification fails.
	next := now.Add(25 * time.Hour)
	it := rec(t, s, "mash conversion", "2", 1, 1, "blanked", next)
	if it.Mastered {
		t.Fatal("a missed re-verification should un-master the item")
	}
	if it.ConsecutiveCorrect != 0 {
		t.Fatalf("miss should reset streak, got %d", it.ConsecutiveCorrect)
	}
	// It returns to active within-session rotation, not the mastered queue.
	if m := s.NextMasteredDue("", next.Add(2*time.Hour)); m != nil {
		t.Fatalf("un-mastered item should not be in the mastered queue, got %q", m.Topic)
	}
	if got, action := s.NextItem("", next.Add(10*time.Minute)); action != "review" || got.Topic != "mash conversion" {
		t.Fatalf("un-mastered item should be back in active rotation, got action=%q item=%+v", action, got)
	}
}

func TestPersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "progress.json")
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	s1, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	s1.Record("yeast biochemistry", Meta{Module: "1", Kind: "freeform"}, 4, 1, "ester pathway hazy", now)
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := NewStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	rep := s2.Report(now)
	if rep.Tracked != 1 {
		t.Fatalf("expected 1 tracked after reload, got %d", rep.Tracked)
	}
	if len(rep.Weak) != 1 || rep.Weak[0].Note != "ester pathway hazy" {
		t.Fatalf("note did not survive reload: %+v", rep.Weak)
	}
}

func TestSingleWriterLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "progress.json")
	s1, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s1.Close()

	if _, err := NewStore(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("second opener should fail with ErrLocked, got %v", err)
	}
	// After Close the lock is released and the next opener succeeds.
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s2, err := NewStore(path)
	if err != nil {
		t.Fatalf("open after close should succeed: %v", err)
	}
	s2.Close()
}

func TestMigrateIDs(t *testing.T) {
	s := newTestStore(t)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// State recorded under a legacy position-based ID.
	rec(t, s, "module_2.lesson-5-x-saq.q3", "module_2", 1, 3, "confident miss", now)
	rec(t, s, "module_2.lesson-5-x-saq.q7", "module_2", 5, 3, "", now)

	err := s.MigrateIDs(map[string]string{
		"module_2.lesson-5-x-saq.q3": "module_2.lesson-5-x-saq.q7f3a9c2e",
		"module_2.lesson-5-x-saq.q9": "module_2.lesson-5-x-saq.q11111111", // no state: no-op
	})
	if err != nil {
		t.Fatalf("MigrateIDs: %v", err)
	}
	if s.Seen("module_2.lesson-5-x-saq.q3") {
		t.Error("old key should be gone after migration")
	}
	if !s.Seen("module_2.lesson-5-x-saq.q7f3a9c2e") {
		t.Fatal("migrated item missing under new ID")
	}
	gaps := s.Gaps(5, "module_2")
	if len(gaps) != 2 {
		t.Fatalf("expected 2 items after migration, got %d", len(gaps))
	}

	// Collision: state exists under BOTH old and new IDs — the new ID's
	// scheduling state wins, but the old item's history merges in.
	rec(t, s, "module_2.lesson-5-x-saq.q7", "module_2", 1, 0, "lapsed under old id", now.Add(time.Hour))
	err = s.MigrateIDs(map[string]string{
		"module_2.lesson-5-x-saq.q7": "module_2.lesson-5-x-saq.q7f3a9c2e",
	})
	if err != nil {
		t.Fatalf("MigrateIDs (collision): %v", err)
	}
	if s.Seen("module_2.lesson-5-x-saq.q7") {
		t.Error("colliding old key should be gone")
	}
	stats := s.Stats()
	st, ok := stats["module_2.lesson-5-x-saq.q7f3a9c2e"]
	if !ok {
		t.Fatal("collision target missing")
	}
	if st.Calibration != "blindspot" {
		t.Errorf("target's own scheduling state should win, got calibration %q", st.Calibration)
	}
}

func TestNumberKindSeparation(t *testing.T) {
	s := newTestStore(t)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	// one ordinary item (wrong, so it stays due) and one number card (also wrong)
	s.Record("module_2.lesson-5-x.q1", Meta{Module: "module_2", Kind: "official"}, 1, 0, "", now)
	s.Record("module_2.num.0001", Meta{Module: "module_2", Kind: "number"}, 1, 0, "", now)
	later := now.Add(time.Hour)
	// NextItem must NOT surface the number card.
	it, act := s.NextItem("module_2", later)
	if act != "review" || it == nil || it.Kind == "number" {
		t.Fatalf("NextItem should return the ordinary item, not a number card: %+v (%s)", it, act)
	}
	// NextNumberItem must return ONLY the number card.
	nit, nact := s.NextNumberItem("module_2", later)
	if nact != "review" || nit == nil || nit.Kind != "number" {
		t.Fatalf("NextNumberItem should return the number card, got %+v (%s)", nit, nact)
	}
}

func topicsOf(items []*Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Topic
	}
	return out
}
