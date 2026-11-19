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
