package rag

import (
	"strings"
	"testing"
)

func TestParseEnrichment(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr bool
		summary string
	}{
		{
			name:    "bare json",
			raw:     `{"summary":"s","key_terms":["a"],"multiple_choice":[],"short_answer":[]}`,
			summary: "s",
		},
		{
			name:    "json fence",
			raw:     "```json\n{\"summary\":\"fenced\"}\n```",
			summary: "fenced",
		},
		{
			name:    "bare fence with whitespace",
			raw:     "  ```\n{\"summary\":\"trim\"}\n```  ",
			summary: "trim",
		},
		{
			name:    "prose preamble breaks parse",
			raw:     "Sure! Here is the JSON: {\"summary\":\"x\"}",
			wantErr: true,
		},
		{
			name:    "empty",
			raw:     "",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enr, err := parseEnrichment(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if enr.Summary != tt.summary {
				t.Fatalf("summary = %q, want %q", enr.Summary, tt.summary)
			}
		})
	}
}

func TestValidateEquationsMarkdown(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr bool
		want    string
	}{
		{
			name: "plain markdown",
			raw:  "# Equations\n\n## Topic",
			want: "# Equations\n\n## Topic",
		},
		{
			name: "markdown fence stripped",
			raw:  "```markdown\n# Equations\n```",
			want: "# Equations",
		},
		{
			name:    "prose preamble rejected",
			raw:     "Here are the equations you asked for.",
			wantErr: true,
		},
		{
			name:    "empty rejected",
			raw:     "   \n  ",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateEquationsMarkdown(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (got %q)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCardIDStableAndIndependentOfText(t *testing.T) {
	// The whole point of the change: the id is a function of the chunk
	// id + type + index, never the rendered card text. Two cards that
	// differ only in edited text must keep the same id so the Anki
	// re-import updates rather than orphans.
	a := cardID("module_1_l003_prose_0", "mc", 1)
	b := cardID("module_1_l003_prose_0", "mc", 1)
	if a != b {
		t.Fatalf("cardID not stable: %q vs %q", a, b)
	}
	if !strings.HasPrefix(a, "module_1_l003_prose_0_mc_1") {
		t.Fatalf("unexpected cardID shape: %q", a)
	}
	// Different question index → different id.
	if cardID("c", "mc", 0) == cardID("c", "mc", 1) {
		t.Fatal("cardID collides across question index")
	}
	// MC vs SA at the same index must not collide.
	if cardID("c", "mc", 0) == cardID("c", "sa", 0) {
		t.Fatal("cardID collides across question type")
	}
}

func TestWriteFlashcardsTSV_IDColumn(t *testing.T) {
	chunks := []Chunk{{
		ID:          "module_1_l000_prose_0",
		Module:      "module_1",
		LessonIndex: 0,
		LessonTitle: "Intro",
		Type:        ChunkTypeProse,
		Enrichment: &Enrichment{
			MultipleChoice: []MultipleChoice{{
				Question:     "Q?",
				Options:      []string{"a", "b"},
				CorrectIndex: 0,
			}},
			ShortAnswer: []ShortAnswer{{Question: "SAQ?", Answer: "ans"}},
		},
	}}
	var b strings.Builder
	if err := WriteFlashcardsTSV(&b, chunks); err != nil {
		t.Fatalf("write: %v", err)
	}
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if lines[0] != "ID\tFront\tBack\tLesson\tTags" {
		t.Fatalf("header = %q", lines[0])
	}
	if len(lines) != 3 { // header + 1 MC + 1 SA
		t.Fatalf("expected 3 lines, got %d: %q", len(lines), lines)
	}
	for _, ln := range lines[1:] {
		cols := strings.Split(ln, "\t")
		if len(cols) != 5 {
			t.Fatalf("row has %d columns, want 5: %q", len(cols), ln)
		}
		if !strings.HasPrefix(cols[0], "module_1_l000_prose_0_") {
			t.Fatalf("id column not derived from chunk id: %q", cols[0])
		}
	}
}
