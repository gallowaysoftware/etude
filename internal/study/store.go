// Package study implements the spaced retrieval-practice tracker behind
// the drill coach. It is tuned for a short, high-stakes run-up to an
// exam (days, not months), so it favours *successive relearning to a
// criterion* with *within-session interleaved requeue* over
// calendar-based SM-2 spacing: a missed or shaky item comes back a few
// questions later in the same sitting, and only graduates out once it's
// been retrieved correctly, confidently, to criterion. A graduated item
// is not gone for good — it is re-verified a day later (and at expanding
// intervals after), because relearning across days, not within one
// sitting, is what makes it stick by exam day; a missed re-verification
// drops it back into active rotation. It also records *confidence* with
// every attempt so the coach can surface the dangerous quadrant — high
// confidence + wrong — which is where exam failures hide.
//
// State persists as a single JSON file in the course directory so
// progress is greppable, diffable, and backed up with the course.
//
// Excised from the private drill coach per docs/excision-checklist.md
// (source recorded in the introducing commit). The scheduler semantics
// are preserved verbatim — they are tuned and validated, and
// re-deriving them would be a downgrade. The single-writer file lock
// and the stable-ID migration are new.
package study

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const (
	// criterion is the number of consecutive confident-correct retrievals a
	// topic needs before it graduates out of rotation (successive relearning).
	criterion = 2
	// goodQuality is the minimum grade that counts as a correct retrieval.
	goodQuality = 4
	// confidentEnough is the minimum confidence (0-3) required, alongside a
	// correct answer, for a rep to count toward mastery — you don't "know" what
	// you only got right hesitantly.
	confidentEnough = 2
)

// requeueMinutes maps a topic's current consecutive-correct streak to how long
// to wait before showing it again *within a session*. A missed item still comes
// back this session for relearning, but with enough of a gap that a few OTHER
// questions land in between — spaced/interleaved relearning beats massed
// re-asking, both for retention and for not grinding the learner on the same
// item back-to-back. Each confident-correct rep then pushes it further out.
// Day-to-day, any non-mastered item is already overdue by the next session, so
// it resurfaces for relearning automatically.
func requeueMinutes(consecutiveCorrect int, quality int) int {
	if quality < goodQuality {
		return 7 // missed/shaky — relearn this session, but interleave first
	}
	switch consecutiveCorrect {
	case 1:
		return 20
	default:
		return 40
	}
}

// masteredReviewDays sets the cross-day re-verification interval for a mastered
// topic. Successive relearning consolidates across days, not within one sitting,
// so a just-mastered item is re-checked the next day, then at expanding
// intervals. A missed re-verification un-masters it (see Record), dropping it
// back into active within-session rotation.
func masteredReviewDays(consecutiveCorrect int) int {
	switch {
	case consecutiveCorrect <= criterion:
		return 1
	case consecutiveCorrect == criterion+1:
		return 2
	default:
		return 4
	}
}

// Review is one graded attempt at a topic.
type Review struct {
	Time       time.Time `json:"time"`
	Quality    int       `json:"quality"`
	Confidence int       `json:"confidence"`
	Note       string    `json:"note,omitempty"`
}

// Item is the retrieval-practice state for a single topic. A topic is keyed by
// a stable string — either an official question ID (e.g.
// "module_2.lesson-5-distillation-theory-saq.q7f3a9c2e") or a free-form
// concept name.
type Item struct {
	Topic              string    `json:"topic"`
	Module             string    `json:"module,omitempty"`
	Unit               string    `json:"unit,omitempty"`       // human unit/lesson label
	Kind               string    `json:"kind,omitempty"`       // "official" | "freeform" | "number" | "math"
	Difficulty         string    `json:"difficulty,omitempty"` // short | progressive | long
	Question           string    `json:"question,omitempty"`   // the prompt as posed (official items)
	Reps               int       `json:"reps"`                 // total correct retrievals
	ConsecutiveCorrect int       `json:"consecutive_correct"`
	Due                time.Time `json:"due"`
	Created            time.Time `json:"created"`
	LastReviewed       time.Time `json:"last_reviewed"`
	LastQuality        int       `json:"last_quality"`
	LastConfidence     int       `json:"last_confidence"`
	Lapses             int       `json:"lapses"`
	Mastered           bool      `json:"mastered"`
	Note               string    `json:"note,omitempty"` // latest gap note
	History            []Review  `json:"history,omitempty"`
}

// Calibration classifies the learner's metacognition on this item's last
// attempt — the heart of gap-finding.
//
//	"blindspot" = confident but wrong (the dangerous quadrant; drill first)
//	"shaky"     = unsure (right-but-hesitant or partial); keep in rotation
//	"solid"     = confident and correct
//	"new"       = not yet attempted meaningfully
func (it *Item) Calibration() string {
	switch {
	case it.LastQuality < 3 && it.LastConfidence >= confidentEnough:
		return "blindspot"
	case it.LastQuality >= goodQuality && it.LastConfidence >= confidentEnough:
		return "solid"
	case it.LastReviewed.IsZero():
		return "new"
	default:
		return "shaky"
	}
}

// Store is a concurrency-safe, JSON-backed set of items. Opening a store
// takes an exclusive lock on a sibling lock file: `etude serve` and a
// `etude drill` REPL against the same course are both long-lived
// writers, and two of them would silently interleave lost updates. The
// second opener fails loudly instead. Multi-writer merge is out of
// scope by design.
type Store struct {
	mu     sync.Mutex
	path   string
	lockF  *os.File
	items  map[string]*Item // key: normalized topic
	closed bool
}

type fileFormat struct {
	Items map[string]*Item `json:"items"`
}

// Report summarizes overall progress.
type Report struct {
	Tracked   int
	Mastered  int
	DueNow    int
	Blindspot int // confident-but-wrong items outstanding
	Weak      []*Item
	Strong    []string
	NextDue   *time.Time
}

// ErrLocked reports that another process holds the store's write lock.
var ErrLocked = errors.New("study store is locked by another process")

func normalize(topic string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(topic))), " ")
}

// NewStore loads the tracker file at path, creating its directory (and an
// empty store) if it doesn't exist yet, and takes the single-writer lock.
// The caller must Close the store to release the lock.
func NewStore(path string) (*Store, error) {
	s := &Store{path: path, items: map[string]*Item{}}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create study dir: %w", err)
	}
	lockPath := path + ".lock"
	lockF, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open study lock: %w", err)
	}
	if err := unix.Flock(int(lockF.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lockF.Close()
		return nil, fmt.Errorf("%w: %s (is another etude drill/serve running against this course?)", ErrLocked, lockPath)
	}
	s.lockF = lockF

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		s.Close()
		return nil, fmt.Errorf("read study file: %w", err)
	}
	var ff fileFormat
	if err := json.Unmarshal(data, &ff); err != nil {
		s.Close()
		return nil, fmt.Errorf("parse study file: %w", err)
	}
	if ff.Items != nil {
		s.items = ff.Items
	}
	return s, nil
}

// Close releases the single-writer lock. The store must not be used
// after Close.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.lockF == nil {
		return nil
	}
	err := unix.Flock(int(s.lockF.Fd()), unix.LOCK_UN)
	return errors.Join(err, s.lockF.Close())
}

// Path returns the on-disk location of the tracker file.
func (s *Store) Path() string { return s.path }

// save writes the store atomically. Caller must hold s.mu.
func (s *Store) save() error {
	b, err := json.MarshalIndent(fileFormat{Items: s.items}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Meta carries optional descriptive fields when recording an official item, so
// the tracker can show and group it later without a second lookup.
type Meta struct {
	Module     string
	Unit       string
	Kind       string
	Difficulty string
	Question   string
}

// Record applies a retrieval-practice update for a graded attempt and persists.
// quality is clamped to 0-5; confidence to 0-3. A correct, confident answer
// advances the streak (and graduates the topic at criterion); anything below a
// correct grade resets the streak and logs a lapse.
func (s *Store) Record(topic string, meta Meta, quality, confidence int, note string, now time.Time) (*Item, error) {
	quality = clamp(quality, 0, 5)
	confidence = clamp(confidence, 0, 3)

	s.mu.Lock()
	defer s.mu.Unlock()

	key := normalize(topic)
	it := s.items[key]
	if it == nil {
		it = &Item{Topic: strings.TrimSpace(topic), Created: now}
		s.items[key] = it
	}
	applyMeta(it, meta)

	if quality >= goodQuality {
		it.Reps++
		it.ConsecutiveCorrect++
		if it.ConsecutiveCorrect >= criterion && confidence >= confidentEnough {
			it.Mastered = true
		}
	} else {
		if quality < 3 {
			it.Lapses++
		}
		it.ConsecutiveCorrect = 0
		it.Mastered = false
	}

	confidentCorrect := quality >= goodQuality && confidence >= confidentEnough
	switch {
	case it.Mastered:
		// Mastered → schedule a cross-day re-verification rather than dropping it
		// forever; relearning across days is what makes it durable by exam day.
		it.Due = now.Add(time.Duration(masteredReviewDays(it.ConsecutiveCorrect)) * 24 * time.Hour)
	case confidentCorrect:
		// Known (right + confident) but not at the mastery criterion yet:
		// re-verify it across days, NOT within this session. Re-quizzing things
		// the learner clearly has wastes the sitting; sending them to tomorrow
		// frees the session to branch into new/weak material (breadth). A
		// confident-correct rep tomorrow then masters it — successive
		// relearning across days.
		it.Due = now.Add(24 * time.Hour)
	default:
		// Missed, or right-but-unsure: keep it in within-session rotation so it
		// is relearned (and confidence built) before the sitting ends.
		it.Due = now.Add(time.Duration(requeueMinutes(it.ConsecutiveCorrect, quality)) * time.Minute)
	}
	it.LastReviewed = now
	it.LastQuality = quality
	it.LastConfidence = confidence
	note = strings.TrimSpace(note)
	it.Note = note
	it.History = append(it.History, Review{Time: now, Quality: quality, Confidence: confidence, Note: note})

	if err := s.save(); err != nil {
		return nil, err
	}
	clone := *it
	return &clone, nil
}

func applyMeta(it *Item, m Meta) {
	if m.Module != "" {
		it.Module = m.Module
	}
	if m.Unit != "" {
		it.Unit = m.Unit
	}
	if m.Kind != "" {
		it.Kind = m.Kind
	}
	if m.Difficulty != "" {
		it.Difficulty = m.Difficulty
	}
	if m.Question != "" {
		it.Question = m.Question
	}
}

// MigrateIDs renames topic keys (old → new), preserving every item's
// scheduling state, and persists if anything moved. This is how mastery
// survives a corpus edit: re-extraction hands the store a map from each
// question's former ID (its aliases) to its current stable ID. A key
// already present under the new ID keeps its (fresher) scheduling
// state; the migrated item's history merges in by time so the lapse
// record is not lost.
func (s *Store) MigrateIDs(mapping map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	changed := false
	for oldID, newID := range mapping {
		oldKey, newKey := normalize(oldID), normalize(newID)
		if oldKey == newKey {
			continue
		}
		it := s.items[oldKey]
		if it == nil {
			continue
		}
		if existing := s.items[newKey]; existing != nil {
			existing.History = mergeHistory(existing.History, it.History)
		} else {
			it.Topic = strings.TrimSpace(newID)
			s.items[newKey] = it
		}
		delete(s.items, oldKey)
		changed = true
	}
	if !changed {
		return nil
	}
	return s.save()
}

func mergeHistory(a, b []Review) []Review {
	out := make([]Review, 0, len(a)+len(b))
	out = append(out, a...)
	out = append(out, b...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	return out
}

// Seen reports whether a topic has ever been recorded.
func (s *Store) Seen(topic string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.items[normalize(topic)]
	return ok
}

// NextItem picks the next due topic to re-quiz. It returns ("review", item)
// when something non-mastered is due now, otherwise ("introduce_new", nil) —
// the coach should then pose a fresh question (from the official bank). module,
// when non-empty, restricts selection to that module. Ordering is
// weakest-and-most-overdue first: blindspots and low streaks come back hardest.
func (s *Store) NextItem(module string, now time.Time) (*Item, string) {
	return s.nextOfKind("", module, now)
}

// NextNumberItem is NextItem restricted to number cloze cards (Kind "number"),
// for a numbers-drill focus mode.
func (s *Store) NextNumberItem(module string, now time.Time) (*Item, string) {
	return s.nextOfKind("number", module, now)
}

// NextMathItem is NextItem restricted to generated math problems (Kind
// "math"), for a math-drill focus mode. A due math topic means the
// equation is due for re-drilling — the caller regenerates a FRESH problem
// (new numbers) for it rather than re-posing the stored one.
func (s *Store) NextMathItem(module string, now time.Time) (*Item, string) {
	return s.nextOfKind("math", module, now)
}

func (s *Store) nextOfKind(kind, module string, now time.Time) (*Item, string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var due []*Item
	for _, it := range s.items {
		// Number cloze cards and math problems are drilled in their own focus
		// modes, so they never surface in the regular question flow; the
		// default flow (kind "") takes everything that ISN'T a focus-mode kind.
		if it.Mastered || (module != "" && it.Module != module) {
			continue
		}
		if kind == "" {
			if it.Kind == "number" || it.Kind == "math" {
				continue
			}
		} else if it.Kind != kind {
			continue
		}
		if !it.Due.After(now) {
			due = append(due, it)
		}
	}
	if len(due) == 0 {
		return nil, "introduce_new"
	}
	sortByRisk(due)
	clone := *due[0]
	return &clone, "review"
}

// sortByRisk orders due items weakest-first: confident-wrong blindspots, then
// lowest streak, then most overdue.
func sortByRisk(due []*Item) {
	sort.Slice(due, func(i, j int) bool {
		a, b := due[i], due[j]
		ab, bb := a.Calibration() == "blindspot", b.Calibration() == "blindspot"
		if ab != bb {
			return ab
		}
		if a.ConsecutiveCorrect != b.ConsecutiveCorrect {
			return a.ConsecutiveCorrect < b.ConsecutiveCorrect
		}
		return a.Due.Before(b.Due)
	})
}

// NextMasteredDue returns a mastered topic whose cross-day re-verification is
// due (earliest first), or nil if none. The coach surfaces these only after
// weak items and fresh coverage are exhausted — maintenance of strong material,
// not the main work. A missed re-verification un-masters the item via Record.
func (s *Store) NextMasteredDue(module string, now time.Time) *Item {
	s.mu.Lock()
	defer s.mu.Unlock()

	var best *Item
	for _, it := range s.items {
		// Main flow only: number/math have their own focus modes.
		if !it.Mastered || it.Kind == "number" || it.Kind == "math" || (module != "" && it.Module != module) {
			continue
		}
		if it.Due.After(now) {
			continue
		}
		if best == nil || it.Due.Before(best.Due) {
			best = it
		}
	}
	if best == nil {
		return nil
	}
	clone := *best
	return &clone
}

// Gaps returns up to n non-mastered items ordered by exam risk: confident-wrong
// blindspots first, then outright wrong, then shaky/low-streak. module, when
// non-empty, filters to that module.
func (s *Store) Gaps(n int, module string) []*Item {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []*Item
	for _, it := range s.items {
		if it.Mastered || (module != "" && it.Module != module) {
			continue
		}
		clone := *it
		out = append(out, &clone)
	}
	sort.Slice(out, func(i, j int) bool {
		return riskScore(out[i]) > riskScore(out[j])
	})
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// riskScore ranks how urgently an item needs drilling before the exam.
func riskScore(it *Item) float64 {
	score := 0.0
	switch it.Calibration() {
	case "blindspot":
		score += 100 // confident + wrong: the most dangerous
	case "shaky":
		score += 40
	}
	if it.LastQuality < 3 {
		score += 30
	}
	score += float64(it.Lapses) * 10
	score += float64(criterion-it.ConsecutiveCorrect) * 5
	if it.LastReviewed.IsZero() {
		score += 15 // never really attempted
	}
	return score
}

// Report summarizes the whole store as of now.
func (s *Store) Report(now time.Time) Report {
	s.mu.Lock()
	defer s.mu.Unlock()

	r := Report{}
	var weak []*Item
	for _, it := range s.items {
		r.Tracked++
		if it.Mastered {
			r.Mastered++
			r.Strong = append(r.Strong, it.Topic)
			continue
		}
		if it.Calibration() == "blindspot" {
			r.Blindspot++
		}
		if !it.Due.After(now) {
			r.DueNow++
		}
		if r.NextDue == nil || it.Due.Before(*r.NextDue) {
			d := it.Due
			r.NextDue = &d
		}
		clone := *it
		weak = append(weak, &clone)
	}
	sort.Slice(weak, func(i, j int) bool { return riskScore(weak[i]) > riskScore(weak[j]) })
	if len(weak) > 8 {
		weak = weak[:8]
	}
	r.Weak = weak
	sort.Strings(r.Strong)
	return r
}

// Stat is a per-topic snapshot used to compute coverage against the bank.
type Stat struct {
	Mastered    bool
	Attempted   bool
	Calibration string
}

// Stats returns a snapshot of every tracked topic keyed by its (un-normalized)
// topic string, for the coverage view to join against the question bank.
func (s *Store) Stats() map[string]Stat {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]Stat, len(s.items))
	for _, it := range s.items {
		out[it.Topic] = Stat{
			Mastered:    it.Mastered,
			Attempted:   !it.LastReviewed.IsZero(),
			Calibration: it.Calibration(),
		}
	}
	return out
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
