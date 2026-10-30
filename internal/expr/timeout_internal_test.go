package expr

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/catalog"
)

// TestTimeLayer checks that a run past the time limit returns ErrRuleFailed
// at the limit, and not when the run ends.
func TestTimeLayer(t *testing.T) {
	t.Parallel()

	e := New()
	e.timeout = time.Microsecond

	env := &catalog.Env{
		Value:   nil,
		Inputs:  map[string]any{"big": strings.Repeat("ab", 16<<10)},
		Usage:   nil,
		Input:   nil,
		Request: schema().Request,
	}

	start := time.Now()

	got, err := e.Bool(t.Context(), "inputs.big matches '^(a|b|ab|ba)*c$'", env)
	if got || !errors.Is(err, catalog.ErrRuleFailed) {
		t.Fatalf("Bool() = %v, %v, want false, ErrRuleFailed", got, err)
	}

	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("Bool() returned after %v, want about the limit of %v", elapsed, e.timeout)
	}
}

func TestNewTimeLimit(t *testing.T) {
	t.Parallel()

	if got, want := New().timeout, catalog.ExprTimeoutMillis*time.Millisecond; got != want {
		t.Fatalf("timeout = %v, want %v", got, want)
	}
}

// TestWorkLayer checks the work budget alone: the time limit is a minute, the
// environment is under its limit, and the run still ends at the budget.
func TestWorkLayer(t *testing.T) {
	t.Parallel()

	e := New()
	e.timeout = time.Minute

	env := &catalog.Env{
		Value:   nil,
		Inputs:  map[string]any{"big": strings.Repeat("ab", 8<<10)},
		Usage:   nil,
		Input:   nil,
		Request: schema().Request,
	}
	env.Request.Requester.Groups = make([]string, catalog.MaxEnvGroups)

	start := time.Now()

	_, err := e.run(t.Context(), "none(request.requester.groups, {inputs.big matches '^(a|b|ab|ba)*c$'})",
		catalog.ResultBool, env)
	if !errors.Is(err, errWork) || !errors.Is(err, catalog.ErrRuleFailed) {
		t.Fatalf("run() = %v, want the work budget error", err)
	}

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("run() took %v, want less than 1s", elapsed)
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

	e := New()
	e.timeout = time.Nanosecond

	env := &catalog.Env{
		Value:   nil,
		Inputs:  map[string]any{"s": "abc"},
		Usage:   nil,
		Input:   nil,
		Request: schema().Request,
	}
	env.Request.Requester.Groups = make([]string, catalog.MaxEnvGroups)

	got, err := e.Bool(t.Context(), "all(request.requester.groups, lower(inputs.s) == 'abc')", env)
	if got || !errors.Is(err, catalog.ErrRuleFailed) {
		t.Fatalf("Bool() = %v, %v, want false, ErrRuleFailed", got, err)
	}

	timeout := time.After(5 * time.Second)
	tick := time.NewTicker(time.Millisecond)

	defer tick.Stop()

	for len(e.slots) > 0 {
		select {
		case <-timeout:
			t.Fatalf("%d runs have not ended 5s after the limit", len(e.slots))
		case <-tick.C:
		}
	}
}

// TestRunCap checks that a full Evaluator fails closed, and works again when a
// slot is free.
func TestRunCap(t *testing.T) {
	t.Parallel()

	e := New()
	env := &catalog.Env{
		Value: nil, Inputs: nil, Usage: nil, Input: nil, Request: schema().Request,
	}

	for range MaxConcurrentRuns {
		e.slots <- struct{}{}
	}

	got, err := e.Bool(t.Context(), "true", env)
	if got || !errors.Is(err, catalog.ErrRuleFailed) || !errors.Is(err, errBusy) {
		t.Fatalf("Bool() on a full evaluator = %v, %v, want false, ErrRuleFailed", got, err)
	}

	<-e.slots

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
