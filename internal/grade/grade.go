// Package grade owns the one place a model, not code, decides a fact
// about the learner: grading judgement. Questions, answers, arithmetic,
// and scheduling are owned by code; the grade is not. So the grader is
// a small, swappable interface behind an explicit contract — grade ONLY
// against the official answer and its rubric points, never from the
// model's own knowledge — and `etude eval grading` qualifies any
// candidate grader against golden answer/grade pairs before it is
// trusted with weeks of study.
//
// The shipped implementation talks to any OpenAI-compatible chat
// endpoint (a local router or an external provider); no vibe daemon is
// required for the text legs.
package grade

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Verdict is one graded answer.
type Verdict struct {
	// Quality is recall quality 0-5 vs the official answer (4+ counts
	// as a correct retrieval): 5 every point clear and correct · 4 all
	// key points, minor omission · 3 partial, missed a substantive
	// point · 2 major gaps · 1 mostly wrong · 0 blank.
	Quality int `json:"quality"`
	// Hits and Misses are the rubric points the learner did and did not
	// state, verbatim from the rubric where possible.
	Hits   []string `json:"hits"`
	Misses []string `json:"misses"`
	// Explanation is one or two sentences on the gap, for feedback.
	Explanation string `json:"explanation"`
}

// Grader grades a learner's recall against the official answer.
type Grader interface {
	Grade(ctx context.Context, req Request) (Verdict, error)
}

// Request is one grading job.
type Request struct {
	Question   string   // the prompt as posed
	Answer     string   // the official model answer
	Points     []string // the rubric decomposition of the answer
	Learner    string   // the learner's answer
	Difficulty string   // short | progressive | long, for structure expectations
}

// ChatClient grades via any OpenAI-compatible /chat/completions
// endpoint.
type ChatClient struct {
	BaseURL string // e.g. "http://localhost:8080/v1" — no trailing slash needed
	APIKey  string // optional; sent as a bearer token when set
	Model   string
	// HTTPClient is overridable for tests; nil uses a sane default.
	HTTPClient *http.Client
}

func (c *ChatClient) http() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 120 * time.Second}
}

// systemPrompt is the grading contract. It is deliberately strict about
// the knowledge boundary: the official answer is the only source.
const systemPrompt = `You are an examiner grading a learner's recalled answer against the official answer key. The key is your ONLY source of truth: your own knowledge of the subject is not a source. If the key doesn't mention something, it is not required; if the learner states something that contradicts the key, it is wrong.

Grade structurally, never holistically:
1. Check the learner's answer against each required point from the rubric: hit / missed. Credit a point made in different words; do not credit a point that isn't actually there. A point stated WRONG (not just omitted) caps the score regardless of fluency.
2. Derive the quality score from coverage, not impression:
   5 = every point clear and correct
   4 = all key points, minor omission or loose wording
   3 = partial: missed a substantive point, or needed a nudge
   2 = major gaps
   1 = mostly wrong
   0 = blank or unrelated
3. Report which rubric points were hit and which were missed, quoting the rubric point text.

Respond with ONLY a JSON object, no markdown fencing:
{"quality": <0-5>, "hits": ["point", ...], "misses": ["point", ...], "explanation": "<one or two sentences>"}`

// Grade implements Grader.
func (c *ChatClient) Grade(ctx context.Context, req Request) (Verdict, error) {
	var user strings.Builder
	user.WriteString("QUESTION:\n" + req.Question + "\n\nOFFICIAL ANSWER KEY (private):\n" + req.Answer + "\n")
	if len(req.Points) > 0 {
		user.WriteString("\nREQUIRED POINTS:\n")
		for _, p := range req.Points {
			user.WriteString("- " + p + "\n")
		}
	}
	if req.Difficulty == "long" {
		user.WriteString("\nThis is a long/integrative question: grade on completeness and structure, not just whether the key fact appears.\n")
	}
	user.WriteString("\nLEARNER'S ANSWER:\n" + req.Learner + "\n")

	body, err := json.Marshal(map[string]any{
		"model": c.Model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": user.String()},
		},
		"temperature": 0,
	})
	if err != nil {
		return Verdict{}, err
	}

	url := strings.TrimSuffix(c.BaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Verdict{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := c.http().Do(httpReq)
	if err != nil {
		return Verdict{}, fmt.Errorf("grader request: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return Verdict{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return Verdict{}, fmt.Errorf("grader endpoint %s: %s: %.200s", url, resp.Status, raw)
	}

	var chat struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &chat); err != nil {
		return Verdict{}, fmt.Errorf("grader response: %w", err)
	}
	if len(chat.Choices) == 0 {
		return Verdict{}, fmt.Errorf("grader endpoint returned no choices")
	}
	return ParseVerdict(chat.Choices[0].Message.Content)
}

// ParseVerdict tolerantly decodes the grader's JSON: models wrap output
// in prose or fencing despite instructions, so the first {...} block
// wins. Quality is clamped to 0-5.
func ParseVerdict(content string) (Verdict, error) {
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end <= start {
		return Verdict{}, fmt.Errorf("grader returned no JSON object: %.200s", content)
	}
	var v Verdict
	if err := json.Unmarshal([]byte(content[start:end+1]), &v); err != nil {
		return Verdict{}, fmt.Errorf("grader JSON: %w", err)
	}
	v.Quality = max(0, min(5, v.Quality))
	return v, nil
}
