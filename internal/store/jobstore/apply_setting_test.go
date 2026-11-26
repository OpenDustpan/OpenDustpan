package jobstore_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/store/jobstore"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// testResolver returns the resolver of the tests. The setting is on unless its
// database value is "false", and a missing row means the default, which is on.
// It stands in for the resolver of the server, which uses the same rule as the
// effective value of the setting.
func testResolver() jobstore.FreshResolver {
	return func(stored *string) (bool, error) {
		return stored == nil || *stored != "false", nil
	}
}

// settingTx is a jobstore.Tx whose settings table is a map. The embedded fake
// serves the other rows. A key that is not in the map has no row.
type settingTx struct {
	*fakeTx

	settings map[string]string
	err      error
}

// SettingValue implements jobstore.Tx.
func (f settingTx) SettingValue(_ context.Context, key string) (string, bool, error) {
	if f.err != nil {
		return "", false, f.err
	}

	v, ok := f.settings[key]

	return v, ok, nil
}

// changingRunner runs beforeWrite when a write starts, before the write
// transaction reads anything. It is the point between the start of a call and
// its write, where a setting change can land.
type changingRunner struct {
	tx          jobstore.Tx
	beforeWrite func()
}

// Write implements jobstore.Runner.
func (r changingRunner) Write(_ context.Context, _ namespace.Fence, fn func(jobstore.Tx) error) error {
	r.beforeWrite()

	return fn(r.tx)
}

// Read implements jobstore.Runner.
func (r changingRunner) Read(_ context.Context, fn func(jobstore.Tx) error) error {
	return fn(r.tx)
}

// Logger implements jobstore.Runner: nil, so the store logs nothing.
func (changingRunner) Logger() *slog.Logger { return nil }

// TestApplyReadsTheSettingInItsWriteTransaction proves that the approval of a
// destroy decides with the value of freshAuthEnabled that its write transaction
// reads. The setting goes off after Apply starts and before the write. The
// approval records the authentication none, so it passes only when the write
// sees the setting off.
func TestApplyReadsTheSettingInItsWriteTransaction(t *testing.T) {
	t.Parallel()

	now := time.UnixMilli(testClockMs).UTC()

	j := testJob(testUnit(t), now)
	j.State = job.StateAwaitingApproval
	j.Spec.Operation = job.OperationDestroy

	fake := newFakeTx(now)
	fake.put(t, &j)

	stored := map[string]string{"freshAuthEnabled": "true"}
	// The approver is a person, so only the setting decides the approval.
	base := settingTx{fakeTx: fake, settings: stored, err: nil}
	tx := kindTx{
		settingTx:  base,
		principals: map[string]identity.Kind{"ada": identity.KindPerson},
	}

	s := jobstore.New(changingRunner{
		tx:          tx,
		beforeWrite: func() { stored["freshAuthEnabled"] = "false" },
	}, idempotency.JobStartRetention())
	s.SetFreshResolver(testResolver())

	c := changeOf(j.ID, j.Version, job.EventApproved)
	c.Approval = &job.Approval{
		Gate: "", For: nil, Subject: "ada", Agent: "", Verdict: job.VerdictApprove, Comment: "", Plan: nil,
		At: now, Auth: job.ConfirmAuthNone, AuthAt: time.Time{},
	}

	if _, err := s.Apply(t.Context(), c, testFence(), nil); err != nil {
		t.Fatalf("apply the approval after the setting went off: %v", err)
	}
}
