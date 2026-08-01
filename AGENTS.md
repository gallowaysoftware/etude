# AGENTS.md

Operating notes for agents (Claude Code, Aider, Codex, Cursor, …) working in
this repo. The user-facing model lives in `README.md`; this file captures the
conventions and invariants needed to make changes that fit.

## Repo at a glance

A single Go binary (`github.com/gallowaysoftware/etude`) that
turns a directory of structured lessons into a chapterised M4B audiobook,
per-unit MP3s, a markdown study guide, and an EPUB companion. It is a Go-DSL
pipeline built on [vibe](https://github.com/gallowaysoftware/vibe)'s `vamp`
package: `vibe` supervises the model backends (LLM, vision, TTS, image-gen)
under capability profiles; this binary describes the DAG and ships the prompts
as `go:embed` assets.

- `cmd/etude/` — the Cobra entrypoint. `main.go` binds
  persistent flags into a `pipeline.Config`; `vamp.BuildRoot` auto-registers
  the `run` / `requirements` / `doctor` / `activate` / `viz` / `validate`
  subcommands. `cmd_rag.go` adds the downstream `rag` family.
- `pipeline/` — the DAG (`pipeline.go`) and the fork-tunable `Config`
  (`config.go`). Public so external forks can import it. Embedded prompts live
  under `pipeline/prompts/`, ComfyUI graphs under `pipeline/workflows/`.
- `course/`, `qbank/`, `study/`, `coach/`, `grade/` — the drill: the
  course.yaml contract, the question-bank extractor, the
  successive-relearning store, the scheduling policy + coach prompt, and
  the grader interface. Public so the private predecessor (and forks)
  consume them as dependencies — that is what keeps the excision honest
  (docs/excision-checklist.md, "After landing").
- `internal/rag/` — the retrieval-augmented export (chunk → enrich → embed →
  study aids → Anki/Chroma/Open WebUI). Strict downstream of a pipeline run.

## Inner loop

```
go build ./...
go vet ./...
go test ./...
gofmt -l .          # must print nothing
```

## Conventions

- **Curriculum-agnostic core.** The pipeline ships NO subject-specific content.
  All material-specific surface (subject/program/persona/assessment labels,
  cover prompt, search suffix, EPUB metadata) is supplied at run time via CLI
  flags or a `Config` fork. See `examples/example-course/` for the pattern.
  The `rag` study-aid system prompts take the program descriptor as a parameter
  (`rag.Config.Program`, fed from `--program`) — never hard-code a curriculum
  identity into a prompt.
- **Prompts are data.** Pipeline prompts are `go:embed`-ed markdown templates
  referencing `{{ .inputs.<name> }}`; the `rag` package builds its system
  prompts in Go. Either way, subject framing flows from config, not constants.
- **Stdlib first**, modern Go (`log/slog`, `errors.Join/Is/As`, `any`,
  `embed.FS`). Justify any new dependency.
- **Comments explain WHY, not WHAT.** No task-narration comments.
- **No emojis** in code, comments, or commit messages unless asked.
- **No new docs files** unless requested.

## Runtime

The binary is the pipeline, not the inference stack — it needs a running `vibe`
daemon with the right capability profiles. Run `etude
requirements` for the live contract and `doctor` to check the host. `rag pack`
/ `rag anki` shell out to Python (`chromadb` / `genanki`), preferring a venv at
`~/.local/state/etude/rag-venv` (`ETUDE_RAG_PYTHON` to
override).

## Course manifest and validation

- `course/` owns `course.yaml` (the per-course manifest) and the
  lesson-tree contract: `manifest.go` (types, load, validate),
  `tree.go` (the tree scan), `scaffold.go` + `templates/` (what `init`
  writes). Specs live in `docs/course-yaml.md` and
  `docs/course-format.md` — change one and change the other.
- **The validator's job is silent losses.** The pipeline drops a lesson
  directory with no `lesson.md`, drops a `lesson.md` over 1 MiB, filters
  oversized files out of batch prompt context, and orders lessons by
  bytewise sort — none of which announce themselves at run time. A new
  check earns its place by catching something that would otherwise
  produce a quietly smaller course. Errors are for content that
  vanishes; warnings are for degradation.
- **Limits are mirrored from vamp, not invented here.** `maxLessonBytes`,
  `batchReadBytes`, and `truncateBytes` in `tree.go` restate caps
  enforced in vibe's template functions and the pipeline prompts. If
  those move, these move.
- **Ground format rules in measurement.** The `saq` preset's patterns
  come from a real 189-lesson corpus; a plausible-looking guess
  (`*_Unit_SAQ`) would have matched 18 of 189 directories. New presets
  or rules cite what they were checked against.
- `REPLACE-` values in a scaffolded manifest are a hard gate: validation
  rejects them, so a half-filled course fails loudly instead of
  producing generically-worded lectures. Placeholder VALUES use the
  prefix; explanatory comments may mention it freely.
