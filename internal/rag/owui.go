package rag

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// PushConfig drives the Open WebUI push.
type PushConfig struct {
	ChunksFile string
	OWUIURL    string
	OWUIToken  string
	Collection string
}

// Push reads chunks.jsonl and uploads each chunk as a knowledge-base
// entry under the named OWUI collection. Open WebUI re-embeds on
// ingest (the configured server-side model wins), so the embeddings
// in chunks.jsonl are NOT reused on this path. If the user wants
// embedding-model parity with their chroma_db export, they should
// configure OWUI to use bge-large-en-v1.5 too.
//
// Idempotency: OWUI's Knowledge API upserts by file name within a
// collection; we use the chunk id as the file name so a re-push of
// the same chunks.jsonl is a no-op.
func Push(ctx context.Context, cfg PushConfig) error {
	if cfg.ChunksFile == "" || cfg.Collection == "" || cfg.OWUIToken == "" {
		return fmt.Errorf("ChunksFile + Collection + OWUIToken required")
	}
	if cfg.OWUIURL == "" {
		cfg.OWUIURL = "http://127.0.0.1:14001"
	}
	cfg.OWUIURL = strings.TrimRight(cfg.OWUIURL, "/")

	client := &http.Client{Timeout: 60 * time.Second}

	// Resolve (or create) the collection id. OWUI's API lets us
	// query collections by name; if missing, create it.
	collectionID, err := ensureCollection(ctx, client, cfg)
	if err != nil {
		return fmt.Errorf("ensure collection: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[rag] OWUI collection %q (id=%s)\n", cfg.Collection, collectionID)

	// Walk the chunks file and POST each.
	f, err := os.Open(cfg.ChunksFile)
	if err != nil {
		return fmt.Errorf("open chunks: %w", err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	pushed := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var chunk Chunk
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			return fmt.Errorf("parse chunk: %w", err)
		}
		if err := pushChunk(ctx, client, cfg, collectionID, chunk); err != nil {
			return fmt.Errorf("push %s: %w", chunk.ID, err)
		}
		pushed++
		if pushed%20 == 0 {
			fmt.Fprintf(os.Stderr, "[rag]   pushed %d\n", pushed)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[rag] pushed %d chunks to OWUI collection %q\n", pushed, cfg.Collection)
	return nil
}

// ensureCollection finds the named collection or creates it. Returns
// the collection id OWUI uses for subsequent file uploads.
//
// Note on stability: Open WebUI's REST API has changed between
// versions (the Knowledge feature has been refactored at least
// twice). This implementation targets the 0.5.x API shape. If the
// instance reports a different schema the call will fail with a
// clear HTTP error and the caller can pin a version.
func ensureCollection(ctx context.Context, client *http.Client, cfg PushConfig) (string, error) {
	// List existing collections.
	listURL := cfg.OWUIURL + "/api/v1/knowledge/list"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
	req.Header.Set("Authorization", "Bearer "+cfg.OWUIToken)
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("list collections: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("OWUI HTTP %d listing collections: %s", resp.StatusCode, string(raw))
	}
	var collections []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&collections); err != nil {
		return "", fmt.Errorf("decode list: %w", err)
	}
	for _, c := range collections {
		if c.Name == cfg.Collection {
			return c.ID, nil
		}
	}
	// Create.
	createBody, _ := json.Marshal(map[string]string{"name": cfg.Collection, "description": "Auto-pushed by textbook-to-audiobook rag push"})
	createURL := cfg.OWUIURL + "/api/v1/knowledge/create"
	creq, _ := http.NewRequestWithContext(ctx, http.MethodPost, createURL, bytes.NewReader(createBody))
	creq.Header.Set("Authorization", "Bearer "+cfg.OWUIToken)
	creq.Header.Set("Content-Type", "application/json")
	cresp, err := client.Do(creq)
	if err != nil {
		return "", fmt.Errorf("create: %w", err)
	}
	defer cresp.Body.Close()
	if cresp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(cresp.Body, 4096))
		return "", fmt.Errorf("OWUI HTTP %d creating collection: %s", cresp.StatusCode, string(raw))
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(cresp.Body).Decode(&created); err != nil {
		return "", fmt.Errorf("decode create: %w", err)
	}
	return created.ID, nil
}

// pushChunk uploads one chunk as a file in the collection. We use
// the multipart-upload endpoint OWUI uses for drag-and-drop files —
// it accepts a name + content blob and runs OWUI's normal ingestion
// (parse → chunk → embed → store). Our chunking is essentially
// thrown away here; the trade-off is automatic re-embedding under
// OWUI's chosen model so retrieval-from-chat works without bridging.
func pushChunk(ctx context.Context, client *http.Client, cfg PushConfig, collectionID string, chunk Chunk) error {
	// Wrap the chunk content with a small metadata header so a human
	// reading the file in OWUI can trace back to source.
	body := fmt.Sprintf(
		"# %s — %s\n\nType: %s\nID: %s\n\n%s\n",
		chunk.LessonTitle, chunk.ID, chunk.Type, chunk.ID, chunk.Text,
	)
	pushURL := fmt.Sprintf("%s/api/v1/knowledge/%s/file/add", cfg.OWUIURL, collectionID)
	payload, _ := json.Marshal(map[string]string{
		"name":    chunk.ID + ".md",
		"content": body,
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, pushURL, bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+cfg.OWUIToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("OWUI HTTP %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}
