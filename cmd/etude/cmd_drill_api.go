package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/spf13/cobra"
)

// drillAPICmd is the drill contract as a JSON CLI: one subcommand per
// coach operation, each printing EXACTLY ONE JSON object to stdout.
// Agent harnesses with no MCP drive this surface (the `etude skill`
// file teaches them how), so anything that isn't the result — warnings,
// endpoint chatter — goes to stderr. The shapes come from drill_api.go
// and are identical to the MCP tools'.
func drillAPICmd() *cobra.Command {
	// One --course for the whole tree; subcommands share the pointer so
	// the flag can precede or follow the subcommand name.
	var courseDir string
	api := &cobra.Command{
		Use:   "api",
		Short: "Drill coach as JSON commands (one object per call, for agent harnesses)",
		Long: `Each api subcommand performs one drill operation and prints exactly one
JSON object to stdout. Parse stdout; treat stderr as logs.`,
	}
	api.PersistentFlags().StringVar(&courseDir, "course", ".", "Course directory or course.yaml.")
	api.AddCommand(
		apiNextCmd(&courseDir),
		apiRecordCmd(&courseDir),
		apiReportCmd(&courseDir),
		apiCoverageCmd(&courseDir),
		apiGapsCmd(&courseDir),
	)
	return api
}

// withCoach runs one drill operation against the course and prints its
// result as the single JSON object on stdout.
func withCoach(courseDir string, out io.Writer, fn func(*drillDeps) (any, error)) error {
	deps, err := loadCoach(courseDir)
	if err != nil {
		return err
	}
	defer deps.Close()
	v, err := fn(deps)
	if err != nil {
		return err
	}
	// One Encode call, one object — the contract agents parse.
	return json.NewEncoder(out).Encode(v)
}

func apiNextCmd(courseDir *string) *cobra.Command {
	var module string
	cmd := &cobra.Command{
		Use:   "next",
		Short: "Return the next drill item (review | quiz_new | reverify | introduce_new)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withCoach(*courseDir, cmd.OutOrStdout(), func(d *drillDeps) (any, error) {
				return nextItem(d, module, time.Now()), nil
			})
		},
	}
	cmd.Flags().StringVar(&module, "module", "", "Scope to one module (e.g. 2, M2, module_2).")
	return cmd
}

func apiRecordCmd(courseDir *string) *cobra.Command {
	var topic, module, quality, confidence, note string
	cmd := &cobra.Command{
		Use:   "record",
		Short: "Record one graded attempt (quality 0-5, confidence 0-3)",
		Long: `Record exactly one graded answer. The visible feedback to the learner must
happen in the same turn as this call — the call itself shows them nothing.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if topic == "" {
				return fmt.Errorf("--topic is required")
			}
			q, err := scoreFlag("--quality", quality, 5)
			if err != nil {
				return err
			}
			c, err := scoreFlag("--confidence", confidence, 3)
			if err != nil {
				return err
			}
			return withCoach(*courseDir, cmd.OutOrStdout(), func(d *drillDeps) (any, error) {
				return recordAttempt(d, topic, module, q, c, note, time.Now())
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&topic, "topic", "", "Topic id from the item (or a stable concept name for freeform).")
	f.StringVar(&module, "module", "", "Module label (freeform topics only; official topics take the bank's).")
	// Strings, not ints: 0 is a real score (a blank answer), so presence
	// must be checked before parsing.
	f.StringVar(&quality, "quality", "", "Recall quality 0-5 vs the grading key (4+ = correct).")
	f.StringVar(&confidence, "confidence", "", "Learner confidence 0-3, stated before the reveal.")
	f.StringVar(&note, "note", "", "Short, specific note on the gap.")
	return cmd
}

func apiReportCmd(courseDir *string) *cobra.Command {
	var module string
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Session-briefing view (tracked / mastered / due / blindspots)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withCoach(*courseDir, cmd.OutOrStdout(), func(d *drillDeps) (any, error) {
				return d.Coach.Report(module, time.Now()), nil
			})
		},
	}
	cmd.Flags().StringVar(&module, "module", "", "Scope to one module.")
	return cmd
}

func apiCoverageCmd(courseDir *string) *cobra.Command {
	var module string
	cmd := &cobra.Command{
		Use:   "coverage",
		Short: "Drill-through per unit, least-mastered first",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withCoach(*courseDir, cmd.OutOrStdout(), func(d *drillDeps) (any, error) {
				return coverageView(d, module), nil
			})
		},
	}
	cmd.Flags().StringVar(&module, "module", "", "Scope to one module.")
	return cmd
}

func apiGapsCmd(courseDir *string) *cobra.Command {
	var module string
	cmd := &cobra.Command{
		Use:   "gaps",
		Short: "Weak spots ranked by exam risk (blindspots first)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withCoach(*courseDir, cmd.OutOrStdout(), func(d *drillDeps) (any, error) {
				return gapsView(d, module), nil
			})
		},
	}
	cmd.Flags().StringVar(&module, "module", "", "Scope to one module.")
	return cmd
}

// scoreFlag parses a required score flag, rejecting out-of-range values
// loudly instead of letting the store clamp a typo into a wrong grade.
func scoreFlag(name, raw string, max int) (int, error) {
	if raw == "" {
		return 0, fmt.Errorf("%s is required", name)
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a whole number 0-%d, got %q", name, max, raw)
	}
	if n < 0 || n > max {
		return 0, fmt.Errorf("%s must be 0-%d, got %d", name, max, n)
	}
	return n, nil
}
