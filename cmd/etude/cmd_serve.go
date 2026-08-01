package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/gallowaysoftware/etude/coach"
	"github.com/gallowaysoftware/etude/grade"
)

// drillServer adapts the shared drill contract (drill_api.go) to MCP
// tools. The tool layer adds nothing but transport: every shape is
// built by the same functions the JSON CLI prints.
type drillServer struct {
	deps   *drillDeps
	grader grade.Grader // nil unless the server owns grading (serve --grader-url)
	// now is a clock seam: scheduling is time-driven (a blindspot is due
	// minutes after the miss), so tests advance the clock instead of
	// sleeping.
	now func() time.Time
}

func serveCmd() *cobra.Command {
	var (
		courseDir   string
		addr        string
		useStdio    bool
		graderURL   string
		graderKey   string
		graderModel string
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the drill coach as MCP tools (stdio, or streamable HTTP with --addr)",
		Long: `Serve the drill coach as MCP tools: study_next_item, study_record_result,
study_report, study_gaps, study_coverage. The server's instructions are
the coach system prompt rendered from the course manifest.

The default transport is stdio, which is how agent harnesses spawn it.
--addr serves streamable HTTP instead; the bind defaults to loopback
because the server mutates learner state.

With --grader-url (or ETUDE_LLM_URL) the server also exposes
study_grade, graded by the server's own model — grading authority then
sits with the course owner, not the connecting client.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if courseDir == "" {
				return fmt.Errorf("--course is required")
			}
			if useStdio && addr != "" {
				return fmt.Errorf("--stdio and --addr are mutually exclusive")
			}
			deps, err := loadCoach(courseDir)
			if err != nil {
				return err
			}
			defer deps.Close()

			var grader grade.Grader
			if ep := resolveEndpoint(graderURL, graderKey, graderModel); ep.URL != "" {
				g, err := ep.grader()
				if err != nil {
					return err
				}
				grader = g
				fmt.Fprintf(os.Stderr, "etude: server-side grading enabled (model %s)\n", ep.Model)
			}

			srv := newDrillServer(deps, grader)
			if addr == "" {
				// stdio: the harness owns the process, and stdout carries
				// protocol frames only — every diagnostic goes to stderr.
				return srv.Run(cmd.Context(), &mcp.StdioTransport{})
			}
			return serveHTTP(cmd.Context(), srv, addr)
		},
	}
	f := cmd.Flags()
	f.StringVar(&courseDir, "course", "", "Course directory or course.yaml (required).")
	f.BoolVar(&useStdio, "stdio", false, "Serve MCP over stdio (the default).")
	f.StringVar(&addr, "addr", "", "Serve streamable HTTP on this address instead of stdio (empty host binds 127.0.0.1).")
	f.StringVar(&graderURL, "grader-url", "", "OpenAI-compatible endpoint for server-side grading (env ETUDE_LLM_URL); exposes study_grade.")
	f.StringVar(&graderKey, "grader-key", "", "API key for the grading endpoint (env ETUDE_LLM_API_KEY).")
	f.StringVar(&graderModel, "grader-model", "", "Grading model (env ETUDE_LLM_MODEL).")
	return cmd
}

// newDrillServer builds the MCP server over the coach. The instructions
// ARE the coach prompt: the client model reads them at connect time, so
// the relay-not-author contract travels with the tools.
func newDrillServer(deps *drillDeps, grader grade.Grader) *mcp.Server {
	return (&drillServer{deps: deps, grader: grader, now: time.Now}).server()
}

func (s *drillServer) server() *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "etude",
		Title:   "etude — drill coach for " + s.deps.Manifest.Title,
		Version: "dev",
	}, &mcp.ServerOptions{Instructions: coach.SystemPrompt(s.deps.Manifest)})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "study_next_item",
		Description: "Return the next drill item: the action (review | quiz_new | reverify | " +
			"introduce_new), the item to pose, a one-line instruction, and progress counts. " +
			"Pose the item's question VERBATIM — never invent or paraphrase a question. Collect " +
			"the learner's answer and a confidence (0-3) before revealing anything. The " +
			"grading_key is the official answer: grade only against it, and keep it private " +
			"until the learner has attempted.",
	}, s.studyNext)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "study_record_result",
		Description: "Record one graded attempt. Call exactly once per real answer the learner " +
			"gave, in the SAME message as the visible feedback (grade, hits/misses, correct " +
			"answer, review pointer) — this tool shows the learner nothing. quality is 0-5 vs " +
			"the grading_key (4+ = correct); confidence is 0-3, stated before the reveal. " +
			"Both accept a number or a quoted string.",
	}, s.studyRecord)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "study_report",
		Description: "Session-briefing view: tracked / mastered / due / outstanding blindspots, " +
			"strong and weak topics, and how many bank questions remain in scope.",
	}, s.studyReport)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "study_gaps",
		Description: "Weak spots ranked by exam risk: confident-but-wrong blindspots first, " +
			"then outright wrong, then shaky. Use it to steer the session.",
	}, s.studyGaps)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "study_coverage",
		Description: "Drill-through per unit, least-mastered first. Untouched units are the " +
			"silent risk — use this to keep the diagnostic sweep broad.",
	}, s.studyCoverage)

	if s.grader != nil {
		mcp.AddTool(srv, &mcp.Tool{
			Name: "study_grade",
			Description: "Grade a learner answer against a bank question's official key. This " +
				"server was started with its own grading model, so THIS tool is the " +
				"authoritative grader: prefer it over grading in your own reasoning. Record " +
				"the returned quality with study_record_result as usual.",
		}, s.studyGrade)
	}
	return srv
}

// Tool argument shapes. Quality/Confidence are `any` because models
// send both 4 and "4"; the advertised schema stays unrestricted and
// flexInt does the tolerant parse (a strict integer schema would reject
// quoted strings before the handler ever ran).

type nextArgs struct {
	Module string `json:"module,omitempty" jsonschema:"scope to one module ('module_2', 'M2', or '2'); omit to drill the whole course"`
}

type recordArgs struct {
	Topic      string `json:"topic" jsonschema:"the exact topic id from the served item (or a short stable concept name for a freeform teaching exchange)"`
	Module     string `json:"module,omitempty" jsonschema:"module label for freeform topics; official topics take their module from the bank"`
	Quality    any    `json:"quality" jsonschema:"recall quality 0-5 vs the grading_key (4+ = correct); number or quoted string"`
	Confidence any    `json:"confidence" jsonschema:"learner confidence 0-3, stated BEFORE the reveal; number or quoted string"`
	Note       string `json:"note,omitempty" jsonschema:"short, specific note on the gap"`
}

type moduleArgs struct {
	Module string `json:"module,omitempty" jsonschema:"scope to one module ('module_2', 'M2', or '2'); omit for the whole course"`
}

type gradeArgs struct {
	QuestionID    string `json:"question_id" jsonschema:"the bank question id (the item's topic)"`
	LearnerAnswer string `json:"learner_answer" jsonschema:"the learner's recalled answer, verbatim"`
}

func (s *drillServer) studyNext(_ context.Context, _ *mcp.CallToolRequest, in nextArgs) (*mcp.CallToolResult, nextResult, error) {
	return nil, nextItem(s.deps, in.Module, s.now()), nil
}

func (s *drillServer) studyRecord(_ context.Context, _ *mcp.CallToolRequest, in recordArgs) (*mcp.CallToolResult, recordResult, error) {
	quality, err := flexInt(in.Quality)
	if err != nil {
		return nil, recordResult{}, fmt.Errorf("quality: %w", err)
	}
	confidence, err := flexInt(in.Confidence)
	if err != nil {
		return nil, recordResult{}, fmt.Errorf("confidence: %w", err)
	}
	res, err := recordAttempt(s.deps, in.Topic, in.Module, quality, confidence, in.Note, s.now())
	if err != nil {
		return nil, recordResult{}, err
	}
	return nil, res, nil
}

func (s *drillServer) studyReport(_ context.Context, _ *mcp.CallToolRequest, in moduleArgs) (*mcp.CallToolResult, coach.Report, error) {
	return nil, s.deps.Coach.Report(in.Module, s.now()), nil
}

func (s *drillServer) studyGaps(_ context.Context, _ *mcp.CallToolRequest, in moduleArgs) (*mcp.CallToolResult, gapsResult, error) {
	return nil, gapsView(s.deps, in.Module), nil
}

func (s *drillServer) studyCoverage(_ context.Context, _ *mcp.CallToolRequest, in moduleArgs) (*mcp.CallToolResult, coverageResult, error) {
	return nil, coverageView(s.deps, in.Module), nil
}

func (s *drillServer) studyGrade(ctx context.Context, _ *mcp.CallToolRequest, in gradeArgs) (*mcp.CallToolResult, grade.Verdict, error) {
	v, err := gradeAnswer(ctx, s.deps, s.grader, in.QuestionID, in.LearnerAnswer)
	if err != nil {
		return nil, grade.Verdict{}, err
	}
	return nil, v, nil
}

// flexInt tolerates quality/confidence arriving as a JSON number or a
// quoted decimal string — models do both, and rejecting either just
// teaches the model to retry.
func flexInt(v any) (int, error) {
	switch t := v.(type) {
	case nil:
		return 0, fmt.Errorf("value is required")
	case float64:
		if t != float64(int(t)) {
			return 0, fmt.Errorf("must be a whole number, got %v", t)
		}
		return int(t), nil
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(t))
		if err != nil {
			return 0, fmt.Errorf("must be a whole number, got %q", t)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("must be a number or quoted string, got %T", v)
	}
}

// serveHTTP exposes the tools over streamable HTTP. An empty host binds
// loopback: the server mutates learner state, so a drill server
// reachable off-box by default would be a remote write to someone's
// study progress.
func serveHTTP(ctx context.Context, srv *mcp.Server, addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("--addr: %w", err)
	}
	if host == "" {
		host = "127.0.0.1"
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, port))
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "etude: serving MCP (streamable HTTP) at http://%s/mcp\n", ln.Addr())

	httpSrv := &http.Server{
		Handler:           mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		// The parent context is already cancelled, so shutdown gets its
		// own. A shutdown error only means connections were cut mid-
		// flight during teardown — nothing to act on.
		_ = httpSrv.Shutdown(context.Background())
	}()
	if err := httpSrv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
