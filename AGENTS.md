# AGENTS.md

Operating notes for agents (Claude Code, Aider, Codex, Cursor, …) working in
this repo. The user-facing model lives in `README.md`; this file captures the
conventions and invariants needed to make changes that fit.

## Repo at a glance

A single Go binary (`github.com/gallowaysoftware/textbook-to-audiobook`) that
turns a directory of structured lessons into a chapterised M4B audiobook,
per-unit MP3s, a markdown study guide, and an EPUB companion. It is a Go-DSL
pipeline built on [vibe](https://github.com/gallowaysoftware/vibe)'s `vamp`
package: `vibe` supervises the model backends (LLM, vision, TTS, image-gen)
under capability profiles; this binary describes the DAG and ships the prompts
as `go:embed` assets.

- `cmd/textbook-to-audiobook/` — the Cobra entrypoint. `main.go` binds
  persistent flags into a `pipeline.Config`; `vamp.BuildRoot` auto-registers
  the `run` / `requirements` / `doctor` / `activate` / `viz` / `validate`
  subcommands. `cmd_rag.go` adds the downstream `rag` family.
- `internal/pipeline/` — the DAG (`pipeline.go`) and the fork-tunable `Config`
  (`config.go`). Embedded prompts live under `internal/pipeline/prompts/`,
  ComfyUI graphs under `workflows/`.
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
daemon with the right capability profiles. Run `textbook-to-audiobook
requirements` for the live contract and `doctor` to check the host. `rag pack`
/ `rag anki` shell out to Python (`chromadb` / `genanki`), preferring a venv at
`~/.local/state/textbook-to-audiobook/rag-venv` (`TEXTBOOK_RAG_PYTHON` to
override).
