package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/gallowaysoftware/etude/grade"
	"github.com/gallowaysoftware/etude/internal/refrain"
)

// fakeRefrainSink records emissions in memory; the MCP round trip
// itself is covered in internal/refrain's tests.
type fakeRefrainSink struct {
	states []stateWrite
	logs   []logWrite
}

type stateWrite struct {
	expert string
	key    string
	value  masteryState
}

type logWrite struct {
	expert  string
	summary string
}

func (f *fakeRefrainSink) SetState(_ context.Context, expert, key string, value any) error {
	st, ok := value.(masteryState)
	if !ok {
		panic("SetState value is not a masteryState")
	}
	f.states = append(f.states, stateWrite{expert, key, st})
	return nil
}

func (f *fakeRefrainSink) AppendSessionLog(_ context.Context, expert, summary string) error {
	f.logs = append(f.logs, logWrite{expert, summary})
	return nil
}

func (f *fakeRefrainSink) emissions() int { return len(f.states) + len(f.logs) }

// fixtureEmitter wires a fake sink for the fixture course's expert.
func fixtureEmitter(sink *fakeRefrainSink) *refrain.Emitter {
	return refrain.NewEmitter(sink, "fixture")
}

// recordBlindspots stores two confident-but-wrong attempts, Beta's
// recorded twice so its risk score outranks Alpha's — the digest's
// blindspot order must follow the store's risk order.
func recordBlindspots(t *testing.T, deps *drillDeps) {
	t.Helper()
	alpha := bankQuestion(t, deps.Bank, "Alpha question one?")
	beta := bankQuestion(t, deps.Bank, "Beta question one?")
	now := time.Now()
	if _, err := deps.Coach.Record(alpha.ID, "", 1, 3, "missed: alpha point", now); err != nil {
		t.Fatal(err)
	}
	if _, err := deps.Coach.Record(beta.ID, "", 1, 3, "missed: beta point", now); err != nil {
		t.Fatal(err)
	}
	if _, err := deps.Coach.Record(beta.ID, "", 0, 3, "missed: beta point again", now); err != nil {
		t.Fatal(err)
	}
}

func TestBuildMasteryMatchesContract(t *testing.T) {
	deps := newDrillDeps(t)
	recordBlindspots(t, deps)
	// Master Alpha Q2 (two consecutive correct), so coverage has one
	// mastered question and one unit at 50%.
	alpha2 := bankQuestion(t, deps.Bank, "Alpha question two?")
	now := time.Now()
	for range 2 {
		if _, err := deps.Coach.Record(alpha2.ID, "", 5, 2, "", now); err != nil {
			t.Fatal(err)
		}
	}

	st := buildMastery(deps, now)

	// The contract shape, asserted on the wire form: exactly these
	// keys, no more, no fewer.
	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	wantKeys := []string{"course_slug", "updated", "coverage", "due", "blindspots", "weak_units"}
	if len(doc) != len(wantKeys) {
		t.Fatalf("mastery.json keys = %v, want exactly %v", keysOf(doc), wantKeys)
	}
	for _, k := range wantKeys {
		if _, ok := doc[k]; !ok {
			t.Fatalf("mastery.json missing key %q (has %v)", k, keysOf(doc))
		}
	}
	cov, ok := doc["coverage"].(map[string]any)
	if !ok {
		t.Fatalf("coverage is not an object: %v", doc["coverage"])
	}
	for _, k := range []string{"questions", "attempted", "mastered", "mastered_pct"} {
		if _, ok := cov[k]; !ok {
			t.Fatalf("coverage missing key %q (has %v)", k, cov)
		}
	}

	if st.CourseSlug != "fixture" {
		t.Errorf("course_slug = %q, want the manifest slug", st.CourseSlug)
	}
	if _, err := time.Parse(time.RFC3339, st.Updated); err != nil {
		t.Errorf("updated %q is not RFC3339: %v", st.Updated, err)
	}
	if st.Coverage.Questions != 3 || st.Coverage.Attempted != 3 || st.Coverage.Mastered != 1 {
		t.Errorf("coverage = %+v, want 3/3 attempted, 1 mastered", st.Coverage)
	}
	if st.Coverage.MasteredPct != 33 {
		t.Errorf("mastered_pct = %d, want 33", st.Coverage.MasteredPct)
	}

	// Blindspots keep the store's risk order: Beta (two lapses) before
	// Alpha (one), labeled by prompt, not question ID.
	if len(st.Blindspots) != 2 {
		t.Fatalf("blindspots = %v, want 2", st.Blindspots)
	}
	if st.Blindspots[0].Topic != "Beta question one?" || st.Blindspots[1].Topic != "Alpha question one?" {
		t.Errorf("blindspot order = %q, %q; want Beta (higher risk) first",
			st.Blindspots[0].Topic, st.Blindspots[1].Topic)
	}
	if st.Blindspots[0].Note != "missed: beta point again" {
		t.Errorf("blindspot note = %q, want the last attempt's note", st.Blindspots[0].Note)
	}

	// Weak units: least-mastered first, fully mastered excluded.
	if len(st.WeakUnits) != 2 {
		t.Fatalf("weak_units = %v, want both units (none fully mastered)", st.WeakUnits)
	}
	if st.WeakUnits[0].MasteredPct > st.WeakUnits[1].MasteredPct {
		t.Errorf("weak_units not ascending by mastered_pct: %v", st.WeakUnits)
	}
	if st.WeakUnits[0].Unit != "module_1 · Beta" || st.WeakUnits[0].MasteredPct != 0 {
		t.Errorf("weakest unit = %+v, want module_1 · Beta at 0%%", st.WeakUnits[0])
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestMasterySummaryCoversContractInThreeLines(t *testing.T) {
	deps := newDrillDeps(t)
	recordBlindspots(t, deps)
	st := buildMastery(deps, time.Now())

	s := masterySummary(st)
	if n := strings.Count(s, "\n") + 1; n > 3 {
		t.Fatalf("summary is %d lines, max 3:\n%s", n, s)
	}
	for _, want := range []string{
		"2/3 bank questions attempted",
		"0 mastered (0%)",
		"due",
		"Blindspots (2)",
		"Beta question one?",
		"Weakest units:",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("summary missing %q:\n%s", want, s)
		}
	}

	// An empty store still summarizes cleanly.
	empty := masterySummary(buildMastery(newDrillDeps(t), time.Now()))
	if !strings.Contains(empty, "Blindspots: none") || strings.Count(empty, "\n") > 2 {
		t.Errorf("empty-store summary wrong:\n%s", empty)
	}
}

func TestDrillEmitsOnceOnCleanExitWithAttempts(t *testing.T) {
	deps := newDrillDeps(t)
	sink := &fakeRefrainSink{}
	fg := &recordingGrader{verdict: grade.Verdict{Quality: 4}}
	// One full round (answer, terminator, confidence), then quit at the
	// next prompt: one recorded attempt, one clean exit.
	in := strings.NewReader("alpha answer one attempt\n.\n2\nquit\n")
	var out bytes.Buffer
	if err := runDrill(context.Background(), deps, fg, in, &out, "", fixtureEmitter(sink)); err != nil {
		t.Fatalf("runDrill: %v", err)
	}

	if len(sink.states) != 1 || len(sink.logs) != 1 {
		t.Fatalf("expected exactly 1 set_state + 1 append_session_log, got %d/%d",
			len(sink.states), len(sink.logs))
	}
	sw := sink.states[0]
	if sw.expert != "fixture" || sw.key != "mastery" {
		t.Errorf("set_state addressed %q/%q, want fixture/mastery", sw.expert, sw.key)
	}
	if sw.value.CourseSlug != "fixture" || sw.value.Coverage.Attempted != 1 {
		t.Errorf("state value = %+v, want the fixture course with 1 attempt", sw.value)
	}
	if lw := sink.logs[0]; lw.expert != "fixture" || !strings.Contains(lw.summary, "1/3 bank questions attempted") {
		t.Errorf("session log = %+v, want the fixture summary", lw)
	}
}

func TestDrillNoEmissionWithoutAttempts(t *testing.T) {
	deps := newDrillDeps(t)
	sink := &fakeRefrainSink{}
	fg := &recordingGrader{verdict: grade.Verdict{Quality: 4}}
	// Immediate quit: nothing recorded, so nothing to steer the tutor
	// with — the REPL must not emit an empty digest.
	in := strings.NewReader("quit\n")
	var out bytes.Buffer
	if err := runDrill(context.Background(), deps, fg, in, &out, "", fixtureEmitter(sink)); err != nil {
		t.Fatalf("runDrill: %v", err)
	}
	if sink.emissions() != 0 {
		t.Fatalf("quit without attempts emitted %d writes, want 0", sink.emissions())
	}
}

func TestDrillUnreachableRefrainStillCompletes(t *testing.T) {
	// Reserve-then-release a loopback port so nothing listens on it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := "http://" + ln.Addr().String()
	ln.Close()
	t.Setenv("ETUDE_REFRAIN_URL", dead)

	var notes bytes.Buffer
	em := connectRefrain(context.Background(), &notes, "fixture")
	if em.Enabled() {
		t.Fatal("unreachable refrain must yield a disabled emitter")
	}
	if n := strings.Count(notes.String(), "\n"); n != 1 {
		t.Fatalf("startup probe should leave exactly one stderr note, got %d:\n%s", n, notes.String())
	}
	if !strings.Contains(notes.String(), dead) {
		t.Fatalf("note should name the endpoint:\n%s", notes.String())
	}

	// A full session runs to clean exit with memory down: no hang, no
	// error, emission silently skipped.
	deps := newDrillDeps(t)
	fg := &recordingGrader{verdict: grade.Verdict{Quality: 4}}
	in := strings.NewReader("alpha answer one attempt\n.\n2\nquit\n")
	var out bytes.Buffer
	if err := runDrill(context.Background(), deps, fg, in, &out, "", em); err != nil {
		t.Fatalf("runDrill with memory down: %v", err)
	}
	if rep := deps.Coach.Report("", time.Now()); rep.Tracked != 1 {
		t.Fatalf("session should have recorded 1 attempt, got %d", rep.Tracked)
	}
}

func TestServeCadenceEmitsOnFifthRecord(t *testing.T) {
	deps := newDrillDeps(t)
	sink := &fakeRefrainSink{}
	ds := &drillServer{deps: deps, now: time.Now, em: fixtureEmitter(sink)}
	q := bankQuestion(t, deps.Bank, "Alpha question one?")

	record := func() {
		t.Helper()
		_, _, err := ds.studyRecord(context.Background(), nil, recordArgs{
			Topic: q.ID, Quality: float64(4), Confidence: float64(2),
		})
		if err != nil {
			t.Fatalf("studyRecord: %v", err)
		}
	}

	for range 4 {
		record()
	}
	if sink.emissions() != 0 {
		t.Fatalf("4 records triggered %d writes; the cadence is every 5th", sink.emissions())
	}
	record()
	if len(sink.states) != 1 || len(sink.logs) != 1 {
		t.Fatalf("5th record should emit both channels once, got %d/%d", len(sink.states), len(sink.logs))
	}

	// Shutdown right on the cadence boundary: nothing pending, no
	// duplicate commit.
	ds.flushOnShutdown()
	if sink.emissions() != 2 {
		t.Fatalf("flush with nothing pending emitted again (%d writes)", sink.emissions())
	}

	// A trailing partial batch flushes once at shutdown.
	record()
	record()
	ds.flushOnShutdown()
	if sink.emissions() != 4 {
		t.Fatalf("shutdown should flush the 2 pending records (%d writes)", sink.emissions())
	}
	ds.flushOnShutdown()
	if sink.emissions() != 4 {
		t.Fatalf("second shutdown flush must be a no-op (%d writes)", sink.emissions())
	}
}

func TestServeEmissionFailureNeverFailsTheRecord(t *testing.T) {
	deps := newDrillDeps(t)
	// A nil emitter (memory down) exercises the silent-skip path; the
	// swallow-on-error path is covered by emitDigest's design, and the
	// MCP error surfacing by internal/refrain's tests.
	ds := &drillServer{deps: deps, now: time.Now}
	q := bankQuestion(t, deps.Bank, "Alpha question one?")
	for range 5 {
		if _, _, err := ds.studyRecord(context.Background(), nil, recordArgs{
			Topic: q.ID, Quality: float64(4), Confidence: float64(2),
		}); err != nil {
			t.Fatalf("studyRecord with memory down: %v", err)
		}
	}
	ds.flushOnShutdown()
	if rep := deps.Coach.Report("", time.Now()); rep.Tracked != 1 {
		t.Fatalf("records must land regardless of emission, tracked %d", rep.Tracked)
	}
}

func TestReportPublishAlwaysEmits(t *testing.T) {
	deps := newDrillDeps(t)
	sink := &fakeRefrainSink{}
	// Zero attempts: the explicit flush emits anyway — the user asked
	// for the current state to be published, whatever it contains.
	if err := publishDigest(context.Background(), fixtureEmitter(sink), deps); err != nil {
		t.Fatalf("publishDigest: %v", err)
	}
	if len(sink.states) != 1 || len(sink.logs) != 1 {
		t.Fatalf("--publish must emit both channels, got %d/%d", len(sink.states), len(sink.logs))
	}
	if got := sink.states[0].value.Coverage; got.Questions != 3 || got.Attempted != 0 {
		t.Errorf("published coverage = %+v, want the fresh-store zeros", got)
	}
}
