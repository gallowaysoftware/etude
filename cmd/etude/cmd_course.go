package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/gallowaysoftware/etude/course"
)

// initCmd scaffolds a new course directory. It is the first command a
// new user runs, so it optimises for "the file explains itself": the
// generated manifest and sample lesson carry the format documentation
// inline, and every value the author must decide is a REPLACE-
// placeholder that validation rejects until it is filled in.
func initCmd() *cobra.Command {
	var opts course.ScaffoldOptions
	var sample bool

	cmd := &cobra.Command{
		Use:   "init [dir]",
		Short: "Scaffold a new course directory (course.yaml + module tree).",
		Long: `init creates a course skeleton: a course.yaml manifest, one directory
per module, and (unless --no-sample) an example lesson that documents the
lesson format inline.

The scaffolded manifest carries REPLACE- placeholders for every value you
must decide. 'etude course validate' refuses to load a manifest that still
has one, so a half-filled course fails loudly instead of producing
generically-worded lectures.

If your material is not already split into lessons — a book, a PDF export,
a pile of documents — scaffold the course first, then use 'etude ingest' to
propose a lesson breakdown from the source material.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Dir = "."
			if len(args) == 1 {
				opts.Dir = args[0]
			}
			opts.SampleLesson = sample

			created, err := course.Scaffold(opts)
			if err != nil {
				return err
			}
			for _, p := range created {
				fmt.Fprintln(cmd.OutOrStdout(), p)
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"\nNext: fill in the REPLACE- values in %s, then run 'etude course validate %s'.\n",
				filepath.Join(opts.Dir, course.FileName), opts.Dir)
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVar(&opts.Slug, "slug", "", "Course slug (lowercase letters, digits, hyphens).")
	f.StringVar(&opts.Title, "title", "", "Human-readable course title.")
	f.IntVar(&opts.Modules, "modules", 1, "Number of module directories to stub out.")
	f.StringVar(&opts.Preset, "preset", "none", "Assessment-marker preset: none, saq.")
	f.BoolVar(&sample, "sample", true, "Write an example lesson documenting the format.")
	f.BoolVar(&opts.Force, "force", false, "Overwrite an existing course.yaml.")
	return cmd
}

// courseCmd is the umbrella for manifest-level operations.
func courseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "course",
		Short: "Inspect and validate a course manifest and its lesson tree.",
	}
	cmd.AddCommand(courseValidateCmd())
	cmd.AddCommand(courseShowCmd())
	return cmd
}

// courseValidateCmd checks a course before a multi-hour build spends GPU
// time on it. Its real job is catching SILENT losses: the pipeline drops
// a lesson directory with no lesson.md, drops an oversized lesson.md,
// filters oversized files out of batch prompt context, and orders
// lessons lexically — none of which announce themselves at run time.
func courseValidateCmd() *cobra.Command {
	var strict bool
	var courseDir string

	cmd := &cobra.Command{
		Use:   "validate [dir]",
		Short: "Validate a course manifest and lesson tree.",
		Long: `validate checks the manifest (identity, required labels, module list,
leftover placeholders) and then walks the lesson tree looking for content
that would vanish from a build.

The tree checks matter because the pipeline fails silently: a lesson
directory without a lesson.md, or with one over 1 MiB, is dropped from the
course without a word, and a lesson.md over 200 KiB is filtered out of
corpus-wide prompt context. A clean validate means nothing disappears.

Exits non-zero on errors. Warnings are advisory unless --strict.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := courseDir
			if dir == "" {
				dir = "."
			}
			if len(args) == 1 {
				if courseDir != "" {
					return fmt.Errorf("course given twice: positional %q and --course %q", args[0], courseDir)
				}
				dir = args[0]
			}

			m, err := course.Load(dir)
			if err != nil {
				return err
			}

			rep := m.ScanTree()
			out := cmd.OutOrStdout()
			for _, f := range rep.Findings {
				fmt.Fprintln(out, f)
			}

			var lessons, images, assessment int
			for _, mod := range rep.Modules {
				fmt.Fprintf(out, "module %d (%s): %d lesson(s), %d image(s)",
					mod.Num, mod.Dir, mod.Lessons, mod.Images)
				if mod.Assessment > 0 {
					fmt.Fprintf(out, ", %d with assessment material", mod.Assessment)
				}
				fmt.Fprintln(out)
				lessons += mod.Lessons
				images += mod.Images
				assessment += mod.Assessment
			}
			fmt.Fprintf(out, "%s: %d module(s), %d lesson(s), %d image(s)\n",
				m.Slug, len(rep.Modules), lessons, images)

			if m.HasAssessmentMaterial() && assessment == 0 {
				fmt.Fprintf(out, "%s\n", course.Finding{
					Severity: course.Warning,
					Message:  "assessment markers are configured but no lesson matched them — the drill will have no questions to extract",
				})
				rep.Findings = append(rep.Findings, course.Finding{Severity: course.Warning})
			}

			switch {
			case rep.Errors() > 0:
				return fmt.Errorf("%d error(s), %d warning(s)", rep.Errors(), rep.Warnings())
			case strict && rep.Warnings() > 0:
				return fmt.Errorf("%d warning(s) with --strict", rep.Warnings())
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&strict, "strict", false, "Treat warnings as errors.")
	cmd.Flags().StringVar(&courseDir, "course", "", "Course directory (alternative to the positional argument, matching run --course).")
	return cmd
}

// courseShowCmd prints what the manifest resolves to. The slug bindings
// are the point: one declared slug becomes the memory expert and the
// knowledge collection, and seeing them side by side is how an author
// catches a drifted name before three services disagree.
func courseShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show [dir]",
		Short: "Print the resolved manifest, including service bindings.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			m, err := course.Load(dir)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "slug:                 %s\n", m.Slug)
			fmt.Fprintf(out, "title:                %s\n", m.Title)
			fmt.Fprintf(out, "subject:              %s\n", m.Subject)
			fmt.Fprintf(out, "program:              %s\n", m.Program)
			fmt.Fprintf(out, "persona:              %s\n", m.Persona)
			fmt.Fprintf(out, "assessment:           %s\n", m.Assessment)
			fmt.Fprintf(out, "source:               %s\n", m.SourceDir())
			fmt.Fprintf(out, "memory expert:        %s\n", m.MemorySlug())
			fmt.Fprintf(out, "knowledge collection: %s\n", m.KnowledgeCollection())
			if m.HasAssessmentMaterial() {
				a := m.Assessments.Resolved()
				fmt.Fprintf(out, "assessment markers:   preset=%s lessons=%q question=%q answer=%q\n",
					a.Preset, a.LessonPattern, a.QuestionHeading, a.AnswerHeading)
			} else {
				fmt.Fprintf(out, "assessment markers:   none (questions must be generated and reviewed)\n")
			}
			for _, mod := range m.Modules {
				fmt.Fprintf(out, "module %d:             %s — %s\n", mod.Num, m.ModuleDir(mod), mod.Topic)
			}
			return nil
		},
	}
}

// applyCourse overlays a manifest onto the pipeline Config so a build
// driven by course.yaml and one driven by flags produce identical
// inputs. Flags win: a manifest is the course's stable identity, but an
// operator overriding one value on the command line should not have to
// edit the file. ModuleNum is deliberately untouched — it is the
// selector that chose mod in the first place.
func applyCourse(m *course.Manifest, mod course.Module) error {
	if cfg.LessonRoot == "" {
		cfg.LessonRoot = m.ModuleDir(mod)
	}
	if cfg.ModuleTopic == "" {
		cfg.ModuleTopic = mod.Topic
	}
	if cfg.SubjectLabel == "" {
		cfg.SubjectLabel = m.Subject
	}
	if cfg.ProgramLabel == "" {
		cfg.ProgramLabel = m.Program
	}
	if cfg.ExpertPersona == "" {
		cfg.ExpertPersona = m.Persona
	}
	if cfg.AssessmentLabel == "" {
		cfg.AssessmentLabel = m.Assessment
	}
	if cfg.Voice == "" {
		cfg.Voice = m.Render.Voice
	}
	if cfg.CoverPrompt == "" {
		cfg.CoverPrompt = m.Render.CoverPrompt
	}
	if cfg.SearchSuffix == "" {
		cfg.SearchSuffix = m.Render.SearchSuffix
	}
	if cfg.EPUBTitleTemplate == "" {
		cfg.EPUBTitleTemplate = m.Render.EPUBTitle
	}
	if cfg.EPUBAuthor == "" {
		cfg.EPUBAuthor = m.Render.EPUBAuthor
	}
	if cfg.FilenamePrefix == "" {
		cfg.FilenamePrefix = m.Render.FilenamePrefix
	}
	if _, err := os.Stat(cfg.LessonRoot); err != nil {
		return fmt.Errorf("module %d: %w", mod.Num, err)
	}
	return nil
}
