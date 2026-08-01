// Package refrain is etude's client half of the memory contract: drill
// results steer the tutor by publishing a mastery digest into refrain,
// the memory service. The channel is duplex by design —
// set_state writes the durable state/mastery.json document while
// append_session_log fills the interim slot that already reaches the
// digest today — so both are written on every emission.
//
// The package stays deliberately small: a Sink interface (what emission
// needs of a connection), an MCP-over-streamable-HTTP Client that
// implements it, and an Emitter that fans one digest out to both
// channels. Memory is a side channel everywhere it is consumed: a drill
// must never fail because the memory box is down, so every error here
// is for the caller to log, not to propagate into learner-facing flow.
package refrain

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Sink is what digest emission needs of a memory connection. The MCP
// Client implements it; tests fake it.
type Sink interface {
	SetState(ctx context.Context, expert, key string, value any) error
	AppendSessionLog(ctx context.Context, expert, summary string) error
}

// Client speaks MCP to a refrain server over streamable HTTP.
type Client struct {
	session *mcp.ClientSession
}

// MCPURL resolves the configured base URL (ETUDE_REFRAIN_URL, e.g.
// http://127.0.0.1:14010) to the MCP endpoint: refrain mounts its
// streamable HTTP handler at /mcp, not at the root. A base that already
// carries a path is used as-is, so a nonstandard deployment can point
// straight at the endpoint.
func MCPURL(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("refrain URL %q: %w", base, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("refrain URL %q: need an absolute http(s) URL", base)
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/mcp"
	}
	return u.String(), nil
}

// Dial connects and performs the MCP handshake, so a returned Client is
// known-good — callers use it as the one startup probe after which a
// dead memory box disables emission for the session.
func Dial(ctx context.Context, base string) (*Client, error) {
	endpoint, err := MCPURL(base)
	if err != nil {
		return nil, err
	}
	transport := &mcp.StreamableClientTransport{
		Endpoint: endpoint,
		// Emission is pure request-response: nothing here consumes
		// server-initiated messages, and the standalone SSE stream is
		// one more connection to break when memory is down.
		DisableStandaloneSSE: true,
		// No reconnect backoff: an unreachable memory box must fail
		// fast, never stall a drill behind retries.
		MaxRetries: -1,
		// Backstop only — callers bound every call with a context
		// timeout; this catches the "connected but never answers" case.
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "etude", Version: "dev"}, nil).
		Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", endpoint, err)
	}
	return &Client{session: session}, nil
}

// Close ends the MCP session.
func (c *Client) Close() error {
	return c.session.Close()
}

// SetState writes the durable state document at <expert>/state/<key>.json.
func (c *Client) SetState(ctx context.Context, expert, key string, value any) error {
	return c.call(ctx, "set_state", map[string]any{"expert": expert, "key": key, "value": value})
}

// AppendSessionLog appends the interim digest line to the expert's log.
func (c *Client) AppendSessionLog(ctx context.Context, expert, summary string) error {
	return c.call(ctx, "append_session_log", map[string]any{"expert": expert, "summary": summary})
}

// call invokes one tool and lifts a tool-level error (IsError) into a
// Go error, since the SDK reports those in-band rather than failing the
// request.
func (c *Client) call(ctx context.Context, name string, args map[string]any) error {
	res, err := c.session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if res.IsError {
		return fmt.Errorf("%s: %s", name, resultText(res))
	}
	return nil
}

// resultText flattens the text content of a tool result for error
// messages.
func resultText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
