# etude

Formerly **textbook-to-audiobook**. The audiobook pipeline below is
becoming one renderer of a larger course compiler — see [PLAN.md](PLAN.md)
for the product plan.

Turn a directory of structured lessons (markdown + diagrams) into:

- a chapterised **M4B audiobook** (one chapter per unit)
- per-unit **MP3** files
- a markdown **study guide**
- an **EPUB** companion with the SDXL cover embedded

Built as a Go-DSL pipeline on top of [vibe](https://github.com/gallowaysoftware/vibe) — vibe supervises the model backends (LLM, vision, TTS, image generation) under capability profiles; this binary describes the DAG, ships the prompts as embedded assets, and surfaces a first-class CLI.

## Quickstart

```bash
# 1. install vibe + start the daemon
go install github.com/gallowaysoftware/vibe/cmd/vibe@latest
vibe daemon &

# 2. install this binary
go install github.com/gallowaysoftware/etude/cmd/etude@latest

# 3. see what the host needs
etude requirements

# 4. check what's missing (read-only), then bring everything up
etude doctor      # reports each declared service: ✓ or ✗
etude activate    # `vibe start`s every required profile

# 5. run
etude run \
  --source ./my-course/Module_1 \
  --module-num 1 \
  --topic "introductory thermodynamics"
```

`activate` reads the pipeline's RequireProfile + RequireService declarations and brings up everything via vibe in one go — same as running `vibe start <each-profile>` by hand, but the pipeline tells vibe what it needs. `--skip-active` brings up only the CPU sidecars when the GPU is busy elsewhere.

Run time: ~5 hours on a single RTX 5090 for a 60-lesson module. Deliverables land in `$XDG_STATE_HOME/vamp/runs/etude_<timestamp>/`.

Versioning note: `v0.1.0` and `v0.2.0` predate the rename and declare the old module path (`github.com/gallowaysoftware/textbook-to-audiobook`) — the Go module mirror and checksum database are immutable, so those two tags resolve only under that path. `v0.3.0` onward is the installable line under `github.com/gallowaysoftware/etude`.

## The drill coach

Point etude at a course whose material carries its own questions *with model answers* and you get an honest drill: questions relayed verbatim from the corpus, answer **and confidence** collected before any reveal, grading only against the official key (decomposed into rubric points at extraction), misses requeued at 7/20/40 minutes within the session, mastery re-verified across days at 1/2/4-day intervals, and confident-but-wrong answers — blindspots — drilled hardest. It never invents a question and never grades from the model's own opinion.

```bash
# the demo course is the five-minute tour — original material, ships with the repo
export ETUDE_LLM_URL=http://localhost:8080/v1   # any OpenAI-compatible endpoint
export ETUDE_LLM_MODEL=<your-model>

etude drill --course examples/demo-course    # the terminal drill loop
etude report --course examples/demo-course   # coverage, mastery, blindspots

# same coach, agent frontends
etude serve --course examples/demo-course              # MCP over stdio (study_* tools)
etude drill api next --course examples/demo-course     # one JSON object per call
etude skill --course examples/demo-course              # skill file for agent harnesses
```

Grading judgement is the one place a model decides a fact about you, so qualify yours before trusting it with weeks of study:

```bash
etude eval grading --course examples/demo-course \
  --golden examples/demo-course/eval/grading-golden.jsonl
```

The text legs (drill, grading, eval) need only an OpenAI-compatible endpoint — a local router or an external key; no vibe daemon. Audio, vision, and covers remain local-stack. Learner state is greppable JSON at `<course>/.etude/study.json`, single-writer locked. How a corpus marks its own questions is declared in `course.yaml` (`assessment_markers`; see [docs/course-format.md](docs/course-format.md#assessment-material)).

## RAG export

After a textbook run lands, the `rag` family of subcommands produces a portable retrieval-augmented dataset — chunked + embedded content + study aids — loadable in AnythingLLM, LangChain, LlamaIndex, or Open WebUI.

```bash
# Bring up the embedding sidecar (one-time per session).
vibe start embed_bge_large    # bge-large-en-v1.5-q8 on :14004

# Chunk + enrich + embed.
etude rag run \
  --lessons <run-dir>/processed_lessons.json \
  --out <run-dir>/rag \
  --module module_1

# Build a ChromaDB persistent directory.
etude rag pack \
  --chunks <run-dir>/rag/chunks.jsonl \
  --out <run-dir>/rag \
  --collection module_1
```

`rag run` outputs `chunks.jsonl` (source of truth, embeddings + LLM enrichment), `flashcards.tsv` (Anki), `study_qa.md`, `glossary.md`, `key_numbers.md`, `equations.md`, and `manifest.json`. `rag pack` then derives the loadable `chroma_db/` directory from `chunks.jsonl`.

`rag pack` and `rag anki` shell out to Python helpers — `rag pack` needs the `chromadb` package, `rag anki` needs `genanki`. Both prefer a venv at `~/.local/state/etude/rag-venv` if present (set `ETUDE_RAG_PYTHON` to point elsewhere); otherwise `python3` on `$PATH` must have the package installed. The study-aid prompts frame themselves around `--program`, so pass it (e.g. `--program "an introductory astronomy course"`) for material-appropriate questions; it defaults to generic exam-prep language.

## What's the difference from a generic vamp YAML pipeline

A YAML pipeline is portable but stops at YAML's expressive ceiling — no real CLI, no embedded assets, no auto-generated runtime contract. This binary:

- **Embeds prompts + workflows** via `go:embed`, so installing the binary is enough to run; no separate prompt directory.
- **First-class Cobra CLI** — `--source`, `--topic`, `--module-num` instead of `--input lesson_root=… --input module_topic=…`.
- **Pipeline runtime contract** via the `requirements` subcommand — vibe doctor (or anything else that has to set the host up) reads it as JSON.
- **Single static binary** — `go build`/`go install` produces one self-contained binary (prompts + workflows are `go:embed`-ed); no separate asset directory.

The runtime still needs vibe + the right model backends; the binary is the pipeline, not the inference stack.

## Configurability

All curriculum-specific surface area is in `pipeline/config.go` as a `Config` struct. The top-level CLI flags write into it:

| Flag | Config field | What it controls |
|---|---|---|
| `--source` | `LessonRoot` | Lesson directory root |
| `--module-num` | `ModuleNum` | Numeric module id (cover seed + filenames) |
| `--topic` | `ModuleTopic` | Short prose summary |
| `--subject` | `SubjectLabel` | "the course subject" → "astronomy", "physics", … |
| `--program` | `ProgramLabel` | "this course" → "an introductory astronomy course", "Stanford CS103", … |
| `--persona` | `ExpertPersona` | First-person voice the LLM adopts |
| `--assessment` | `AssessmentLabel` | "final assessment" → "course final", "qualifying review", … |
| `--cover-prompt` | `CoverPrompt` | SDXL prompt for the module cover |
| `--voice` | `Voice` | Kokoro voice (default `af_bella`) |
| `--search-suffix` | `SearchSuffix` | Appended to every web-search query |

The `rag` study-aid prompts (enrichment + equation extraction) frame themselves around `--program` too, so a passed-in program label is the only place curriculum identity lives — the pipeline itself ships subject-agnostic.

For more elaborate forks (different output filename templates, EPUB metadata, etc.) see [`examples/example-course/config.go`](examples/example-course/config.go) — it shows how to skin the pipeline for a specific curriculum by setting just the diverging fields.

## Lesson directory layout

```
Module_1/
├── Lesson_1_-_Introduction_to_Stars/
│   ├── lesson.md
│   └── images/
│       ├── diagram_01.svg
│       └── photo_01.png
├── Lesson_2_-_Stellar_Spectra/
│   └── lesson.md
└── …
```

Each `lesson.md` is what the model reads. `images/` is optional; SVGs and rasters are both supported. Identical diagrams across lessons (sha256-equal) get one vision pass shared via content-addressed cache.

## Runtime requirements

Run `etude requirements` to see the live list. As of this writing:

- **Capabilities**: `long_form` (27B+ text model, 128k+ context), `vision` (27B+ multimodal), `tts`, `image_gen`.
- **Services**: `searxng` on `:14002`, `kokoro-fastapi` on `:8880` (activated as the `tts_kokoro` vibe profile).
- **Hardware**: ~32GB VRAM peak, ~20GB disk per module run.
- **Models** tested against: Qwen3.6-27B-MTP @ 131k, Gemma 3 27B + mmproj, Kokoro-FastAPI, SDXL-Turbo.

## Pipeline structure

The DAG was lifted from a production `module.yaml` pipeline:

```
list_lessons → flatten_unique_images → describe_image (vision, foreach unique image)
                                            ↓
                          compact_lesson_diagrams (per lesson)
                                            ↓
                          process_lesson → merge_lessons → compact_lessons
                                                                ↓
                          ┌─ extract_units ─────────────────────┤
                          ↓                                     ↓
                   generate_unit_script (per unit)         extract_topics
                          ↓                                     ↓
                   flatten_segments → chunk_for_tts          search → compact_search
                          ↓                                     ↓
                          audio (kokoro, per chunk) ←───────────┘
                          ↓
                          mp3 (per unit)
                          ↓
                          m4b (chapterised)        study_guide → epub
                                              cover_art ─┘
```

Run `etude viz` for the full Mermaid graph.

## Status

Validated against the production pipeline it was lifted from, by reproducing several module deliverables end-to-end.

## License

MIT — the pipeline is Galloway Software's. It ships no curriculum content: the subject-specific surface (labels, cover prompt, source lessons) is supplied at run time via flags or a `Config` fork — see [`examples/example-course/`](examples/example-course/). Bring your own lesson material; whatever you generate from it is yours.
