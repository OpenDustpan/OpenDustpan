package expr

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/selfservice"
)

// TestTimeLayer checks that the time limit ends the run. The limit is 1 ns,
// so the run cannot finish first, and the error names the deadline. This
// test does not prove that the caller gets control back while the program
// still runs; TestUncountedWorkEndsWithinTheLimit proves that.
func TestTimeLayer(t *testing.T) {
	t.Parallel()

	e := mustNew(t, NewLimiter())
	e.timeout = time.Nanosecond

	env := &selfservice.Env{
		Value:   nil,
		Inputs:  map[string]any{"big": strings.Repeat("ab", 16<<10)},
		Usage:   nil,
		Input:   nil,
		Request: schema().Request,
	}

	got, err := e.Bool(t.Context(), "inputs.big matches '^(a|b|ab|ba)*c$'", env)
	if got || !errors.Is(err, selfservice.ErrRuleFailed) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Bool() = %v, %v, want false, ErrRuleFailed from the time limit", got, err)
	}
}

func TestNewTimeLimit(t *testing.T) {
	t.Parallel()

	if got, want := mustNew(t, NewLimiter()).timeout, selfservice.ExprTimeoutMillis*time.Millisecond; got != want {
		t.Fatalf("timeout = %v, want %v", got, want)
	}
}

// TestWorkLayer checks the work budget alone: the time limit is a minute, the
// environment is under its limit, and the run still ends at the budget. The
// error proves which layer ended the run: errWork comes only from the meter,
// and the time limit of a minute cannot end it first. The test reads no wall
// clock, so the load of the machine cannot change its result.
func TestWorkLayer(t *testing.T) {
	t.Parallel()

	e := mustNew(t, NewLimiter())
	e.timeout = time.Minute

	env := &selfservice.Env{
		Value:   nil,
		Inputs:  map[string]any{"big": strings.Repeat("ab", 8<<10)},
		Usage:   nil,
		Input:   nil,
		Request: schema().Request,
	}
	env.Request.Requester.Groups = make([]string, selfservice.MaxEnvGroups)

	_, err := e.run(t.Context(), "none(request.requester.groups, {inputs.big matches '^(a|b|ab|ba)*c$'})",
		selfservice.ResultBool, env)
	if !errors.Is(err, errWork) || !errors.Is(err, selfservice.ErrRuleFailed) {
		t.Fatalf("run() = %v, want the work budget error", err)
	}

	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errAlive) {
		t.Fatalf("run() = %v, want the work budget and not the time limit to end the run", err)
	}
}

// TestWorkBudgetCounts checks the units that a call charges.
func TestWorkBudgetCounts(t *testing.T) {
	t.Parallel()

	m := newMeter(func() error { return nil })

	if err := m.charge(MaxWorkUnits/MatchesUnitsPerByte, MatchesUnitsPerByte); err != nil {
		t.Fatalf("charge(the whole budget) = %v, want nil", err)
	}

	if err := m.charge(1, 1); !errors.Is(err, errWork) {
		t.Fatalf("charge(1) after the budget = %v, want errWork", err)
	}

	ended := newMeter(func() error { return context.Canceled })
	if err := ended.charge(1, 1); !errors.Is(err, errAlive) {
		t.Fatalf("charge() on an ended run = %v, want errAlive", err)
	}
}

// TestRunEndsAfterTheLimit checks that the goroutine of a run that passed the
// time limit ends at its next counted call, and gives its slot back.
func TestRunEndsAfterTheLimit(t *testing.T) {
	t.Parallel()

	e := mustNew(t, NewLimiter())
	e.timeout = time.Nanosecond

	env := &selfservice.Env{
		Value:   nil,
		Inputs:  map[string]any{"s": "abc"},
		Usage:   nil,
		Input:   nil,
		Request: schema().Request,
	}
	env.Request.Requester.Groups = make([]string, selfservice.MaxEnvGroups)

	got, err := e.Bool(t.Context(), "all(request.requester.groups, lower(inputs.s) == 'abc')", env)
	if got || !errors.Is(err, selfservice.ErrRuleFailed) {
		t.Fatalf("Bool() = %v, %v, want false, ErrRuleFailed", got, err)
	}

	timeout := time.After(5 * time.Second)
	tick := time.NewTicker(time.Millisecond)

	defer tick.Stop()

	for len(e.limiter.slots) > 0 {
		select {
		case <-timeout:
			t.Fatalf("%d runs have not ended 5s after the limit", len(e.limiter.slots))
		case <-tick.C:
		}
	}
}

// TestRunCap checks that a full Evaluator fails closed, and works again when a
// slot is free.
func TestRunCap(t *testing.T) {
	t.Parallel()

	e := mustNew(t, NewLimiter())
	env := &selfservice.Env{
		Value: nil, Inputs: nil, Usage: nil, Input: nil, Request: schema().Request,
	}

	for range MaxConcurrentRuns {
		e.limiter.slots <- struct{}{}
	}

	got, err := e.Bool(t.Context(), "true", env)
	if got || !errors.Is(err, selfservice.ErrRuleFailed) || !errors.Is(err, errBusy) {
		t.Fatalf("Bool() on a full evaluator = %v, %v, want false, ErrRuleFailed", got, err)
	}

	<-e.limiter.slots

	if got, err = e.Bool(t.Context(), "true", env); err != nil || !got {
		t.Fatalf("Bool() with a free slot = %v, %v, want true", got, err)
	}
}

func TestClip(t *testing.T) {
	t.Parallel()

	short := fmt.Errorf("%w", errBusy)
	if got := clip(short); !errors.Is(got, short) || got.Error() != errBusy.Error() {
		t.Fatalf("clip(short) = %v, want the same error", got)
	}

	cause := errWork
	long := fmt.Errorf("%w: %s", cause, strings.Repeat("é", 1000))
	got := clip(long)

	if n := len(got.Error()); n > MaxErrorBytes+len("...") || !utf8.ValidString(got.Error()) {
		t.Fatalf("clip(long) has %d bytes (valid UTF-8: %v), want at most %d and valid", n,
			utf8.ValidString(got.Error()), MaxErrorBytes+len("..."))
	}

	if !errors.Is(got, cause) {
		t.Fatalf("clip(long) = %v, want an error that wraps the cause", got)
	}
}

func mustNew(t *testing.T, l *Limiter) *Evaluator {
	t.Helper()

	e, err := New(l)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	return e
}

// TestNewNeedsALimiter checks that no Evaluator is made with a private limiter.
func TestNewNeedsALimiter(t *testing.T) {
	t.Parallel()

	e, err := New(nil)
	if !errors.Is(err, ErrNoLimiter) || e != nil {
		t.Fatalf("New(nil) = %v, %v, want nil, ErrNoLimiter", e, err)
	}
}

// TestEvaluatorsShareTheLimiter checks that the Evaluators of one Limiter share
// its cap: when the runs of the first fill it, the second fails closed.
func TestEvaluatorsShareTheLimiter(t *testing.T) {
	t.Parallel()

	limiter := limiterOf(2)
	first, second := mustNew(t, limiter), mustNew(t, limiter)
	other := mustNew(t, limiterOf(2))

	env := &selfservice.Env{Value: nil, Inputs: nil, Usage: nil, Input: nil, Request: schema().Request}

	// Two runs of the first Evaluator that have not ended.
	limiter.slots <- struct{}{}

	limiter.slots <- struct{}{}

	for name, e := range map[string]*Evaluator{"first": first, "second": second} {
		got, err := e.Bool(t.Context(), "true", env)
		if got || !errors.Is(err, selfservice.ErrRuleFailed) || !errors.Is(err, errBusy) {
			t.Fatalf("%s: Bool() on a full limiter = %v, %v, want false, ErrRuleFailed", name, got, err)
		}
	}

	// An Evaluator of another Limiter is not affected.
	if got, err := other.Bool(t.Context(), "true", env); err != nil || !got {
		t.Fatalf("other: Bool() = %v, %v, want true", got, err)
	}

	<-limiter.slots

	if got, err := second.Bool(t.Context(), "true", env); err != nil || !got {
		t.Fatalf("second: Bool() with a free slot = %v, %v, want true", got, err)
	}
}
