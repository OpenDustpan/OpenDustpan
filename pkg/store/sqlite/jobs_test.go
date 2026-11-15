package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/job/jobtest"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// jobDomain makes the rows that the job stores need, on the real
// namespaces, workspaces, manual locks and audit chain of one SQLite store.
type jobDomain struct {
	s *sqlite.Store
}

var _ jobtest.Domain = (*jobDomain)(nil)

func (d *jobDomain) CreateNamespace(ctx context.Context, name namespace.Name) (namespace.Fence, error) {
	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.UnixMilli(1_700_000_000_000).UTC(),
		Name:                name,
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := d.s.CreateNamespace(ctx, ns); err != nil {
		return namespace.Fence{}, fmt.Errorf("create namespace: %w", err)
	}

	return ns.Fence(), nil
}

func (d *jobDomain) CreateUnit(
	ctx context.Context, fence namespace.Fence, tool, name string,
) (identity.Object, error) {
	now := time.UnixMilli(1_700_000_000_000).UTC()

	switch tool {
	case "opentofu":
		ref, err := workspace.ParseName(name)
		if err != nil {
			return identity.Object{}, fmt.Errorf("workspace name: %w", err)
		}

		w := workspace.Workspace{
			NamespaceID: "",
			CreatedAt:   now,
			UpdatedAt:   now,
			Lock:        workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
			LastRelease: workspace.NoRelease(),
			ID:          workspace.ID(ident.NewID("ws-")),
			Ref:         workspace.Ref{Namespace: fence.Namespace, Name: ref},
			Revision:    1,
			Overrides:   namespace.Overrides{Forced: false, Unlocked: false},
			Origin:      identity.StateOrigin{Job: "", Commit: ""},
		}

		if err := d.s.CreateWorkspace(ctx, w, fence); err != nil {
			return identity.Object{}, fmt.Errorf("create workspace: %w", err)
		}
	case "pulumi":
		project, stackName, _ := strings.Cut(name, "/")
		st := stack.Stack{
			NamespaceID: "",
			CreatedAt:   now, UpdatedAt: now, Tags: map[string]string{},
			Ref: stack.Ref{
				Namespace: fence.Namespace, Project: stack.ProjectName(project), Stack: stack.Name(stackName),
			},
			ID: stack.ID(ident.NewID("stk-")), Version: 0,
		}

		if err := d.s.CreateStack(ctx, st, fence); err != nil {
			return identity.Object{}, fmt.Errorf("create stack: %w", err)
		}
	default:
		return identity.Object{}, fmt.Errorf("%w: tool %q", identity.ErrInvalidObject, tool)
	}

	entries, err := d.s.ListUnits(ctx, fence.Namespace)
	if err != nil {
		return identity.Object{}, fmt.Errorf("list units: %w", err)
	}

	for i := range entries {
		e := &entries[i]
		if e.Tool == tool && e.Name == name {
			o, err := identity.UnitObject(tool, fence.Namespace, name)
			if err != nil {
				return identity.Object{}, fmt.Errorf("unit object: %w", err)
			}

			return o.WithUnitID(e.UnitID), nil
		}
	}

	return identity.Object{}, fmt.Errorf("%w: unit %s not in the inventory", identity.ErrInvalidObject, name)
}

func (d *jobDomain) RenameUnit(
	ctx context.Context, fence namespace.Fence, unit identity.Object, newName string,
) (identity.Object, error) {
	ref, err := pulumiRef(fence.Namespace, unit)
	if err != nil {
		return identity.Object{}, err
	}

	_, stackName, _ := strings.Cut(newName, "/")

	entry := renameTestEntry(fence.Namespace, ref)
	if err = d.s.RenameStack(ctx, ref, stack.Name(stackName), fence, entry); err != nil {
		return identity.Object{}, fmt.Errorf("rename stack: %w", err)
	}

	o, err := identity.UnitObject("pulumi", fence.Namespace, newName)
	if err != nil {
		return identity.Object{}, fmt.Errorf("unit object: %w", err)
	}

	return o.WithUnitID(unit.UnitID()), nil
}

func (d *jobDomain) RemoveUnit(ctx context.Context, fence namespace.Fence, unit identity.Object) error {
	ref, err := pulumiRef(fence.Namespace, unit)
	if err != nil {
		return err
	}

	entry := lockEntry(fence.Namespace, manuallock.UnitTarget(collection.UnitRef{
		Namespace: fence.Namespace, Tool: "pulumi", Name: unit.UnitName(),
	}))
	entry.Action = "unit:delete"

	deletion := inventory.Deletion{Entry: entry, PurgeAfter: entry.At.Add(inventory.DeletedRetention)}
	if err := d.s.DeleteStack(ctx, ref, fence, stack.EmptyGuard(), deletion); err != nil {
		return fmt.Errorf("delete stack: %w", err)
	}

	return nil
}

// pulumiRef returns the stack reference of a Pulumi unit.
func pulumiRef(ns namespace.Name, unit identity.Object) (stack.Ref, error) {
	if unit.Tool() != "pulumi" {
		return stack.Ref{}, fmt.Errorf("%w: tool %q", identity.ErrInvalidObject, unit.Tool())
	}

	project, stackName, _ := strings.Cut(unit.UnitName(), "/")

	return stack.Ref{Namespace: ns, Project: stack.ProjectName(project), Stack: stack.Name(stackName)}, nil
}

func (d *jobDomain) BumpFence(ctx context.Context, fence namespace.Fence) (namespace.Fence, error) {
	if _, err := d.s.BumpFailoverVersions(ctx); err != nil {
		return namespace.Fence{}, fmt.Errorf("bump failover versions: %w", err)
	}

	ns, err := d.s.GetNamespace(ctx, fence.Namespace)
	if err != nil {
		return namespace.Fence{}, fmt.Errorf("get namespace: %w", err)
	}

	return ns.Fence(), nil
}

// writeStateHolder is the holder of the lock that WriteState takes.
const writeStateHolder = "state-writer"

// writeStatePendingCap bounds the pending state versions of WriteState.
const writeStatePendingCap = 10

// WriteState returns the jobtest.StateWriter of the store: it writes the
// state of unit through the workspace store, as an OpenTofu state write does,
// and returns the state version that the write made current. A final write
// releases the lock, as the end of an apply does.
func (d *jobDomain) WriteState() jobtest.StateWriter {
	return func(
		ctx context.Context, fence namespace.Fence, unit identity.Object, final bool,
	) (string, error) {
		if unit.Tool() != "opentofu" {
			return "", fmt.Errorf("%w: tool %q", identity.ErrInvalidObject, unit.Tool())
		}

		w, err := d.lockForStateWrite(ctx, fence, unit)
		if err != nil {
			return "", err
		}

		sv, err := d.declareStateWrite(ctx, fence, w)
		if err != nil {
			return "", err
		}

		if final {
			if releaseErr := d.releaseStateWrite(ctx, fence, w.ID); releaseErr != nil {
				return "", releaseErr
			}
		}

		return sv.ID.String(), nil
	}
}

func (d *jobDomain) PutManualLock(
	ctx context.Context, fence namespace.Fence, target manuallock.Target, holder, reason string,
) error {
	l := manuallock.Lock{Since: time.Now().UTC(), Target: target, Holder: holder, Reason: reason}

	return d.s.PutLock(ctx, l, fence, lockEntry(fence.Namespace, target))
}

func (d *jobDomain) RemoveManualLock(ctx context.Context, fence namespace.Fence, target manuallock.Target) error {
	_, err := d.s.ForceRemoveLock(ctx, target, fence, lockEntry(fence.Namespace, target))

	return err
}

func lockEntry(ns namespace.Name, target manuallock.Target) audit.Entry {
	return audit.Entry{
		At: time.Now().UTC(), Namespace: ns, Subject: "tester", IDPSubject: "", TokenID: "",
		Action: "unit:lock", Object: target.String(), Version: "", ValuePath: "", ValueCount: nil, Changes: nil,
		SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess,
		RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, Reason: "",
		PrincipalKind: "", JobID: "",
	}
}

// renameTestEntry returns a valid audit entry for a rename of ref in ns.
func renameTestEntry(ns namespace.Name, ref stack.Ref) audit.Entry {
	return audit.Entry{
		At: time.UnixMilli(1_700_000_300_000).UTC(), Namespace: ns, Subject: "tester", IDPSubject: "", TokenID: "",
		Action: "unit:manage", Object: "pulumi:" + ns.String() + "/" + ref.Project.String() + "/" + ref.Stack.String(),
		Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "",
		Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess,
		RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, Reason: "",
		PrincipalKind: "", JobID: "",
	}
}

// stepAppender is what the job store of both adapters adds to job.Store: the
// write of a step record, which checks the claim and which the recorder of a
// running phase uses.
type stepAppender interface {
	AppendStepRecord(ctx context.Context, pc job.PhaseClaim, r job.StepRecord) error
}

func (d *jobDomain) RecordStepBegin(ctx context.Context, pc job.PhaseClaim, r job.StepRecord) error {
	a, ok := d.s.Jobs().(stepAppender)
	if !ok {
		return fmt.Errorf("the job store has no step record write: %w", job.ErrUnsupported)
	}

	return a.AppendStepRecord(ctx, pc, r)
}

func (d *jobDomain) AuditEntries(ctx context.Context, ns namespace.Name) ([]audit.Entry, error) {
	var out []audit.Entry

	for page := 1; ; page++ {
		listing, err := d.s.List(ctx, ns, audit.Page{Number: page, Size: audit.MaxPageSize})
		if err != nil {
			return nil, fmt.Errorf("list audit: %w", err)
		}

		out = append(out, listing.Entries...)

		if len(listing.Entries) < audit.MaxPageSize {
			break
		}
	}

	slices.SortFunc(out, func(a, b audit.Entry) int { return int(a.Sequence - b.Sequence) })

	return out, nil
}

// DamageJobBody implements jobtest.Domain. It writes a body that no decoder
// reads, as a restore or another version of the server leaves one.
func (d *jobDomain) DamageJobBody(ctx context.Context, id job.ID) error {
	return d.s.ExecForTest(ctx, `UPDATE jobs SET body = '{' WHERE id = ?`, string(id))
}

// lockForStateWrite returns the workspace of unit, locked for a state write. A
// workspace that is not locked takes the lock in the same write.
func (d *jobDomain) lockForStateWrite(
	ctx context.Context, fence namespace.Fence, unit identity.Object,
) (workspace.Workspace, error) {
	w, err := d.s.GetWorkspaceByID(ctx, workspace.ID(unit.UnitID()))
	if err != nil {
		return workspace.Workspace{}, fmt.Errorf("get workspace: %w", err)
	}

	if w.Lock.Locked() {
		return w, nil
	}

	now := time.Now().UTC()

	w.Lock = workspace.Lock{LockedAt: now, ID: ident.NewID("lock-"), Holder: writeStateHolder, Reason: "state write"}
	w.Revision++
	w.UpdatedAt = now

	if saveErr := d.s.SaveWorkspace(ctx, w, fence); saveErr != nil {
		return workspace.Workspace{}, fmt.Errorf("lock workspace: %w", saveErr)
	}

	return w, nil
}

// declareStateWrite declares and finalizes a state version of the locked
// workspace w, under its lock.
func (d *jobDomain) declareStateWrite(
	ctx context.Context, fence namespace.Fence, w workspace.Workspace,
) (workspace.StateVersion, error) {
	lineage, serial := "lineage-1", int64(1)

	current, err := d.s.CurrentStateVersion(ctx, w.ID)
	switch {
	case err == nil:
		lineage, serial = current.Lineage, current.Serial+1
	case errors.Is(err, workspace.ErrNoState):
		// The unit has no state: the first write sets its own lineage and serial.
	default:
		return workspace.StateVersion{}, fmt.Errorf("read current state: %w", err)
	}

	tok, err := token.New()
	if err != nil {
		return workspace.StateVersion{}, fmt.Errorf("new upload capability: %w", err)
	}

	now := time.Now().UTC()

	sv := workspace.StateVersion{
		CreatedAt: now, FinalizedAt: time.Time{}, UploadExpiresAt: now.Add(workspace.UploadWindow),
		Upload: tok.Digest(), ID: workspace.StateVersionID(ident.NewID("sv-")), Workspace: w.ID,
		Holder: w.Lock.Holder, LockID: w.Lock.ID, Lineage: lineage, Status: workspace.StatusPending,
		Content: "", Outputs: nil, MD5: [16]byte{1, 2, 3}, Serial: serial, Size: 0, Force: false,
		UnlockedWrite: false, Sealed: true, SensitiveMarks: nil, RestoredFrom: "", FenceBy: "",
		Origin: identity.StateOrigin{Job: "", Commit: ""},
	}

	key := blob.Key("state-write/" + sv.ID.String())

	if err = d.s.CreateStateVersion(ctx, sv, fence, writeStatePendingCap); err != nil {
		return workspace.StateVersion{}, fmt.Errorf("declare state version: %w", err)
	}

	if _, err = d.s.RecordUploadAttempt(ctx, sv.ID, key, fence); err != nil {
		return workspace.StateVersion{}, fmt.Errorf("record upload attempt: %w", err)
	}

	sv.Content = key
	sv.FinalizedAt = now

	if err = d.s.FinalizeStateVersion(ctx, sv, fence); err != nil {
		return workspace.StateVersion{}, fmt.Errorf("finalize state version: %w", err)
	}

	return sv, nil
}

// releaseStateWrite releases the lock of the workspace id and records the
// release, which makes the current state version final.
func (d *jobDomain) releaseStateWrite(ctx context.Context, fence namespace.Fence, id workspace.ID) error {
	w, err := d.s.GetWorkspaceByID(ctx, id)
	if err != nil {
		return fmt.Errorf("get workspace to release: %w", err)
	}

	now := time.Now().UTC()
	lock := w.Lock

	w.Lock = workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""}
	w.LastRelease = workspace.Release{
		At: now, LockID: lock.ID, Holder: lock.Holder, By: writeStateHolder, Kind: workspace.ReleaseNormal,
	}
	w.Revision++
	w.UpdatedAt = now

	if saveErr := d.s.SaveWorkspace(ctx, w, fence); saveErr != nil {
		return fmt.Errorf("release workspace: %w", saveErr)
	}

	return nil
}

func TestJobConformance(t *testing.T) {
	t.Parallel()

	jobtest.Run(t, func(t *testing.T) jobtest.Harness {
		t.Helper()

		s := open(t)
		j := s.Jobs()

		ops := s.DurableOperationsPort()
		d := &jobDomain{s: s}

		return jobtest.Harness{
			Jobs: j, Durable: ops, Freezes: j, Claimer: job.NewClaimer(j, ops, s, jobtest.NoGates{}), Namespaces: s,
			Domain: d, WriteState: d.WriteState(),
		}
	})
}

func TestRecorderConformance(t *testing.T) {
	t.Parallel()

	jobtest.RunRecorder(t, func(t *testing.T) jobtest.RecorderHarness {
		t.Helper()

		return newRecorderHarness(t).h
	})
}

func TestArtifactSweepConformance(t *testing.T) {
	t.Parallel()

	jobtest.RunArtifactSweep(t, func(t *testing.T) jobtest.ArtifactSweepHarness {
		t.Helper()

		r := newRecorderHarness(t)

		return jobtest.ArtifactSweepHarness{RecorderHarness: r.h, Names: r.names, Clock: r.store, Audit: r.store}
	})
}

// recorderStores are the recorder harness over a fresh store and the job store
// of that store, which serves the artifact names.
type recorderStores struct {
	h     jobtest.RecorderHarness
	names job.ArtifactKeyStore
	store *sqlite.Store
}

// newRecorderHarness returns the recorder harness over a fresh store, with
// the job store that serves the artifact names.
func newRecorderHarness(t *testing.T) recorderStores {
	t.Helper()

	return newRecorderHarnessOf(t, jobtest.NewRecorderHarness)
}

// newRecorderHarnessOf is newRecorderHarness with the function that builds
// the harness over the stores, so a test can set the retention periods.
func newRecorderHarnessOf(
	t *testing.T, build func(*testing.T, jobtest.RecorderParts) jobtest.RecorderHarness,
) recorderStores {
	t.Helper()

	s := open(t)
	j := s.Jobs()
	ops := s.DurableOperationsPort()
	d := &jobDomain{s: s}

	blobs, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() {
		if err := blobs.Close(); err != nil {
			t.Errorf("close blob store: %v", err)
		}
	})

	h := build(t, jobtest.RecorderParts{
		Harness: jobtest.Harness{
			Jobs: j, Durable: ops, Freezes: j, Claimer: job.NewClaimer(j, ops, s, jobtest.NoGates{}), Namespaces: s,
			Domain: d, WriteState: d.WriteState(),
		},
		Records:  j,
		ItemKeys: s,
		Blobs:    blobs,
		Wrapper:  nil,
	})

	return recorderStores{h: h, names: j, store: s}
}

func TestJobBackupKeepsTheDefaultedValues(t *testing.T) {
	t.Parallel()

	storetest.RunJobBackup(t, func(t *testing.T) storetest.JobBackup {
		t.Helper()

		s, _ := openDrift(t)

		return storetest.JobBackup{Store: s, Jobs: s.Jobs(), Domain: &jobDomain{s: s}, Namespaces: s}
	})
}

func TestJobRestoreIsReconciled(t *testing.T) {
	t.Parallel()

	storetest.RunJobRestoreReconcile(t, func(t *testing.T) (storetest.JobBackup, storetest.JobBackup) {
		t.Helper()

		src, _ := openDrift(t)
		dst, _ := openDrift(t)

		return storetest.JobBackup{Store: src, Jobs: src.Jobs(), Domain: &jobDomain{s: src}, Namespaces: src},
			storetest.JobBackup{Store: dst, Jobs: dst.Jobs(), Domain: &jobDomain{s: dst}, Namespaces: dst}
	})
}

// TestRecorderAppliesTheConfiguredLogRetention proves that the expiry of a
// log version is its creation time plus the job log period that the recorder
// got: two days here, not the default.
func TestRecorderAppliesTheConfiguredLogRetention(t *testing.T) {
	t.Parallel()

	const period = 48 * time.Hour

	rs := newRecorderHarnessOf(t, func(t *testing.T, parts jobtest.RecorderParts) jobtest.RecorderHarness {
		t.Helper()

		return jobtest.NewRecorderHarnessWithRetention(
			t, parts, history.RetentionSettings{Plan: history.DefaultRetention, JobLog: period},
		)
	})

	times := jobtest.LogExpiry(t, rs.h)
	if got := times.ExpiresAt.Sub(times.CreatedAt); got != period {
		t.Fatalf("log expiry - creation = %s, want %s", got, period)
	}
}
