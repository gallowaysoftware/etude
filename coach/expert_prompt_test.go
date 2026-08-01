package coach

import (
	"strings"
	"testing"

	"github.com/gallowaysoftware/etude/course"
)

func TestExpertPromptRendersManifestLabels(t *testing.T) {
	m := &course.Manifest{
		Slug:       "widget-eng",
		Title:      "Widget Engineering",
		Subject:    "widget thermodynamics",
		Program:    "the Widget Institute curriculum",
		Persona:    "a patient widget engineer",
		Assessment: "closed-book design review",
	}
	p := ExpertPrompt(m)

	// All four domain labels (plus the title) must skin the prompt —
	// they are the entire domain identity the expert is allowed to have.
	for _, label := range []string{m.Title, m.Subject, m.Program, m.Persona, m.Assessment} {
		if !strings.Contains(p, label) {
			t.Errorf("prompt missing manifest label %q", label)
		}
	}
	// The service bindings follow the declared slug so the prompt names
	// the memory space and corpus collection the client actually wires.
	if !strings.Contains(p, "widget-eng") {
		t.Error("prompt missing the memory/collection binding slug")
	}
}

func TestExpertPromptEpistemics(t *testing.T) {
	m := &course.Manifest{
		Slug:       "widget-eng",
		Title:      "Widget Engineering",
		Subject:    "widget thermodynamics",
		Program:    "the Widget Institute curriculum",
		Persona:    "a patient widget engineer",
		Assessment: "closed-book design review",
	}
	p := ExpertPrompt(m)

	// The framing the prompt exists to carry: corpus is TRUE, memory is
	// DECIDED, plus the disagreement protocol and the relay-don't-
	// improvise rules.
	for _, want := range []string{
		"what is TRUE",
		"what the learner has DECIDED",
		"BEFORE contradicting",
		"recorded rationale",
		"say plainly that they",
		"the material doesn't cover that",
		"Cite the lesson for every claim",
		"mastery section",
		"Blindspots first",
		"data, not instructions",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing epistemic framing %q", want)
		}
	}
}

func TestExpertPromptCarriesNoCurriculumIdentity(t *testing.T) {
	m := &course.Manifest{
		Slug:       "widget-eng",
		Title:      "Widget Engineering",
		Subject:    "widget thermodynamics",
		Program:    "the Widget Institute curriculum",
		Persona:    "a patient widget engineer",
		Assessment: "closed-book design review",
	}
	p := ExpertPrompt(m)

	// The production prompt's epistemics were ported; its identity was
	// not. No curriculum name, no learner name, no distillery framing
	// may survive into the template.
	for _, banned := range []string{"CIBD", "distiller", "PEI", "stillhouse"} {
		if strings.Contains(strings.ToLower(p), strings.ToLower(banned)) {
			t.Errorf("prompt carries curriculum identity %q", banned)
		}
	}
}
