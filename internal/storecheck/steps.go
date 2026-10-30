package storecheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/durable"
)

// input is the input of a check operation.
type input struct {
	StartedAt time.Time `json:"startedAt"`
	ID        ID        `json:"id"`
	Target    Target    `json:"target"`
	Actor     Actor     `json:"actor"`
}

// outcome is the result of the run step: the report, and the reason when the
// check did not run to its end. Failure is fixed text.
type outcome struct {
	Report  Report `json:"report"`
	Failure string `json:"failure,omitempty"`
}

// readInput decodes and checks the input of an operation. An input that
// fails is permanent: no retry changes it.
func readInput(op durable.Operation) (input, error) {
	var in input
	if err := json.Unmarshal(op.Input, &in); err != nil {
		return input{}, durable.Permanent(fmt.Errorf("decode input of store check operation: %w", err))
	}

	id, err := ParseID(in.ID.String())
	if err != nil {
		return input{}, durable.Permanent(err)
	}

	// Defense in depth: the start key names the check too. This check stays so
	// that an operation whose input names another check never runs that check.
	if op.Key != OperationKey(id) {
		return input{}, durable.Permanent(fmt.Errorf("%w: the input names another check", ErrInvalidID))
	}

	target, err := ParseTarget(string(in.Target))
	if err != nil {
		return input{}, durable.Permanent(err)
	}

	in.ID, in.Target = id, target

	return in, nil
}

// stepRun runs the cases on the throwaway prefix. It writes no row: its result
// is the outcome, which the runner records, and the finish step stores. The
// step is safe to run again after a takeover: it deletes what an earlier run
// left under the prefix first.
func (s *Service) stepRun(ctx context.Context, sc durable.StepContext) ([]byte, error) {
	in, err := readInput(sc.Operation)
	if err != nil {
		return nil, err
	}

	// A check that waited in the queue while maintenance mode started, or a
	// restore began, waits too. The runner defers it.
	if admitErr := s.admitNow(ctx); admitErr != nil {
		return nil, admitErr
	}

	store, ok := s.stores[in.Target]
	if !ok {
		return nil, durable.Permanent(fmt.Errorf("%w: %s", ErrNoTarget, in.Target))
	}

	result, err := s.execute(ctx, in, store)
	if err != nil {
		return nil, err
	}

	data, err := json.Marshal(result)
	if err != nil {
		return nil, durable.Permanent(fmt.Errorf("encode report of store check %s: %w", in.ID, err))
	}

	return data, nil
}

// execute runs the check. It returns an error only when ctx ended, so the
// runner releases the claim. Every other end is an outcome.
func (s *Service) execute(ctx context.Context, in input, store blob.Store) (outcome, error) {
	prefix := in.ID.Prefix()

	view, err := s.viewOf(ctx, store, in.ID, prefix)
	if err != nil {
		return outcome{}, durable.Permanent(err)
	}

	cleanCtx, stop := context.WithTimeout(ctx, cleanupTimeout)
	defer stop()

	// An earlier run of the step, before a takeover, can have left objects.
	if _, clearErr := deletePrefix(cleanCtx, view, prefix); clearErr != nil && ctx.Err() == nil {
		return outcome{}, durable.Permanent(
			fmt.Errorf("clear the prefix of store check %s: %w", in.ID, errPrefixNotCleared),
		)
	}

	runCtx, cancel := context.WithTimeout(ctx, s.limits.Timeout)
	defer cancel()

	run := newRun(view, s.limits, s.now)
	report, failure := run.all(runCtx, in)

	if ctx.Err() != nil {
		return outcome{}, fmt.Errorf("store check %s stopped: %w", in.ID, ctx.Err())
	}

	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		failure = fmt.Sprintf("the check stopped after the time limit of %s", s.limits.Timeout)
	}

	// The delete of the prefix has its own time limit, so a check that ran out
	// of time still removes its objects.
	remaining, delErr := run.finishPrefix(cleanCtx, &report)
	if ctx.Err() != nil {
		return outcome{}, fmt.Errorf("store check %s stopped: %w", in.ID, ctx.Err())
	}

	if delErr != nil {
		s.logger.WarnContext(ctx, "store check left objects under its prefix",
			slog.String("check", in.ID.String()), slog.Int("objects", remaining),
			slog.String("error", errorClass(delErr)))
	}

	return outcome{Report: report, Failure: failure}, nil
}

// stepFinish stores the report in the row and writes the end entry. The row
// write records the step in its own transaction. The entry follows the
// commit, so a crash between the two loses the entry and never repeats it.
func (s *Service) stepFinish(ctx context.Context, sc durable.StepContext) ([]byte, error) {
	in, err := readInput(sc.Operation)
	if err != nil {
		return nil, err
	}

	rec, ok := sc.Operation.Step(StepRun)
	if !ok {
		return nil, durable.Permanent(fmt.Errorf("store check %s: %w", in.ID, errNoRunResult))
	}

	var out outcome
	if decodeErr := json.Unmarshal(rec.Result, &out); decodeErr != nil {
		return nil, durable.Permanent(fmt.Errorf("decode result of store check %s: %w", in.ID, decodeErr))
	}

	end := Finish{EndedAt: s.now().UTC(), State: StateSucceeded, Error: out.Failure, Report: nil}
	if out.Failure != "" {
		end.State = StateFailed
	}

	// The row keeps the report alone, without the failure text.
	report, err := json.Marshal(out.Report)
	if err != nil {
		return nil, durable.Permanent(fmt.Errorf("encode report of store check %s: %w", in.ID, err))
	}

	end.Report = report

	if finishErr := s.records.FinishCheck(ctx, sc.Fence, in.ID, end); finishErr != nil {
		if errors.Is(finishErr, durable.ErrClaimLost) || errors.Is(finishErr, durable.ErrCanceled) {
			return nil, finishErr
		}

		return nil, durable.Permanent(fmt.Errorf("finish store check %s: %w", in.ID, finishErr))
	}

	s.auditEnd(ctx, in, out)

	return nil, nil
}

// auditEnd writes the end entry: the target, the principal that asked and
// the result.
func (s *Service) auditEnd(ctx context.Context, in input, out outcome) {
	entry := s.entry(in.Actor, "store-check "+in.ID.String())
	entry.Outcome = audit.OutcomeSuccess

	result := "passed"

	switch {
	case out.Failure != "":
		entry.Outcome = audit.OutcomeFailure
		entry.Reason = out.Failure
		result = "failed"
	case !out.Report.Passed:
		entry.Outcome = audit.OutcomeFailure
		result = "cases failed"
	default:
	}

	entry.Changes = []audit.Change{
		{Key: "result", Old: nil, New: result, Layer: "", TakesEffect: ""},
		{Key: "target", Old: nil, New: string(in.Target), Layer: "", TakesEffect: ""},
	}

	s.appendAudit(ctx, entry)
}
