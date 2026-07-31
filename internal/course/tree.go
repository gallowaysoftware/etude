package course

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Lesson-tree limits enforced by the vamp template functions the
// pipeline calls. They are reproduced here because the failure mode on
// the pipeline side is SILENT: enumerateLessons drops an oversized or
// lesson.md-less directory from the course without a word, and the batch
// readers filter oversized files out of prompt context the same way. A
// course that validates clean is one where nothing disappears.
const (
	// maxLessonBytes mirrors enumerateLessons: a lesson.md above this is
	// dropped from the lesson list entirely, so the lesson never reaches
	// any stage.
	maxLessonBytes = 1024 * 1024
	// batchReadBytes mirrors readFiles/readLessons/readFileBatch: a file
	// above this is filtered out of batch prompt context, so the lesson
	// is listed and rendered but its content may be missing from
	// corpus-wide stages.
	batchReadBytes = 200 * 1024
	// truncateBytes is the tighter of the two prompt truncation caps the
	// pipeline applies per lesson (120k in the study-guide prompt, 150k
	// in the lesson-processing prompt). Past it the two stages no longer
	// see the same lesson, which is a quieter and more confusing failure
	// than losing the lesson outright.
	truncateBytes = 120 * 1000
)

// imageExts is the set enumerateUniqueImages and
// imageDescriptionsForLesson accept. Anything else in an images/
// directory is ignored by the vision stage.
var imageExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".webp": true, ".bmp": true, ".svg": true,
}

// Severity distinguishes "this course is broken" from "this course will
// build but something will be missing or surprising".
type Severity string

const (
	// Error means a stage will fail or content will vanish from the
	// build.
	Error Severity = "error"
	// Warning means the build succeeds but the result is degraded or
	// the author probably did not intend it.
	Warning Severity = "warning"
)

// Finding is one diagnostic against a course tree.
type Finding struct {
	Severity Severity
	// Path is relative to the course directory when possible, so output
	// is readable and diffable.
	Path    string
	Message string
}

func (f Finding) String() string {
	if f.Path == "" {
		return fmt.Sprintf("%s: %s", f.Severity, f.Message)
	}
	return fmt.Sprintf("%s: %s: %s", f.Severity, f.Path, f.Message)
}

// Report is the result of scanning a course tree.
type Report struct {
	Findings []Finding
	// Modules summarises what the scan found, in manifest order.
	Modules []ModuleSummary
}

// ModuleSummary is the per-module tally a human wants to see: the point
// of validate is as much "did it find everything" as "is it valid".
type ModuleSummary struct {
	Num     int
	Dir     string
	Lessons int
	Images  int
	// Assessment counts lessons matching the assessment lesson pattern,
	// i.e. the ones the drill's bank extractor will read.
	Assessment int
	// SVG and SVGOnly track vector figures, the latter being those with
	// no raster sibling. Both are module-level because SVG-heavy corpora
	// are the norm, not the exception.
	SVG     int
	SVGOnly int
}

// Errors reports whether the scan found anything fatal.
func (r *Report) Errors() int { return r.count(Error) }

// Warnings reports how many advisory findings the scan produced.
func (r *Report) Warnings() int { return r.count(Warning) }

func (r *Report) count(s Severity) int {
	n := 0
	for _, f := range r.Findings {
		if f.Severity == s {
			n++
		}
	}
	return n
}

func (r *Report) add(sev Severity, path, format string, args ...any) {
	r.Findings = append(r.Findings, Finding{
		Severity: sev,
		Path:     path,
		Message:  fmt.Sprintf(format, args...),
	})
}

// ScanTree walks the course's lesson tree and reports everything that
// would break, vanish, or surprise. It is the content half of
// validation; Manifest.Validate covers the manifest itself.
func (m *Manifest) ScanTree() *Report {
	rep := &Report{}

	src := m.SourceDir()
	if info, err := os.Stat(src); err != nil || !info.IsDir() {
		rep.add(Error, m.rel(src), "source directory does not exist")
		return rep
	}

	markers := m.Assessments.Resolved()
	for _, mod := range m.Modules {
		rep.Modules = append(rep.Modules, m.scanModule(rep, mod, markers))
	}

	m.reportUnlistedModules(rep)
	return rep
}

// scanModule validates one module directory and returns its tally.
func (m *Manifest) scanModule(rep *Report, mod Module, markers Assessments) ModuleSummary {
	dir := m.ModuleDir(mod)
	sum := ModuleSummary{Num: mod.Num, Dir: m.rel(dir)}

	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		rep.add(Error, m.rel(dir), "module %d: directory does not exist", mod.Num)
		return sum
	}

	// The same glob the pipeline runs, so validation sees exactly what
	// the build will see.
	matches, err := filepath.Glob(filepath.Join(dir, "Lesson_*"))
	if err != nil {
		rep.add(Error, m.rel(dir), "module %d: cannot list lessons: %v", mod.Num, err)
		return sum
	}
	sort.Strings(matches)

	var (
		names   []string
		skipped int
	)
	for _, match := range matches {
		fi, statErr := os.Stat(match)
		if statErr != nil || !fi.IsDir() {
			continue
		}
		name := filepath.Base(match)
		if m.scanLesson(rep, match, name, markers, &sum) {
			names = append(names, name)
		} else {
			skipped++
		}
	}

	sum.Lessons = len(names)
	if sum.Lessons == 0 {
		rep.add(Error, m.rel(dir), "module %d: no usable Lesson_*/ directories (a lesson directory must contain a lesson.md)", mod.Num)
	}
	if skipped > 0 {
		rep.add(Warning, m.rel(dir), "module %d: %d Lesson_*/ director(ies) will be skipped by the build", mod.Num, skipped)
	}
	if sum.SVG > 0 {
		// Two distinct consequences, so say both once: vision needs a
		// rasteriser on the host, and the study guide's figure pass
		// skips SVGs, so vector-only figures never reach the EPUB.
		msg := fmt.Sprintf("module %d: %d SVG figure(s) — the vision stage needs rsvg-convert on the host", mod.Num, sum.SVG)
		if sum.SVGOnly > 0 {
			msg += fmt.Sprintf(", and %d with no raster sibling will be missing from the study guide and EPUB", sum.SVGOnly)
		}
		rep.add(Warning, m.rel(dir), "%s", msg)
	}
	m.reportNonLessonDirs(rep, dir, mod)
	reportLessonOrdering(rep, m.rel(dir), names)
	return sum
}

// scanLesson checks one lesson directory. It returns false when the
// pipeline would drop the lesson entirely, so the caller can count what
// disappears.
func (m *Manifest) scanLesson(rep *Report, dir, name string, markers Assessments, sum *ModuleSummary) bool {
	lessonFile := filepath.Join(dir, "lesson.md")
	fi, err := os.Stat(lessonFile)
	switch {
	case os.IsNotExist(err):
		rep.add(Error, m.rel(dir), "no lesson.md — the build silently drops this directory")
		return false
	case err != nil:
		rep.add(Error, m.rel(lessonFile), "cannot read: %v", err)
		return false
	case fi.IsDir():
		rep.add(Error, m.rel(lessonFile), "is a directory, not a file")
		return false
	}

	switch {
	case fi.Size() == 0:
		rep.add(Error, m.rel(lessonFile), "is empty")
	case fi.Size() > maxLessonBytes:
		rep.add(Error, m.rel(lessonFile), "is %s, over the %s limit — the build silently drops this lesson; split it",
			humanBytes(fi.Size()), humanBytes(maxLessonBytes))
		return false
	case fi.Size() > batchReadBytes:
		rep.add(Warning, m.rel(lessonFile), "is %s, over the %s batch-read limit — corpus-wide stages will omit its content; consider splitting it",
			humanBytes(fi.Size()), humanBytes(batchReadBytes))
	case fi.Size() > truncateBytes:
		rep.add(Warning, m.rel(lessonFile), "is %s: past %s the lecture and study-guide stages truncate at different lengths and stop seeing the same lesson; consider splitting it",
			humanBytes(fi.Size()), humanBytes(truncateBytes))
	}

	m.checkTitle(rep, lessonFile)
	sum.Images += m.scanImages(rep, dir, sum)

	if markers.LessonPattern != "" {
		if ok, _ := filepath.Match(markers.LessonPattern, name); ok {
			sum.Assessment++
		}
	} else if m.HasAssessmentMaterial() {
		sum.Assessment++
	}
	return true
}

// checkTitle warns when a lesson has no leading H1. The pipeline treats
// that first heading as the lesson's title — the study-guide prompt is
// told to open each section with it verbatim — so a lesson without one
// gets an invented title downstream.
func (m *Manifest) checkTitle(rep *Report, lessonFile string) {
	f, err := os.Open(lessonFile)
	if err != nil {
		return
	}
	defer f.Close()

	// The title is the first non-blank line by construction; reading a
	// small prefix keeps validation cheap on a large corpus.
	buf := make([]byte, 4096)
	n, _ := f.Read(buf)
	for _, line := range strings.Split(string(buf[:n]), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "# ") {
			rep.add(Warning, m.rel(lessonFile), "does not open with an H1 title — the study guide will invent one")
		}
		return
	}
}

// scanImages counts usable images and accumulates the SVG tally on the
// module summary. SVG findings are reported per module rather than per
// lesson: a corpus exported from a diagramming tool is frequently SVG
// throughout, and one warning per lesson would bury everything else.
func (m *Manifest) scanImages(rep *Report, lessonDir string, sum *ModuleSummary) int {
	imgDir := filepath.Join(lessonDir, "images")
	entries, err := os.ReadDir(imgDir)
	if err != nil {
		// No images/ directory is normal; anything else is worth saying.
		if !os.IsNotExist(err) {
			rep.add(Warning, m.rel(imgDir), "cannot read images directory: %v", err)
		}
		return 0
	}

	var usable int
	stems := make(map[string]map[string]bool)
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		name := ent.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if !imageExts[ext] {
			rep.add(Warning, m.rel(filepath.Join(imgDir, name)), "unsupported image type %q — the vision stage ignores it", ext)
			continue
		}
		usable++
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		if stems[stem] == nil {
			stems[stem] = map[string]bool{}
		}
		stems[stem][ext] = true
	}

	for _, exts := range stems {
		if !exts[".svg"] {
			continue
		}
		sum.SVG++
		if len(exts) == 1 {
			sum.SVGOnly++
		}
	}
	return usable
}

// reportNonLessonDirs flags directories that hold a lesson.md but do not
// match the Lesson_* glob. Without this, a mis-named directory is
// invisible: the build simply never sees it.
func (m *Manifest) reportNonLessonDirs(rep *Report, moduleDir string, mod Module) {
	entries, err := os.ReadDir(moduleDir)
	if err != nil {
		return
	}
	for _, ent := range entries {
		if !ent.IsDir() || strings.HasPrefix(ent.Name(), "Lesson_") {
			continue
		}
		if _, err := os.Stat(filepath.Join(moduleDir, ent.Name(), "lesson.md")); err == nil {
			rep.add(Warning, m.rel(filepath.Join(moduleDir, ent.Name())),
				"module %d: contains a lesson.md but is not named Lesson_* — the build ignores it", mod.Num)
		}
	}
}

// reportUnlistedModules flags module-shaped directories the manifest
// does not list, since teaching order is explicit and an unlisted module
// simply never builds.
func (m *Manifest) reportUnlistedModules(rep *Report) {
	src := m.SourceDir()
	entries, err := os.ReadDir(src)
	if err != nil {
		return
	}
	listed := make(map[string]bool, len(m.Modules))
	for _, mod := range m.Modules {
		listed[filepath.Base(m.ModuleDir(mod))] = true
	}
	for _, ent := range entries {
		if !ent.IsDir() || !strings.HasPrefix(ent.Name(), "Module_") || listed[ent.Name()] {
			continue
		}
		rep.add(Warning, m.rel(filepath.Join(src, ent.Name())),
			"looks like a module but is not listed in the manifest — it will not build")
	}
}

// reportLessonOrdering warns when lexical order — what the build
// actually uses — diverges from the numeric order the author means.
//
// Two distinct faults produce that divergence and they need different
// advice. Unpadded numbers sort wrong (Lesson_10 before Lesson_2) and
// zero-padding fixes them. Numbers that REPEAT within one module mean
// several units were flattened into one directory, each restarting at
// lesson 1; sorting then interleaves the units, and no amount of padding
// recovers the intended sequence.
func reportLessonOrdering(rep *Report, dir string, names []string) {
	type lesson struct {
		name string
		num  int
	}
	var numbered []lesson
	counts := map[int]int{}
	for _, n := range names {
		if num, ok := lessonNumber(n); ok {
			numbered = append(numbered, lesson{name: n, num: num})
			counts[num]++
		}
	}
	if len(numbered) < 2 {
		return
	}

	var repeated int
	for _, c := range counts {
		if c > 1 {
			repeated++
		}
	}
	if repeated > 0 {
		rep.add(Warning, dir,
			"%d lesson number(s) repeat, so several units share one flat directory and the build interleaves them — give each unit its own module, or renumber the lessons across the whole module",
			repeated)
		return
	}

	for i := 1; i < len(numbered); i++ {
		if numbered[i].num < numbered[i-1].num {
			rep.add(Warning, dir,
				"lesson order is lexical, so %s builds before %s — zero-pad lesson numbers (Lesson_02) to fix the order",
				numbered[i-1].name, numbered[i].name)
			return
		}
	}
}

// lessonNumber extracts the leading number from a Lesson_<n>... name.
func lessonNumber(name string) (int, bool) {
	rest := strings.TrimPrefix(name, "Lesson_")
	if rest == name {
		return 0, false
	}
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 0, false
	}
	return n, true
}

// rel renders a path relative to the course directory for readable
// output, falling back to the absolute path.
func (m *Manifest) rel(path string) string {
	if m.dir == "" {
		return path
	}
	r, err := filepath.Rel(m.dir, path)
	if err != nil || strings.HasPrefix(r, "..") {
		return path
	}
	return r
}

func humanBytes(n int64) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1024*1024))
	case n >= 1024:
		return fmt.Sprintf("%.0f KiB", float64(n)/1024)
	default:
		return fmt.Sprintf("%d B", n)
	}
}
