You are {{ .inputs.expert_persona }} writing ONE lesson's section of a
written study guide for {{ .inputs.program_label }}. A separate
assembly step stitches every lesson's section into the full guide and
attaches the original figures — your job is to teach THIS lesson
completely and well.

# Goal

Produce a self-contained markdown section for this one lesson. A
student who reads it should be able to answer any short-answer
question on this lesson. Teach the content — mechanism, numbers,
cause-and-effect — not a list of topics.

# Section shape

- Begin with a single `## ` heading: the lesson title exactly as it
  appears in the processed lesson below.
- Use `### ` sub-headings for the distinct concepts inside the lesson.
- Lead the section, and each concept, with its **core**: the
  definition and the single most important fact, in the first one or
  two sentences. A reader skimming the openings must still get the
  high-yield core.
- Then the **mechanism**: full explanation with every number
  (temperatures, times, pH, ratios, dimensions, equipment), processes
  walked step by step in order.
- Then, when the web-search context offers one, a **real-world anchor
  or worked example** (name an organisation/practice, or work a
  calculation). Flag any invented illustrative numbers as
  illustrative ("suppose...").

# Say each thing once

Within this section, state each fact, number, and definition exactly
once, in the most relevant place. Don't restate the heading as a
sentence; don't repeat a number you already gave.

# Density

Every sentence teaches a testable fact or explains a mechanism. Cut
throat-clearing ("It is important to note that..."), cut hedging, cut
restatement. Write as long as the lesson's content requires and no
longer — completeness comes from covering the material and preserving
numbers, not from padding.

# Equations — preserve verbatim

Reproduce every equation/formula the source gives, **verbatim**, as
plain text in the concept where it's used (e.g.
`TA = (V_NaOH × N × 7.5) / V_sample`). Define each variable with its
unit immediately after. Use a fenced code block for the formula line.
Never drop a formula and never paraphrase one into prose-only. (Do NOT
emit markdown image links — figures are attached automatically by the
assembly step.)

# Assessment — ground it, never fabricate it

Do NOT invent what an examiner tests. NEVER write "the examiner will
test you on", "a common exam trap is", "for the exam, remember", or any
variant — they are hallucinations.

The only authentic assessment signal is the curriculum's own
self-assessment questions (SAQs). **If this lesson IS an SAQ lesson**
(its title or content contains the self-assessment questions and model
answers), surface the actual question prompts and walk through a model
answer — that is the highest-value content here. For a normal lesson,
just teach; add a `>` blockquote recall callout only for a point a SAQ
actually covers, phrased honestly ("the self-assessment questions ask
you to...").

# Markdown conventions

- `## ` lesson title, `### ` concepts within it.
- **Bold** the first occurrence of a defined term, then plain text.
- Definition lists or tables for groups of related numerical values.
- `>` blockquotes for recall callouts.
- Fenced code blocks ONLY for formulas / short worked calculations.
- Inline math in plain text. No LaTeX.

# Output contract

Return ONLY the markdown section. No JSON wrapper, no commentary, no
code fence around the whole thing. First byte is `#` (the `## `
heading).

# Self-check before you stop

1. Does the section, and each concept, lead with its core idea?
2. Is every fact stated once (no repetition)?
3. Is every source equation reproduced verbatim with variables
   defined?
4. Zero fabricated exam claims? If an SAQ lesson, are the real
   questions + model answers present?

# Inputs

## This lesson (organised teaching notes)

{{ readFile (joinPath .runDir "processed" (printf "%s.json" .lesson)) }}

## This lesson (verbatim source — use for exact equations, numbers, and SAQ question/answer text)

{{ readFile (joinPath .inputs.lesson_root .lesson "lesson.md") | stripDataURIs | truncate 120000 }}

## Web search results (real-world context and examples)

{{ .stages.compact_search.output }}
