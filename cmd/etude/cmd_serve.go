package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/gallowaysoftware/etude/coach"
	"github.com/gallowaysoftware/etude/grade"
	"github.com/gallowaysoftware/etude/internal/refrain"
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
	// em publishes the mastery digest to refrain; nil when memory is
	// unreachable (every emission then silently skips). emitMu guards
	// the cadence counters: MCP handlers may run concurrently, and two
	// records crossing the cadence boundary together must not
	// double-emit.
	em      *refrain.Emitter
	emitMu  sync.Mutex
	records int // attempts recorded this run
	flushed int // attempts covered by the last digest emission
}

func serveCmd() *cobra.Command {
	var (
		courseDir   string
		addr        string
		useStdio    bool
		persona     string
		graderURL   string
		graderKey   string
		graderModel string
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve a course persona as MCP tools (stdio, or streamable HTTP with --addr)",
		Long: `Serve a course persona as MCP tools. --persona selects which one:

coach (default): the drill coach — study_next_item, study_record_result,
study_report, study_gaps, study_coverage. The server's instructions are
the coach system prompt rendered from the course manifest.

expert: the subject-expert tutor. Its instructions are the expert prompt
rendered from the course manifest, and its toolset is READ-ONLY over
learner state (study_report, study_gaps, study_coverage) — the expert
teaches; recording results is the drill's job. Any state-mutating tool
the expert ever gains runs behind the two-call confirm gate enforced in
code, not in prompt text.

The default transport is stdio, which is how agent harnesses spawn it.
--addr serves streamable HTTP instead; the bind defaults to loopback
because the server mutates learner state.

With --grader-url (or ETUDE_LLM_URL) the coach server also exposes
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
				if persona == personaExpert {
					// The expert exposes no grading or recording tools, so a
					// grader endpoint would be silently dead config — fail
					// loudly instead of letting it look wired up.
					return fmt.Errorf("--grader-url applies to the coach persona, not expert")
				}
				g, err := ep.grader()
				if err != nil {
					return err
				}
				grader = g
				fmt.Fprintf(os.Stderr, "etude: server-side grading enabled (model %s)\n", ep.Model)
			}

			var srv *mcp.Server
			var ds *drillServer
			switch persona {
			case personaCoach:
				// Only the coach records, so only the coach emits. Probe
				// memory once: unreachable refrain costs one stderr note
				// here and silence at every emission point after.
				em := connectRefrain(cmd.Context(), os.Stderr, deps.Manifest.MemorySlug())
				defer func() { _ = em.Close() }()
				ds, srv = newDrillServer(deps, grader, em)
			case personaExpert:
				srv = newExpertServer(deps)
			default:
				return fmt.Errorf("--persona must be %q or %q, got %q", personaCoach, personaExpert, persona)
			}
			var runErr error
			if addr == "" {
				// stdio: the harness owns the process, and stdout carries
				// protocol frames only — every diagnostic goes to stderr.
				runErr = srv.Run(cmd.Context(), &mcp.StdioTransport{})
			} else {
				runErr = serveHTTP(cmd.Context(), srv, addr)
			}
			// Whatever the cadence has not covered yet is still valid
			// learner state: flush the trailing partial batch on the way
			// out (SIGINT/SIGTERM cancel the context; serveHTTP and Run
			// both return nil on that path).
			if ds != nil {
				ds.flushOnShutdown()
			}
			return runErr
		},
	}
	f := cmd.Flags()
	f.StringVar(&courseDir, "course", "", "Course directory or course.yaml (required).")
	f.BoolVar(&useStdio, "stdio", false, "Serve MCP over stdio (the default).")
	f.StringVar(&addr, "addr", "", "Serve streamable HTTP on this address instead of stdio (empty host binds 127.0.0.1).")
	f.StringVar(&persona, "persona", personaCoach, "Which persona to serve: coach (drill) or expert (read-only tutor).")
	f.StringVar(&graderURL, "grader-url", "", "OpenAI-compatible endpoint for server-side grading (env ETUDE_LLM_URL); exposes study_grade.")
	f.StringVar(&graderKey, "grader-key", "", "API key for the grading endpoint (env ETUDE_LLM_API_KEY).")
	f.StringVar(&graderModel, "grader-model", "", "Grading model (env ETUDE_LLM_MODEL).")
	return cmd
}

// Persona names for serve --persona. The coach drills (it owns
// scheduling, grading, and recording); the expert tutors (read-only
// over learner state).
const (
	personaCoach  = "coach"
	personaExpert = "expert"
)

// newDrillServer builds the coach-persona drillServer and its MCP
// server. The instructions ARE the coach prompt: the client model reads
// them at connect time, so the relay-not-author contract travels with
// the tools.
func newDrillServer(deps *drillDeps, grader grade.Grader, em *refrain.Emitter) (*drillServer, *mcp.Server) {
	ds := &drillServer{deps: deps, grader: grader, now: time.Now, em: em}
	return ds, ds.server()
}

// newExpertServer builds the MCP server for the subject-expert persona:
// the expert prompt as instructions, and the read-only toolset.
func newExpertServer(deps *drillDeps) *mcp.Server {
	return (&drillServer{deps: deps, now: time.Now}).expertServer()
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

// expertServer builds the subject-expert server. The instructions are
// the expert prompt; the toolset is exactly what expertToolset lists —
// read-only over learner state by default, with the confirm gate as the
// only path a write tool can ever take in.
func (s *drillServer) expertServer() *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "etude",
		Title:   "etude — subject expert for " + s.deps.Manifest.Title,
		Version: "dev",
	}, &mcp.ServerOptions{Instructions: coach.ExpertPrompt(s.deps.Manifest)})

	tools := s.expertToolset()
	for _, reg := range tools.readOnly {
		reg(srv)
	}
	for _, w := range tools.writes {
		w.reg(srv)
	}
	return srv
}

// toolReg registers one tool on a server. The expert toolset is a list
// of registrations rather than inline AddTool calls so the expert's
// capabilities are a value you can audit in one place.
type toolReg func(srv *mcp.Server)

// gatedWrite is a state-mutating expert tool. Its only constructor is
// newGatedWrite, which routes the handler through the confirm gate — so
// joining the expert toolset as a write REQUIRES the gate. That is a
// type-level property, not a comment: a handler added to readOnly
// cannot compile its way into writes, and a write built any other way
// does not type-check.
type gatedWrite struct{ reg toolReg }

// expertToolset is the subject expert's toolset. readOnly tools answer
// questions and change nothing; writes run only behind the two-call
// confirm gate. The gate lives in code because the corpus is untrusted
// input: prompt text can be talked around by injected content, a missing
// token cannot.
type expertToolset struct {
	readOnly []toolReg
	writes   []gatedWrite
}

func (s *drillServer) expertToolset() expertToolset {
	return expertToolset{
		readOnly: []toolReg{
			func(srv *mcp.Server) {
				mcp.AddTool(srv, &mcp.Tool{
					Name: "study_report",
					Description: "Session-briefing view: tracked / mastered / due / outstanding blindspots, " +
						"strong and weak topics, and how many bank questions remain in scope. Read-only.",
				}, s.studyReport)
			},
			func(srv *mcp.Server) {
				mcp.AddTool(srv, &mcp.Tool{
					Name: "study_gaps",
					Description: "Weak spots ranked by exam risk: confident-but-wrong blindspots first, " +
						"then outright wrong, then shaky. Read-only — use it to steer what to explain.",
				}, s.studyGaps)
			},
			func(srv *mcp.Server) {
				mcp.AddTool(srv, &mcp.Tool{
					Name: "study_coverage",
					Description: "Drill-through per unit, least-mastered first. Read-only — untouched " +
						"units are the silent risk when choosing what to teach next.",
				}, s.studyCoverage)
			},
		},
		// No write tools yet: the expert teaches, the drill records. The
		// first write belongs HERE, built with newGatedWrite — anywhere
		// else it bypasses the confirm gate.
	}
}

// newGatedWrite wraps a state-mutating handler in the confirm gate and
// packages it for the expert toolset's writes list.
func newGatedWrite[In, Out any](gate *confirmGate, tool *mcp.Tool, h mcp.ToolHandlerFor[In, Out]) gatedWrite {
	return gatedWrite{reg: func(srv *mcp.Server) {
		mcp.AddTool(srv, tool, confirmGated(gate, tool.Name, h))
	}}
}

// confirmGate enforces the two-call pattern for state-mutating tools:
// the first call is refused with a one-shot token bound to the tool
// name and exact arguments, and the operation runs only when the caller
// re-issues the identical call carrying that token. Binding the token
// to the arguments matters — a token minted for one call must not
// confirm a different one.
type confirmGate struct {
	mu      sync.Mutex
	pending map[string]string // token -> fingerprint of the approved call
}

func newConfirmGate() *confirmGate {
	return &confirmGate{pending: make(map[string]string)}
}

// confirmGated wraps a handler with the gate. The confirm field is
// deliberately absent from every tool's advertised schema — the token
// is a capability the caller must already hold, not a parameter to be
// discovered.
func confirmGated[In, Out any](g *confirmGate, name string, h mcp.ToolHandlerFor[In, Out]) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		var zero Out
		fp, err := callFingerprint(name, req.Params.Arguments)
		if err != nil {
			return nil, zero, err
		}
		var probe struct {
			Confirm string `json:"confirm"`
		}
		// Arguments were already decoded into In by the SDK; this second
		// pass only lifts the gate token, and a malformed confirm field
		// is simply treated as absent.
		_ = json.Unmarshal(req.Params.Arguments, &probe)
		if probe.Confirm != "" && g.redeem(probe.Confirm, fp) {
			return h(ctx, req, in)
		}
		token := g.issue(fp)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(
				"%s mutates state and requires explicit confirmation. To execute, re-issue the identical call with \"confirm\": %q added. The token is one-shot and bound to these exact arguments.", name, token)}},
			IsError: true,
		}, zero, nil
	}
}

// callFingerprint hashes the tool name with its canonical arguments
// (confirm stripped). JSON map keys marshal in sorted order, so two
// transports encoding the same call fingerprint identically.
func callFingerprint(name string, raw json.RawMessage) (string, error) {
	var args map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("decode arguments: %w", err)
		}
	}
	delete(args, "confirm")
	canon, err := json.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("canonicalize arguments: %w", err)
	}
	sum := sha256.Sum256(append([]byte(name), canon...))
	return hex.EncodeToString(sum[:]), nil
}

func (g *confirmGate) issue(fp string) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing leaves no safe token source; a gate that
		// cannot mint must not open.
		panic(err)
	}
	token := hex.EncodeToString(b)
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pending[token] = fp
	return token
}

// redeem consumes a one-shot token; it approves only the exact call it
// was issued for.
func (g *confirmGate) redeem(token, fp string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if want, ok := g.pending[token]; ok && want == fp {
		delete(g.pending, token)
		return true
	}
	return false
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
	s.noteRecorded()
	return nil, res, nil
}

// noteRecorded counts one stored attempt and flushes the mastery digest
// every digestEveryRecords-th one. Emission failures never reach the
// tool result — a dead memory box must not fail a learner's record.
func (s *drillServer) noteRecorded() {
	s.emitMu.Lock()
	defer s.emitMu.Unlock()
	s.records++
	if s.records%digestEveryRecords == 0 {
		s.flushLocked()
	}
}

// flushLocked publishes the digest and marks every record so far as
// covered. emitMu is held: concurrent records must not double-emit.
func (s *drillServer) flushLocked() {
	emitDigest(s.em, s.deps, os.Stderr)
	s.flushed = s.records
}

// flushOnShutdown publishes the trailing partial batch — the records
// since the last cadence flush — and nothing when the cadence just
// fired, so a clean 5th record is not committed twice.
func (s *drillServer) flushOnShutdown() {
	s.emitMu.Lock()
	defer s.emitMu.Unlock()
	if s.records > s.flushed {
		s.flushLocked()
	}
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
