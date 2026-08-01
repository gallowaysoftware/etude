package qbank

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gallowaysoftware/etude/course"
)

// writeCourse scaffolds a synthetic course tree and loads its manifest.
// lessons maps "<module>/<lesson-dir>" to lesson.md content.
func writeCourse(t *testing.T, markersYAML string, lessons map[string]string) string {
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
    topic: first module
  - num: 2
    topic: second module
` + markersYAML
	if err := os.WriteFile(filepath.Join(root, "course.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
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

const saqMarkers = "assessment_markers:\n  preset: saq\n"

// The reference saq shape, with the corpus's measured inconsistencies:
// one lowercase "response", one en-dash answer heading, one bold label,
// one section with no label at all.
const saqLesson = `# Lesson 2 - Barley SAQ

## Self-Assessment Questions

1. \* List three objectives of malting.
2. \*\* Label the components of the diagram below.

![kernel cross-section](images/kernel.png)

3. \*\*\* Explain the full malting process.

## Suggested Response - Q1

\* List three objectives of malting.

Response:

- Enzyme development
- Modification of the endosperm
- Moisture control

## Suggested Response – Q2

\*\* Label the components of the diagram below.

**Response:**

The acrospire, the scutellum, and the aleurone layer.

## Suggested Response - Q3

\*\*\* Explain the full malting process.

response:

Steeping, germination, and kilning, in that order.
`

const noLabelLesson = `# Lesson 1 - Mashing SAQ

## Self-Assessment Questions

1. \* What is the purpose of mashing?

## Suggested Response - Q1

To convert grain starches into fermentable sugars using endogenous enzymes.
`

func TestExtractSAQ(t *testing.T) {
	root := writeCourse(t, saqMarkers, map[string]string{
		"Module_1/Lesson_01_Barley":     "# Lesson 1 - Barley\n\nOrdinary lesson, no assessment material.\n",
		"Module_1/Lesson_02_Barley_SAQ": saqLesson,
	})
	b := extractFrom(t, root)

	if b.Len() != 3 {
		t.Fatalf("expected 3 questions, got %d", b.Len())
	}
	q1 := b.Questions[0]
	if q1.Module != "module_1" || q1.UnitTopic != "Barley" || q1.Num != 1 {
		t.Errorf("identity wrong: %+v", q1)
	}
	if q1.Difficulty != Short || b.Questions[1].Difficulty != Progressive || b.Questions[2].Difficulty != Long {
		t.Errorf("difficulty tiers wrong: %q %q %q", q1.Difficulty, b.Questions[1].Difficulty, b.Questions[2].Difficulty)
	}
	if q1.Prompt != "List three objectives of malting." {
		t.Errorf("prompt not clean verbatim: %q", q1.Prompt)
	}
	if !strings.Contains(q1.Answer, "Enzyme development") {
		t.Errorf("answer missing body: %q", q1.Answer)
	}
	if len(q1.Points) != 3 || q1.Points[0] != "Enzyme development" {
		t.Errorf("rubric decomposition wrong: %+v", q1.Points)
	}
	if q1.Citation != "module_1 / Lesson 02 Barley SAQ › Q1" {
		t.Errorf("citation: %q", q1.Citation)
	}
	// Q2's figure is referenced by the nested block under its list item.
	q2 := b.Questions[1]
	if len(q2.Figures) != 1 || q2.Figures[0] != "Module_1/Lesson_02_Barley_SAQ/images/kernel.png" {
		t.Errorf("figure attribution wrong: %+v", q2.Figures)
	}
	if strings.Contains(q2.Prompt, "![") {
		t.Errorf("figure ref leaked into prompt: %q", q2.Prompt)
	}
	// En-dash heading + bold label (Q2) and lowercase label (Q3) must
	// still split question from answer.
	if b.Questions[1].Answer == "" || b.Questions[2].Answer == "" {
		t.Errorf("en-dash/bold/lowercase variants dropped answers: %q / %q", b.Questions[1].Answer, b.Questions[2].Answer)
	}
	for _, q := range b.Questions {
		if len(q.Aliases) != 1 || !strings.HasSuffix(q.Aliases[0], ".q"+itoa(q.Num)) {
			t.Errorf("%s: legacy alias missing: %+v", q.ID, q.Aliases)
		}
	}
}

func TestNoResponseLabelKeepsAnswer(t *testing.T) {
	root := writeCourse(t, saqMarkers, map[string]string{
		"Module_1/Lesson_01_Mashing_SAQ": noLabelLesson,
	})
	b := extractFrom(t, root)
	if b.Len() != 1 {
		t.Fatalf("expected 1 question, got %d", b.Len())
	}
	q := b.Questions[0]
	if q.Prompt != "What is the purpose of mashing?" {
		t.Errorf("prompt should come from the questions list: %q", q.Prompt)
	}
	if !strings.Contains(q.Answer, "fermentable sugars") {
		t.Errorf("label-less section should be treated as the answer: %q", q.Answer)
	}
}

func TestNestedListKeepsNumbering(t *testing.T) {
	lesson := `# Lesson 3 - Filling SAQ

## Self-Assessment Questions

1. \* Fill in the blanks:

   1. First blank is ___.
   2. Second blank is ___.

2. \* State the filling temperature.

![filler](images/filler.png)

## Suggested Response - Q1

Response:

First blank is malt. Second blank is hops.

## Suggested Response - Q2

Response:

85 degrees.
`
	root := writeCourse(t, saqMarkers, map[string]string{
		"Module_1/Lesson_03_Filling_SAQ": lesson,
	})
	b := extractFrom(t, root)
	if b.Len() != 2 {
		t.Fatalf("nested list restarted numbering: got %d questions", b.Len())
	}
	if b.Questions[1].Num != 2 {
		t.Fatalf("second question mis-numbered: %+v", b.Questions[1])
	}
	if len(b.Questions[0].Figures) != 0 {
		t.Errorf("nested figure mis-attributed to Q1: %+v", b.Questions[0].Figures)
	}
	if len(b.Questions[1].Figures) != 1 {
		t.Errorf("Q2 figure missing: %+v", b.Questions[1].Figures)
	}
}

func TestStableIDs(t *testing.T) {
	lessons := map[string]string{
		"Module_1/Lesson_02_Barley_SAQ": saqLesson,
	}
	b1 := extractFrom(t, writeCourse(t, saqMarkers, lessons))
	ids1 := map[string]string{} // prompt -> ID
	for _, q := range b1.Questions {
		ids1[q.Prompt] = q.ID
	}

	// Renumber every question: list position changes, identity must not.
	renumbered := strings.NewReplacer(
		"1. \\* List three", "7. \\* List three",
		"2. \\*\\* Label the", "8. \\*\\* Label the",
		"3. \\*\\*\\* Explain the", "9. \\*\\*\\* Explain the",
		"Suggested Response - Q1", "Suggested Response - Q7",
		"Suggested Response – Q2", "Suggested Response – Q8",
		"Suggested Response - Q3", "Suggested Response - Q9",
	).Replace(saqLesson)
	b2 := extractFrom(t, writeCourse(t, saqMarkers, map[string]string{
		"Module_1/Lesson_02_Barley_SAQ": renumbered,
	}))
	for _, q := range b2.Questions {
		if ids1[q.Prompt] != q.ID {
			t.Errorf("renumbering changed the ID for %q: %s -> %s", q.Prompt, ids1[q.Prompt], q.ID)
		}
		if q.Aliases[0] == b1.Questions[0].Aliases[0] && q.Num != b1.Questions[0].Num {
			t.Errorf("alias should track the new position: %+v", q.Aliases)
		}
	}

	// A real text edit is a new question with a new ID.
	edited := strings.Replace(saqLesson, "objectives of malting", "aims of the malting process", 1)
	b3 := extractFrom(t, writeCourse(t, saqMarkers, map[string]string{
		"Module_1/Lesson_02_Barley_SAQ": edited,
	}))
	for _, q := range b3.Questions {
		if strings.Contains(q.Prompt, "aims of the malting process") && ids1["List three objectives of malting."] == q.ID {
			t.Errorf("editing the question text must change its ID")
		}
	}
}

func TestPerQuestionHeadings(t *testing.T) {
	markers := `assessment_markers:
  preset: saq
  lesson_pattern: "*_Exercises"
  question_heading: "### Question {n}"
  answer_heading: "### Model Answer {n}"
`
	lesson := `# Lesson 1 - Weights Exercises

### Question 1

What does a hydrometer measure?

### Model Answer 1

Specific gravity of a wort.

### Question 2

Name two hydrometer scales.

### Model Answer 2

- Brix
- Plato
`
	root := writeCourse(t, markers, map[string]string{
		"Module_2/Lesson_01_Weights_Exercises": lesson,
	})
	b := extractFrom(t, root)
	if b.Len() != 2 {
		t.Fatalf("expected 2 questions, got %d", b.Len())
	}
	q := b.Questions[0]
	if q.Prompt != "What does a hydrometer measure?" {
		t.Errorf("per-question prompt: %q", q.Prompt)
	}
	if q.Answer != "Specific gravity of a wort." {
		t.Errorf("per-question answer: %q", q.Answer)
	}
	if len(b.Questions[1].Points) != 2 {
		t.Errorf("points: %+v", b.Questions[1].Points)
	}
}

func TestDecomposePoints(t *testing.T) {
	list := decomposePoints("Intro line.\n\n- alpha\n- beta\n- gamma")
	if len(list) != 3 || list[2] != "gamma" {
		t.Errorf("list decomposition: %+v", list)
	}
	prose := decomposePoints("First point spans\none paragraph.\n\nSecond point is here.")
	if len(prose) != 2 || !strings.HasPrefix(prose[0], "First point") {
		t.Errorf("prose decomposition: %+v", prose)
	}
	single := decomposePoints("One short answer.")
	if len(single) != 1 {
		t.Errorf("single decomposition: %+v", single)
	}
	if got := decomposePoints("  \n "); got != nil {
		t.Errorf("empty answer should give no points: %+v", got)
	}
}

func extractFrom(t *testing.T, root string) *Bank {
	t.Helper()
	m, err := course.Load(root)
	if err != nil {
		t.Fatalf("course.Load: %v", err)
	}
	b, err := Extract(m)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return b
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
