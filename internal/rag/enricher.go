package rag

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

// LLMClient hits an OpenAI-compatible /v1/chat/completions endpoint —
// vibe's proxy when an LLM profile is active, or any compatible
// server. Used for chunk enrichment (MC + SA question generation) and
// equation extraction.
type LLMClient struct {
	BaseURL string
	Model   string
	HTTP    *http.Client
	// Subject is the course/program descriptor woven into the study-aid
	// system prompts so the open pipeline ships no curriculum-specific
	// identity. Defaults to a generic "exam preparation".
	Subject string
}

// NewLLMClient returns a client. baseURL is the proxy root (no /v1
// suffix); model is the alias the server registered (e.g.
// "qwen3.6-27b-mtp-q6_k").
//
// Timeout is 30 minutes because the equation-extraction pass feeds
// the full module corpus (~100k tokens for Module 1) and a single
// inference can run 10-20 minutes on a 27B model. Smaller calls
// finish in seconds — the long timeout is only consumed when an
// equation pass actually needs it.
func NewLLMClient(baseURL, model, subject string) *LLMClient {
	if subject == "" {
		subject = "exam preparation"
	}
	return &LLMClient{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Model:   model,
		Subject: subject,
		HTTP:    &http.Client{Timeout: 30 * time.Minute},
	}
}

// Chat issues a single-turn completion. The system + user pair are
// the standard shape — most prompts here use a structured-output
// system message + the lesson chunk as the user message.
func (c *LLMClient) Chat(ctx context.Context, system, user string, opts ChatOpts) (string, error) {
	if opts.MaxTokens == 0 {
		opts.MaxTokens = 4096
	}
	payload := map[string]any{
		"model": c.Model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"temperature": opts.Temperature,
		"max_tokens":  opts.MaxTokens,
	}
	// Ask the server to constrain decoding to a JSON object when the
	// caller expects strict JSON. OpenAI-compatible servers that don't
	// recognise response_format ignore it, so this is safe to send
	// unconditionally for JSON callers — it tightens output where
	// supported and is a no-op elsewhere (the parse-retry covers the rest).
	if opts.JSONMode {
		payload["response_format"] = map[string]string{"type": "json_object"}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("llm HTTP %d: %s", resp.StatusCode, string(raw))
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("empty chat response")
	}
	return parsed.Choices[0].Message.Content, nil
}

// ChatOpts carries the knobs the enricher and equations passes use.
type ChatOpts struct {
	Temperature float64
	MaxTokens   int
	// JSONMode asks the server to constrain output to a JSON object via
	// response_format. Servers that don't support it ignore the field.
	JSONMode bool
}

// maxParseAttempts bounds how many times a structured-output LLM call
// is retried when the response fails to parse. The model is
// nondeterministic, so a re-roll often fixes a one-off malformed
// response; an unbounded loop would hang on a systematically broken
// prompt/server, so cap it and surface the final error.
const maxParseAttempts = 3

// enrichSystemPrompt is the system message for the per-chunk
// enrichment pass. Asks for strict JSON output so the response
// round-trips through json.Unmarshal without parsing tricks. The
// subject descriptor is injected at run time so the open pipeline
// ships no curriculum-specific identity.
func enrichSystemPrompt(subject string) string {
	return "You are a study-aid generator for " + subject + ".\n\n" + `Given one chunk of a lesson's content, produce structured study
material in strict JSON matching the schema below. No prose
preamble, no markdown fences — first byte is '{'.

Schema:
{
  "summary": "<one sentence summarising the chunk's main idea>",
  "key_terms": ["<term1>", "<term2>", ...],   // 3 to 5 entries
  "multiple_choice": [
    {
      "question": "<exam-quality question about the chunk>",
      "options": ["<A>", "<B>", "<C>", "<D>"],
      "correct_index": <0|1|2|3>,
      "explanation": "<one sentence on why the correct option is correct>"
    },
    ... 2 entries total
  ],
  "short_answer": [
    {
      "question": "<exam-quality question requiring 1-2 sentence answer>",
      "answer": "<the canonical 1-2 sentence answer, grounded in the chunk>"
    },
    ... 2 entries total
  ]
}

Quality rules:
- Questions must be answerable strictly from the chunk's content.
  Don't ask about things the chunk implies but doesn't state.
- Multiple-choice distractors should be plausible — not absurd. The
  goal is to catch a student who half-learned the material.
- key_terms are the named entities a student should be able to
  define on sight (enzymes, processes, equipment, units of measure).
- The summary is the one-liner a flashcard would use as a hint.

Avoid generic test-prep filler ("which of the following is true?"
without specifics) and avoid restating the chunk verbatim in the
answer.`
}

// EnrichChunk runs the enrichment LLM against one prose chunk and
// unmarshals the strict-JSON response. Non-prose chunks (definitions,
// key numbers, processes) skip enrichment — they're already short
// enough that the chunk itself is the study aid.
func EnrichChunk(ctx context.Context, c *LLMClient, chunk *Chunk) error {
	if chunk.Type != ChunkTypeProse {
		return nil
	}
	userMsg := fmt.Sprintf(
		"Lesson: %s\n\nChunk:\n%s",
		chunk.LessonTitle, chunk.Text,
	)
	var lastErr error
	for attempt := 1; attempt <= maxParseAttempts; attempt++ {
		raw, err := c.Chat(ctx, enrichSystemPrompt(c.Subject), userMsg, ChatOpts{
			Temperature: 0.3,
			MaxTokens:   8192,
			JSONMode:    true,
		})
		if err != nil {
			// A transport/HTTP error won't be fixed by re-rolling the
			// same request, so fail fast rather than burning attempts.
			return fmt.Errorf("chat: %w", err)
		}
		enr, perr := parseEnrichment(raw)
		if perr == nil {
			chunk.Enrichment = enr
			return nil
		}
		lastErr = perr
	}
	return fmt.Errorf("enrichment failed after %d attempts: %w", maxParseAttempts, lastErr)
}

// parseEnrichment strips any markdown fences the model adds and
// unmarshals the strict-JSON enrichment payload. Split out so the
// fence-tolerance and parse contract are unit-testable without a live
// LLM.
func parseEnrichment(raw string) (*Enrichment, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)
	var enr Enrichment
	if err := json.Unmarshal([]byte(raw), &enr); err != nil {
		return nil, fmt.Errorf("unmarshal enrichment (raw: %.200s): %w", raw, err)
	}
	return &enr, nil
}

// EnrichChunks runs enrichment over every prose chunk. Sequential
// for the same reason EmbedChunks is — single-stream LLM calls
// avoid VRAM contention, and on a 27B model the per-chunk latency
// (~30s) makes pipelining gains marginal.
//
// Already-enriched chunks (Enrichment != nil) are skipped — this
// is the resume path. The progress callback counts completed
// chunks INCLUDING the ones carried over from checkpoint so the
// progress display matches the user's mental model.
func EnrichChunks(ctx context.Context, c *LLMClient, chunks []Chunk, onProgress func(done, total int)) error {
	total := 0
	for i := range chunks {
		if chunks[i].Type == ChunkTypeProse {
			total++
		}
	}
	done := 0
	for i := range chunks {
		if chunks[i].Type != ChunkTypeProse {
			continue
		}
		if chunks[i].Enrichment != nil {
			done++
			continue
		}
		if err := EnrichChunk(ctx, c, &chunks[i]); err != nil {
			return fmt.Errorf("enrich chunk %s: %w", chunks[i].ID, err)
		}
		done++
		if onProgress != nil {
			onProgress(done, total)
		}
	}
	return nil
}
