package rag

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// CheckpointFilename is the conventional name for the resume-able
// intermediate output. Kept consistent across runs so a `--resume`
// can find it without the caller having to remember the path.
const CheckpointFilename = "chunks_partial.jsonl"

// writeCheckpoint atomically writes chunks to <outDir>/chunks_partial.jsonl.
// "Atomic" via write-then-rename — a crash mid-write leaves the
// previous good checkpoint intact rather than a truncated file the
// next --resume can't parse.
func writeCheckpoint(outDir string, chunks []Chunk) error {
	final := filepath.Join(outDir, CheckpointFilename)
	tmp := final + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create checkpoint tmp: %w", err)
	}
	if err := WriteChunksJSONL(f, chunks); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("write checkpoint: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, final)
}

// loadCheckpoint reads <outDir>/chunks_partial.jsonl into a map keyed
// by chunk id, returning ({}, nil) if the file doesn't exist. Used
// by --resume to skip work that was already done.
func loadCheckpoint(outDir string) (map[string]Chunk, error) {
	path := filepath.Join(outDir, CheckpointFilename)
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]Chunk{}, nil
		}
		return nil, fmt.Errorf("open checkpoint: %w", err)
	}
	defer f.Close()
	out := map[string]Chunk{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var c Chunk
		if err := json.Unmarshal(scanner.Bytes(), &c); err != nil {
			return nil, fmt.Errorf("parse checkpoint line: %w", err)
		}
		out[c.ID] = c
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan checkpoint: %w", err)
	}
	return out, nil
}

// applyCheckpoint merges previously-completed work from a checkpoint
// into a fresh chunks slice. For each chunk in `fresh`, if the
// checkpoint has a matching id with the same Text (i.e. the source
// hasn't changed), carry over the prior Enrichment + Embedding.
// Otherwise the chunk re-does that work on the next run. Returns
// (merged-slice, stats).
type checkpointStats struct {
	carriedEnrichment int
	carriedEmbedding  int
}

func applyCheckpoint(fresh []Chunk, prior map[string]Chunk) ([]Chunk, checkpointStats) {
	var stats checkpointStats
	for i := range fresh {
		p, ok := prior[fresh[i].ID]
		if !ok || p.Text != fresh[i].Text {
			continue
		}
		if p.Enrichment != nil {
			fresh[i].Enrichment = p.Enrichment
			stats.carriedEnrichment++
		}
		if len(p.Embedding) > 0 {
			fresh[i].Embedding = p.Embedding
			stats.carriedEmbedding++
		}
	}
	return fresh, stats
}
