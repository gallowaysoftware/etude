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

	"github.com/spf13/cobra"

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

	// --program is the one pipeline-shaping field the downstream rag
	// family also consumes (`rag run` feeds cfg.ProgramLabel into the
	// study-aid system prompts), so it stays a root persistent flag that
	// every subcommand inherits. Every OTHER pipeline-shaping flag is
	// meaningless to rag / requirements / doctor / viz / validate and
	// only clutters their --help, so those bind to the `run` subcommand
	// alone (see bindRunFlags). The factory closure reads cfg verbatim
	// regardless of where a flag is bound; values reach the pipeline via
	// WithDefault on each input, so a user can supply --source / --topic
	// on `run` OR override via --input lesson_root=... .
	root.PersistentFlags().StringVar(&cfg.ProgramLabel, "program", "",
		"Program / course this material belongs to (default 'this course').")

	// Scope the pipeline-shaping flags to the auto-registered `run`
	// command. Done before AddCommand(ragCmd) so the rag tree (which
	// is added below and has its own flags) never inherits them.
	if err := bindRunFlags(root); err != nil {
		fmt.Fprintln(os.Stderr, "textbook-to-audiobook:", err)
		os.Exit(1)
	}

	// Validate the user-facing flags before vamp's input check fires.
	// vamp surfaces a missing required input by its internal name
	// ("lesson_root", "module_topic"); a PreRun on `run` that checks the
	// bound cfg fields lets us name the actual CLI flag (--source,
	// --topic) the user must supply instead.
	bindRunRequiredCheck(root)

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

// runCommand returns the vamp-registered `run` subcommand, the only one
// that consumes the pipeline-shaping inputs. Returns an error if it's
// absent so a vamp API change surfaces loudly instead of silently
// dropping the flags.
func runCommand(root *cobra.Command) (*cobra.Command, error) {
	for _, c := range root.Commands() {
		if c.Name() == "run" {
			return c, nil
		}
	}
	return nil, fmt.Errorf("vamp did not register a 'run' subcommand")
}

// bindRunFlags attaches the pipeline-shaping flags to the `run`
// subcommand instead of the root, so they don't pollute the rag /
// requirements / doctor / viz / validate help. --program is bound on
// root separately because the rag family also consumes it.
func bindRunFlags(root *cobra.Command) error {
	run, err := runCommand(root)
	if err != nil {
		return err
	}
	f := run.Flags()
	f.StringVar(&cfg.LessonRoot, "source", "",
		"Directory containing Lesson_*/ subdirectories (required).")
	f.IntVar(&cfg.ModuleNum, "module-num", 1,
		"Numeric module identifier; used in cover seeds + output filenames.")
	f.StringVar(&cfg.ModuleTopic, "topic", "",
		"Short prose summary of the module (required).")
	f.StringVar(&cfg.SearchSuffix, "search-suffix", "",
		"Appended to every web-search query for topic enrichment.")
	f.StringVar(&cfg.Voice, "voice", "",
		"Kokoro voice (default af_bella).")
	f.StringVar(&cfg.SubjectLabel, "subject", "",
		"Field-of-study label (default 'the course subject').")
	f.StringVar(&cfg.ExpertPersona, "persona", "",
		"First-person identity the lecturer prompts adopt (default 'an experienced instructor').")
	f.StringVar(&cfg.AssessmentLabel, "assessment", "",
		"How learners are evaluated (default 'final assessment').")
	f.StringVar(&cfg.CoverPrompt, "cover-prompt", "",
		"SDXL prompt for the module cover. Templated.")
	return nil
}

// bindRunRequiredCheck installs a PreRunE on `run` that validates the
// user-facing required flags before vamp's input validation runs.
// Without it, a missing input surfaces by its internal vamp name
// (e.g. `input "lesson_root" is required`) rather than the CLI flag the
// user actually types (--source). vamp's run command sets no PreRunE,
// so there's nothing to chain.
func bindRunRequiredCheck(root *cobra.Command) {
	run, err := runCommand(root)
	if err != nil {
		// bindRunFlags already reported and exited on this; if we got
		// here the command exists, so this is unreachable in practice.
		return
	}
	run.PreRunE = func(cmd *cobra.Command, args []string) error {
		var missing []string
		if cfg.LessonRoot == "" {
			missing = append(missing, "--source")
		}
		if cfg.ModuleTopic == "" {
			missing = append(missing, "--topic")
		}
		if len(missing) > 0 {
			return fmt.Errorf("required flag(s) %v not set", missing)
		}
		return nil
	}
}
