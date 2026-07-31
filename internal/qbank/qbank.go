// Package qbank extracts a course's own assessment material into a
// structured, gradeable question bank. Corpora that ship self-assessment
// questions with model answers are the privileged input: the drill relays
// those questions verbatim and grades against the official answer,
// inventing neither. The extractor never invents a question — that
// contract is what the whole drill rests on.
//
// Matching is driven by the course manifest's assessment_markers block
// (course.Assessments), not by per-curriculum code: the heading
// templates, the lesson-directory glob, and the difficulty convention
// are all configuration. The "saq" preset is the reference shape.
//
// Excised from the private drill coach per docs/excision-checklist.md
// (source repo, SHA, and prefix are recorded in the commit that
// introduced this package): parsing mechanism kept, corpus identity and
// scrape-specific cleanup dropped.
package qbank

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gallowaysoftware/etude/internal/course"
)

// Difficulty mirrors the asterisk tier convention: short & retrospective,
// progressive, or long/integrative (closest to a long-answer exam
// question). A corpus with no difficulty marker leaves it empty.
type Difficulty string

const (
	Short       Difficulty = "short"
	Progressive Difficulty = "progressive"
	Long        Difficulty = "long"
)

// maxLessonBytes mirrors the pipeline's silent-drop ceiling (restated in
// internal/course/tree.go): a lesson.md over 1 MiB never reaches a
// prompt, so the bank must not pretend its questions exist.
const maxLessonBytes = 1 << 20

// Question is one official assessment item.
type Question struct {
	// ID is stable across re-extraction: a hash of the unit and the
	// question's own text, never its list position, so editing the
	// corpus elsewhere does not orphan weeks of scheduling state.
	ID         string     `json:"id"`
	Module     string     `json:"module"`     // "module_2"
	Unit       string     `json:"unit"`       // "Lesson 5 - Distillation Theory SAQ"
	UnitTopic  string     `json:"unit_topic"` // "Distillation Theory" (grouping key for coverage)
	Num        int        `json:"num"`        // question number within the assessment lesson (display/order only)
	Difficulty Difficulty `json:"difficulty,omitempty"`
	Prompt     string     `json:"prompt"`            // the question, as posed
	Answer     string     `json:"answer"`            // the official model answer
	Points     []string   `json:"points"`            // the answer decomposed into scoreable rubric points at extraction
	Citation   string     `json:"citation"`          // "module_2 / Lesson 5 - Distillation Theory SAQ › Q3"
	Figures    []string   `json:"figures,omitempty"` // image paths (relative to source root) referenced by the question
	// Aliases are former IDs this question was extracted under (the
	// position-based scheme), so the study store can migrate mastery
	// state forward instead of orphaning it.
	Aliases []string `json:"aliases,omitempty"`
}

// UnitKey is the grouping key for coverage/spreading: module + unit topic.
func (q *Question) UnitKey() string { return q.Module + "|" + q.UnitTopic }

// Bank is an indexed, immutable set of questions.
type Bank struct {
	Questions []*Question
	byID      map[string]*Question
	byModule  map[string][]*Question
}

// Len returns the number of questions.
func (b *Bank) Len() int { return len(b.Questions) }

// Get returns a question by ID, or nil.
func (b *Bank) Get(id string) *Question { return b.byID[id] }

// ForModule returns all questions in a module ("module_2"); an empty
// module returns the whole bank.
func (b *Bank) ForModule(module string) []*Question {
	if module == "" {
		return b.Questions
	}
	return b.byModule[module]
}

// Modules returns the module labels present, in manifest order.
func (b *Bank) Modules() []string {
	out := make([]string, 0, len(b.byModule))
	for m := range b.byModule {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

func (b *Bank) add(q *Question) {
	if q == nil || b.byID[q.ID] != nil {
		return
	}
	b.Questions = append(b.Questions, q)
	b.byID[q.ID] = q
	b.byModule[q.Module] = append(b.byModule[q.Module], q)
}

// Extract builds the question bank for a course. Only assessment lessons
// (directories matching the markers' lesson pattern) contribute. Modules
// are taken in manifest order so the bank's question sequence is the
// teaching sequence, not a bytewise directory sort.
func Extract(m *course.Manifest) (*Bank, error) {
	markers := m.Assessments.Resolved()
	pat, err := compileMarkers(markers)
	if err != nil {
		return nil, err
	}
	b := &Bank{byID: map[string]*Question{}, byModule: map[string][]*Question{}}
	for _, mod := range m.Modules {
		label := fmt.Sprintf("module_%d", mod.Num)
		dir := m.ModuleDir(mod)
		entries, err := os.ReadDir(dir)
		if err != nil {
			// A listed module with no directory is a validation finding,
			// not an extraction failure — its questions simply don't exist.
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || !strings.HasPrefix(e.Name(), "Lesson_") {
				continue
			}
			if !matchLesson(pat.lessonGlob, e.Name()) {
				continue
			}
			lessonFile := filepath.Join(dir, e.Name(), "lesson.md")
			info, err := os.Stat(lessonFile)
			if err != nil || info.Size() == 0 || info.Size() > maxLessonBytes {
				continue // same silent-drop ceiling as the pipeline
			}
			data, err := os.ReadFile(lessonFile)
			if err != nil {
				continue
			}
			rel := filepath.Join(filepath.Base(dir), e.Name())
			for _, q := range pat.parseLesson(string(data), label, e.Name(), rel) {
				b.add(q)
			}
		}
	}
	return b, nil
}

// matchLesson applies the lesson glob (a path.Match pattern against the
// directory name). An empty pattern matches every lesson.
func matchLesson(glob, name string) bool {
	if glob == "" {
		return true
	}
	ok, err := filepath.Match(glob, name)
	return err == nil && ok
}

// stableID derives the question's durable identity: a hash of module,
// unit, and the question's normalized text. List position is
// deliberately absent — renumbering questions must not orphan mastery
// state. Two questions with identical text in one unit are the same
// question as far as scheduling is concerned (the bank dedupes them).
func stableID(module, unit, prompt string) string {
	sum := sha256.Sum256([]byte(normalizeText(module) + "|" + normalizeText(unit) + "|" + normalizeText(prompt)))
	return fmt.Sprintf("%s.%s.q%s", module, slugify(unit), hex.EncodeToString(sum[:4]))
}

// legacyID is the pre-stable-ID scheme (position-based), retained as an
// alias so stores written by it migrate forward.
func legacyID(module, unit string, num int) string {
	return fmt.Sprintf("%s.%s.q%d", module, slugify(unit), num)
}

// normalizeText collapses case and whitespace so an ID survives
// reformatting but not a real edit.
func normalizeText(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

func slugify(s string) string {
	var sb strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			sb.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				sb.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(sb.String(), "-")
}

// decomposePoints splits an official answer into scoreable rubric points
// at bank-build time, so grading is matching against a list rather than
// holistic judgement (see docs: the grading eval harness qualifies a
// grader model against exactly these points). List items are the points
// when the answer is a list; otherwise blank-line paragraphs; a
// single-paragraph answer is one point.
func decomposePoints(answer string) []string {
	// List items are the points when the answer carries a list.
	var points []string
	for line := range strings.Lines(answer) {
		if rest, ok := cutListMarker(strings.TrimSpace(line)); ok && rest != "" {
			points = append(points, rest)
		}
	}
	if len(points) > 0 {
		return points
	}
	// A prose answer decomposes by paragraph instead — one point per
	// line would shred sentences into ungradeable fragments.
	for _, para := range strings.Split(answer, "\n\n") {
		if para = strings.TrimSpace(para); para != "" {
			points = append(points, para)
		}
	}
	if len(points) == 0 && strings.TrimSpace(answer) != "" {
		points = []string{strings.TrimSpace(answer)}
	}
	return points
}

// cutListMarker strips a markdown list marker ("- ", "* ", or "N. ")
// from a line, reporting whether one was present.
func cutListMarker(line string) (string, bool) {
	for _, marker := range []string{"- ", "* "} {
		if rest, ok := strings.CutPrefix(line, marker); ok {
			return strings.TrimSpace(rest), true
		}
	}
	if n := strings.Index(line, ". "); n > 0 && n <= 3 && isDigits(line[:n]) {
		return strings.TrimSpace(line[n+2:]), true
	}
	return line, false
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}
