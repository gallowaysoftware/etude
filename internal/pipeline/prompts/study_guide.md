You are {{ .inputs.expert_persona }} writing a written study guide for
{{ .inputs.program_label }}. The audio lecture series covers the same
material in narration; this study guide is the readable companion the
student keeps open during revision. Both deliverables teach the content
with full rigor — neither is a summary of the other.

# Goal

Produce a self-contained written lecture in **markdown**. A student
who reads this guide cover-to-cover should be able to answer any
short-answer question on the covered lessons. That means **teaching
the content**, not listing topics.

# Structure

Use markdown headings to organize the guide. The recommended structure
is one top-level heading per major topic area, with sub-sections for
distinct concepts inside each area. Use natural ordering — start with
foundational chemistry/biology/theory, build up to applied operations,
end with quality assessment and SAQ-style worked examples (when the
curriculum includes them).

Within each section, follow this teaching pattern:

1. **What it is and why it matters** (1-2 sentences framing)
2. **Mechanism / process** (full explanation with all the numbers —
   temperatures, times, conditions, ratios, equipment, formulas)
3. **Worked example or real-world anchor** (when the web-search results
   give one — name an organization, cite an industry practice, walk
   through a calculation)
4. **Assessment pointer** (one short note on what an examiner tests
   on, or a common mistake students make — relate this to the
   {{ .inputs.assessment_label }} the student is preparing for)

# Markdown conventions

- Use `##` for major topic sections, `###` for concepts within them.
- Use **bold** for the first occurrence of a defined term, then plain
  text afterward.
- Use definition lists or tables when presenting groups of related
  numerical values.
- Use `>` blockquotes for assessment-pointer callouts at the end of
  each concept, so a student scanning for high-yield notes can spot
  them.
- Use fenced code blocks ONLY for formulas or short worked
  calculations. Don't use code blocks for normal prose.
- Inline math: stay in plain text. No LaTeX.

# Coverage and depth

- **One section per lesson, minimum.** Don't lump three lessons into
  one paragraph — give each its own H3 under the appropriate H2 area.
- Cover every lesson present in the processed input. Don't skip
  lessons that "seem similar to" another — the curriculum tests subtle
  distinctions.
- **Target length: 25,000 to 60,000 words.** Underwriting hides
  assessment-relevant detail. The student will study this guide for
  hours; give them the depth they need.
- Include every numerical value the source material provides. If the
  source gives a range, give the range AND describe what each end of
  the range means in practice.
- For SAQ-style lessons (when the curriculum has them), surface the
  actual question prompts and walk through a model answer in the
  relevant section.

# Output contract

Return ONLY the markdown document. No JSON wrapper. No commentary
preamble or footer. No code fence wrapping the whole document. The
first byte of your output should be the first markdown character (a
`#` for the document title, or text).

Start with a single `# ` H1 title for the whole guide, then a brief
2-3 sentence orientation paragraph telling the reader what the guide
covers and how to use it, then the `##` sections.

# Inputs

NOTE: Inputs are truncated to fit the model's context window. Cover as
much of the curriculum as the truncated input lets you, in order.

## Processed lessons (organized curriculum knowledge)

{{ .stages.compact_lessons.output }}

## Web search results (real-world context and examples)

{{ .stages.compact_search.output }}
