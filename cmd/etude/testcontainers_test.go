package main

// End-to-end integration test: the shipped binary, in a clean container,
// driven through a full drill round via the JSON API. This is the
// ten-minutes-to-first-graded-answer path exercised against the real
// artifact with no host dependencies — no Go toolchain, no endpoint, no
// pre-existing course — so a packaging or asset-embedding break fails
// here rather than at a user's first run.

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestDrillLoopInContainer(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	cli, err := exec.Command("docker", "info", "--format", "{{.ServerVersion}}").Output()
	if err != nil || len(cli) == 0 {
		t.Skip("docker daemon not reachable")
	}

	ctx := context.Background()
	workDir := t.TempDir()

	// Build the binary for the container's platform. CGO is off: the
	// deliverable is a single static binary, and this test is what keeps
	// that honest.
	binPath := filepath.Join(workDir, "etude")
	build := exec.CommandContext(ctx, "go", "build", "-o", binPath, ".")
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build linux binary: %v\n%s", err, out)
	}

	// A minimal course with a two-question bank: enough for a quiz, a
	// confident miss, and the blindspot coming back.
	courseDir := filepath.Join(workDir, "course")
	writeContainerCourse(t, courseDir)

	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:      "alpine:3",
			Entrypoint: []string{"sleep", "infinity"},
			Files: []testcontainers.ContainerFile{
				{HostFilePath: binPath, ContainerFilePath: "/work/etude", FileMode: 0o755},
				{HostFilePath: filepath.Join(courseDir, "course.yaml"), ContainerFilePath: "/work/course/course.yaml", FileMode: 0o644},
				{HostFilePath: filepath.Join(courseDir, "Module_1", "Lesson_01_Fixture_SAQ", "lesson.md"), ContainerFilePath: "/work/course/Module_1/Lesson_01_Fixture_SAQ/lesson.md", FileMode: 0o644},
			},
			WaitingFor: wait.ForExec([]string{"true"}).WithStartupTimeout(30 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start container: %v", err)
	}
	t.Cleanup(func() { c.Terminate(ctx) })

	run := func(args ...string) string {
		t.Helper()
		full := append([]string{"/work/etude"}, args...)
		code, outR, err := c.Exec(ctx, full)
		if err != nil {
			t.Fatalf("exec %v: %v", full, err)
		}
		raw, err := io.ReadAll(outR)
		if err != nil {
			t.Fatal(err)
		}
		out := string(raw)
		if code != 0 {
			t.Fatalf("%v exited %d\n%s", full, code, out)
		}
		// The exec stream is docker's multiplexed format: 8-byte frame
		// headers precede the payload. The API prints exactly one JSON
		// object, so the object boundaries are the reliable extraction.
		if start, end := strings.Index(out, "{"), strings.LastIndex(out, "}"); start >= 0 && end > start {
			return out[start : end+1]
		}
		return out
	}

	// Round one: the sweep must serve a fresh question with its key.
	out := run("drill", "api", "next", "--course", "/work/course")
	var next1 struct {
		Action string `json:"action"`
		Item   struct {
			Topic      string   `json:"topic"`
			Question   string   `json:"question"`
			GradingKey string   `json:"grading_key"`
			Points     []string `json:"points"`
			Citation   string   `json:"citation"`
		} `json:"item"`
	}
	if err := json.Unmarshal([]byte(out), &next1); err != nil {
		t.Fatalf("next did not print one JSON object: %q", out)
	}
	if next1.Action != "quiz_new" || next1.Item.Question == "" || next1.Item.GradingKey == "" || len(next1.Item.Points) == 0 {
		t.Fatalf("first round should be quiz_new with key and rubric: %s", out)
	}

	// Answer it confidently wrong, then wait out the 7-minute requeue by
	// draining the sweep — the blindspot must come back as a review.
	run("drill", "api", "record", "--course", "/work/course",
		"--topic", next1.Item.Topic, "--quality", "2", "--confidence", "3",
		"--note", "confident miss")

	out = run("drill", "api", "next", "--course", "/work/course")
	var next2 struct {
		Action string `json:"action"`
		Item   struct {
			Topic string `json:"topic"`
		} `json:"item"`
	}
	if err := json.Unmarshal([]byte(out), &next2); err != nil {
		t.Fatalf("second next: %q", out)
	}
	if next2.Action != "quiz_new" {
		t.Fatalf("sweep should continue with the second question, got %q", next2.Action)
	}
	run("drill", "api", "record", "--course", "/work/course",
		"--topic", next2.Item.Topic, "--quality", "5", "--confidence", "3")

	// Report reflects both attempts, persisted in the container's store.
	out = run("drill", "api", "report", "--course", "/work/course")
	var rep struct {
		Tracked    int `json:"tracked"`
		Blindspots int `json:"blindspots"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("report: %q", out)
	}
	if rep.Tracked != 2 || rep.Blindspots != 1 {
		t.Fatalf("report should show 2 tracked, 1 blindspot: %s", out)
	}

	// Coverage names both units attempted.
	out = run("drill", "api", "coverage", "--course", "/work/course")
	if !strings.Contains(out, `"attempted":2`) {
		t.Fatalf("coverage should show 2 attempted: %s", out)
	}
	t.Log("container drill loop OK")
}

func writeContainerCourse(t *testing.T, dir string) {
	t.Helper()
	manifest := `version: 1
slug: container-fixture
title: Container Fixture
subject: fixture subject
program: the fixture program
persona: a fixture lecturer
assessment: end-of-unit self-check questions
source: .
modules:
  - num: 1
    topic: only module
assessment_markers:
  preset: saq
`
	lesson := `# Fixture SAQ

## Self-Assessment Questions

1. \* What is a widget?
2. \* What is a sprocket?

## Suggested Response - Q1

Response:

- A generic mechanical component
- Used as a placeholder name

## Suggested Response - Q2

Response:

A toothed wheel that engages a chain.
`
	files := map[string]string{
		"course.yaml": manifest,
		"Module_1/Lesson_01_Fixture_SAQ/lesson.md": lesson,
	}
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
