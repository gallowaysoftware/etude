You are reading one diagram from a {{ .inputs.subject_label }} course.
The diagram was authored by the course instructor; treat it as
authoritative. A downstream stage folds your description into the
lecture notes of every lesson that references this image, so write
content a learner will USE — not a vision-AI's surface gloss.

# Source material

You are looking at an image rasterised to fit within 896×896 pixels.
{{ if eq .img.ext ".svg" -}}
The original was an SVG. Its embedded text labels (rendered text
elements, in document order) are reproduced verbatim below — use
these as ground truth for any text you read on the page; the
rasterised pixels are the layout/colour reference.

## Text labels embedded in the SVG

{{ extractSVGText .img.path }}
{{- end }}

# What to extract

- Identify every labelled component, valve, pipe, vessel, control
  point, axis, or annotation.
- Follow the flow direction (arrows, gradients, ordering of items).
- Capture every number on the page — temperatures, flow rates,
  pressures, percentages, ratios, equipment sizes, time values,
  axis units.
- For graphs: name the variables on each axis, describe the shape of
  each curve, identify intersections / inflection points / labelled
  regions.
- For process schematics: list the equipment in order of process
  flow with the material that passes through each.
- For structural / molecular / equipment cross-sections: identify
  the parts and what each does.

# Output contract

Return ONLY a single JSON object. No prose before or after, no
markdown code fences, no commentary.

```
{
  "hash": "{{ .img.hash }}",
  "title": "Short descriptor: what kind of diagram this is",
  "content": "Detailed reading: every label, every number, every
              flow direction, every axis. 100-400 words.",
  "concept": "Which subject this diagram illustrates (one short
              sentence). Lesson-agnostic — the same diagram may
              appear in several lessons."
}
```

# Anti-patterns

- "The image shows..." prefacing every sentence.
- Dropping numerical labels because they're hard to read — say so
  ("temperature axis labelled in °C, top of scale appears to be
  120") rather than omitting. The SVG-embedded text labels above
  are authoritative for any text you can't read clearly.
- Restating the SVG text labels verbatim as the content. Use them
  to ground your reading, then write a synthesis.
