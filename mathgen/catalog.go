package mathgen

import (
	"fmt"
	"maps"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Role classifies a variable in an equation.
type Role string

const (
	RoleInput    Role = "input"    // sampled within [Min, Max]
	RoleConstant Role = "constant" // fixed at Value
)

// Variable is one symbol an equation uses.
type Variable struct {
	Name     string  `yaml:"name" json:"name"`                             // identifier as it appears in Expr, e.g. "V_NaOH"
	Symbol   string  `yaml:"symbol,omitempty" json:"symbol,omitempty"`     // display form (defaults to Name)
	Unit     string  `yaml:"unit,omitempty" json:"unit,omitempty"`         // "mL", "g/L", "" for dimensionless
	Desc     string  `yaml:"desc,omitempty" json:"desc,omitempty"`         // human description
	Role     Role    `yaml:"role,omitempty" json:"role,omitempty"`         // input (default) or constant
	Min      float64 `yaml:"min,omitempty" json:"min,omitempty"`           // sampling lower bound (input)
	Max      float64 `yaml:"max,omitempty" json:"max,omitempty"`           // sampling upper bound (input)
	Step     float64 `yaml:"step,omitempty" json:"step,omitempty"`         // quantize samples to this grid (0 = continuous)
	Value    float64 `yaml:"value,omitempty" json:"value,omitempty"`       // fixed value (constant)
	Decimals int     `yaml:"decimals,omitempty" json:"decimals,omitempty"` // display precision for sampled values
}

func (v Variable) symbol() string {
	if v.Symbol != "" {
		return v.Symbol
	}
	return v.Name
}

// Result describes the quantity an equation computes.
type Result struct {
	Symbol   string `yaml:"symbol,omitempty" json:"symbol,omitempty"`
	Unit     string `yaml:"unit,omitempty" json:"unit,omitempty"`
	Desc     string `yaml:"desc,omitempty" json:"desc,omitempty"`
	Decimals int    `yaml:"decimals,omitempty" json:"decimals,omitempty"`
}

// Example is a known (inputs -> expected) computation taken from the
// source material. The validator uses it to confirm the encoded Expr
// actually reproduces the curriculum's own worked answer — the gate that
// keeps an auto-extracted but wrong formula out of the live material.
type Example struct {
	Inputs   map[string]float64 `yaml:"inputs" json:"inputs"`
	Expected float64            `yaml:"expected" json:"expected"`
	Tol      float64            `yaml:"tol,omitempty" json:"tol,omitempty"` // absolute tolerance; default relative 0.5%
	Note     string             `yaml:"note,omitempty" json:"note,omitempty"`
}

// Equation is one computable catalog entry.
type Equation struct {
	ID       string     `yaml:"id" json:"id"`
	Module   string     `yaml:"module,omitempty" json:"module,omitempty"`
	Topic    string     `yaml:"topic,omitempty" json:"topic,omitempty"`
	Name     string     `yaml:"name" json:"name"`
	Source   string     `yaml:"source,omitempty" json:"source,omitempty"` // verbatim source equation, for display
	Expr     string     `yaml:"expr" json:"expr"`                         // computable; evaluates to the Result
	Vars     []Variable `yaml:"vars" json:"vars"`
	Result   Result     `yaml:"result" json:"result"`
	Prompt   string     `yaml:"prompt,omitempty" json:"prompt,omitempty"` // narrative template; {var} placeholders -> sampled values
	UseNote  string     `yaml:"use_note,omitempty" json:"use_note,omitempty"`
	Citation string     `yaml:"citation,omitempty" json:"citation,omitempty"`
	Examples []Example  `yaml:"examples,omitempty" json:"examples,omitempty"`
}

// inputs returns just the sampled variables.
func (e Equation) inputs() []Variable {
	var out []Variable
	for _, v := range e.Vars {
		if v.Role != RoleConstant {
			out = append(out, v)
		}
	}
	return out
}

// constEnv returns the constant portion of the evaluation environment.
func (e Equation) constEnv() map[string]float64 {
	env := map[string]float64{}
	for _, v := range e.Vars {
		if v.Role == RoleConstant {
			env[v.Name] = v.Value
		}
	}
	return env
}

func (e Equation) varByName(name string) (Variable, bool) {
	for _, v := range e.Vars {
		if v.Name == name {
			return v, true
		}
	}
	return Variable{}, false
}

// Catalog is a set of computable equations, typically loaded from a data
// file authored or auto-extracted per curriculum.
type Catalog struct {
	Program   string     `yaml:"program,omitempty" json:"program,omitempty"`
	Equations []Equation `yaml:"equations" json:"equations"`
}

// Verified reports whether the equation carries at least one source
// worked example that the validator can check the formula against. An
// unverified equation parses and computes but has no external oracle, so
// it is the right set for a human to spot-check after auto-extraction.
func (e Equation) Verified() bool { return len(e.Examples) > 0 }

// LoadCatalog reads a YAML catalog from disk.
func LoadCatalog(path string) (*Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read catalog %s: %w", path, err)
	}
	var c Catalog
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse catalog %s: %w", path, err)
	}
	return &c, nil
}

// Save writes the catalog back to YAML (used by the extractor after it
// drops entries that failed validation).
func (c *Catalog) Save(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal catalog: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// ByModule returns the equations for a module ("module_2"); empty module
// returns all. Stable order by topic then name.
func (c *Catalog) ByModule(module string) []Equation {
	var out []Equation
	for _, e := range c.Equations {
		if module == "" || e.Module == module {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Topic != out[j].Topic {
			return out[i].Topic < out[j].Topic
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Get returns the equation with the given ID, or false.
func (c *Catalog) Get(id string) (Equation, bool) {
	for _, e := range c.Equations {
		if e.ID == id {
			return e, true
		}
	}
	return Equation{}, false
}

var placeholderRe = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// placeholders returns the variable names referenced as {name} in s.
func placeholders(s string) []string {
	var out []string
	for _, m := range placeholderRe.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

// ValidationIssue describes one problem with a catalog entry.
type ValidationIssue struct {
	EquationID string
	Reason     string
}

func (v ValidationIssue) String() string {
	id := v.EquationID
	if id == "" {
		id = "<no id>"
	}
	return id + ": " + v.Reason
}

// Validate checks every equation in the catalog and returns the issues
// found. An equation with ANY issue is unsafe to use (wrong arithmetic in
// study material is worse than a missing entry), so callers typically
// drop equations that appear in the issue list. Checks:
//
//   - id, name, expr present
//   - Expr parses and only references declared variables / known constants
//   - every input has a usable [Min, Max] range; constants have a value
//   - Prompt placeholders all reference declared inputs
//   - a deterministic mid-range sample computes to a finite number
//   - every source Example reproduces (encoded Expr matches the
//     curriculum's own worked answer within tolerance) — the gate that
//     rejects auto-extracted-but-wrong formulas
func Validate(c *Catalog) []ValidationIssue {
	var issues []ValidationIssue
	seen := map[string]bool{}
	for _, e := range c.Equations {
		add := func(reason string) { issues = append(issues, ValidationIssue{EquationID: e.ID, Reason: reason}) }

		if e.ID == "" {
			add("missing id")
		} else if seen[e.ID] {
			add("duplicate id")
		} else {
			seen[e.ID] = true
		}
		if e.Name == "" {
			add("missing name")
		}
		if strings.TrimSpace(e.Expr) == "" {
			add("missing expr")
			continue
		}

		used, err := Variables(e.Expr)
		if err != nil {
			add(fmt.Sprintf("expr does not parse: %v", err))
			continue
		}
		declared := map[string]bool{}
		for _, v := range e.Vars {
			if v.Name == "" {
				add("variable with empty name")
				continue
			}
			declared[v.Name] = true
			if v.Role == RoleConstant {
				continue
			}
			if v.Max < v.Min {
				add(fmt.Sprintf("variable %q: max (%g) < min (%g)", v.Name, v.Max, v.Min))
			}
			if v.Max == v.Min {
				add(fmt.Sprintf("variable %q: empty sampling range (min == max == %g); set a range or mark it a constant", v.Name, v.Min))
			}
			if v.Step < 0 {
				add(fmt.Sprintf("variable %q: negative step %g", v.Name, v.Step))
			}
		}
		for _, u := range used {
			if !declared[u] {
				add(fmt.Sprintf("expr references undeclared variable %q", u))
			}
		}
		for _, ph := range placeholders(e.Prompt) {
			if !declared[ph] {
				add(fmt.Sprintf("prompt placeholder {%s} is not a declared variable", ph))
			}
		}

		// Deterministic mid-range smoke test.
		env := e.constEnv()
		for _, v := range e.inputs() {
			if declared[v.Name] {
				env[v.Name] = (v.Min + v.Max) / 2
			}
		}
		if got, err := Eval(e.Expr, env); err != nil {
			add(fmt.Sprintf("mid-range sample does not compute: %v", err))
		} else if got == 0 && len(e.inputs()) > 0 {
			// Not fatal, but a constant-zero result usually signals a bad expr.
			// Leave as informational only if there are no examples to prove it.
			_ = got
		}

		// The correctness gate: reproduce the curriculum's worked answers.
		for i, ex := range e.Examples {
			exEnv := e.constEnv()
			maps.Copy(exEnv, ex.Inputs)
			got, err := Eval(e.Expr, exEnv)
			if err != nil {
				add(fmt.Sprintf("example %d does not compute: %v", i, err))
				continue
			}
			tol := ex.Tol
			rel := false
			if tol == 0 {
				tol = 0.005 // 0.5% relative
				rel = true
			}
			diff := got - ex.Expected
			if diff < 0 {
				diff = -diff
			}
			bound := tol
			if rel {
				mag := ex.Expected
				if mag < 0 {
					mag = -mag
				}
				bound = tol * mag
				if bound < 1e-9 {
					bound = 1e-9
				}
			}
			if diff > bound {
				add(fmt.Sprintf("example %d: expr yields %g but source expects %g (diff %g > tol %g) — formula likely wrong", i, got, ex.Expected, diff, bound))
			}
		}
	}
	return issues
}

// Filter returns a copy of the catalog with every equation that has a
// validation issue removed, plus the issues that were dropped. This is the
// "auto-extract everything, keep only what computes correctly" path.
func (c *Catalog) Filter() (*Catalog, []ValidationIssue) {
	issues := Validate(c)
	bad := map[string]bool{}
	for _, is := range issues {
		bad[is.EquationID] = true
	}
	out := &Catalog{Program: c.Program}
	for _, e := range c.Equations {
		if !bad[e.ID] {
			out.Equations = append(out.Equations, e)
		}
	}
	return out, issues
}
