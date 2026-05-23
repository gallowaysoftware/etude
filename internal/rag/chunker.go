package rag

import (
	"fmt"
	"sort"
	"strings"
)

// DefaultChunkMaxChars is the prose-chunk char budget. bge-large
// tokenises technical content (enzymes, chemical names, units) more
// densely than the 4-chars/token average — observed 3.3 chars/token
// in CIBD content. 1600 chars × (1 token / 3.3 chars) ≈ 485 tokens,
// safely under the model's 512-token sequence limit.
const DefaultChunkMaxChars = 1600

// ChunkLessons turns one ProcessedLessons document into a flat slice
// of Chunk rows. Each lesson contributes:
//
//   - N prose chunks from lecture_content, where N depends on the
//     content length + chunkMaxChars (paragraph-respecting splits).
//   - 1 chunk per definitions entry.
//   - 1 chunk per key_numbers entry.
//   - 1 chunk per process entry.
//
// IDs are deterministic given (module, lessonIdx, chunk type, idx)
// so re-running on the same input produces stable identifiers — the
// rag-pack step relies on this for incremental Chroma upserts.
func ChunkLessons(module string, lessons *ProcessedLessons, chunkMaxChars int) []Chunk {
	if chunkMaxChars <= 0 {
		chunkMaxChars = DefaultChunkMaxChars
	}
	var out []Chunk
	for i, lesson := range lessons.Items {
		out = append(out, lessonChunks(module, i, lesson, chunkMaxChars)...)
	}
	return out
}

func lessonChunks(module string, lessonIdx int, lesson ProcessedLesson, chunkMaxChars int) []Chunk {
	var out []Chunk
	base := func(typ ChunkType, idx int, text string) Chunk {
		return Chunk{
			ID:          fmt.Sprintf("%s_l%03d_%s_%d", module, lessonIdx, typ, idx),
			Module:      module,
			LessonIndex: lessonIdx,
			LessonTitle: lesson.Lesson,
			Type:        typ,
			Idx:         idx,
			Text:        text,
		}
	}
	// Prose chunks — paragraph-respecting windows of lecture_content.
	for i, prose := range chunkParagraphs(lesson.LectureContent, chunkMaxChars) {
		out = append(out, base(ChunkTypeProse, i, prose))
	}
	// Definitions — one chunk per term, embedding surface is the
	// "term: definition" form so queries about either side land here.
	// Sort by term first so IDs (which use the position) are stable
	// across runs despite Go's randomised map iteration order, AND
	// so unique because no two terms within a lesson can share an
	// index. Earlier versions slugified the term into the ID and
	// hit DuplicateIDError when "α-amylase" and "β-amylase" both
	// collapsed to "amylase".
	defTerms := sortedKeys(lesson.Definitions)
	for i, term := range defTerms {
		def := lesson.Definitions[term]
		c := base(ChunkTypeDefinition, i, fmt.Sprintf("%s: %s", term, def))
		c.Term = term
		out = append(out, c)
	}
	// Key numbers — value as the "term", "value: context" as the
	// text. Same sort-by-key-for-stable-IDs treatment.
	kn := sortedKeys(lesson.KeyNumbers)
	for i, value := range kn {
		ctx := lesson.KeyNumbers[value]
		c := base(ChunkTypeKeyNumber, i, fmt.Sprintf("%s: %s", value, ctx))
		c.Term = value
		out = append(out, c)
	}
	// Processes — one chunk per process description.
	for i, proc := range lesson.Processes {
		out = append(out, base(ChunkTypeProcess, i, proc))
	}
	return out
}

// chunkParagraphs splits text into chunks respecting paragraph
// boundaries (double-newline). Greedy packing — adds paragraphs to
// the current chunk until the next would push past maxChars, then
// emits and starts a new chunk. Single paragraphs longer than
// maxChars are emitted alone (no mid-sentence splitting; bge-large
// silently truncates input past its 512-token limit, which we accept
// as the failure mode for runaway paragraphs).
func chunkParagraphs(text string, maxChars int) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		s := strings.TrimSpace(cur.String())
		if s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	for _, para := range strings.Split(strings.TrimSpace(text), "\n\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		if cur.Len() > 0 && cur.Len()+len(para)+2 > maxChars {
			flush()
		}
		if cur.Len() > 0 {
			cur.WriteString("\n\n")
		}
		cur.WriteString(para)
	}
	flush()
	return out
}

// sortedKeys returns the keys of a string-keyed map in alphabetical
// order. Used by the chunker to give definition + key-number entries
// stable per-lesson positions despite Go's randomised map iteration.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Compile-time sanity check: strings.Builder used elsewhere in this
// file; keeping the import live without strings.NewReader / etc.
var _ = strings.Builder{}
