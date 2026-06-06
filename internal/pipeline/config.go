// Package pipeline holds the textbook-to-audiobook pipeline definition,
// its embedded prompts, and the Config struct that pipeline binaries
// derive from CLI flags. The DSL graph lives in pipeline.go; the
// configurable, opinion-bearing fields (subject voice, cover art prompt,
// EPUB metadata, output filename templates) all live here so a fork can
// reskin the pipeline for a different curriculum without touching the
// graph.
package pipeline

import (
	"fmt"
	"strings"
)

// Config captures every fork-tunable knob in the textbook-to-audiobook
// pipeline. CLI flags on the binary's root command write into a Config,
// and the pipeline factory pushes each field into the vamp pipeline's
// inputs map so prompts can reference them via `{{ .inputs.<name> }}`.
//
// Defaults are tuned for a generic course: short, neutral terminology;
// a neutral cover prompt; the af_bella Kokoro voice (A-grade en-US
// female narration that lectures cleanly). A fork for a specific
// curriculum overrides the descriptive fields and the cover prompt;
// see examples/example-course for a worked example.
type Config struct {
	// ---- Required at run time (no defaults) ----------------------------

	// LessonRoot is the absolute or relative path to the directory that
	// holds one subdirectory per lesson (each containing a lesson.md
	// and optionally an images/ subdir). The pipeline globs
	// Lesson_*/ under this path.
	LessonRoot string
	// ModuleNum is the numeric identifier for the module being built —
	// used in cover-art seeds and output filenames so multi-module
	// runs don't collide.
	ModuleNum int
	// ModuleTopic is a short prose summary of the module's subject
	// (e.g. "fermentation and wort production"). Used in cover-art
	// prompts and EPUB titles.
	ModuleTopic string

	// ---- Optional / defaults-supplied ----------------------------------

	// OutputDir is the run directory the pipeline writes into. Empty
	// lets vamp pick a timestamped dir under
	// $XDG_STATE_HOME/vamp/runs/.
	OutputDir string

	// SearchSuffix is appended to every web-search query (e.g.
	// "astronomy worked example"). Empty disables the suffix.
	SearchSuffix string

	// Voice names the Kokoro voice the audio stage uses (e.g.
	// af_bella, am_adam). See Kokoro-FastAPI's voice catalogue.
	Voice string

	// SubjectLabel is the field-of-study label woven into every prompt
	// (e.g. "introductory astronomy", "introductory physics",
	// "constitutional law"). Drives how the model frames its writing voice.
	SubjectLabel string
	// ProgramLabel names the program / certification / course this
	// material belongs to (e.g. "an introductory astronomy course",
	// "Stanford CS103"). Used to anchor the lecturer's persona.
	ProgramLabel string
	// ExpertPersona is the first-person identity the lecturer prompts
	// adopt (e.g. "an astronomy lecturer", "a constitutional law
	// scholar"). The full prompt prefixes "You are " in front of this.
	ExpertPersona string
	// AssessmentLabel names how learners will be evaluated on the
	// material (e.g. "graded exam", "course final", "qualifying
	// review"). Drives the "exam pointer" callout language.
	AssessmentLabel string

	// CoverPrompt is the SDXL prompt rendered for the module cover.
	// Templated against {{ .inputs.module_num }} and
	// {{ .inputs.module_topic }} at run time.
	CoverPrompt string

	// EPUBTitleTemplate renders the EPUB's title metadata. Templated
	// against {{ .inputs.module_num }} and {{ .inputs.module_topic }}.
	// Default produces "<ProgramLabel> Module <N> — <topic> (Study
	// Guide)".
	EPUBTitleTemplate string
	// EPUBAuthor sets the EPUB's author metadata. Default credits the
	// pipeline rather than the operator.
	EPUBAuthor string

	// AudiobookFilenameTemplate renders the M4B output filename.
	// Default: "<topic-slug>.m4b" (or module_<N>.m4b when --topic is empty).
	AudiobookFilenameTemplate string
	// UnitMP3FilenameTemplate renders each per-unit MP3 filename.
	// Default: "<topic-slug>_<parent_unit_id>.mp3" (or
	// module_<N>_<parent_unit_id>.mp3 when --topic is empty).
	UnitMP3FilenameTemplate string
	// StudyGuideFilenameTemplate renders the markdown study guide
	// filename. Default: "<topic-slug>_study_guide.md" (or
	// module_<N>_study_guide.md when --topic is empty).
	StudyGuideFilenameTemplate string
	// EPUBFilenameTemplate renders the EPUB study guide filename.
	// Default: "<topic-slug>_study_guide.epub" (or
	// module_<N>_study_guide.epub when --topic is empty).
	EPUBFilenameTemplate string

	// FilenamePrefix is the basename stem used by the default
	// filename templates ("<prefix>.m4b", "<prefix>_<unit>.mp3",
	// "<prefix>_study_guide.md", "<prefix>_study_guide.epub").
	// Default behaviour: derived from ModuleTopic (e.g. "Transformers
	// and Attention" → "transformers_and_attention") so deliverables
	// are immediately findable in a multi-topic library. Override
	// for series-numbered material ("module_1") or any custom shape.
	FilenamePrefix string
}

// WithDefaults returns a copy of c with every empty optional field
// populated from the generic defaults. The pipeline factory calls this
// before pushing values into pipeline inputs so authors never have to
// supply the boilerplate fields.
func (c Config) WithDefaults() Config {
	if c.Voice == "" {
		c.Voice = "af_bella"
	}
	if c.SubjectLabel == "" {
		c.SubjectLabel = "the course subject"
	}
	if c.ProgramLabel == "" {
		c.ProgramLabel = "this course"
	}
	if c.ExpertPersona == "" {
		c.ExpertPersona = "an experienced instructor"
	}
	if c.AssessmentLabel == "" {
		c.AssessmentLabel = "final assessment"
	}
	if c.CoverPrompt == "" {
		c.CoverPrompt = "Tasteful textbook cover illustration, themed around the course subject, painterly style, no text, no labels, no people, focal subject centered. Module {{ .inputs.module_num }}: {{ .inputs.module_topic }}."
	}
	if c.FilenamePrefix == "" {
		// Topic slug if the caller supplied one; otherwise fall
		// back to module_N for numbered series.
		if c.ModuleTopic != "" {
			c.FilenamePrefix = filenameSlug(c.ModuleTopic)
		} else {
			c.FilenamePrefix = fmt.Sprintf("module_%d", c.ModuleNum)
		}
	}
	if c.EPUBTitleTemplate == "" {
		// Composed at WithDefaults time using cfg values, NOT as a
		// Go template — Pandoc's --metadata title= takes a literal
		// string, not a template, so a template reference here ends
		// up in the EPUB verbatim. cfg fields are already resolved
		// by this point so a plain Sprintf is the right tool.
		topic := c.ModuleTopic
		if topic == "" {
			topic = "the course material"
		}
		c.EPUBTitleTemplate = fmt.Sprintf("%s (Study Guide)", titleCase(topic))
	}
	if c.EPUBAuthor == "" {
		c.EPUBAuthor = "Generated by textbook-to-audiobook"
	}
	if c.AudiobookFilenameTemplate == "" {
		c.AudiobookFilenameTemplate = c.FilenamePrefix + ".m4b"
	}
	if c.UnitMP3FilenameTemplate == "" {
		c.UnitMP3FilenameTemplate = c.FilenamePrefix + "_{{ .parent.parent_unit_id }}.mp3"
	}
	if c.StudyGuideFilenameTemplate == "" {
		c.StudyGuideFilenameTemplate = c.FilenamePrefix + "_study_guide.md"
	}
	if c.EPUBFilenameTemplate == "" {
		c.EPUBFilenameTemplate = c.FilenamePrefix + "_study_guide.epub"
	}
	return c
}

// filenameSlug turns a free-form topic into a filesystem-safe
// basename stem: lowercase, [a-z0-9_]+ only, max 60 chars.
func filenameSlug(s string) string {
	out := make([]rune, 0, len(s))
	prevUnderscore := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out = append(out, r)
			prevUnderscore = false
		case r >= 'A' && r <= 'Z':
			out = append(out, r+('a'-'A'))
			prevUnderscore = false
		case r == ' ', r == '_', r == '-':
			if len(out) > 0 && !prevUnderscore {
				out = append(out, '_')
				prevUnderscore = true
			}
		}
	}
	for len(out) > 0 && out[len(out)-1] == '_' {
		out = out[:len(out)-1]
	}
	if len(out) > 60 {
		out = out[:60]
	}
	if len(out) == 0 {
		return "untitled"
	}
	return string(out)
}

// titleCase is a small word-capitaliser for the EPUB title: turns
// "transformers and attention" into "Transformers and Attention".
// Articles/conjunctions stay lowercase except at the start.
func titleCase(s string) string {
	smalls := map[string]bool{
		"a": true, "an": true, "the": true, "and": true, "or": true,
		"but": true, "for": true, "nor": true, "on": true, "at": true,
		"to": true, "by": true, "of": true, "in": true, "with": true,
	}
	parts := strings.Fields(s)
	for i, p := range parts {
		lower := strings.ToLower(p)
		if i > 0 && smalls[lower] {
			parts[i] = lower
		} else if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + strings.ToLower(p[1:])
		}
	}
	return strings.Join(parts, " ")
}
