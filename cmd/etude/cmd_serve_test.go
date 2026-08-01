package main

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gallowaysoftware/etude/coach"
)

// connectServer wires an in-process MCP client to an already-built
// server — the same path connectClient takes, but persona-agnostic.
func connectServer(t *testing.T, srv *mcp.Server) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
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

// TestExpertServerReadOnlyTools is the safety contract of
// serve --persona expert: exactly the three read-only views, with the
// expert prompt as instructions. If a mutating tool ever appears in
// this list without going through the confirm gate, this test is the
// tripwire.
func TestExpertServerReadOnlyTools(t *testing.T) {
	deps, err := loadCoach(fixtureCourse(t))
	if err != nil {
		t.Fatalf("loadCoach: %v", err)
	}
	defer deps.Close()
	ds := &drillServer{deps: deps, now: func() time.Time { return drillT0 }}
	cs := connectServer(t, ds.expertServer())

	names := toolNames(t, cs)
	sort.Strings(names)
	want := []string{"study_coverage", "study_gaps", "study_report"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("expert tools = %v, want exactly %v", names, want)
	}

	// The instructions ARE the expert prompt: the client model reads
	// them at connect time, so the tutor contract travels with the tools.
	if instr := cs.InitializeResult().Instructions; !strings.Contains(instr, "Subject Expert") {
		t.Fatalf("expert instructions should be the expert prompt, got:\n%s", instr)
	}

	// The read-only views must actually answer over this course.
	rep := callTool[coach.Report](t, cs, "study_report", map[string]any{})
	if rep.BankSize == 0 {
		t.Fatalf("study_report over the fixture course should see its bank, got %+v", rep)
	}
}

// TestGatedWriteOverMCP registers a write tool the only way the expert
// toolset allows — newGatedWrite — and drives the two-call pattern over
// the real protocol path: the unconfirmed call is a tool error carrying
// a token, the confirmed re-issue executes.
func TestGatedWriteOverMCP(t *testing.T) {
	gate := newConfirmGate()
	executed := 0
	w := newGatedWrite(gate, &mcp.Tool{
		Name:        "test_write",
		Description: "test double for a state-mutating expert tool",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ gateArgs) (*mcp.CallToolResult, string, error) {
		executed++
		return nil, "done", nil
	})
	srv := mcp.NewServer(&mcp.Implementation{Name: "gate-test", Version: "dev"}, nil)
	w.reg(srv)
	cs := connectServer(t, srv)

	// Unconfirmed: a tool error whose message carries the token.
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "test_write",
		Arguments: map[string]any{"v": 1},
	})
	if err != nil {
		t.Fatalf("call test_write: %v", err)
	}
	if !res.IsError {
		t.Fatalf("unconfirmed write must be a tool error, got %+v", res)
	}
	m := confirmTokenRe.FindStringSubmatch(toolErrorText(res))
	if m == nil {
		t.Fatalf("refusal must carry a confirmation token, got: %s", toolErrorText(res))
	}
	if executed != 0 {
		t.Fatal("unconfirmed call must not execute the handler")
	}

	// Confirmed re-issue: executes exactly once.
	token := m[1]
	res, err = cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "test_write",
		Arguments: map[string]any{"v": 1, "confirm": token},
	})
	if err != nil {
		t.Fatalf("confirmed call test_write: %v", err)
	}
	if res.IsError {
		t.Fatalf("confirmed call must succeed, got: %s", toolErrorText(res))
	}
	if executed != 1 {
		t.Fatalf("handler ran %d times, want exactly 1", executed)
	}
}

var confirmTokenRe = regexp.MustCompile(`"confirm": "([0-9a-f]+)"`)

type gateArgs struct {
	V       int    `json:"v"`
	Confirm string `json:"confirm,omitempty"`
}

// gateCall invokes a gated handler with raw JSON arguments and reports
// whether the wrapped operation ran.
func gateCall(t *testing.T, h mcp.ToolHandlerFor[gateArgs, string], raw string) (*mcp.CallToolResult, bool) {
	t.Helper()
	var in gateArgs
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatalf("bad test arguments: %v", err)
	}
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
		Name:      "test_write",
		Arguments: json.RawMessage(raw),
	}}
	res, out, err := h(context.Background(), req, in)
	if err != nil {
		t.Fatalf("gated call: %v", err)
	}
	return res, out == "done"
}

// TestConfirmGateRejectsUnconfirmedWrite exercises the whole two-call
// pattern: refusal with a token, rejection of forged tokens and changed
// arguments, execution on the matching token, and one-shot semantics.
func TestConfirmGateRejectsUnconfirmedWrite(t *testing.T) {
	gate := newConfirmGate()
	executed := 0
	h := confirmGated(gate, "test_write", func(_ context.Context, _ *mcp.CallToolRequest, _ gateArgs) (*mcp.CallToolResult, string, error) {
		executed++
		return nil, "done", nil
	})

	// No token: refused, and the refusal carries the token to echo back.
	res, ran := gateCall(t, h, `{"v": 1}`)
	if ran || !res.IsError {
		t.Fatalf("unconfirmed call must be refused (ran=%v, res=%+v)", ran, res)
	}
	m := confirmTokenRe.FindStringSubmatch(toolErrorText(res))
	if m == nil {
		t.Fatalf("refusal must carry a confirmation token, got: %s", toolErrorText(res))
	}
	token := m[1]

	// Forged token: still refused.
	if _, ran := gateCall(t, h, `{"v": 1, "confirm": "00000000000000000000000000000000"}`); ran {
		t.Fatal("a forged token must not open the gate")
	}

	// Right token, different arguments: refused — the token is bound to
	// the exact call it was issued for.
	if _, ran := gateCall(t, h, `{"v": 2, "confirm": "`+token+`"}`); ran {
		t.Fatal("a token must not confirm a call with different arguments")
	}

	// Right token, identical call: executes.
	if _, ran := gateCall(t, h, `{"v": 1, "confirm": "`+token+`"}`); !ran {
		t.Fatal("the issued token must confirm its exact call")
	}

	// One-shot: the same token must not work twice.
	if _, ran := gateCall(t, h, `{"v": 1, "confirm": "`+token+`"}`); ran {
		t.Fatal("a spent token must not confirm a second call")
	}
	if executed != 1 {
		t.Fatalf("wrapped handler ran %d times, want exactly 1", executed)
	}
}
