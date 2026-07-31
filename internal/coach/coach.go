// Package coach is the drill's scheduling policy: it decides WHAT comes
// next, so every frontend (terminal REPL, MCP tools, agent skill) runs
// the identical loop. The policy is an automatic two-phase schedule
// grounded in spacing and interleaving:
//
//  1. A due blindspot (confident-but-wrong) always jumps the queue — a
//     confident error is best corrected promptly.
//  2. Breadth first: while any question in scope is unseen, serve a
//     fresh one (diagnostic sweep, least-covered unit first). Untouched
//     units are the biggest exam risk.
//  3. Sweep complete → spaced gap-drilling of the weakest due item.
//  4. Everything drilled → re-verify mastered items due for cross-day
//     relearning.
//
// What it never does is judge: questions come from the bank verbatim,
// grading happens against the bank's official answers, and arithmetic
// and scheduling stay deterministic. Judgement belongs to the grader
// the user configures; facts belong to code.
//
// Excised from the private drill coach's tool layer per
// docs/excision-checklist.md, decoupled from MCP so the REPL and the
// skill drive the same policy.
package coach

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gallowaysoftware/etude/internal/qbank"
	"github.com/gallowaysoftware/etude/internal/study"
)

// Coach couples the question bank to the study store.
type Coach struct {
	Store *study.Store
	Bank  *qbank.Bank
}

// Action names the scheduler's verdict for one round.
type Action string

const (
	Review       Action = "review"        // re-ask a due item
	QuizNew      Action = "quiz_new"      // pose a fresh bank question
	Reverify     Action = "reverify"      // brisk re-check of a mastered item
	IntroduceNew Action = "introduce_new" // bank exhausted in scope
)

// Next is the scheduler's output for one round. Question carries the
// bank item (verbatim prompt, official answer, citation, figures, rubric
// points); Item carries the scheduling state for reviews.
type Next struct {
	Action   Action
	Question *qbank.Question
	Item     *study.Item
	Counts   Counts
	// WeakTopics accompanies IntroduceNew: where to steer free-recall.
	WeakTopics []Gap
}

// Counts is the standing progress summary attached to every verdict.
type Counts struct {
	Tracked    int `json:"tracked"`
	Mastered   int `json:"mastered"`
	Due        int `json:"due"`
	Blindspots int `json:"blindspots"`
}

// Gap is one weak spot in ranked order.
type Gap struct {
	Topic       string `json:"topic"`
	Module      string `json:"module,omitempty"`
	Unit        string `json:"unit,omitempty"`
	Calibration string `json:"calibration"`
	LastQuality int    `json:"last_quality"`
	Streak      int    `json:"streak"`
	Note        string `json:"note,omitempty"`
}

// ModuleLabel reduces a loose module reference ("module_2", "M2", "2")
// to the stored "module_N" form. "" stays "" (all modules).
func ModuleLabel(m string) string {
	for _, r := range m {
		if r >= '1' && r <= '9' {
			return "module_" + string(r)
		}
	}
	return ""
}

// MigrateAliases folds any state recorded under the bank's former IDs
// into the current stable IDs. Run once at startup after extraction.
func (c *Coach) MigrateAliases() error {
	if c.Bank == nil {
		return nil
	}
	mapping := map[string]string{}
	for _, q := range c.Bank.Questions {
		for _, alias := range q.Aliases {
			mapping[alias] = q.ID
		}
	}
	return c.Store.MigrateIDs(mapping)
}

// Next picks the next thing to drill in scope (module may be "").
func (c *Coach) Next(module string, now time.Time) Next {
	modLabel := ModuleLabel(module)
	counts := c.counts(now)

	dueItem, dueAction := c.Store.NextItem(modLabel, now)
	dueReview := dueAction == "review" && dueItem != nil

	// 1. A due blindspot jumps the queue.
	if dueReview && dueItem.Calibration() == "blindspot" {
		return Next{Action: Review, Question: c.bankQuestion(dueItem), Item: dueItem, Counts: counts}
	}

	// 2. Breadth first: the diagnostic sweep.
	if q := c.pickNewQuestion(modLabel); q != nil {
		return Next{Action: QuizNew, Question: q, Counts: counts}
	}

	// 3. Sweep complete → spaced gap-drilling.
	if dueReview {
		return Next{Action: Review, Question: c.bankQuestion(dueItem), Item: dueItem, Counts: counts}
	}

	// 4. Maintenance of strong material.
	if it := c.Store.NextMasteredDue(modLabel, now); it != nil {
		return Next{Action: Reverify, Question: c.bankQuestion(it), Item: it, Counts: counts}
	}

	// 5. Nothing left in scope.
	return Next{Action: IntroduceNew, Counts: counts, WeakTopics: c.Gaps(5, modLabel)}
}

// bankQuestion re-attaches the official item for a stored topic, so a
// review is graded against the real answer, not the model's memory.
// Freeform topics have no bank entry and return nil.
func (c *Coach) bankQuestion(it *study.Item) *qbank.Question {
	if c.Bank == nil || it == nil {
		return nil
	}
	return c.Bank.Get(it.Topic)
}

// pickNewQuestion chooses an unattempted official question, preferring
// the least-drilled unit (broad coverage first) and easier tiers before
// harder ones within a unit. Returns nil when none remain in scope.
func (c *Coach) pickNewQuestion(modLabel string) *qbank.Question {
	if c.Bank == nil {
		return nil
	}
	attempted := map[string]int{}
	var candidates []*qbank.Question
	for _, q := range c.Bank.ForModule(modLabel) {
		if strings.TrimSpace(q.Answer) == "" {
			continue // need an official answer to grade against
		}
		if c.Store.Seen(q.ID) {
			attempted[q.UnitKey()]++
			continue
		}
		candidates = append(candidates, q)
	}
	if len(candidates) == 0 {
		return nil
	}
	diffRank := map[qbank.Difficulty]int{qbank.Short: 0, qbank.Progressive: 1, qbank.Long: 2}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if na, nb := attempted[a.UnitKey()], attempted[b.UnitKey()]; na != nb {
			return na < nb // least-covered unit first
		}
		if a.UnitKey() != b.UnitKey() {
			return a.UnitKey() < b.UnitKey()
		}
		if da, db := diffRank[a.Difficulty], diffRank[b.Difficulty]; da != db {
			return da < db // easier within a unit first
		}
		return a.Num < b.Num
	})
	return candidates[0]
}

// Record applies a graded attempt. For official topics the bank
// contributes the descriptive meta; anything else records as freeform.
func (c *Coach) Record(topic, module string, quality, confidence int, note string, now time.Time) (*study.Item, error) {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return nil, fmt.Errorf("topic is required")
	}
	meta := study.Meta{Module: ModuleLabel(module), Kind: "freeform"}
	if c.Bank != nil {
		if q := c.Bank.Get(topic); q != nil {
			meta = study.Meta{Module: q.Module, Unit: q.Unit, Kind: "official",
				Difficulty: string(q.Difficulty), Question: q.Prompt}
		}
	}
	return c.Store.Record(topic, meta, quality, confidence, note, now)
}

// Report is the session-briefing view.
type Report struct {
	Tracked    int      `json:"tracked"`
	Mastered   int      `json:"mastered"`
	DueNow     int      `json:"due_now"`
	Blindspots int      `json:"blindspots"`
	Strong     []string `json:"strong"`
	Weak       []Gap    `json:"weak"`
	NextDue    string   `json:"next_due,omitempty"`
	BankSize   int      `json:"bank_questions_available,omitempty"`
}

// Report summarizes the whole store plus bank size in scope.
func (c *Coach) Report(module string, now time.Time) Report {
	rep := c.Store.Report(now)
	out := Report{
		Tracked:    rep.Tracked,
		Mastered:   rep.Mastered,
		DueNow:     rep.DueNow,
		Blindspots: rep.Blindspot,
		Strong:     rep.Strong,
		Weak:       gapViews(rep.Weak),
	}
	if rep.NextDue != nil {
		out.NextDue = rep.NextDue.Format(time.RFC3339)
	}
	if c.Bank != nil {
		out.BankSize = len(c.Bank.ForModule(ModuleLabel(module)))
	}
	return out
}

// Gaps lists weak spots ranked by exam risk.
func (c *Coach) Gaps(n int, module string) []Gap {
	return gapViews(c.Store.Gaps(n, ModuleLabel(module)))
}

// UnitCoverage is one unit's drill-through tally.
type UnitCoverage struct {
	Module      string `json:"module"`
	Unit        string `json:"unit"`
	Questions   int    `json:"questions"`
	Attempted   int    `json:"attempted"`
	Mastered    int    `json:"mastered"`
	MasteredPct int    `json:"mastered_pct"`
}

// Coverage joins the bank's unit structure with the tracker, least-
// mastered units first — untouched units are the silent risk.
func (c *Coach) Coverage(module string) ([]UnitCoverage, UnitCoverage) {
	modLabel := ModuleLabel(module)
	stats := c.Store.Stats()

	byUnit := map[string]*UnitCoverage{}
	var order []string
	if c.Bank != nil {
		for _, q := range c.Bank.ForModule(modLabel) {
			key := q.UnitKey()
			uc := byUnit[key]
			if uc == nil {
				uc = &UnitCoverage{Module: q.Module, Unit: q.UnitTopic}
				byUnit[key] = uc
				order = append(order, key)
			}
			uc.Questions++
			if st, ok := stats[q.ID]; ok {
				if st.Attempted {
					uc.Attempted++
				}
				if st.Mastered {
					uc.Mastered++
				}
			}
		}
	}

	units := make([]UnitCoverage, 0, len(order))
	var overall UnitCoverage
	for _, k := range order {
		uc := byUnit[k]
		uc.MasteredPct = pct(uc.Mastered, uc.Questions)
		units = append(units, *uc)
		overall.Questions += uc.Questions
		overall.Attempted += uc.Attempted
		overall.Mastered += uc.Mastered
	}
	overall.MasteredPct = pct(overall.Mastered, overall.Questions)
	sort.SliceStable(units, func(i, j int) bool {
		return units[i].MasteredPct < units[j].MasteredPct
	})
	return units, overall
}

func (c *Coach) counts(now time.Time) Counts {
	rep := c.Store.Report(now)
	return Counts{
		Tracked:    rep.Tracked,
		Mastered:   rep.Mastered,
		Due:        rep.DueNow,
		Blindspots: rep.Blindspot,
	}
}

func gapViews(items []*study.Item) []Gap {
	out := make([]Gap, 0, len(items))
	for _, it := range items {
		out = append(out, Gap{
			Topic:       it.Topic,
			Module:      it.Module,
			Unit:        it.Unit,
			Calibration: it.Calibration(),
			LastQuality: it.LastQuality,
			Streak:      it.ConsecutiveCorrect,
			Note:        it.Note,
		})
	}
	return out
}

func pct(n, d int) int {
	if d == 0 {
		return 0
	}
	return n * 100 / d
}
