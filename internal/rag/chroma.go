package rag

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// PackConfig drives the chroma_db builder.
type PackConfig struct {
	ChunksFile string
	OutDir     string
	Collection string
}

// packHelperPy is a bundled Python script that uses chromadb's
// PersistentClient to write chunks.jsonl into a Chroma database
// directory. Embedded so the binary is self-contained — chromadb
// only needs to be available at runtime, not at build time.
//
//go:embed chroma_pack.py
var packHelperPy []byte

// Pack invokes the bundled Python helper to write a Chroma
// persistent database from chunks.jsonl. The script is written to
// a temp file each run rather than relying on a $PATH-installed
// script — keeps the helper version pinned to the binary version.
func Pack(ctx context.Context, cfg PackConfig) error {
	if cfg.ChunksFile == "" || cfg.OutDir == "" {
		return fmt.Errorf("ChunksFile + OutDir required")
	}
	if cfg.Collection == "" {
		cfg.Collection = "module"
	}
	chromaDir := filepath.Join(cfg.OutDir, "chroma_db")
	if err := os.MkdirAll(chromaDir, 0o755); err != nil {
		return fmt.Errorf("mkdir chroma_db: %w", err)
	}

	// Stage the helper to a temp file. Using TempFile gives a
	// guaranteed-unique path even when two Pack runs race.
	tmpf, err := os.CreateTemp("", "chroma_pack_*.py")
	if err != nil {
		return fmt.Errorf("create tempfile: %w", err)
	}
	defer os.Remove(tmpf.Name())
	if _, err := tmpf.Write(packHelperPy); err != nil {
		tmpf.Close()
		return fmt.Errorf("write helper: %w", err)
	}
	if err := tmpf.Close(); err != nil {
		return fmt.Errorf("close helper: %w", err)
	}

	// Resolve which python to invoke. The bundled helper needs the
	// chromadb package available. PEP 668-protected Linuxes (Arch,
	// recent Debian/Ubuntu) refuse `pip install` into system
	// python; the convention this repo follows is a dedicated venv
	// at ~/.local/state/etude/rag-venv. Honors
	// $ETUDE_RAG_PYTHON for callers who want to point at a
	// different interpreter (uv tool's chromadb shim, a conda env,
	// etc.).
	python := "python3"
	if env := os.Getenv("ETUDE_RAG_PYTHON"); env != "" {
		python = env
	} else if home, err := os.UserHomeDir(); err == nil {
		candidate := filepath.Join(home, ".local", "state", "etude", "rag-venv", "bin", "python3")
		if _, err := os.Stat(candidate); err == nil {
			python = candidate
		}
	}

	cmd := exec.CommandContext(ctx, python, tmpf.Name(),
		"--chunks", cfg.ChunksFile,
		"--out", chromaDir,
		"--collection", cfg.Collection,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = os.Stderr
	fmt.Fprintf(os.Stderr, "[rag] packing %s → %s (collection=%s)\n", cfg.ChunksFile, chromaDir, cfg.Collection)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("python helper: %w\nstderr:\n%s", err, stderr.String())
	}
	fmt.Fprintf(os.Stderr, "[rag] wrote chroma_db at %s\n", chromaDir)
	return nil
}
