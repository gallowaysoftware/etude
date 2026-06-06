package rag

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// WriteFlashcardsTSV emits Anki-importable tab-separated flashcards.
// One row per question: front \t back. MC questions render as
// "Q: <question>\n A. <opt>\n B. ..." on the front and the correct
// letter + explanation on the back. SA questions are front=question,
// back=answer. Tabs and newlines are escaped — Anki's TSV parser
// reads literal "\n" as a line break inside a cell.
func WriteFlashcardsTSV(w io.Writer, chunks []Chunk) error {
	if _, err := fmt.Fprintln(w, "Front\tBack\tLesson\tTags"); err != nil {
		return err
	}
	for _, c := range chunks {
		if c.Enrichment == nil {
			continue
		}
		tags := fmt.Sprintf("module::%s lesson::%03d type::prose", c.Module, c.LessonIndex)
		for _, mc := range c.Enrichment.MultipleChoice {
			letters := []string{"A", "B", "C", "D", "E", "F"}
			var front strings.Builder
			front.WriteString("Q: ")
			front.WriteString(mc.Question)
			for i, opt := range mc.Options {
				if i >= len(letters) {
					break
				}
				front.WriteString("<br>")
				front.WriteString(letters[i])
				front.WriteString(". ")
				front.WriteString(opt)
			}
			var back strings.Builder
			if mc.CorrectIndex >= 0 && mc.CorrectIndex < len(letters) {
				back.WriteString(letters[mc.CorrectIndex])
				back.WriteString(". ")
				if mc.CorrectIndex < len(mc.Options) {
					back.WriteString(mc.Options[mc.CorrectIndex])
				}
			}
			if mc.Explanation != "" {
				back.WriteString("<br><br>")
				back.WriteString(mc.Explanation)
			}
			if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
				escapeTSV(front.String()), escapeTSV(back.String()),
				escapeTSV(c.LessonTitle), escapeTSV(tags+" question_type::mc")); err != nil {
				return err
			}
		}
		for _, sa := range c.Enrichment.ShortAnswer {
			if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
				escapeTSV(sa.Question), escapeTSV(sa.Answer),
				escapeTSV(c.LessonTitle), escapeTSV(tags+" question_type::sa")); err != nil {
				return err
			}
		}
	}
	return nil
}

// WriteStudyQA emits a human-readable markdown Q&A grouped by lesson.
func WriteStudyQA(w io.Writer, chunks []Chunk) error {
	type lessonGroup struct {
		title  string
		index  int
		chunks []Chunk
	}
	groups := map[int]*lessonGroup{}
	for _, c := range chunks {
		if c.Enrichment == nil {
			continue
		}
		g, ok := groups[c.LessonIndex]
		if !ok {
			g = &lessonGroup{title: c.LessonTitle, index: c.LessonIndex}
			groups[c.LessonIndex] = g
		}
		g.chunks = append(g.chunks, c)
	}
	order := make([]int, 0, len(groups))
	for k := range groups {
		order = append(order, k)
	}
	sort.Ints(order)
	if _, err := fmt.Fprintln(w, "# Study Q&A"); err != nil {
		return err
	}
	for _, k := range order {
		g := groups[k]
		if _, err := fmt.Fprintf(w, "\n## %s\n\n", g.title); err != nil {
			return err
		}
		for _, c := range g.chunks {
			if c.Enrichment.Summary != "" {
				fmt.Fprintf(w, "**Summary**: %s\n\n", c.Enrichment.Summary)
			}
			for i, mc := range c.Enrichment.MultipleChoice {
				fmt.Fprintf(w, "**MC %d.** %s\n\n", i+1, mc.Question)
				letters := []string{"A", "B", "C", "D", "E", "F"}
				for j, opt := range mc.Options {
					marker := ""
					if j == mc.CorrectIndex {
						marker = " ✓"
					}
					if j < len(letters) {
						fmt.Fprintf(w, "  %s. %s%s\n", letters[j], opt, marker)
					}
				}
				if mc.Explanation != "" {
					fmt.Fprintf(w, "\n  _%s_\n", mc.Explanation)
				}
				fmt.Fprintln(w)
			}
			for i, sa := range c.Enrichment.ShortAnswer {
				fmt.Fprintf(w, "**SA %d.** %s\n\n", i+1, sa.Question)
				fmt.Fprintf(w, "> %s\n\n", sa.Answer)
			}
		}
	}
	return nil
}

// WriteGlossary emits an alphabetised definitions markdown — pulled
// directly from each lesson's structured definitions dict, deduped
// case-insensitively. No LLM call needed.
func WriteGlossary(w io.Writer, lessons *ProcessedLessons) error {
	merged := map[string]string{}
	keyByCanon := map[string]string{}
	for _, l := range lessons.Items {
		for term, def := range l.Definitions {
			canon := strings.ToLower(strings.TrimSpace(term))
			if _, exists := merged[canon]; !exists {
				merged[canon] = strings.TrimSpace(def)
				keyByCanon[canon] = strings.TrimSpace(term)
			}
		}
	}
	canons := make([]string, 0, len(merged))
	for k := range merged {
		canons = append(canons, k)
	}
	sort.Strings(canons)
	if _, err := fmt.Fprintln(w, "# Glossary"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	for _, k := range canons {
		fmt.Fprintf(w, "**%s** — %s\n\n", keyByCanon[k], merged[k])
	}
	return nil
}

// WriteKeyNumbers emits a reference document of every numeric value
// the lessons flagged as memorisable, grouped by lesson. Pulled
// directly from structured key_numbers; no LLM call.
func WriteKeyNumbers(w io.Writer, lessons *ProcessedLessons) error {
	if _, err := fmt.Fprintln(w, "# Key Numbers"); err != nil {
		return err
	}
	for _, l := range lessons.Items {
		if len(l.KeyNumbers) == 0 {
			continue
		}
		fmt.Fprintf(w, "\n## %s\n\n", l.Lesson)
		// Sort by value string for stable output.
		keys := make([]string, 0, len(l.KeyNumbers))
		for k := range l.KeyNumbers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, v := range keys {
			fmt.Fprintf(w, "- **%s** — %s\n", v, l.KeyNumbers[v])
		}
	}
	return nil
}

// WriteChunksJSONL emits one chunk per line. Source of truth — every
// other RAG export artefact is regenerable from this file.
func WriteChunksJSONL(w io.Writer, chunks []Chunk) error {
	enc := newJSONEncoder(w)
	for _, c := range chunks {
		if err := enc.encode(c); err != nil {
			return err
		}
	}
	return enc.Close()
}

// escapeTSV maps embedded tabs to spaces and newlines to "<br>" so
// the row stays single-line + tab-delimited for Anki / spreadsheet
// import. Lossy on purpose: nothing else in our content currently
// uses tabs intentionally.
func escapeTSV(s string) string {
	s = strings.ReplaceAll(s, "\t", "    ")
	// Normalise carriage returns first — a bare \r or \r\n survives
	// into the TSV otherwise, and csv.DictReader treats \r as a record
	// terminator, splitting one card across two malformed rows.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\n", "<br>")
	return s
}
