# textbook-to-audiobook

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
go install github.com/gallowaysoftware/textbook-to-audiobook/cmd/textbook-to-audiobook@latest

# 3. see what the host needs
textbook-to-audiobook requirements

# 4. start the external services it expects
docker compose -f ~/.config/vibe/compose/searxng/docker-compose.yaml up -d
vibe profile activate tts_kokoro

# 5. run
textbook-to-audiobook run \
  --source ./my-course/Module_1 \
  --module-num 1 \
  --topic "introductory thermodynamics"
```

Run time: ~5 hours on a single RTX 5090 for a 60-lesson module. Deliverables land in `$XDG_STATE_HOME/vamp/runs/textbook-to-audiobook_<timestamp>/`.

## What's the difference from a generic vamp YAML pipeline

A YAML pipeline is portable but stops at YAML's expressive ceiling — no real CLI, no embedded assets, no auto-generated runtime contract. This binary:

- **Embeds prompts + workflows** via `go:embed`, so installing the binary is enough to run; no separate prompt directory.
- **First-class Cobra CLI** — `--source`, `--topic`, `--module-num` instead of `--input lesson_root=… --input module_topic=…`.
- **Pipeline runtime contract** via the `requirements` subcommand — vibe doctor (or anything else that has to set the host up) reads it as JSON.
- **Single static binary** cross-compiled by GoReleaser; no Go install on the user's machine.

The runtime still needs vibe + the right model backends; the binary is the pipeline, not the inference stack.

## Configurability

All curriculum-specific surface area is in `internal/pipeline/config.go` as a `Config` struct. The top-level CLI flags write into it:

| Flag | Config field | What it controls |
|---|---|---|
| `--source` | `LessonRoot` | Lesson directory root |
| `--module-num` | `ModuleNum` | Numeric module id (cover seed + filenames) |
| `--topic` | `ModuleTopic` | Short prose summary |
| `--subject` | `SubjectLabel` | "the course subject" → "distilling", "physics", … |
| `--program` | `ProgramLabel` | "this course" → "CIBD certification", "Stanford CS103", … |
| `--persona` | `ExpertPersona` | First-person voice the LLM adopts |
| `--assessment` | `AssessmentLabel` | "final assessment" → "CIBD exam", "qualifying review", … |
| `--cover-prompt` | `CoverPrompt` | SDXL prompt for the module cover |
| `--voice` | `Voice` | Kokoro voice (default `af_bella`) |
| `--search-suffix` | `SearchSuffix` | Appended to every web-search query |

For more elaborate forks (different output filename templates, EPUB metadata, etc.) see `examples/cibd/config.go` — it shows how the original CIBD-distilling pipeline reproduces by setting just the diverging fields.

## Lesson directory layout

```
Module_1/
├── Lesson_1_-_Introduction_to_Cereals/
│   ├── lesson.md
│   └── images/
│       ├── diagram_01.svg
│       └── photo_01.png
├── Lesson_2_-_Barley_Modification/
│   └── lesson.md
└── …
```

Each `lesson.md` is what the model reads. `images/` is optional; SVGs and rasters are both supported. Identical diagrams across lessons (sha256-equal) get one vision pass shared via content-addressed cache.

## Runtime requirements

Run `textbook-to-audiobook requirements` to see the live list. As of this writing:

- **Capabilities**: `long_form` (27B+ text model, 128k+ context), `vision` (27B+ multimodal), `tts`, `image_gen`.
- **Services**: `searxng` on `:14002`, `kokoro-fastapi` on `:8880` (activated as the `tts_kokoro` vibe profile).
- **Hardware**: ~32GB VRAM peak, ~20GB disk per module run.
- **Models** tested against: Qwen3.6-27B-MTP @ 131k, Gemma 3 27B + mmproj, Kokoro-FastAPI, SDXL-Turbo.

## Pipeline structure

The DAG mirrors the original cibd-distilling `module.yaml`:

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

Run `textbook-to-audiobook viz` for the full Mermaid graph.

## Status

Validated to produce equivalent output to the original CIBD pipeline. Tested by reproducing M3 and M1 deliverables (M2 in flight at the time of writing).

## License

MIT. The pipeline is Galloway Software's; the CIBD curriculum is not — see `examples/cibd/` for how the curriculum-specific surface plugs in.
