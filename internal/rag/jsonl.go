package rag

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// jsonlEncoder is a thin wrapper around json.Encoder that buffers
// output. json.Encoder already writes one object per line by default;
// the buffering is so a large chunks.jsonl (hundreds of chunks ×
// thousands of float32 entries each) doesn't issue a syscall per
// chunk.
type jsonlEncoder struct {
	bw  *bufio.Writer
	enc *json.Encoder
}

func newJSONEncoder(w io.Writer) *jsonlEncoder {
	bw := bufio.NewWriterSize(w, 64*1024)
	enc := json.NewEncoder(bw)
	enc.SetEscapeHTML(false)
	return &jsonlEncoder{bw: bw, enc: enc}
}

func (e *jsonlEncoder) encode(v any) error {
	if err := e.enc.Encode(v); err != nil {
		return fmt.Errorf("jsonl encode: %w", err)
	}
	return nil
}

// Flush writes any buffered output to the underlying writer; it does
// not close that writer (the caller owns it). Callers MUST call this
// before closing the writer or the tail of the file may be missing.
func (e *jsonlEncoder) Flush() error { return e.bw.Flush() }
