// Package pipeline holds the textbook-to-audiobook pipeline definition,
// its embedded prompts, and the Config struct that pipeline binaries
// derive from CLI flags. The DSL graph lives in pipeline.go; the
// configurable, opinion-bearing fields (subject voice, cover art prompt,
// EPUB metadata, output filename templates) all live here so a fork can
// reskin the pipeline for a different curriculum without touching the
// graph.
package pipeline

// Config captures every fork-tunable knob in the textbook-to-audiobook
// pipeline. CLI flags on the binary's root command write into a Config,
// and the pipeline factory pushes each field into the vamp pipeline's
// inputs map so prompts can reference them via `{{ .inputs.<name> }}`.
//
// Defaults are tuned for a generic course: short, neutral terminology;
// a neutral cover prompt; the af_bella Kokoro voice (A-grade en-US
// female narration that lectures cleanly). A fork that wants the
// CIBD-distilling flavour overrides the descriptive fields and the
// cover prompt; see examples/cibd for a worked example.
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
	// "distilling spirits practical example"). Empty disables the
	// suffix.
	SearchSuffix string

	// Voice names the Kokoro voice the audio stage uses (e.g.
	// af_bella, am_adam). See Kokoro-FastAPI's voice catalogue.
	Voice string

	// SubjectLabel is the field-of-study label woven into every prompt
	// (e.g. "distilling", "introductory physics", "constitutional
	// law"). Drives how the model frames its writing voice.
	SubjectLabel string
	// ProgramLabel names the program / certification / course this
	// material belongs to (e.g. "CIBD certification course",
	// "Stanford CS103"). Used to anchor the lecturer's persona.
	ProgramLabel string
	// ExpertPersona is the first-person identity the lecturer prompts
	// adopt (e.g. "an expert distiller", "a constitutional law
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
	// Default: "module_<N>.m4b".
	AudiobookFilenameTemplate string
	// UnitMP3FilenameTemplate renders each per-unit MP3 filename.
	// Default: "module_<N>_<unit_id>.mp3".
	UnitMP3FilenameTemplate string
	// StudyGuideFilenameTemplate renders the markdown study guide
	// filename. Default: "module_<N>_study_guide.md".
	StudyGuideFilenameTemplate string
	// EPUBFilenameTemplate renders the EPUB study guide filename.
	// Default: "module_<N>_study_guide.epub".
	EPUBFilenameTemplate string
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
	if c.EPUBTitleTemplate == "" {
		c.EPUBTitleTemplate = "{{ .inputs.program_label }} — Module {{ .inputs.module_num }}: {{ .inputs.module_topic }} (Study Guide)"
	}
	if c.EPUBAuthor == "" {
		c.EPUBAuthor = "Generated by textbook-to-audiobook"
	}
	if c.AudiobookFilenameTemplate == "" {
		c.AudiobookFilenameTemplate = "module_{{ .inputs.module_num }}.m4b"
	}
	if c.UnitMP3FilenameTemplate == "" {
		c.UnitMP3FilenameTemplate = "module_{{ .inputs.module_num }}_{{ .parent.parent_unit_id }}.mp3"
	}
	if c.StudyGuideFilenameTemplate == "" {
		c.StudyGuideFilenameTemplate = "module_{{ .inputs.module_num }}_study_guide.md"
	}
	if c.EPUBFilenameTemplate == "" {
		c.EPUBFilenameTemplate = "module_{{ .inputs.module_num }}_study_guide.epub"
	}
	return c
}
