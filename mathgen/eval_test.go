package mathgen

import (
	"math"
	"testing"
)

func TestEval(t *testing.T) {
	env := map[string]float64{"a": 2, "b": 3, "c": 10, "V_NaOH": 12.5, "N": 0.1, "V_sample": 25}
	cases := []struct {
		expr string
		want float64
	}{
		{"1 + 2 * 3", 7},
		{"(1 + 2) * 3", 9},
		{"2 ^ 3 ^ 2", 512}, // right-assoc: 2^(3^2)=2^9
		{"-2 ^ 2", -4},     // unary binds looser than ^: -(2^2)
		{"(-2) ^ 2", 4},
		{"10 / 4", 2.5},
		{"a * b + c", 16},
		{"a + b * c", 32},
		{"sqrt(c * 1.6 + 0.04)", 4.004996878900157}, // sqrt(16.04)
		{"(V_NaOH * N * 7.5) / V_sample", 0.375},    // titratable acidity
		{"abs(-5) + min(3, 8) + max(3, 8)", 16},
		{"pow(2, 10)", 1024},
		{"100 * (1 - exp(0))", 0},
		{"1e3 + 2.5e-1", 1000.25},
		{"pi > 0", 0}, // '>' is unknown op -> error path tested below; placeholder won't run
	}
	for _, tc := range cases {
		if tc.expr == "pi > 0" {
			continue
		}
		got, err := Eval(tc.expr, env)
		if err != nil {
			t.Errorf("Eval(%q) error: %v", tc.expr, err)
			continue
		}
		if math.Abs(got-tc.want) > 1e-6 {
			t.Errorf("Eval(%q) = %v, want %v", tc.expr, got, tc.want)
		}
	}
}

func TestEvalErrors(t *testing.T) {
	cases := []string{
		"1 +",           // trailing operator
		"a + ",          // missing operand
		"undefined_var", // undefined
		"sqrt(",         // unclosed
		"foo(1)",        // unknown function
		"pow(1)",        // wrong arity
		"1 / 0",         // div by zero
		"sqrt(-1)",      // NaN result
		"1 ) 2",         // garbage
		"1 > 2",         // unknown operator
	}
	for _, expr := range cases {
		if _, err := Eval(expr, map[string]float64{"a": 1}); err == nil {
			t.Errorf("Eval(%q) expected error, got nil", expr)
		}
	}
}

func TestVariables(t *testing.T) {
	vars, err := Variables("(V_NaOH * N * 7.5) / V_sample + pi")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"V_NaOH": true, "N": true, "V_sample": true}
	if len(vars) != len(want) {
		t.Fatalf("Variables = %v, want keys %v (pi is a constant, excluded)", vars, want)
	}
	for _, v := range vars {
		if !want[v] {
			t.Errorf("unexpected variable %q (pi should be excluded as a constant)", v)
		}
	}
}
