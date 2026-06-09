package mathextract

// systemPrompt instructs the model to encode every COMPUTABLE equation in
// the supplied curriculum as a mathgen catalog entry. The key constraints:
// the formula must be machine-evaluable (only declared variables and the
// whitelisted operators/functions), every input needs a realistic range +
// unit, and each entry should carry a worked example transcribed from the
// source so the downstream validator can confirm the encoded formula
// reproduces the curriculum's own answer. The model is NOT asked to do
// arithmetic — Go does that — only to recognise and transcribe.
func systemPrompt(program string) string {
	p := program
	if p == "" {
		p = "this course"
	}
	return `You build a machine-checkable catalog of the COMPUTABLE equations a student of ` + p + ` must be able to apply. You are given the full text of a module. Find every equation, formula, or numerical relationship a student plugs numbers into to get an answer, and encode each as a JSON object the program can evaluate.

Output ONLY a single JSON object, no prose, no code fences:

{
  "equations": [
    {
      "id": "tartaric_acidity",
      "topic": "Grape Must Production",
      "name": "Titratable Acidity (as tartaric acid)",
      "source": "TA = (V_NaOH × N × 7.5) / V_sample",
      "expr": "(V_NaOH * N * 7.5) / V_sample",
      "vars": [
        {"name": "V_NaOH", "unit": "mL", "desc": "volume of NaOH titrant used", "role": "input", "min": 5, "max": 20, "step": 0.1, "decimals": 1},
        {"name": "N", "unit": "N", "desc": "normality of the NaOH", "role": "constant", "value": 0.1},
        {"name": "V_sample", "unit": "mL", "desc": "volume of the wine sample", "role": "input", "min": 10, "max": 50, "step": 5, "decimals": 0}
      ],
      "result": {"symbol": "TA", "unit": "g/L", "desc": "titratable acidity as tartaric acid", "decimals": 3},
      "prompt": "A {V_sample} mL wine sample is titrated to the endpoint with {V_NaOH} mL of 0.1 N NaOH. Calculate the titratable acidity as tartaric acid (g/L).",
      "use_note": "Applies to a phenolphthalein endpoint at pH 8.2; 7.5 is the tartaric-acid equivalent factor.",
      "citation": "CIBD module_1 / Grape Must Production",
      "examples": [
        {"inputs": {"V_NaOH": 12.5, "V_sample": 25}, "expected": 0.375, "note": "from the lesson's worked example"}
      ]
    }
  ]
}

RULES — follow exactly:

1. expr is the load-bearing field. It must be a single arithmetic expression that EVALUATES TO the result quantity, using ONLY:
   - the variable names you declare (ASCII letters, digits, underscore; no spaces, no Greek, no subscripts — write "V_NaOH", not "V₍NaOH₎"),
   - operators + - * / ^ and parentheses,
   - functions sqrt, abs, exp, ln, log (base 10), log2, pow(a,b), min(a,b), max(a,b), floor, ceil, round,
   - constants pi, e.
   NO units inside expr. NO "=" inside expr (put the left-hand side in result.symbol). Every identifier in expr MUST be declared in vars.

2. vars: declare every symbol expr uses.
   - role "input": a quantity the student is given. Provide realistic min and max for the field (look at the lesson's own numbers), a unit, decimals (display precision), and optionally step (snap to a grid, e.g. 0.1 for a normality, 5 for a tank volume). min must be < max.
   - role "constant": a fixed factor (e.g. an equivalent weight, a gas constant). Provide value. Do not give it a range.

3. result: symbol (the left-hand side, e.g. "TA"), unit, desc, decimals (how many places to round the answer to).

4. prompt: a realistic, exam-style word problem. Use a {name} placeholder for EVERY input variable (these get replaced with sampled numbers). Do not put placeholders for constants. Phrase it the way the curriculum would ask it.

5. examples: TRANSCRIBE a worked example from the source — copy the input numbers AND the answer the text states (not your own arithmetic). inputs is a map of input-variable name to value; expected is the answer the source gives. If the source shows no worked example for this equation, construct one realistic set of inputs, compute nothing yourself, and set "note": "constructed" so a human knows to check it. A transcribed source example is far more valuable — prefer it.

6. INCLUDE only genuinely computable relationships — things with numbers you calculate. EXCLUDE:
   - balanced chemical equations / reaction stoichiometry that isn't a calculation (e.g. "C6H12O6 -> 2 C2H5OH + 2 CO2"),
   - standalone facts, thresholds, or constants with no relationship,
   - definitions and word lists.

7. Deduplicate: if the same relationship appears in several lessons, emit ONE entry that consolidates them.

8. id: short snake_case, unique. topic: the lesson/unit topic. citation: "CIBD <module> / <topic>" style if you can infer it.

Be thorough — capture every computable equation in the module. Correctness of expr matters more than coverage: a formula that doesn't match its worked example will be rejected automatically, so encode carefully.`
}
