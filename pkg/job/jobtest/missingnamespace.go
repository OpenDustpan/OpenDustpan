package jobtest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// missingNamespaceCases returns the cases of a claim whose namespace does not
// exist: the phase of that claim ends, so it does not hold the work of the
// other namespaces.
func missingNamespaceCases() []suiteCase {
	return []suiteCase{
		{"claimer: a phase whose namespace is missing ends and does not hold valid work", testMissingNamespace},
	}
}

// missingCase is one row of the case: the error of the read of the namespace
// of the job, and what the claims must do with its phase.
type missingCase struct {
	name string
	// readErr is the error of the read of the namespace of the job.
	readErr error
	// cancel asks to cancel the job before the claim, so that its phase runs
	// the rule of a job that cannot take a claim.
	cancel bool
	// wantState is the state of the phase after the claims.
	wantState durable.State
	// wantCode is the last error of the phase.
	wantCode string
	// wantClaimErr is the error of a claim, for a read that can clear.
	wantClaimErr error
}

// missingCases returns the rows of the case.
func missingCases() []missingCase {
	return []missingCase{
		{
			name: "a missing namespace ends the phase failed", readErr: namespace.ErrNotFound,
			cancel: false, wantState: durable.StateFailed, wantCode: job.ReasonNamespaceMissing, wantClaimErr: nil,
		},
		{
			name: "a missing namespace ends the phase of a canceling job canceled", readErr: namespace.ErrNotFound,
			cancel: true, wantState: durable.StateCanceled, wantCode: "", wantClaimErr: nil,
		},
		{
			name: "an unavailable namespace gives the phase back", readErr: namespace.ErrUnavailable,
			cancel: false, wantState: durable.StatePending, wantCode: "", wantClaimErr: namespace.ErrUnavailable,
		},
	}
}

// goneNamespaces is a job.NamespaceReader over the namespaces of the harness
// that answers err for the name gone, as a store answers the namespace that a
// purge removed or a store that does not answer.
type goneNamespaces struct {
	reader job.NamespaceReader
	gone   namespace.Name
	err    error
}

// GetNamespace implements job.NamespaceReader.
func (g goneNamespaces) GetNamespace(ctx context.Context, name namespace.Name) (namespace.Namespace, error) {
	if name == g.gone {
		return namespace.Namespace{}, g.err
	}

	return g.reader.GetNamespace(ctx, name)
}

// namespaceFixture creates the namespace name with one OpenTofu unit in it.
func namespaceFixture(t *testing.T, h Harness, name, unit string) fixture {
	t.Helper()

	fence, err := h.Domain.CreateNamespace(t.Context(), namespace.Name(name))
	if err != nil {
		t.Fatalf("CreateNamespace(%s) = %v", name, err)
	}

	created, err := h.Domain.CreateUnit(t.Context(), fence, "opentofu", unit)
	if err != nil {
		t.Fatalf("CreateUnit(%s) = %v", unit, err)
	}

	return fixture{fence: fence, unit: created}
}

// missingPlan is the phase of the jobs of the case: a refresh whose steps
// change resources.
func missingPlan() plannedJob {
	return plannedJob{unit: "", op: job.OperationRefresh, steps: changeSteps()}
}

// opOf returns the operation of the current phase of the job id.
func opOf(t *testing.T, h Harness, id job.ID) durable.Operation {
	t.Helper()

	j := jobOf(t, h, id)

	op, err := h.Durable.Get(t.Context(), durable.ID(j.Phases[j.Current].Operation))
	if err != nil {
		t.Fatalf("durable Get(%s) = %v", j.Phases[j.Current].Operation, err)
	}

	return op
}

// testMissingNamespace proves that the claim of a phase whose namespace does
// not exist ends the phase, so that the valid work of another namespace stays
// claimable, and that a read of the namespace that can clear gives the phase
// back instead of ending it. The phase of the missing namespace is ahead of
// the valid work in the queue of the kind.
func testMissingNamespace(t *testing.T, h Harness) {
	t.Helper()

	for i, tc := range missingCases() {
		gone := namespaceFixture(t, h, fmt.Sprintf("gone-%d", i), unitName(i))

		// The phase of the missing namespace is created first, so the store
		// hands it to a claim before the valid work.
		goneJob := dispatched(t, h, gone, missingPlan())

		if tc.cancel {
			cancelPhase(t, h, gone, &goneJob)
		}

		valid := namespaceFixture(t, h, fmt.Sprintf("valid-%d", i), unitName(i))
		validJob := dispatched(t, h, valid, missingPlan())

		var logged bytes.Buffer

		holder := missingClaimer(t, h, gone.fence.Namespace, tc.readErr, &logged)

		claimMissing(t, h, holder, tc, &validJob)

		op := checkMissingPhase(t, h, tc, &goneJob)

		checkMissingLog(t, tc, logged.String(), op)
	}
}

// missingClaimer returns the claimer of the case over the stores of the
// harness, with a clock at the injected time and the lines in logged.
func missingClaimer(
	t *testing.T, h Harness, gone namespace.Name, readErr error, logged *bytes.Buffer,
) *job.DurableClaimer {
	t.Helper()

	// The time of the injected clock of the claimer.
	at := time.Date(2031, time.March, 4, 5, 6, 7, 0, time.UTC)

	return job.NewClaimer(
		h.Jobs, h.Durable, goneNamespaces{reader: h.Namespaces, gone: gone, err: readErr}, NoGates{},
		job.WithClaimerClock(func() time.Time { return at }),
		job.WithClaimerLogger(slog.New(slog.NewTextHandler(logged, nil))),
	)
}

// cancelPhase claims the phase of the job, shortens the lease, cancels the
// job and lets the lease pass, so that the next claim of the phase finds a
// canceling job that cannot take a claim.
func cancelPhase(t *testing.T, h Harness, f fixture, j *job.Job) {
	t.Helper()

	a := claim(t, h, runnerOne, longRenew, runnerKind(j))
	shorten(t, h, &a)

	if canceling := cancelJob(t, h, f, j.ID); canceling.State != job.StateCanceling {
		t.Fatalf("job after the cancel = %s, want canceling", canceling.State)
	}

	expired(t, a.Claim)
}

// claimMissing runs the polls of the row. A read that can clear gives the
// phase back, so each poll returns the read error. A missing namespace ends
// the phase: the first poll takes the valid work, and the second ends the
// phase of the missing namespace when the first one did not.
func claimMissing(t *testing.T, h Harness, holder job.Claimer, tc missingCase, valid *job.Job) {
	t.Helper()

	k := runnerKind(valid)

	if tc.wantClaimErr != nil {
		for range 2 {
			_, err := holder.Claim(t.Context(), request(runnerTwo, longRenew, k))
			if !errors.Is(err, tc.wantClaimErr) {
				t.Fatalf("Claim, %s = %v, want %v", tc.name, err, tc.wantClaimErr)
			}
		}

		return
	}

	a, err := holder.Claim(t.Context(), request(runnerTwo, longRenew, k))
	if err != nil {
		t.Fatalf("Claim, %s = %v, want the work of the valid job", tc.name, err)
	}

	if a.Job.ID != valid.ID {
		t.Fatalf("Claim, %s = job %s, want %s", tc.name, a.Job.ID, valid.ID)
	}

	if _, err := holder.Claim(t.Context(), request(runnerTwo, longRenew, k)); !errors.Is(err, durable.ErrNoWork) {
		t.Fatalf("second Claim, %s = %v, want durable.ErrNoWork", tc.name, err)
	}

	got := jobOf(t, h, valid.ID)
	if got.State != job.StateRunning || got.Phases[0].Executor != runnerTwo {
		t.Fatalf("valid job after the claims, %s = %s under %q, want running under %s", tc.name, got.State,
			got.Phases[0].Executor, runnerTwo)
	}
}

// checkMissingPhase checks the state and the last error of the phase of the
// missing namespace, and returns it.
func checkMissingPhase(t *testing.T, h Harness, tc missingCase, goneJob *job.Job) durable.Operation {
	t.Helper()

	op := opOf(t, h, goneJob.ID)
	if op.State != tc.wantState || op.LastError != tc.wantCode {
		t.Fatalf("phase of the missing namespace, %s = %s, last error %q; want %s, %q", tc.name, op.State,
			op.LastError, tc.wantState, tc.wantCode)
	}

	return op
}

// checkMissingLog checks the line of the end of the phase: a namespace that
// does not exist names the operation and the code, and a read that can clear
// writes none.
func checkMissingLog(t *testing.T, tc missingCase, line string, op durable.Operation) {
	t.Helper()

	if errors.Is(tc.readErr, namespace.ErrNotFound) {
		if !strings.Contains(line, string(op.ID)) || !strings.Contains(line, job.ReasonNamespaceMissing) {
			t.Fatalf("log, %s = %q, want the operation %s and the code %s", tc.name, line, op.ID,
				job.ReasonNamespaceMissing)
		}

		return
	}

	if line != "" {
		t.Fatalf("log, %s = %q, want no line for a read that can clear", tc.name, line)
	}
}
