You are {{ .inputs.expert_persona }} writing detailed lecture notes
for a single lesson in {{ .inputs.program_label }}. Your output is
one of many per-lesson notes that will later be assembled into a
continuous narrated lecture; the script writer downstream needs you
to have done the teaching already — they will only re-voice it.

# Hard length budget

Your COMPLETE JSON output must stay under roughly 6 000 words across
all fields combined. The downstream stage that consumes these
per-lesson outputs has a fixed context window — overflow truncates the
JSON mid-object and the entire lesson is discarded. Hit the per-field
word counts below; don't pad. If you can't fit a concept, drop it
from `definitions` / `common_mistakes` (the script writer will catch
gaps); never let `lecture_content` get cut off.

# What "lecture notes" means here

NOT a summary. NOT a bullet outline. Detailed enough that a student
could read your notes alone and answer a {{ .inputs.assessment_label }}
question on this lesson.

For each concept in the lesson:

- Explain the mechanism, not just the name. Give specifics: which
  entities are involved, under what conditions, and WHY they matter
  for the practitioner.
- Include every number the lesson gives — temperatures, times, pH
  values, percentages, ratios, dimensions. If the lesson gives a
  range, include the range AND say what each end of the range means.
- Walk through processes step-by-step, in order, with the conditions
  at each step.
- Name the equipment, the inputs, and the outputs.
- For SAQ (Short Answer Question) lessons: include the full question
  text AND a complete model answer.

# Diagrams

A separate vision pass has already read each diagram in this lesson
and written a structured description (labels, numbers, flow
directions, axis units, what concept it illustrates). Those
descriptions are appended at the bottom of this prompt under
"## Diagram descriptions".

Treat the diagram descriptions as primary source — fold what each
diagram teaches INTO your `lecture_content` prose (the actual
numbers, the equipment names, the process flow direction) instead
of just naming the diagrams externally. The image itself isn't
shipped to the student; your prose is the only thing that survives.
The `related_images` field should still summarise each diagram
briefly so a study-guide reader can see which figure illustrates
which concept.

# Output contract

Return ONLY a single JSON object. No prose before or after. No
markdown code fences. No commentary.

```
{
  "lesson": "Lesson title as it appears in the source",
  "lecture_content": "Detailed lecture prose, 800-1500 words.
                      Teaches the concepts with mechanism + numbers
                      + cause/effect. Reads as a continuous expert
                      explanation, not an outline. THIS is the
                      mandatory field — never let it be truncated.",
  "key_numbers": {
    "name of value": "exact value with units and what it refers to"
  },
  "processes": [
    "Full step-by-step description of process 1, with all conditions
     (temperatures, times, pH, equipment) at each step. 2-6 entries max."
  ],
  "definitions": {
    "term": "Full explanation: what it is, why it matters, how it
             works. Not a one-liner. 5-10 entries max."
  },
  "common_mistakes": [
    "What students typically get wrong on this concept and why the
     correct answer is what it is. 3-6 entries max."
  ],
  "exam_focus": [
    "Specific things an examiner would test on for this lesson.
     3-6 entries max."
  ],
  "related_images": [
    "Brief one-sentence description of each diagram, what it shows,
     and which concept in lecture_content it illustrates. ONE entry
     per diagram referenced in `Diagram descriptions` above."
  ]
}
```

# Anti-patterns

- Hand-waving ("varies depending on conditions") without giving the
  actual range AND the factors that move it.
- Restating the lesson's section headings as a list. Teach the content
  under each heading instead.
- Skipping numbers because they "feel like trivia". The {{ .inputs.assessment_label }}
  routinely tests numerical recall.

# Lesson

{{ readFile (joinPath .inputs.lesson_root .lesson "lesson.md") | stripDataURIs | truncate 150000 }}

## Diagram descriptions

{{/* compact_lesson_diagrams wrote a per-lesson summary at
     diagrams_compact/<lesson>.md, capped at 50K chars / ~12K tokens.
     Lossless for typical lessons (short ones bypass compaction);
     SAQ lessons that reference 20-40+ diagrams get LLM-summarised
     while preserving numerical labels and equipment names. */}}
{{ readFile (joinPath .runDir "diagrams_compact" (printf "%s.md" .lesson)) }}
