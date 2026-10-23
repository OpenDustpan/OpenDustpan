package runnerapi_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// TestRenewAfterTheSessionWaitReleasesThroughTheClaimer proves that a renewal
// of a claim with no job session after MaxSessionWait releases the claim
// through the claimer, once, and answers 409 claim-lost.
func TestRenewAfterTheSessionWaitReleasesThroughTheClaimer(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	pool := e.createPool()
	r := e.newRunner("builder-1")

	if status, body := r.join(pool, e.joinToken(pool)); status != http.StatusOK {
		t.Fatalf("join = %d %s, want 200", status, body)
	}

	e.startOperation(pool)

	work := r.poll(2 * time.Second)
	if work == nil {
		t.Fatal("poll = 204, want work")
	}

	_ = r.handoff(work.Operation)

	e.clock.advance(runner.MaxSessionWait + time.Minute)

	status, body := r.call(opPath(work.Operation, "renew"), nil)
	if status != http.StatusConflict || errorCode(t, body) != protocol.ErrorClaimLost {
		t.Fatalf("renew after the session wait = %d %s, want 409 claim-lost", status, body)
	}

	if got := e.claimer.released.Load(); got != 1 {
		t.Fatalf("the claimer released %d phases, want 1: the handler releases through job.Claimer", got)
	}

	op, err := e.store.operations.Get(t.Context(), durable.ID(work.Operation))
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}

	if op.State != durable.StatePending {
		t.Fatalf("operation state = %q, want %q after the release", op.State, durable.StatePending)
	}
}

// TestHeartbeatListsAClaimWhoseLeaseLapsed proves that the heartbeat reports a
// claim as lost when its lease passed, also if no other runner took it.
func TestHeartbeatListsAClaimWhoseLeaseLapsed(t *testing.T) {
	t.Parallel()

	e := newEnvOn(t, openSQLite)
	pool := e.createPool()
	r := e.newRunner("builder-1")

	if status, body := r.join(pool, e.joinToken(pool)); status != http.StatusOK {
		t.Fatalf("join = %d %s, want 200", status, body)
	}

	e.startOperation(pool)

	work := r.poll(2 * time.Second)
	if work == nil {
		t.Fatal("poll = 204, want work")
	}

	e.lapse(work.Operation)

	if beat := r.heartbeat(work.Operation); len(beat.Lost) != 1 || beat.Lost[0] != work.Operation {
		t.Fatalf("heartbeat after the lease passed = %+v, want the claim lost", beat)
	}
}

// failingRecord is a session store whose RecordClaim fails.
type failingRecord struct {
	runnerStores
}

var errRecord = errors.New("record claim fails")

func (failingRecord) RecordClaim(context.Context, durable.Claim, string, int) error { return errRecord }

// TestFailedRecordClaimReleasesTheClaim proves that a claim that the server
// cannot record goes back through the claimer, so it does not wait for its
// lease, and that the poll fails.
func TestFailedRecordClaimReleasesTheClaim(t *testing.T) {
	t.Parallel()

	e := newEnvOn(t, func(t *testing.T) stores {
		t.Helper()

		s := openSQLite(t)
		s.runners = failingRecord{runnerStores: s.runners}

		return s
	})
	pool := e.createPool()
	r := e.newRunner("builder-1")

	if status, body := r.join(pool, e.joinToken(pool)); status != http.StatusOK {
		t.Fatalf("join = %d %s, want 200", status, body)
	}

	e.startOperation(pool)

	status, body := r.call(protocol.PathPoll, protocol.PollRequest{
		Wait: protocol.FromStd(time.Second), AssignmentVersion: runner.AssignmentVersion,
	})
	if status != http.StatusInternalServerError {
		t.Fatalf("poll after a failed record = %d %s, want 500", status, body)
	}

	if got := e.claimer.released.Load(); got != 1 {
		t.Fatalf("the claimer released %d phases, want 1", got)
	}
}
