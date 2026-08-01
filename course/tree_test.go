package course

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// courseTree builds a course on disk. lessons maps a lesson directory
// name to its lesson.md contents; a nil body means "create the directory
// but no lesson.md".
func courseTree(t *testing.T, manifest string, lessons map[string][]byte) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, body := range lessons {
		lessonDir := filepath.Join(dir, "Module_1", name)
		if err := os.MkdirAll(lessonDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if body == nil {
			continue
		}
		if err := os.WriteFile(filepath.Join(lessonDir, "lesson.md"), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func scan(t *testing.T, dir string) *Report {
	t.Helper()
	m, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return m.ScanTree()
}

// findingsFor returns the messages at one severity, for assertions that
// care about what was said rather than how many.
func findingsFor(rep *Report, sev Severity) []string {
	var out []string
	for _, f := range rep.Findings {
		if f.Severity == sev {
			out = append(out, f.Message)
		}
	}
	return out
}

func assertFinding(t *testing.T, rep *Report, sev Severity, substr string) {
	t.Helper()
	for _, msg := range findingsFor(rep, sev) {
		if strings.Contains(msg, substr) {
			return
		}
	}
	t.Errorf("no %s finding containing %q; got %v", sev, substr, findingsFor(rep, sev))
}

func TestScanTreeClean(t *testing.T) {
	dir := courseTree(t, validManifest, map[string][]byte{
		"Lesson_01_Memory": []byte("# Memory\n\nHow weights fit.\n"),
		"Lesson_02_Quants": []byte("# Quantization\n\nWhat q4 costs you.\n"),
	})
	rep := scan(t, dir)
	if rep.Errors() != 0 || rep.Warnings() != 0 {
		t.Fatalf("clean course reported %d error(s), %d warning(s): %v", rep.Errors(), rep.Warnings(), rep.Findings)
	}
	if len(rep.Modules) != 1 || rep.Modules[0].Lessons != 2 {
		t.Fatalf("summary = %+v, want 1 module with 2 lessons", rep.Modules)
	}
}

// TestScanTreeCatchesSilentDrops covers the failures that make
// validation worth running: each of these produces a smaller course with
// no complaint from the pipeline itself.
func TestScanTreeCatchesSilentDrops(t *testing.T) {
	t.Run("missing lesson.md", func(t *testing.T) {
		dir := courseTree(t, validManifest, map[string][]byte{
			"Lesson_01_Good":  []byte("# Fine\n"),
			"Lesson_02_Empty": nil,
		})
		rep := scan(t, dir)
		assertFinding(t, rep, Error, "no lesson.md")
		if rep.Modules[0].Lessons != 1 {
			t.Errorf("counted %d lesson(s), want 1 usable", rep.Modules[0].Lessons)
		}
	})

	t.Run("oversized lesson is dropped", func(t *testing.T) {
		dir := courseTree(t, validManifest, map[string][]byte{
			"Lesson_01_Good": []byte("# Fine\n"),
			"Lesson_02_Huge": make([]byte, maxLessonBytes+1),
		})
		rep := scan(t, dir)
		assertFinding(t, rep, Error, "silently drops this lesson")
	})

	t.Run("batch-read limit warns", func(t *testing.T) {
		dir := courseTree(t, validManifest, map[string][]byte{
			"Lesson_01_Big": make([]byte, batchReadBytes+1),
		})
		rep := scan(t, dir)
		assertFinding(t, rep, Warning, "batch-read limit")
		if rep.Errors() != 0 {
			t.Errorf("batch-read overflow should not be fatal: %v", rep.Findings)
		}
	})

	t.Run("empty lesson", func(t *testing.T) {
		dir := courseTree(t, validManifest, map[string][]byte{"Lesson_01_Void": {}})
		rep := scan(t, dir)
		assertFinding(t, rep, Error, "is empty")
	})

	t.Run("module with no usable lessons", func(t *testing.T) {
		dir := courseTree(t, validManifest, map[string][]byte{"Lesson_01_Bare": nil})
		rep := scan(t, dir)
		assertFinding(t, rep, Error, "no usable Lesson_*/ directories")
	})

	t.Run("missing module directory", func(t *testing.T) {
		dir := courseTree(t, validManifest, nil)
		rep := scan(t, dir)
		assertFinding(t, rep, Error, "directory does not exist")
	})

	t.Run("misnamed lesson directory", func(t *testing.T) {
		dir := courseTree(t, validManifest, map[string][]byte{"Lesson_01_Good": []byte("# Fine\n")})
		stray := filepath.Join(dir, "Module_1", "Chapter_02")
		if err := os.MkdirAll(stray, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(stray, "lesson.md"), []byte("# Orphan\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		rep := scan(t, dir)
		assertFinding(t, rep, Warning, "not named Lesson_*")
	})

	t.Run("unlisted module directory", func(t *testing.T) {
		dir := courseTree(t, validManifest, map[string][]byte{"Lesson_01_Good": []byte("# Fine\n")})
		if err := os.MkdirAll(filepath.Join(dir, "Module_2", "Lesson_01_Ghost"), 0o755); err != nil {
			t.Fatal(err)
		}
		rep := scan(t, dir)
		assertFinding(t, rep, Warning, "not listed in the manifest")
	})
}

// TestScanTreeLessonOrdering pins the lexical-vs-numeric trap:
// Lesson_10 sorts before Lesson_2, silently reordering a course.
func TestScanTreeLessonOrdering(t *testing.T) {
	t.Run("unpadded numbers warn", func(t *testing.T) {
		lessons := map[string][]byte{}
		for _, n := range []string{"Lesson_1_A", "Lesson_2_B", "Lesson_10_C"} {
			lessons[n] = []byte("# Content\n")
		}
		rep := scan(t, courseTree(t, validManifest, lessons))
		assertFinding(t, rep, Warning, "lesson order is lexical")
	})

	t.Run("zero-padded numbers are quiet", func(t *testing.T) {
		lessons := map[string][]byte{}
		for _, n := range []string{"Lesson_01_A", "Lesson_02_B", "Lesson_10_C"} {
			lessons[n] = []byte("# Content\n")
		}
		rep := scan(t, courseTree(t, validManifest, lessons))
		if rep.Warnings() != 0 {
			t.Errorf("padded lessons warned: %v", rep.Findings)
		}
	})

	// A real scraped corpus flattened several units into one module
	// directory, each restarting at lesson 1, which interleaves the
	// units under a bytewise sort. Padding cannot fix that, so the
	// advice has to differ.
	t.Run("repeated numbers get different advice", func(t *testing.T) {
		lessons := map[string][]byte{}
		for _, n := range []string{"Lesson_1_-_Barley", "Lesson_2_-_Milling", "Lesson_1_-_Water", "Lesson_2_-_Yeast"} {
			lessons[n] = []byte("# Content\n")
		}
		rep := scan(t, courseTree(t, validManifest, lessons))
		assertFinding(t, rep, Warning, "repeat")
		for _, msg := range findingsFor(rep, Warning) {
			if strings.Contains(msg, "zero-pad") {
				t.Errorf("advised zero-padding for restarting numbers, which cannot fix it: %q", msg)
			}
		}
	})
}

// TestScanTreeTitle covers the H1 contract: the pipeline treats a
// lesson's first heading as its title and the study-guide prompt is told
// to reuse it verbatim, so a lesson without one gets an invented title.
func TestScanTreeTitle(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantWarn bool
	}{
		{name: "leading h1", body: "# Barley\n\nProse.\n"},
		{name: "blank lines before h1", body: "\n\n# Barley\n\nProse.\n"},
		{name: "no heading", body: "Prose with no title at all.\n", wantWarn: true},
		{name: "starts at h2", body: "## Section\n\nProse.\n", wantWarn: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := courseTree(t, validManifest, map[string][]byte{"Lesson_01_A": []byte(tc.body)})
			rep := scan(t, dir)
			var got bool
			for _, msg := range findingsFor(rep, Warning) {
				if strings.Contains(msg, "H1 title") {
					got = true
				}
			}
			if got != tc.wantWarn {
				t.Errorf("H1 warning = %v, want %v (findings: %v)", got, tc.wantWarn, rep.Findings)
			}
		})
	}
}

func TestScanTreeImages(t *testing.T) {
	writeImages := func(t *testing.T, dir string, names ...string) {
		t.Helper()
		imgDir := filepath.Join(dir, "Module_1", "Lesson_01_Figures", "images")
		if err := os.MkdirAll(imgDir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, n := range names {
			if err := os.WriteFile(filepath.Join(imgDir, n), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	t.Run("counts supported images", func(t *testing.T) {
		dir := courseTree(t, validManifest, map[string][]byte{"Lesson_01_Figures": []byte("# Figures\n")})
		writeImages(t, dir, "one.png", "two.jpg")
		rep := scan(t, dir)
		if rep.Modules[0].Images != 2 {
			t.Errorf("counted %d image(s), want 2", rep.Modules[0].Images)
		}
		if rep.Warnings() != 0 {
			t.Errorf("unexpected warnings: %v", rep.Findings)
		}
	})

	t.Run("unsupported type warns", func(t *testing.T) {
		dir := courseTree(t, validManifest, map[string][]byte{"Lesson_01_Figures": []byte("# Figures\n")})
		writeImages(t, dir, "diagram.pdf")
		rep := scan(t, dir)
		assertFinding(t, rep, Warning, "unsupported image type")
	})

	// SVG findings are reported once per module: a corpus exported from
	// a diagramming tool can be SVG throughout, and per-lesson warnings
	// would bury every other finding.
	t.Run("svg reported once per module", func(t *testing.T) {
		dir := courseTree(t, validManifest, map[string][]byte{"Lesson_01_Figures": []byte("# Figures\n")})
		writeImages(t, dir, "solo.svg", "paired.svg", "paired.png")
		rep := scan(t, dir)
		assertFinding(t, rep, Warning, "rsvg-convert")
		assertFinding(t, rep, Warning, "no raster sibling")
		if rep.Modules[0].SVG != 2 {
			t.Errorf("SVG count = %d, want 2", rep.Modules[0].SVG)
		}
		if rep.Modules[0].SVGOnly != 1 {
			t.Errorf("SVGOnly count = %d, want 1 (the paired SVG is not orphaned)", rep.Modules[0].SVGOnly)
		}
		var svgWarnings int
		for _, msg := range findingsFor(rep, Warning) {
			if strings.Contains(msg, "SVG figure(s)") {
				svgWarnings++
			}
		}
		if svgWarnings != 1 {
			t.Errorf("emitted %d SVG warnings, want exactly 1 per module", svgWarnings)
		}
	})

	t.Run("raster-only figures are quiet", func(t *testing.T) {
		dir := courseTree(t, validManifest, map[string][]byte{"Lesson_01_Figures": []byte("# Figures\n")})
		writeImages(t, dir, "fig.png")
		rep := scan(t, dir)
		if rep.Warnings() != 0 {
			t.Errorf("unexpected warnings: %v", rep.Findings)
		}
	})
}

// TestScanTreeAssessmentMarkers verifies the drill's input is visible at
// validate time: a course claiming assessment material whose pattern
// matches nothing has no questions to extract, and that should be said
// before the drill is empty.
func TestScanTreeAssessmentMarkers(t *testing.T) {
	body := validManifest + "assessment_markers:\n  preset: saq\n"

	t.Run("matching lessons counted", func(t *testing.T) {
		dir := courseTree(t, body, map[string][]byte{
			"Lesson_01_Content":          []byte("# Content\n"),
			"Lesson_02_Unit_SAQ":         []byte("## Question 1\n"),
			"Lesson_03_Cereals_Unit_SAQ": []byte("# Cereals SAQ\n\n## Self-Assessment Questions\n"),
		})
		rep := scan(t, dir)
		if rep.Modules[0].Assessment != 2 {
			t.Errorf("counted %d assessment lesson(s), want 2", rep.Modules[0].Assessment)
		}
	})

	t.Run("no matching lessons", func(t *testing.T) {
		dir := courseTree(t, body, map[string][]byte{"Lesson_01_Content": []byte("# Content\n")})
		rep := scan(t, dir)
		if rep.Modules[0].Assessment != 0 {
			t.Errorf("counted %d assessment lesson(s), want 0", rep.Modules[0].Assessment)
		}
	})
}
