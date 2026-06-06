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

// AnkiConfig drives the .apkg builder.
type AnkiConfig struct {
	// FlashcardsFile is the rag/flashcards.tsv produced by `rag run`.
	FlashcardsFile string
	// OutFile is the path to write the .apkg to. If a directory, the
	// helper appends "<deck-name>.apkg".
	OutFile string
	// DeckName is the human-readable deck title shown in Anki.
	DeckName string
}

//go:embed anki_pack.py
var ankiHelperPy []byte

// PackAnki invokes the bundled Python helper to convert
// flashcards.tsv into an Anki .apkg deck file (sqlite + media,
// directly importable). Uses the same venv-resolution heuristic as
// the chromadb Pack helper.
//
// The helper requires the `genanki` package on the host. Install
// path (matches chroma): ~/.local/state/textbook-to-audiobook/rag-venv
// pre-populated with `genanki`.
func PackAnki(ctx context.Context, cfg AnkiConfig) error {
	if cfg.FlashcardsFile == "" {
		return fmt.Errorf("FlashcardsFile required")
	}
	if cfg.DeckName == "" {
		cfg.DeckName = "Study Deck"
	}
	out := cfg.OutFile
	if out == "" {
		out = filepath.Join(filepath.Dir(cfg.FlashcardsFile), "anki_deck.apkg")
	}
	if info, err := os.Stat(out); err == nil && info.IsDir() {
		out = filepath.Join(out, "anki_deck.apkg")
	}

	tmpf, err := os.CreateTemp("", "anki_pack_*.py")
	if err != nil {
		return fmt.Errorf("create tempfile: %w", err)
	}
	defer os.Remove(tmpf.Name())
	if _, err := tmpf.Write(ankiHelperPy); err != nil {
		tmpf.Close()
		return fmt.Errorf("write helper: %w", err)
	}
	tmpf.Close()

	python := "python3"
	if env := os.Getenv("TEXTBOOK_RAG_PYTHON"); env != "" {
		python = env
	} else if home, err := os.UserHomeDir(); err == nil {
		candidate := filepath.Join(home, ".local", "state", "textbook-to-audiobook", "rag-venv", "bin", "python3")
		if _, err := os.Stat(candidate); err == nil {
			python = candidate
		}
	}

	cmd := exec.CommandContext(ctx, python, tmpf.Name(),
		"--flashcards", cfg.FlashcardsFile,
		"--out", out,
		"--deck-name", cfg.DeckName,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = os.Stderr
	fmt.Fprintf(os.Stderr, "[rag] packing Anki deck %q → %s\n", cfg.DeckName, out)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("python helper: %w\nstderr:\n%s", err, stderr.String())
	}
	fmt.Fprintf(os.Stderr, "[rag] wrote %s — import into Anki via File > Import\n", out)
	return nil
}
