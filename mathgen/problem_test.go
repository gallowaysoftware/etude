package mathgen

import (
	"math"
	"math/rand"
	"strings"
	"testing"
)

// titratableAcidity is a real CIBD equation used as the test fixture.
func titratableAcidity() Equation {
	return Equation{
		ID:     "tartaric_acidity",
		Module: "module_1",
		Topic:  "Grape Must Production",
		Name:   "Titratable Acidity (as tartaric acid)",
		Source: "TA = (V_NaOH × N × 7.5) / V_sample",
		Expr:   "(V_NaOH * N * 7.5) / V_sample",
		Vars: []Variable{
			{Name: "V_NaOH", Unit: "mL", Desc: "volume of NaOH titrant", Role: RoleInput, Min: 5, Max: 20, Step: 0.1, Decimals: 1},
			{Name: "N", Unit: "N", Desc: "normality of the NaOH", Role: RoleConstant, Value: 0.1},
			{Name: "V_sample", Unit: "mL", Desc: "volume of the wine sample", Role: RoleInput, Min: 10, Max: 50, Step: 5, Decimals: 0},
		},
		Result:   Result{Symbol: "TA", Unit: "g/L", Desc: "titratable acidity as tartaric acid", Decimals: 3},
		Prompt:   "A {V_sample} mL wine sample is titrated to the endpoint with {V_NaOH} mL of 0.1 N NaOH. Calculate the titratable acidity as tartaric acid (g/L).",
		Citation: "CIBD module_1 / Grape Must Production",
		Examples: []Example{{Inputs: map[string]float64{"V_NaOH": 12.5, "V_sample": 25}, Expected: 0.375}},
	}
}

func TestValidateGood(t *testing.T) {
	c := &Catalog{Equations: []Equation{titratableAcidity()}}
	if issues := Validate(c); len(issues) != 0 {
		t.Fatalf("expected no issues, got: %v", issues)
	}
}

func TestValidateCatchesWrongFormula(t *testing.T) {
	bad := titratableAcidity()
	bad.ID = "tartaric_acidity_wrong"
	bad.Expr = "(V_NaOH * N * 5.0) / V_sample" // wrong constant: 5.0 not 7.5
	c := &Catalog{Equations: []Equation{titratableAcidity(), bad}}
	issues := Validate(c)
	if len(issues) == 0 {
		t.Fatal("expected the wrong formula to fail its source example, got no issues")
	}
	// Filter must keep the good one and drop the wrong one.
	clean, _ := c.Filter()
	if len(clean.Equations) != 1 || clean.Equations[0].ID != "tartaric_acidity" {
		t.Fatalf("Filter should keep only the correct equation, got %d: %v", len(clean.Equations), clean.Equations)
	}
}

func TestValidateCatchesUndeclaredAndBadRange(t *testing.T) {
	c := &Catalog{Equations: []Equation{{
		ID:   "broken",
		Name: "Broken",
		Expr: "x * y",                                                   // y undeclared
		Vars: []Variable{{Name: "x", Role: RoleInput, Min: 10, Max: 5}}, // max < min
	}}}
	issues := Validate(c)
	var sawUndeclared, sawRange bool
	for _, is := range issues {
		if strings.Contains(is.Reason, "undeclared variable") {
			sawUndeclared = true
		}
		if strings.Contains(is.Reason, "max") && strings.Contains(is.Reason, "min") {
			sawRange = true
		}
	}
	if !sawUndeclared || !sawRange {
		t.Fatalf("expected undeclared-var and bad-range issues, got: %v", issues)
	}
}

func TestSolveCorrectArithmetic(t *testing.T) {
	e := titratableAcidity()
	p, err := e.Solve(map[string]float64{"V_NaOH": 12.5, "V_sample": 25})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(p.Answer-0.375) > 1e-9 {
		t.Fatalf("answer = %v, want 0.375", p.Answer)
	}
	if p.AnswerText != "0.375 g/L" {
		t.Errorf("answer text = %q, want %q", p.AnswerText, "0.375 g/L")
	}
	// Worked steps: formula, substitution (values, × not *), result.
	joined := strings.Join(p.Steps, "\n")
	if !strings.Contains(joined, "TA = (V_NaOH × N × 7.5) / V_sample") {
		t.Errorf("missing formula line:\n%s", joined)
	}
	if !strings.Contains(joined, "12.5") || !strings.Contains(joined, "×") {
		t.Errorf("substitution line should show values and ×:\n%s", joined)
	}
	if strings.Contains(p.Steps[1], "V_NaOH") {
		t.Errorf("substitution line should have replaced variable names with values:\n%s", p.Steps[1])
	}
	// Question renders placeholders.
	if !strings.Contains(p.Question, "12.5") || !strings.Contains(p.Question, "25") {
		t.Errorf("question should substitute sampled values: %q", p.Question)
	}
}

func TestSampleRespectsRangesAndDeterminism(t *testing.T) {
	e := titratableAcidity()
	r1 := rand.New(rand.NewSource(42))
	r2 := rand.New(rand.NewSource(42))
	for range 200 {
		in := e.Sample(r1)
		if in["V_NaOH"] < 5 || in["V_NaOH"] > 20 {
			t.Fatalf("V_NaOH %v out of [5,20]", in["V_NaOH"])
		}
		if in["V_sample"] < 10 || in["V_sample"] > 50 {
			t.Fatalf("V_sample %v out of [10,50]", in["V_sample"])
		}
		// V_sample quantised to step 5.
		if math.Mod(in["V_sample"], 5) != 0 {
			t.Fatalf("V_sample %v not on the 5-step grid", in["V_sample"])
		}
	}
	// Same seed -> same stream (guide reproducibility).
	if e.Sample(r2)["V_NaOH"] == 0 {
		t.Skip("smoke")
	}
}
