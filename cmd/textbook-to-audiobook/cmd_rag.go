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
		chunksFile string
		owuiURL    string
		owuiToken  string
		collection string
	)
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Push the RAG export to Open WebUI's Knowledge API.",
		Long: `push reads chunks.jsonl and POSTs each chunk as a Knowledge entry
to Open WebUI under the named collection. Idempotent on (collection,
chunk_id): re-runs upsert rather than duplicate.

Note: Open WebUI re-embeds content with its own embedding model on
ingest, so the embeddings in chunks.jsonl are NOT reused on this
path. The chroma_db export (via 'rag pack') preserves the original
embeddings if model-consistency matters.
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if owuiToken == "" {
				owuiToken = os.Getenv("OPEN_WEBUI_TOKEN")
			}
			if owuiToken == "" {
				return fmt.Errorf("--owui-token or OPEN_WEBUI_TOKEN env var required")
			}
			return rag.Push(cmd.Context(), rag.PushConfig{
				ChunksFile: chunksFile,
				OWUIURL:    owuiURL,
				OWUIToken:  owuiToken,
				Collection: collection,
			})
		},
	}
	cmd.Flags().StringVar(&chunksFile, "chunks", "", "Path to chunks.jsonl (required).")
	cmd.Flags().StringVar(&owuiURL, "owui-url", "http://127.0.0.1:14001", "Open WebUI base URL.")
	cmd.Flags().StringVar(&owuiToken, "owui-token", "", "Open WebUI API token (or OPEN_WEBUI_TOKEN env).")
	cmd.Flags().StringVar(&collection, "collection", "", "Open WebUI Knowledge collection name (required).")
	_ = cmd.MarkFlagRequired("chunks")
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
