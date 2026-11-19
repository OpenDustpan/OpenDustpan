package gate

import (
	"context"
	"errors"
	"testing"
	"time"
)

// spyBreakGlassStore counts its Consume calls and returns no pass.
type spyBreakGlassStore struct {
	consumes int
}

func (*spyBreakGlassStore) Issue(context.Context, BreakGlassPass) error { return nil }

func (s *spyBreakGlassStore) Consume(context.Context, BreakGlassKey, time.Time) (BreakGlassPass, error) {
	s.consumes++

	var none BreakGlassPass

	return none, nil
}

// TestApplyBreakGlassOnlyPassesRuleFailed calls applyBreakGlass with
// onlyErrors set, as if the decision above it were wrong. Only a refusal of
// the kind ErrRuleFailed may pass, so no other refusal uses a pass.
func TestApplyBreakGlassOnlyPassesRuleFailed(t *testing.T) {
	t.Parallel()

	for name, refusalErr := range map[string]error{
		"a deny":           ErrDenied,
		"an engine down":   ErrUnavailable,
		"an invalid input": ErrInvalid,
	} {
		spy := &spyBreakGlassStore{consumes: 0}
		var (
			svc Service
			run stateWriteRun
			rec Record
		)

		svc.breakGlass, svc.now = spy, time.Now
		run.svc, run.onlyErrors = &svc, true
		run.refusal = &RefusalError{Err: refusalErr, Record: "", Messages: nil}

		key := BreakGlassKey{Holder: "alice", UnitID: "ws-1", Kind: OperationApply}

		run.applyBreakGlass(t.Context(), &rec, key)

		if spy.consumes != 0 || run.refusal == nil || !errors.Is(run.refusal.Err, refusalErr) || rec.BreakGlass != "" {
			t.Errorf("%s: consumes = %d, refusal = %v, want no pass used and the refusal kept",
				name, spy.consumes, run.refusal)
		}
	}
}

// TestDecideOnlyErrors calls decide directly and checks that onlyErrors is
// true only when a gate in error is the sole reason of the refusal. Each row
// holds one term of the rule: a deny, an engine failure, no error, an
// approval.
func TestDecideOnlyErrors(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		total      unitEffect
		engineDown bool
		want       bool
	}{
		"errors only":      {total: unitEffect{errs: []string{"e"}}, want: true},
		"denies and errs":  {total: unitEffect{denies: []string{"d"}, errs: []string{"e"}}, want: false},
		"errs, engineDown": {total: unitEffect{errs: []string{"e"}}, engineDown: true, want: false},
		"no errs, no deny": {total: unitEffect{}, want: false},
		"approvals + errs": {total: unitEffect{approvals: []string{"a"}, errs: []string{"e"}}, want: false},
		"blocked + errs":   {total: unitEffect{blocked: true, errs: []string{"e"}}, want: false},
	} {
		var (
			run stateWriteRun
			rec Record
		)

		run.engineErr = errors.New("engine down")
		total := tc.total

		run.decide(&rec, &total, tc.engineDown)

		if run.onlyErrors != tc.want {
			t.Errorf("%s: onlyErrors = %v, want %v", name, run.onlyErrors, tc.want)
		}
	}
}

// TestApplyBreakGlassNoRefusalUsesNoPass calls applyBreakGlass with no
// refusal and onlyErrors set, as if the decision above it were wrong. Nothing
// is refused, so no pass is used and nothing panics.
func TestApplyBreakGlassNoRefusalUsesNoPass(t *testing.T) {
	t.Parallel()

	spy := &spyBreakGlassStore{consumes: 0}
	var (
		svc Service
		run stateWriteRun
		rec Record
	)

	svc.breakGlass, svc.now = spy, time.Now
	run.svc, run.onlyErrors, run.refusal = &svc, true, nil

	run.applyBreakGlass(t.Context(), &rec, BreakGlassKey{Holder: "alice", UnitID: "ws-1", Kind: OperationApply})

	if spy.consumes != 0 || run.refusal != nil || rec.BreakGlass != "" {
		t.Errorf("consumes = %d, refusal = %v, BreakGlass = %q, want no pass used and no change",
			spy.consumes, run.refusal, rec.BreakGlass)
	}
}
