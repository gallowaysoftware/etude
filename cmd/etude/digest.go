package main

// digest.go is the etude half of the drill→tutor channel (issue #10):
// drill results steer the tutor by publishing a deterministic mastery
// summary into refrain. Every emission writes BOTH channels — set_state
// (the durable state/mastery.json the digest renders) and
// append_session_log (the interim slot that already reaches the digest
// today) — and every emission point treats memory as a side channel: a
// drill must never fail because the memory box is down.
//
// Emission points and cadence:
//   - drill REPL: once on clean exit, only if an attempt was recorded.
//   - serve: every digestEveryRecords-th study_record_result, plus the
//     trailing partial batch on shutdown. Per-record writes would spam
//     git commits in the memory repo; the 5th-record cadence is the
//     documented compromise.
//   - report --publish: explicit one-shot flush; here a publish failure
//     IS the result the user asked for, so it is returned, not swallowed.

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/gallowaysoftware/etude/internal/refrain"
)

const (
	// refrainURLEnv points at the memory service base URL (the MCP
	// endpoint lives at /mcp under it).
	refrainURLEnv = "ETUDE_REFRAIN_URL"
	// defaultRefrainURL is the deploy-time mapping of refrain's MCP
	// port on the same box.
	defaultRefrainURL = "http://127.0.0.1:14010"
	// masteryStateKey stores the document at <expert>/state/mastery.json.
	masteryStateKey = "mastery"
	// digestEveryRecords is the serve-side cadence (see file comment).
	digestEveryRecords = 5
	// dialTimeout bounds the startup probe; emitTimeout bounds each
	// publish. Memory down must cost seconds at most, never a hang.
	dialTimeout = 2 * time.Second
	emitTimeout = 10 * time.Second
	// maxWeakUnits caps the steering list: the digest tells the tutor
	// where to aim, it is not a coverage dump.
	maxWeakUnits = 5
)

// masteryState is the state/mastery.json contract refrain's digest
// renders. Keys are snake_case and the shape is fixed; refrain reads it
// defensively, so etude is the side that must not drift.
type masteryState struct {
	CourseSlug string             `json:"course_slug"`
	Updated    string             `json:"updated"`
	Coverage   masteryCoverage    `json:"coverage"`
	Due        int                `json:"due"`
	Blindspots []masteryBlindspot `json:"blindspots"`
	WeakUnits  []masteryWeakUnit  `json:"weak_units"`
}

type masteryCoverage struct {
	Questions   int `json:"questions"`
	Attempted   int `json:"attempted"`
	Mastered    int `json:"mastered"`
	MasteredPct int `json:"mastered_pct"`
}

type masteryBlindspot struct {
	Topic string `json:"topic"`
	Note  string `json:"note"`
}

type masteryWeakUnit struct {
	Unit        string `json:"unit"`
	MasteredPct int    `json:"mastered_pct"`
}

// buildMastery derives the contract document from the coach's own views
// (Report + Coverage + Gaps), so the digest can never disagree with
// what `etude report` prints. Blindspots keep the store's risk order —
// coach.Gaps is already ranked by exam risk — and use the bank prompt
// rather than the question ID, the label a tutor (or human) recognizes.
func buildMastery(deps *drillDeps, now time.Time) masteryState {
	rep := deps.Coach.Report("", now)
	units, overall := deps.Coach.Coverage("")

	st := masteryState{
		CourseSlug: deps.Manifest.Slug,
		Updated:    now.UTC().Format(time.RFC3339),
		Coverage: masteryCoverage{
			Questions:   overall.Questions,
			Attempted:   overall.Attempted,
			Mastered:    overall.Mastered,
			MasteredPct: overall.MasteredPct,
		},
		Due:        rep.DueNow,
		Blindspots: []masteryBlindspot{},
		WeakUnits:  []masteryWeakUnit{},
	}
	for _, g := range deps.Coach.Gaps(0, "") {
		if g.Calibration != "blindspot" {
			continue
		}
		topic := g.Topic
		if q := bankGet(deps.Bank, g.Topic); q != nil {
			topic = q.Prompt
		}
		st.Blindspots = append(st.Blindspots, masteryBlindspot{Topic: topic, Note: g.Note})
	}
	// Units arrive least-mastered first; a fully mastered unit is not
	// weak no matter where it sits.
	for _, u := range units {
		if u.MasteredPct >= 100 || len(st.WeakUnits) >= maxWeakUnits {
			continue
		}
		label := u.Unit
		if u.Module != "" {
			label = u.Module + " · " + u.Unit
		}
		st.WeakUnits = append(st.WeakUnits, masteryWeakUnit{Unit: label, MasteredPct: u.MasteredPct})
	}
	return st
}

// masterySummary is the one-paragraph interim log line: coverage,
// mastered, due, and blindspots in at most three lines, because the
// digest's last-log-entry slot is precious screen real estate.
func masterySummary(st masteryState) string {
	lines := []string{
		fmt.Sprintf("Drill digest: %d/%d bank questions attempted, %d mastered (%d%%), %d due now.",
			st.Coverage.Attempted, st.Coverage.Questions, st.Coverage.Mastered, st.Coverage.MasteredPct, st.Due),
	}
	if len(st.Blindspots) == 0 {
		lines = append(lines, "Blindspots: none outstanding.")
	} else {
		top := st.Blindspots
		if len(top) > 3 {
			top = top[:3]
		}
		topics := make([]string, 0, len(top))
		for _, b := range top {
			topics = append(topics, trunc(b.Topic, 60))
		}
		lines = append(lines, fmt.Sprintf("Blindspots (%d): %s.",
			len(st.Blindspots), strings.Join(topics, "; ")))
	}
	if len(st.WeakUnits) > 0 {
		units := make([]string, 0, len(st.WeakUnits))
		for _, u := range st.WeakUnits {
			units = append(units, fmt.Sprintf("%s (%d%%)", trunc(u.Unit, 40), u.MasteredPct))
		}
		lines = append(lines, "Weakest units: "+strings.Join(units, ", ")+".")
	}
	return strings.Join(lines, "\n")
}

// refrainBase resolves the memory endpoint: env override, else the
// same-box default.
func refrainBase() string {
	if u := os.Getenv(refrainURLEnv); u != "" {
		return u
	}
	return defaultRefrainURL
}

// connectRefrain probes the memory endpoint once at startup. A failed
// probe costs exactly one stderr note and yields a nil Emitter, after
// which every emission point silently skips — a drill must never fail
// (or hang) because memory is down.
func connectRefrain(ctx context.Context, errW io.Writer, expert string) *refrain.Emitter {
	base := refrainBase()
	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	c, err := refrain.Dial(ctx, base)
	if err != nil {
		fmt.Fprintf(errW, "etude: refrain memory unreachable at %s; mastery digests disabled this session (%v)\n", base, err)
		return nil
	}
	return refrain.NewEmitter(c, expert)
}

// emitDigest publishes the current mastery digest from an ambient
// emission point, swallowing failures after one note. It runs on a
// fresh context because shutdown-time callers hold an already-cancelled
// command context.
func emitDigest(em *refrain.Emitter, deps *drillDeps, errW io.Writer) {
	if !em.Enabled() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), emitTimeout)
	defer cancel()
	if err := publishDigest(ctx, em, deps); err != nil {
		fmt.Fprintf(errW, "etude: mastery digest not published: %v\n", err)
	}
}

// publishDigest builds and emits one digest. The error is returned for
// the explicit `report --publish` path, where publishing IS the
// request; ambient callers use emitDigest instead.
func publishDigest(ctx context.Context, em *refrain.Emitter, deps *drillDeps) error {
	st := buildMastery(deps, time.Now())
	return em.Emit(ctx, masteryStateKey, st, masterySummary(st))
}
