package expr

import (
	"context"
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
// and of nested lists. The time limit is 0, so the context is already done
// with context.DeadlineExceeded and the test does not depend on a timer. A run
// that ends after the limit fails closed: the caller gets the error of the
// deadline, and the goroutine of the run ends at the end of its scan.
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
			e.timeout = 0

			env := &selfservice.Env{
				Value:   nil,
				Inputs:  map[string]any{"l": bigList(100_000), "rows": rows},
				Usage:   nil,
				Input:   nil,
				Request: schema().Request,
			}

			_, err := e.Bool(t.Context(), tc.source, env)

			if errors.Is(err, errEnvTooBig) {
				t.Fatalf("Bool() error = %v, the test environment must be under its size limit", err)
			}

			if !errors.Is(err, selfservice.ErrRuleFailed) || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("Bool() error = %v, want ErrRuleFailed from the time limit", err)
			}

			waitIdle(t, e)
		})
	}
}

// TestRunOutcomeFailsClosedAtTheLimit checks the time limit of a run that
// ended. When the context is done, the outcome is the error of the deadline,
// also when the run gave a result. In run, the result and the end of the
// context can be ready together, and select then picks one at random.
func TestRunOutcomeFailsClosedAtTheLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		ctx      func(*testing.T) context.Context
		result   runResult
		want     any
		wantErrs []error
	}{
		{
			name:     "result after the limit",
			ctx:      pastLimit,
			result:   runResult{out: true, err: nil},
			want:     nil,
			wantErrs: []error{selfservice.ErrRuleFailed, context.DeadlineExceeded},
		},
		{
			name:     "error after the limit",
			ctx:      pastLimit,
			result:   runResult{out: nil, err: selfservice.ErrLimit},
			want:     nil,
			wantErrs: []error{selfservice.ErrRuleFailed, context.DeadlineExceeded},
		},
		{
			name:     "result within the limit",
			ctx:      (*testing.T).Context,
			result:   runResult{out: true, err: nil},
			want:     true,
			wantErrs: nil,
		},
		{
			name:     "error within the limit",
			ctx:      (*testing.T).Context,
			result:   runResult{out: nil, err: selfservice.ErrLimit},
			want:     nil,
			wantErrs: []error{selfservice.ErrRuleFailed, selfservice.ErrLimit},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := runOutcome(tc.ctx(t), tc.result)
			checkRunOutcome(t, got, err, tc.want, tc.wantErrs)
		})
	}
}

// pastLimit answers a context whose time limit has passed.
func pastLimit(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithDeadline(t.Context(), time.Unix(0, 0))
	t.Cleanup(cancel)

	return ctx
}

// checkRunOutcome checks the outcome of runOutcome against want and the
// errors that err must wrap.
func checkRunOutcome(t *testing.T, got any, err error, want any, wantErrs []error) {
	t.Helper()

	if got != want {
		t.Errorf("runOutcome() = %v, want %v", got, want)
	}

	if len(wantErrs) == 0 && err != nil {
		t.Fatalf("runOutcome() error = %v, want nil", err)
	}

	for _, wantErr := range wantErrs {
		if !errors.Is(err, wantErr) {
			t.Errorf("runOutcome() error = %v, want %v", err, wantErr)
		}
	}
}
