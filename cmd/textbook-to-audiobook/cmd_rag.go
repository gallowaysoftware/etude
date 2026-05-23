package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/gallowaysoftware/textbook-to-audiobook/internal/rag"
)

// ragCmd is the umbrella for the RAG-export subcommands: `rag run`
// produces all the artefacts, `rag pack` builds the chroma_db
// directory, `rag push` ships the export to Open WebUI.
//
// The pipeline-level `textbook-to-audiobook run` produces the audio
// + EPUB + study guide; the `rag` family is a strict downstream of
// that — it reads processed_lessons.json (the merge_lessons output)
// from a prior run and produces the retrieval-augmented artefacts.
// Decoupled so re-running just the RAG export doesn't require
// re-running the textbook pipeline.
func ragCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rag",
		Short: "RAG-export subcommands (run / pack / push).",
		Long: `rag produces the retrieval-augmented exports from a prior
textbook-to-audiobook run's processed_lessons.json:

  rag run    — chunk + enrich + embed + study aids
  rag pack   — build chroma_db/ from chunks.jsonl
  rag push   — POST the export to Open WebUI's Knowledge API
`,
	}
	cmd.AddCommand(ragRunCmd())
	cmd.AddCommand(ragPackCmd())
	cmd.AddCommand(ragPushCmd())
	cmd.AddCommand(ragAnkiCmd())
	return cmd
}

func ragAnkiCmd() *cobra.Command {
	var (
		flashcardsFile string
		outFile        string
		deckName       string
	)
	cmd := &cobra.Command{
		Use:   "anki",
		Short: "Convert flashcards.tsv into a .apkg Anki deck.",
		Long: `anki produces an Anki-importable .apkg from flashcards.tsv. Open Anki
on any platform (desktop or mobile), File > Import, pick the .apkg —
spaced-repetition drilling, sync across devices via AnkiWeb.

Cards use stable deterministic IDs derived from front+back text, so
re-importing an updated deck updates existing cards instead of
duplicating.

Requires Python 3 + the genanki package. The Go side prefers
~/.local/state/textbook-to-audiobook/rag-venv if it exists (same
convention as rag pack); set TEXTBOOK_RAG_PYTHON to override.
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return rag.PackAnki(rag.AnkiConfig{
				FlashcardsFile: flashcardsFile,
				OutFile:        outFile,
				DeckName:       deckName,
			})
		},
	}
	cmd.Flags().StringVar(&flashcardsFile, "flashcards", "", "Path to flashcards.tsv (required).")
	cmd.Flags().StringVar(&outFile, "out", "", "Output .apkg path (defaults to anki_deck.apkg next to flashcards.tsv).")
	cmd.Flags().StringVar(&deckName, "deck-name", "CIBD Study", "Deck title shown in Anki.")
	_ = cmd.MarkFlagRequired("flashcards")
	return cmd
}

func ragRunCmd() *cobra.Command {
	var (
		lessonsFile    string
		outDir         string
		module         string
		embedURL       string
		embedModel     string
		llmURL         string
		llmModel       string
		chunkMaxChars  int
		skipEnrichment bool
		skipEquations  bool
		resume         bool
	)
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Chunk + enrich + embed a processed_lessons.json into a RAG export.",
		Long: `run consumes processed_lessons.json from a prior textbook-to-audiobook
pipeline run and produces, under --out:

  chunks.jsonl       chunks + embeddings + per-prose-chunk LLM enrichment
  flashcards.tsv     Anki-importable MC + SA flashcards
  study_qa.md        human-readable Q&A grouped by lesson
  glossary.md        alphabetised definitions (structural, no LLM)
  key_numbers.md     constants by lesson (structural, no LLM)
  equations.md       deduped equation cheat sheet (LLM)
  manifest.json      embedding model id + chunk params + counts

Requires:
  - The embedding server running, vibe start embed_bge_large (port :14004)
  - The LLM proxy running, vibe start <long_form profile> (proxy :9000)

For Module 1: --lessons points at the merge_lessons output of a
prior textbook run, --out at a directory under the same run.
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return rag.Run(ctx, rag.Config{
				LessonsFile:    lessonsFile,
				OutDir:         outDir,
				Module:         module,
				EmbedURL:       embedURL,
				EmbedModel:     embedModel,
				LLMURL:         llmURL,
				LLMModel:       llmModel,
				ChunkMaxChars:  chunkMaxChars,
				SkipEnrichment: skipEnrichment,
				SkipEquations:  skipEquations,
				Resume:         resume,
			})
		},
	}
	cmd.Flags().StringVar(&lessonsFile, "lessons", "", "Path to processed_lessons.json (required).")
	cmd.Flags().StringVar(&outDir, "out", "", "Output directory for the RAG export (required).")
	cmd.Flags().StringVar(&module, "module", "", "Module identifier embedded in chunk IDs (e.g. 'module_1') (required).")
	cmd.Flags().StringVar(&embedURL, "embed-url", "http://127.0.0.1:14004", "Embedding server base URL.")
	cmd.Flags().StringVar(&embedModel, "embed-model", "bge-large-en-v1.5-q8", "Embedding model alias.")
	cmd.Flags().StringVar(&llmURL, "llm-url", "http://127.0.0.1:9000", "LLM proxy base URL (vibe proxy).")
	cmd.Flags().StringVar(&llmModel, "llm-model", "qwen3.6-27b-mtp-q6_k", "LLM model alias as registered by the active vibe profile.")
	cmd.Flags().IntVar(&chunkMaxChars, "chunk-max-chars", rag.DefaultChunkMaxChars, "Max chars per prose chunk.")
	cmd.Flags().BoolVar(&skipEnrichment, "skip-enrichment", false, "Skip the LLM enrichment pass (faster iteration on chunking/embedding).")
	cmd.Flags().BoolVar(&skipEquations, "skip-equations", false, "Skip the equation-extraction LLM pass.")
	cmd.Flags().BoolVar(&resume, "resume", false, "Load <out>/chunks_partial.jsonl if it exists and skip already-completed enrichment + embedding for chunks whose source text matches.")
	_ = cmd.MarkFlagRequired("lessons")
	_ = cmd.MarkFlagRequired("out")
	_ = cmd.MarkFlagRequired("module")
	return cmd
}

func ragPackCmd() *cobra.Command {
	var (
		chunksFile string
		outDir     string
		collection string
	)
	cmd := &cobra.Command{
		Use:   "pack",
		Short: "Build a ChromaDB persistent directory from chunks.jsonl.",
		Long: `pack reads chunks.jsonl and writes a ChromaDB-format directory at
<out>/chroma_db. Loadable into AnythingLLM, LangChain, LlamaIndex, or
any other tool that accepts a Chroma persistent client.

Shells out to a bundled Python helper (chromadb.PersistentClient).
Python 3 + the chromadb package must be on $PATH.
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return rag.Pack(rag.PackConfig{
				ChunksFile: chunksFile,
				OutDir:     outDir,
				Collection: collection,
			})
		},
	}
	cmd.Flags().StringVar(&chunksFile, "chunks", "", "Path to chunks.jsonl (required).")
	cmd.Flags().StringVar(&outDir, "out", "", "Output directory; chroma_db/ lands inside (required).")
	cmd.Flags().StringVar(&collection, "collection", "module", "Chroma collection name.")
	_ = cmd.MarkFlagRequired("chunks")
	_ = cmd.MarkFlagRequired("out")
	return cmd
}

func ragPushCmd() *cobra.Command {
	var (
		chunksFile  string
		lessonsFile string
		owuiURL     string
		owuiToken   string
		collection  string
	)
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Push the RAG export to Open WebUI's Knowledge API.",
		Long: `push uploads one richly-formatted markdown file per lesson into
Open WebUI's Knowledge feature, attaching them all to the named
collection. Each lesson's prose + definitions + key numbers +
processes + study questions become one queryable document.

Open WebUI re-embeds the uploaded content with its own configured
model (default BAAI/bge-m3 in the vibe rag profile), so the
embeddings in chunks.jsonl are NOT reused on this path. If you
need bge-large parity at retrieval time, use the chroma_db export
from 'rag pack' instead.

Get an API token from Open WebUI: Settings → Account → API Keys.
Pass it via --owui-token or the OPEN_WEBUI_TOKEN env var.
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if owuiToken == "" {
				owuiToken = os.Getenv("OPEN_WEBUI_TOKEN")
			}
			if owuiToken == "" {
				return fmt.Errorf("--owui-token or OPEN_WEBUI_TOKEN env var required (get one at Open WebUI Settings → Account → API Keys)")
			}
			return rag.Push(cmd.Context(), rag.PushConfig{
				ChunksFile:  chunksFile,
				LessonsFile: lessonsFile,
				OWUIURL:     owuiURL,
				OWUIToken:   owuiToken,
				Collection:  collection,
			})
		},
	}
	cmd.Flags().StringVar(&chunksFile, "chunks", "", "Path to chunks.jsonl (required).")
	cmd.Flags().StringVar(&lessonsFile, "lessons", "", "Path to processed_lessons.json (required — used for per-lesson grouping context).")
	cmd.Flags().StringVar(&owuiURL, "owui-url", "http://127.0.0.1:14001", "Open WebUI base URL.")
	cmd.Flags().StringVar(&owuiToken, "owui-token", "", "Open WebUI API token (or OPEN_WEBUI_TOKEN env).")
	cmd.Flags().StringVar(&collection, "collection", "", "Open WebUI Knowledge collection name (required).")
	_ = cmd.MarkFlagRequired("chunks")
	_ = cmd.MarkFlagRequired("lessons")
	_ = cmd.MarkFlagRequired("collection")
	return cmd
}

// suggestModuleDirHint returns a hint string with the user's likely
// processed_lessons.json path. Pure UX helper for `rag run` so the
// caller doesn't have to dig for it. Currently unused but kept for
// the moment a help message gets longer.
func suggestModuleDirHint(runDir string) string {
	return filepath.Join(runDir, "processed_lessons.json")
}
