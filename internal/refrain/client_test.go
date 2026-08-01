package refrain

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// recordedCall captures one tool invocation the fake refrain received.
type recordedCall struct {
	name string
	args map[string]any
}

// fakeRefrain serves the two memory tools over the real streamable HTTP
// protocol, recording what arrives. failOn, when non-empty, makes that
// tool return a tool-level error, the way a refrain that rejects the
// call would.
func fakeRefrain(t *testing.T, calls *[]recordedCall, failOn string) *httptest.Server {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "refrain-fake", Version: "test"}, nil)

	type stateArgs struct {
		Expert string         `json:"expert"`
		Key    string         `json:"key"`
		Value  map[string]any `json:"value"`
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "set_state"},
		func(_ context.Context, _ *mcp.CallToolRequest, in stateArgs) (*mcp.CallToolResult, struct{}, error) {
			*calls = append(*calls, recordedCall{"set_state", map[string]any{
				"expert": in.Expert, "key": in.Key, "value": in.Value,
			}})
			if failOn == "set_state" {
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: "unknown expert"}},
					IsError: true,
				}, struct{}{}, nil
			}
			return nil, struct{}{}, nil
		})

	type logArgs struct {
		Expert  string `json:"expert"`
		Summary string `json:"summary"`
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "append_session_log"},
		func(_ context.Context, _ *mcp.CallToolRequest, in logArgs) (*mcp.CallToolResult, struct{}, error) {
			*calls = append(*calls, recordedCall{"append_session_log", map[string]any{
				"expert": in.Expert, "summary": in.Summary,
			}})
			return nil, struct{}{}, nil
		})

	ts := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil))
	t.Cleanup(ts.Close)
	return ts
}

func TestDialRoundTrip(t *testing.T) {
	var calls []recordedCall
	ts := fakeRefrain(t, &calls, "")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// The base URL carries no path: Dial must reach the handler at /mcp.
	c, err := Dial(ctx, ts.URL)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	value := map[string]any{"course_slug": "fixture", "due": 3}
	if err := c.SetState(ctx, "fixture-expert", "mastery", value); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	if err := c.AppendSessionLog(ctx, "fixture-expert", "the summary"); err != nil {
		t.Fatalf("AppendSessionLog: %v", err)
	}

	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d: %v", len(calls), calls)
	}
	if calls[0].name != "set_state" || calls[1].name != "append_session_log" {
		t.Fatalf("unexpected call order: %v", calls)
	}
	st := calls[0].args
	if st["expert"] != "fixture-expert" || st["key"] != "mastery" {
		t.Fatalf("set_state args: %v", st)
	}
	got, ok := st["value"].(map[string]any)
	if !ok || got["course_slug"] != "fixture" || got["due"] != float64(3) {
		t.Fatalf("set_state value did not round-trip: %#v", st["value"])
	}
	if lg := calls[1].args; lg["expert"] != "fixture-expert" || lg["summary"] != "the summary" {
		t.Fatalf("append_session_log args: %v", lg)
	}
}

func TestDialUnreachableFailsFast(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	// Port 1 on loopback refuses connections; with retries disabled this
	// must come back well inside the deadline.
	if _, err := Dial(ctx, "http://127.0.0.1:1"); err == nil {
		t.Fatal("Dial to a closed port should fail")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("Dial took %v; memory down must fail fast", d)
	}
}

func TestToolErrorSurfaces(t *testing.T) {
	var calls []recordedCall
	ts := fakeRefrain(t, &calls, "set_state")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Dial(ctx, ts.URL)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	err = c.SetState(ctx, "ghost", "mastery", map[string]any{})
	if err == nil {
		t.Fatal("tool-level IsError must surface as a Go error")
	}
	if got := err.Error(); !strings.Contains(got, "set_state") || !strings.Contains(got, "unknown expert") {
		t.Fatalf("error should name the tool and the server's reason, got %q", got)
	}
}

func TestMCPURL(t *testing.T) {
	for _, tc := range []struct {
		base, want string
	}{
		{"http://127.0.0.1:14010", "http://127.0.0.1:14010/mcp"},
		{"http://127.0.0.1:14010/", "http://127.0.0.1:14010/mcp"},
		{"https://memory.example.com/mcp", "https://memory.example.com/mcp"},
	} {
		got, err := MCPURL(tc.base)
		if err != nil {
			t.Fatalf("MCPURL(%q): %v", tc.base, err)
		}
		if got != tc.want {
			t.Errorf("MCPURL(%q) = %q, want %q", tc.base, got, tc.want)
		}
	}
	for _, bad := range []string{"", "127.0.0.1:14010", "://nope"} {
		if _, err := MCPURL(bad); err == nil {
			t.Errorf("MCPURL(%q) should reject a non-absolute URL", bad)
		}
	}
}

// stubSink records emissions without any network.
type stubSink struct {
	states  []recordedCall
	logs    []recordedCall
	failLog error
}

func (s *stubSink) SetState(_ context.Context, expert, key string, value any) error {
	s.states = append(s.states, recordedCall{"set_state", map[string]any{"expert": expert, "key": key, "value": value}})
	return nil
}

func (s *stubSink) AppendSessionLog(_ context.Context, expert, summary string) error {
	s.logs = append(s.logs, recordedCall{"append_session_log", map[string]any{"expert": expert, "summary": summary}})
	return s.failLog
}

func TestEmitterWritesBothChannels(t *testing.T) {
	sink := &stubSink{}
	em := NewEmitter(sink, "fixture-expert")
	if !em.Enabled() {
		t.Fatal("emitter over a sink should be enabled")
	}
	if err := em.Emit(context.Background(), "mastery", map[string]any{"due": 1}, "summary line"); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if len(sink.states) != 1 || len(sink.logs) != 1 {
		t.Fatalf("Emit must write both channels, got %d states and %d logs", len(sink.states), len(sink.logs))
	}
	if sink.states[0].args["expert"] != "fixture-expert" || sink.logs[0].args["expert"] != "fixture-expert" {
		t.Fatalf("expert not threaded through: %v %v", sink.states, sink.logs)
	}
}

func TestEmitterJoinsErrorsButAttemptsBoth(t *testing.T) {
	sink := &stubSink{failLog: errors.New("log write failed")}
	em := NewEmitter(sink, "fixture-expert")
	err := em.Emit(context.Background(), "mastery", map[string]any{}, "s")
	if err == nil {
		t.Fatal("a failing channel must surface an error")
	}
	if len(sink.states) != 1 {
		t.Fatalf("the healthy channel must still be written, got %d state writes", len(sink.states))
	}
}

func TestNilEmitterIsDisabled(t *testing.T) {
	var em *Emitter
	if em.Enabled() {
		t.Fatal("nil emitter must report disabled")
	}
	if err := em.Emit(context.Background(), "mastery", nil, ""); err != nil {
		t.Fatalf("nil emitter Emit must be a no-op, got %v", err)
	}
	if err := em.Close(); err != nil {
		t.Fatalf("nil emitter Close must be a no-op, got %v", err)
	}
}
