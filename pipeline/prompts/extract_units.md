You analyze a {{ .inputs.subject_label }} course module's processed
lessons and identify the thematic UNITS. Each unit you identify will
become its own standalone audio lecture — the downstream pipeline runs
your output through a foreach loop, one lecture per unit. Your job is
to (1) cluster the lessons into coherent units and (2) judge how long
each unit's lecture should be from the content density of its lessons.

# How units are organized

Most curricula structure each unit ending with a summary, review, or
SAQ ("Short Answer Question") lesson. Use those terminal lessons'
titles as the canonical unit identity when present. Group all
non-terminal lessons into the unit whose terminal lesson they
correspond to.

If the curriculum uses a different organizing principle (chronological
ordering, dependency-graph topological sort, etc.), follow that
instead. The exact unit boundaries come from the lessons themselves
— don't impose an unfamiliar structure.

Expect roughly 4 to 12 units for a single module.

# Length judgement and pass-splitting

For each unit, judge the FULL lecture duration in minutes from
content density, then split into PASSES of at most 60 minutes each.
The downstream script generator can only reliably hit ~60 minutes
(~8,400 words) of output per LLM call; longer single calls
under-shoot material. Splitting an oversized unit into 2 or 3
sequential passes preserves the per-unit deliverable shape (one final
MP3 per "parent unit") while letting each pass land within the
model's realistic output ceiling.

Total-length heuristic by density:

- **30 minutes** for a small unit (one or two foundational lessons +
  short summary; few numerical processes).
- **45-60 minutes** for a focused unit (3-4 lesson topics; some worked
  numerical examples; moderate detail).
- **75-90 minutes** for a typical full unit (5-7 lessons including
  process flows, equipment selection, SAQ walkthrough).
- **90-120 minutes** for a dense unit (many numerical formulas,
  multiple worked examples, deep process-detail lessons).

Pass-splitting rules:
- If total ≤ 60 min: 1 pass.
- If 61-90 min: 2 passes (e.g. 45 + 45, or 60 + 30 if natural).
- If 91-120 min: 3 passes (e.g. 40 + 40 + 40).
- Each pass covers a coherent contiguous slice of the unit's lessons
  (don't interleave; pass 1 is the first third / half, pass 2 is the
  next, etc.). Each pass gets its own subset of the unit's lessons.
- Pass `target_minutes` is the per-pass time (e.g. 40 for a 120-min
  unit split 3 ways), NOT the unit total.

Be honest. Don't pad a short unit; don't shortchange a dense one.

# Output contract

Return ONLY a single JSON object. No prose preamble, no markdown
fences, no commentary. Emit one item PER PASS — a 1-pass unit
produces one item, a 3-pass unit produces three items with the same
`parent_unit_id`.

```
{
  "items": [
    {
      "unit_id": "<slug: parent_unit_id + optional _partN suffix>",
      "parent_unit_id": "<slug: the unit's identity for grouping>",
      "parent_unit_title": "<human-readable unit title>",
      "title": "<this pass's human-readable title>",
      "lessons": ["<exact lesson directory name>", ...],
      "target_minutes": <integer 30 to 60>,
      "pass_index": <integer 1, 2, or 3 — 1 for single-pass units>,
      "pass_total": <integer 1, 2, or 3 — total passes for this parent>,
      "reason": "<one sentence — why this duration + lesson subset>"
    }
  ]
}
```

Field rules:
- `unit_id` MUST be filesystem-safe (`[a-z0-9_]+`). For single-pass
  units use the parent name. For multi-pass units suffix with
  `_part1`, `_part2`, `_part3`.
- `parent_unit_id` is the same across all passes of one parent unit.
  Downstream stages group by this — it becomes the audio
  subdirectory and the final MP3 basename.
- `title` describes THIS pass specifically when split (e.g. "Topic —
  Part 1: Foundations"). For single-pass units use the parent's title.
- `lessons` is the EXACT lesson directory names from the source
  material for THIS pass only. Don't invent, don't paraphrase. Each
  lesson belongs to exactly one pass — no overlap between passes of
  the same parent.
- `target_minutes` is 30 to 60 for any single pass.
- `pass_index` starts at 1. `pass_total` equals the number of passes
  for that parent.

# Processed lessons

{{ .stages.compact_lessons.output }}
