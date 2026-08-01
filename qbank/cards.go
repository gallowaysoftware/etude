package qbank

import (
	"encoding/json"
	"fmt"
	"os"
)

// cardJSON is the on-disk shape of a number cloze card: a
// fill-in-the-blank generated from the corpus's own answer values,
// drilled as a focus mode beside the question bank.
type cardJSON struct {
	ID       string `json:"id"`
	Module   string `json:"module"`
	Unit     string `json:"unit"`
	Prompt   string `json:"prompt"`
	Answer   string `json:"answer"`
	Citation string `json:"citation"`
}

// LoadCards reads a JSON array of cloze cards into a Bank, tagged
// Difficulty "number". A missing file yields an empty bank (not an
// error), so a numbers drill simply stays empty when no deck is
// deployed. Card IDs are authored in the deck file and are the course
// owner's stability contract, so no hashing applies.
func LoadCards(path string) (*Bank, error) {
	b := &Bank{byID: map[string]*Question{}, byModule: map[string][]*Question{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return b, nil
		}
		return nil, fmt.Errorf("read cards %s: %w", path, err)
	}
	var cards []cardJSON
	if err := json.Unmarshal(data, &cards); err != nil {
		return nil, fmt.Errorf("parse cards %s: %w", path, err)
	}
	for i, c := range cards {
		b.add(&Question{
			ID: c.ID, Module: c.Module, Unit: c.Unit, UnitTopic: c.Unit, Num: i + 1,
			Difficulty: "number", Prompt: c.Prompt, Answer: c.Answer, Citation: c.Citation,
		})
	}
	return b, nil
}
