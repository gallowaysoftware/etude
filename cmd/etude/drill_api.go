package main

// drill_api.go holds the one JSON contract every drill frontend speaks:
// the MCP tools (cmd_serve.go) and the `drill api` JSON CLI
// (cmd_drill_api.go) are thin transports over these builders, so an
// agent parses byte-identical shapes no matter which frontend serves
// it. Duplicating the shapes per transport would let them drift — the
// whole point of the three-frontends-one-coach design is that they
// can't.

import (
	"context"
	"fmt"
	"time"

	"github.com/gallowaysoftware/etude/coach"
	"github.com/gallowaysoftware/etude/grade"
	"github.com/gallowaysoftware/etude/qbank"
	"github.com/gallowaysoftware/etude/study"
)

// scheduleView is the retrieval-practice state attached to review and
// reverify items (and to the updated item returned by record). quiz_new
// items have no history yet, so they carry no schedule.
type scheduleView struct {
	Calibration    string `json:"calibration"` // blindspot | shaky | solid | new
	Streak         int    `json:"streak"`      // consecutive confident-correct retrievals
	Lapses         int    `json:"lapses"`
	LastQuality    int    `json:"last_quality"`
	LastConfidence int    `json:"last_confidence"`
	Due            string `json:"due"` // RFC3339
}

// itemView is everything a frontend needs to pose one item verbatim and
// grade against its official key. GradingKey is private until the
// learner has attempted — that rule lives in the prompt/skill, not the
// wire, because the frontend must grade against it.
type itemView struct {
	Topic      string   `json:"topic"` // record results against this exact id
	Module     string   `json:"module,omitempty"`
	Unit       string   `json:"unit,omitempty"`
	Difficulty string   `json:"difficulty,omitempty"`
	Kind       string   `json:"kind"` // official | freeform
	Question   string   `json:"question,omitempty"`
	GradingKey string   `json:"grading_key,omitempty"` // the official answer
	Points     []string `json:"points,omitempty"`      // grading_key decomposed into rubric points
	Citation   string   `json:"citation,omitempty"`
	Figures    []string `json:"figures,omitempty"`

	Schedule *scheduleView `json:"schedule,omitempty"`
	Reverify bool          `json:"reverify,omitempty"` // mastered item up for a brisk re-check
}

// nextResult is the study_next_item / `api next` response.
type nextResult struct {
	Action      coach.Action `json:"action"` // review | quiz_new | reverify | introduce_new
	Item        *itemView    `json:"item,omitempty"`
	Instruction string       `json:"instruction"` // one-line directive for the posing agent
	Counts      coach.Counts `json:"counts"`

	// introduce_new only: the bank is exhausted in scope, so there is
	// no item — steer with the weak topics instead of inventing one.
	Message    string      `json:"message,omitempty"`
	WeakTopics []coach.Gap `json:"weak_topics,omitempty"`
}

// recordResult is the study_record_result / `api record` response.
type recordResult struct {
	Item    itemView     `json:"item"` // the updated scheduling view
	Counts  coach.Counts `json:"counts"`
	Message string       `json:"message"` // what the record changed (mastered, blindspot, ...)
}

// gapsResult is the study_gaps / `api gaps` response.
type gapsResult struct {
	Gaps []coach.Gap `json:"gaps"`
}

// coverageResult is the study_coverage / `api coverage` response.
type coverageResult struct {
	Units   []coach.UnitCoverage `json:"units"` // least-mastered first
	Overall coach.UnitCoverage   `json:"overall"`
}

// nextItem builds the study_next_item response for one round.
func nextItem(d *drillDeps, module string, now time.Time) nextResult {
	n := d.Coach.Next(module, now)
	out := nextResult{Action: n.Action, Counts: n.Counts}
	switch n.Action {
	case coach.QuizNew:
		out.Item = questionView(n.Question)
		out.Instruction = "Pose this fresh official question verbatim, then collect the learner's " +
			"answer and a confidence (0-3) before revealing anything. Grade only against " +
			"grading_key, which stays private until the learner has attempted."
	case coach.Review:
		out.Item = reviewItemView(n.Item, n.Question, false)
		out.Instruction = "Re-ask this item — it was missed or shaky and is due again. Pose the " +
			"question verbatim, collect answer + confidence before revealing, grade against grading_key."
	case coach.Reverify:
		out.Item = reviewItemView(n.Item, n.Question, true)
		out.Instruction = "Mastered item up for a brisk spaced re-check — keep it short. Pose " +
			"verbatim, collect answer + confidence; a miss drops it back into rotation."
	case coach.IntroduceNew:
		out.Instruction = "The bank is exhausted in scope. Do not invent questions."
		out.Message = "No unseen official questions remain in scope. Show the weak topics and " +
			"suggest another module or wrapping up."
		out.WeakTopics = n.WeakTopics
	}
	return out
}

// recordAttempt applies one graded attempt and builds the
// study_record_result response: the updated item plus a message naming
// what changed, so the frontend can tell the learner where the item now
// stands (out of rotation, back at the front of the queue, ...).
func recordAttempt(d *drillDeps, topic, module string, quality, confidence int, note string, now time.Time) (recordResult, error) {
	it, err := d.Coach.Record(topic, module, quality, confidence, note, now)
	if err != nil {
		return recordResult{}, err
	}
	var msg string
	switch cal := it.Calibration(); {
	case it.Mastered:
		msg = fmt.Sprintf("Mastered — %q leaves rotation and returns for a spaced re-verification on %s.",
			it.Topic, it.Due.Format("2006-01-02"))
	case cal == "blindspot":
		msg = fmt.Sprintf("Blindspot (confident but wrong) — %q jumps back to the front of the queue.", it.Topic)
	default:
		msg = fmt.Sprintf("Recorded — %q is due again %s.", it.Topic, it.Due.Format(time.RFC3339))
	}
	return recordResult{
		Item:    *reviewItemView(it, bankGet(d.Bank, it.Topic), false),
		Counts:  currentCounts(d, now),
		Message: msg,
	}, nil
}

// gapsView ranks the weak spots by exam risk.
func gapsView(d *drillDeps, module string) gapsResult {
	// 10 is a briefing, not an exhaustive dump — the gaps view steers
	// the session, it isn't a data export.
	return gapsResult{Gaps: d.Coach.Gaps(10, module)}
}

// coverageView joins the bank's unit structure with the tracker.
func coverageView(d *drillDeps, module string) coverageResult {
	units, overall := d.Coach.Coverage(module)
	return coverageResult{Units: units, Overall: overall}
}

// gradeAnswer runs one grading job against the bank's official key.
// The grader belongs to whoever runs the frontend: with `etude serve
// --grader-url` that is the course owner, so grading authority doesn't
// move to the connecting client.
func gradeAnswer(ctx context.Context, d *drillDeps, g grade.Grader, questionID, learner string) (grade.Verdict, error) {
	q := bankGet(d.Bank, questionID)
	if q == nil {
		return grade.Verdict{}, fmt.Errorf("unknown question_id %q (use a topic id from study_next_item)", questionID)
	}
	return g.Grade(ctx, grade.Request{
		Question:   q.Prompt,
		Answer:     q.Answer,
		Points:     q.Points,
		Learner:    learner,
		Difficulty: string(q.Difficulty),
	})
}

// questionView relays a fresh bank question.
func questionView(q *qbank.Question) *itemView {
	if q == nil {
		return nil
	}
	return &itemView{
		Topic:      q.ID,
		Module:     q.Module,
		Unit:       q.Unit,
		Difficulty: string(q.Difficulty),
		Kind:       "official",
		Question:   q.Prompt,
		GradingKey: q.Answer,
		Points:     q.Points,
		Citation:   q.Citation,
		Figures:    q.Figures,
	}
}

// reviewItemView relays a due item: its scheduling state plus the
// official key re-attached from the bank (not from the store's memory
// of the question), so a review is graded against the real answer.
func reviewItemView(it *study.Item, q *qbank.Question, reverify bool) *itemView {
	if it == nil {
		return nil
	}
	v := &itemView{
		Topic:      it.Topic,
		Module:     it.Module,
		Unit:       it.Unit,
		Difficulty: it.Difficulty,
		Kind:       it.Kind,
		Question:   it.Question,
		Schedule: &scheduleView{
			Calibration:    it.Calibration(),
			Streak:         it.ConsecutiveCorrect,
			Lapses:         it.Lapses,
			LastQuality:    it.LastQuality,
			LastConfidence: it.LastConfidence,
			Due:            it.Due.Format(time.RFC3339),
		},
		Reverify: reverify,
	}
	if v.Kind == "" {
		v.Kind = "freeform"
	}
	if q != nil {
		v.Module = q.Module
		v.Unit = q.Unit
		v.Difficulty = string(q.Difficulty)
		v.Question = q.Prompt
		v.GradingKey = q.Answer
		v.Points = q.Points
		v.Citation = q.Citation
		v.Figures = q.Figures
	}
	return v
}

// bankGet is nil-bank-safe lookup: a course without assessment_markers
// has no bank at all.
func bankGet(b *qbank.Bank, id string) *qbank.Question {
	if b == nil {
		return nil
	}
	return b.Get(id)
}

// currentCounts re-derives the standing counts from the report view —
// the coach exposes counts only as part of Next, but record needs them
// too, and Report carries the same four numbers.
func currentCounts(d *drillDeps, now time.Time) coach.Counts {
	rep := d.Coach.Report("", now)
	return coach.Counts{
		Tracked:    rep.Tracked,
		Mastered:   rep.Mastered,
		Due:        rep.DueNow,
		Blindspots: rep.Blindspots,
	}
}
