package refrain

import (
	"context"
	"errors"
	"io"
)

// Emitter fans one digest out to both memory channels for one expert.
// The nil Emitter is the disabled case — every method on it is a safe
// no-op, which is how "memory unreachable" propagates: call sites hold
// a nil and never branch.
type Emitter struct {
	sink   Sink
	expert string
}

// NewEmitter builds an Emitter over a sink (the MCP client, or a test
// fake) for one refrain expert.
func NewEmitter(s Sink, expert string) *Emitter {
	return &Emitter{sink: s, expert: expert}
}

// Enabled reports whether emission will do anything.
func (e *Emitter) Enabled() bool {
	return e != nil && e.sink != nil
}

// Emit writes both channels — the durable state document AND the
// interim session-log line — and joins their errors. The channels fail
// independently (one may be a stale refrain that predates set_state),
// so both are always attempted and both errors are kept.
func (e *Emitter) Emit(ctx context.Context, key string, value any, summary string) error {
	if !e.Enabled() {
		return nil
	}
	return errors.Join(
		e.sink.SetState(ctx, e.expert, key, value),
		e.sink.AppendSessionLog(ctx, e.expert, summary),
	)
}

// Close releases the underlying connection, if the sink holds one.
func (e *Emitter) Close() error {
	if !e.Enabled() {
		return nil
	}
	if c, ok := e.sink.(io.Closer); ok {
		return c.Close()
	}
	return nil
}
