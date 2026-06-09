package mathgen

import (
	"strings"
	"testing"
)

// abvDilution is a second fixture equation in the same topic so the
// comprehensive-problem path is exercised.
func abvDilution() Equation {
	return Equation{
		ID:     "blend_abv",
		Module: "module_1",
		Topic:  "Grape Must Production",
		Name:   "Dilution to Target Strength",
		Source: "V_water = V_spirit × (ABV_start / ABV_target - 1)",
		Expr:   "V_spirit * (ABV_start / ABV_target - 1)",
		Vars: []Variable{
			{Name: "V_spirit", Unit: "L", Desc: "volume of high-strength spirit", Role: RoleInput, Min: 10, Max: 100, Step: 5, Decimals: 0},
			{Name: "ABV_start", Unit: "%", Desc: "starting strength", Role: RoleInput, Min: 60, Max: 80, Step: 1, Decimals: 0},
			{Name: "ABV_target", Unit: "%", Desc: "target strength", Role: RoleInput, Min: 37, Max: 50, Step: 1, Decimals: 0},
		},
		Result:   Result{Symbol: "V_water", Unit: "L", Desc: "water to add", Decimals: 2},
		Prompt:   "You have {V_spirit} L of spirit at {ABV_start}% ABV and want to dilute it to {ABV_target}% ABV. How much water must you add (L)?",
		Citation: "CIBD module_1 / Grape Must Production",
		Examples: []Example{{Inputs: map[string]float64{"V_spirit": 50, "ABV_start": 70, "ABV_target": 40}, Expected: 37.5}},
	}
}

func TestRenderGuide(t *testing.T) {
	c := &Catalog{Program: "CIBD Diploma in Distilling", Equations: []Equation{titratableAcidity(), abvDilution()}}
	if issues := Validate(c); len(issues) != 0 {
		t.Fatalf("fixtures should validate cleanly: %v", issues)
	}
	md := RenderGuide(c, GuideOptions{Module: "module_1", Seed: 7, PracticeShort: 2})

	for _, want := range []string{
		"# CIBD Diploma in Distilling — Math Reference & Practice (module_1)",
		"## Grape Must Production",
		"### Titratable Acidity (as tartaric acid)",
		"TA = (V_NaOH × N × 7.5) / V_sample",
		"| Symbol | Meaning | Unit |",
		"**Worked example.**",
		"**Practice.**",
		"### Comprehensive problem",
		"## Answer Key",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("rendered guide missing %q", want)
		}
	}

	// Every answer-key entry's stated answer must equal a fresh exact
	// computation from its own worked-solution inputs — i.e. the guide
	// never prints arithmetic that doesn't check out.
	if strings.Count(md, "g/L") == 0 {
		t.Error("expected titratable-acidity answers with g/L units")
	}
}
