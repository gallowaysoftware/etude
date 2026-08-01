package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gallowaysoftware/etude/coach"
	"github.com/gallowaysoftware/etude/course"
	"github.com/gallowaysoftware/etude/grade"
	"github.com/gallowaysoftware/etude/qbank"
	"github.com/gallowaysoftware/etude/study"
)

// drillDeps bundles everything a drill frontend needs. Close releases
// the store's single-writer lock.
type drillDeps struct {
	Manifest *course.Manifest
	Bank     *qbank.Bank
	Coach    *coach.Coach
}

func (d *drillDeps) Close() error {
	if d.Coach == nil || d.Coach.Store == nil {
		return nil
	}
	return d.Coach.Store.Close()
}

// loadCoach resolves a course into a ready coach: manifest → extracted
// bank → locked study store with alias migration applied. The store
// lives at <course>/.etude/study.json — greppable JSON that backs up
// with the course.
func loadCoach(coursePath string) (*drillDeps, error) {
	m, err := course.Load(coursePath)
	if err != nil {
		return nil, err
	}
	var b *qbank.Bank
	if m.HasAssessmentMaterial() {
		b, err = qbank.Extract(m)
		if err != nil {
			return nil, fmt.Errorf("extract question bank: %w", err)
		}
		if b.Len() == 0 {
			fmt.Fprintf(os.Stderr, "etude: warning: assessment_markers matched no questions under %s\n", m.SourceDir())
		}
	} else {
		fmt.Fprintln(os.Stderr, "etude: course has no assessment_markers; bank is empty (generated questions are a later phase)")
	}
	s, err := study.NewStore(filepath.Join(m.Dir(), ".etude", "study.json"))
	if err != nil {
		return nil, err
	}
	c := &coach.Coach{Store: s, Bank: b}
	if err := c.MigrateAliases(); err != nil {
		s.Close()
		return nil, fmt.Errorf("migrate question IDs: %w", err)
	}
	return &drillDeps{Manifest: m, Bank: b, Coach: c}, nil
}

// endpointConfig resolves the OpenAI-compatible endpoint for the text
// legs. Precedence: flag > ETUDE_* env. Endpoints are machine-scoped by
// design (docs/course-yaml.md), so the manifest never carries them.
type endpointConfig struct {
	URL    string
	APIKey string
	Model  string
}

func resolveEndpoint(flagURL, flagKey, flagModel string) endpointConfig {
	return endpointConfig{
		URL:    firstNonEmpty(flagURL, os.Getenv("ETUDE_LLM_URL")),
		APIKey: firstNonEmpty(flagKey, os.Getenv("ETUDE_LLM_API_KEY")),
		Model:  firstNonEmpty(flagModel, os.Getenv("ETUDE_LLM_MODEL")),
	}
}

// grader builds the chat grader, or errors with setup guidance when no
// endpoint is configured. ETUDE_LLM_EXTRA_BODY (a JSON object) merges
// endpoint-specific request fields — e.g.
// {"chat_template_kwargs":{"enable_thinking":false}} to bound a
// reasoning model's thinking time.
func (e endpointConfig) grader() (*grade.ChatClient, error) {
	if e.URL == "" {
		return nil, fmt.Errorf("no LLM endpoint configured: pass --llm-url or set ETUDE_LLM_URL (any OpenAI-compatible endpoint, local router or external provider)")
	}
	if e.Model == "" {
		return nil, fmt.Errorf("no grader model configured: pass --llm-model or set ETUDE_LLM_MODEL")
	}
	c := &grade.ChatClient{BaseURL: e.URL, APIKey: e.APIKey, Model: e.Model}
	if raw := os.Getenv("ETUDE_LLM_EXTRA_BODY"); raw != "" {
		var extra map[string]any
		if err := json.Unmarshal([]byte(raw), &extra); err != nil {
			return nil, fmt.Errorf("ETUDE_LLM_EXTRA_BODY is not a JSON object: %w", err)
		}
		c.ExtraBody = extra
	}
	return c, nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
