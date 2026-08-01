# etude — product plan

This repo (formerly `textbook-to-audiobook`) is the product repo for
**etude** — an étude is a piece composed to practice a skill. This
document is the working plan: what the product is, what moves where,
and in what order. It supersedes the roadmap implied by the README,
which describes only the audiobook pipeline.

## What the product is

Point it at course material **you own** and get a complete private
course:

- a chaptered **audio lecture series** (M4B) with a companion **EPUB**
  and study guide — the pipeline already in this repo;
- an honest **drill coach**: questions relayed verbatim from the
  corpus's own assessment material, answer + confidence collected
  before any reveal, grading only against the official key, misses
  requeued at 7/20/40 minutes, mastery re-verified across days,
  confident-but-wrong answers prioritized as blindspots;
- **Anki decks** with stable GUIDs that update in place instead of
  duplicating (already in this repo);
- a **subject expert** served over MCP to any agent client, grounded in
  the corpus with citations, carrying persistent learner memory;
- **learner memory you own**: markdown and JSON in a git repo, readable
  and revertible, served by the
  [refrain](https://github.com/gallowaysoftware/refrain) memory service.

The tagline that carries the differentiation: **it never writes
questions, never grades from its own opinion, and never forgets what
you got wrong.** Where the corpus carries no assessment material,
generated questions are allowed only through a staged human
accept/edit/reject review, and are always labeled generated — the two
banks are never silently mixed.

## Positioning constraints (these are features)

- **Own material only.** Copyright makes anything else impractical, so
  the product does not pretend otherwise: it compiles material you own
  or have rights to hold — purchased course content, your notes, your
  org's docs, archives you legally hold. Nothing is redistributed;
  the pipeline ships with no content. This is also why local-first is
  the design center rather than a preference: the corpora this tool is
  *for* are exactly the ones you cannot upload to a cloud service.
- **Local drives everything.** The full stack — LLM, TTS, vision,
  covers, embeddings — runs on your own hardware via
  [vibe](https://github.com/gallowaysoftware/vibe) capability profiles.
  An external key (any OpenAI-compatible provider: OpenRouter,
  Moonshot, z.ai, or a router on another box) is the no-GPU on-ramp for
  the text legs — drill, tutor, question generation, ingestion — and is
  configured the same way as any other endpoint. Audio, vision, and
  cover art remain local-stack; the plan does not promise an M4B from
  an API key.
- **No owned UI.** Surfaces are the terminal, MCP clients the user
  already has (chat apps, coding agents), Anki, podcast players, and
  e-readers.

## Architecture target

One fast-moving product repo (this one) over frozen service surfaces:

- **this repo** — the compiler + renderers + drill + the `course.yaml`
  contract. The only repo that moves fast.
- **vibe** — engine (profiles, router, pipeline substrate), consumed as
  a Go module. Unchanged.
- **refrain** — per-expert learner memory (digest injection + MCP
  tools). Gains a structured mastery slot (below) and stays otherwise
  frozen.
- **knowledge service** (private today) — hybrid retrieval over
  subreddit + document corpora with coverage watermarks and
  hardware-fit tools. Joins as an optional power-up in the final phase
  as a fresh-history public excision. The document-corpus path is
  already proven (the distilling curriculum runs through it); the
  drill coach predecessor lives in a private repo and is extracted in
  Phase 1.

### Contracts

- **`course.yaml`** — one manifest per course: slug, the five domain
  labels (subject / program / persona / assessment / cover prompt),
  voice, endpoints, assessment-marker config. Everything renders from
  it (expert prompt, memory binding, client wiring), and
  `doctor` live-verifies that the bound services agree on the slug —
  identity drift becomes a caught error, not a convention.
- **Lesson interchange format** — `Module_*/Lesson_*/lesson.md` +
  `images/`, i.e. this repo's existing input contract, promoted to a
  written spec with a validator. Ingestion targets it; third parties
  integrate against it (a spec, not an API to maintain).
- **Question bank** — verbatim question + official answer + difficulty
  + citation + stable ID (hash of unit + question identity, not
  position), so re-extracting after a corpus edit migrates mastery
  state instead of orphaning it. Provenance (`official` vs
  `generated`) is first-class.
- **MCP topology** — plain servers, no gateway: this repo's `serve`
  (assessment + course search), refrain (memory), later the knowledge
  service. Optional shared bearer token; localhost-default binds;
  trust posture documented honestly.

### The trust dial

The drill exists in two proven generations: a prompt-driven examiner
that authors questions and marking schemes (viable with frontier-class
models), and a relay-not-author coach where code owns the bank, the
grading keys, and the scheduler (correct for local models). The product
ships the dial, not one end: bank-first extraction where the corpus
carries assessment material; generate-second through critique/tournament
plus staged review where it doesn't; authored-question modes unlock only
when a frontier-class grader is configured, and always labeled.

### Converge, don't one-shot

Where a stage makes a judgement with a wide solution space — how to
split a book into lessons, how to word a question, which of several
lecture framings teaches best — a single pass is the wrong shape. The
sibling repos in this family converged on the same answer: generate
several candidates, critique them against stated criteria, score, and
synthesize, then put a human gate in front of anything that becomes
durable. It costs more tokens and produces materially better output
than iterating on one draft.

Applies to lesson structuring (Phase 3), generated questions (Phase 2),
and lecture drafting. It does NOT apply to anything code can decide:
question relaying, grading against a key, arithmetic, and scheduling
stay deterministic. Convergence is for judgement, not for facts.

### Grading is the guarded leg

"Grades you honestly" cannot rest on a prompt contract alone:

- grading keys are decomposed into point-level rubrics at extraction;
- a **grading eval harness** (`eval grading --llm-url ...`) scores any
  candidate grader against a golden set of answer/grade pairs, so users
  qualify a model before trusting it;
- an optional server-side grading mode keeps grading authority with the
  course owner instead of whatever client connects.

### The memory bridge (the signature feature)

Drill results steer the tutor: every session ends with a structured
mastery summary (coverage, blindspots, streaks) written into refrain,
so the next tutor conversation — in any client — opens already knowing
what you missed. Mechanics: session-log append as the interim channel;
the durable design is a small structured-state API in refrain plus a
dedicated digest section for mastery, so no later chat session can
evict it. Exact numbers are always fetched via tools, never retrieved
from prose.

## Phases

Each phase has one deliverable and an exit criterion in the user's
units. Two public beats, one brand.

Individual work items live as GitHub issues labelled `phase-1` through
`phase-5`; this document holds the shape and the exit criteria, the
issue tracker holds the queue.

### Phase 0 — names and contracts (done, 2026-07-31)

- [x] Rename this repo/module to `etude`.
- [x] Rename the memory service: recall → refrain (getrecall.ai
      collision).
- [x] Registry/trademark sweep for `etude`. Verdict: keep it. The two
      things that gate a CLI are clean — the binary name collides with
      nothing in Homebrew, MacPorts, Debian, Ubuntu, Arch, AUR, or
      nixpkgs, and crates.io is free. The only ETUDE trademark ever
      filed for education software was cancelled in 2004; every live
      one is cosmetics, wine, or beauty services — distant classes.
      Accepted costs: npm and PyPI `etude` are permanently taken (use
      `etude-cli` if ever needed), the GitHub org name is taken, and
      the word is overloaded enough that we will always ship the
      qualifier ("etude — the course compiler"). One name to re-check
      before launch: an "Etude AI Inc." using the same word and the
      same deliberate-practice metaphor, currently unsubstantiated.
- [x] `docs/course-format.md` (lesson interchange spec) +
      `course validate`.
- [x] `docs/course-yaml.md` (manifest spec) + `init` scaffolding.
- [x] `docs/excision-checklist.md` — the gate on every future extraction
      from a private repo (tracked files only, fresh history, classify
      before editing, mechanised greps).
- [x] Tag v0.3.0. The pre-rename tags declare the old module path and
      can never be installed under the new one (the module mirror is
      immutable), so the new name needed an installable version.

Exit met: `etude init` scaffolds a course a stranger could fill in, and
`etude course validate` refuses it until they do.

### Phase 1 — v0.1 "The Coach" (public beat one)

Extract the drill from its private home into this repo, beside the
question-generation and Anki machinery that already lives here.

- [x] `internal/qbank`: assessment-material extractor with
      configurable markers (heading patterns, difficulty conventions,
      figure refs) — the current curriculum-specific parsing becomes
      the first marker preset; synthetic fixtures in public CI.
- [x] `internal/study`: the successive-relearning store, semantics
      preserved verbatim (graduate after 2 confident-correct, 7/20/40
      min requeue, 1/2/4-day re-verify, un-master on miss, blindspot
      priority, diagnostic sweep first); stable-ID migration on
      re-extract.
- [x] `drill` — terminal REPL over the store (answer + confidence
      before reveal, point-by-point grading, citations).
- [x] `serve` — MCP: study_next_item / study_record_result /
      study_report / study_gaps / study_coverage + the coach system
      prompt; `skill` — the CLI-driving agent-harness file (one agent,
      three frontends: REPL, chat client, agent skill).
- [x] `eval grading` — golden answer/grade pairs + harness; documented
      minimum-grader guidance.
- [x] Endpoint config: everything text takes `--llm-url`/`ETUDE_LLM_*`
      — local router or any OpenAI-compatible provider.
- [x] Demo course: authored (`examples/demo-course/`, slug home-llms)
      with its own SAQ bank, model answers, figures, math lesson, and a
      23-pair golden grading set. Still open: fetch-nothing prebuilt
      artifacts on a release (sample M4B chapters, EPUB, .apkg, a drill
      asciinema).
- [x] Maintainer kit: issue templates demanding `doctor` output,
      SUPPORT.md with published non-goals, Discussions on. Weekly
      triage is process, not code.

Exit: 2–3 recruited strangers reach a graded drill answer within ten
minutes of the README, from a local endpoint or an external key,
before any announcement. Soft first-person launch.

### Phase 2 — v0.2 "The Loop" (public beat two)

- [x] Mastery digest → refrain session log (interim channel).
- [x] refrain: structured-state API + dedicated mastery digest slot;
      ship its documented-but-missing Claude Code SessionStart hook.
- [x] Expert prompt rendered from course.yaml: corpus-is-true /
      memory-is-decided epistemics, the disagreement protocol (check
      recorded rationale before contradicting; say plainly when the
      curriculum says you're wrong, with the citation), read-only
      default toolset over untrusted corpora, confirm-gated writes
      enforced in code rather than prompt.
- [ ] `search_course` MCP tool: lexical-first (SQLite FTS5 over the
      lesson tree + processed notes, citations = lesson path +
      heading); embeddings optional later — the expert leg gets a real
      retrieval surface without a database dependency.
- [ ] Generated-question funnel: rag MC/SA enrichment →
      critique/tournament → staged accept/edit/reject → labeled bank.
- [ ] Scheduler no-exam-date mode for open-ended domains (games,
      lore): steady-state review instead of run-up-to-criterion.
- [ ] `doctor` verifies slug agreement across course.yaml, refrain,
      and configured MCP endpoints.

Exit: the recorded day-two demo — a missed drill answer visibly opens
the next day's tutor session in two different MCP clients. Second
public beat, led by that recording.

### Phase 3 — v0.3 "The Intake"

The largest gap between this and "point it at what you want to learn".
Everything downstream assumes a lesson tree, and most material is not
organised into lessons — a book has chapters, an export has files, a
wiki has pages. The reference corpus arrived pre-arranged only because
it was scraped from a structured course; that was luck, not the normal
case.

Splitting source material into lessons is the highest-leverage
judgement in the pipeline: a bad breakdown degrades the lectures, the
study guide, and the question bank at once, and stays invisible until
you listen to the output. So it is built as a converging quality
problem rather than a file conversion.

- [ ] `ingest`: EPUB/HTML/markdown-dir → lesson tree. Deterministic
      first (pandoc, heading segmentation, figure extraction and
      placement), then the structuring pass, then the human gate.
- [ ] Structuring pass: propose several candidate breakdowns at
      different granularities, critique each against explicit criteria
      (does each lesson stand alone, is it one sitting's worth, do
      prerequisites precede dependents, are figures with their
      referring text), score, and synthesize the best boundaries.
- [ ] Staged review of the proposed outline — accept / edit / reject
      before any lesson tree is written.
- [ ] Output passes `etude course validate` clean, including the rules
      the reference corpus violated (padded, non-restarting lesson
      numbers; one H1 per lesson; figures beside their text).
- [ ] PDF stays excluded (pandoc cannot read it; OCR is a tarpit;
      DRM'd files are out of scope, stated in docs).

Exit: a real DRM-free EPUB goes from file to first graded drill in one
command plus one review pass.

### Phase 4 — v0.4 "The Course"

- [ ] Renderers read course.yaml; one manifest drives M4B + EPUB +
      deck + drill + expert.
- [ ] Fidelity-audit stage validated on a GPU run (not marketed until
      then).
- [ ] Published timing/VRAM table per hardware tier and measured
      external-key cost estimates for the text legs.

Exit: the demo course reproduces end-to-end from published code on
both a single-GPU box and the external-key tier (text legs).

### Phase 5 — v1.0 "The Commons"

- [ ] Knowledge service published as a fresh-history excision: config
      instead of embedded inventory, bearer token, bring-your-own-dump
      compliance docs for community corpora.
- [ ] Coverage watermarks reach the tutor; hardware-fit answers
      (can_i_run) join `doctor`.
- [ ] The noisy-archive → curriculum distillation path gets its first
      production caller (community corpus → reviewed lesson slate →
      drillable course). Derived artifacts from third-party archives
      are never shipped; the tooling is.

Exit: one user-supplied archive becomes a drillable, tutorable course
using only public tooling.

## Honesty ledger (kept current in the README)

- The external-key tier covers drill, tutor, question generation, and
  ingestion. It does not produce the M4B — TTS, vision, and covers are
  local-stack.
- Local TTS loses to produced cloud audio on polish; the audio leg is
  positioned on structure (a lecture series, not banter), length,
  privacy, and zero marginal cost.
- Frontier chat products are better conversationalists than any local
  model; the value here is grounding + memory + the loop, and the same
  loop accepts a frontier endpoint.
- English-only at launch (embedder, FTS config, TTS normalization,
  prompts). Declared, not discovered.
- macOS/Linux. Windows is not a commitment.
- The full three-leg loop has run end-to-end for one real corpus (a
  brewing & distilling diploma). That is the existence proof, not a
  testimonial base.
- Client conformance varies: confidence-before-reveal and same-turn
  recording are prompt contracts; tested clients are documented,
  others best-effort.

## Non-goals

No web UI, dashboard, hosted service, accounts, or telemetry. No custom
chat or mobile apps. No multi-user or classroom features. No FSRS
reimplementation (Anki interop over competing with its ecosystem). No
PDF/OCR/DRM ingestion. No live question invention during drills. No
shipped curriculum content. No automated fetching of third-party
archives. No video leg. No gamification. No feature-chasing cloud
notebooks (mind maps, video overviews). No plugin API promise —
integration points are the specs.

## Decisions (resolved 2026-07-31)

1. **Repo rename: done.** This repo is `gallowaysoftware/etude`
   (formerly textbook-to-audiobook); GitHub redirects cover the old
   name. Only the registry/trademark sweep remains open in Phase 0.
2. **Demo course: author it.** A small original course with its own
   question bank and model answers — no surveyed public corpus ships
   questions *with* answer keys (e.g. OpenStax keeps model answers
   instructor-only), so the extract path's happy path must be owned
   material. One-time labor, permanent artifact, doubles as docs.
3. **Grading default: eval harness + client-side grader.** Golden
   answer/grade pairs and `eval grading` qualify whatever model the
   user brings, with a documented minimum-grader floor; server-side
   grading stays an option flag, not the default.
4. **Headline persona: the exam-dated studier.** The launch story is
   the exam run-up; games and open-ended domains ride the same
   machinery ("the exam is optional; the loop isn't"), and the
   scheduler grows a no-exam-date mode in Phase 2.
