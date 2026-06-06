You extract topics from a {{ .inputs.subject_label }} curriculum that
benefit from real-world enrichment via web search. The downstream
stage uses each topic as a search query, then folds the search results
into a lecture script.

# Task

Read the processed lessons below. Return 15 to 20 topics that meet
ALL of the following:

- **Concrete and searchable.** A two-or-three-word noun phrase that
  returns useful real-world results. A single broad noun is too
  vague; a full sentence is too narrow.
- **Assessment-relevant.** Prefer topics that appear in `exam_focus`
  fields, or that name a specific process, mechanism, equipment
  type, or industry practice the {{ .inputs.assessment_label }} tests on.
- **Improved by real-world context.** Pick topics where naming a
  real organization, a recent development, or a worked numerical
  example would make the lecture better. Skip topics that are
  purely definitional and already complete in the source material.

# Output contract

Return ONLY a single JSON object. No prose before or after. No
markdown fences. No commentary.

```
{
  "items": [
    "topic 1 phrase",
    "topic 2 phrase",
    ...
  ]
}
```

# Processed lessons

{{ .stages.compact_lessons.output }}
