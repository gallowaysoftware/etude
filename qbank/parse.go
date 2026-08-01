package qbank

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gallowaysoftware/etude/course"
)

// markers is the compiled form of course.Assessments: heading templates
// become regexes once, at extraction start.
type markers struct {
	lessonGlob       string
	perQuestion      bool // QuestionHeading contains {n}: one heading per question
	questionHeading  *regexp.Regexp
	questionLevel    int
	answerHeading    *regexp.Regexp // one capture group: the {n} number
	answerLevel      int
	difficultyMarker string
}

var (
	figureRe = regexp.MustCompile(`!\[[^\]]*\]\(([^)]+)\)`)
	// topNumberedRe matches ONLY top-level ordered-list items. A nested
	// list (fill-in-the-blank sub-items) is indented, and counting it
	// would restart question numbering and mis-attribute figures — a
	// measured failure on the reference corpus.
	topNumberedRe = regexp.MustCompile(`(?m)^(\d+)\.\s`)
	anyHeadingRe  = regexp.MustCompile(`(?m)^(#{1,6})\s`)
	wsRe          = regexp.MustCompile(`[ \t]+`)
	blankRunRe    = regexp.MustCompile(`\n{3,}`)
	// responseLabelRe finds the answer-body label on its own line. The
	// reference corpus writes it as "Response:", "**Response:**", or
	// lowercase "response:" (423 / 33 / 8 of 464 sections, plus 8 with
	// no label at all); the private extractor's literal match missed the
	// bold and lowercase forms and silently dropped those answers.
	responseLabelRe = regexp.MustCompile(`(?im)^[ \t]*(?:\*\*)?[ \t]*(?:response|answer)[ \t]*:(?:\*\*)?[ \t]*$`)
)

// compileMarkers turns the manifest's assessment markers into regexes.
// Heading templates are literal except for "{n}" (the question number);
// matching is case-insensitive and a hyphen in the template also matches
// en/em dashes, because real corpora are inconsistent about both.
func compileMarkers(a course.Assessments) (*markers, error) {
	m := &markers{
		lessonGlob:       a.LessonPattern,
		perQuestion:      a.PerQuestionHeadings(),
		difficultyMarker: a.DifficultyMarker,
	}
	var err error
	if a.QuestionHeading != "" {
		m.questionHeading, m.questionLevel, err = headingRe(a.QuestionHeading)
		if err != nil {
			return nil, fmt.Errorf("assessment_markers.question_heading: %w", err)
		}
	}
	if a.AnswerHeading == "" {
		return nil, fmt.Errorf("assessment_markers.answer_heading is required for extraction")
	}
	if !strings.Contains(a.AnswerHeading, "{n}") {
		return nil, fmt.Errorf("assessment_markers.answer_heading %q lacks {n}: answer headings are numbered per question", a.AnswerHeading)
	}
	m.answerHeading, m.answerLevel, err = headingRe(a.AnswerHeading)
	if err != nil {
		return nil, fmt.Errorf("assessment_markers.answer_heading: %w", err)
	}
	return m, nil
}

// headingRe compiles one heading template. It returns the regex (with a
// capture group for {n} when present) and the template's heading level.
func headingRe(tmpl string) (*regexp.Regexp, int, error) {
	level := 0
	for level < len(tmpl) && tmpl[level] == '#' {
		level++
	}
	parts := strings.Split(tmpl, "{n}")
	var sb strings.Builder
	sb.WriteString(`(?im)^[ \t]*`)
	for i, lit := range parts {
		// QuoteMeta leaves "-" alone; widen it so a template hyphen also
		// matches the en/em dashes corpora actually use.
		sb.WriteString(strings.ReplaceAll(regexp.QuoteMeta(lit), "-", `[-–—]`))
		if i+1 < len(parts) {
			sb.WriteString(`(\d+)`)
		}
	}
	sb.WriteString(`[ \t]*$`)
	re, err := regexp.Compile(sb.String())
	if err != nil {
		return nil, 0, err
	}
	return re, level, nil
}

// parseLesson extracts questions from one assessment lesson. module is
// the manifest-derived label ("module_2"), dirName the lesson directory,
// and rel the lesson directory's path relative to the source root (for
// figure paths).
func (m *markers) parseLesson(content, module, dirName, rel string) []*Question {
	unit := strings.ReplaceAll(dirName, "_", " ")
	topic := unitTopic(unit, m.lessonGlob)

	// The questions block supplies prompt text, figures, and (in the
	// saq shape) difficulty markers; answer sections supply the official
	// answer and a fallback prompt. Both halves are matched by number.
	qs := m.questionsByNum(content, rel)

	var out []*Question
	for _, sec := range m.answerSections(content) {
		restated, answer, diff := m.splitResponse(sec.body)
		q := qs[sec.num]
		prompt := q.prompt
		if prompt == "" {
			prompt = restated // no list entry: the response restatement is all we have
		}
		if prompt == "" {
			continue // nothing to pose
		}
		if q.difficulty != "" {
			diff = q.difficulty
		}
		out = append(out, &Question{
			ID:         stableID(module, unit, prompt),
			Module:     module,
			Unit:       unit,
			UnitTopic:  topic,
			Num:        sec.num,
			Difficulty: diff,
			Prompt:     prompt,
			Answer:     answer,
			Points:     decomposePoints(answer),
			Citation:   fmt.Sprintf("%s / %s › Q%d", module, unit, sec.num),
			Figures:    q.figures,
			Aliases:    []string{legacyID(module, unit, sec.num)},
		})
	}
	// Per-question-heading shape: questions that never got an answer
	// section still enter the bank (answer-less items are skipped by the
	// coach but visible to coverage).
	if m.perQuestion {
		seen := map[int]bool{}
		for _, q := range out {
			seen[q.Num] = true
		}
		for num, q := range qs {
			if seen[num] || q.prompt == "" {
				continue
			}
			out = append(out, &Question{
				ID:        stableID(module, unit, q.prompt),
				Module:    module,
				Unit:      unit,
				UnitTopic: topic,
				Num:       num,
				Prompt:    q.prompt,
				Figures:   q.figures,
				Citation:  fmt.Sprintf("%s / %s › Q%d", module, unit, num),
				Aliases:   []string{legacyID(module, unit, num)},
			})
		}
	}
	return out
}

// questionHalf is the prompt side of one numbered question.
type questionHalf struct {
	prompt     string
	figures    []string
	difficulty Difficulty
}

// questionsByNum parses the question side of a lesson: either the single
// ordered list under the question heading (list shape), or each
// per-question heading's section ({n} shape).
func (m *markers) questionsByNum(content, rel string) map[int]questionHalf {
	out := map[int]questionHalf{}
	if m.questionHeading == nil {
		return out
	}
	if m.perQuestion {
		for _, sec := range m.sections(m.questionHeading, m.questionLevel, content, m.answerHeading) {
			figs := figuresIn(sec.body, rel)
			out[sec.num] = questionHalf{
				prompt:     clean(stripFigures(sec.body)),
				figures:    figs,
				difficulty: m.difficultyOf(sec.body),
			}
		}
		return out
	}
	// List shape: one heading opens a single ordered list. The block
	// ends at the first answer heading or a same-or-higher heading.
	loc := m.questionHeading.FindStringIndex(content)
	if loc == nil {
		return out
	}
	block := content[loc[1]:]
	if aloc := m.answerHeading.FindStringIndex(block); aloc != nil {
		block = block[:aloc[0]]
	}
	if hloc := anyHeadingRe.FindStringIndex(block); hloc != nil {
		block = block[:hloc[0]]
	}
	items := topNumberedRe.FindAllStringSubmatchIndex(block, -1)
	for i, it := range items {
		num := atoi(block[it[2]:it[3]])
		end := len(block)
		if i+1 < len(items) {
			end = items[i+1][0]
		}
		seg := block[it[0]:end]
		// Drop the "1." marker itself, then the difficulty run.
		seg = strings.TrimSpace(seg[it[1]-it[0]:])
		diff := m.difficultyOf(seg)
		figs := figuresIn(seg, rel)
		out[num] = questionHalf{
			prompt:     clean(stripFigures(stripDifficulty(seg))),
			figures:    figs,
			difficulty: diff,
		}
	}
	return out
}

// answerSection is one model-answer heading plus its body.
type answerSection struct {
	num  int
	body string
}

// answerSections finds every model-answer section. A section ends at the
// next answer heading or at any heading of the same or higher level —
// deeper subheadings belong to the answer.
func (m *markers) answerSections(content string) []answerSection {
	return m.sections(m.answerHeading, m.answerLevel, content, nil)
}

// sections is the shared heading-walker: every match of re opens a
// section that ends at the next match of re, the first match of stop,
// or a heading of level <= level, whichever comes first.
func (m *markers) sections(re *regexp.Regexp, level int, content string, stop *regexp.Regexp) []answerSection {
	locs := re.FindAllStringSubmatchIndex(content, -1)
	var out []answerSection
	for i, loc := range locs {
		bodyStart := loc[1]
		bodyEnd := len(content)
		if i+1 < len(locs) {
			bodyEnd = locs[i+1][0]
		}
		body := content[bodyStart:bodyEnd]
		if stop != nil {
			if sloc := stop.FindStringIndex(body); sloc != nil && sloc[0] < bodyEnd-bodyStart {
				body = body[:sloc[0]]
			}
		}
		for _, h := range anyHeadingRe.FindAllStringSubmatchIndex(body, -1) {
			if len(body[h[2]:h[3]]) <= level {
				body = body[:h[0]]
				break
			}
		}
		out = append(out, answerSection{num: atoi(content[loc[2]:loc[3]]), body: body})
	}
	return out
}

// splitResponse separates the restated question, the official answer,
// and the difficulty marker within one answer section. The section looks
// like "\n\* <question text>\n\nResponse:\n\n<answer>". A section with
// no label line is treated as all answer — the questions list supplies
// the prompt, and dropping the item (the private behaviour) silently
// shrank the bank.
func (m *markers) splitResponse(section string) (restated, answer string, diff Difficulty) {
	diff = m.difficultyOf(section)
	if loc := responseLabelRe.FindStringIndex(section); loc != nil {
		restated = clean(stripFigures(stripDifficulty(section[:loc[0]])))
		answer = clean(section[loc[1]:])
		return restated, answer, diff
	}
	return "", clean(stripDifficulty(section)), diff
}

// difficultyOf counts the leading (possibly escaped) asterisk run to map
// to the difficulty tiers. Empty when the corpus encodes no difficulty.
func (m *markers) difficultyOf(s string) Difficulty {
	if m.difficultyMarker == "" {
		return ""
	}
	s = strings.TrimLeft(s, " \t\n")
	// A list item's number precedes the marker run.
	if loc := topNumberedRe.FindStringIndex(s); loc != nil && loc[0] == 0 {
		s = strings.TrimLeft(s[loc[1]:], " \t")
	}
	n := 0
	for i := 0; i < len(s) && n < 4; i++ {
		switch s[i] {
		case '*':
			n++
		case '\\', ' ', '\t':
		default:
			i = len(s)
		}
	}
	switch {
	case n >= 3:
		return Long
	case n == 2:
		return Progressive
	case n == 1:
		return Short
	default:
		return ""
	}
}

// stripDifficulty removes the leading (escaped) asterisk run.
func stripDifficulty(s string) string {
	s = strings.TrimLeft(s, " \t\n")
	i := 0
	for i < len(s) {
		switch s[i] {
		case '*', '\\', ' ', '\t':
			i++
		default:
			return s[i:]
		}
	}
	return s[i:]
}

// figuresIn collects the image refs in a text segment as paths relative
// to the source root. External refs are not figures.
func figuresIn(seg, rel string) []string {
	var out []string
	for _, f := range figureRe.FindAllStringSubmatch(seg, -1) {
		ref := strings.TrimSpace(f[1])
		if ref == "" || strings.HasPrefix(ref, "http") {
			continue
		}
		out = append(out, filepath.ToSlash(filepath.Join(rel, ref)))
	}
	return out
}

func stripFigures(s string) string { return figureRe.ReplaceAllString(s, "") }

// clean normalizes whitespace and de-escapes common markdown escapes so
// the text reads naturally when posed to a learner.
func clean(s string) string {
	s = strings.NewReplacer(`\*`, "", `\#`, "#", `\_`, "_", `\-`, "-").Replace(s)
	s = wsRe.ReplaceAllString(s, " ")
	s = blankRunRe.ReplaceAllString(s, "\n\n")
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "---")
	return strings.TrimSpace(s)
}

var lessonPrefixRe = regexp.MustCompile(`(?i)^lesson[ _]+\d+[ _\-]*`)

// unitTopic strips the "Lesson N" prefix and the lesson glob's fixed
// token (e.g. "SAQ") so questions group by their underlying topic for
// coverage ("Lesson 5 - Distillation Theory SAQ" → "Distillation
// Theory", "Lesson_05_Distillation_Theory_SAQ" likewise).
func unitTopic(unit, glob string) string {
	t := lessonPrefixRe.ReplaceAllString(unit, "")
	t = strings.Trim(t, "- ")
	token := strings.Trim(glob, "*")
	if token != "" && len(t) > len(token) && strings.EqualFold(t[len(t)-len(token):], token) {
		t = strings.TrimRight(strings.TrimSpace(t[:len(t)-len(token)]), "- ")
	}
	return strings.TrimSpace(t)
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}
