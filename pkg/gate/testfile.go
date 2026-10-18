package gate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
)

// TestFile is a file of the offline runner "gates test": gates, rules and
// cases. A case evaluates facts and states the result and the matched
// gates that it expects.
type TestFile struct {
	Gates []Gate         `json:"gates"`
	Rules []ApprovalRule `json:"rules,omitempty"`
	Cases []TestCase     `json:"cases"`
}

// TestCase is one case of a TestFile.
type TestCase struct {
	Name      string    `json:"name"`
	Subject   Subject   `json:"subject"`
	Principal Principal `json:"principal"`
	Facts     Facts     `json:"facts"`
	Owners    string    `json:"owners,omitempty"`
	Expect    Expect    `json:"expect"`
}

// Expect is what a case expects. Matched lists gate IDs, sorted.
type Expect struct {
	Result  Result   `json:"result"`
	Matched []string `json:"matched,omitempty"`
}

// CaseResult is the outcome of one case.
type CaseResult struct {
	Name   string
	Passed bool
	Detail string
}

// RunTestFile reads the file at path and runs its cases on a fresh engine.
// It needs no server and no store. A case that fails does not stop the
// others.
func RunTestFile(ctx context.Context, path string) ([]CaseResult, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // path is the argument of the runner
	if err != nil {
		return nil, fmt.Errorf("read test file: %w", err)
	}

	var tf TestFile

	err = json.Unmarshal(raw, &tf)
	if err != nil {
		return nil, fmt.Errorf("%w: parse %s: %w", ErrInvalid, path, err)
	}

	eng := NewEngine()

	for _, g := range tf.Gates {
		err = eng.Registry.PutGate(g)
		if err != nil {
			return nil, err
		}
	}

	for _, r := range tf.Rules {
		err = eng.Registry.PutRule(r)
		if err != nil {
			return nil, err
		}
	}

	out := make([]CaseResult, 0, len(tf.Cases))

	for _, c := range tf.Cases {
		out = append(out, runCase(ctx, eng, c))
	}

	return out, nil
}

func runCase(ctx context.Context, eng *Engine, c TestCase) CaseResult {
	rec, err := eng.Planned(ctx, PlannedRequest{
		Subject: c.Subject, Principal: c.Principal, Facts: c.Facts, Owners: []byte(c.Owners),
	})
	if err != nil && !errors.Is(err, ErrRuleFailed) {
		return CaseResult{Name: c.Name, Detail: err.Error()}
	}

	got := make([]string, 0, len(rec.Matched))
	for _, m := range rec.Matched {
		got = append(got, m.Gate)
	}

	want := slices.Clone(c.Expect.Matched)
	slices.Sort(want)

	if rec.Result != c.Expect.Result || !slices.Equal(got, want) {
		return CaseResult{Name: c.Name, Detail: fmt.Sprintf(
			"want result %s matched %v, got result %s matched %v", c.Expect.Result, want, rec.Result, got,
		)}
	}

	return CaseResult{Name: c.Name, Passed: true}
}
