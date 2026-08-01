package course

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"text/template"
)

//go:embed templates/course.yaml.tmpl
var courseTemplate string

//go:embed templates/lesson.md.tmpl
var lessonTemplate string

// ScaffoldOptions configures a new course tree.
type ScaffoldOptions struct {
	// Dir is the course directory to create or fill.
	Dir string
	// Slug is the course identity. Empty scaffolds a placeholder the
	// author must replace.
	Slug string
	// Title is the human-readable course name. Empty scaffolds a
	// placeholder.
	Title string
	// Modules is how many module directories to stub out.
	Modules int
	// Preset names an assessment-marker preset, or "none".
	Preset string
	// SampleLesson writes one example lesson per module, which doubles
	// as inline documentation of the format.
	SampleLesson bool
	// Force allows writing into a directory that already holds a
	// manifest.
	Force bool
}

// Scaffold writes a new course skeleton and returns the paths it
// created, in creation order.
func Scaffold(opts ScaffoldOptions) ([]string, error) {
	if opts.Dir == "" {
		return nil, fmt.Errorf("course directory is required")
	}
	if opts.Modules < 1 {
		opts.Modules = 1
	}
	if opts.Preset == "" {
		opts.Preset = "none"
	}
	if opts.Preset != "none" {
		if _, ok := Presets[opts.Preset]; !ok {
			return nil, fmt.Errorf("unknown assessment preset %q", opts.Preset)
		}
	}
	if opts.Slug != "" && !slugPattern.MatchString(opts.Slug) {
		return nil, fmt.Errorf("slug %q: must be lowercase letters, digits, and hyphens, starting with a letter or digit, at most 64 characters", opts.Slug)
	}

	manifestPath := filepath.Join(opts.Dir, FileName)
	if _, err := os.Stat(manifestPath); err == nil && !opts.Force {
		return nil, fmt.Errorf("%s already exists (use --force to overwrite)", manifestPath)
	}

	if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
		return nil, err
	}

	data := scaffoldData(opts)
	manifest, err := render("course.yaml", courseTemplate, data)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(manifestPath, manifest, 0o644); err != nil {
		return nil, err
	}
	created := []string{manifestPath}

	for i := 1; i <= opts.Modules; i++ {
		moduleDir := filepath.Join(opts.Dir, fmt.Sprintf("Module_%d", i))
		if err := os.MkdirAll(moduleDir, 0o755); err != nil {
			return created, err
		}
		created = append(created, moduleDir)

		if !opts.SampleLesson {
			continue
		}
		lessonDir := filepath.Join(moduleDir, "Lesson_01_Example")
		if err := os.MkdirAll(filepath.Join(lessonDir, "images"), 0o755); err != nil {
			return created, err
		}
		lesson, err := render("lesson.md", lessonTemplate, data)
		if err != nil {
			return created, err
		}
		lessonPath := filepath.Join(lessonDir, "lesson.md")
		if err := os.WriteFile(lessonPath, lesson, 0o644); err != nil {
			return created, err
		}
		created = append(created, lessonPath)
	}
	return created, nil
}

// scaffoldFields is the template context for the scaffolded files.
type scaffoldFields struct {
	Slug    string
	Title   string
	Modules []int
	Preset  string
	// Placeholder is the prefix Validate rejects, exposed to the
	// template so the two never drift apart.
	Placeholder string
}

func scaffoldData(opts ScaffoldOptions) scaffoldFields {
	f := scaffoldFields{
		Slug:        opts.Slug,
		Title:       opts.Title,
		Preset:      opts.Preset,
		Placeholder: placeholderPrefix,
	}
	if f.Slug == "" {
		f.Slug = placeholderPrefix + "course-slug"
	}
	if f.Title == "" {
		f.Title = placeholderPrefix + "with the course title"
	}
	for i := 1; i <= opts.Modules; i++ {
		f.Modules = append(f.Modules, i)
	}
	return f
}

func render(name, tmpl string, data scaffoldFields) ([]byte, error) {
	t, err := template.New(name).Parse(tmpl)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
