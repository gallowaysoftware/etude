package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gallowaysoftware/etude/internal/coach"
	"github.com/gallowaysoftware/etude/internal/grade"
)

var drillT0 = time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC)

// fixtureCourse writes a two-unit saq course: unit A has two questions,
// unit B has one, so the sweep and the coverage view both have
// something to spread over.
func fixtureCourse(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	manifest := `version: 1
slug: fixture
title: Fixture Course
subject: fixture subject
program: the fixture program
persona: a fixture lecturer
assessment: end-of-unit self-check questions
source: .
modules:
  - num: 1
    topic: only module
assessment_markers:
  preset: saq
`
	if err := os.WriteFile(filepath.Join(root, "course.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	lessons := map[string]string{
		"Module_1/Lesson_01_Alpha_SAQ": `# Alpha SAQ

## Self-Assessment Questions

1. \* Alpha question one?
2. \*\* Alpha question two?

## Suggested Response - Q1

Response:

Alpha answer one.

## Suggested Response - Q2

Response:

Alpha answer two.
`,
		"Module_1/Lesson_02_Beta_SAQ": `# Beta SAQ

## Self-Assessment Questions

1. \* Beta question one?

## Suggested Response - Q1

Response:

Beta answer one.
`,
	}
	for rel, content := range lessons {
		dir := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "lesson.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// connectClient wires an in-process MCP client to the drill server over
// the SDK's in-memory transport — the same protocol path a real client
// takes, minus the subprocess.
func connectClient(t *testing.T, ds *drillServer) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := ds.server().Connect(ctx, st, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "dev"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// callTool invokes one tool and decodes its structured result into T.
func callTool[T any](t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) T {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("%s returned a tool error: %s", name, toolErrorText(res))
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content of %s: %v", name, err)
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode %s result: %v\n%s", name, err, raw)
	}
	return v
}

func toolErrorText(res *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

func toolNames(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	names := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return names
}

// TestMCPServerContract drives the acceptance flow end to end over the
// in-memory transport: sweep → confident-wrong record → the blindspot
// jumps back as a review → the briefing views stay sane.
func TestMCPServerContract(t *testing.T) {
	deps, err := loadCoach(fixtureCourse(t))
	if err != nil {
		t.Fatalf("loadCoach: %v", err)
	}
	defer deps.Close()
	clock := drillT0
	ds := &drillServer{deps: deps, now: func() time.Time { return clock }}
	cs := connectClient(t, ds)

	names := toolNames(t, cs)
	sort.Strings(names)
	want := []string{"study_coverage", "study_gaps", "study_next_item", "study_record_result", "study_report"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("tools = %v, want %v", names, want)
	}

	first := callTool[nextResult](t, cs, "study_next_item", nil)
	if first.Action != coach.QuizNew {
		t.Fatalf("fresh bank should quiz_new, got %q", first.Action)
	}
	if first.Item == nil || first.Item.Kind != "official" || first.Item.GradingKey == "" {
		t.Fatalf("quiz_new must carry an official item with its grading key: %+v", first.Item)
	}
	if first.Item.Question == "" || len(first.Item.Points) == 0 || first.Item.Citation == "" {
		t.Fatalf("item must relay question, points, and citation: %+v", first.Item)
	}
	if first.Instruction == "" {
		t.Fatal("next must carry an instruction for the posing agent")
	}

	// Confident but wrong, sent as quoted strings — models do both, and
	// the blindspot is the quadrant the drill exists to surface.
	rec := callTool[recordResult](t, cs, "study_record_result", map[string]any{
		"topic":      first.Item.Topic,
		"quality":    "2",
		"confidence": "3",
		"note":       "confident miss",
	})
	if !strings.Contains(rec.Message, "Blindspot") {
		t.Fatalf("confident-wrong record should name the blindspot, got %q", rec.Message)
	}
	if rec.Item.Schedule == nil || rec.Item.Schedule.Calibration != "blindspot" {
		t.Fatalf("updated item should show blindspot calibration: %+v", rec.Item.Schedule)
	}

	// The miss requeues in minutes; advance the clock instead of sleeping.
	clock = clock.Add(8 * time.Minute)
	second := callTool[nextResult](t, cs, "study_next_item", nil)
	if second.Action != coach.Review {
		t.Fatalf("due blindspot must jump the sweep, got %q", second.Action)
	}
	if second.Item.Topic != first.Item.Topic {
		t.Fatalf("review should resurface the blindspot %q, got %q", first.Item.Topic, second.Item.Topic)
	}
	if second.Item.GradingKey == "" || second.Item.Schedule == nil {
		t.Fatalf("review must re-attach the key and the schedule: %+v", second.Item)
	}

	rep := callTool[coach.Report](t, cs, "study_report", nil)
	if rep.Tracked != 1 || rep.Blindspots != 1 {
		t.Fatalf("report = %+v, want 1 tracked / 1 blindspot", rep)
	}

	cov := callTool[coverageResult](t, cs, "study_coverage", nil)
	if len(cov.Units) != 2 || cov.Overall.Questions != 3 {
		t.Fatalf("coverage = %+v, want 2 units / 3 questions overall", cov)
	}

	gaps := callTool[gapsResult](t, cs, "study_gaps", nil)
	if len(gaps.Gaps) == 0 || gaps.Gaps[0].Topic != first.Item.Topic {
		t.Fatalf("gaps should lead with the blindspot: %+v", gaps.Gaps)
	}
}

// TestStudyGradeRegisteredWithGrader covers server-side grading mode:
// the tool appears only when the server was started with a grader, and
// it grades against the bank's official key.
func TestStudyGradeRegisteredWithGrader(t *testing.T) {
	deps, err := loadCoach(fixtureCourse(t))
	if err != nil {
		t.Fatalf("loadCoach: %v", err)
	}
	defer deps.Close()
	q := deps.Bank.ForModule("")[0]
	ds := &drillServer{
		deps:   deps,
		grader: &fakeGrader{byPrompt: map[string]grade.Verdict{q.Prompt: {Quality: 4, Explanation: "scripted verdict"}}},
		now:    time.Now,
	}
	cs := connectClient(t, ds)

	found := false
	for _, name := range toolNames(t, cs) {
		if name == "study_grade" {
			found = true
		}
	}
	if !found {
		t.Fatal("study_grade must be exposed when the server owns a grader")
	}

	verdict := callTool[grade.Verdict](t, cs, "study_grade", map[string]any{
		"question_id":    q.ID,
		"learner_answer": "a learner answer",
	})
	if verdict.Quality != 4 || verdict.Explanation == "" {
		t.Fatalf("verdict = %+v, want the grader's verdict relayed", verdict)
	}
	// An unknown id is a tool error, not a fabricated grade.
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "study_grade",
		Arguments: map[string]any{"question_id": "nope", "learner_answer": "x"},
	})
	if err != nil {
		t.Fatalf("call study_grade: %v", err)
	}
	if !res.IsError {
		t.Fatal("unknown question_id must be a tool error")
	}
}

// TestAPIPrintsOneJSONObject pins the CLI contract: stdout carries
// exactly one parseable JSON object per invocation — anything else
// breaks the harnesses that parse it.
func TestAPIPrintsOneJSONObject(t *testing.T) {
	root := fixtureCourse(t)

	var out bytes.Buffer
	cmd := drillAPICmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"next", "--course", root})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("api next: %v", err)
	}
	trimmed := strings.TrimSpace(out.String())
	if !strings.HasPrefix(trimmed, "{") || !strings.HasSuffix(trimmed, "}") || strings.Count(trimmed, "\n") != 0 {
		t.Fatalf("stdout must be exactly one JSON object, got:\n%s", out.String())
	}
	var v nextResult
	if err := json.Unmarshal([]byte(trimmed), &v); err != nil {
		t.Fatalf("stdout must parse as the next result: %v", err)
	}
	if v.Action != coach.QuizNew || v.Item == nil || v.Item.GradingKey == "" {
		t.Fatalf("api next = %+v, want quiz_new with a grading key", v)
	}

	// record round-trips through the same shapes, and the study store
	// lock must survive sequential invocations.
	out.Reset()
	cmd = drillAPICmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"record", "--course", root, "--topic", v.Item.Topic,
		"--quality", "5", "--confidence", "3", "--note", "clean recall"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("api record: %v", err)
	}
	var rec recordResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &rec); err != nil {
		t.Fatalf("api record output must parse: %v\n%s", err, out.String())
	}
	if rec.Item.Topic != v.Item.Topic || rec.Message == "" {
		t.Fatalf("api record = %+v", rec)
	}

	// A missing score is a usage error, not a silent zero.
	cmd = drillAPICmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"record", "--course", root, "--topic", "x", "--confidence", "3"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("record without --quality must fail")
	}
}

// TestSkillFile pins the skill's load-bearing content: the command
// table, the iron rule, and portability (no machine paths, denylist
// clean).
func TestSkillFile(t *testing.T) {
	root := fixtureCourse(t)

	var out bytes.Buffer
	cmd := skillCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--course", root})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("skill: %v", err)
	}
	text := out.String()

	for _, want := range []string{
		"| Command |",
		"IRON RULE",
		"etude drill api next",
		"etude drill api record",
		"etude drill api report",
		"etude drill api coverage",
		"etude drill api gaps",
		"quality", "confidence",
		"grading_key",
		"Fixture Course",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("skill missing %q", want)
		}
	}
	if strings.Contains(text, root) {
		t.Error("skill must not embed the course's absolute path")
	}
	// The CI denylist greps tracked files for personal identifiers; the
	// skill is generated content and must stay clean of them too.
	lower := strings.ToLower(text)
	for _, banned := range []string{"kyle", "pequalsnp", "thegalloways"} {
		if strings.Contains(lower, banned) {
			t.Errorf("skill contains denylisted identifier %q", banned)
		}
	}
	if strings.Contains(lower, "galloway") && !strings.Contains(text, "Galloway Software") {
		t.Error("skill contains 'galloway' outside the public brand")
	}

	// --out writes the same document to disk.
	outPath := filepath.Join(t.TempDir(), "SKILL.md")
	cmd = skillCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--course", root, "--out", outPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("skill --out: %v", err)
	}
	written, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read --out file: %v", err)
	}
	if string(written) != text {
		t.Error("--out file must match the stdout rendering")
	}
}

func TestFlexInt(t *testing.T) {
	cases := []struct {
		in      any
		want    int
		wantErr bool
	}{
		{float64(4), 4, false},
		{float64(0), 0, false},
		{"3", 3, false},
		{" 2 ", 2, false},
		{nil, 0, true},
		{"four", 0, true},
		{float64(2.5), 0, true},
		{true, 0, true},
	}
	for _, tc := range cases {
		got, err := flexInt(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("flexInt(%v) err = %v, wantErr %v", tc.in, err, tc.wantErr)
		}
		if err == nil && got != tc.want {
			t.Errorf("flexInt(%v) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
