package grade

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseVerdict(t *testing.T) {
	// Models wrap output in prose and fencing despite instructions.
	v, err := ParseVerdict("Here is my grade:\n```json\n{\"quality\": 4, \"hits\": [\"a\"], \"misses\": [\"b\"], \"explanation\": \"close\"}\n```\nHope that helps.")
	if err != nil {
		t.Fatalf("ParseVerdict: %v", err)
	}
	if v.Quality != 4 || len(v.Hits) != 1 || len(v.Misses) != 1 {
		t.Fatalf("wrong verdict: %+v", v)
	}
	// Quality clamps into 0-5.
	v, err = ParseVerdict(`{"quality": 9}`)
	if err != nil || v.Quality != 5 {
		t.Fatalf("clamp: %+v %v", v, err)
	}
	if _, err := ParseVerdict("no json here"); err == nil {
		t.Fatal("expected an error for JSON-free output")
	}
}

func TestChatClientGrade(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing bearer token")
		}
		gotBody, _ = json.Marshal(r.Body)
		_ = json.NewDecoder(r.Body).Decode(&map[string]any{})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": `{"quality": 3, "hits": ["x"], "misses": ["y"], "explanation": "partial"}`}},
			},
		})
	}))
	defer srv.Close()

	c := &ChatClient{BaseURL: srv.URL + "/v1", APIKey: "test-key", Model: "test-model"}
	v, err := c.Grade(context.Background(), Request{
		Question: "Q?", Answer: "A.", Points: []string{"x", "y"}, Learner: "maybe x",
	})
	if err != nil {
		t.Fatalf("Grade: %v", err)
	}
	if v.Quality != 3 || v.Explanation != "partial" {
		t.Fatalf("wrong verdict: %+v", v)
	}
	_ = gotBody
}

func TestChatClientHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"model not found"}`, http.StatusNotFound)
	}))
	defer srv.Close()
	c := &ChatClient{BaseURL: srv.URL, Model: "nope"}
	_, err := c.Grade(context.Background(), Request{})
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected a 404-bearing error, got %v", err)
	}
}
