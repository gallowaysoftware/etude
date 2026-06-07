package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// EmbedClient hits an OpenAI-compatible /v1/embeddings endpoint. The
// expected backend is llama-server in --embeddings mode, bge-large
// loaded — that's the embed_bge_large vibe profile (:14004 by
// default), but any compatible server works.
type EmbedClient struct {
	BaseURL string
	Model   string
	HTTP    *http.Client
}

// NewEmbedClient returns a client with sensible defaults.
func NewEmbedClient(baseURL, model string) *EmbedClient {
	return &EmbedClient{
		BaseURL: baseURL,
		Model:   model,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Embed encodes one piece of text. Returns the 1024-dim vector (for
// bge-large) or whatever the server's model produces — callers should
// verify dims match their manifest.
//
// On HTTP 400 "exceed_context_size_error" (the server enforces a hard
// 512-token cap on bge-large), the call halves the input and retries
// up to 6 times (256-char floor). The chunker keeps inputs well under
// the limit on average, but dense English (chemistry / biology terms
// with hyphens, equation-rich text) occasionally packs more tokens-
// per-char than the chunker's char budget expected. Truncating retains
// most of the semantic content rather than losing the whole chunk.
func (c *EmbedClient) Embed(ctx context.Context, text string) ([]float32, error) {
	// Pre-cap: dense raw chunk text (multibyte runes, equation-rich
	// passages) packs more tokens-per-char than the chunker's char
	// budget assumed, so trying full-size first then retrying down is
	// wasteful when we know the embed server's 512-token cap means
	// anything over ~1800 chars (best case, ~3.5 chars/token) will
	// fail. Cap up-front, retry-on-overflow as a backstop for text
	// that still exceeds the cap at 1800.
	const initialCap = 1800
	const maxRetries = 6
	cur := truncRunes(text, initialCap)
	for attempt := 0; attempt <= maxRetries; attempt++ {
		vec, err := c.embedOnce(ctx, cur)
		if err == nil {
			return vec, nil
		}
		// Only retry on the specific over-context error; everything
		// else (server died, malformed response, etc.) is a real fail.
		if !isExceedContextErr(err) || attempt == maxRetries {
			return nil, err
		}
		// Aggressive shrink — half each attempt. From 1800 chars: 900,
		// 450, 225, 112, ... 6 retries hits ~28 chars, which is well
		// past "useful signal." The < 256 floor below trips first.
		newLen := len(cur) / 2
		if newLen < 256 {
			return nil, fmt.Errorf("embed: text too dense to fit (next shrink would fall below the 256-char floor, from %d chars): %w", len(cur), err)
		}
		cur = truncRunes(cur, newLen)
	}
	return nil, fmt.Errorf("embed: exhausted retries")
}

// truncRunes truncates s to at most n bytes without splitting a
// multi-byte UTF-8 rune. A raw byte cut can land mid-rune, which
// json.Marshal then emits as U+FFFD, silently mangling the text the
// embedding is computed over.
func truncRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func (c *EmbedClient) embedOnce(ctx context.Context, text string) ([]float32, error) {
	body, err := json.Marshal(map[string]any{
		"input": text,
		"model": c.Model,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("embed HTTP %d: %s", resp.StatusCode, string(raw))
	}
	var parsed struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if len(parsed.Data) == 0 {
		return nil, fmt.Errorf("empty embedding response")
	}
	return parsed.Data[0].Embedding, nil
}

// isExceedContextErr matches llama-server's two over-limit failure
// modes. The server distinguishes them inconsistently — same root
// cause, different status codes + payloads:
//
//   - HTTP 400 with `"type":"exceed_context_size_error"` when the
//     input parses to MORE than the model's max_seq_len (e.g. 514
//     tokens on a model trained at 512).
//   - HTTP 500 with `"too large to process"` + a "physical batch
//     size" hint when the input fits the seq-len but exceeds the
//     server's --ubatch-size setting (per-request memory budget).
//
// Both respond to the same fix client-side: truncate the input and
// retry. The retry loop in Embed treats them as one class.
func isExceedContextErr(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "exceed_context_size_error") ||
		strings.Contains(s, "too large to process")
}

// EmbedChunks fills the Embedding field of every chunk in-place.
// Sequential — bge-large via llama-server is fast enough on CPU
// (~10-30 ms / chunk) that the network overhead would dominate any
// gain from goroutine fan-out, and serial requests keep server
// memory bounded.
//
// Already-embedded chunks (len(Embedding) > 0) are skipped — the
// resume path. Errors abort the batch; a failure usually means the
// embedding server died and retrying is wasted work.
func EmbedChunks(ctx context.Context, c *EmbedClient, chunks []Chunk, onProgress func(done, total int)) error {
	for i := range chunks {
		if len(chunks[i].Embedding) > 0 {
			if onProgress != nil {
				onProgress(i+1, len(chunks))
			}
			continue
		}
		vec, err := c.Embed(ctx, chunks[i].Text)
		if err != nil {
			return fmt.Errorf("embed chunk %s: %w", chunks[i].ID, err)
		}
		chunks[i].Embedding = vec
		if onProgress != nil {
			onProgress(i+1, len(chunks))
		}
	}
	return nil
}
