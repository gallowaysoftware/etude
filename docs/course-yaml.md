# course.yaml

`course.yaml` is a course's manifest: its identity, the labels that skin
every prompt, where its material lives, and how that material marks its
own questions. It sits at the root of a course directory.

What it deliberately does **not** hold is where models run. Endpoints,
API keys, and hardware profiles are machine-scoped and live in vibe/vamp
config or `ETUDE_*` environment variables, so the same manifest travels
between a laptop, a GPU box, and an external provider unchanged.

Scaffold one with `etude init`, check it with `etude course validate`,
and see what it resolves to with `etude course show`.

## Example

```yaml
version: 1

slug: home-llms
title: Running LLMs at Home

subject: running language models on your own hardware
program: the Running LLMs at Home course
persona: an engineer who runs models locally
assessment: end-of-unit self-check questions

source: .
modules:
  - num: 1
    topic: how models fit in memory
  - num: 2
    topic: quantization and what it costs you

render:
  voice: af_bella

assessment_markers:
  preset: saq
```

## Fields

### `version` (required)

Schema version. Currently `1`. A manifest declaring a newer version is
rejected rather than half-interpreted.

### `slug` (required)

The course's identity everywhere it appears: the learner-memory expert,
the knowledge-base collection, deliverable filenames. Lowercase letters,
digits, and hyphens; starts with a letter or digit; 64 characters max.

The pattern is deliberately identical to the memory service's expert
slug rule, so one declared name binds every service without a
translation table. Declaring the name once is the point: naming a course
separately in three places is how a deployment ends up with a memory
expert called `distillery` reading a collection called `distilling`.

### `title` (required)

The human-readable course name.

### `subject`, `program`, `persona`, `assessment` (required)

The four labels that carry the entire domain identity of a course. The
pipeline itself knows nothing about your subject; these are what make
its prompts sound like they are about your material.

| Field | What it is | Example |
|---|---|---|
| `subject` | the field of study, as a lecturer would name it | `introductory astronomy` |
| `program` | the course or certification the material belongs to | `an introductory astronomy course` |
| `persona` | the first-person identity the lecturer adopts | `an astronomy lecturer` |
| `assessment` | how learners are evaluated | `end-of-unit self-check questions` |

Leave one generic and the lectures sound generic. That is why `etude
init` writes them as `REPLACE-` placeholders and validation refuses to
load a manifest that still carries one — a half-filled manifest
otherwise produces plausible-sounding lectures about nothing in
particular.

### `source`

Directory holding the module subdirectories, relative to the manifest.
Defaults to `.`.

### `modules` (required)

The modules in teaching order, each with:

- `num` — the module's identifier, used in cover seeds and filenames.
  Positive, unique.
- `topic` — a short prose summary, reaching cover prompts and EPUB
  titles.
- `dir` — the directory name, defaulting to `Module_<num>`.

Order is explicit rather than derived from a directory listing because
`Module_10` sorts before `Module_2`, and because a course is a sequence,
not a set.

### `render`

Presentation, all optional:

- `voice` — the narration voice (default `af_bella`).
- `cover_prompt` — the image prompt for cover art.
- `search_suffix` — appended to web-search queries during enrichment.
- `epub_title`, `epub_author` — EPUB metadata.
- `filename_prefix` — deliverable basename stem; defaults from the
  module topic.

### `assessment_markers`

How the corpus marks its own questions and model answers. See
[course-format.md](course-format.md#assessment-material) for the
conventions and the `saq` preset. `preset: none` (the default) means the
corpus carries no extractable assessment material and questions will be
generated and reviewed instead.

### `expert`

Bindings to the services that make a course conversational. Both default
to `slug` and exist only as overrides for deployments that already use
different names:

- `memory_slug` — the memory expert holding this course's learner state.
- `knowledge_collection` — the knowledge-service collection holding the
  corpus.

`etude course show` prints the resolved bindings, which is the cheap way
to catch a drifted name before three services disagree about it.

## Using a manifest

```bash
etude run --course . --module-num 1
```

supplies the lesson root and every domain label from the manifest.
Explicit flags still win, so overriding one value for a single run does
not require editing the file.

## Unknown fields are errors

A misspelled key is rejected rather than ignored. A `voise:` that
quietly reverts narration to the default, discovered five hours into a
build, is exactly the failure a manifest should not have.
