You split a single lecture segment into TTS-sized chunks for
downstream speech synthesis. The TTS engine (Kokoro) sounds best when
each call receives roughly 100-200 of its internal tokens — empirically
about 1 to 3 normal sentences, or 200-700 characters of clean prose.
Past ~400 tokens it begins to rush; very short single-clause fragments
lose prosodic context. Your job: cut at natural pause boundaries to
land each chunk in that sweet spot.

# Segment you're splitting

This segment belongs to unit `{{ .segment.unit_id }}`, written for an
audio lecture. It is one coherent stretch of lecturer prose; do not
paraphrase, summarize, or reorder it — only split.

# Splitting rules

- Cut on sentence boundaries (`.`, `!`, `?`). Don't split mid-sentence.
- Keep tightly-bound phrases together: a numerical formula and its
  surrounding setup, an entity name and its conditions, a step in a
  process and its parameters. Splitting at a clause inside a single
  factual claim makes the TTS engine read each half with the wrong
  intonation.
- Target chunk size: 200-700 characters of spoken text per chunk
  (1-3 sentences). A 1000-character clause-laden sentence is fine as
  one chunk if it can't be split cleanly; a flurry of short
  declarative sentences combine 2-3 into one chunk.
- Preserve the original text verbatim — same words, same numbers,
  same punctuation — across all chunks. Concatenating the chunks in
  order should yield exactly the segment's `text`, modulo the
  whitespace between chunks.
- Keep paragraph-internal callouts intact. If the segment uses
  phrases like "Now, the key thing to watch out for here is..." or
  "Let's walk through a worked example..." those go in the same
  chunk as the sentence(s) they introduce.

# Output contract

Return ONLY a single JSON object. No prose preamble, no markdown
fences, no commentary.

```
{
  "unit_id": "{{ .segment.unit_id }}",
  "segment_idx": {{ .i }},
  "items": [
    {"text": "<200-700 chars of spoken text for chunk 0>"},
    {"text": "<200-700 chars for chunk 1>"},
    ...
  ]
}
```

The downstream stage flattens these per-segment outputs into one flat
list of chunks (each chunk inherits `unit_id` and `segment_idx`), then
fans the TTS calls out per chunk in order.

# Segment text

{{ .segment.text }}
