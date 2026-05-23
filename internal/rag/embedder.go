package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
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
func (c *EmbedClient) Embed(ctx context.Context, text string) ([]float32, error) {
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
