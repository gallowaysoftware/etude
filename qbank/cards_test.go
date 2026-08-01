package qbank

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCards(t *testing.T) {
	// A missing file is an empty bank, not an error.
	b, err := LoadCards(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil || b.Len() != 0 {
		t.Fatalf("missing file should give an empty bank: %v, %d", err, b.Len())
	}

	path := filepath.Join(t.TempDir(), "cards.json")
	deck := `[
	  {"id":"module_2.num.0001","module":"module_2","unit":"Numbers","prompt":"A widget spins at ___ rpm.","answer":"4200","citation":"module_2 / Numbers › 1"},
	  {"id":"module_2.num.0002","module":"module_2","unit":"Numbers","prompt":"A sprocket has ___ teeth.","answer":"12","citation":"module_2 / Numbers › 2"}
	]`
	if err := os.WriteFile(path, []byte(deck), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err = LoadCards(path)
	if err != nil {
		t.Fatalf("LoadCards: %v", err)
	}
	if b.Len() != 2 {
		t.Fatalf("expected 2 cards, got %d", b.Len())
	}
	q := b.Get("module_2.num.0001")
	if q == nil || q.Difficulty != "number" || q.Answer != "4200" {
		t.Fatalf("card wrong: %+v", q)
	}
	if got := len(b.ForModule("module_2")); got != 2 {
		t.Fatalf("ForModule: %d", got)
	}
}
