package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
)

// KindReap is the kind of the durable operation that sweeps expired reveals
// and ends orphan runs. The schedule of ReapSchedule starts one on one
// instance at each interval.
const KindReap durable.Kind = "agent.reap"

// ReapKindVersion is the version of KindReap that this binary starts.
const ReapKindVersion = 1

// StepReap is the one step of a reap operation.
const StepReap = "reap"

// ReapInterval is the interval of the reap schedule. A run whose turn
// operation failed for good holds its place in the limits of active runs for
// about this long at most, and a reveal stays about this long at most after
// its ExpiresAt.
const ReapInterval = time.Minute

// ReapSchedule returns the schedule of the reap operation, for the
// durable scheduler.
func ReapSchedule() durable.ScheduleSpec {
	return durable.ScheduleSpec{
		Name:     string(KindReap),
		Kind:     KindReap,
		Version:  ReapKindVersion,
		Interval: ReapInterval,
		Input:    []byte("{}"),
	}
}

// ReapResult is what one reap did, the result of its step.
type ReapResult struct {
	Swept  int `json:"swept"`
	Reaped int `json:"reaped"`
}

// Reap sweeps the sealed reveals that expired, then ends each orphan run:
// a run whose turn operation the durable runner failed or canceled with no
// step that ended the run. It reaps at most MaxRunList runs in one call; the
// next call takes the rest. A run that another instance ended or that went
// on between Orphans and Reap is not an error.
func Reap(ctx context.Context, runs RunStore) (ReapResult, error) {
	var res ReapResult

	swept, err := runs.SweepReveals(ctx)
	if err != nil {
		return res, fmt.Errorf("sweep expired reveals: %w", err)
	}

	res.Swept = swept

	orphans, err := runs.Orphans(ctx, MaxRunList)
	if err != nil {
		return res, fmt.Errorf("list orphan runs: %w", err)
	}

	for i := range orphans {
		_, err := runs.Reap(ctx, orphans[i].ID)

		switch {
		case err == nil:
			res.Reaped++
		case errors.Is(err, ErrFinished), errors.Is(err, ErrTurnActive), errors.Is(err, ErrNotFound):
		default:
			return res, fmt.Errorf("reap run %s: %w", orphans[i].ID, err)
		}
	}

	return res, nil
}

// ReapKind returns the durable kind of the reap operation, for
// durable.RunnerConfig.Kinds.
func ReapKind(runs RunStore) durable.KindSpec {
	return durable.KindSpec{
		Kind:       KindReap,
		Version:    ReapKindVersion,
		MinVersion: ReapKindVersion,
		Steps: []durable.Step{{Name: StepReap, Run: func(ctx context.Context, _ durable.StepContext) ([]byte, error) {
			res, err := Reap(ctx, runs)
			if err != nil {
				return nil, err
			}

			out, err := json.Marshal(res)
			if err != nil {
				return nil, fmt.Errorf("encode the reap result: %w", err)
			}

			return out, nil
		}}},
	}
}
