package expr

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/selfservice"
)

// waitIdle waits until every run of e has ended and has given its slot back.
func waitIdle(t *testing.T, e *Evaluator) {
	t.Helper()

	deadline := time.After(10 * time.Second)
	tick := time.NewTicker(time.Millisecond)

	defer tick.Stop()

	for len(e.limiter.slots) > 0 {
		select {
		case <-deadline:
			t.Fatalf("%d runs have not ended 10s after the limit", len(e.limiter.slots))
		case <-tick.C:
		}
	}
}

// bigList returns a list of n small items.
func bigList(n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = float64(i)
	}

	return out
}

// TestAbandonedRunOwnsItsData is the race layer. A run that passes the time
// limit leaves its goroutine reading the environment. The caller then changes
// the environment, as a caller that reuses it does. Under -race this test
// fails when the goroutine shares the data of the caller.
func TestAbandonedRunOwnsItsData(t *testing.T) {
	t.Parallel()

	e := mustNew(t, NewLimiter())
	e.timeout = time.Nanosecond

	list := bigList(100_000)
	nested := map[string]any{"deep": bigList(1000)}
	env := &selfservice.Env{
		Value:   list,
		Inputs:  map[string]any{"l": list, "m": nested},
		Usage:   map[string]int{"units": 1},
		Input:   nested,
		Request: schema().Request,
	}
	env.Request.Requester.Groups = []string{"a", "b"}

	got, err := e.Bool(t.Context(), "count(inputs.l, {# >= 0}) > 0 && len(inputs.m.deep) > 0", env)
	if got || !errors.Is(err, selfservice.ErrRuleFailed) || errors.Is(err, errEnvTooBig) {
		t.Fatalf("Bool() = %v, %v, want false, ErrRuleFailed from the time limit", got, err)
	}

	// The caller changes every shared part while the abandoned run reads.
	for i := range list {
		list[i] = float64(-i)
	}

	nested["deep"] = bigList(1)
	env.Inputs["l"] = nil
	env.Usage["units"] = 2
	env.Request.Requester.Groups[0] = "changed"

	waitIdle(t, e)
}

// TestCloneEnvIsDeep checks that the copy shares no list, map or slice with
// the original.
func TestCloneEnvIsDeep(t *testing.T) {
	t.Parallel()

	list := []any{"x"}
	object := map[string]any{"l": list}
	input := map[string]any{"k": "v"}
	env := &selfservice.Env{
		Value:   list,
		Inputs:  map[string]any{"k": object},
		Usage:   map[string]int{"u": 1},
		Input:   input,
		Request: schema().Request,
	}
	env.Request.Requester.Groups = []string{"g"}

	got := cloneEnv(env)

	list[0] = "changed"
	object["l"] = "changed"
	env.Usage["u"] = 2
	input["k"] = "w"
	env.Request.Requester.Groups[0] = "h"

	want := selfservice.Env{
		Value:   []any{"x"},
		Inputs:  map[string]any{"k": map[string]any{"l": []any{"x"}}},
		Usage:   map[string]int{"u": 1},
		Input:   map[string]any{"k": "v"},
		Request: schema().Request,
	}
	want.Request.Requester.Groups = []string{"g"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cloneEnv() = %+v, want %+v: the copy shares data with the original", got, want)
	}
}

// TestUncountedWorkEndsWithinTheLimit runs expressions whose work no counter
// of the meter covers: scans of the largest list that the environment allows,
// and of nested lists. Each must return within a short time after the limit,
// with a result or with ErrRuleFailed, and its goroutine must end.
func TestUncountedWorkEndsWithinTheLimit(t *testing.T) {
	t.Parallel()

	rows := make([]any, 200)
	for i := range rows {
		rows[i] = bigList(400)
	}

	tests := []struct {
		name   string
		source string
	}{
		{"scan of the largest list", "count(inputs.l, {# == -1}) == 0"},
		{"filter of the largest list", "len(filter(inputs.l, {# > 10})) > 0"},
		{"sum of the largest list", "sum(inputs.l) > 0"},
		{"scan of nested lists", "sum(map(inputs.rows, {len(#)})) > 0"},
		{"max of nested lists", "max(map(inputs.rows, {max(#)})) > 0"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := mustNew(t, NewLimiter())
			env := &selfservice.Env{
				Value:   nil,
				Inputs:  map[string]any{"l": bigList(100_000), "rows": rows},
				Usage:   nil,
				Input:   nil,
				Request: schema().Request,
			}

			start := time.Now()
			_, err := e.Bool(t.Context(), tc.source, env)

			if errors.Is(err, errEnvTooBig) {
				t.Fatalf("Bool() error = %v, the test environment must be under its size limit", err)
			}

			if err != nil && !errors.Is(err, selfservice.ErrRuleFailed) {
				t.Fatalf("Bool() error = %v, want nil or ErrRuleFailed", err)
			}

			if elapsed := time.Since(start); elapsed > time.Second {
				t.Fatalf("Bool() returned after %v, want within 1s of the limit of %v", elapsed, e.timeout)
			}

			waitIdle(t, e)
		})
	}
}
