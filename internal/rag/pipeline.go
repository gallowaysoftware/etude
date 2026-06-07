package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config controls Run. Most fields have defaults; the only required
// inputs are LessonsFile + OutDir + Module.
type Config struct {
	// LessonsFile is the path to processed_lessons.json from a prior
	// textbook-to-audiobook pipeline run.
	LessonsFile string
	// OutDir is the directory all RAG artefacts land in. Created if
	// missing.
	OutDir string
	// Module is the module identifier embedded in chunk IDs. e.g.
	// "module_1". No slashes.
	Module string

	// EmbedURL is the base URL of the embedding server. Default
	// "http://127.0.0.1:14004" — the embed_bge_large vibe profile.
	EmbedURL   string
	EmbedModel string // default "bge-large-en-v1.5-q8"

	// LLMURL is the base URL of the chat/completions server. Default
	// "http://127.0.0.1:9000" — the vibe proxy fronting the active
	// LLM profile.
	LLMURL   string
	LLMModel string // default "qwen3.6-27b-mtp-q6_k"

	// Program is the course/program descriptor woven into the study-aid
	// system prompts (enrichment + equations), e.g. "a constitutional
	// law course" or "an introductory astronomy course". Empty
	// defaults to a generic "exam preparation" — the open pipeline
	// ships no curriculum-specific identity, so pass the
	// material-specific label at run time (the --program flag).
	Program string

	// ChunkMaxChars is the prose-chunk character budget. Default
	// DefaultChunkMaxChars.
	ChunkMaxChars int

	// SkipEnrichment skips the LLM enrichment pass (MC + SA
	// generation). Useful when iterating on chunking/embedding and
	// the user doesn't want to wait on the slow stage.
	SkipEnrichment bool
	// SkipEquations skips the equation-extraction pass. Useful for
	// the same reason.
	SkipEquations bool
	// Resume, when true, loads <OutDir>/chunks_partial.jsonl if it
	// exists and carries over completed enrichment + embedding for
	// chunks whose source text hasn't changed. A killed run can
	// pick up nearly where it left off — at most one chunk's worth
	// of repeated work (the one in-flight at kill time).
	Resume bool
}

// Run executes the full RAG export against one module's
// processed_lessons.json. Produces, under OutDir:
//
//	chunks.jsonl       — every chunk + embedding + per-prose-chunk enrichment
//	flashcards.tsv     — Anki-importable MC + SA flashcards
//	study_qa.md        — human-readable Q&A grouped by lesson
//	glossary.md        — alphabetised definitions (from structured data, no LLM)
//	key_numbers.md     — constants by lesson (from structured data, no LLM)
//	equations.md       — deduped equation cheat sheet (LLM-extracted)
//	manifest.json      — embedding model id + chunk params + source file path
//
// Sequential by design — see EmbedChunks / EnrichChunks for the
// rationale. Errors are returned as the first failure surfaces;
// partial outputs may exist in OutDir.
func Run(ctx context.Context, cfg Config) error {
	if cfg.LessonsFile == "" {
		return fmt.Errorf("LessonsFile is required")
	}
	if cfg.OutDir == "" {
		return fmt.Errorf("OutDir is required")
	}
	if cfg.Module == "" {
		return fmt.Errorf("module is required")
	}
	// Module is interpolated into chunk IDs and OWUI filenames, so a
	// path separator or space would corrupt those identifiers (or, with
	// a slash, escape the output dir on the push path).
	if strings.ContainsAny(cfg.Module, `/\ `) {
		return fmt.Errorf("module %q must not contain slashes, backslashes, or spaces", cfg.Module)
	}
	if cfg.EmbedURL == "" {
		cfg.EmbedURL = "http://127.0.0.1:14004"
	}
	if cfg.EmbedModel == "" {
		cfg.EmbedModel = "bge-large-en-v1.5-q8"
	}
	if cfg.LLMURL == "" {
		cfg.LLMURL = "http://127.0.0.1:9000"
	}
	if cfg.LLMModel == "" {
		cfg.LLMModel = "qwen3.6-27b-mtp-q6_k"
	}
	if cfg.ChunkMaxChars <= 0 {
		cfg.ChunkMaxChars = DefaultChunkMaxChars
	}
	if err := os.MkdirAll(cfg.OutDir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	logf := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "[rag] "+format+"\n", args...)
	}

	logf("loading lessons from %s", cfg.LessonsFile)
	lessonsBytes, err := os.ReadFile(cfg.LessonsFile)
	if err != nil {
		return fmt.Errorf("read lessons: %w", err)
	}
	var lessons ProcessedLessons
	if err := json.Unmarshal(lessonsBytes, &lessons); err != nil {
		return fmt.Errorf("parse lessons: %w", err)
	}
	logf("loaded %d lessons", len(lessons.Items))

	// ---- chunk ----
	logf("chunking (max %d chars per prose chunk)", cfg.ChunkMaxChars)
	chunks := ChunkLessons(cfg.Module, &lessons, cfg.ChunkMaxChars)
	numDefs, numKN, numProcs := 0, 0, 0
	for _, c := range chunks {
		switch c.Type {
		case ChunkTypeDefinition:
			numDefs++
		case ChunkTypeKeyNumber:
			numKN++
		case ChunkTypeProcess:
			numProcs++
		}
	}
	logf("chunks: total=%d (prose %d / def %d / key_num %d / process %d)",
		len(chunks), len(chunks)-numDefs-numKN-numProcs, numDefs, numKN, numProcs)

	// ---- resume from checkpoint ----
	if cfg.Resume {
		prior, err := loadCheckpoint(cfg.OutDir)
		if err != nil {
			return fmt.Errorf("load checkpoint: %w", err)
		}
		if len(prior) > 0 {
			var stats checkpointStats
			chunks, stats = applyCheckpoint(chunks, prior)
			logf("resume: carried %d enriched + %d embedded chunks from checkpoint",
				stats.carriedEnrichment, stats.carriedEmbedding)
		} else {
			logf("resume: no checkpoint found at %s (starting fresh)",
				filepath.Join(cfg.OutDir, CheckpointFilename))
		}
	}

	// Checkpoint hook — writes the whole chunks slice to
	// chunks_partial.jsonl. atomic write-then-rename so a crash during
	// checkpoint doesn't corrupt the file the next --resume reads.
	//
	// Each call rewrites the entire slice, so calling it per-chunk over
	// N chunks costs O(N^2) total write work. Callers throttle it (see
	// checkpointEvery) and flush once at the end of a pass.
	checkpoint := func() {
		if err := writeCheckpoint(cfg.OutDir, chunks); err != nil {
			// Non-fatal: a failed checkpoint write means the next resume
			// has slightly more work to redo, not that the run fails.
			logf("warning: checkpoint write failed: %v", err)
		}
	}

	// checkpointEvery bounds the per-chunk rewrite cost: checkpoint
	// every 50 completed chunks instead of every one. A kill loses at
	// most this many chunks of work, matching the embed pass's cadence.
	const checkpointEvery = 50

	// ---- enrich ----
	llm := NewLLMClient(cfg.LLMURL, cfg.LLMModel, cfg.Program)
	if cfg.SkipEnrichment {
		logf("enrichment SKIPPED (--skip-enrichment)")
	} else {
		alreadyDone := 0
		for _, c := range chunks {
			if c.Type == ChunkTypeProse && c.Enrichment != nil {
				alreadyDone++
			}
		}
		if alreadyDone > 0 {
			logf("enriching prose chunks (skipping %d already-enriched from checkpoint)", alreadyDone)
		} else {
			logf("enriching prose chunks via %s (model=%s)", cfg.LLMURL, cfg.LLMModel)
		}
		err := EnrichChunks(ctx, llm, chunks, func(done, total int) {
			logf("  enriched %d/%d", done, total)
			if done%checkpointEvery == 0 {
				checkpoint()
			}
		})
		if err != nil {
			// Persist the chunks enriched between the last throttled
			// checkpoint and the kill/error so a Ctrl-C doesn't discard
			// up to checkpointEvery chunks of completed LLM work.
			checkpoint()
			return fmt.Errorf("enrich: %w", err)
		}
		// Flush once at the end so a kill during the (long) equation
		// pass that follows doesn't discard the tail of enrichment that
		// fell between the last throttled checkpoint and completion.
		checkpoint()
	}

	// ---- equations ----
	// ExtractEquations is a 10-20 min LLM pass whose only output is
	// equations.md. On --resume, carry over an existing equations.md
	// rather than re-running it; --skip-equations still wins.
	var equationsMD string
	equationsPath := filepath.Join(cfg.OutDir, "equations.md")
	switch {
	case cfg.SkipEquations:
		logf("equations SKIPPED (--skip-equations)")
	case cfg.Resume && fileExists(equationsPath):
		prior, rerr := os.ReadFile(equationsPath)
		if rerr != nil {
			return fmt.Errorf("resume equations: %w", rerr)
		}
		equationsMD = string(prior)
		logf("resume: carried equations from %s (skipping LLM extraction)", equationsPath)
	default:
		logf("extracting equations via LLM")
		equationsMD, err = ExtractEquations(ctx, llm, &lessons)
		if err != nil {
			return fmt.Errorf("equations: %w", err)
		}
	}

	// ---- embed ----
	alreadyEmbedded := 0
	for _, c := range chunks {
		if len(c.Embedding) > 0 {
			alreadyEmbedded++
		}
	}
	if alreadyEmbedded > 0 {
		logf("embedding chunks (skipping %d already-embedded from checkpoint)", alreadyEmbedded)
	} else {
		logf("embedding chunks via %s (model=%s)", cfg.EmbedURL, cfg.EmbedModel)
	}
	emb := NewEmbedClient(cfg.EmbedURL, cfg.EmbedModel)
	err = EmbedChunks(ctx, emb, chunks, func(done, total int) {
		if done == 1 || done%50 == 0 || done == total {
			logf("  embedded %d/%d", done, total)
		}
		// Embedding finishes in seconds-per-chunk; checkpointing
		// every 50 instead of every chunk avoids hammering the disk
		// without meaningfully increasing the kill-loss window.
		if done%50 == 0 {
			checkpoint()
		}
	})
	if err != nil {
		// Persist the chunks embedded since the last throttled checkpoint
		// so a Ctrl-C mid-embed doesn't discard completed vectors.
		checkpoint()
		return fmt.Errorf("embed: %w", err)
	}
	// Final checkpoint after the embed pass — covers the case where
	// the run completes normally so the checkpoint file's a faithful
	// snapshot for any future --resume rerun.
	checkpoint()

	// ---- validate embeddings ----
	// Guard against a silently-misconfigured embedding server (wrong
	// model, truncated/empty vectors) before any artefact is written:
	// a corrupt chunks.jsonl/manifest is worse than a failed run. On a
	// resume the prior manifest pins the expected dim, so a swapped
	// embedding model is caught; a fresh run has no such anchor (and a
	// stale manifest from a different model must not block it).
	expectedDim := 0
	if cfg.Resume {
		expectedDim = expectedEmbedDim(cfg.OutDir)
	}
	embedDims, err := validateEmbeddingDims(chunks, expectedDim)
	if err != nil {
		return fmt.Errorf("embedding validation: %w", err)
	}

	// ---- write artefacts ----
	type artefact struct {
		path string
		fn   func(io.Writer) error
	}
	tasks := []artefact{
		{"chunks.jsonl", func(w io.Writer) error { return WriteChunksJSONL(w, chunks) }},
		{"flashcards.tsv", func(w io.Writer) error { return WriteFlashcardsTSV(w, chunks) }},
		{"study_qa.md", func(w io.Writer) error { return WriteStudyQA(w, chunks) }},
		{"glossary.md", func(w io.Writer) error { return WriteGlossary(w, &lessons) }},
		{"key_numbers.md", func(w io.Writer) error { return WriteKeyNumbers(w, &lessons) }},
	}
	if !cfg.SkipEquations {
		tasks = append(tasks, artefact{"equations.md", func(w io.Writer) error {
			_, err := w.Write([]byte(equationsMD))
			return err
		}})
	}
	for _, t := range tasks {
		p := filepath.Join(cfg.OutDir, t.path)
		f, err := os.Create(p)
		if err != nil {
			return fmt.Errorf("create %s: %w", t.path, err)
		}
		if err := t.fn(f); err != nil {
			f.Close()
			return fmt.Errorf("write %s: %w", t.path, err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("close %s: %w", t.path, err)
		}
		logf("wrote %s", p)
	}

	// ---- manifest ----
	numEquations := 0
	if equationsMD != "" {
		// Crude count: one ### header per equation entry.
		numEquations = strings.Count(equationsMD, "\n### ")
	}
	man := Manifest{
		GeneratedAt:    time.Now().UTC().Format(time.RFC3339),
		Module:         cfg.Module,
		EmbeddingModel: cfg.EmbedModel,
		EmbeddingDims:  embedDims,
		ChunkMaxChars:  cfg.ChunkMaxChars,
		NumLessons:     len(lessons.Items),
		NumChunks:      len(chunks),
		NumDefinitions: numDefs,
		NumKeyNumbers:  numKN,
		NumProcesses:   numProcs,
		NumEquations:   numEquations,
		SourceFile:     cfg.LessonsFile,
	}
	manPath := filepath.Join(cfg.OutDir, "manifest.json")
	mf, err := os.Create(manPath)
	if err != nil {
		return fmt.Errorf("create manifest: %w", err)
	}
	enc := json.NewEncoder(mf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(man); err != nil {
		mf.Close()
		return fmt.Errorf("encode manifest: %w", err)
	}
	if err := mf.Close(); err != nil {
		return fmt.Errorf("close manifest: %w", err)
	}
	logf("wrote %s", manPath)

	// The run completed, so chunks.jsonl is the authoritative copy and
	// the checkpoint is stale duplicate state. Remove it so a later
	// --resume doesn't reload an outdated snapshot. Best-effort — a
	// leftover checkpoint is harmless, just redundant.
	if rmErr := os.Remove(filepath.Join(cfg.OutDir, CheckpointFilename)); rmErr != nil && !os.IsNotExist(rmErr) {
		logf("warning: couldn't remove checkpoint %s: %v", CheckpointFilename, rmErr)
	}

	logf("done — RAG export at %s", cfg.OutDir)
	return nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// validateEmbeddingDims verifies every embedded chunk shares the same,
// non-zero vector dimension and (when expected > 0) that it matches the
// expected dim. It returns the observed dimension. A mismatch usually
// means the embedding server is serving a different model than the run
// was configured for — catching it here keeps a corrupt chunks.jsonl /
// manifest off disk.
func validateEmbeddingDims(chunks []Chunk, expected int) (int, error) {
	dim := 0
	for _, c := range chunks {
		n := len(c.Embedding)
		if n == 0 {
			continue
		}
		switch {
		case dim == 0:
			dim = n
		case n != dim:
			return 0, fmt.Errorf("inconsistent embedding dims: chunk %s has %d, others have %d", c.ID, n, dim)
		}
	}
	if dim == 0 {
		return 0, fmt.Errorf("no chunk produced an embedding (zero-dimension vectors); check the embedding server")
	}
	if expected > 0 && dim != expected {
		return 0, fmt.Errorf("embedding dim %d does not match expected %d (from prior manifest); wrong embedding model?", dim, expected)
	}
	return dim, nil
}

// expectedEmbedDim reads the embedding_dims recorded by a prior run's
// manifest.json in outDir, if any. Returns 0 when absent or unreadable
// — a resume that lacks a usable manifest just skips the cross-check.
func expectedEmbedDim(outDir string) int {
	b, err := os.ReadFile(filepath.Join(outDir, "manifest.json"))
	if err != nil {
		return 0
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return 0
	}
	return m.EmbeddingDims
}
