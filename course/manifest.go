// Package course implements the course manifest (course.yaml) and the
// lesson-tree contract it describes: loading, defaulting, validation, and
// scaffolding.
//
// The manifest answers "what is this course" — identity, the labels that
// skin every prompt, where the material lives, how the corpus marks its
// own assessment material. It deliberately does NOT answer "where do
// models run": endpoints, profiles, and API keys are machine-scoped and
// belong to vibe/vamp config or ETUDE_* environment variables, because
// the same course.yaml is meant to travel between a laptop, a GPU box,
// and an external provider unchanged.
package course

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileName is the manifest's fixed basename. A course is a directory
// containing one of these.
const FileName = "course.yaml"

// Version is the manifest schema version this build understands. A
// manifest carrying a higher version is rejected rather than
// half-interpreted.
const Version = 1

// slugPattern is deliberately identical to the refrain memory service's
// expert-slug rule. The course slug becomes a refrain expert directory,
// a knowledge-base collection name, and a filename stem, so it must
// satisfy the strictest consumer — and matching refrain's regex exactly
// is what lets one declared slug bind all three without a translation
// table.
var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// placeholderPrefix marks a value the author must replace before the
// manifest is usable. Borrowed from vibe's profile templates: scaffolded
// files fail loudly at load time instead of producing a confusing
// downstream error (a cover prompt reading "REPLACE-with-..." would
// otherwise sail through and land in the rendered artwork).
const placeholderPrefix = "REPLACE-"

// Manifest is the parsed course.yaml. Field order here mirrors the
// scaffolded file so a reader can follow one against the other.
type Manifest struct {
	Version int    `yaml:"version"`
	Slug    string `yaml:"slug"`
	Title   string `yaml:"title"`

	// Subject, Program, Persona, and Assessment are the four labels that
	// skin every prompt in the pipeline. They carry the entire
	// domain identity of a course — the DAG itself is subject-agnostic.
	Subject    string `yaml:"subject"`
	Program    string `yaml:"program"`
	Persona    string `yaml:"persona"`
	Assessment string `yaml:"assessment"`

	// Source is the directory holding the module subdirectories,
	// resolved relative to the manifest's own directory so a course
	// moves as one tree.
	Source string `yaml:"source"`

	// Modules lists the modules in teaching order. Order is explicit
	// rather than derived from directory sort because "Module_10" sorts
	// before "Module_2" and a course is not a set.
	Modules []Module `yaml:"modules"`

	Render      Render      `yaml:"render"`
	Assessments Assessments `yaml:"assessment_markers"`
	Expert      Expert      `yaml:"expert"`

	// dir is the manifest's own directory, captured at load so relative
	// paths resolve against the course rather than the process's cwd.
	dir string `yaml:"-"`
}

// Module is one unit of production: a directory of lessons that becomes
// one audiobook, one study guide, and one EPUB.
type Module struct {
	// Num is the module's identifier, used in cover-art seeds and
	// output filenames.
	Num int `yaml:"num"`
	// Topic is a short prose summary ("fermentation and wort
	// production"). It reaches cover prompts and EPUB titles.
	Topic string `yaml:"topic"`
	// Dir names the module's directory under Source. Empty defaults to
	// "Module_<Num>".
	Dir string `yaml:"dir"`
}

// Render holds the presentation knobs: how the course sounds, looks, and
// is named on disk. Every field is optional.
type Render struct {
	Voice        string `yaml:"voice"`
	CoverPrompt  string `yaml:"cover_prompt"`
	SearchSuffix string `yaml:"search_suffix"`
	EPUBTitle    string `yaml:"epub_title"`
	EPUBAuthor   string `yaml:"epub_author"`
	// FilenamePrefix overrides the deliverable basename stem. Empty
	// derives it from each module's topic.
	FilenamePrefix string `yaml:"filename_prefix"`
}

// Assessments describes how the corpus marks its own questions and model
// answers, so the drill's bank extractor can find them without
// per-curriculum code. A corpus that carries no assessment material
// leaves Preset empty and drills generated questions instead.
type Assessments struct {
	// Preset names a built-in marker set. "none" (or empty) means the
	// corpus carries no extractable assessment material.
	Preset string `yaml:"preset"`
	// LessonPattern matches the lesson directories that hold assessment
	// material (e.g. "*SAQ*"). Empty scans every lesson.
	LessonPattern string `yaml:"lesson_pattern"`
	// QuestionHeading opens the questions. Two shapes are supported and
	// the "{n}" placeholder is what distinguishes them: a heading
	// containing {n} marks one heading per question, while a heading
	// without it opens a single markdown ordered list whose items are
	// the questions. Real curricula use both.
	QuestionHeading string `yaml:"question_heading"`
	// AnswerHeading marks each model answer; "{n}" is the question
	// number. Matching is case-insensitive and tolerates an en-dash for
	// the hyphen, because real corpora are inconsistent about both.
	AnswerHeading string `yaml:"answer_heading"`
	// DifficultyMarker names the convention that encodes difficulty
	// (e.g. "asterisks" for the * / ** / *** convention). Empty means
	// difficulty is not encoded.
	DifficultyMarker string `yaml:"difficulty_marker"`
}

// Expert binds this course to the services that make it conversational.
// Both slugs default to the course slug; they exist as overrides only
// because pre-existing deployments may already have named them
// differently.
type Expert struct {
	// MemorySlug is the refrain expert whose memory holds this course's
	// learner state.
	MemorySlug string `yaml:"memory_slug"`
	// KnowledgeCollection is the knowledge-service collection holding
	// this course's corpus.
	KnowledgeCollection string `yaml:"knowledge_collection"`
}

// Load reads and validates the manifest at path. A directory path is
// accepted and resolved to the course.yaml inside it.
func Load(path string) (*Manifest, error) {
	resolved, err := resolveManifestPath(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil, err
	}
	var m Manifest
	// KnownFields turns a typo into an error instead of a silently
	// ignored setting — a misspelled "voise:" that quietly reverts the
	// narrator to the default is exactly the kind of failure a manifest
	// should not have.
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("%s: %w", resolved, err)
	}
	m.dir = filepath.Dir(resolved)
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", resolved, err)
	}
	return &m, nil
}

// resolveManifestPath accepts either a course directory or a manifest
// file and returns the manifest path.
func resolveManifestPath(path string) (string, error) {
	if path == "" {
		path = "."
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return filepath.Join(path, FileName), nil
	}
	return path, nil
}

// Validate checks the manifest in isolation — structure, identity, and
// placeholders — without touching the lesson tree. ScanTree covers the
// content on disk.
func (m *Manifest) Validate() error {
	var errs []error

	switch {
	case m.Version == 0:
		errs = append(errs, errors.New("version: required (current schema is 1)"))
	case m.Version > Version:
		errs = append(errs, fmt.Errorf("version %d is newer than this build understands (max %d)", m.Version, Version))
	}

	if m.Slug == "" {
		errs = append(errs, errors.New("slug: required"))
	} else if !slugPattern.MatchString(m.Slug) {
		errs = append(errs, fmt.Errorf("slug %q: must be lowercase letters, digits, and hyphens, starting with a letter or digit, at most 64 characters", m.Slug))
	}

	for field, value := range map[string]string{
		"title":      m.Title,
		"subject":    m.Subject,
		"program":    m.Program,
		"persona":    m.Persona,
		"assessment": m.Assessment,
	} {
		if strings.TrimSpace(value) == "" {
			errs = append(errs, fmt.Errorf("%s: required", field))
		}
	}

	if len(m.Modules) == 0 {
		errs = append(errs, errors.New("modules: at least one module is required"))
	}
	seen := make(map[int]bool, len(m.Modules))
	for i, mod := range m.Modules {
		if mod.Num <= 0 {
			errs = append(errs, fmt.Errorf("modules[%d].num: must be a positive integer", i))
		} else if seen[mod.Num] {
			errs = append(errs, fmt.Errorf("modules[%d].num: %d is used more than once", i, mod.Num))
		}
		seen[mod.Num] = true
		if strings.TrimSpace(mod.Topic) == "" {
			errs = append(errs, fmt.Errorf("modules[%d].topic: required", i))
		}
	}

	if err := m.Assessments.validate(); err != nil {
		errs = append(errs, err)
	}

	errs = append(errs, m.placeholderErrors()...)

	return errors.Join(errs...)
}

// placeholderErrors reports every field still carrying a scaffolded
// REPLACE- value. Reported together so an author fixes the whole file in
// one pass rather than one error per run.
func (m *Manifest) placeholderErrors() []error {
	var errs []error
	check := func(field, value string) {
		if strings.HasPrefix(value, placeholderPrefix) {
			errs = append(errs, fmt.Errorf("%s: still holds the scaffolded placeholder %q", field, value))
		}
	}
	check("slug", m.Slug)
	check("title", m.Title)
	check("subject", m.Subject)
	check("program", m.Program)
	check("persona", m.Persona)
	check("assessment", m.Assessment)
	check("render.cover_prompt", m.Render.CoverPrompt)
	for i, mod := range m.Modules {
		check(fmt.Sprintf("modules[%d].topic", i), mod.Topic)
	}
	return errs
}

// Presets are the built-in assessment-marker sets. A preset is a
// starting point, not a constraint: any field an author sets explicitly
// wins over the preset's value.
var Presets = map[string]Assessments{
	// saq matches curricula that ship self-assessment questions with
	// model answers inside the lesson itself: one heading opening a
	// numbered list of questions, then one "suggested response" heading
	// per question carrying the official answer.
	//
	// Every value here is measured against a real 189-lesson corpus
	// rather than guessed. The lesson pattern in particular is "*SAQ*"
	// and not "*_Unit_SAQ": the narrower pattern matched 18 of 189
	// lesson directories and would have left 83% of the question bank
	// on the floor, because per-lesson SAQs outnumber end-of-unit ones.
	"saq": {
		LessonPattern:    "*SAQ*",
		QuestionHeading:  "## Self-Assessment Questions",
		AnswerHeading:    "## Suggested Response - Q{n}",
		DifficultyMarker: "escaped-asterisks",
	},
}

// PerQuestionHeadings reports whether QuestionHeading marks each
// question individually rather than opening a single list.
func (a Assessments) PerQuestionHeadings() bool {
	return strings.Contains(a.QuestionHeading, "{n}")
}

func (a Assessments) validate() error {
	preset := strings.TrimSpace(a.Preset)
	if preset == "" || preset == "none" {
		return nil
	}
	if _, ok := Presets[preset]; !ok {
		names := make([]string, 0, len(Presets)+1)
		names = append(names, "none")
		for k := range Presets {
			names = append(names, k)
		}
		return fmt.Errorf("assessment_markers.preset %q: unknown (known presets: %s)", preset, strings.Join(names, ", "))
	}
	return nil
}

// Resolved returns the assessment markers with preset defaults filled in
// under any explicit overrides.
func (a Assessments) Resolved() Assessments {
	base, ok := Presets[strings.TrimSpace(a.Preset)]
	if !ok {
		return a
	}
	if a.LessonPattern == "" {
		a.LessonPattern = base.LessonPattern
	}
	if a.QuestionHeading == "" {
		a.QuestionHeading = base.QuestionHeading
	}
	if a.AnswerHeading == "" {
		a.AnswerHeading = base.AnswerHeading
	}
	if a.DifficultyMarker == "" {
		a.DifficultyMarker = base.DifficultyMarker
	}
	return a
}

// HasAssessmentMaterial reports whether the course claims extractable
// questions. The drill uses this to choose between extracting the
// corpus's own bank and generating one.
func (m *Manifest) HasAssessmentMaterial() bool {
	preset := strings.TrimSpace(m.Assessments.Preset)
	if preset != "" && preset != "none" {
		return true
	}
	return m.Assessments.QuestionHeading != "" || m.Assessments.AnswerHeading != ""
}

// Dir returns the directory containing the manifest.
func (m *Manifest) Dir() string { return m.dir }

// SourceDir returns the absolute directory holding the module
// subdirectories.
func (m *Manifest) SourceDir() string {
	src := m.Source
	if src == "" {
		src = "."
	}
	if filepath.IsAbs(src) {
		return filepath.Clean(src)
	}
	return filepath.Join(m.dir, src)
}

// ModuleDir returns the absolute directory for one module.
func (m *Manifest) ModuleDir(mod Module) string {
	dir := mod.Dir
	if dir == "" {
		dir = fmt.Sprintf("Module_%d", mod.Num)
	}
	if filepath.IsAbs(dir) {
		return filepath.Clean(dir)
	}
	return filepath.Join(m.SourceDir(), dir)
}

// FindModule returns the module with the given number.
func (m *Manifest) FindModule(num int) (Module, bool) {
	for _, mod := range m.Modules {
		if mod.Num == num {
			return mod, true
		}
	}
	return Module{}, false
}

// MemorySlug returns the refrain expert slug for this course.
func (m *Manifest) MemorySlug() string {
	if m.Expert.MemorySlug != "" {
		return m.Expert.MemorySlug
	}
	return m.Slug
}

// KnowledgeCollection returns the knowledge-service collection name for
// this course.
func (m *Manifest) KnowledgeCollection() string {
	if m.Expert.KnowledgeCollection != "" {
		return m.Expert.KnowledgeCollection
	}
	return m.Slug
}
