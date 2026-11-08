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

// missingNamespaceCases returns the cases of a claim whose namespace read
// does not give the namespace that held the job: the phase of that claim
// ends, so it does not hold the work of the other namespaces.
func missingNamespaceCases() []suiteCase {
	return []suiteCase{
		{"claimer: a phase whose namespace is missing ends and does not hold valid work", testMissingNamespace},
	}
}

// missingCase is one row of the case: the answer of the read of the namespace
// of the first job, and what the claims must do with its phase.
type missingCase struct {
	name string
	// readErr is the error of the read of the namespace of the first job.
	readErr error
	// replaced makes the read answer the name of the namespace of the first
	// job with the same name, the same failover version and another ID: a
	// namespace that a restore dropped and a later create made again under
	// the same name.
	replaced bool
	// runs makes the read answer the real namespace: the claim of the phase
	// of the first job runs.
	runs bool
	// cancel asks to cancel the job before the claim, so that its phase runs
	// the rule of a job that cannot take a claim.
	cancel bool
	// wantState is the state of the phase of the first job after the claims.
	wantState durable.State
	// wantCode is the last error of the phase of the first job.
	wantCode string
	// wantClaimErr is the error of a claim, for a read that can clear.
	wantClaimErr error
}

// missingCases returns the rows of the case. The row of a read that can clear
// is last: its phase stays claimable, and no later row may take it.
func missingCases() []missingCase {
	return []missingCase{
		{
			name: "a missing namespace ends the phase failed", readErr: namespace.ErrNotFound, replaced: false,
			runs: false, cancel: false, wantState: durable.StateFailed, wantCode: job.ReasonNamespaceMissing,
			wantClaimErr: nil,
		},
		{
			name: "a missing namespace ends the phase of a canceling job canceled", readErr: namespace.ErrNotFound,
			replaced: false, runs: false, cancel: true, wantState: durable.StateCanceled, wantCode: "",
			wantClaimErr: nil,
		},
		{
			name: "a namespace created again under the name ends the phase failed", readErr: nil, replaced: true,
			runs: false, cancel: false, wantState: durable.StateFailed, wantCode: job.ReasonNamespaceMissing,
			wantClaimErr: nil,
		},
		{
			name: "a job whose namespace ID matches runs its phase", readErr: nil, replaced: false, runs: true,
			cancel: false, wantState: durable.StateRunning, wantCode: "", wantClaimErr: nil,
		},
		{
			name: "an unavailable namespace gives the phase back", readErr: namespace.ErrUnavailable, replaced: false,
			runs: false, cancel: false, wantState: durable.StatePending, wantCode: "",
			wantClaimErr: namespace.ErrUnavailable,
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

// replacedNamespaces is a job.NamespaceReader over the namespaces of the
// harness that answers the name replaced with the same namespace under
// another ID, as a store answers when a restore dropped the namespace and a
// later create made another one under the same name. The ID is random and is
// never given again, so the answer is not the namespace that held the job.
type replacedNamespaces struct {
	reader   job.NamespaceReader
	replaced namespace.Name
}

// GetNamespace implements job.NamespaceReader.
func (r replacedNamespaces) GetNamespace(ctx context.Context, name namespace.Name) (namespace.Namespace, error) {
	ns, err := r.reader.GetNamespace(ctx, name)
	if err != nil {
		return namespace.Namespace{}, err
	}

	if name == r.replaced {
		ns.ID = namespace.NewID()
	}

	return ns, nil
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

// testMissingNamespace proves that the claim of a phase whose namespace read
// does not give the namespace that held the job ends the phase, so that the
// valid work of another namespace stays claimable, and that a read of the
// namespace that can clear gives the phase back instead of ending it. The
// phase of the first job is ahead of the valid work in the queue of the kind.
func testMissingNamespace(t *testing.T, h Harness) {
	t.Helper()

	for i, tc := range missingCases() {
		started := namespaceFixture(t, h, fmt.Sprintf("started-%d", i), unitName(i))

		// The phase of the first job is created first, so the store hands it
		// to a claim before the valid work.
		startedJob := dispatched(t, h, started, missingPlan())

		if tc.cancel {
			cancelPhase(t, h, started, &startedJob)
		}

		valid := namespaceFixture(t, h, fmt.Sprintf("valid-%d", i), unitName(i))
		validJob := dispatched(t, h, valid, missingPlan())

		var logged bytes.Buffer

		holder := missingClaimer(t, h, tc, started.fence.Namespace, &logged)

		if tc.runs {
			claimRuns(t, h, holder, tc, []*job.Job{&startedJob, &validJob})
		} else {
			claimMissing(t, h, holder, tc, &validJob)
		}

		op := checkMissingPhase(t, h, tc, &startedJob)

		checkMissingLog(t, tc, logged.String(), op)
	}
}

// missingClaimer returns the claimer of the case over the stores of the
// harness, with a clock at the injected time and the lines in logged. Its
// reader answers for the name of the namespace of the first job.
func missingClaimer(
	t *testing.T, h Harness, tc missingCase, name namespace.Name, logged *bytes.Buffer,
) *job.DurableClaimer {
	t.Helper()

	namespaces := h.Namespaces

	if tc.replaced {
		namespaces = replacedNamespaces{reader: h.Namespaces, replaced: name}
	} else if tc.readErr != nil {
		namespaces = goneNamespaces{reader: h.Namespaces, gone: name, err: tc.readErr}
	}

	// The time of the injected clock of the claimer.
	at := time.Date(2031, time.March, 4, 5, 6, 7, 0, time.UTC)

	return job.NewClaimer(
		h.Jobs, h.Durable, namespaces, NoGates{},
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
// phase back, so each poll returns the read error. A read that does not give
// the namespace that held the job ends the phase: the first poll takes the
// valid work, and the second ends the phase of the first job when the first
// one did not.
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

// claimRuns runs the claims of the row whose reader answers the real
// namespace: the phases of the jobs run in order, so the check does not
// refuse a valid job, and no phase stays claimable for a later row.
func claimRuns(t *testing.T, h Harness, holder job.Claimer, tc missingCase, jobs []*job.Job) {
	t.Helper()

	for _, want := range jobs {
		a, err := holder.Claim(t.Context(), request(runnerTwo, longRenew, runnerKind(want)))
		if err != nil {
			t.Fatalf("Claim, %s = %v, want the phase of job %s", tc.name, err, want.ID)
		}

		if a.Job.ID != want.ID {
			t.Fatalf("Claim, %s = job %s, want %s", tc.name, a.Job.ID, want.ID)
		}

		got := jobOf(t, h, want.ID)
		if got.State != job.StateRunning || got.Phases[0].Executor != runnerTwo {
			t.Fatalf("job %s after the claim, %s = %s under %q, want running under %s", want.ID, tc.name, got.State,
				got.Phases[0].Executor, runnerTwo)
		}
	}
}

// checkMissingPhase checks the state and the last error of the phase of the
// first job, and returns it.
func checkMissingPhase(t *testing.T, h Harness, tc missingCase, startedJob *job.Job) durable.Operation {
	t.Helper()

	op := opOf(t, h, startedJob.ID)
	if op.State != tc.wantState || op.LastError != tc.wantCode {
		t.Fatalf("phase of the first job, %s = %s, last error %q; want %s, %q", tc.name, op.State,
			op.LastError, tc.wantState, tc.wantCode)
	}

	return op
}

// checkMissingLog checks the line of the end of the phase: a namespace that
// does not exist, or a namespace that another one replaced under the same
// name, names the operation and the code, and a read that can clear writes
// none.
func checkMissingLog(t *testing.T, tc missingCase, line string, op durable.Operation) {
	t.Helper()

	if errors.Is(tc.readErr, namespace.ErrNotFound) || tc.replaced {
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
