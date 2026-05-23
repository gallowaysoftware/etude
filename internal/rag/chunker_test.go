package rag

import "testing"

func TestChunkParagraphs_RespectsBudget(t *testing.T) {
	text := "Para one.\n\nPara two is medium length.\n\nPara three is also medium length here."
	chunks := chunkParagraphs(text, 30)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d: %v", len(chunks), chunks)
	}
	for _, c := range chunks {
		// Single paragraphs larger than budget are emitted alone —
		// that's documented behavior, not a budget violation.
		if len(c) > 30 {
			isWholePara := false
			// Find this chunk in original; if it's a whole single
			// paragraph that exceeded budget, the over-size is OK.
			for _, p := range []string{"Para one.", "Para two is medium length.", "Para three is also medium length here."} {
				if c == p {
					isWholePara = true
					break
				}
			}
			if !isWholePara {
				t.Errorf("chunk over budget AND not a single paragraph: %q (len %d)", c, len(c))
			}
		}
	}
}

func TestChunkParagraphs_PackTwoParagraphs(t *testing.T) {
	// Each paragraph is well under the 100-char budget; both should
	// fit in one chunk.
	text := "First para is short.\n\nSecond para is also short."
	chunks := chunkParagraphs(text, 100)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 packed chunk, got %d: %v", len(chunks), chunks)
	}
}

func TestChunkParagraphs_Empty(t *testing.T) {
	if chunks := chunkParagraphs("", 100); len(chunks) != 0 {
		t.Errorf("empty input should produce no chunks; got %v", chunks)
	}
	if chunks := chunkParagraphs("   \n\n   ", 100); len(chunks) != 0 {
		t.Errorf("whitespace-only input should produce no chunks; got %v", chunks)
	}
}

func TestChunkLessons_StableIDs(t *testing.T) {
	// Map-iteration order is randomised in Go; the chunker MUST
	// produce identical IDs across runs of the same input. We
	// verify this by chunking the same lessons twice and comparing
	// ID slices.
	lessons := &ProcessedLessons{Items: []ProcessedLesson{{
		Lesson:         "Lesson 1",
		LectureContent: "Some prose paragraph for chunking.",
		Definitions: map[string]string{
			"α-amylase": "An enzyme that cleaves alpha-1,4 bonds.",
			"β-amylase": "An enzyme that cleaves maltose units.",
			"limit dextrinase": "An enzyme that cleaves alpha-1,6 bonds.",
		},
		KeyNumbers: map[string]string{
			"64°C": "Optimum temperature for β-amylase",
			"70°C": "Optimum temperature for α-amylase",
		},
	}}}
	a := ChunkLessons("module_x", lessons, 1000)
	b := ChunkLessons("module_x", lessons, 1000)
	if len(a) != len(b) {
		t.Fatalf("chunk counts differ between runs: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].ID != b[i].ID {
			t.Errorf("chunk %d ID differs between runs: %q vs %q", i, a[i].ID, b[i].ID)
		}
		if a[i].Text != b[i].Text {
			t.Errorf("chunk %d Text differs between runs: %q vs %q", i, a[i].Text, b[i].Text)
		}
	}
}

func TestChunkLessons_NoDuplicateIDsWithCollidingSlugs(t *testing.T) {
	// Earlier versions slugified the term into the chunk ID and hit
	// DuplicateIDError on Chroma upsert because α-amylase and
	// β-amylase both collapsed to "amylase". Guard against
	// regression.
	lessons := &ProcessedLessons{Items: []ProcessedLesson{{
		Lesson: "Lesson 1",
		Definitions: map[string]string{
			"α-amylase":         "alpha enzyme",
			"β-amylase":         "beta enzyme",
			"limit dextrinase":  "branching enzyme",
			"amyloglucosidase":  "glucose-producing enzyme",
		},
	}}}
	chunks := ChunkLessons("m", lessons, 1000)
	seen := map[string]bool{}
	for _, c := range chunks {
		if seen[c.ID] {
			t.Fatalf("duplicate chunk id: %s", c.ID)
		}
		seen[c.ID] = true
	}
}
