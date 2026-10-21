package conformance_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/internal/clientgen/conformance"
)

// countingProxy is a Proxy that counts Reset.
type countingProxy struct {
	conformance.Proxy

	resets int
}

func (p *countingProxy) Reset() { p.resets++ }

var errBroke = errors.New("broke")

func TestRunClassifiesCases(t *testing.T) {
	t.Parallel()

	pass := func(context.Context, conformance.Env) error { return nil }
	fail := func(context.Context, conformance.Env) error { return errBroke }
	skip := func(context.Context, conformance.Env) error { return fmt.Errorf("%w: x", conformance.ErrNotApplicable) }
	lose := func(context.Context, conformance.Env) error {
		return fmt.Errorf("%w: gone", conformance.ErrAdapterExited)
	}

	refuse := func(context.Context, conformance.Env) error {
		return fmt.Errorf("call: %w", &conformance.RPCError{Code: -32602, Message: "x"})
	}

	cases := []conformance.Case{
		{Group: conformance.GroupOperations, Name: "passes", Run: pass},
		{Group: conformance.GroupOperations, Name: "fails", Run: fail},
		{Group: conformance.GroupOperations, Name: "has-no-body", Run: nil},
		{Group: conformance.GroupOperations, Name: "does-not-apply", Run: skip},
		{Group: conformance.GroupErrors, Name: "adapter-refuses-one-request", Run: refuse},
		{Group: conformance.GroupErrors, Name: "runs-after-a-refusal", Run: pass},
		{Group: conformance.GroupErrors, Name: "loses-the-session", Run: lose},
		{Group: conformance.GroupErrors, Name: "runs-after-the-loss", Run: pass},
		{Group: conformance.GroupErrors, Name: "has-no-body-after-the-loss", Run: nil},
	}

	proxy := &countingProxy{Proxy: nil, resets: 0}
	results := conformance.Run(t.Context(), env(proxy), cases, 0)

	want := []conformance.Status{
		conformance.StatusPassed, conformance.StatusFailed, conformance.StatusNotImplemented,
		conformance.StatusNotApplicable, conformance.StatusFailed, conformance.StatusPassed,
		conformance.StatusFailed, conformance.StatusFailed, conformance.StatusNotImplemented,
	}

	if len(results) != len(want) {
		t.Fatalf("want %d results, got %d", len(want), len(results))
	}

	for i, r := range results {
		if r.Status != want[i] || r.Name != cases[i].Name {
			t.Errorf("result %d (%s): want %s, got %s (%v)", i, cases[i].Name, want[i], r.Status, r.Err)
		}
	}

	if proxy.resets != 6 {
		t.Errorf("the proxy resets before each case that runs: want 6, got %d", proxy.resets)
	}

	if !errors.Is(results[7].Err, conformance.ErrAdapterExited) {
		t.Errorf("a case after the loss of the session fails with the reason: %v", results[7].Err)
	}
}

func TestStatusStrings(t *testing.T) {
	t.Parallel()

	for s, want := range map[conformance.Status]string{
		conformance.StatusPassed: "passed", conformance.StatusFailed: "failed",
		conformance.StatusNotImplemented: "not implemented", conformance.StatusNotApplicable: "not applicable",
		conformance.Status(0): "unknown",
	} {
		if s.String() != want {
			t.Errorf("status %d: want %q, got %q", int(s), want, s.String())
		}
	}
}

// TestCasesOfTheFirstSliceHaveABody checks the cases that the Go client must
// pass. The others may have no body yet.
func TestCasesOfTheFirstSliceHaveABody(t *testing.T) {
	t.Parallel()

	have := make(map[string]bool)

	for _, c := range conformance.Cases() {
		have[string(c.Group)+"/"+c.Name] = c.Run != nil
	}

	for _, name := range requiredCases() {
		if !have[name] {
			t.Errorf("the case %s has no body", name)
		}
	}
}
