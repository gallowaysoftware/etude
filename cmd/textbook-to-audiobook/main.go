// Command textbook-to-audiobook turns a directory of structured lessons
// (markdown + diagrams) into a chapterised audiobook (M4B), per-unit
// MP3s, a markdown study guide, and an EPUB companion.
//
// It is a Go-DSL pipeline binary built against
// github.com/gallowaysoftware/vibe/vamp: vibe supervises the model
// backends (LLM, vision, TTS, image-gen) under capability profiles, and
// vamp orchestrates the DAG. Run `textbook-to-audiobook requirements`
// to see what services + capabilities the host must provide.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/gallowaysoftware/vibe/vamp"

	"github.com/gallowaysoftware/textbook-to-audiobook/pipeline"
)

// cfg is populated by Cobra flag binding on the root command before any
// subcommand's RunE fires. The pipeline factory closure (registered with
// vamp.BuildRoot) reads from cfg at run time so the user's --source,
// --topic, --module-num, etc. flow into pipeline inputs without
// requiring an explicit --input key=value invocation.
var cfg pipeline.Config

func main() {
	root, err := vamp.BuildRoot(func() (*vamp.Pipeline, error) {
		return pipeline.Build(cfg)
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "textbook-to-audiobook:", err)
		os.Exit(1)
	}

	root.Use = "textbook-to-audiobook"
	root.Short = "Generate an audiobook + EPUB study guide from a directory of structured lessons."
	root.Long = `textbook-to-audiobook turns a directory of structured lessons (one
subdirectory per lesson, each containing a lesson.md and optional
images/) into a chapterised audiobook (M4B), per-unit MP3s, a markdown
study guide, and an EPUB companion.

Backed by github.com/gallowaysoftware/vibe — vibe supervises the model
profiles (long_form text, vision, TTS, image-gen), the host must have a
running daemon and the right capabilities mapped. Run the
'requirements' subcommand to see what's needed.`

	// First-class flags. PersistentFlags so every subcommand inherits.
	// The factory closure above reads cfg verbatim; the values reach the
	// pipeline via WithDefault on each input, so a user can either
	// supply --source / --topic at the top level OR override via
	// --input lesson_root=... on the run subcommand.
	root.PersistentFlags().StringVar(&cfg.LessonRoot, "source", "",
		"Directory containing Lesson_*/ subdirectories (required).")
	root.PersistentFlags().IntVar(&cfg.ModuleNum, "module-num", 1,
		"Numeric module identifier; used in cover seeds + output filenames.")
	root.PersistentFlags().StringVar(&cfg.ModuleTopic, "topic", "",
		"Short prose summary of the module (required).")
	root.PersistentFlags().StringVar(&cfg.SearchSuffix, "search-suffix", "",
		"Appended to every web-search query for topic enrichment.")
	root.PersistentFlags().StringVar(&cfg.Voice, "voice", "",
		"Kokoro voice (default af_bella).")
	root.PersistentFlags().StringVar(&cfg.SubjectLabel, "subject", "",
		"Field-of-study label (default 'the course subject').")
	root.PersistentFlags().StringVar(&cfg.ProgramLabel, "program", "",
		"Program / course this material belongs to (default 'this course').")
	root.PersistentFlags().StringVar(&cfg.ExpertPersona, "persona", "",
		"First-person identity the lecturer prompts adopt (default 'an experienced instructor').")
	root.PersistentFlags().StringVar(&cfg.AssessmentLabel, "assessment", "",
		"How learners are evaluated (default 'final assessment').")
	root.PersistentFlags().StringVar(&cfg.CoverPrompt, "cover-prompt", "",
		"SDXL prompt for the module cover. Templated.")

	// `rag` is a strict downstream of the textbook pipeline — it
	// consumes processed_lessons.json and produces the RAG export.
	// Lives alongside vamp's auto-registered `run` so the user has
	// a single binary for both flows.
	root.AddCommand(ragCmd())

	// vamp.BuildRoot leaves SilenceErrors false, so Cobra prints the
	// error from Execute AND the block below prints it again. Silence
	// Cobra's copy and keep the prefixed reporter here as the single
	// printer.
	root.SilenceErrors = true

	// A signal-aware context so the rag subcommands (which thread
	// cmd.Context() into long-running HTTP/LLM/embed loops) unwind on
	// Ctrl-C with a final checkpoint flush instead of a hard abort.
	// vamp's own `run` re-wraps its own signal context, so it is
	// unaffected.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "textbook-to-audiobook:", err)
		os.Exit(1)
	}
}
