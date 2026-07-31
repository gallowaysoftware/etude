package course

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestScaffoldRoundTrip is the contract the scaffold exists to satisfy:
// a fresh course must NOT load (every placeholder is a decision the
// author owes), and must load once those decisions are made. Without the
// first half, a half-filled course produces generically-worded lectures
// that look fine until you listen to them.
func TestScaffoldRoundTrip(t *testing.T) {
	dir := t.TempDir()
	created, err := Scaffold(ScaffoldOptions{Dir: dir, Slug: "home-llms", Title: "Running LLMs at Home", Modules: 2, SampleLesson: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(created) == 0 {
		t.Fatal("Scaffold() created nothing")
	}

	if _, err := Load(dir); err == nil {
		t.Fatal("Load() on a fresh scaffold = nil, want placeholder errors")
	} else if !strings.Contains(err.Error(), "placeholder") {
		t.Fatalf("Load() = %v, want placeholder errors", err)
	}

	manifestPath := filepath.Join(dir, FileName)
	body, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	filled := replacePlaceholders(string(body))
	// Only values matter: the manifest's own comments mention the
	// placeholder prefix by design, explaining the rule to the author.
	for _, line := range strings.Split(filled, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") && strings.Contains(line, placeholderPrefix) {
			t.Fatalf("test helper left a placeholder value behind: %q", line)
		}
	}
	if err := os.WriteFile(manifestPath, []byte(filled), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() after filling placeholders = %v, want success", err)
	}
	if m.Slug != "home-llms" {
		t.Errorf("slug = %q, want the scaffolded value", m.Slug)
	}
	if len(m.Modules) != 2 {
		t.Errorf("modules = %d, want 2", len(m.Modules))
	}

	// The sample lesson must satisfy the tree contract it documents,
	// otherwise `init` hands the user a course that fails validation for
	// reasons they did not cause.
	rep := m.ScanTree()
	if rep.Errors() != 0 {
		t.Errorf("scaffolded tree reported errors: %v", rep.Findings)
	}
	if rep.Modules[0].Lessons != 1 {
		t.Errorf("sample module has %d lesson(s), want 1", rep.Modules[0].Lessons)
	}
}

// replacePlaceholders fills every REPLACE- value with plausible content,
// the way an author would.
func replacePlaceholders(body string) string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if idx := strings.Index(line, placeholderPrefix); idx >= 0 && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			out = append(out, line[:idx]+"filled in by the author")
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func TestScaffoldRefusesToClobber(t *testing.T) {
	dir := t.TempDir()
	if _, err := Scaffold(ScaffoldOptions{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := Scaffold(ScaffoldOptions{Dir: dir}); err == nil {
		t.Fatal("second Scaffold() = nil, want an already-exists error")
	}
	if _, err := Scaffold(ScaffoldOptions{Dir: dir, Force: true}); err != nil {
		t.Fatalf("Scaffold(force) = %v, want success", err)
	}
}

func TestScaffoldValidatesOptions(t *testing.T) {
	tests := []struct {
		name    string
		opts    ScaffoldOptions
		wantErr string
	}{
		{name: "bad slug", opts: ScaffoldOptions{Slug: "Not A Slug"}, wantErr: "must be lowercase"},
		{name: "unknown preset", opts: ScaffoldOptions{Preset: "nonesuch"}, wantErr: "unknown assessment preset"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.opts.Dir = t.TempDir()
			_, err := Scaffold(tc.opts)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Scaffold() = %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestScaffoldPresetReachesManifest(t *testing.T) {
	dir := t.TempDir()
	if _, err := Scaffold(ScaffoldOptions{Dir: dir, Slug: "drilled", Title: "Drilled", Preset: "saq"}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "preset: saq") {
		t.Errorf("scaffolded manifest does not carry the requested preset:\n%s", body)
	}
}
