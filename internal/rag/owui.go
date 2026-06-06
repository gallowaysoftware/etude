package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// PushConfig drives the Open WebUI push.
type PushConfig struct {
	ChunksFile string
	OWUIURL    string
	OWUIToken  string
	Collection string
	// LessonsFile is the original processed_lessons.json. Used to
	// render one richly-formatted markdown file per lesson rather
	// than 1500 files-per-chunk (OWUI re-embeds everything anyway
	// with its own bge-m3 model, so granular-chunk fidelity is
	// lost on this path; per-lesson is the more navigable
	// granularity).
	LessonsFile string
}

// Push uploads the module to Open WebUI's Knowledge as one
// markdown file per lesson. OWUI runs its own embedding model on
// ingest (bge-m3 by default), so the embeddings in chunks.jsonl
// are not reused on this path. The bge-large export in chroma_db/
// stays the source of truth for embedding-model-consistent
// retrieval; OWUI is the polished chat-UX layer for "just ask the
// textbook" workflows.
//
// API flow per OWUI 0.5.x:
//
//  1. GET /api/v1/knowledge/ → list collections
//  2. POST /api/v1/knowledge/create (if not found) → collection id
//  3. For each lesson:
//     a. POST /api/v1/files/ (multipart) → file id
//     b. POST /api/v1/knowledge/{id}/file/add {file_id} → attach
//
// Idempotency: OWUI does NOT dedup files by name; a re-push would
// duplicate the lessons. We mitigate by listing existing files
// in the collection first and skipping ones whose name matches.
// A mid-run failure rolls back the files uploaded so far (see the
// cleanup closure) so a retry doesn't leave orphaned uploads behind.
func Push(ctx context.Context, cfg PushConfig) error {
	if cfg.ChunksFile == "" || cfg.Collection == "" || cfg.OWUIToken == "" {
		return fmt.Errorf("ChunksFile + Collection + OWUIToken required")
	}
	if cfg.LessonsFile == "" {
		return fmt.Errorf("LessonsFile required (--lessons)")
	}
	if cfg.OWUIURL == "" {
		cfg.OWUIURL = "http://127.0.0.1:14001"
	}
	cfg.OWUIURL = strings.TrimRight(cfg.OWUIURL, "/")

	client := &http.Client{Timeout: 120 * time.Second}

	// Load + group the chunks by lesson so each file the user sees
	// in OWUI's collection corresponds to one source lesson.
	chunksByLesson, err := chunksGroupedByLesson(cfg.ChunksFile)
	if err != nil {
		return fmt.Errorf("group chunks: %w", err)
	}

	collectionID, err := owuiEnsureCollection(ctx, client, cfg)
	if err != nil {
		return fmt.Errorf("ensure collection: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[rag] OWUI collection %q (id=%s)\n", cfg.Collection, collectionID)

	existingNames, err := owuiListCollectionFiles(ctx, client, cfg, collectionID)
	if err != nil {
		// Non-fatal — if the list endpoint fails we proceed and
		// accept that duplicates may land. Better than aborting
		// the whole push for a side-channel issue.
		fmt.Fprintf(os.Stderr, "[rag] warning: couldn't list existing files (%v); proceeding without dedup\n", err)
	}

	pushed, skipped := 0, 0
	lessonIndices := make([]int, 0, len(chunksByLesson))
	for k := range chunksByLesson {
		lessonIndices = append(lessonIndices, k)
	}
	sort.Ints(lessonIndices)

	// Track every file id we upload this run. A file that's uploaded
	// to /api/v1/files/ but not yet attached to (or whose attach fails)
	// is an orphan — it consumes storage and a server-side embedding
	// pass but is invisible in the collection. On any mid-run failure
	// we best-effort delete the ids uploaded so far so a retry starts
	// clean instead of accumulating orphans on every attempt.
	var uploaded []string
	cleanup := func() {
		for _, id := range uploaded {
			if derr := owuiDeleteFile(ctx, client, cfg, id); derr != nil {
				fmt.Fprintf(os.Stderr, "[rag] warning: couldn't delete orphaned file %s after failure: %v\n", id, derr)
			}
		}
	}

	for _, idx := range lessonIndices {
		chunks := chunksByLesson[idx]
		filename := fmt.Sprintf("lesson_%03d.md", idx)
		if existingNames[filename] {
			skipped++
			continue
		}
		body := renderLessonMarkdown(chunks)
		fileID, err := owuiUploadFile(ctx, client, cfg, filename, body)
		if err != nil {
			cleanup()
			return fmt.Errorf("upload %s: %w", filename, err)
		}
		uploaded = append(uploaded, fileID)
		if err := owuiAttachFile(ctx, client, cfg, collectionID, fileID); err != nil {
			cleanup()
			return fmt.Errorf("attach %s: %w", filename, err)
		}
		pushed++
		if pushed%10 == 0 || pushed == 1 {
			fmt.Fprintf(os.Stderr, "[rag]   pushed %d lessons\n", pushed)
		}
	}
	fmt.Fprintf(os.Stderr, "[rag] pushed %d lessons (%d skipped as already present) to OWUI collection %q\n",
		pushed, skipped, cfg.Collection)
	return nil
}

// chunksGroupedByLesson reads chunks.jsonl and returns chunks
// keyed by lesson_index in the source order. The structured
// per-chunk metadata (term for definitions/key numbers, enrichment
// MC/SA questions) feeds renderLessonMarkdown.
func chunksGroupedByLesson(path string) (map[int][]Chunk, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	out := map[int][]Chunk{}
	for {
		var c Chunk
		if err := dec.Decode(&c); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		out[c.LessonIndex] = append(out[c.LessonIndex], c)
	}
	return out, nil
}

// renderLessonMarkdown stitches one lesson's chunks back into a
// readable markdown document. Order: prose first (in chunk_idx
// order), then definitions (alphabetised), key numbers, processes.
// Enrichment is rendered as a "Study Questions" section so the
// OWUI ingest captures both the source text AND the LLM-generated
// MC/SA questions for retrieval.
func renderLessonMarkdown(chunks []Chunk) string {
	if len(chunks) == 0 {
		return ""
	}
	var prose, defs, keyNum, procs []Chunk
	for _, c := range chunks {
		switch c.Type {
		case ChunkTypeProse:
			prose = append(prose, c)
		case ChunkTypeDefinition:
			defs = append(defs, c)
		case ChunkTypeKeyNumber:
			keyNum = append(keyNum, c)
		case ChunkTypeProcess:
			procs = append(procs, c)
		}
	}
	sort.Slice(prose, func(i, j int) bool { return prose[i].Idx < prose[j].Idx })
	sort.Slice(defs, func(i, j int) bool { return defs[i].Term < defs[j].Term })
	sort.Slice(keyNum, func(i, j int) bool { return keyNum[i].Term < keyNum[j].Term })

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", chunks[0].LessonTitle)
	if len(prose) > 0 {
		b.WriteString("## Lecture content\n\n")
		for _, c := range prose {
			b.WriteString(c.Text)
			b.WriteString("\n\n")
		}
	}
	if len(defs) > 0 {
		b.WriteString("## Definitions\n\n")
		for _, c := range defs {
			fmt.Fprintf(&b, "- **%s** — %s\n", c.Term, strings.TrimPrefix(c.Text, c.Term+": "))
		}
		b.WriteString("\n")
	}
	if len(keyNum) > 0 {
		b.WriteString("## Key numbers\n\n")
		for _, c := range keyNum {
			fmt.Fprintf(&b, "- **%s** — %s\n", c.Term, strings.TrimPrefix(c.Text, c.Term+": "))
		}
		b.WriteString("\n")
	}
	if len(procs) > 0 {
		b.WriteString("## Processes\n\n")
		for _, c := range procs {
			fmt.Fprintf(&b, "- %s\n", c.Text)
		}
		b.WriteString("\n")
	}
	// Study questions from any prose chunks that have enrichment.
	hasQ := false
	for _, c := range prose {
		if c.Enrichment == nil {
			continue
		}
		if !hasQ {
			b.WriteString("## Study questions\n\n")
			hasQ = true
		}
		for i, mc := range c.Enrichment.MultipleChoice {
			fmt.Fprintf(&b, "**MC %d.** %s\n\n", i+1, mc.Question)
			letters := []string{"A", "B", "C", "D", "E", "F"}
			for j, opt := range mc.Options {
				marker := ""
				if j == mc.CorrectIndex {
					marker = " ✓"
				}
				if j < len(letters) {
					fmt.Fprintf(&b, "  %s. %s%s\n", letters[j], opt, marker)
				}
			}
			if mc.Explanation != "" {
				fmt.Fprintf(&b, "\n  _%s_\n", mc.Explanation)
			}
			b.WriteString("\n")
		}
		for i, sa := range c.Enrichment.ShortAnswer {
			fmt.Fprintf(&b, "**SA %d.** %s\n\n", i+1, sa.Question)
			fmt.Fprintf(&b, "> %s\n\n", sa.Answer)
		}
	}
	return b.String()
}

// owuiEnsureCollection finds the named knowledge collection or
// creates it. Returns the collection's id.
func owuiEnsureCollection(ctx context.Context, client *http.Client, cfg PushConfig) (string, error) {
	listURL := cfg.OWUIURL + "/api/v1/knowledge/"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
	req.Header.Set("Authorization", "Bearer "+cfg.OWUIToken)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("OWUI HTTP %d listing knowledge: %s", resp.StatusCode, string(raw))
	}
	var list []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return "", fmt.Errorf("decode list: %w", err)
	}
	for _, c := range list {
		if c.Name == cfg.Collection {
			return c.ID, nil
		}
	}
	// Create.
	createBody, _ := json.Marshal(map[string]any{
		"name":           cfg.Collection,
		"description":    "Pushed by textbook-to-audiobook rag push",
		"access_control": map[string]any{},
	})
	createURL := cfg.OWUIURL + "/api/v1/knowledge/create"
	creq, _ := http.NewRequestWithContext(ctx, http.MethodPost, createURL, bytes.NewReader(createBody))
	creq.Header.Set("Authorization", "Bearer "+cfg.OWUIToken)
	creq.Header.Set("Content-Type", "application/json")
	cresp, err := client.Do(creq)
	if err != nil {
		return "", err
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
		return "", err
	}
	return created.ID, nil
}

// owuiListCollectionFiles returns the set of filenames already in
// the collection. Used to skip already-pushed lessons on re-runs.
func owuiListCollectionFiles(ctx context.Context, client *http.Client, cfg PushConfig, collectionID string) (map[string]bool, error) {
	url := cfg.OWUIURL + "/api/v1/knowledge/" + collectionID + "/files"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+cfg.OWUIToken)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(raw))
	}
	var files []struct {
		Filename string `json:"filename"`
		Meta     struct {
			Name string `json:"name"`
		} `json:"meta"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&files); err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(files))
	for _, f := range files {
		if f.Filename != "" {
			out[f.Filename] = true
		}
		if f.Meta.Name != "" {
			out[f.Meta.Name] = true
		}
	}
	return out, nil
}

// owuiUploadFile pushes a file to /api/v1/files/ as multipart
// form-data. Returns the OWUI-assigned file id.
func owuiUploadFile(ctx context.Context, client *http.Client, cfg PushConfig, filename, body string) (string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	if _, err := fw.Write([]byte(body)); err != nil {
		return "", err
	}
	mw.Close()
	url := cfg.OWUIURL + "/api/v1/files/"
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	req.Header.Set("Authorization", "Bearer "+cfg.OWUIToken)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("OWUI HTTP %d uploading file: %s", resp.StatusCode, string(raw))
	}
	var parsed struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", err
	}
	return parsed.ID, nil
}

// owuiDeleteFile removes an uploaded file by id. Used to roll back
// orphaned uploads when a push fails partway. Best-effort — a delete
// failure is logged, not propagated, so it never masks the original
// error that triggered the cleanup.
func owuiDeleteFile(ctx context.Context, client *http.Client, cfg PushConfig, fileID string) error {
	url := cfg.OWUIURL + "/api/v1/files/" + fileID
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.OWUIToken)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("OWUI HTTP %d deleting file: %s", resp.StatusCode, string(raw))
	}
	return nil
}

// owuiAttachFile adds an uploaded file's id to a knowledge
// collection. Triggers OWUI's per-file embedding pass server-side.
func owuiAttachFile(ctx context.Context, client *http.Client, cfg PushConfig, collectionID, fileID string) error {
	body, _ := json.Marshal(map[string]string{"file_id": fileID})
	url := cfg.OWUIURL + "/api/v1/knowledge/" + collectionID + "/file/add"
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+cfg.OWUIToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("OWUI HTTP %d attach: %s", resp.StatusCode, string(raw))
	}
	return nil
}
