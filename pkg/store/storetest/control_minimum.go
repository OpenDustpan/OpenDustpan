package storetest

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/store/schema"
)

// ControlMinimum is the minimum binary version that Raise sets in the
// control minimum suites.
const ControlMinimum = "v0.5.0"

// ControlBackups is the part of the backup port that starts and finishes a
// backup row.
type ControlBackups interface {
	StartBackup(ctx context.Context, r backup.Record, start durable.StartRequest) error
	FinishBackup(
		ctx context.Context, f durable.StepFence, id backup.ID, state backup.State, errText string, endedAt time.Time,
	) (backup.Record, error)
}

// ControlDurable is every port of the durable operations that a control path
// writes.
type ControlDurable interface {
	durable.Store
	durable.StepCanceler
	durable.HeartbeatStore
	durable.ScheduleStore
}

// ControlAudit is every port of the audit log that a control path writes.
type ControlAudit interface {
	audit.ServerStore
	audit.ChainStore
	audit.CheckStore
	audit.SinkLeaser
}

// ControlStore groups the ports of one store whose control writes run in a
// transaction with no installation state check. A control write reads the
// minimum binary version only when it changes domain data or runs work.
type ControlStore struct {
	Instances   installation.InstanceStore
	Maintenance maintenance.Store
	Backups     ControlBackups
	Durable     ControlDurable
	Audit       ControlAudit
	Failures    authgate.FailureCounter
	// Handle is the own handle of the adapter, for its harness: the suites do
	// not read it.
	Handle any
}

// ControlMinimumHarness gives the control minimum suites a database and the
// way to raise its minimum. Each method works on a fresh database.
type ControlMinimumHarness struct {
	// Open opens a new database as a binary of version binary. The minimum of
	// the database is not set yet.
	Open func(t *testing.T, binary string) ControlStore
	// Raise sets the minimum binary version of the database of s to
	// ControlMinimum, as the contract migration of a new binary does. The
	// handle s stays in use: it stands for an instance that still runs during
	// a rolling deploy.
	Raise func(t *testing.T, s ControlStore)
	// Successor returns a store on the database of old for a binary of a
	// version at or above ControlMinimum. The store may close old, so the
	// caller does not use old after it.
	Successor func(t *testing.T, old ControlStore) ControlStore
}

// controlKind is the kind of the operations that the suites start.
const controlKind = durable.Kind("control-minimum")

// controlPath is one control write and the decision about it. A path with an
// empty openReason refuses a binary below the minimum. A path with a reason
// stays open, and the reason is the text of the decision.
type controlPath struct {
	prepare    func(t *testing.T, s ControlStore) func(ctx context.Context) error
	name       string
	openReason string
}

// controlBinary is one binary version and whether the minimum refuses it.
type controlBinary struct {
	name    string
	version string
	below   bool
}

// RunControlMinimum executes the suite of the control paths against stores
// from h. For each path, a binary below the minimum gets a *schema.MinimumError
// from a path that must refuse, and the result of the write from a path that
// stays open. A binary at the minimum and a dev build pass every path.
func RunControlMinimum(t *testing.T, h ControlMinimumHarness) {
	t.Helper()

	binaries := []controlBinary{
		{name: "below the minimum", version: "v0.4.9", below: true},
		{name: "at the minimum", version: ControlMinimum, below: false},
		{name: "dev build", version: "dev", below: false},
	}

	for _, path := range controlPaths() {
		t.Run(path.name, func(t *testing.T) {
			t.Parallel()

			for _, bin := range binaries {
				t.Run(bin.name, func(t *testing.T) {
					t.Parallel()

					s := h.Open(t, bin.version)
					attempt := path.prepare(t, s)

					h.Raise(t, s)

					err := attempt(t.Context())
					checkControlResult(t, path, bin, err)
				})
			}
		})
	}
}

// checkControlResult fails when err is not what the decision about path wants
// for bin.
func checkControlResult(t *testing.T, path controlPath, bin controlBinary, err error) {
	t.Helper()

	refused := bin.below && path.openReason == ""

	if !refused {
		if err != nil {
			t.Errorf("%s with %q = %v, want nil (%s)", path.name, bin.version, err, path.openReason)
		}

		return
	}

	below, ok := errors.AsType[*schema.MinimumError](err)
	if !ok || !errors.Is(err, schema.ErrBinaryBelowMinimum) {
		t.Fatalf("%s with %q = %v, want a MinimumError", path.name, bin.version, err)
	}

	if below.SchemaMinimum != ControlMinimum || below.BinaryVersion != bin.version {
		t.Errorf("%s: error = %+v, want a MinimumError that names %s and %q",
			path.name, below, ControlMinimum, bin.version)
	}
}

// controlPaths lists every control path with its decision.
func controlPaths() []controlPath {
	paths := instancePaths()

	paths = append(paths, maintenancePaths()...)
	paths = append(paths, backupPaths()...)
	paths = append(paths, durablePaths()...)
	paths = append(paths, schedulePaths()...)
	paths = append(paths, auditPaths()...)
	paths = append(paths, controlPath{
		name:       "sign-in failure count",
		openReason: "",
		prepare: func(_ *testing.T, s ControlStore) func(context.Context) error {
			return func(ctx context.Context) error {
				_, err := s.Failures.AddFailure(ctx, "192.0.2.1", time.Minute)

				return err
			}
		},
	})

	return paths
}

func controlInstance() installation.Instance {
	return installation.Instance{
		StartedAt:     time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC),
		ID:            "inst-control",
		BinaryVersion: "v0.4.9",
	}
}

func instancePaths() []controlPath {
	registered := func(t *testing.T, s ControlStore) {
		t.Helper()

		if err := s.Instances.RegisterInstance(t.Context(), controlInstance()); err != nil {
			t.Fatalf("register the instance before the minimum goes up: %v", err)
		}
	}

	return []controlPath{
		{
			name: "register instance", openReason: "",
			prepare: func(_ *testing.T, s ControlStore) func(context.Context) error {
				return func(ctx context.Context) error { return s.Instances.RegisterInstance(ctx, controlInstance()) }
			},
		},
		{
			name: "unregister instance",
			openReason: "an old binary deletes its own row when it stops, so the row does not claim a " +
				"live instance below the minimum",
			prepare: func(t *testing.T, s ControlStore) func(context.Context) error {
				t.Helper()

				registered(t, s)

				return func(ctx context.Context) error { return s.Instances.UnregisterInstance(ctx, controlInstance().ID) }
			},
		},
		{
			name: "delete instance",
			openReason: "an administrator deletes the row of an instance below the minimum, and the " +
				"contract migration waits for that delete",
			prepare: func(t *testing.T, s ControlStore) func(context.Context) error {
				t.Helper()

				registered(t, s)

				return func(ctx context.Context) error { return s.Instances.DeleteInstance(ctx, controlInstance().ID) }
			},
		},
	}
}

func maintenancePaths() []controlPath {
	return []controlPath{
		{
			name: "enter maintenance mode", openReason: "",
			prepare: func(_ *testing.T, s ControlStore) func(context.Context) error {
				return func(ctx context.Context) error {
					return s.Maintenance.SetState(ctx, maintenance.StateMaintenance)
				}
			},
		},
		{
			name: "enter maintenance mode when the state is normal", openReason: "",
			prepare: func(_ *testing.T, s ControlStore) func(context.Context) error {
				return func(ctx context.Context) error {
					return s.Maintenance.SetStateIf(ctx, maintenance.StateNormal, maintenance.StateMaintenance)
				}
			},
		},
	}
}

func controlBackup(t *testing.T) (backup.Record, durable.StartRequest) {
	t.Helper()

	id, err := backup.ParseID("bkp-control")
	if err != nil {
		t.Fatalf("parse the backup ID: %v", err)
	}

	rec := backup.Record{
		StartedAt: time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC),
		EndedAt:   time.Time{},
		ID:        id,
		Target:    "dir:/var/backups",
		Error:     "",
		State:     backup.StateRunning,
	}

	return rec, durable.StartRequest{
		Key: backup.OperationKey(id), Kind: backup.OperationKind, Version: backup.OperationVersion, Input: nil,
	}
}

func backupPaths() []controlPath {
	return []controlPath{
		{
			name: "start backup", openReason: "",
			prepare: func(t *testing.T, s ControlStore) func(context.Context) error {
				t.Helper()

				rec, start := controlBackup(t)

				return func(ctx context.Context) error { return s.Backups.StartBackup(ctx, rec, start) }
			},
		},
		{
			name: "finish backup", openReason: "",
			prepare: func(t *testing.T, s ControlStore) func(context.Context) error {
				t.Helper()

				rec, start := controlBackup(t)
				if err := s.Backups.StartBackup(t.Context(), rec, start); err != nil {
					t.Fatalf("start the backup before the minimum goes up: %v", err)
				}

				claim := claimControl(t, s, backup.OperationKind)

				return func(ctx context.Context) error {
					_, err := s.Backups.FinishBackup(
						ctx, claim.Step("finish"), rec.ID, backup.StateSucceeded, "", rec.StartedAt.Add(time.Minute),
					)

					return err
				}
			},
		},
	}
}

// startControl starts the operation of the suites.
func startControl(t *testing.T, s ControlStore) durable.Operation {
	t.Helper()

	req := durable.StartRequest{Key: "control-key", Kind: controlKind, Version: 0, Input: nil}

	op, _, err := s.Durable.Start(t.Context(), req)
	if err != nil {
		t.Fatalf("start the operation before the minimum goes up: %v", err)
	}

	return op
}

// claimControl claims the one operation of kind that is due.
func claimControl(t *testing.T, s ControlStore, kind durable.Kind) durable.Claim {
	t.Helper()

	op, err := s.Durable.Claim(t.Context(), durable.ClaimRequest{
		Owner: "worker-old", Kinds: []durable.Kind{kind}, MaxVersions: nil, Lease: time.Minute, TakeCanceled: false,
	})
	if err != nil {
		t.Fatalf("claim the operation before the minimum goes up: %v", err)
	}

	return durable.Claim{Operation: op.ID, Owner: op.Claim.Owner, Token: op.Claim.Token, ExpiresAt: op.Claim.ExpiresAt}
}

// claimedControl starts and claims an operation, and returns a path whose
// attempt is do.
func claimedControl(
	do func(ctx context.Context, s ControlStore, c durable.Claim) error,
) func(t *testing.T, s ControlStore) func(context.Context) error {
	return func(t *testing.T, s ControlStore) func(context.Context) error {
		t.Helper()

		startControl(t, s)
		claim := claimControl(t, s, controlKind)

		return func(ctx context.Context) error { return do(ctx, s, claim) }
	}
}

func durablePaths() []controlPath {
	return []controlPath{
		{
			name: "start durable operation", openReason: "",
			prepare: func(_ *testing.T, s ControlStore) func(context.Context) error {
				return func(ctx context.Context) error {
					_, _, err := s.Durable.Start(ctx, durable.StartRequest{
						Key: "control-key", Kind: controlKind, Version: 0, Input: nil,
					})

					return err
				}
			},
		},
		{
			name: "claim durable operation", openReason: "",
			prepare: func(t *testing.T, s ControlStore) func(context.Context) error {
				t.Helper()

				startControl(t, s)

				return func(ctx context.Context) error {
					_, err := s.Durable.Claim(ctx, durable.ClaimRequest{
						Owner: "worker-old", Kinds: []durable.Kind{controlKind}, MaxVersions: nil,
						Lease: time.Minute, TakeCanceled: false,
					})

					return err
				}
			},
		},
		{
			name: "extend durable claim", openReason: "",
			prepare: claimedControl(func(ctx context.Context, s ControlStore, c durable.Claim) error {
				_, err := s.Durable.Extend(ctx, c, time.Minute)

				return err
			}),
		},
		{
			name: "record durable step", openReason: "",
			prepare: claimedControl(func(ctx context.Context, s ControlStore, c durable.Claim) error {
				return s.Durable.RecordStep(ctx, c.Step("first"), nil)
			}),
		},
		{
			name: "complete durable operation", openReason: "",
			prepare: claimedControl(func(ctx context.Context, s ControlStore, c durable.Claim) error {
				return s.Durable.Complete(ctx, c)
			}),
		},
		{
			name: "fail durable operation", openReason: "",
			prepare: claimedControl(func(ctx context.Context, s ControlStore, c durable.Claim) error {
				return s.Durable.Fail(ctx, c, durable.Failure{Message: "control", Final: true, RetryAfter: 0})
			}),
		},
		{
			name: "release durable claim",
			openReason: "an old worker hands its claim back when it stops, so a new worker takes the " +
				"operation at once and not at the end of the lease",
			prepare: claimedControl(func(ctx context.Context, s ControlStore, c durable.Claim) error {
				return s.Durable.Release(ctx, c)
			}),
		},
		{
			name: "give back durable claim",
			openReason: "an old worker gives back an operation that needs a newer binary, so a new " +
				"worker takes it at once",
			prepare: claimedControl(func(ctx context.Context, s ControlStore, c durable.Claim) error {
				return s.Durable.GiveBack(ctx, c)
			}),
		},
		{
			name: "cancel durable operation", openReason: "",
			prepare: func(t *testing.T, s ControlStore) func(context.Context) error {
				t.Helper()

				op := startControl(t, s)

				return func(ctx context.Context) error { return s.Durable.Cancel(ctx, op.ID) }
			},
		},
		{
			name: "cancel durable operation before a step", openReason: "",
			prepare: func(t *testing.T, s ControlStore) func(context.Context) error {
				t.Helper()

				op := startControl(t, s)

				return func(ctx context.Context) error { return s.Durable.CancelBefore(ctx, op.ID, "first") }
			},
		},
		{
			name: "durable heartbeat", openReason: "",
			prepare: func(_ *testing.T, s ControlStore) func(context.Context) error {
				return func(ctx context.Context) error { return s.Durable.Beat(ctx, "worker-old", time.Minute) }
			},
		},
	}
}

func controlSchedule() durable.ScheduleSpec {
	return durable.ScheduleSpec{
		Name: "control-schedule", Kind: controlKind, Version: 0, Interval: time.Hour, Input: nil,
	}
}

func schedulePaths() []controlPath {
	return []controlPath{
		{
			name: "ensure schedule", openReason: "",
			prepare: func(_ *testing.T, s ControlStore) func(context.Context) error {
				return func(ctx context.Context) error {
					_, err := s.Durable.EnsureSchedule(ctx, controlSchedule())

					return err
				}
			},
		},
		{
			name: "claim schedule tick", openReason: "",
			prepare: func(t *testing.T, s ControlStore) func(context.Context) error {
				t.Helper()

				if _, err := s.Durable.EnsureSchedule(t.Context(), controlSchedule()); err != nil {
					t.Fatalf("ensure the schedule before the minimum goes up: %v", err)
				}

				return func(ctx context.Context) error {
					_, err := s.Durable.ClaimTick(ctx, controlSchedule())

					return err
				}
			},
		},
	}
}

func auditPaths() []controlPath {
	return []controlPath{
		{
			name: "append server audit entry",
			openReason: "an entry records what happened, for example a refusal of an old binary, and " +
				"dropping it would hide the refusal",
			prepare: func(_ *testing.T, s ControlStore) func(context.Context) error {
				return func(ctx context.Context) error {
					at := time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC)
					_, err := s.Audit.AppendServer(ctx, ServerChange(at, "principal:control"))

					return err
				}
			},
		},
		{
			name: "save audit chain record", openReason: "",
			prepare: func(_ *testing.T, s ControlStore) func(context.Context) error {
				return func(ctx context.Context) error {
					return s.Audit.SaveChainRecord(ctx, audit.ChainRecord{
						ChangedAt: time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC), ChangedBy: "admin",
						State: audit.ChainStateOn, Pending: false, EnteredMaintenance: false,
						MigratingAt: time.Time{}, LogReplaced: false,
					})
				}
			},
		},
		{
			name: "save audit chain check result", openReason: "",
			prepare: func(_ *testing.T, s ControlStore) func(context.Context) error {
				return func(ctx context.Context) error { return s.Audit.SaveCheckResult(ctx, 0) }
			},
		},
		{
			name: "acquire audit sink lease", openReason: "",
			prepare: func(_ *testing.T, s ControlStore) func(context.Context) error {
				return func(ctx context.Context) error {
					_, err := s.Audit.AcquireSinkLease(ctx, "control-sink", "worker-old", time.Minute)

					return err
				}
			},
		},
		{
			name: "release audit sink lease",
			openReason: "an old binary hands back the lease of a sink when it stops, so a new instance " +
				"ships the sink at once",
			prepare: func(t *testing.T, s ControlStore) func(context.Context) error {
				t.Helper()

				claim, err := s.Audit.AcquireSinkLease(t.Context(), "control-sink", "worker-old", time.Minute)
				if err != nil {
					t.Fatalf("acquire the sink lease before the minimum goes up: %v", err)
				}

				return func(ctx context.Context) error { return s.Audit.ReleaseSinkLease(ctx, claim) }
			},
		},
	}
}

// RunControlMinimumRunner executes the durable runner against stores from h.
// A runner of a binary below the minimum never claims the operation and never
// runs its steps, the operation stays pending, and a runner of a new binary
// takes it.
func RunControlMinimumRunner(t *testing.T, h ControlMinimumHarness) {
	t.Helper()

	var ran atomic.Int32

	spec := durable.KindSpec{
		Kind: controlKind, Version: 0, MinVersion: 0,
		Steps: []durable.Step{{Name: "work", Run: func(context.Context, durable.StepContext) ([]byte, error) {
			ran.Add(1)

			return nil, nil
		}}},
	}

	runnerOf := func(t *testing.T, s ControlStore, owner string) *durable.Runner {
		t.Helper()

		r, err := durable.NewRunner(durable.RunnerConfig{
			Store: s.Durable, Owner: owner, Kinds: []durable.KindSpec{spec}, Lease: time.Minute,
			RenewEvery: 0, PollEvery: 0, MaxAttempts: 0, ExpiryLimit: 0, Backoff: durable.Backoff{Base: 0, Max: 0},
			ClaimBackoff: durable.Backoff{Base: 0, Max: 0}, WriteTimeout: 0, Deferred: nil, StopOnRenewalError: false,
			Logger: nil, Rand: nil,
		})
		if err != nil {
			t.Fatalf("new runner: %v", err)
		}

		return r
	}

	old := h.Open(t, "v0.4.9")
	op := startControl(t, old)

	h.Raise(t, old)

	err := runnerOf(t, old, "worker-old").RunOnce(t.Context())
	if !errors.Is(err, schema.ErrBinaryBelowMinimum) {
		t.Fatalf("RunOnce of the old binary = %v, want a MinimumError", err)
	}

	if ran.Load() != 0 {
		t.Fatalf("steps run by the old binary = %d, want 0", ran.Load())
	}

	got, err := old.Durable.Get(t.Context(), op.ID)
	if err != nil {
		t.Fatalf("get the operation: %v", err)
	}

	if got.State != durable.StatePending || got.Claim.Owner != "" {
		t.Fatalf("operation after the refused claim = state %q, owner %q, want pending and no owner",
			got.State, got.Claim.Owner)
	}

	next := h.Successor(t, old)

	if err = runnerOf(t, next, "worker-new").RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce of the new binary = %v, want nil", err)
	}

	if ran.Load() != 1 {
		t.Errorf("steps run by the new binary = %d, want 1", ran.Load())
	}

	got, err = next.Durable.Get(t.Context(), op.ID)
	if err != nil {
		t.Fatalf("get the operation: %v", err)
	}

	if got.State != durable.StateSucceeded {
		t.Errorf("operation after the new binary = state %q, want %q", got.State, durable.StateSucceeded)
	}
}
