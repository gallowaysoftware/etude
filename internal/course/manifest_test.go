package course

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validManifest is the smallest manifest that loads. Tests mutate a copy
// of it so each case states only what it is exercising.
const validManifest = `
version: 1
slug: home-llms
title: Running LLMs at Home
subject: running language models on your own hardware
program: the Running LLMs at Home course
persona: an engineer who runs models locally
assessment: end-of-unit self-check questions
source: .
modules:
  - num: 1
    topic: how models fit in memory
`

func writeCourse(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{name: "valid", body: validManifest},
		{
			name:    "missing version",
			body:    strings.Replace(validManifest, "version: 1\n", "", 1),
			wantErr: "version: required",
		},
		{
			name:    "future version",
			body:    strings.Replace(validManifest, "version: 1", "version: 99", 1),
			wantErr: "newer than this build understands",
		},
		{
			name:    "slug with uppercase",
			body:    strings.Replace(validManifest, "slug: home-llms", "slug: Home-LLMs", 1),
			wantErr: "must be lowercase",
		},
		{
			name:    "slug with underscore",
			body:    strings.Replace(validManifest, "slug: home-llms", "slug: home_llms", 1),
			wantErr: "must be lowercase",
		},
		{
			name:    "missing persona",
			body:    strings.Replace(validManifest, "persona: an engineer who runs models locally\n", "", 1),
			wantErr: "persona: required",
		},
		{
			name:    "no modules",
			body:    strings.Split(validManifest, "modules:")[0],
			wantErr: "at least one module",
		},
		{
			name: "duplicate module numbers",
			body: validManifest + `  - num: 1
    topic: a second module claiming the same number
`,
			wantErr: "used more than once",
		},
		{
			name:    "module without topic",
			body:    strings.Replace(validManifest, "    topic: how models fit in memory\n", "", 1),
			wantErr: "topic: required",
		},
		{
			// A typo that silently reverts a setting to its default is
			// the failure mode KnownFields exists to prevent.
			name:    "unknown field",
			body:    validManifest + "voise: af_bella\n",
			wantErr: "field voise not found",
		},
		{
			name:    "unknown assessment preset",
			body:    validManifest + "assessment_markers:\n  preset: nonesuch\n",
			wantErr: "unknown (known presets",
		},
		{
			name:    "surviving placeholder",
			body:    strings.Replace(validManifest, "title: Running LLMs at Home", "title: REPLACE-with the course title", 1),
			wantErr: "still holds the scaffolded placeholder",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeCourse(t, tc.body))
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("Load() = %v, want success", err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("Load() = nil, want error containing %q", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("Load() = %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}

// TestLoadReportsEveryProblem verifies errors accumulate. An author
// fixing a scaffolded manifest should see the whole list once, not
// discover the next problem on every re-run.
func TestLoadReportsEveryProblem(t *testing.T) {
	body := `
version: 1
slug: BAD SLUG
title: ""
subject: ""
program: ""
persona: ""
assessment: ""
modules: []
`
	_, err := Load(writeCourse(t, body))
	if err == nil {
		t.Fatal("Load() = nil, want errors")
	}
	for _, want := range []string{"slug", "title: required", "subject: required", "persona: required", "at least one module"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

func TestLoadAcceptsDirectoryOrFile(t *testing.T) {
	dir := writeCourse(t, validManifest)
	for _, path := range []string{dir, filepath.Join(dir, FileName)} {
		if _, err := Load(path); err != nil {
			t.Errorf("Load(%q) = %v, want success", path, err)
		}
	}
}

// TestSlugBindings pins the identity rule the whole product depends on:
// one declared slug names the memory expert and the knowledge
// collection, and an explicit override wins. The live distillery/
// distilling mismatch is what this prevents.
func TestSlugBindings(t *testing.T) {
	m, err := Load(writeCourse(t, validManifest))
	if err != nil {
		t.Fatal(err)
	}
	if got := m.MemorySlug(); got != "home-llms" {
		t.Errorf("MemorySlug() = %q, want the course slug", got)
	}
	if got := m.KnowledgeCollection(); got != "home-llms" {
		t.Errorf("KnowledgeCollection() = %q, want the course slug", got)
	}

	override := validManifest + `expert:
  memory_slug: legacy-expert
  knowledge_collection: legacy-collection
`
	m, err = Load(writeCourse(t, override))
	if err != nil {
		t.Fatal(err)
	}
	if got := m.MemorySlug(); got != "legacy-expert" {
		t.Errorf("MemorySlug() = %q, want the override", got)
	}
	if got := m.KnowledgeCollection(); got != "legacy-collection" {
		t.Errorf("KnowledgeCollection() = %q, want the override", got)
	}
}

func TestAssessmentPresetResolution(t *testing.T) {
	body := validManifest + `assessment_markers:
  preset: saq
  answer_heading: "## Model Answer {n}"
`
	m, err := Load(writeCourse(t, body))
	if err != nil {
		t.Fatal(err)
	}
	got := m.Assessments.Resolved()
	if got.AnswerHeading != "## Model Answer {n}" {
		t.Errorf("explicit answer_heading = %q, want the override to win over the preset", got.AnswerHeading)
	}
	if got.QuestionHeading != Presets["saq"].QuestionHeading {
		t.Errorf("question_heading = %q, want the preset value", got.QuestionHeading)
	}
	if !m.HasAssessmentMaterial() {
		t.Error("HasAssessmentMaterial() = false, want true for a preset course")
	}
}

func TestHasAssessmentMaterial(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "absent block", body: validManifest, want: false},
		{name: "explicit none", body: validManifest + "assessment_markers:\n  preset: none\n", want: false},
		{name: "preset", body: validManifest + "assessment_markers:\n  preset: saq\n", want: true},
		{
			name: "custom headings without preset",
			body: validManifest + "assessment_markers:\n  question_heading: \"### Q{n}\"\n",
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, err := Load(writeCourse(t, tc.body))
			if err != nil {
				t.Fatal(err)
			}
			if got := m.HasAssessmentMaterial(); got != tc.want {
				t.Errorf("HasAssessmentMaterial() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestModuleDirDefaultsAndOverrides(t *testing.T) {
	body := validManifest + `  - num: 2
    topic: quantization
    dir: unit-two
`
	m, err := Load(writeCourse(t, body))
	if err != nil {
		t.Fatal(err)
	}
	mod, ok := m.FindModule(1)
	if !ok {
		t.Fatal("FindModule(1) not found")
	}
	if got := filepath.Base(m.ModuleDir(mod)); got != "Module_1" {
		t.Errorf("ModuleDir default = %q, want Module_1", got)
	}
	mod, ok = m.FindModule(2)
	if !ok {
		t.Fatal("FindModule(2) not found")
	}
	if got := filepath.Base(m.ModuleDir(mod)); got != "unit-two" {
		t.Errorf("ModuleDir override = %q, want unit-two", got)
	}
	if _, ok := m.FindModule(3); ok {
		t.Error("FindModule(3) = found, want missing")
	}
}
