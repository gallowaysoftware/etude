# The course format

A course is a directory of markdown that etude compiles into lectures, a
study guide, an EPUB, retrieval chunks, and a question bank. This
document specifies that directory.

It is written for two audiences at once, and the second one matters
more. A person may hand-author a course, but most material does not
arrive as lessons — a book has chapters, an export has files, a wiki has
pages. Turning those into lessons is a judgement call that `etude
ingest` makes with a model and a human review gate. So treat this
format as a **target that a machine produces and a human approves**, not
as a filing convention. Every rule below is one an ingest pass must
satisfy.

Check your course against this document with:

```bash
etude course validate
```

## Shape

```
<course>/
  course.yaml              # the manifest — see docs/course-yaml.md
  Module_1/
    Lesson_01_Barley/
      lesson.md            # required, exact lowercase name
      images/              # optional, flat
        kernel.png
    Lesson_02_Barley_SAQ/
      lesson.md
  Module_2/
    ...
```

## Rules

MUST rules describe things that break or silently disappear. SHOULD
rules describe degradation. Both are checked by `etude course validate`,
which reports MUST violations as errors and SHOULD violations as
warnings.

### Modules

- **MUST** Each module listed in `course.yaml` has a directory under
  `source:`, named `Module_<num>` unless the manifest overrides `dir:`.
- **SHOULD** Every module directory present on disk is listed in the
  manifest. An unlisted module never builds; nothing warns at build
  time.

### Lessons

- **MUST** A lesson is a directory whose name begins with `Lesson_`.
  This glob is the *only* thing the build enumerates. A directory named
  anything else is invisible, even if it contains a perfectly good
  `lesson.md` — a real scraped corpus lost nine lessons this way,
  including its entire maths primer.
- **MUST** Each lesson directory contains a regular file named exactly
  `lesson.md`, lowercase. A lesson directory without one is dropped from
  the course with no diagnostic.
- **MUST** `lesson.md` is non-empty and at most 1 MiB. Over that, the
  lesson is dropped silently.
- **SHOULD** Keep `lesson.md` under 120 KB. The lecture stage and the
  study-guide stage truncate lesson text at different lengths (150 KB
  and 120 KB), so past the lower cap the two stages stop seeing the same
  lesson — a divergence that produces a study guide covering material
  the lecture never mentions.
- **SHOULD** The lesson directory name is filesystem-safe: letters,
  digits, `_`, and `-`. The name is interpolated verbatim into output
  paths for per-lesson artifacts.
- **SHOULD** Zero-pad lesson numbers (`Lesson_01`, not `Lesson_1`).
  Build order is a bytewise sort, under which `Lesson_10` comes before
  `Lesson_2`.
- **SHOULD** Lesson numbers are unique within a module. If several units
  are flattened into one module directory and each restarts at lesson 1,
  sorting interleaves them and no amount of zero-padding recovers the
  intended order. Give each unit its own module, or number lessons
  continuously across the module.

### Lesson content

- **SHOULD** `lesson.md` opens with exactly one H1 that is the lesson
  title, and uses H2 for sections. The pipeline treats that first
  heading as the lesson's title and the study guide reuses it verbatim;
  without one, a title gets invented.
- **SHOULD** No YAML frontmatter. Nothing parses it, so it reaches the
  model as literal noise.
- **SHOULD** Prefer prose that explains the mechanism. Downstream
  prompts are forbidden from inventing facts, so anything the lecture
  should say must be present here first.
- **SHOULD** Keep equations as text — fenced blocks survive verbatim. If
  equations arrive as images, their alt text is what the model reads, so
  the alt text must be a faithful linearization, not "equation-3.png".

### Figures

- **MUST** Figures live in a flat `images/` directory inside the lesson
  directory. Subdirectories are not searched.
- **MUST** Figure files use one of `.png`, `.jpg`, `.jpeg`, `.gif`,
  `.webp`, `.bmp`, `.svg`. Anything else is ignored by the vision stage.
- **MUST** If any figure is an SVG, the build host has `rsvg-convert`
  installed — the vision stage rasterises SVGs and fails without it.
- **SHOULD** Ship a raster copy of every SVG figure. The study guide's
  figure pass skips SVGs, so a vector-only figure reaches the audio
  (via its vision description) but is missing from the markdown guide
  and the EPUB. On an SVG-heavy corpus this silently drops most figures
  from the written deliverables.
- **Note** Figures are discovered by walking `images/`, not by following
  markdown links. An unreferenced file still gets described and
  attached; a dangling link is invisible.

An `images/` directory is optional and frequently absent — roughly one
lesson in eight has no figures at all.

### Assessment material

Material that carries its own questions *with model answers* is the
privileged input: the drill relays those questions verbatim and grades
against the official answer, inventing neither. Corpora without them get
generated questions, which pass through human review before entering the
bank.

Assessment lessons are ordinary `Lesson_*` directories — there is no
separate file type. `course.yaml`'s `assessment_markers` block says how
to recognise them:

```yaml
assessment_markers:
  preset: saq
```

The `saq` preset matches lesson directories against `*SAQ*` and expects,
inside them:

```markdown
# Lesson 3 - Barley SAQ

## Self-Assessment Questions

1. \* List three objectives of malting.
2. \*\* Label the components of the diagram below.

## Suggested Response - Q1

Response:

The three objectives are ...
```

- The question heading opens **one ordered list** whose items are the
  questions. A heading containing `{n}` means the opposite convention —
  one heading per question — and both are supported.
- Each answer gets its own heading, `{n}` being the question number.
  Matching is case-insensitive and tolerates an en-dash.
- Difficulty is the leading escaped-asterisk run: `\*` short, `\*\*`
  progressive, `\*\*\*` long.

Override any field to describe a different convention:

```yaml
assessment_markers:
  preset: saq
  lesson_pattern: "*_Exercises"
  answer_heading: "## Model Answer {n}"
```

## Producing this format

`etude ingest` (planned — see [PLAN.md](../PLAN.md)) turns unstructured
source material into this shape: deterministic extraction first, then a
model proposes lesson boundaries and titles, then you accept, edit, or
reject the proposal before anything is written. Splitting a book into
lessons is the highest-leverage judgement in the whole pipeline — a bad
breakdown degrades the lectures, the study guide, and the question bank
at once — so it is a reviewed proposal rather than an automatic
conversion.

Until then, `etude init` scaffolds the tree and a sample lesson, and
`etude course validate` tells you what a build would silently lose.
